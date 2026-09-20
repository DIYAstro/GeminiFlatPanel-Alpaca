package events

import "sync"

// ComPortStatus represents the connection status of the COM port.
type ComPortStatus int

const (
	// Disconnected indicates the COM port is unexpectedly not connected (the
	// connection was lost, or a connection attempt failed) -- worth a "check your
	// device and cable"-style notification.
	Disconnected ComPortStatus = iota
	// Connected indicates the COM port is connected.
	Connected
	// Idle indicates the port is deliberately not connected (connect-on-demand mode,
	// released or never asked for) -- not a problem, so notification listeners
	// should treat this differently than Disconnected.
	Idle
)

var (
	// ComPortStatusChan is a channel that broadcasts the connection status of the COM port.
	// The serial manager will write to this channel, and other parts of the application (like systray) can listen to it.
	ComPortStatusChan = make(chan ComPortStatus, 1)

	// CoverStateChangedChan signals whenever the cover's ASCOM CoverState actually
	// changes value -- e.g. so dewcontrol's DewControlOnlyWhenOpen gate can react
	// immediately instead of waiting for its own next scheduled tick. A plain wake-up
	// signal (not a value channel): receivers just re-read whatever they need
	// themselves. Buffered + non-blocking send, like ComPortStatusChan, so a burst of
	// rapid transitions collapses into a single pending signal.
	CoverStateChangedChan = make(chan struct{}, 1)

	// once is used to ensure the listener is only started once.
	once sync.Once
)

// StartListener ensures that any component that needs to react to events can do so.
// It is designed to be called multiple times safely, but the listener function will only be executed once.
func StartListener(listener func()) {
	once.Do(listener)
}
