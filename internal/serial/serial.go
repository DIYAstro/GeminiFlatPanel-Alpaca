package serial

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"geminiflatpanel/internal/config"
	"geminiflatpanel/internal/events"
	"geminiflatpanel/internal/logger"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// Command defines a command to be sent to the serial device.
type Command struct {
	Command  string
	Response chan<- string
	Error    chan<- error
	Timeout  time.Duration
}

var (
	highPriorityCommands = make(chan Command)
	lowPriorityCommands  = make(chan Command)
	panelPort            serial.Port
	portMutex            = &sync.Mutex{}
	firmwareVersion      = "unknown"
	firmwareVersionMu    sync.RWMutex

	// activeProtocol is the identified Protocol for the current connection. It is set
	// by reconnect() on every successful connect/identify and guarded by stateMutex
	// (like the rest of the connection-derived state below), even though the physical
	// port itself is guarded separately by portMutex.
	activeProtocol Protocol

	lastSentStatus  events.ComPortStatus = events.Disconnected
	reconnectPaused                      = false

	// State variables
	currentBrightness     int
	lastNonZeroBrightness int
	highMode              bool
	coverState            int = 4 // 0 moving, 1 closed, 2 open, 3 timed out, 4 unknown
	// coverStateUnreliable is set by HaltCover the moment a halt is requested, and
	// cleared at the start of the next fresh OpenCover/CloseCover/auto-calibrate
	// attempt. While true, an interrupted move's own reply can look exactly like a
	// genuine "reached target" confirmation (same *O<adc>#/*C<adc>#) even though the
	// motor was stopped early — confirmed live: after a halted Close, >S# kept
	// reporting "closed" long after the fact while >G# showed the real position still
	// ~90% toward open. So both parseStatusString and moveCoverBlocking/moveCoverAsync
	// refuse to trust an ordinary status value or a same-shaped confirmation reply
	// while this is set, instead of assuming the very next poll or reply resolves it.
	coverStateUnreliable bool
	lightStatus          int // 0 off, 1 on
	currentMotorAngle    int // Ca from *M/*E/*F responses = current motor position
	openMotorAngle       int // Oa from *M/*E/*F responses = calibrated open position
	deviceReadyState     int // 0 = unknown, 1 = not ready (not calibrated), 2 = ready (calibrated)
	calibratorOnState    bool
	// heaterPercent is tracked locally, best-effort only — there is no known query
	// command to read the heater's actual state back from the device (unlike
	// currentBrightness, which >J# can at least confirm). See SetHeaterPower.
	heaterPercent              int
	stateMutex                 sync.RWMutex
	openCloseInProgress        bool
	brightnessChangeInProgress bool

	serialConnected   bool
	serialConnectedMu sync.RWMutex

	failedReconnectCount int
	reconnectTrigger     = make(chan struct{}, 1)

	// haltRequested signals ProcessCommands' motion-command read loop to inject >K#
	// onto the wire while it's still blocked waiting on an in-progress >O#/>C#'s own
	// reply -- see HaltCover's doc comment for why a normally-queued >K# can't do this
	// on its own (the single-worker queue wouldn't even try to send it until the
	// in-progress command's own wait already ended).
	haltRequested = make(chan struct{}, 1)
)

// Connect-on-demand tuning (config.SerialConnectOnDemand) -- see ConnectOnDemand and
// ScheduleIdleRelease, and internal/alpaca's HandleConnected, their only caller today.
// The disconnect-side release timeout is user-configurable
// (config.IdleReleaseTimeoutSeconds, read at ScheduleIdleRelease's call site) rather than
// a constant here, unlike this one -- see ConnectOnDemandTimeout's own comment for why.
const (
	// ConnectOnDemandTimeout is how long ConnectOnDemand actively waits for a
	// connection before giving up: comfortably above the worst-case natural handshake
	// (up to probePerAttemptTimeout per known protocol reconnect() tries, plus its
	// own settle delay).
	ConnectOnDemandTimeout = 15 * time.Second
)

func TriggerImmediateReconnect() {
	select {
	case reconnectTrigger <- struct{}{}:
	default:
	}
}

// StartManager initializes all background tasks for serial communication. Returns
// immediately -- the initial connection attempt itself runs in the background (see
// below), it does not block the caller.
func StartManager() {
	initDone := make(chan struct{})

	go ProcessCommands()
	go ManageConnection(initDone)
	go periodicPoller(initDone)

	// The initial connection attempt itself also runs in its own goroutine rather
	// than blocking StartManager's return: startApp() calls StartManager() before
	// starting the web server, and a slow-to-boot or absent panel must never delay
	// the dashboard/Alpaca API from becoming reachable. Confirmed live: with this
	// previously synchronous, the web UI was unreachable for as long as the attempt
	// took -- including, worst of all, while troubleshooting a connection problem,
	// since Settings wasn't reachable either to change the port configuration in the
	// meantime.
	go func() {
		conf := config.Get()

		// Connect-on-demand mode: stay fully dormant, not even one attempt at startup
		// -- the port is only ever touched once an Alpaca client or the dashboard's
		// Connect button actually asks for it (see ConnectOnDemand). ManageConnection's
		// loop already checks reconnectPaused on every cycle, so setting it here before
		// that loop's first iteration is enough; nothing else to skip.
		if conf.SerialConnectOnDemand {
			portMutex.Lock()
			reconnectPaused = true
			portMutex.Unlock()
			logger.Info("Connect-on-demand is enabled: not attempting a connection until an Alpaca client or the dashboard's Connect button asks for one.")
			close(initDone)
			return
		}

		if conf.SerialPortName != "" {
			logger.Info("Performing initial device connection attempt...")
			portMutex.Lock()
			reconnect(conf.SerialPortName)
			portMutex.Unlock()
		} else {
			logger.Info("No serial port configured yet -- skipping the initial connection attempt until one is set in Settings.")
		}

		portMutex.Lock()
		if panelPort != nil {
			logger.Info("Initial connection attempt finished successfully.")
		} else {
			logger.Warn("Initial connection attempt failed. Will retry in background.")
		}
		portMutex.Unlock()

		close(initDone)
	}()
}

// IsConnected returns the current connection status of the serial port.
func IsConnected() bool {
	serialConnectedMu.RLock()
	defer serialConnectedMu.RUnlock()
	return serialConnected
}

// getActiveProtocol returns the identified protocol for the current connection, or a
// Rev2 default if none has been identified yet — this matches this project's original,
// Rev2-only behavior, so any code path reached before the first successful connect
// keeps working exactly as before.
//
// Callers that already hold stateMutex must read the activeProtocol package var
// directly instead (sync.RWMutex is not reentrant) — see SetBrightness/parseStatusString.
func getActiveProtocol() Protocol {
	stateMutex.RLock()
	defer stateMutex.RUnlock()
	if activeProtocol != nil {
		return activeProtocol
	}
	return rev2Protocol{}
}

// SupportsCover reports whether the connected panel has a motorized cover at all.
func SupportsCover() bool { return getActiveProtocol().SupportsCover() }

// SupportsHeater reports whether the connected panel accepts the dew-heater power
// command (Pro only, and not yet physically confirmed — see protocol_pro.go).
func SupportsHeater() bool { return getActiveProtocol().SupportsHeater() }

// SupportsAutoCalibrate reports whether the connected panel accepts the automatic
// closed/open-position learning commands (Pro only, experimental — see
// protocol_pro.go).
func SupportsAutoCalibrate() bool { return getActiveProtocol().SupportsAutoCalibrate() }

// SupportsHalt reports whether the connected panel accepts a real "stop the motor now"
// command (>K#, Pro only, confirmed live — see HaltCover and protocol_pro.go).
func SupportsHalt() bool { return getActiveProtocol().SupportsHalt() }

// EffectiveMaxBrightness returns the actually reachable brightness ceiling for the
// connected panel: configuredMax, unless the panel doesn't support low/high-bank
// switching (none of the currently known revisions -- a defensive rule for one that
// doesn't), in which case its single-bank ceiling of 254 applies regardless of what's
// configured. Shared by
// SetBrightness and the Alpaca MaxBrightness endpoint (covercalibrator.go's
// effectiveMaxBrightness) so they can't silently disagree. Pure/stateless
// on purpose: SetBrightness already holds stateMutex when it calls this, so bankCapable
// is passed in rather than resolved here via SupportsBrightnessMode() — same reentrancy
// reasoning as ShouldBlockLightForOpenCover above.
func EffectiveMaxBrightness(configuredMax int, bankCapable bool) int {
	if !bankCapable && configuredMax > 254 {
		return 254
	}
	return configuredMax
}

// brightnessFallbackGuess computes the best-effort brightness value to assume when the
// physical light is reported on but currentBrightness's cache was reset (e.g. a
// reconnect while the panel was already lit) — see parseStatusString. Prefers
// lastNonZero (whatever this session last knew it was set to), else 128, clamped to
// the panel's actual reachable ceiling via EffectiveMaxBrightness.
// Previously hardcoded to 255, which exceeds a single bank's real 0-254 range, handing
// ASCOM clients a value above the same request's own MaxBrightness. Pure/stateless, same
// reasoning as the other Should*/Effective* helpers in this file.
func brightnessFallbackGuess(lastNonZero int, bankCapable bool) int {
	guess := lastNonZero
	if guess <= 0 {
		guess = 128
	}
	return EffectiveMaxBrightness(guess, bankCapable)
}

