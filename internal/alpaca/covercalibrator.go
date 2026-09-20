package alpaca

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"geminiflatpanel/internal/config"
	"geminiflatpanel/internal/logger"
	"geminiflatpanel/internal/serial"
)

// AlpacaDescription defines the structure for the management/v1/description endpoint.
type AlpacaDescription struct {
	ServerName          string `json:"ServerName"`
	Manufacturer        string `json:"Manufacturer"`
	ManufacturerVersion string `json:"ManufacturerVersion"`
	Location            string `json:"Location"`
}

// AlpacaConfiguredDevice defines the structure for a single device in the management/v1/configureddevices endpoint.
type AlpacaConfiguredDevice struct {
	DeviceName   string `json:"DeviceName"`
	DeviceType   string `json:"DeviceType"`
	DeviceNumber int    `json:"DeviceNumber"`
	UniqueID     string `json:"UniqueID"`
}

// API holds all dependencies for the Alpaca API handlers.
type API struct {
	appVersion      string
	driverConnected atomic.Bool
}

// NewAPI creates a new API instance.
func NewAPI(appVersion string) *API {
	return &API{
		appVersion: appVersion,
	}
}

func (a *API) HandleManagementDescription(w http.ResponseWriter, r *http.Request) {
	description := AlpacaDescription{
		ServerName:          "Gemini Flat Panel Proxy",
		Manufacturer:        "User-Made",
		ManufacturerVersion: a.appVersion,
		Location:            "Observatory",
	}
	ManagementValueResponse(w, r, description)
}

func HandleManagementConfiguredDevices(w http.ResponseWriter, r *http.Request) {
	devices := []AlpacaConfiguredDevice{
		{
			DeviceName:   "Gemini Flat Panel",
			DeviceType:   "CoverCalibrator",
			DeviceNumber: 0,
			UniqueID:     "9408b4eb-5527-4ec9-8db2-2df02e1b8b64", // Static GUID
		},
	}
	// Only list the Switch (dew-heater) device when the currently-connected panel
	// actually supports it (Pro) — Lite/Rev2 have no heater at all, so there's
	// nothing useful behind it there. SupportsHeater() already defaults to false before
	// any panel is identified, so it's correctly absent during that startup window too.
	// The /api/v1/switch/0/... routes themselves stay registered regardless (see
	// switchdevice.go) as a safety net for a client with a stale reference from before a
	// panel swap; this only controls whether new clients discover it in the first place.
	if serial.SupportsHeater() {
		devices = append(devices, AlpacaConfiguredDevice{
			DeviceName:   "Gemini Flat Panel Dew Heater",
			DeviceType:   "Switch",
			DeviceNumber: 0,
			UniqueID:     "b2f7a6e1-3c4d-4e8a-9f2b-7a1e5c6d8b30", // Static GUID
		})
	}
	ManagementValueResponse(w, r, devices)
}

