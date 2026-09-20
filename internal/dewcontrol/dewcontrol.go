// Package dewcontrol drives the Pro panel's dew heater automatically from a heating
// curve based on the delta between ambient temperature and dew point, instead of
// requiring manual 0-100% control every time conditions change.
//
// Data source priority: an Alpaca ObservingConditions client (internal/obsconditions)
// is preferred when configured; Open-Meteo (internal/weather) is the fallback,
// attempted only when ObservingConditions is unconfigured or its fetch fails. If
// neither source is usable this tick, the tick is simply skipped — the heater stays at
// whatever it was last set to (by this loop or manually); this is enough of a "freeze"
// behavior on its own, no special-case code needed.
//
// Manual heater changes (Switch/Action/custom endpoint/dashboard slider) made while
// auto mode is enabled are intentionally left alone here: they apply immediately via
// the normal serial.SetHeaterPower path, and are simply transient — this loop's own
// next tick recomputes and overwrites them again, the same way it would overwrite its
// own previous tick's value. No "disable auto on manual touch" logic exists by design.
package dewcontrol

import (
	"errors"
	"math"
	"sync"
	"time"

	"geminiflatpanel/internal/config"
	"geminiflatpanel/internal/dewheaterswitch"
	"geminiflatpanel/internal/events"
	"geminiflatpanel/internal/logger"
	"geminiflatpanel/internal/obsconditions"
	"geminiflatpanel/internal/serial"
	"geminiflatpanel/internal/weather"
)

var recomputeTrigger = make(chan struct{}, 1)

// lastResult records the data source, temperature, and dew point of the most recent
// successful tick, for display in the web dashboard. Zero/empty until the first
// successful tick, or if auto mode has never engaged.
var (
	lastResultMu sync.RWMutex
	lastResult   struct {
		source      string
		temperature float64
		dewPoint    float64
		valid       bool
	}
)

// LastSource returns the data source used by the most recent successful tick, or ""
// if none has succeeded yet (or auto mode has never been enabled).
func LastSource() string {
	lastResultMu.RLock()
	defer lastResultMu.RUnlock()
	return lastResult.source
}

// LastReading returns the temperature and dew point from the most recent successful
// tick. ok is false if no tick has succeeded yet (or auto mode has never been
// enabled), in which case temperature/dewPoint are meaningless.
func LastReading() (temperature, dewPoint float64, ok bool) {
	lastResultMu.RLock()
	defer lastResultMu.RUnlock()
	return lastResult.temperature, lastResult.dewPoint, lastResult.valid
}

func setLastResult(source string, r reading) {
	lastResultMu.Lock()
	lastResult.source = source
	lastResult.temperature = r.Temperature
	lastResult.dewPoint = r.DewPoint
	lastResult.valid = true
	lastResultMu.Unlock()
}

// lastExternal tracks the external backend's last commanded value. percent is only
// meaningful for a rheostat channel, on only for a boolean channel -- but both are
// always kept in sync (setExternalHeater/tickExternal set both together) so either
// getter reflects the truth regardless of which channel shape is actually configured.
// Tracking on is also functionally required, not just cosmetic: computeHeaterState's
// hysteresis decision depends on the switch's *current* state, unlike the stateless
// PWM ramp. Defaults to 0/false, a safe assumption before the first tick ever runs.
var (
	lastExternalMu sync.RWMutex
	lastExternal   struct {
		percent int
		on      bool
	}
)

func lastExternalOn() bool {
	lastExternalMu.RLock()
	defer lastExternalMu.RUnlock()
	return lastExternal.on
}

func setLastExternalOn(on bool) {
	lastExternalMu.Lock()
	lastExternal.on = on
	if on {
		lastExternal.percent = 100
	} else {
		lastExternal.percent = 0
	}
	lastExternalMu.Unlock()
}

func setLastExternalPercent(percent int) {
	lastExternalMu.Lock()
	lastExternal.percent = percent
	lastExternal.on = percent > 0
	lastExternalMu.Unlock()
}

// LastExternalOn returns the external backend's last commanded on/off state, for
// display in the web dashboard.
func LastExternalOn() bool {
	return lastExternalOn()
}

// LastExternalPercent returns the external backend's last commanded rheostat value
// (0-100), for display in the web dashboard.
func LastExternalPercent() int {
	lastExternalMu.RLock()
	defer lastExternalMu.RUnlock()
	return lastExternal.percent
}

