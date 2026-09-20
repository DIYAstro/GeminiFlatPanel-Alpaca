package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"geminiflatpanel/internal/logger"
)

// defaultListenAddress returns the platform-specific default listen address.
// On Linux (typically headless servers/Astro-Pi), we default to 0.0.0.0 for network access.
// On Windows, we default to 127.0.0.1 (localhost only) for security.
func defaultListenAddress() string {
	if runtime.GOOS == "linux" {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// ProxyConfig stores configuration specific to the Go proxy itself.
type ProxyConfig struct {
	// SerialPortName must name a real port explicitly (e.g. "COM8") -- there is no
	// autodetect. It used to blindly scan every enumerated serial port, but that
	// requires resetting a candidate's MCU to get it to identify itself (see
	// internal/serial's reconnect doc comment), which would just as readily reset an
	// unrelated device sharing the same common USB-serial chip (confirmed live: this
	// project's own sibling project uses the identical CH340 chip as the Gemini
	// panel, so scanning could not tell them apart without risking exactly that). The
	// Settings UI instead offers a dropdown of enumerated port names (no opening/
	// resetting of USB serial adapters involved) to pick from, refreshable on demand.
	SerialPortName          string `json:"serialPortName"`
	PanelRevision           string `json:"panelRevision"` // "auto" | "rev2" | "lite" | "pro"
	NetworkPort             int    `json:"networkPort"`
	ListenAddress           string `json:"listenAddress"`
	LogLevel                string `json:"logLevel"`
	MaxBrightness           int    `json:"maxBrightness"`
	HighBankStartValue      int    `json:"highBankStartValue"`
	EnableAlpacaDiscovery   bool   `json:"enableAlpacaDiscovery"` // Respond to Alpaca UDP discovery packets
	BlockLightWhenOpen      bool   `json:"blockLightWhenOpen"`
	EnableBeep              bool   `json:"enableBeep"`
	EnableNotifications     bool   `json:"enableNotifications"`
	SettleTime              int    `json:"settleTime"`
	MaxConnectionRetries    int    `json:"maxConnectionRetries"`
	ConnectionRetryInterval int    `json:"connectionRetryInterval"`
	CoverTimeout            int    `json:"coverTimeout"`
	// SerialConnectOnDemand, when true, replaces the default always-on background
	// retry (internal/serial's ManageConnection loop) with a dormant-until-asked
	// model: the port isn't touched at startup or in the background at all, only
	// opened when an Alpaca client PUTs Connected=true (or the dashboard's manual
	// Connect button is used), and released again IdleReleaseTimeoutSeconds after the
	// client disconnects with nothing reconnecting in that window. See internal/serial's
	// ConnectOnDemand/ScheduleIdleRelease. Off by default -- opt-in, existing
	// always-on behavior unchanged unless enabled.
	SerialConnectOnDemand bool `json:"serialConnectOnDemand"`
	// IdleReleaseTimeoutSeconds is how long the port stays open after an Alpaca client
	// disconnects before SerialConnectOnDemand releases it, in case of a quick reconnect
	// (e.g. an equipment profile switch in the client software). Only meaningful when
	// SerialConnectOnDemand is on.
	IdleReleaseTimeoutSeconds int `json:"idleReleaseTimeoutSeconds"`

	// Automatic dew-heater control: a heating curve driven by the delta between
	// ambient temperature and dew point, sourced from an Alpaca ObservingConditions
	// client (preferred) or Open-Meteo (fallback). See internal/dewcontrol.
	EnableAutoDewControl      bool    `json:"enableAutoDewControl"`
	DewControlIntervalMinutes int     `json:"dewControlIntervalMinutes"`
	DewControlDeltaFullPower  float64 `json:"dewControlDeltaFullPower"` // °C; delta <= this -> 100%
	DewControlDeltaZeroPower  float64 `json:"dewControlDeltaZeroPower"` // °C; delta >= this -> 0%
	// DewControlOnlyWhenOpen, when true, forces the auto-control loop's heater output to
	// 0% on any tick where the cover isn't confirmed Open (ASCOM CoverState 3) -- a
	// closed panel already shields the optics, so heating only matters once it's open.
	// Off by default. See internal/dewcontrol's coverGateBlocksHeat.
	DewControlOnlyWhenOpen bool `json:"dewControlOnlyWhenOpen"`
	// DewControlFailsafeEnabled, when true, forces the heater to
	// DewControlFailsafePercent (instead of leaving it at its last value) on any tick
	// where no weather data is available at all. Off by default.
	DewControlFailsafeEnabled bool `json:"dewControlFailsafeEnabled"`
	// DewControlFailsafePercent is the heater power (0-100) forced when the failsafe
	// above applies. Default 0 (off).
	DewControlFailsafePercent       int    `json:"dewControlFailsafePercent"`
	ObservingConditionsURL          string `json:"observingConditionsUrl"` // e.g. http://host:port ; empty = not configured
	ObservingConditionsDeviceNumber int    `json:"observingConditionsDeviceNumber"`
	// EnableOpenMeteo gates whether the Open-Meteo fallback is used at all -- an
	// explicit flag rather than inferring "configured" from WeatherLatitude/
	// WeatherLongitude being non-zero, which would otherwise treat a real location at
	// exactly 0,0 ("Null Island") as unconfigured. See loadLocked's migration handling
	// for how this is inferred on upgrade from a config saved before this field existed.
	EnableOpenMeteo  bool    `json:"enableOpenMeteo"`
	WeatherLatitude  float64 `json:"weatherLatitude"`
	WeatherLongitude float64 `json:"weatherLongitude"`

	// DewHeaterBackend selects what the dew-control loop actually drives: "none" (the
	// feature is off, and the dashboard's Dew Heater card stays hidden entirely --
	// without this, the card would have to always show on a Pro panel whether or not
	// dew control is actually wanted), "built-in" (the Pro panel's own heater output,
	// via serial.SetHeaterPower -- today's only behavior), or "external" (a channel on
	// a separate Alpaca Switch device, see internal/dewheaterswitch -- lets a
	// Rev2/Lite panel, which has no heater output of its own, still use automatic dew
	// control against e.g. a smart-plug-driven dew strap).
	DewHeaterBackend string `json:"dewHeaterBackend"` // "none" | "built-in" | "external"
	// DewHeaterSwitchURL/DeviceNumber/ID address the specific channel on an external
	// Alpaca Switch device, analogous to ObservingConditionsURL/DeviceNumber above.
	// Only meaningful when DewHeaterBackend is "external".
	DewHeaterSwitchURL          string `json:"dewHeaterSwitchUrl"`
	DewHeaterSwitchDeviceNumber int    `json:"dewHeaterSwitchDeviceNumber"`
	DewHeaterSwitchID           int    `json:"dewHeaterSwitchId"`
	// DewHeaterSwitchIsRheostat records whether the selected external channel is a
	// PWM-style rheostat (true) or a plain boolean on/off outlet (false) -- detected
	// once from the channel's own MinSwitchValue/MaxSwitchValue/SwitchStep when the
	// Dew Control Setup dialog is saved (internal/dewheaterswitch.ListChannels), not
	// re-queried on every tick, since a fixed hardware channel's shape practically
	// never changes after it's wired up.
	DewHeaterSwitchIsRheostat bool `json:"dewHeaterSwitchIsRheostat"`
	// DewControlOnOffTargetDelta/Hysteresis drive the external on/off backend's bang-
	// bang control: the switch turns on at delta <= TargetDelta-Hysteresis and off at
	// delta >= TargetDelta+Hysteresis, holding its last state in between. A single
	// threshold would chatter a relay near the boundary on realistically noisy
	// temperature readings; this mirrors an ordinary thermostat's deadband. Only
	// meaningful when DewHeaterBackend is "external" and the selected channel isn't a
	// rheostat (see internal/dewcontrol.computeHeaterState).
	DewControlOnOffTargetDelta float64 `json:"dewControlOnOffTargetDelta"` // °C
	DewControlOnOffHysteresis  float64 `json:"dewControlOnOffHysteresis"`  // °C
}

var (
	proxyConfig     *ProxyConfig // Singleton instance
	proxyConfigFile string       // Full path to the config file
	configMutex     sync.RWMutex // Protects the proxyConfig singleton
)

// init sets up the path to the configuration file.
// init is called once at program startup, so it doesn't need locks.
func init() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		logger.Fatal("FATAL: Could not get user config directory: %v", err)
	}
	appConfigDir := filepath.Join(configDir, "GeminiFlatPanelProxy")
	if err := os.MkdirAll(appConfigDir, 0755); err != nil {
		logger.Fatal("FATAL: Could not create application config directory '%s': %v", appConfigDir, err)
	}
	proxyConfigFile = filepath.Join(appConfigDir, "proxy_config.json")
}