// setCoverStateLocked updates coverState and, only when the value actually changes,
// signals events.CoverStateChangedChan -- e.g. so dewcontrol's DewControlOnlyWhenOpen
// gate can react immediately instead of waiting for its own next scheduled tick.
// Caller must already hold stateMutex (write lock) -- every current call site does.
func setCoverStateLocked(newState int) {
	if coverState == newState {
		return
	}
	coverState = newState
	if newState == 0 { // Moving
		// A long blocking (or async fallback-polling) command is about to occupy the
		// single serialized command worker for the whole physical move -- reacting now
		// would just lose that race and fail with "command queue busy" (found live:
		// DewControlOnlyWhenOpen tried to zero the heater the instant a move started,
		// timing out ~3.5s later since the move itself was still running). The
		// transition into the settled state once the move finishes fires this same
		// signal, when the worker is actually free again.
		return
	}
	select {
	case events.CoverStateChangedChan <- struct{}{}:
	default:
	}
}

// ascomCoverStateFromRaw maps the raw coverState device value (0=moving, 1=closed,
// 2=open, 3=timed out, 4=unknown) to the ASCOM CoverState standard (0=NotPresent,
// 1=Closed, 2=Moving, 3=Open, 4=Unknown/Error). Shared by GetCoverState() and
// GetLiveStatus() so the Alpaca endpoint and the web dashboard's live status can't
// disagree about the same physical condition. Pure/stateless: both callers
// already hold stateMutex, so hasCover/raw are passed in rather than resolved here.
func ascomCoverStateFromRaw(hasCover bool, raw int) int {
	if !hasCover {
		return 0 // NotPresent
	}
	switch raw {
	case 1:
		return 1 // Closed
	case 2:
		return 3 // Open
	case 0:
		return 2 // Moving
	default:
		return 4 // Unknown
	}
}

// ShouldBlockLightForOpenCover reports whether turning the light on to the given
// brightness should be refused by the "block light when cover is open" safety setting.
// It's a no-op whenever there's no cover to guard against (a cover-less panel like Lite
// can never report "Closed", which used to make this block permanent) or the setting is
// off, and it never blocks turning the light off. Pure/stateless on
// purpose: callers that already hold stateMutex (SetBrightness) must not go through
// SupportsCover()/GetCoverState() here, see the reentrancy note above getActiveProtocol().
func ShouldBlockLightForOpenCover(brightness int, blockSetting, supportsCover bool, coverState int) bool {
	if brightness <= 0 || !blockSetting || !supportsCover {
		return false
	}
	return coverState != 1
}

// SupportsBeep reports whether the connected panel supports the beep on/off command.
func SupportsBeep() bool { return getActiveProtocol().SupportsBeep() }

// SupportsBrightnessMode reports whether the connected panel supports low/high-bank
// brightness switching (the feature computeBankedBrightness automates).
func SupportsBrightnessMode() bool { return getActiveProtocol().SupportsBrightnessMode() }

// GetActiveProtocolName returns the identified panel revision's display name, or "-" if
// no device has been identified yet.
func GetActiveProtocolName() string {
	stateMutex.RLock()
	defer stateMutex.RUnlock()
	if activeProtocol == nil {
		return "-"
	}
	return activeProtocol.Name()
}

// SyncBeep sends the beep mode settings to the device based on configuration.
func SyncBeep() {
	if !IsConnected() {
		return
	}
	proto := getActiveProtocol()
	if !proto.SupportsBeep() {
		return
	}
	conf := config.Get()
	mode := 0
	if conf.EnableBeep {
		mode = 1
	}
	cmd := proto.FormatCommand('T', mode, true)
	logger.Info("Syncing beep setting: sending %s to device", cmd)
	SendCommand(cmd, true, 3*time.Second)
}

// GetFirmwareVersion returns the cached firmware version.
func GetFirmwareVersion() string {
	firmwareVersionMu.RLock()
	defer firmwareVersionMu.RUnlock()
	return firmwareVersion
}

func FetchBrightness() {
	if !IsConnected() {
		return
	}
	proto := getActiveProtocol()
	resp, err := SendCommand(proto.FormatCommand('J', 0, false), false, 3*time.Second)
	if err == nil {
		trimmed := strings.TrimSpace(resp)
		if b, ok := proto.ParseBrightness(trimmed); ok {
			stateMutex.Lock()
			currentBrightness = b
			if b > 0 {
				lastNonZeroBrightness = b
			}
			stateMutex.Unlock()
		}
	}
}

// FetchCalibrationStatus sends >A# to check whether the firmware has valid
// open/close angle calibration. Only Rev2 has this command (HasReadyQuery); other
// revisions are treated as always-ready once connected.
func FetchCalibrationStatus() {
	if !IsConnected() {
		return
	}
	proto := getActiveProtocol()
	if !proto.HasReadyQuery() {
		stateMutex.Lock()
		deviceReadyState = 2 // Ready
		stateMutex.Unlock()
		return
	}

	// Use HIGH priority so it is not delayed behind periodic >S# polling
	resp, err := SendCommand(proto.FormatCommand('A', 0, false), true, 3*time.Second)
	if err == nil {
		trimmed := strings.TrimSpace(resp)
		// Response: *Ax# where x=0 not calibrated, x=1 ready
		if strings.HasPrefix(trimmed, "*A") && strings.HasSuffix(trimmed, "#") && len(trimmed) >= 4 {
			valStr := trimmed[2 : len(trimmed)-1]
			// Firmware sends *A0Ready# or *A1Ready# (not just *A0# / *A1#)
			isReady := strings.HasPrefix(valStr, "1")
			stateMutex.Lock()
			if isReady {
				deviceReadyState = 2 // Ready
			} else {
				deviceReadyState = 1 // Not Ready
			}
			stateMutex.Unlock()
			if !isReady {
				logger.Warn("FetchCalibrationStatus: device not calibrated (>A# returned %q). Set both closed and open positions.", valStr)
			} else {
				logger.Debug("FetchCalibrationStatus: device calibration OK (>A# returned %q).", valStr)
			}
		} else {
			logger.Warn("FetchCalibrationStatus: unexpected response: %q", trimmed)
		}
	} else {
		logger.Warn("FetchCalibrationStatus: >A# failed: %v", err)
	}
}

// GetCalibratorState returns the ASCOM CalibratorState
func GetCalibratorState() int {
	stateMutex.RLock()
	defer stateMutex.RUnlock()
	// ASCOM CalibratorState: 0=NotPresent, 1=Off, 2=NotReady, 3=Ready, 4=Unknown, 5=Error
	if !calibratorOnState {
		return 1 // Off
	}
	return 3 // Ready
}

func SetCalibratorOnState(on bool) {
	stateMutex.Lock()
	calibratorOnState = on
	stateMutex.Unlock()
}

// GetCoverState returns the ASCOM CoverState
func GetCoverState() int {
	stateMutex.RLock()
	defer stateMutex.RUnlock()
	hasCover := activeProtocol == nil || activeProtocol.SupportsCover()
	return ascomCoverStateFromRaw(hasCover, coverState)
}

func GetCurrentBrightness() int {
	stateMutex.RLock()
	defer stateMutex.RUnlock()
	return currentBrightness
}

// GetHeaterPower returns the last-known dew-heater percent (0-100). Best-effort only,
// same caveat as SetHeaterPower's doc comment — there's no known query command, so this
// is whatever this driver itself last sent, not a device readback.
func GetHeaterPower() int {
	stateMutex.RLock()
	defer stateMutex.RUnlock()
	return heaterPercent
}

func GetLiveStatus() map[string]interface{} {
	stateMutex.RLock()
	defer stateMutex.RUnlock()

	revisionName := "-"
	hasCover := true
	hasBeep := true
	hasBrightnessMode := true
	hasHeater := false
	hasAutoCalibrate := false
	hasHalt := false
	if activeProtocol != nil {
		revisionName = activeProtocol.Name()
		hasCover = activeProtocol.SupportsCover()
		hasBeep = activeProtocol.SupportsBeep()
		hasBrightnessMode = activeProtocol.SupportsBrightnessMode()
		hasHeater = activeProtocol.SupportsHeater()
		hasAutoCalibrate = activeProtocol.SupportsAutoCalibrate()
		hasHalt = activeProtocol.SupportsHalt()
	}

	ascomCoverState := ascomCoverStateFromRaw(hasCover, coverState)

	// Also expose the effective (already-capped) brightness ceiling here, rather than
	// making the dashboard re-derive the same 254-cap rule from has_brightness_mode
	// itself, which used to be duplicated a third time there.
	effectiveMaxBrightness := EffectiveMaxBrightness(config.Get().MaxBrightness, hasBrightnessMode)

	var deviceReadyVal interface{}
	if deviceReadyState == 0 {
		deviceReadyVal = nil
	} else {
		deviceReadyVal = (deviceReadyState == 2)
	}

	return map[string]interface{}{
		"brightness":               currentBrightness,
		"cover_state":              ascomCoverState,
		"light_state":              lightStatus,
		"high_mode":                highMode,
		"current_motor_angle":      currentMotorAngle,
		"open_motor_angle":         openMotorAngle,
		"device_ready":             deviceReadyVal,
		"panel_revision":           revisionName,
		"has_cover":                hasCover,
		"has_beep":                 hasBeep,
		"has_brightness_mode":      hasBrightnessMode,
		"effective_max_brightness": effectiveMaxBrightness,
		"has_heater":               hasHeater,
		"heater_percent":           heaterPercent,
		"has_auto_calibrate":       hasAutoCalibrate,
		"has_halt":                 hasHalt,
	}
}