// TriggerRecompute requests an immediate tick instead of waiting for the next
// interval — called after a settings save that changes anything dew-control-related,
// same non-blocking idiom as serial.TriggerImmediateReconnect().
func TriggerRecompute() {
	select {
	case recomputeTrigger <- struct{}{}:
	default:
	}
}

// Start launches the background control loop. Call once at startup.
func Start() {
	go run()
	go listenForCoverStateChanges()
}

// SetManualHeaterPower applies an immediate manual power change, dispatching on the
// configured backend the same way tick() does. Manual changes are always transient
// under Auto mode by design (see the package doc comment) -- this does not disable or
// interact with the auto loop, it just applies once, the same as tick() itself would.
// Returns an error if there's nothing to set: backend "none", or an external boolean
// (on/off) channel, which has no single "power" value a slider could represent.
func SetManualHeaterPower(percent int) error {
	cfg := config.Get()
	switch cfg.DewHeaterBackend {
	case "built-in":
		return serial.SetHeaterPower(percent)
	case "external":
		if !cfg.DewHeaterSwitchIsRheostat {
			return errors.New("manual power control is not available for a boolean on/off switch")
		}
		if cfg.DewHeaterSwitchURL == "" {
			return errors.New("no external switch configured")
		}
		if err := dewheaterswitch.SetValue(cfg.DewHeaterSwitchURL, cfg.DewHeaterSwitchDeviceNumber, cfg.DewHeaterSwitchID, float64(percent)); err != nil {
			return err
		}
		setLastExternalPercent(percent)
		return nil
	default:
		return errors.New("dew heater backend is not enabled")
	}
}

// listenForCoverStateChanges requests an immediate recompute on every cover-state
// transition (open/close/jog/etc.), so DewControlOnlyWhenOpen reacts right away instead
// of waiting up to dewControlIntervalMinutes for the next scheduled tick. Safe to listen
// unconditionally -- tick() already no-ops harmlessly whenever auto mode or the gate
// itself isn't relevant.
func listenForCoverStateChanges() {
	for range events.CoverStateChangedChan {
		TriggerRecompute()
	}
}

func run() {
	logger.Info("DewControl: background loop started.")
	for {
		// Tick first, then wait -- not the other way around. If auto mode was
		// already enabled in the config loaded at startup, this makes it engage
		// immediately instead of silently doing nothing until either the full
		// interval elapses (5 minutes by default) or a settings save happens to
		// call TriggerRecompute(). Matches the "act, then sleep" shape
		// internal/serial/serial.go's periodicPoller already uses for the same
		// reason. tick() itself already no-ops harmlessly if disabled,
		// disconnected, or the panel has no heater, so calling it immediately
		// here is always safe.
		tick()

		cfg := config.Get()
		interval := cfg.DewControlIntervalMinutes
		if interval < 1 {
			interval = 5
		}

		select {
		case <-recomputeTrigger:
		case <-time.After(time.Duration(interval) * time.Minute):
		}
	}
}

func tick() {
	cfg := config.Get()
	if !cfg.EnableAutoDewControl {
		return
	}

	switch cfg.DewHeaterBackend {
	case "built-in":
		tickBuiltIn(*cfg)
	case "external":
		tickExternal(*cfg)
	}
	// "none" (or any legacy/invalid value, already normalized to "none" by config
	// validation): nothing to drive.
}

// tickBuiltIn drives the Pro panel's own heater output over serial. Unchanged from
// this loop's original, panel-only behavior.
func tickBuiltIn(cfg config.ProxyConfig) {
	if !serial.IsConnected() {
		return
	}
	if !serial.SupportsHeater() {
		logger.Debug("DewControl: connected panel has no heater output, skipping tick.")
		return
	}

	if coverGateBlocksHeat(cfg.DewControlOnlyWhenOpen, serial.GetCoverState()) {
		logger.Info("DewControl: cover not open and dewControlOnlyWhenOpen is on, forcing heater to 0%%.")
		if err := serial.SetHeaterPower(0); err != nil {
			logger.Warn("DewControl: failed to set heater power: %v", err)
		}
		return
	}

	reading, source, err := getReading(cfg)
	if err != nil {
		if cfg.DewControlFailsafeEnabled {
			logger.Warn("DewControl: no weather data available this tick (%v) -- failsafe forcing heater to %d%%.", err, cfg.DewControlFailsafePercent)
			if err := serial.SetHeaterPower(cfg.DewControlFailsafePercent); err != nil {
				logger.Warn("DewControl: failed to set heater power: %v", err)
			}
		} else {
			logger.Warn("DewControl: no weather data available this tick (%v) -- leaving heater at its current value.", err)
		}
		return
	}

	percent := computeHeaterPercent(reading.Temperature, reading.DewPoint, cfg.DewControlDeltaFullPower, cfg.DewControlDeltaZeroPower)
	logger.Info("DewControl: %s reading temp=%.1f°C dewpoint=%.1f°C delta=%.1f°C -> heater %d%%",
		source, reading.Temperature, reading.DewPoint, reading.Temperature-reading.DewPoint, percent)
	setLastResult(source, reading)

	if err := serial.SetHeaterPower(percent); err != nil {
		logger.Warn("DewControl: failed to set heater power: %v", err)
	}
}