func HandleManagementApiVersions(w http.ResponseWriter, r *http.Request) {
	response := struct {
		Value               []int  `json:"Value"`
		ClientTransactionID uint32 `json:"ClientTransactionID"`
		ServerTransactionID uint32 `json:"ServerTransactionID"`
		ErrorNumber         int    `json:"ErrorNumber"`
		ErrorMessage        string `json:"ErrorMessage"`
	}{
		Value: []int{1},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// --- Common Device Handlers ---

func (a *API) HandleDeviceDescription(w http.ResponseWriter, r *http.Request) {
	StringResponse(w, r, "Gemini Flat Panel Proxy Driver")
}

func (a *API) HandleDriverInfo(w http.ResponseWriter, r *http.Request) {
	StringResponse(w, r, "A Go-based ASCOM Alpaca proxy driver for the Gemini Flat Panel.")
}

func (a *API) HandleDriverVersion(w http.ResponseWriter, r *http.Request) {
	StringResponse(w, r, a.appVersion)
}

func (a *API) HandleInterfaceVersion(w http.ResponseWriter, r *http.Request) {
	IntResponse(w, r, 1)
}

func (a *API) HandleSupportedActions(w http.ResponseWriter, r *http.Request) {
	StringListResponse(w, r, []string{"SetHeaterPower", "GetHeaterPower"})
}

// HandleAction implements the generic ASCOM Alpaca Action mechanism (Action +
// Parameters form fields, a single string Value in the response) — not previously
// implemented at all in this project. Currently exposes the same dew-heater control
// already available via the custom /api/custom/heater endpoint and the new Switch
// device, so a client that's only connected to this CoverCalibrator device (and hasn't
// added the separate Switch device) can still reach it. Action names match the
// convention AlpacaBridge uses for its own camera-heater actions.
func (a *API) HandleAction(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if r.Method != "PUT" {
		ErrorResponse(w, r, http.StatusOK, 0x400, "Method not allowed")
		return
	}

	action, ok, err := GetFormValueStrict(r, "Action")
	if err != nil {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, err.Error())
		return
	}
	if !ok {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, "Missing Action parameter")
		return
	}

	switch {
	case strings.EqualFold(action, "SetHeaterPower"):
		params, _, _ := GetFormValueStrict(r, "Parameters")
		percent, err := strconv.Atoi(strings.TrimSpace(params))
		if err != nil {
			ErrorResponse(w, r, http.StatusOK, 0x401, fmt.Sprintf("Invalid Parameters for SetHeaterPower: '%s' (expected an integer 0-100)", params))
			return
		}
		if err := serial.SetHeaterPower(percent); err != nil {
			ErrorResponse(w, r, http.StatusOK, 0x500, err.Error())
			return
		}
		StringResponse(w, r, strconv.Itoa(serial.GetHeaterPower()))
	case strings.EqualFold(action, "GetHeaterPower"):
		StringResponse(w, r, strconv.Itoa(serial.GetHeaterPower()))
	default:
		ErrorResponse(w, r, http.StatusOK, 0x40C, fmt.Sprintf("Action '%s' is not implemented", action))
	}
}

func (a *API) HandleDeviceName(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		StringResponse(w, r, name)
	}
}

func (a *API) HandleConnected(w http.ResponseWriter, r *http.Request) {
	if r.Method == "PUT" {
		connectedStr, ok, err := GetFormValueStrict(r, "Connected")
		if err != nil {
			ErrorResponse(w, r, http.StatusBadRequest, 0x400, err.Error())
			return
		}
		if !ok {
			ErrorResponse(w, r, http.StatusBadRequest, 0x400, "Missing Connected parameter for PUT request")
			return
		}
		connected, err := strconv.ParseBool(connectedStr)
		if err != nil {
			ErrorResponse(w, r, http.StatusBadRequest, 0x400, fmt.Sprintf("Invalid value for Connected: '%s'", connectedStr))
			return
		}

		if connected {
			// Only latch driverConnected=true once the serial link is actually verified —
			// storing it unconditionally before this check let a failed connect attempt
			// (client PUTs Connected=true while disconnected, gets the 0x40B error below)
			// silently turn into a reported "Connected=true" later if the background
			// ManageConnection loop reconnected on its own, even though the client's own
			// connect request never succeeded and it never retried.
			if !serial.IsConnected() {
				if config.Get().SerialConnectOnDemand {
					// Connect-on-demand: the port is dormant until something actually asks
					// for it — this request IS that ask. Actively try (up to
					// connectOnDemandTimeout) instead of immediately erroring, matching
					// what a real driver's own hardware handshake would do. See
					// serial.ConnectOnDemand's own doc comment.
					serial.ConnectOnDemand(serial.ConnectOnDemandTimeout)
				}
				if !serial.IsConnected() {
					a.driverConnected.Store(false)
					ErrorResponse(w, r, http.StatusOK, 0x40B, "Device not connected via serial.")
					return
				}
			}
			// A pending idle-release (see the `else` branch below) shouldn't fire now
			// that a client has (re)connected — a no-op when connect-on-demand is off
			// or nothing was pending.
			serial.CancelIdleRelease()
			a.driverConnected.Store(true)
			// Initialize state asynchronously to avoid blocking HTTP response if cover movement is in progress
			go serial.SetLowBankAndZeroBrightness()
		} else {
			a.driverConnected.Store(false)
			// Once no Alpaca client is actively connected, don't leave the light on
			// indefinitely with nothing controlling or monitoring it -- regardless of
			// connect-on-demand mode (which additionally schedules releasing the port
			// itself, below). Same fire-and-forget style as the connect side's own
			// SetLowBankAndZeroBrightness call above.
			go serial.TurnOffLightIfOn()
			if config.Get().SerialConnectOnDemand {
				// Don't release immediately — hold the port for a grace period (config's
				// IdleReleaseTimeoutSeconds, default 60s) in case this (or another)
				// client reconnects shortly after, e.g. an equipment profile switch in
				// the client software, rather than paying for a full reconnect
				// handshake every time. Released via ReleasePort if nothing reconnects
				// within the window.
				//
				// Known, pre-existing simplification this inherits: driverConnected is
				// one shared flag for both the CoverCalibrator and Switch facades, not
				// tracked per-client. Two different Alpaca clients each connected to a
				// different facade would have one's disconnect start this timer even
				// though the other is still logically connected — rare in practice (most
				// setups use one client for both), and fixing it properly needs
				// per-client connection tracking, out of scope here.
				serial.ScheduleIdleRelease(time.Duration(config.Get().IdleReleaseTimeoutSeconds) * time.Second)
			}
		}

		EmptyResponse(w, r)
		return
	}

	BoolResponse(w, r, a.driverConnected.Load() && serial.IsConnected())
}

