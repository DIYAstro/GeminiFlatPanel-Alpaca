package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"geminiflatpanel/internal/alpaca"
	"geminiflatpanel/internal/config"
	"geminiflatpanel/internal/dewcontrol"

	"geminiflatpanel/internal/logger"
	"geminiflatpanel/internal/logstream"
	"geminiflatpanel/internal/serial"
	"geminiflatpanel/internal/server"
)

//go:embed frontend-vue/dist
var embeddedFS embed.FS

var frontendFS fs.FS

// AppVersion is set at build time via ldflags.
// The default "dev" is used when the program is compiled without ldflags (e.g. 'go run').
var AppVersion string = "dev"

// fatalNotify displays a fatal error to the user.
// On Windows, this is overridden to show a MessageBox via the systray package.
// On Linux, it defaults to stderr output.
var fatalNotify = func(title, message string) {
	fmt.Fprintf(os.Stderr, "FATAL: %s: %s\n", title, message)
}

// startApp initializes and starts all the application's components.
func startApp() {
	// 1. Start the WebSocket hub for live logging.
	logStreamHub := logstream.NewHub()
	go logStreamHub.Run()

	// 2. Initialize the logger to use the hub as a writer.
	if err := logger.Setup(&logstream.Broadcaster{}); err != nil {
		fatalNotify("Fatal Error", "Failed to initialize file logger. The application will exit.")
		return
	}

	// 3. Load the proxy configuration.
	if err := config.Load(); err != nil {
		logger.Fatal("Failed to load proxy configuration: %v", err)
	}

	// 4. Start background tasks for serial communication and cache updates. Returns
	// immediately -- the initial connection attempt runs in the background, so a
	// slow/failing one can never delay the web server (step 7) from starting.
	serial.StartManager()


	// 5. Start the Alpaca discovery responder.
	go alpaca.RespondToDiscovery()

	// 6. Start the automatic dew-heater control loop (Pro panels only; no-ops
	// until both enabled and a data source are configured).
	dewcontrol.Start()

	// 7. Start the web server. This is a blocking call and will run for the
	// lifetime of the application, so it must be last.
	server.Start(frontendFS, AppVersion)
}