// Load reads the configuration from the JSON file into the singleton instance.
// If the file doesn't exist, it initializes a default configuration and saves it.
func Load() error {
	configMutex.Lock()
	defer configMutex.Unlock()
	return loadLocked()
}

func loadLocked() error {
	file, err := os.ReadFile(proxyConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("Proxy config file '%s' not found. Using default settings.", proxyConfigFile)
			// Initialize with default values
			proxyConfig = &ProxyConfig{
				PanelRevision:              "auto",
				NetworkPort:                32300, // Default Alpaca port or standard proxy port
				ListenAddress:              defaultListenAddress(),
				LogLevel:                   "INFO",
				MaxBrightness:              510,
				HighBankStartValue:         9,
				EnableAlpacaDiscovery:      true,
				BlockLightWhenOpen:         false,
				EnableBeep:                 true,
				EnableNotifications:        true,
				SettleTime:                 2000,
				MaxConnectionRetries:       3,
				ConnectionRetryInterval:    1000,
				CoverTimeout:               60,
				IdleReleaseTimeoutSeconds:  60,
				DewControlIntervalMinutes:  5,
				DewControlDeltaFullPower:   1.0,
				DewControlDeltaZeroPower:   5.0,
				DewHeaterBackend:           "none",
				DewControlOnOffTargetDelta: 3.0,
				DewControlOnOffHysteresis:  1.0,
			}
			return saveLocked() // File not found is not an error, just means defaults apply
		}
		return fmt.Errorf("failed to read proxy config file: %w", err)
	}

	var tempConfig ProxyConfig
	if err := json.Unmarshal(file, &tempConfig); err != nil {
		return fmt.Errorf("failed to unmarshal proxy config: %w", err)
	}

	// Unmarshal into a map to check for missing boolean or numeric keys
	var rawMap map[string]interface{}
	if err := json.Unmarshal(file, &rawMap); err == nil {
		if _, exists := rawMap["enableAlpacaDiscovery"]; !exists {
			logger.Info("Configuration key 'enableAlpacaDiscovery' not found, defaulting to true.")
			tempConfig.EnableAlpacaDiscovery = true
		}
		if _, exists := rawMap["blockLightWhenOpen"]; !exists {
			logger.Info("Configuration key 'blockLightWhenOpen' not found, defaulting to false.")
			tempConfig.BlockLightWhenOpen = false
		}
		if _, exists := rawMap["highBankStartValue"]; !exists {
			logger.Info("Configuration key 'highBankStartValue' not found, defaulting to 9.")
			tempConfig.HighBankStartValue = 9
		}
		if _, exists := rawMap["enableBeep"]; !exists {
			logger.Info("Configuration key 'enableBeep' not found, defaulting to true.")
			tempConfig.EnableBeep = true
		}
		if _, exists := rawMap["enableNotifications"]; !exists {
			logger.Info("Configuration key 'enableNotifications' not found, defaulting to true.")
			tempConfig.EnableNotifications = true
		}
		if _, exists := rawMap["settleTime"]; !exists {
			logger.Info("Configuration key 'settleTime' not found, defaulting to 2000.")
			tempConfig.SettleTime = 2000
		}
		if _, exists := rawMap["maxConnectionRetries"]; !exists {
			logger.Info("Configuration key 'maxConnectionRetries' not found, defaulting to 3.")
			tempConfig.MaxConnectionRetries = 3
		}
		if _, exists := rawMap["connectionRetryInterval"]; !exists {
			logger.Info("Configuration key 'connectionRetryInterval' not found, defaulting to 1000.")
			tempConfig.ConnectionRetryInterval = 1000
		}
		if _, exists := rawMap["coverTimeout"]; !exists {
			logger.Info("Configuration key 'coverTimeout' not found, defaulting to 60.")
			tempConfig.CoverTimeout = 60
		}
		if _, exists := rawMap["idleReleaseTimeoutSeconds"]; !exists {
			logger.Info("Configuration key 'idleReleaseTimeoutSeconds' not found, defaulting to 60.")
			tempConfig.IdleReleaseTimeoutSeconds = 60
		}
		if _, exists := rawMap["panelRevision"]; !exists {
			logger.Info("Configuration key 'panelRevision' not found, defaulting to 'auto'.")
			tempConfig.PanelRevision = "auto"
		}
		if _, exists := rawMap["dewControlIntervalMinutes"]; !exists {
			logger.Info("Configuration key 'dewControlIntervalMinutes' not found, defaulting to 5.")
			tempConfig.DewControlIntervalMinutes = 5
		}
		if _, exists := rawMap["dewControlDeltaFullPower"]; !exists {
			logger.Info("Configuration key 'dewControlDeltaFullPower' not found, defaulting to 1.0.")
			tempConfig.DewControlDeltaFullPower = 1.0
		}
		if _, exists := rawMap["dewControlDeltaZeroPower"]; !exists {
			logger.Info("Configuration key 'dewControlDeltaZeroPower' not found, defaulting to 5.0.")
			tempConfig.DewControlDeltaZeroPower = 5.0
		}
		if _, exists := rawMap["dewControlOnlyWhenOpen"]; !exists {
			logger.Info("Configuration key 'dewControlOnlyWhenOpen' not found, defaulting to false.")
			tempConfig.DewControlOnlyWhenOpen = false
		}
		if _, exists := rawMap["dewControlFailsafeEnabled"]; !exists {
			logger.Info("Configuration key 'dewControlFailsafeEnabled' not found, defaulting to false.")
			tempConfig.DewControlFailsafeEnabled = false
		}
		if _, exists := rawMap["dewControlFailsafePercent"]; !exists {
			logger.Info("Configuration key 'dewControlFailsafePercent' not found, defaulting to 0.")
			tempConfig.DewControlFailsafePercent = 0
		}
		if _, exists := rawMap["enableOpenMeteo"]; !exists {
			// Migrating from before this flag existed: infer it from the old heuristic
			// (non-zero coordinates meant "configured") so an already-working Open-Meteo
			// fallback doesn't silently go dark for anyone upgrading.
			inferred := tempConfig.WeatherLatitude != 0 || tempConfig.WeatherLongitude != 0
			logger.Info("Configuration key 'enableOpenMeteo' not found, defaulting to %v (inferred from existing coordinates).", inferred)
			tempConfig.EnableOpenMeteo = inferred
		}
	}

	proxyConfig = &tempConfig

	// --- Validate and set defaults for missing fields ---
	if proxyConfig.NetworkPort == 0 {
		proxyConfig.NetworkPort = 32300
	}
	if proxyConfig.ListenAddress == "" {
		proxyConfig.ListenAddress = defaultListenAddress()
	}
	if proxyConfig.LogLevel == "" {
		proxyConfig.LogLevel = "INFO"
	}
	if proxyConfig.MaxBrightness == 0 {
		proxyConfig.MaxBrightness = 510
	}
	if proxyConfig.SettleTime < 0 {
		proxyConfig.SettleTime = 2000
	}
	if proxyConfig.MaxConnectionRetries < 0 {
		proxyConfig.MaxConnectionRetries = 3
	}
	if proxyConfig.ConnectionRetryInterval <= 0 {
		proxyConfig.ConnectionRetryInterval = 1000
	}
	if proxyConfig.CoverTimeout <= 0 {
		proxyConfig.CoverTimeout = 60
	}
	if proxyConfig.DewControlFailsafePercent < 0 {
		proxyConfig.DewControlFailsafePercent = 0
	}
	if proxyConfig.DewControlFailsafePercent > 100 {
		proxyConfig.DewControlFailsafePercent = 100
	}
	if proxyConfig.IdleReleaseTimeoutSeconds <= 0 {
		proxyConfig.IdleReleaseTimeoutSeconds = 60
	}
	if proxyConfig.DewControlIntervalMinutes < 1 {
		proxyConfig.DewControlIntervalMinutes = 5
	}
	if proxyConfig.DewControlOnOffHysteresis < 0 {
		proxyConfig.DewControlOnOffHysteresis = 0
	}
	switch proxyConfig.DewHeaterBackend {
	case "none", "built-in", "external":
		// valid
	default:
		// Also covers a config saved before this field existed (empty string).
		proxyConfig.DewHeaterBackend = "none"
	}
	switch proxyConfig.PanelRevision {
	case "auto", "rev2", "lite", "pro":
		// valid
	default:
		// Also catches a config saved before Rev1 support was removed -- falls back to
		// auto-detect rather than staying pinned to a revision that no longer exists.
		proxyConfig.PanelRevision = "auto"
	}

	// Apply the loaded log level immediately.
	logger.SetLevelFromString(proxyConfig.LogLevel)
	logger.Info("Loaded proxy config from '%s'", proxyConfigFile)
	return nil
}