// SendCommand queues a command to be sent to the device.
func SendCommand(command string, isHighPriority bool, timeout time.Duration) (string, error) {
	if timeout == 0 {
		timeout = 1500 * time.Millisecond
	}

	// Ensure the queue timeout is longer than the execution timeout
	// so that if a previous command times out, the next command has
	// enough time to be queued without hitting the channel select timeout.
	queueTimeout := timeout + 2*time.Second

	responseChan := make(chan string, 1)
	errorChan := make(chan error, 1)
	cmd := Command{
		Command:  command,
		Response: responseChan,
		Error:    errorChan,
		Timeout:  timeout,
	}
	if isHighPriority {
		select {
		case highPriorityCommands <- cmd:
		case <-time.After(queueTimeout):
			return "", errors.New("command queue busy, timeout before sending")
		}
	} else {
		select {
		case lowPriorityCommands <- cmd:
		case <-time.After(queueTimeout):
			return "", errors.New("command queue busy, timeout before sending")
		}
	}
	select {
	case response := <-responseChan:
		return response, nil
	case err := <-errorChan:
		return "", err
	case <-time.After(timeout):
		return "", errors.New("command timed out")
	}
}

// Brightness Logic
func SetBrightness(brightness int) error {
	stateMutex.Lock()

	// Guards against a race found via live testing: SetBrightness sends multiple
	// commands in sequence (an optional bank switch, then >B#, then >L#/>D#). Without
	// this, periodicPoller's independent >S# poll could land in the gap between them
	// and observe a transitional device state (e.g. brightness already set but the
	// light not yet turned on) — parseStatusString would then misread that as "light
	// was turned off externally" and zero out currentBrightness, which a later poll
	// then "fixed" by guessing 255. Block status polling for the duration of our own
	// transaction instead, mirroring the existing openCloseInProgress guard.
	//
	// This flag also doubles as mutual exclusion against SetLowBankAndZeroBrightness:
	// both functions write the same cached fields (currentBrightness/highMode/
	// lightStatus/calibratorOnState) at different points relative to their own wire
	// commands, so two of these calls running concurrently could leave the cache
	// disagreeing with whatever order their commands actually landed on the wire in.
	// Rejecting here (rather than blocking) matches JogMotor's busy-guard pattern and
	// needs no caller changes since this already returns error.
	if brightnessChangeInProgress {
		stateMutex.Unlock()
		return errors.New("device is busy changing brightness")
	}
	brightnessChangeInProgress = true
	defer func() {
		stateMutex.Lock()
		brightnessChangeInProgress = false
		stateMutex.Unlock()
	}()

	conf := config.Get()
	proto := activeProtocol
	if proto == nil {
		proto = rev2Protocol{}
	}
	bankCapable := proto.SupportsBrightnessMode()
	effectiveMax := EffectiveMaxBrightness(conf.MaxBrightness, bankCapable)

	if brightness < 0 {
		brightness = 0
	}
	if brightness > effectiveMax {
		brightness = effectiveMax
	}

	// Safety Check: block turning on the light unless cover is closed (1)
	if ShouldBlockLightForOpenCover(brightness, conf.BlockLightWhenOpen, proto.SupportsCover(), coverState) {
		stateMutex.Unlock()
		return errors.New("cannot turn on calibrator when cover is not closed")
	}

	currentBrightness = brightness
	if brightness > 0 {
		lastNonZeroBrightness = brightness
		calibratorOnState = true
	}

	var cmds []string
	var internalVal int

	if bankCapable {
		res := computeBankedBrightness(brightness, conf.HighBankStartValue, highMode)
		if res.NeedsSwitch {
			mode := 0
			if res.NewHighMode {
				mode = 1
			}
			cmds = append(cmds, proto.FormatCommand('Y', mode, true))
		}
		highMode = res.NewHighMode
		internalVal = res.InternalValue
	} else {
		internalVal = brightness
		if internalVal > 254 {
			internalVal = 254
		}
		highMode = false
	}

	cmds = append(cmds, proto.FormatCommand('B', internalVal, true))

	// Turn on or off explicitly if needed based on value
	if brightness > 0 {
		cmds = append(cmds, proto.FormatCommand('L', 0, false))
		lightStatus = 1
	} else {
		cmds = append(cmds, proto.FormatCommand('D', 0, false))
		lightStatus = 0
	}
	stateMutex.Unlock()

	var lastErr error
	for _, cmd := range cmds {
		if _, err := SendCommand(cmd, true, 0); err != nil {
			lastErr = err
		}
	}

	settleTimeMs := config.Get().SettleTime
	if settleTimeMs > 0 {
		logger.Debug("SetBrightness: sleeping for %d ms settle time", settleTimeMs)
		time.Sleep(time.Duration(settleTimeMs) * time.Millisecond)
	}

	return lastErr
}

// SetLowBankAndZeroBrightness sends the same kind of multi-command sequence
// (Y0/B0/D0) that motivated adding brightnessChangeInProgress to SetBrightness, but
// previously never set that flag itself — periodicPoller could still interleave a >S#
// poll mid-sequence here and misread a transitional state, same mechanism as the bug
// SetBrightness's own guard was added for.
//
// Called from two places (HandleConnected's PUT Connected=true, and reconnect()'s own
// post-connect init goroutine, 2 seconds later — both fire-and-forget), and both do the
// exact same reset. That means an entirely expected overlap between the two isn't a
// problem to flag, just something to skip: HandleConnected's PUT Connected=true fires
// once per connected Alpaca device (CoverCalibrator and Switch both route through the
// same driverConnected/API struct, so a client connecting to both at once — e.g. N.I.N.A's
// "connect all" — sends two near-simultaneous PUTs), and any of those can also land while
// reconnect()'s own delayed call is in flight for the same fresh connection. Whichever
// call loses the race below is redundant, not lost: the winner already performs the
// identical reset. Both functions share this same mutual-exclusion check against
// SetBrightness, so guarding it here once covers every current and future caller instead
// of duplicating the check at each site.
func SetLowBankAndZeroBrightness() {
	stateMutex.Lock()
	if brightnessChangeInProgress {
		stateMutex.Unlock()
		// Info, not Warn: this fires whenever this call's reset overlaps with either
		// another one already doing the identical reset (harmless, see above), or a
		// genuine in-flight SetBrightness() -- in which case skipping is *correct*
		// (a fresh connect shouldn't stomp on a brightness change already underway).
		// Neither case needs the user's attention.
		logger.Info("SetLowBankAndZeroBrightness: another brightness operation is already in progress, skipping (expected when a connect overlaps another reset or brightness change already in flight).")
		return
	}
	brightnessChangeInProgress = true
	inProgress := openCloseInProgress
	stateMutex.Unlock()
	defer func() {
		stateMutex.Lock()
		brightnessChangeInProgress = false
		stateMutex.Unlock()
	}()

	proto := getActiveProtocol()

	if inProgress {
		logger.Info("SetLowBankAndZeroBrightness: cover movement in progress, skipping sending hardware setup commands to avoid interference.")
	} else {
		if proto.SupportsBrightnessMode() {
			SendCommand(proto.FormatCommand('Y', 0, true), true, 0)
		}
		SendCommand(proto.FormatCommand('B', 0, true), true, 0)
		SendCommand(proto.FormatCommand('D', 0, false), true, 0)
	}

	stateMutex.Lock()
	highMode = false
	currentBrightness = 0
	lightStatus = 0
	calibratorOnState = false
	stateMutex.Unlock()
}

// ShouldTurnOffLightBeforeOpening reports whether OpenCover should turn the light off
// before driving the motor. Previously the "block light when cover is open" safety
// setting only blocked turning the light ON while the cover was open (SetBrightness's
// own guard, ShouldBlockLightForOpenCover above) — it did nothing if the light was
// already on when Open was requested, i.e. the gating was one-directional. Pure/
// stateless, same reasoning as the other Should*/Effective* helpers in this file.
func ShouldTurnOffLightBeforeOpening(blockSetting, supportsCover bool, currentBrightness int) bool {
	return supportsCover && blockSetting && currentBrightness > 0
}

// OpenCover opens the motorized cover, if the connected panel has one. If
// ShouldTurnOffLightBeforeOpening says so, the light is turned off first.
func OpenCover() {
	stateMutex.RLock()
	brightness := currentBrightness
	stateMutex.RUnlock()
	if ShouldTurnOffLightBeforeOpening(config.Get().BlockLightWhenOpen, SupportsCover(), brightness) {
		logger.Info("OpenCover: BlockLightWhenOpen is set and the light is on — turning it off before opening")
		if err := SetBrightness(0); err != nil {
			logger.Warn("OpenCover: failed to turn off the light before opening: %v", err)
		}
	}
	moveCover('O', 2, func(p Protocol, resp string) bool { return p.IsOpenConfirmed(resp) })
}

// CloseCover closes the motorized cover, if the connected panel has one.
func CloseCover() {
	moveCover('C', 1, func(p Protocol, resp string) bool { return p.IsCloseConfirmed(resp) })
}

