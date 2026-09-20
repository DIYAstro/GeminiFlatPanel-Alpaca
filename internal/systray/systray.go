//go:build windows

package systray

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"geminiflatpanel/internal/config"
	"geminiflatpanel/internal/events"
	"geminiflatpanel/internal/logger"
	"syscall"
	"unsafe"

	"fyne.io/systray"
	"github.com/go-toast/toast"
	"golang.org/x/sys/windows"
)

var singleInstanceMutex windows.Handle

// Run is the entry point for the systray functionality.
func Run(onStart func(), iconData []byte) {
	// Check for single instance before doing anything else.
	checkSingleInstance()
	// The `onStart` function will be called by `systray.Run` via `OnReady`.
	systray.Run(func() { OnReady(onStart, iconData) }, OnExit)
}

// OnReady is called when the system tray is ready.
func OnReady(onStart func(), iconData []byte) {
	systray.SetIcon(iconData)
	systray.SetTitle("Gemini Flat Panel Proxy")
	systray.SetTooltip("Gemini Flat Panel Proxy Driver is running")

	mSetup := systray.AddMenuItem("Open Setup Page", "Open the web setup page")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Exit", "Quit the application")

	// Show a notification that the app is running.
	go ShowNotification("Gemini Flat Panel Proxy is active", "The program is running in the background. You can find the icon in the System Tray.")

	// Start the main application logic in a goroutine.
	go onStart()
	events.StartListener(listenForComPortEvents)

	// Handle menu clicks.
	go func() {
		for {
			select {
			case <-mSetup.ClickedCh:
				url := config.GetSetupURL()
				openBrowser(url)
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

// OnExit is called when the application is requested to exit.
func OnExit() {
	logger.Info("Exiting application.")
	logger.Close()

	// Release the single instance mutex.
	if singleInstanceMutex != 0 {
		logger.Info("Releasing single instance mutex.")
		windows.ReleaseMutex(singleInstanceMutex)
		windows.CloseHandle(singleInstanceMutex)
	}
}

// checkSingleInstance ensures only one instance of the application is running.
func checkSingleInstance() {
	mutexName := "GeminiFlatPanelProxySingleInstanceMutex"
	handle, err := windows.CreateMutex(nil, true, windows.StringToUTF16Ptr(mutexName))
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
			// Another instance is running. Open the setup page and exit.
			url := config.GetSetupURLFromFile()
			openBrowser(url)

			if handle != 0 {
				windows.CloseHandle(handle)
			}
			os.Exit(0)
		}

		logger.Error("SingleInstance: CreateMutex failed: %v. Proceeding defensively.", err)
		return
	}

	singleInstanceMutex = handle
}

// openBrowser opens the specified URL in the default browser.
func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		// Use the logger, but since this can be called before the logger is fully set up,
		// it might only go to stdout.
		logger.Error("Failed to open browser: %v", err)
	}
}

// ShowMessageBox displays a Windows message box.
// This is kept in case it's needed for critical errors before the logger is available.
func ShowMessageBox(title, message string, style uint) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBoxW := user32.NewProc("MessageBoxW")
	lpText := syscall.StringToUTF16Ptr(message)
	lpCaption := syscall.StringToUTF16Ptr(title)
	messageBoxW.Call(0, uintptr(unsafe.Pointer(lpText)), uintptr(unsafe.Pointer(lpCaption)), uintptr(style))
}

// ShowNotification displays a toast notification if enabled in config.
func ShowNotification(title, message string) {
	if !config.Get().EnableNotifications {
		return
	}
	notification := toast.Notification{
		AppID:   "Gemini Flat Panel",
		Title:   title,
		Message: message,
		// Duration can be set to toast.Short or toast.Long.
		Duration: toast.Short,
	}
	err := notification.Push()
	if err != nil {
		logger.Warn("Failed to show notification: %v", err)
	}
}

// listenForComPortEvents waits for status updates from the serial manager
// and shows notifications accordingly.
func listenForComPortEvents() {
	logger.Info("Systray is now listening for COM port connection events.")
	go func() {
		for status := range events.ComPortStatusChan {
			switch status {
			case events.Connected:
				go ShowNotification("Gemini Flat Panel Reconnected", "Connection to the COM port has been restored.")
			case events.Disconnected:
				go ShowNotification("Gemini Flat Panel Connection Lost", "Connection to the COM port was interrupted. Please check the device and cable.")
			case events.Idle:
				// Deliberate (connect-on-demand released, manually or after the idle
				// grace period), not a problem -- no "check your cable" notification.
			}
		}
		logger.Info("Systray stopped listening for COM port events.")
	}()
}
