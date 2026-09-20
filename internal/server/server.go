package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"geminiflatpanel/internal/alpaca"
	"geminiflatpanel/internal/config"
	"geminiflatpanel/internal/handlers"
	"geminiflatpanel/internal/logger"
	"geminiflatpanel/internal/logstream"
	"geminiflatpanel/internal/serial"
)

// Start initializes and starts the HTTP server, serving the frontend from the provided filesystem.
func Start(frontendFS fs.FS, appVersion string) {
	setupRoutes(frontendFS, appVersion)

	conf := config.Get()
	addr := fmt.Sprintf("%s:%d", conf.ListenAddress, conf.NetworkPort)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatal("Could not bind to address '%s' (reason: %v). Please check your configuration.", addr, err)
		return // Unreachable, but good practice
	}

	logger.Info("Starting Alpaca API server on %s...", addr)

	// Global HTTP handler with route normalization and logging
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("Global HTTP: %s %s (from %s)", r.Method, r.URL.Path, r.RemoteAddr)

		path := r.URL.Path
		parts := strings.Split(path, "/")

		// Alpaca routes: /api/v1/... or /setup/v1/...
		if len(parts) >= 3 && (strings.EqualFold(parts[1], "api") || strings.EqualFold(parts[1], "setup")) && strings.EqualFold(parts[2], "v1") {
			// Normalize api/v1 segments
			parts[1] = strings.ToLower(parts[1])
			parts[2] = strings.ToLower(parts[2])

			// Device type check (segment 3): MUST be case-sensitively in lowercase.
			// E.g. /api/v1/covercalibrator/0/... is valid, but /api/v1/COVERCALIBRATOR/0/... is bad.
			if len(parts) >= 4 {
				devicetype := parts[3]
				if devicetype != strings.ToLower(devicetype) {
					http.Error(w, "Bad Request: Alpaca device type must be in lowercase", http.StatusBadRequest)
					return
				}
			}

			// Normalize the method segment (the last segment) to lowercase
			if len(parts) >= 6 {
				parts[5] = strings.ToLower(parts[5])
			}

			r.URL.Path = strings.Join(parts, "/")
		} else if strings.EqualFold(r.URL.Path, "/management/apiversions") {
			r.URL.Path = "/management/apiversions"
		} else if len(parts) >= 3 && strings.EqualFold(parts[1], "management") && strings.EqualFold(parts[2], "v1") {
			parts[1] = "management"
			parts[2] = "v1"
			if len(parts) >= 4 {
				parts[3] = strings.ToLower(parts[3]) // e.g. description or configureddevices
			}
			r.URL.Path = strings.Join(parts, "/")
		}

		http.DefaultServeMux.ServeHTTP(w, r)
	})

	if err := http.Serve(listener, handler); err != nil {
		logger.Fatal("HTTP server failed: %v", err)
	}
}

func setupRoutes(frontendFS fs.FS, appVersion string) {
	api := alpaca.NewAPI(appVersion)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/setup" {
			http.ServeFileFS(w, r, frontendFS, "index.html")
		} else {
			http.FileServer(http.FS(frontendFS)).ServeHTTP(w, r)
		}
	})

	// --- Management API ---
	http.HandleFunc("/management/v1/description", api.HandleManagementDescription)
	http.HandleFunc("/management/v1/configureddevices", alpaca.HandleManagementConfiguredDevices)
	http.HandleFunc("/management/apiversions", alpaca.HandleManagementApiVersions)

	// --- Custom Web UI API ---
	http.HandleFunc("/api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			handlers.HandleGetSettings(w, r)
		} else if r.Method == http.MethodPost {
			handlers.HandlePostSettings(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/api/v1/status", handlers.HandleGetLiveStatus)
	http.HandleFunc("/api/custom/jog", handlers.HandleJogMotor)
	http.HandleFunc("/api/custom/set_closed", handlers.HandleSetClosedPosition)
	http.HandleFunc("/api/custom/set_opened", handlers.HandleSetOpenedPosition)
	http.HandleFunc("/api/custom/heater", handlers.HandleSetHeaterPower)
	http.HandleFunc("/api/custom/test_observing_conditions", handlers.HandleTestObservingConditions)
	http.HandleFunc("/api/custom/discover_observing_conditions", handlers.HandleDiscoverObservingConditions)
	http.HandleFunc("/api/custom/list_serial_ports", handlers.HandleListSerialPorts)
	http.HandleFunc("/api/custom/discover_dew_heater_switches", handlers.HandleDiscoverDewHeaterSwitches)
	http.HandleFunc("/api/custom/list_switch_channels", handlers.HandleListSwitchChannels)
	http.HandleFunc("/api/custom/auto_calibrate_closed", handlers.HandleAutoCalibrateClosed)
	http.HandleFunc("/api/custom/auto_calibrate_opened", handlers.HandleAutoCalibrateOpen)
	http.HandleFunc("/api/custom/connect", handlers.HandleManualConnect)
	http.HandleFunc("/api/custom/disconnect", handlers.HandleManualDisconnect)
	http.HandleFunc("/api/v1/proxy/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"version": appVersion})
	})
	http.HandleFunc("/api/v1/firmware/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"version": serial.GetFirmwareVersion()})
	})
	http.HandleFunc("/api/v1/log/download", handlers.HandleDownloadLog)

	// --- WebSocket ---
	http.HandleFunc("/ws/logs", logstream.ServeWs)

	// --- Alpaca Device API ---
	setupAlpacaDeviceRoutes(api)
}