// --- CoverCalibrator Handlers ---

func (a *API) checkConnection(w http.ResponseWriter, r *http.Request) bool {
	if !serial.IsConnected() {
		ErrorResponse(w, r, http.StatusOK, 0x40B, "Device not connected via serial")
		return false
	}
	return true
}

func (a *API) HandleCoverCalibratorMaxBrightness(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	IntResponse(w, r, effectiveMaxBrightness())
}

// effectiveMaxBrightness returns the actually reachable max brightness for the
// connected panel. Thin wrapper around serial.EffectiveMaxBrightness — the shared rule
// also used by SetBrightness in internal/serial/serial.go, so this endpoint and
// SetBrightness's own clamp can't silently disagree.
func effectiveMaxBrightness() int {
	return serial.EffectiveMaxBrightness(config.Get().MaxBrightness, serial.SupportsBrightnessMode())
}

func (a *API) HandleCoverCalibratorState(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	// ASCOM CalibratorState: 0=NotPresent, 1=Off, 2=NotReady, 3=Ready, 4=Unknown, 5=Error
	state := serial.GetCalibratorState()
	IntResponse(w, r, state)
}

func (a *API) HandleCoverCoverState(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	// ASCOM CoverState: 0=NotPresent, 1=Closed, 2=Moving, 3=Open, 4=Unknown, 5=Error
	state := serial.GetCoverState()
	IntResponse(w, r, state)
}

func (a *API) HandleCoverCalibratorBrightness(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}

	if r.Method == "PUT" {
		valStr, ok, err := GetFormValueStrict(r, "Brightness")
		if err != nil {
			ErrorResponse(w, r, http.StatusBadRequest, 0x400, err.Error())
			return
		}
		if !ok {
			ErrorResponse(w, r, http.StatusBadRequest, 0x400, "Missing Brightness parameter")
			return
		}
		logger.Debug("Alpaca CalibratorBrightness: Received Brightness parameter = %s", valStr)
		brightness, err := strconv.Atoi(valStr)
		if err != nil || brightness < 0 || brightness > effectiveMaxBrightness() {
			ErrorResponse(w, r, http.StatusOK, 0x401, fmt.Sprintf("Invalid brightness value. Must be 0-%d", effectiveMaxBrightness()))
			return
		}

		if serial.GetCalibratorState() == 1 { // 1 = Off
			ErrorResponse(w, r, http.StatusOK, 0x40B, "Cannot set brightness when calibrator is Off")
			return
		}

		if serial.ShouldBlockLightForOpenCover(brightness, config.Get().BlockLightWhenOpen, serial.SupportsCover(), serial.GetCoverState()) {
			ErrorResponse(w, r, http.StatusOK, 0x40B, "Cannot turn on calibrator when cover is not closed")
			return
		}

		err = serial.SetBrightness(brightness)
		if err != nil {
			ErrorResponse(w, r, http.StatusOK, 0x500, err.Error())
			return
		}

		EmptyResponse(w, r)
		return
	}

	IntResponse(w, r, serial.GetCurrentBrightness())
}