// moveCover drives OpenCover/CloseCover. letter is 'O' or 'C', confirmedState is the
// raw coverState value to set on success (2=open, 1=closed).
func moveCover(letter byte, confirmedState int, isConfirmed func(Protocol, string) bool) {
	proto := getActiveProtocol()

	if !proto.SupportsCover() {
		logger.Warn("cover command '%c' called on a panel with no motorized cover; ignoring", letter)
		return
	}

	stateMutex.Lock()
	setCoverStateLocked(0) // Moving
	openCloseInProgress = true
	// A fresh Open/Close attempt gets a clean slate to prove itself trustworthy, even
	// if an earlier attempt was halted -- see coverStateUnreliable's doc comment. If
	// this one also gets interrupted, HaltCover sets it again.
	coverStateUnreliable = false
	stateMutex.Unlock()

	cmd := proto.FormatCommand(letter, 0, false)

	if proto.IsAsyncCover() {
		// Pro: the command only acknowledges receipt (with the reached-position
		// number once done — but that arrives later); actual completion must be
		// observed via >S# polling.
		moveCoverAsync(proto, cmd, confirmedState, isConfirmed)
	} else {
		// Rev2 (the only remaining revision that reaches this branch — Lite has no
		// cover at all): the command blocks until the final confirmation arrives on
		// the same read, unchanged from this project's original behavior.
		moveCoverBlocking(proto, cmd, confirmedState, isConfirmed)
	}
}

func moveCoverBlocking(proto Protocol, cmd string, confirmedState int, isConfirmed func(Protocol, string) bool) {
	// We do NOT poll >S# during movement — sending extra commands during motor
	// operation interferes with the firmware and causes calibration loss on these
	// synchronous revisions. The portMutex is held in ProcessCommands during this
	// wait; that is intentional and matches original ASCOM driver behaviour.
	timeout := time.Duration(config.Get().CoverTimeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	resp, err := SendCommand(cmd, true, timeout)

	stateMutex.Lock()
	openCloseInProgress = false
	if coverState == 0 { // Only update if it wasn't halted!
		// A halted move's own reply can look exactly like a genuine confirmation
		// (same *O<adc>#/*C<adc>#) even though the motor was stopped early — see
		// coverStateUnreliable's doc comment. Don't trust it in that case.
		if err == nil && isConfirmed(proto, resp) && !coverStateUnreliable {
			setCoverStateLocked(confirmedState)
		} else {
			if err == nil && isConfirmed(proto, resp) {
				logger.Info("cover move: reply looked like a confirmation but a halt was requested for this move — not trusting it, see coverStateUnreliable")
			} else {
				logger.Warn("cover move: did not receive expected confirmation (resp=%q err=%v)", resp, err)
			}
			setCoverStateLocked(3) // Timed out / Error
		}
	}
	stateMutex.Unlock()

	if err == nil {
		go FetchCalibrationStatus()
	}
}

// clampToRemaining returns the smaller of d and remaining (never negative), so a wait
// never overruns a deadline it's meant to respect. Pure/stateless. See moveCoverAsync's
// fallback loop below.
func clampToRemaining(d, remaining time.Duration) time.Duration {
	if remaining < 0 {
		return 0
	}
	if remaining < d {
		return remaining
	}
	return d
}

func moveCoverAsync(proto Protocol, cmd string, confirmedState int, isConfirmed func(Protocol, string) bool) {
	timeout := time.Duration(config.Get().CoverTimeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)

	// On Pro, the command's own reply already IS the completion signal (a
	// reached-angle acknowledgement sent once the physical move finishes), not an
	// immediate "received" ack, and can take several seconds to arrive. Give it the
	// full timeout budget, matching the synchronous revisions, rather than a short guess.
	resp, err := SendCommand(cmd, true, time.Until(deadline))
	stateMutex.RLock()
	unreliable := coverStateUnreliable
	stateMutex.RUnlock()
	if err == nil && isConfirmed(proto, resp) && !unreliable {
		stateMutex.Lock()
		openCloseInProgress = false
		if coverState == 0 {
			setCoverStateLocked(confirmedState)
		}
		stateMutex.Unlock()
		go FetchCalibrationStatus()
		return
	}
	if err == nil && isConfirmed(proto, resp) {
		// Looked like a genuine confirmation, but a halt was requested for this move
		// — see coverStateUnreliable's doc comment. Don't trust it; fall through to
		// the same status-polling path used for a missing/malformed reply, which
		// (via parseStatusString) also won't trust an ordinary >S# poll right now.
		logger.Info("cover move (async): reply looked like a confirmation but a halt was requested for this move — not trusting it, falling back to status polling")
	} else {
		logger.Warn("cover move (async): command reply was not a recognized confirmation (resp=%q err=%v), falling back to status polling", resp, err)
	}

	// Fallback: whether the direct reply was missing, malformed, or just slower than
	// our budget, keep polling >S# for whatever time remains.
	//
	// Both the sleep and the status query's own timeout are clamped to whatever budget
	// is actually left, rather than always waiting the full 1s+2s regardless — with a
	// short coverTimeout, entering this loop with under 1s remaining previously still
	// slept the full second and ran one more query, overshooting the user-configured
	// timeout by up to ~3s.
	statusCmd := proto.FormatCommand('S', 0, false)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		time.Sleep(clampToRemaining(1*time.Second, remaining))

		remaining = time.Until(deadline)
		if remaining <= 0 {
			break
		}
		SendCommand(statusCmd, false, clampToRemaining(2*time.Second, remaining)) // updates coverState via parseStatusString
		stateMutex.RLock()
		cs := coverState
		stateMutex.RUnlock()
		if cs != 0 {
			break
		}
	}

	stateMutex.Lock()
	openCloseInProgress = false
	if coverState == 0 {
		logger.Warn("cover move (async): cover status did not settle within %v", timeout)
		setCoverStateLocked(3)
	}
	stateMutex.Unlock()

	go FetchCalibrationStatus()
}

// HaltCover interrupts an in-progress cover move using >K#, on revisions that actually
// have one (SupportsHalt -- Pro only, confirmed live). If the cover isn't currently
// moving, this is a no-op success, matching the ASCOM CoverCalibrator spec ("stops any
// operation in progress"): nothing is in progress, so there's nothing to fail.
//
// This is also what the calibration modal's Stop button calls, which can interrupt an
// in-progress auto-calibrate sweep (>N#/>R#) the same way -- see autoCalibrate's own
// doc comment for an important difference confirmed live: unlike a plain Open/Close
// (where closedAngle/openAngle demonstrably survive untouched, see below), halting an
// auto-calibrate sweep makes the firmware immediately save wherever the motor was as
// the new calibration value -- it can't tell a forced stop from a genuine hard stop.
// That's real, not just a stale status report, and this function has no way to prevent
// or undo it; only autoCalibrate's own return value (and the UI not claiming success)
// reflects it.
//
// Signals ProcessCommands' motion-command read loop (still blocked on the in-progress
// >O#/>C#'s own wait) via haltRequested, then polls >S# until the firmware reports the
// motor stopped or a bounded timeout elapses. The firmware stops the motor within
// ~1-2s and, for a plain Open/Close specifically, the panel's calibration
// (closedAngle/openAngle) survives untouched -- but its own >S# cover-state
// classification can stay wrong for a long time afterward, not just briefly, even
// producing a same-shaped, individually-convincing confirmation reply for the
// interrupted command itself. So this doesn't just set the cached state to Unknown
// once and hope the next poll fixes it -- it also sets coverStateUnreliable, which
// both parseStatusString and the interrupted command's own completion handler check,
// so neither an ordinary status poll nor that command's own delayed reply can
// override Unknown with a value that might still be wrong. Only a fresh,
// uninterrupted Open/Close/auto-calibrate clears it.
func HaltCover() error {
	proto := getActiveProtocol()
	if !proto.SupportsHalt() {
		return errors.New("this panel's firmware has no command to interrupt an in-progress cover move")
	}

	stateMutex.Lock()
	moving := coverState == 0
	if moving {
		// Set before anything else reacts to the halt: both parseStatusString and the
		// interrupted command's own completion handler (moveCoverBlocking/
		// moveCoverAsync) check this to avoid trusting a stale or same-shaped-but-not-
		// genuine confirmation once the motor actually stops. See its doc comment.
		coverStateUnreliable = true
	}
	stateMutex.Unlock()
	if !moving {
		return nil
	}

	select {
	case haltRequested <- struct{}{}:
	default:
	}

	statusCmd := proto.FormatCommand('S', 0, false)
	deadline := time.Now().Add(10 * time.Second)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("sent a halt command but the panel still reports the cover moving")
		}
		SendCommand(statusCmd, true, clampToRemaining(2*time.Second, remaining))
		stateMutex.RLock()
		stopped := coverState != 0
		stateMutex.RUnlock()
		if stopped {
			break
		}
		time.Sleep(clampToRemaining(300*time.Millisecond, time.Until(deadline)))
	}

	stateMutex.Lock()
	setCoverStateLocked(4) // Unknown -- see doc comment above
	openCloseInProgress = false
	stateMutex.Unlock()

	go FetchCalibrationStatus()
	return nil
}

// jogCommandValue converts a desired jog delta (in closedAngle/openAngle/
// currentMotorAngle's raw units) into the argument >M<n># actually needs, given the
// revision's confirmed JogScaleFactor(). factor == 1.0 (unverified revisions) passes
// angle straight through unchanged. Otherwise divides by factor, floored to ±1 for any
// non-zero angle so a small request (e.g. Pro's factor of 5.4 on angle=1) can't
// silently round down to 0 and become a no-op. Pure and stateless for easy testing.
func jogCommandValue(angle int, factor float64) int {
	if angle == 0 || factor == 1.0 {
		return angle
	}
	scaled := int(math.Round(float64(angle) / factor))
	if scaled == 0 {
		if angle > 0 {
			return 1
		}
		return -1
	}
	return scaled
}