// Save writes the current configuration to the JSON file.
func Save() error {
	configMutex.Lock()
	defer configMutex.Unlock()
	return saveLocked()
}

func saveLocked() error {
	if proxyConfig == nil {
		return fmt.Errorf("cannot save nil config")
	}
	logger.Debug("Attempting to save proxy config to file: %s", proxyConfigFile)
	data, err := json.MarshalIndent(proxyConfig, "", "  ")
	if err != nil {
		logger.Error("saveProxyConfig: failed to marshal proxy config: %v", err)
		return fmt.Errorf("failed to marshal proxy config: %w", err)
	}

	if err := os.WriteFile(proxyConfigFile, data, 0644); err != nil {
		logger.Error("saveProxyConfig: failed to write proxy config file '%s': %v", proxyConfigFile, err)
		return fmt.Errorf("failed to write proxy config file: %w", err)
	}
	logger.Info("Successfully saved proxy config to file '%s'", proxyConfigFile)
	return nil
}

// Get returns a thread-safe copy of the singleton ProxyConfig instance.
func Get() *ProxyConfig {
	configMutex.RLock()
	if proxyConfig != nil {
		cfgCopy := *proxyConfig
		configMutex.RUnlock()
		return &cfgCopy
	}
	configMutex.RUnlock()

	configMutex.Lock()
	defer configMutex.Unlock()
	// Double check
	if proxyConfig == nil {
		if err := loadLocked(); err != nil {
			logger.Fatal("Failed to load configuration on demand: %v", err)
		}
	}
	cfgCopy := *proxyConfig
	return &cfgCopy
}