// tickExternal drives a channel on a separate Alpaca Switch device instead of the
// panel's own heater output -- independent of serial.IsConnected()/SupportsHeater(),
// since the whole point is supporting panels (Rev2/Lite) that have no heater output at
// all. The cover-position gate still applies where a real cover exists (Rev2 panels
// have one even without a heater); serial.GetCoverState() is safe to call even with no
// panel connected at all, returning its default "unknown" state in that case, which
// coverGateBlocksHeat treats as blocking -- the same conservative default the built-in
// path gets implicitly by never reaching this check while disconnected.
func tickExternal(cfg config.ProxyConfig) {
	if cfg.DewHeaterSwitchURL == "" {
		logger.Debug("DewControl: external heater backend selected but no switch configured, skipping tick.")
		return
	}

	if coverGateBlocksHeat(cfg.DewControlOnlyWhenOpen, serial.GetCoverState()) {
		logger.Info("DewControl: cover not open and dewControlOnlyWhenOpen is on, forcing external heater off.")
		setExternalHeater(cfg, 0)
		return
	}

	reading, source, err := getReading(cfg)
	if err != nil {
		if cfg.DewControlFailsafeEnabled {
			logger.Warn("DewControl: no weather data available this tick (%v) -- failsafe forcing external heater.", err)
			setExternalHeater(cfg, cfg.DewControlFailsafePercent)
		} else {
			logger.Warn("DewControl: no weather data available this tick (%v) -- leaving external heater at its current value.", err)
		}
		return
	}

	if cfg.DewHeaterSwitchIsRheostat {
		percent := computeHeaterPercent(reading.Temperature, reading.DewPoint, cfg.DewControlDeltaFullPower, cfg.DewControlDeltaZeroPower)
		logger.Info("DewControl (external): %s reading temp=%.1f°C dewpoint=%.1f°C delta=%.1f°C -> switch %d%%",
			source, reading.Temperature, reading.DewPoint, reading.Temperature-reading.DewPoint, percent)
		setLastResult(source, reading)
		setExternalHeater(cfg, percent)
		return
	}

	on := computeHeaterState(reading.Temperature, reading.DewPoint, cfg.DewControlOnOffTargetDelta, cfg.DewControlOnOffHysteresis, lastExternalOn())
	logger.Info("DewControl (external): %s reading temp=%.1f°C dewpoint=%.1f°C delta=%.1f°C -> switch %s",
		source, reading.Temperature, reading.DewPoint, reading.Temperature-reading.DewPoint, onOffLabel(on))
	setLastResult(source, reading)
	if err := dewheaterswitch.SetState(cfg.DewHeaterSwitchURL, cfg.DewHeaterSwitchDeviceNumber, cfg.DewHeaterSwitchID, on); err != nil {
		logger.Warn("DewControl: failed to set external switch state: %v", err)
	}
	setLastExternalOn(on)
}

// setExternalHeater drives a rheostat channel to percent (0-100), or a boolean
// channel to on/off (percent > 0 -- reusing DewControlFailsafePercent's existing 0-100
// range for the on/off case too, rather than adding a second failsafe field just for
// it). Used for both the cover-gate-closed and no-data-failsafe cases, which both
// reduce to "force the heater to a specific value" regardless of channel shape.
func setExternalHeater(cfg config.ProxyConfig, percent int) {
	if cfg.DewHeaterSwitchIsRheostat {
		if err := dewheaterswitch.SetValue(cfg.DewHeaterSwitchURL, cfg.DewHeaterSwitchDeviceNumber, cfg.DewHeaterSwitchID, float64(percent)); err != nil {
			logger.Warn("DewControl: failed to set external switch value: %v", err)
		}
		setLastExternalPercent(percent)
		return
	}
	on := percent > 0
	if err := dewheaterswitch.SetState(cfg.DewHeaterSwitchURL, cfg.DewHeaterSwitchDeviceNumber, cfg.DewHeaterSwitchID, on); err != nil {
		logger.Warn("DewControl: failed to set external switch state: %v", err)
	}
	setLastExternalOn(on)
}