func setupAlpacaDeviceRoutes(api *alpaca.API) {
	http.HandleFunc("/setup/v1/covercalibrator/0/setup", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/setup", http.StatusFound) })
	http.HandleFunc("/setup/v1/switch/0/setup", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/setup", http.StatusFound) })

	commonHandlers := map[string]http.HandlerFunc{
		"description":      api.HandleDeviceDescription,
		"driverinfo":       api.HandleDriverInfo,
		"driverversion":    api.HandleDriverVersion,
		"connected":        api.HandleConnected,
		"interfaceversion": api.HandleInterfaceVersion,
		"supportedactions": api.HandleSupportedActions,
		"name":             api.HandleDeviceName("Gemini Flat Panel"),
	}

	// CoverCalibrator device
	ccHandlers := map[string]http.HandlerFunc{
		"brightness":      api.HandleCoverCalibratorBrightness,
		"calibratorstate": api.HandleCoverCalibratorState,
		"calibratoron":    api.HandleCoverCalibratorOn,
		"calibratoroff":   api.HandleCoverCalibratorOff,
		"coverstate":      api.HandleCoverCoverState,
		"maxbrightness":   api.HandleCoverCalibratorMaxBrightness,
		"closecover":      api.HandleCoverCloseCover,
		"haltcover":       api.HandleCoverHaltCover,
		"opencover":       api.HandleCoverOpenCover,
		"action":          api.HandleAction,
	}
	for k, v := range commonHandlers {
		ccHandlers[k] = v
	}
	http.HandleFunc("/api/v1/covercalibrator/0/", alpaca.Handler(deviceMux(ccHandlers, api)))

	// Switch device (dew heater, exposed as a rheostat — see internal/alpaca/switchdevice.go)
	switchHandlers := map[string]http.HandlerFunc{
		"maxswitch":            api.HandleSwitchMaxSwitch,
		"canwrite":             api.HandleSwitchCanWrite,
		"getswitchname":        api.HandleSwitchGetName,
		"getswitchdescription": api.HandleSwitchGetDescription,
		"minswitchvalue":       api.HandleSwitchMinValue,
		"maxswitchvalue":       api.HandleSwitchMaxValue,
		"switchstep":           api.HandleSwitchStep,
		"getswitchvalue":       api.HandleSwitchGetValue,
		"setswitchvalue":       api.HandleSwitchSetValue,
		"getswitch":            api.HandleSwitchGetSwitch,
		"setswitch":            api.HandleSwitchSetSwitch,
		"setswitchname":        api.HandleSwitchSetSwitchName,
	}
	for k, v := range commonHandlers {
		switchHandlers[k] = v
	}
	switchHandlers["name"] = api.HandleDeviceName("Gemini Flat Panel Dew Heater")
	switchHandlers["interfaceversion"] = api.HandleSwitchInterfaceVersion
	switchHandlers["supportedactions"] = api.HandleSwitchSupportedActions
	http.HandleFunc("/api/v1/switch/0/", alpaca.Handler(deviceMux(switchHandlers, api)))
}

// deviceMux creates a handler that routes to sub-handlers based on the final URL path segment.
func deviceMux(handlers map[string]http.HandlerFunc, api *alpaca.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		lastSlash := strings.LastIndex(path, "/")
		if lastSlash == -1 {
			alpaca.ErrorResponse(w, r, http.StatusNotFound, 0x404, "Invalid URL path.")
			return
		}
		method := strings.ToLower(path[lastSlash+1:])
		logger.Debug("Alpaca DeviceMux: Routing method '%s' (Path: %s)", method, r.URL.Path)

		if handler, ok := handlers[method]; ok {
			handler(w, r)
		} else {
			logger.Warn("Alpaca DeviceMux: Method '%s' not found on this device (Path: %s)", method, r.URL.Path)
			alpaca.ErrorResponse(w, r, http.StatusNotFound, 0x40C, fmt.Sprintf("Method '%s' not found on this device.", method))
		}
	}
}
