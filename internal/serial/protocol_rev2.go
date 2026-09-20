package serial

import "fmt"

// rev2Protocol implements the firmware revision this project has supported since its
// initial release — '#'-terminated, unpadded commands (except >M, see FormatCommand),
// full beep + brightness-mode (low/high bank) support, blocking open/close with
// fixed-text confirmations. See docs/serial_communication.md for the full reference.
type rev2Protocol struct{}

func (rev2Protocol) Name() string      { return "Rev2" }
func (rev2Protocol) ConfigKey() string { return "rev2" }
func (rev2Protocol) Terminator() byte  { return '#' }

func (rev2Protocol) HandshakeCommand() string { return ">H#" }
func (rev2Protocol) IsHandshakeReply(resp string) bool {
	return resp == "*HGeminiFlatPanel#"
}

func (rev2Protocol) FormatCommand(letter byte, value int, hasValue bool) string {
	if !hasValue {
		return fmt.Sprintf(">%c#", letter)
	}
	if letter == 'M' {
		// >M is zero-padded to width 3, sign included (Go's %03d reproduces this
		// exactly: -1 -> "-01", 45 -> "045"), unlike every other Rev2 command
		// (>Y0#, >T1#, >J#, >V#, >A#, >X...). Whether this padding is functionally
		// required by the firmware or just a formatting convention is unconfirmed
		// either way — matched here to stay closest to what's known to work, since
		// this is a motor command.
		return fmt.Sprintf(">M%03d#", value)
	}
	return fmt.Sprintf(">%c%d#", letter, value)
}

func (rev2Protocol) SupportsCover() bool          { return true }
func (rev2Protocol) SupportsBeep() bool           { return true }
func (rev2Protocol) SupportsBrightnessMode() bool { return true }
func (rev2Protocol) SupportsFirmwareQuery() bool  { return true }
func (rev2Protocol) SupportsHeater() bool         { return false }
func (rev2Protocol) SupportsAutoCalibrate() bool  { return false }
func (rev2Protocol) IsAsyncCover() bool           { return false }
func (rev2Protocol) HasReadyQuery() bool          { return true }

// SupportsHalt: false -- this Rev2-class firmware's own command dispatch table has no
// K handler at all, unlike Pro's.
func (rev2Protocol) SupportsHalt() bool { return false }

// SupportsPositionSetReply: false -- >F#/>E# are NC (no reply) on this revision, per
// Gemini's own protocol documentation for it.
func (rev2Protocol) SupportsPositionSetReply() bool { return false }

// JogScaleFactor: unlike Pro, Rev2-class firmware computes >M<n>#'s effect with a
// single hardcoded multiply applied directly to the argument, no ADC/sensor
// involvement at all. That constant is NOT uniform across Rev2-class firmware builds:
//   - firmware "408" (GM25, unheated): 31
//   - firmware "405" (the heated "Plus" build): 60
// Deliberately keyed off the exact firmware version rather than "Rev2" generally —
// applying either constant to an unverified firmware build would very likely be wrong,
// given these two already disagree with each other despite being the same protocol
// family. Any other firmware version falls back to 1.0 (unchanged pass-through, >M
// still treated as NC).
func (rev2Protocol) JogScaleFactor() float64 {
	switch GetFirmwareVersion() {
	case "408":
		return 31.0
	case "405":
		return 60.0
	}
	return 1.0
}

func (rev2Protocol) ParseStatus(resp string) (StatusFrame, bool) {
	return parseIdTaggedStatus(resp)
}

func (rev2Protocol) ParseBrightness(resp string) (int, bool) {
	return parseUnpaddedBrightness(resp)
}

func (rev2Protocol) IsOpenConfirmed(resp string) bool  { return resp == "*OOpened#" }
func (rev2Protocol) IsCloseConfirmed(resp string) bool { return resp == "*CClosed#" }