// JogMotor jogs the motor by a relative delta (+/-), in the same raw units
// closedAngle/openAngle/currentMotorAngle already use — not necessarily physical
// degrees 1:1 (see JogScaleFactor's doc comment). Sets the same busy guard
// (coverState/openCloseInProgress) that OpenCover/CloseCover already use, so a jog in
// progress correctly blocks other motor commands from being sent concurrently:
// without it, a CloseCover landing while a jog is still physically moving drives the
// motor past its calibrated open limit all the way to the mechanical hard stop
// instead of reversing.
func JogMotor(angle int) error {
	if !SupportsCover() {
		return errors.New("this panel has no motorized cover")
	}

	stateMutex.Lock()
	if coverState == 0 {
		stateMutex.Unlock()
		return errors.New("device is busy moving cover")
	}
	setCoverStateLocked(0) // Moving
	openCloseInProgress = true
	stateMutex.Unlock()

	proto := getActiveProtocol()
	cmd := proto.FormatCommand('M', jogCommandValue(angle, proto.JogScaleFactor()), true)

	// On revisions where >M is confirmed to genuinely reply (currently Pro only — see
	// hasResponse()), that reply only arrives once the physical move is actually done,
	// with latency scaling with move size (observed up to ~7s for a 45-magnitude jog) —
	// far longer than SendCommand's 1.5s default, so use the same generous timeout
	// OpenCover/CloseCover/the busy-release poller below already rely on. Harmless
	// no-op on unverified revisions, where hasResponse() short-circuits before this
	// timeout is ever consulted.
	timeout := time.Duration(config.Get().CoverTimeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	_, err := SendCommand(cmd, true, timeout)

	// On unverified revisions (still assumed NC — see hasResponse()), a jog has no
	// confirmed reply to wait on and no single discrete "reached state" to set
	// afterward — a jog can land anywhere. So instead of blocking the caller, release
	// the busy guard asynchronously once the normal >S# polling mechanism reports the
	// motor has actually stopped (parseStatusString already flips coverState away from
	// "moving" once MotorRunning goes false). On Pro, SendCommand above already waited
	// for >M's own reply, so the move is done and this poller's first check normally
	// just confirms that immediately — kept as a uniform safety net rather than
	// branching the busy-release logic by revision too.
	go func() {
		timeout := time.Duration(config.Get().CoverTimeout) * time.Second
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		deadline := time.Now().Add(timeout)
		statusCmd := proto.FormatCommand('S', 0, false)
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			time.Sleep(clampToRemaining(1*time.Second, remaining))

			remaining = time.Until(deadline)
			if remaining <= 0 {
				break
			}
			SendCommand(statusCmd, false, clampToRemaining(2*time.Second, remaining)) // updates coverState via parseStatusString
			stateMutex.RLock()
			cs := coverState
			stateMutex.RUnlock()
			if cs != 0 {
				break
			}
		}
		stateMutex.Lock()
		openCloseInProgress = false
		stateMutex.Unlock()
	}()

	return err
}

func SetClosedPosition() error {
	if !SupportsCover() {
		return errors.New("this panel has no motorized cover")
	}
	stateMutex.RLock()
	isMoving := (coverState == 0)
	stateMutex.RUnlock()
	if isMoving {
		return errors.New("device is busy moving cover")
	}
	proto := getActiveProtocol()
	_, err := SendCommand(proto.FormatCommand('F', 0, false), true, 3*time.Second)
	if err == nil {
		go FetchCalibrationStatus()
	}
	return err
}

func SetOpenedPosition() error {
	if !SupportsCover() {
		return errors.New("this panel has no motorized cover")
	}
	stateMutex.RLock()
	isMoving := (coverState == 0)
	stateMutex.RUnlock()
	if isMoving {
		return errors.New("device is busy moving cover")
	}
	proto := getActiveProtocol()
	_, err := SendCommand(proto.FormatCommand('E', 0, false), true, 3*time.Second)
	if err == nil {
		go FetchCalibrationStatus()
	}
	return err
}

// parseAngleReply parses a "*<tag><digits>#" reply (e.g. "*N71#", "*R813#") — the format
// AutoCalibrateClosed/AutoCalibrateOpen's >N#/>R# replies use. Pure/stateless.
func parseAngleReply(resp string, tag byte) (int, bool) {
	if len(resp) < 3 || resp[0] != '*' || resp[1] != tag {
		return 0, false
	}
	end := strings.IndexByte(resp, '#')
	if end <= 2 {
		return 0, false
	}
	v, err := strconv.Atoi(resp[2:end])
	if err != nil {
		return 0, false
	}
	return v, true
}

// AutoCalibrateClosed drives the panel's motor to its physical closed hard-stop and
// records the measured angle as the calibrated closed position — a fully automatic
// alternative to SetClosedPosition's "record wherever the motor currently is" (which
// requires manually jogging it into place first). Experimental — see
// SupportsAutoCalibrate's doc comment in protocol_pro.go.
func AutoCalibrateClosed() error {
	return autoCalibrate('N', &currentMotorAngle, 1) // raw coverState 1 = closed
}

// AutoCalibrateOpen is AutoCalibrateClosed's open-position counterpart (>R#).
func AutoCalibrateOpen() error {
	return autoCalibrate('R', &openMotorAngle, 2) // raw coverState 2 = open
}

// autoCalibrate drives moveCover's shared "busy" guard through the same coverState/
// openCloseInProgress bookkeeping used by the rest of the motor commands, so this can't
// run concurrently with a jog/open/close/save. Observed round trip on real hardware is
// ~22s (a full physical sweep to the hard stop) — the wait is never allowed to be
// shorter than that, regardless of a short configured coverTimeout, since this is a
// genuinely long-running operation by design, not something that should time out early.
func autoCalibrate(letter byte, target *int, confirmedState int) error {
	if !SupportsAutoCalibrate() {
		return errors.New("this panel has no automatic calibration")
	}

	stateMutex.Lock()
	if coverState == 0 {
		stateMutex.Unlock()
		return errors.New("device is busy moving cover")
	}
	setCoverStateLocked(0) // Moving
	openCloseInProgress = true
	// A full hard-stop sweep is as trustworthy a position measurement as this device
	// has -- give it a clean slate even if an earlier Open/Close was halted. See
	// coverStateUnreliable's doc comment.
	coverStateUnreliable = false
	stateMutex.Unlock()

	proto := getActiveProtocol()
	timeout := time.Duration(config.Get().CoverTimeout) * time.Second
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}

	resp, err := SendCommand(proto.FormatCommand(letter, 0, false), true, timeout)

	stateMutex.Lock()
	openCloseInProgress = false
	angle, ok := parseAngleReply(resp, letter)
	// Unlike a halted Open/Close (see coverStateUnreliable's doc comment), this can't
	// be fully undone client-side: confirmed live, the firmware itself commits wherever
	// the motor was as the new calibration value the instant it stops, whether that's a
	// genuine hard stop or a K-forced one -- it uses the same stall-detection completion
	// path either way and can't tell the difference. So the device's own closedAngle/
	// openAngle may already be wrong by the time this runs, regardless of what we do
	// here. What this still controls is only this project's own reaction: not writing
	// *target from an interrupted attempt's reply, and reporting failure (not "✓
	// Learned!") so the caller knows to redo the calibration rather than trust it.
	trustworthy := err == nil && ok && !coverStateUnreliable
	if trustworthy {
		*target = angle
		if coverState == 0 { // Only update if it wasn't halted!
			setCoverStateLocked(confirmedState)
		}
	} else {
		if err == nil && ok {
			logger.Info("auto-calibration ('%c'): reply looked valid but a halt was requested for this attempt — not trusting the measured angle", letter)
		} else {
			logger.Warn("auto-calibration ('%c'): did not receive expected reply (resp=%q err=%v)", letter, resp, err)
		}
		if coverState == 0 {
			setCoverStateLocked(4) // Unknown / Error
		}
	}
	stateMutex.Unlock()

	if err != nil {
		return err
	}
	if !trustworthy {
		return fmt.Errorf("auto-calibration for '%c' was halted or gave an untrustworthy reply (resp=%q)", letter, resp)
	}

	go FetchCalibrationStatus()
	return nil
}

