package handlers

import (
	"encoding/json"
	"geminiflatpanel/internal/config"
	"geminiflatpanel/internal/dewcontrol"
	"geminiflatpanel/internal/dewheaterswitch"
	"geminiflatpanel/internal/logger"
	"geminiflatpanel/internal/obsconditions"
	"geminiflatpanel/internal/serial"
	"net/http"
	"os"
)

// HandleGetLiveStatus returns the live parsed status of the device to the frontend.
// Dew-control status is merged in here rather than living inside
// serial.GetLiveStatus() itself, since internal/dewcontrol already depends on
// internal/serial (for SupportsHeater/SetHeaterPower) — merging the other way around
// would be a circular import.
func HandleGetLiveStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := serial.GetLiveStatus()
	cfg := config.Get()
	status["auto_dew_control_enabled"] = cfg.EnableAutoDewControl
	status["dew_control_backend"] = cfg.DewHeaterBackend
	status["dew_control_last_source"] = dewcontrol.LastSource()
	if temp, dewPoint, ok := dewcontrol.LastReading(); ok {
		status["dew_control_temperature"] = temp
		status["dew_control_dew_point"] = dewPoint
	}
	if cfg.DewHeaterBackend == "external" {
		if cfg.DewHeaterSwitchIsRheostat {
			status["dew_control_external_percent"] = dewcontrol.LastExternalPercent()
		} else {
			status["dew_control_external_on"] = dewcontrol.LastExternalOn()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// HandleJogMotor jogs the motor by a specific angle (+/-)
func HandleJogMotor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !serial.SupportsCover() {
		http.Error(w, "This panel has no motorized cover", http.StatusConflict)
		return
	}

	var payload struct {
		Angle int `json:"angle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err := serial.JogMotor(payload.Angle)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleSetHeaterPower sets the dew heater's output to a percentage (0-100),
// dispatching on the configured backend (built-in Pro heater, or an external Alpaca
// switch's rheostat channel) via dewcontrol.SetManualHeaterPower -- see its own doc
// comment for the backend dispatch rules and why a boolean on/off channel is
// rejected here.
func HandleSetHeaterPower(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Percent int `json:"percent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := dewcontrol.SetManualHeaterPower(payload.Percent); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleSetClosedPosition saves the current position as the CLOSED state
func HandleSetClosedPosition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !serial.SupportsCover() {
		http.Error(w, "This panel has no motorized cover", http.StatusConflict)
		return
	}

	err := serial.SetClosedPosition()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logger.Info("Set Closed Position command sent")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleSetOpenedPosition saves the current position as the OPENED state
func HandleSetOpenedPosition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !serial.SupportsCover() {
		http.Error(w, "This panel has no motorized cover", http.StatusConflict)
		return
	}

	err := serial.SetOpenedPosition()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logger.Info("Set Opened Position command sent")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleAutoCalibrateClosed drives the motor to its physical closed hard-stop and
// records the measured angle — an automatic alternative to HandleSetClosedPosition.
// Experimental (Pro only) — see serial.SupportsAutoCalibrate's doc comment in
// protocol_pro.go. Takes ~22s+ on real hardware; the request blocks for the duration.
func HandleAutoCalibrateClosed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !serial.SupportsAutoCalibrate() {
		http.Error(w, "This panel has no automatic calibration", http.StatusConflict)
		return
	}

	if err := serial.AutoCalibrateClosed(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logger.Info("Auto-calibrate closed position command sent")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleAutoCalibrateOpen is HandleAutoCalibrateClosed's open-position counterpart.
func HandleAutoCalibrateOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !serial.SupportsAutoCalibrate() {
		http.Error(w, "This panel has no automatic calibration", http.StatusConflict)
		return
	}

	if err := serial.AutoCalibrateOpen(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logger.Info("Auto-calibrate open position command sent")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleTestObservingConditions lets the Settings UI verify an ObservingConditions
// URL/device number actually exposes both Temperature and DewPoint before the user
// saves it — directly answering "muss aber prüfen, ob die beiden benötigten
// messdaten vorliegen" (both properties are independently optional per the ASCOM
// spec, so this can only be confirmed against the specific device, not assumed).
func HandleTestObservingConditions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		URL          string `json:"url"`
		DeviceNumber int    `json:"deviceNumber"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reading, err := obsconditions.FetchReading(payload.URL, payload.DeviceNumber)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":          true,
		"temperature": reading.Temperature,
		"dewPoint":    reading.DewPoint,
	})
}

// HandleDiscoverObservingConditions broadcasts an Alpaca discovery request on the
// local network and returns every ObservingConditions device found, so the user can
// pick one in the Dew Control Setup dialog instead of typing a URL by hand.
func HandleDiscoverObservingConditions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	devices, err := obsconditions.Discover()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"devices": devices})
}

// HandleListSerialPorts lists serial ports the OS/driver currently reports, for the
// Settings UI's port-selection dropdown. Pure enumeration -- never opens or resets a
// USB serial adapter (see internal/config's SerialPortName doc comment for why
// autodetect, which necessarily would, was removed).
func HandleListSerialPorts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ports, err := serial.ListAvailablePorts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ports": ports})
}

// HandleDiscoverDewHeaterSwitches broadcasts an Alpaca discovery request on the local
// network and returns every Switch device found, so the user can pick one in the Dew
// Control Setup dialog's "external" backend instead of typing a URL by hand -- same
// pattern as HandleDiscoverObservingConditions.
func HandleDiscoverDewHeaterSwitches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	devices, err := dewheaterswitch.Discover()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"devices": devices})
}

// HandleListSwitchChannels lists the writable channels on a chosen Switch device, so
// the user can pick which one drives the dew heater. Each channel already reports
// whether it's a PWM-style rheostat or a plain on/off outlet (dewheaterswitch.Channel's
// IsRheostat), so the Setup dialog can show the right follow-up fields without a
// separate manual "what kind of switch is this" step.
func HandleListSwitchChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		URL          string `json:"url"`
		DeviceNumber int    `json:"deviceNumber"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	channels, err := dewheaterswitch.ListChannels(payload.URL, payload.DeviceNumber)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "channels": channels})
}

// HandleDownloadLog reads and serves the proxy log file to the user
func HandleDownloadLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	logPath := logger.GetLogFilePath()
	if logPath == "" {
		http.Error(w, "Log file path not found in configuration", http.StatusInternalServerError)
		return
	}

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		http.Error(w, "Log file does not exist", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=geminiflatpanel.log")
	w.Header().Set("Content-Type", "text/plain")
	http.ServeFile(w, r, logPath)
}

// HandleManualConnect actively attempts a serial connection and waits for the result,
// for the dashboard's "Connect" button -- only meaningful (and only shown by the
// frontend) when SerialConnectOnDemand is on, since otherwise the background
// ManageConnection loop already keeps trying on its own. Shares serial.ConnectOnDemand
// with internal/alpaca's HandleConnected (an Alpaca client asking to connect); this is
// just the same action triggered manually instead.
func HandleManualConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	connected := serial.ConnectOnDemand(serial.ConnectOnDemandTimeout)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":        connected,
		"connected": connected,
	})
}

// HandleManualDisconnect releases the serial port on request, for the dashboard's
// "Disconnect" button -- the manual counterpart to HandleManualConnect, for the same
// connect-on-demand mode. Without this, a connection started via the Connect button
// (as opposed to an Alpaca client's Connected=true/false, which already has the
// idle-release grace period) had no way back to dormant at all.
func HandleManualDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serial.CancelIdleRelease()
	err := serial.ReleasePort()

	w.WriteHeader(http.StatusOK)
	resp := map[string]interface{}{"ok": err == nil}
	if err != nil {
		resp["error"] = err.Error()
	}
	json.NewEncoder(w).Encode(resp)
}