// noCoverError is the Alpaca response for Open/Close/Halt on a panel with no motorized
// cover (Lite) — CoverState reports NotPresent (0), so per the ASCOM spec these methods
// must throw MethodNotImplemented rather than attempt a motor command. Matches
// AlpacaBridge's own Lite driver behavior.
func (a *API) noCoverError(w http.ResponseWriter, r *http.Request) {
	ErrorResponse(w, r, http.StatusOK, 0x400, "This panel has no motorized cover (CoverState is NotPresent).")
}

func (a *API) HandleCoverOpenCover(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if r.Method != "PUT" {
		ErrorResponse(w, r, http.StatusOK, 0x400, "Method not allowed")
		return
	}
	if !serial.SupportsCover() {
		a.noCoverError(w, r)
		return
	}
	go serial.OpenCover()
	EmptyResponse(w, r)
}

func (a *API) HandleCoverCloseCover(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if r.Method != "PUT" {
		ErrorResponse(w, r, http.StatusOK, 0x400, "Method not allowed")
		return
	}
	if !serial.SupportsCover() {
		a.noCoverError(w, r)
		return
	}
	go serial.CloseCover()
	EmptyResponse(w, r)
}

func (a *API) HandleCoverHaltCover(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if r.Method != "PUT" {
		ErrorResponse(w, r, http.StatusOK, 0x400, "Method not allowed")
		return
	}
	if !serial.SupportsCover() {
		a.noCoverError(w, r)
		return
	}
	// Per the ASCOM spec, HaltCover must throw MethodNotImplementedException when cover
	// movement cannot be interrupted by the hardware, rather than silently "succeeding"
	// while the motor keeps moving. Pro's firmware genuinely can (>K#, see
	// serial.HaltCover's doc comment); the other revisions' command dispatch tables
	// have no stop/halt opcode at all, so they keep this response unchanged.
	if !serial.SupportsHalt() {
		ErrorResponse(w, r, http.StatusOK, 0x400, "HaltCover is not implemented: this panel's firmware has no command to interrupt an in-progress cover move.")
		return
	}

	if err := serial.HaltCover(); err != nil {
		ErrorResponse(w, r, http.StatusOK, 0x500, err.Error())
		return
	}
	EmptyResponse(w, r)
}

func (a *API) HandleCoverCalibratorOn(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if r.Method != "PUT" {
		ErrorResponse(w, r, http.StatusOK, 0x400, "Method not allowed")
		return
	}

	valStr, ok, err := GetFormValueStrict(r, "Brightness")
	if err != nil {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, err.Error())
		return
	}
	if !ok {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, "Missing Brightness parameter")
		return
	}
	logger.Debug("Alpaca CalibratorOn: Received Brightness parameter = %s", valStr)
	brightness, err := strconv.Atoi(valStr)
	if err != nil || brightness < 0 || brightness > effectiveMaxBrightness() {
		ErrorResponse(w, r, http.StatusOK, 0x401, fmt.Sprintf("Invalid brightness value. Must be 0-%d", effectiveMaxBrightness()))
		return
	}

	if serial.ShouldBlockLightForOpenCover(brightness, config.Get().BlockLightWhenOpen, serial.SupportsCover(), serial.GetCoverState()) {
		ErrorResponse(w, r, http.StatusOK, 0x40B, "Cannot turn on calibrator when cover is not closed")
		return
	}

	serial.SetCalibratorOnState(true)

	err = serial.SetBrightness(brightness)
	if err != nil {
		ErrorResponse(w, r, http.StatusOK, 0x500, err.Error())
		return
	}

	EmptyResponse(w, r)
}

func (a *API) HandleCoverCalibratorOff(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if r.Method != "PUT" {
		ErrorResponse(w, r, http.StatusOK, 0x400, "Method not allowed")
		return
	}

	serial.SetCalibratorOnState(false)

	err := serial.SetBrightness(0)
	if err != nil {
		ErrorResponse(w, r, http.StatusOK, 0x500, err.Error())
		return
	}

	EmptyResponse(w, r)
}