// SetHeaterPower sets the Pro panel's dew-heater output to percent (0-100), clamped to
// that range the same way SetBrightness clamps brightness. Command per the vendor
// driver's own capture (>Wxx#, unpadded) — see SupportsHeater's doc comment in
// protocol_pro.go for the "not yet physically confirmed" caveat. There is no known query
// command, so like currentBrightness this is tracked locally as best-effort state only,
// not read back from the device.
func SetHeaterPower(percent int) error {
	if !SupportsHeater() {
		return errors.New("this panel has no heater output")
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	proto := getActiveProtocol()
	_, err := SendCommand(proto.FormatCommand('W', percent, true), true, 0)
	if err == nil {
		stateMutex.Lock()
		heaterPercent = percent
		stateMutex.Unlock()
	}
	return err
}

func ProcessCommands() {
	logger.Info("Serial command processor started.")
	for {
		var cmd Command
		select {
		case cmd = <-highPriorityCommands:
		default:
			select {
			case cmd = <-highPriorityCommands:
			case cmd = <-lowPriorityCommands:
			}
		}

		portMutex.Lock()
		if panelPort == nil {
			maxWait := 15
			for {
				serialConnectedMu.RLock()
				count := failedReconnectCount
				serialConnectedMu.RUnlock()

				maxRetries := config.Get().MaxConnectionRetries
				if panelPort != nil || count == 0 || count > maxRetries || maxWait <= 0 {
					break
				}
				portMutex.Unlock()
				time.Sleep(100 * time.Millisecond)
				maxWait--
				portMutex.Lock()
			}
			if panelPort == nil {
				portMutex.Unlock()
				cmd.Error <- errors.New("serial port is not open")
				continue
			}
		}

		drainInputBuffer(panelPort)

		_, err := panelPort.Write([]byte(cmd.Command))
		if err != nil {
			handleDisconnect()
			portMutex.Unlock()
			cmd.Error <- fmt.Errorf("failed to write to serial port: %w", err)
			continue
		}

		if !hasResponse(cmd.Command) {
			portMutex.Unlock()
			cmd.Response <- ""
			continue
		}

		term := getActiveProtocol().Terminator()

		var trimmed string
		var readFailed bool

		// For >O# and >C#, the device sends intermediate *M... motor telemetry
		// before the final confirmation frame. We loop with a deadline to skip
		// intermediate frames, never resetting the timeout per frame. Prefix-based
		// (not exact-match) rather than matching ">O#"/">C#" literally, since every
		// currently-supported revision formats these unpadded but there's no reason
		// this couldn't change with a future revision.
		isMotionCmd := strings.HasPrefix(cmd.Command, ">O") || strings.HasPrefix(cmd.Command, ">C")
		// Auto-calibrate (>N#/>R#) also drives the motor for an extended, blocking
		// wait (a hard-stop sweep, ~20-25s) and can be halted the same way -- unlike
		// O/C, its firmware reply doesn't arrive via intermediate frames that need
		// skipping (no *M telemetry during N/R), so it doesn't need isMotionCmd's own
		// expected-prefix handling below, just the same halt-injection hook.
		isHaltableWait := isMotionCmd || strings.HasPrefix(cmd.Command, ">N") || strings.HasPrefix(cmd.Command, ">R")
		deadline := time.Now().Add(cmd.Timeout)

		// onIdle lets HaltCover interrupt this exact wait: if a halt was requested
		// while we're blocked here (portMutex held, so a normally-queued >K# would
		// just sit behind this command and arrive too late to matter), inject >K#
		// directly -- same goroutine, no new locking needed. The firmware stops the
		// motor promptly, but doesn't flush THIS command's own pending reply on its
		// own afterward -- it needs another command sent before it settles and
		// replies. So also shorten this
		// transaction's own deadline to a short grace window instead of its full
		// original timeout, so the worker frees up quickly for HaltCover's own >S#
		// polling, which is what actually gets the firmware to flush.
		var onIdle func()
		if isHaltableWait && getActiveProtocol().SupportsHalt() {
			onIdle = func() {
				select {
				case <-haltRequested:
					logger.Info("HaltCover: injecting >K# while waiting on '%s'", cmd.Command)
					if _, werr := panelPort.Write([]byte(">K#")); werr != nil {
						logger.Warn("HaltCover: failed to write >K#: %v", werr)
						return
					}
					if grace := time.Now().Add(3 * time.Second); grace.Before(deadline) {
						deadline = grace
					}
				default:
				}
			}
		}

		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				logger.Warn("Serial deadline exceeded on command '%s'", cmd.Command)
				portMutex.Unlock()
				cmd.Error <- fmt.Errorf("failed to read from serial port: read timeout")
				readFailed = true
				break
			}

			response, readErr := readLine(panelPort, term, remaining, onIdle)
			if readErr != nil {
				if readErr.Error() == "read timeout" {
					logger.Warn("Serial read timeout on command '%s' (not disconnecting)", cmd.Command)
				} else {
					logger.Error("Serial read error on command '%s' (disconnecting): %v", cmd.Command, readErr)
					handleDisconnect()
				}
				portMutex.Unlock()
				cmd.Error <- fmt.Errorf("failed to read from serial port: %w", readErr)
				readFailed = true
				break
			}

			trimmed = strings.TrimSpace(response)
			logger.Debug("Received: %s", trimmed)

			if strings.HasPrefix(trimmed, "*S") {
				parseStatusString(trimmed)
			} else if strings.HasPrefix(trimmed, "*M") {
				parseMotorInfo(trimmed)
			}

			// For motion commands: skip intermediate *M motor updates and keep
			// reading until we receive the final *O / *C confirmation frame.
			if isMotionCmd {
				expectedPrefix := "*O"
				if strings.HasPrefix(cmd.Command, ">C") {
					expectedPrefix = "*C"
				}
				if !strings.HasPrefix(trimmed, expectedPrefix) {
					continue // skip this intermediate frame, read next
				}
			}
			break
		}

		if !readFailed {
			if strings.HasPrefix(trimmed, "*S") {
				parseStatusString(trimmed)
			} else if strings.HasPrefix(trimmed, "*M") ||
				strings.HasPrefix(trimmed, "*E") ||
				strings.HasPrefix(trimmed, "*F") {
				// *M = motor telemetry, *E = set-opened confirm, *F = set-closed confirm
				// All share the same Ca.../Oa... format
				parseMotorInfo(trimmed)
			}
			portMutex.Unlock()
			cmd.Response <- trimmed
		}
	}
}

func hasResponse(cmd string) bool {
	// Commands with *NC* (No Confirmation) per protocol:
	// >Yx# - Set brightness mode
	// >Tx# - Beep on/off
	// >Wxx# - Dew heater power (no reply)
	// >F#/>E# - Set closed/opened position. Revision-dependent, unlike the NC commands
	// above: Pro replies (*F<adc>#/*E<adc>#) but Rev2 is NC -- SetClosedPosition/
	// SetOpenedPosition always timed out on Rev2 before this was caught, leaving
	// calibration permanently un-saveable there. Not parsed for content either way on
	// the revisions that do reply; SetClosedPosition/SetOpenedPosition just wait for
	// the reply, then re-fetch status separately.
	if strings.HasPrefix(cmd, ">F") || strings.HasPrefix(cmd, ">E") {
		return getActiveProtocol().SupportsPositionSetReply()
	}
	if strings.HasPrefix(cmd, ">Y") ||
		strings.HasPrefix(cmd, ">T") ||
		strings.HasPrefix(cmd, ">W") {
		return false
	}
	if strings.HasPrefix(cmd, ">M") {
		// >Mxxx# - Jog motor. Genuinely replies (*O<newRawPosition>#) on revisions whose
		// jog protocol is fully understood — currently Pro only (see JogScaleFactor's
		// doc comment). Treated as NC everywhere else. Reuses JogScaleFactor() as the
		// "understood" signal since both facts go together.
		return getActiveProtocol().JogScaleFactor() != 1.0
	}
	return true
}

// parseStatusString dispatches a >S# reply to the active protocol's parser and applies
// the result to shared connection state.
func parseStatusString(status string) {
	stateMutex.RLock()
	proto := activeProtocol
	stateMutex.RUnlock()
	if proto == nil {
		proto = rev2Protocol{}
	}

	frame, ok := proto.ParseStatus(status)
	if !ok {
		return
	}

	stateMutex.Lock()
	defer stateMutex.Unlock()

	if frame.LightOn {
		lightStatus = 1
		calibratorOnState = true
		if currentBrightness == 0 {
			currentBrightness = brightnessFallbackGuess(lastNonZeroBrightness, proto.SupportsBrightnessMode())
		}
	} else {
		// If the light is physically off, but we are in "On at brightness 0" state,
		// we keep calibratorOnState = true.
		// Otherwise, if currentBrightness > 0, it means it was turned off externally.
		lightStatus = 0
		if currentBrightness > 0 {
			calibratorOnState = false
			currentBrightness = 0
		}
	}

	if proto.SupportsCover() {
		c := frame.CoverState
		// Override: if motor is running, then cover is moving
		if frame.MotorRunning {
			c = 0
			// A fresh move is happening right now; whatever uncertainty an earlier
			// halt left behind no longer applies to what we're about to observe.
			coverStateUnreliable = false
		} else if c == 0 {
			// If motor is NOT running, but coverState is still reported as moving (0),
			// map it to 4 (Unknown) so it is NOT considered moving in Go!
			c = 4
		} else if coverStateUnreliable {
			// The firmware's own cover-state classification is known-unreliable after
			// an interrupted move (see coverStateUnreliable's doc comment) until a
			// fresh Open/Close/auto-calibrate resolves it -- don't let an ordinary
			// >S# poll's raw value override that with something that might be wrong.
			c = 4
		}
		setCoverStateLocked(c)
	}

	if frame.ClosedAngle != nil {
		currentMotorAngle = *frame.ClosedAngle
	}
	if frame.OpenAngle != nil {
		openMotorAngle = *frame.OpenAngle
	}
	if frame.DewRaw != nil {
		// Unconfirmed field. Logged only, at DEBUG level, so it can be correlated
		// against the panel's physical heater control while testing; never acted on.
		logger.Debug("Status: unconfirmed Pro dew/heater-adjacent field = %d (read-only)", *frame.DewRaw)
	}
}