func onOffLabel(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

// reading is the two values this package actually needs, independent of which source
// they came from.
type reading struct {
	Temperature float64
	DewPoint    float64
}

// getReading tries ObservingConditions first (if configured), then falls back to
// Open-Meteo (if enabled). The returned string names which source succeeded, for
// logging/status purposes.
func getReading(cfg config.ProxyConfig) (reading, string, error) {
	if cfg.ObservingConditionsURL != "" {
		r, err := obsconditions.FetchReading(cfg.ObservingConditionsURL, cfg.ObservingConditionsDeviceNumber)
		if err == nil {
			return reading{Temperature: r.Temperature, DewPoint: r.DewPoint}, "ObservingConditions", nil
		}
		logger.Warn("DewControl: ObservingConditions client failed (%v), falling back to Open-Meteo.", err)
	}

	if cfg.EnableOpenMeteo {
		r, err := weather.Fetch(cfg.WeatherLatitude, cfg.WeatherLongitude)
		if err == nil {
			return reading{Temperature: r.Temperature, DewPoint: r.DewPoint}, "Open-Meteo", nil
		}
		return reading{}, "", err
	}

	return reading{}, "", errors.New("no data source configured (neither ObservingConditions URL is set nor Open-Meteo is enabled)")
}

// coverGateBlocksHeat reports whether DewControlOnlyWhenOpen should force the heater to
// 0% this tick: on, and the cover isn't confirmed Open (ASCOM CoverState 3) -- Moving,
// Closed, and Unknown all block heating, not just Closed. A closed panel already
// shields the optics from dew, so heating only matters once it's actually open.
func coverGateBlocksHeat(onlyWhenOpen bool, coverState int) bool {
	return onlyWhenOpen && coverState != 3
}

// computeHeaterPercent implements the linear ramp: 100% at delta <= deltaFullPower,
// 0% at delta >= deltaZeroPower, linearly interpolated in between and rounded to the
// nearest 10 to match the heater's own SwitchStep granularity (internal/alpaca/switchdevice.go).
// A malformed threshold pair (deltaFullPower >= deltaZeroPower) falls back to a plain
// on/off switch at deltaFullPower rather than producing a nonsensical curve.
func computeHeaterPercent(temperature, dewPoint, deltaFullPower, deltaZeroPower float64) int {
	delta := temperature - dewPoint

	if deltaFullPower >= deltaZeroPower {
		if delta <= deltaFullPower {
			return 100
		}
		return 0
	}

	if delta <= deltaFullPower {
		return 100
	}
	if delta >= deltaZeroPower {
		return 0
	}

	fraction := (deltaZeroPower - delta) / (deltaZeroPower - deltaFullPower)
	percent := fraction * 100
	return int(math.Round(percent/10) * 10)
}

// computeHeaterState implements the external on/off backend's bang-bang control with
// hysteresis, for a boolean (non-rheostat) switch channel: turns on once delta drops
// to/below targetDelta-hysteresis, turns off once delta rises to/above
// targetDelta+hysteresis, and holds currentlyOn in between. A single threshold
// (hysteresis == 0) would chatter a relay near the boundary on realistically noisy
// temperature readings; this mirrors an ordinary thermostat's deadband. Ties (delta
// exactly at the on-threshold) favor turning on, matching computeHeaterPercent's own
// "exactly at full-power threshold -> 100%" convention. config validation already
// clamps hysteresis to >= 0, so onThreshold <= targetDelta <= offThreshold always
// holds -- no malformed-input fallback needed here, unlike computeHeaterPercent's
// independently-configurable pair.
func computeHeaterState(temperature, dewPoint, targetDelta, hysteresis float64, currentlyOn bool) bool {
	delta := temperature - dewPoint
	onThreshold := targetDelta - hysteresis
	offThreshold := targetDelta + hysteresis

	if delta <= onThreshold {
		return true
	}
	if delta >= offThreshold {
		return false
	}
	return currentlyOn
}
