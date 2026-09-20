package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"geminiflatpanel/internal/config"
	"geminiflatpanel/internal/dewcontrol"
	"geminiflatpanel/internal/logger"
	"geminiflatpanel/internal/serial"
)

// SettingsResponse defines the structure for the GET /api/v1/settings response.
type SettingsResponse struct {
	ProxyConfig         *config.ProxyConfig `json:"proxy_config"`
	AvailableIPs        []string            `json:"available_ips"`
	SerialPortConnected bool                `json:"serial_port_connected"`
	ReconnectPaused     bool                `json:"reconnect_paused"`
}

// HandleGetSettings provides the current proxy configuration and available IP addresses.
func HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	conf := config.Get()
	ips, err := getAvailableIPs()
	if err != nil {
		logger.Error("Failed to get available IP addresses: %v", err)
		http.Error(w, "Failed to get IP addresses", http.StatusInternalServerError)
		return
	}

	response := SettingsResponse{
		ProxyConfig:         conf,
		AvailableIPs:        ips,
		SerialPortConnected: serial.IsConnected(),
		ReconnectPaused:     serial.IsReconnectPaused(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandlePostSettings updates the proxy configuration.
func HandlePostSettings(w http.ResponseWriter, r *http.Request) {
	var newConfig config.ProxyConfig
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Basic validation
	if newConfig.NetworkPort <= 0 || newConfig.NetworkPort > 65535 {
		http.Error(w, "Invalid Network Port", http.StatusBadRequest)
		return
	}
	if net.ParseIP(newConfig.ListenAddress) == nil && newConfig.ListenAddress != "0.0.0.0" {
		http.Error(w, "Invalid Listen Address", http.StatusBadRequest)
		return
	}
	if newConfig.MaxBrightness <= 0 || newConfig.MaxBrightness > 10000 {
		http.Error(w, "Invalid Max Brightness (must be 1-10000)", http.StatusBadRequest)
		return
	}
	if newConfig.HighBankStartValue < 0 || newConfig.HighBankStartValue > 254 {
		http.Error(w, "Invalid High Bank Start Value (must be 0-254)", http.StatusBadRequest)
		return
	}
	logLevelUpper := strings.ToUpper(newConfig.LogLevel)
	if logLevelUpper != "DEBUG" && logLevelUpper != "INFO" && logLevelUpper != "WARN" && logLevelUpper != "ERROR" {
		http.Error(w, "Invalid Log Level", http.StatusBadRequest)
		return
	}
	if newConfig.SettleTime < 0 || newConfig.SettleTime > 60000 {
		http.Error(w, "Invalid Settle Time (must be 0-60000 ms)", http.StatusBadRequest)
		return
	}
	if newConfig.MaxConnectionRetries < 0 || newConfig.MaxConnectionRetries > 20 {
		http.Error(w, "Invalid Max Connection Retries (must be 0-20)", http.StatusBadRequest)
		return
	}
	switch newConfig.PanelRevision {
	case "auto", "rev2", "lite", "pro":
		// valid
	default:
		http.Error(w, "Invalid Panel Revision (must be auto, rev2, lite, or pro)", http.StatusBadRequest)
		return
	}
	if newConfig.DewControlIntervalMinutes < 1 || newConfig.DewControlIntervalMinutes > 1440 {
		http.Error(w, "Invalid Dew Control Interval (must be 1-1440 minutes)", http.StatusBadRequest)
		return
	}
	if newConfig.DewControlDeltaFullPower >= newConfig.DewControlDeltaZeroPower {
		http.Error(w, "Invalid Dew Control thresholds (Full-Power delta must be less than Zero-Power delta)", http.StatusBadRequest)
		return
	}
	if newConfig.WeatherLatitude < -90 || newConfig.WeatherLatitude > 90 {
		http.Error(w, "Invalid Weather Latitude (must be -90 to 90)", http.StatusBadRequest)
		return
	}
	if newConfig.WeatherLongitude < -180 || newConfig.WeatherLongitude > 180 {
		http.Error(w, "Invalid Weather Longitude (must be -180 to 180)", http.StatusBadRequest)
		return
	}
	if newConfig.ObservingConditionsDeviceNumber < 0 {
		http.Error(w, "Invalid ObservingConditions Device Number (must be 0 or greater)", http.StatusBadRequest)
		return
	}

	conf := config.Get()
	// Check if serial port settings have changed to trigger a reconnect
	portChanged := conf.SerialPortName != newConfig.SerialPortName
	panelRevisionChanged := conf.PanelRevision != newConfig.PanelRevision
	beepChanged := conf.EnableBeep != newConfig.EnableBeep
	dewControlChanged := conf.EnableAutoDewControl != newConfig.EnableAutoDewControl ||
		conf.DewControlIntervalMinutes != newConfig.DewControlIntervalMinutes ||
		conf.DewControlDeltaFullPower != newConfig.DewControlDeltaFullPower ||
		conf.DewControlDeltaZeroPower != newConfig.DewControlDeltaZeroPower ||
		conf.DewControlOnlyWhenOpen != newConfig.DewControlOnlyWhenOpen ||
		conf.DewControlFailsafeEnabled != newConfig.DewControlFailsafeEnabled ||
		conf.DewControlFailsafePercent != newConfig.DewControlFailsafePercent ||
		conf.ObservingConditionsURL != newConfig.ObservingConditionsURL ||
		conf.ObservingConditionsDeviceNumber != newConfig.ObservingConditionsDeviceNumber ||
		conf.EnableOpenMeteo != newConfig.EnableOpenMeteo ||
		conf.WeatherLatitude != newConfig.WeatherLatitude ||
		conf.WeatherLongitude != newConfig.WeatherLongitude ||
		conf.DewHeaterBackend != newConfig.DewHeaterBackend ||
		conf.DewHeaterSwitchURL != newConfig.DewHeaterSwitchURL ||
		conf.DewHeaterSwitchDeviceNumber != newConfig.DewHeaterSwitchDeviceNumber ||
		conf.DewHeaterSwitchID != newConfig.DewHeaterSwitchID ||
		conf.DewHeaterSwitchIsRheostat != newConfig.DewHeaterSwitchIsRheostat ||
		conf.DewControlOnOffTargetDelta != newConfig.DewControlOnOffTargetDelta ||
		conf.DewControlOnOffHysteresis != newConfig.DewControlOnOffHysteresis

	// Apply log level immediately
	logger.SetLevelFromString(newConfig.LogLevel)

	if err := config.Update(newConfig); err != nil {
		logger.Error("Failed to save proxy config: %v", err)
		http.Error(w, "Failed to save configuration", http.StatusInternalServerError)
		return
	}

	// Trigger reconnect in a goroutine if needed
	if portChanged || panelRevisionChanged {
		logger.Info("Serial port or panel revision configuration changed. Triggering reconnect.")
		go serial.Reconnect(newConfig.SerialPortName)
	} else if beepChanged {
		logger.Info("Beep configuration changed. Syncing beep setting.")
		go serial.SyncBeep()
	}
	if dewControlChanged {
		logger.Info("Dew control configuration changed. Triggering an immediate recompute.")
		dewcontrol.TriggerRecompute()
	}

	logger.Info("Proxy settings updated via API.")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(newConfig)
}

// getAvailableIPs returns a list of local IPv4 addresses.
func getAvailableIPs() ([]string, error) {
	ips := []string{"127.0.0.1", "0.0.0.0"}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ipStr := ipnet.IP.String()
				// Filter out APIPA addresses (169.254.x.x)
				if !strings.HasPrefix(ipStr, "169.254.") {
					ips = append(ips, ipStr)
				}
			}
		}
	}
	return ips, nil
}