func parseMotorInfo(resp string) {
	// Expected format from firmware: *MSt{running}Ca{closedAngle}Oa{openAngle}#
	// Example: *MSt0Ca31Oa9424#
	// Ca = angle set as Closed position
	// Oa = angle set as Open position
	// NOTE: These are raw stepper encoder values, not degrees.

	caIdx := strings.Index(resp, "Ca")
	if caIdx == -1 {
		return
	}

	oaIdx := strings.Index(resp, "Oa")
	if oaIdx == -1 {
		return
	}

	caValStr := resp[caIdx+2 : oaIdx]
	caVal, err := strconv.Atoi(caValStr)
	if err != nil {
		return
	}

	hashIdx := strings.Index(resp, "#")
	if hashIdx == -1 || hashIdx <= oaIdx+2 {
		return
	}
	oaValStr := resp[oaIdx+2 : hashIdx]
	oaVal, err := strconv.Atoi(oaValStr)
	if err != nil {
		return
	}

	stateMutex.Lock()
	currentMotorAngle = caVal
	openMotorAngle = oaVal
	stateMutex.Unlock()
}

func periodicPoller(initDone chan struct{}) {
	<-initDone
	counter := 0
	for {
		if IsConnected() {
			stateMutex.RLock()
			inProgress := openCloseInProgress || brightnessChangeInProgress
			stateMutex.RUnlock()

			if !inProgress {
				SendCommand(getActiveProtocol().FormatCommand('S', 0, false), false, 0)
				counter++
				if counter >= 5 { // Every 10 seconds
					FetchCalibrationStatus()
					counter = 0
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
}

func drainInputBuffer(port serial.Port) {
	port.SetReadTimeout(50 * time.Millisecond)
	buf := make([]byte, 1024)
	for {
		n, err := port.Read(buf)
		if err != nil || n == 0 {
			break
		}
	}
}

// readLine reads bytes from port until it sees terminator, or times out. If onIdle is
// non-nil, it's invoked every time an individual read sub-attempt comes back empty
// (roughly every idleReadInterval) without losing or resetting any bytes already
// accumulated toward the frame in progress -- ProcessCommands' motion-command wait uses
// this to notice a pending HaltCover request and inject >K# onto the wire mid-wait,
// from the same goroutine that already owns the port for this transaction, without
// corrupting a frame that happens to be arriving in multiple chunks right then.
func readLine(port serial.Port, terminator byte, timeout time.Duration, onIdle func()) (string, error) {
	const idleReadInterval = 200 * time.Millisecond
	readTimeout := idleReadInterval
	if timeout < readTimeout {
		readTimeout = timeout
	}
	port.SetReadTimeout(readTimeout)
	var result []byte
	buf := make([]byte, 256)
	start := time.Now()

	for {
		if time.Since(start) > timeout {
			return "", errors.New("read timeout")
		}

		n, err := port.Read(buf)
		if err != nil {
			return "", err
		}
		if n > 0 {
			for i := 0; i < n; i++ {
				if buf[i] == terminator {
					result = append(result, buf[i])
					return string(result), nil
				}
				result = append(result, buf[i])
				if len(result) > 2048 {
					return "", errors.New("read buffer overflow: response line too long without delimiter")
				}
			}
		} else if onIdle != nil {
			onIdle()
		}
	}
}

// identifyOne sends proto's handshake command on an already-open port and reports
// whether the device answered with proto's expected reply.
func identifyOne(p serial.Port, proto Protocol, timeout time.Duration) bool {
	drainInputBuffer(p)
	p.SetReadTimeout(timeout)
	n, err := p.Write([]byte(proto.HandshakeCommand()))
	logger.Debug("identifyOne(%s): wrote %q (n=%d err=%v)", proto.Name(), proto.HandshakeCommand(), n, err)
	if err != nil {
		return false
	}
	line, err := readLine(p, proto.Terminator(), timeout, nil)
	logger.Debug("identifyOne(%s): readLine -> %q err=%v", proto.Name(), line, err)
	if err != nil {
		return false
	}
	ok := proto.IsHandshakeReply(strings.TrimSpace(line))
	logger.Debug("identifyOne(%s): IsHandshakeReply(%q) = %v", proto.Name(), strings.TrimSpace(line), ok)
	return ok
}

// identifyPort tries to determine which Gemini Flat Panel protocol revision is on the
// other end of an already-open port. If preferred names a known revision, only that one
// is tried; otherwise every known revision is tried in probe order (knownProtocols,
// Rev2→Lite→Pro). Returns nil if none matched.
func identifyPort(p serial.Port, preferred string, perAttemptTimeout time.Duration) Protocol {
	candidates := knownProtocols
	if pr := protocolByConfigKey(preferred); pr != nil {
		candidates = []Protocol{pr}
	}
	for _, proto := range candidates {
		if identifyOne(p, proto, perAttemptTimeout) {
			return proto
		}
	}
	return nil
}

// ManageConnection is a background task that ensures the device stays connected.
func ManageConnection(initDone chan struct{}) {
	<-initDone

	for {
		select {
		case <-reconnectTrigger:
			// immediate reconnect triggered
		case <-time.After(5 * time.Second):
			// periodic check
		}

		portMutex.Lock()
		if reconnectPaused {
			portMutex.Unlock()
			continue
		}

		if panelPort == nil {
			conf := config.Get()
			targetPort := conf.SerialPortName

			// Deliberately does NOT clear SerialPortName just because an attempt
			// failed -- it used to, which meant a single unlucky-timed failure (e.g.
			// right after power arrives but before the panel's MCU is fully ready)
			// left nothing configured to retry at all. reconnect() resets the device
			// on every call (see its own doc comment), so the known port deserves a
			// fresh, fully-capable attempt every cycle, forever -- not just once.
			// config.UpdateSerialPort() still runs (inside reconnect() itself) on
			// genuine success, so the saved port always reflects whatever actually
			// last worked.
			var success bool
			if targetPort != "" {
				success = reconnect(targetPort)
			}

			if success {
				serialConnectedMu.Lock()
				failedReconnectCount = 0
				serialConnectedMu.Unlock()
			} else {
				maxRetries := config.Get().MaxConnectionRetries
				if maxRetries > 0 {
					serialConnectedMu.Lock()
					failedReconnectCount++
					count := failedReconnectCount
					serialConnectedMu.Unlock()

					if count > maxRetries {
						setSerialDisconnected()
					} else {
						logger.Warn("Reconnection attempt %d/%d failed. Retrying in background...", count, maxRetries)
						retryInterval := config.Get().ConnectionRetryInterval
						if retryInterval <= 0 {
							retryInterval = 1000
						}
						go func() {
							time.Sleep(time.Duration(retryInterval) * time.Millisecond)
							TriggerImmediateReconnect()
						}()
					}
				} else {
					setSerialDisconnected()
				}
			}
		}
		portMutex.Unlock()
	}
}

// probeSettleDelay/probePerAttemptTimeout size reconnect()'s post-open settle delay
// and per-protocol handshake timeout. Bumped from an earlier, tighter 1500ms/1500ms
// budget after a real, working panel needed the full 2s/3s to reliably identify
// itself (confirmed live) -- the extra time was load-bearing, not padding.
const (
	probeSettleDelay       = 2 * time.Second
	probePerAttemptTimeout = 3 * time.Second
)

// AvailablePort is one serial port as reported by the OS/driver, for the Settings UI's
// port-selection dropdown. Purely descriptive -- listing ports never opens or resets a
// USB serial adapter (see SerialPortName's doc comment for why autodetect, which
// necessarily would, was removed). On Linux, go.bug.st/serial does briefly open and
// close onboard /dev/ttyS* and /dev/ttyHS* entries while listing, to skip placeholder
// ports -- those are built-in UARTs, never a USB adapter.
type AvailablePort struct {
	Name  string `json:"name"`
	IsUSB bool   `json:"isUsb"`
	VID   string `json:"vid"`
	PID   string `json:"pid"`
}

// ListAvailablePorts enumerates serial ports for the Settings UI to offer as choices.
//
// If the detailed enumeration fails (on Linux, a single unreadable sysfs entry makes
// go.bug.st/serial's enumerator abort the whole list instead of returning a partial
// one), falls back to the plain port-name list without USB details, so the dropdown
// stays usable rather than empty.
func ListAvailablePorts() ([]AvailablePort, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		logger.Warn("ListAvailablePorts: detailed port enumeration failed (%v), falling back to plain port names.", err)
		names, namesErr := serial.GetPortsList()
		if namesErr != nil {
			return nil, namesErr
		}
		result := make([]AvailablePort, 0, len(names))
		for _, name := range names {
			result = append(result, AvailablePort{Name: name})
		}
		return result, nil
	}
	result := make([]AvailablePort, 0, len(ports))
	for _, port := range ports {
		result = append(result, AvailablePort{Name: port.Name, IsUSB: port.IsUSB, VID: port.VID, PID: port.PID})
	}
	return result, nil
}

func Reconnect(portName string) {
	portMutex.Lock()
	defer portMutex.Unlock()
	reconnect(portName)
}

func closePort() {
	stateMutex.Lock()
	deviceReadyState = 0
	stateMutex.Unlock()

	if panelPort != nil {
		panelPort.Close()
		panelPort = nil
	}
}

// reconnect opens newPortName and identifies which protocol revision is on the other
// end, honoring the configured panelRevision, failing the connection attempt if
// nothing answers.
//
// Deliberately opens with default (unsuppressed) DTR/RTS: a panel wedged by
// connecting USB before its main power never recovers with DTR held low, no matter how
// many times or how long it's retried, but does recover from an open that resets it
// (exactly what the vendor app's own connection does on every open, and confirmed live
// as the actual reason a blind autodetect scan -- since removed -- could never find a
// real, working panel that this function found instantly: autodetect deliberately held
// DTR/RTS low to avoid resetting an unrelated device on some other candidate port it
// didn't yet know the identity of). This function is only ever called with a known/
// pinned/previously-successful port, so resetting it is safe and necessary. A firmware
// reset here costs nothing but the settle delay below -- Open/Close and auto-calibrate
// calibration survive a real power cycle.
func reconnect(newPortName string) bool {
	closePort()

	if newPortName == "" {
		return false
	}

	p, err := serial.Open(newPortName, &serial.Mode{BaudRate: 9600})
	if err != nil {
		logger.Error("reconnect: Failed to open port %s: %v", newPortName, err)
		return false
	}

	logger.Info("reconnect: Settle delay, waiting %s for device to boot...", probeSettleDelay)
	time.Sleep(probeSettleDelay)
	drainInputBuffer(p)

	preferred := config.Get().PanelRevision
	proto := identifyPort(p, preferred, probePerAttemptTimeout)
	if proto == nil {
		logger.Warn("reconnect: device on %s did not answer any known Gemini Flat Panel handshake (panelRevision=%q)", newPortName, preferred)
		p.Close()
		return false
	}

	stateMutex.Lock()
	activeProtocol = proto
	stateMutex.Unlock()
	logger.Info("reconnect: identified device on %s as %s", newPortName, proto.Name())

	panelPort = p
	serialConnectedMu.Lock()
	serialConnected = true
	serialConnectedMu.Unlock()

	config.UpdateSerialPort(newPortName)

	// != Connected rather than == Disconnected: lastSentStatus can also be Idle (a
	// prior deliberate connect-on-demand release) -- a reconnect from there needs the
	// same "Reconnected" announcement and post-connect init below just as much as one
	// from an actual Disconnected does.
	if lastSentStatus != events.Connected {
		select {
		case events.ComPortStatusChan <- events.Connected:
			lastSentStatus = events.Connected
		default:
		}

		go func() {
			time.Sleep(2 * time.Second)
			SetLowBankAndZeroBrightness()
			FetchFirmwareVersion()
			FetchCalibrationStatus()
			SyncBeep()
			SendCommand(getActiveProtocol().FormatCommand('S', 0, false), true, 0)
		}()
	}
	return true
}

func handleDisconnect() {
	closePort()

	maxRetries := config.Get().MaxConnectionRetries
	if maxRetries <= 0 {
		setSerialDisconnected()
	} else {
		serialConnectedMu.Lock()
		failedReconnectCount++
		count := failedReconnectCount
		serialConnectedMu.Unlock()

		if count > maxRetries {
			setSerialDisconnected()
		} else {
			logger.Warn("Serial connection lost. Retrying connection in background (attempt %d/%d) before notifying Alpaca clients.", count, maxRetries)
			go TriggerImmediateReconnect()
		}
	}
}

// setSerialDisconnected marks the port not-connected and announces events.Disconnected
// -- for an actual/unexpected loss, where a "check your device and cable"-style
// notification is warranted. See setSerialIdle for the deliberate-release equivalent.
func setSerialDisconnected() {
	setNotConnected(events.Disconnected)
}

// setSerialIdle marks the port not-connected the same way setSerialDisconnected does,
// but announces events.Idle instead of events.Disconnected -- for a deliberate
// connect-on-demand release (ReleasePort), where nothing is actually wrong and a "check
// your cable" notification would be misleading. See internal/systray's listener, which
// treats the two differently.
func setSerialIdle() {
	setNotConnected(events.Idle)
}

func setNotConnected(status events.ComPortStatus) {
	serialConnectedMu.Lock()
	wasConnected := serialConnected
	serialConnected = false
	failedReconnectCount = 0
	serialConnectedMu.Unlock()

	if wasConnected || lastSentStatus == events.Connected {
		select {
		case events.ComPortStatusChan <- status:
			lastSentStatus = status
		default:
		}
	}
}

// ReleasePort deliberately releases the port and pauses the background loop -- used by
// connect-on-demand mode (manual Disconnect, and ScheduleIdleRelease's grace-period
// timer). Deliberately does NOT go through handleDisconnect(): that path is for an
// actual/unexpected loss (retry-count bookkeeping, a "connection lost, retrying" WARN
// log, events.Disconnected) -- none of which apply here, where nothing is wrong and
// nothing should retry. Just closes the port and marks it idle.
// TurnOffLightIfOn turns the light off (brightness to 0) if it's currently on. Used
// both by an Alpaca client's own Connected=false (regardless of connect-on-demand
// mode -- once no client is actively connected, leaving the panel lit indefinitely
// with nothing controlling or monitoring it is exactly what this guards against; see
// HandleConnected) and by ReleasePort itself before actually releasing the port (the
// manual-Disconnect-button path, which doesn't go through HandleConnected's
// Connected=false branch at all). Safe to call when the light is already off -- a
// no-op check, no command sent.
func TurnOffLightIfOn() {
	stateMutex.RLock()
	lightOn := lightStatus == 1
	stateMutex.RUnlock()
	if lightOn {
		if err := SetBrightness(0); err != nil {
			// Not fatal -- e.g. the device may have already become unreachable.
			logger.Warn("TurnOffLightIfOn: failed to turn off the light: %v", err)
		}
	}
}

func ReleasePort() error {
	// Must happen before portMutex is taken below: SetBrightness's commands go
	// through SendCommand -> the command queue -> ProcessCommands, which itself
	// needs portMutex to actually write them -- calling it while already holding
	// portMutex here would deadlock against that.
	TurnOffLightIfOn()

	portMutex.Lock()
	defer portMutex.Unlock()
	reconnectPaused = true
	if panelPort != nil {
		logger.Info("Releasing serial port (connect-on-demand) -- going idle until asked again.")
	}
	closePort()
	setSerialIdle()
	return nil
}

func ResumeReconnect() {
	portMutex.Lock()
	defer portMutex.Unlock()
	reconnectPaused = false
}

func IsReconnectPaused() bool {
	portMutex.Lock()
	defer portMutex.Unlock()
	return reconnectPaused
}

// idleReleaseTimer backs ScheduleIdleRelease/CancelIdleRelease -- the connect-on-demand
// mode's "hold the port for a grace period after a client disconnects, in case it (or
// another client) reconnects shortly after" mechanism, e.g. an equipment profile switch
// in the client software. Separate mutex from portMutex/stateMutex/serialConnectedMu:
// this only ever guards the timer handle itself, never blocks on any of them.
var (
	idleReleaseTimer *time.Timer
	idleReleaseMu    sync.Mutex
)

// ScheduleIdleRelease arms (or re-arms, replacing any existing one) a timer that
// releases the port via ReleasePort after the given duration, unless CancelIdleRelease
// is called first (e.g. the client reconnects within the window).
func ScheduleIdleRelease(after time.Duration) {
	idleReleaseMu.Lock()
	defer idleReleaseMu.Unlock()
	if idleReleaseTimer != nil {
		idleReleaseTimer.Stop()
	}
	idleReleaseTimer = time.AfterFunc(after, func() {
		logger.Info("Connect-on-demand: idle grace period elapsed with nothing reconnecting, releasing the port.")
		ReleasePort()
	})
}

// CancelIdleRelease stops a pending ScheduleIdleRelease timer, if any. A no-op if none
// is pending.
func CancelIdleRelease() {
	idleReleaseMu.Lock()
	defer idleReleaseMu.Unlock()
	if idleReleaseTimer != nil {
		idleReleaseTimer.Stop()
		idleReleaseTimer = nil
	}
}

// ConnectOnDemand actively attempts a connection and waits up to timeout for it to
// succeed. Used by connect-on-demand mode (config.SerialConnectOnDemand) from two
// callers: HandleConnected, when an Alpaca client PUTs Connected=true while not
// already connected, and the dashboard's manual Connect button
// (HandleManualConnect) -- both just call this and report the result, no separate
// connection logic of their own.
//
// Reuses the existing ManageConnection background loop rather than duplicating its
// reconnect() logic: clearing reconnectPaused and nudging
// TriggerImmediateReconnect lets that loop do the actual work (including its own
// retry burst on failure), this just resumes it and polls IsConnected for up to
// timeout. On failure, re-pauses via ReleasePort rather than leaving the loop
// retrying forever in the background -- otherwise a single failed on-demand attempt
// would silently turn connect-on-demand into today's always-on behavior from then on.
func ConnectOnDemand(timeout time.Duration) bool {
	CancelIdleRelease()
	ResumeReconnect()

	if IsConnected() {
		return true
	}
	TriggerImmediateReconnect()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if IsConnected() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}

	if IsConnected() {
		return true
	}
	ReleasePort()
	return false
}

func FetchFirmwareVersion() {
	proto := getActiveProtocol()
	if !proto.SupportsFirmwareQuery() {
		return
	}
	resp, err := SendCommand(proto.FormatCommand('V', 0, false), false, 0)
	if err == nil && len(resp) >= 4 && strings.HasPrefix(resp, "*V") {
		vStr := resp[2 : len(resp)-1]
		firmwareVersionMu.Lock()
		firmwareVersion = vStr
		firmwareVersionMu.Unlock()
	}
}