// Update updates the global configuration singleton and saves it to disk.
func Update(newConfig ProxyConfig) error {
	configMutex.Lock()
	defer configMutex.Unlock()
	proxyConfig = &newConfig
	return saveLocked()
}

// UpdateSerialPort updates the serial port name in the configuration.
func UpdateSerialPort(portName string) error {
	configMutex.Lock()
	defer configMutex.Unlock()
	if proxyConfig == nil {
		return fmt.Errorf("config not loaded")
	}
	proxyConfig.SerialPortName = portName
	return saveLocked()
}

// GetSetupURL builds the full URL for the web setup page based on the current config.
func GetSetupURL() string {
	conf := Get()
	host := conf.ListenAddress
	if host == "0.0.0.0" || host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d/setup", host, conf.NetworkPort)
}

// GetSetupURLFromFile reads the configuration file directly to build the setup URL.
func GetSetupURLFromFile() string {
	const defaultHost = "127.0.0.1"
	const defaultPort = 32300

	file, err := os.ReadFile(proxyConfigFile)
	if err != nil {
		return fmt.Sprintf("http://%s:%d/setup", defaultHost, defaultPort)
	}

	var config struct {
		NetworkPort   int    `json:"networkPort"`
		ListenAddress string `json:"listenAddress"`
	}
	if err := json.Unmarshal(file, &config); err != nil {
		return fmt.Sprintf("http://%s:%d/setup", defaultHost, defaultPort)
	}

	host := config.ListenAddress
	port := config.NetworkPort

	if host == "0.0.0.0" || host == "" {
		host = defaultHost
	}
	if port == 0 {
		port = defaultPort
	}

	return fmt.Sprintf("http://%s:%d/setup", host, port)
}
