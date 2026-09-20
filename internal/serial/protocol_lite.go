package serial

import (
	"fmt"
	"strconv"
)

// liteProtocol implements the non-motorized "Lite" Gemini Flat Panel: same command
// framing as Rev2 ('#'-terminated, unpadded), but no cover/motor at all and a
// structurally different >S# reply. See docs/serial_communication.md.
type liteProtocol struct{}

func (liteProtocol) Name() string      { return "Lite" }
func (liteProtocol) ConfigKey() string { return "lite" }
func (liteProtocol) Terminator() byte  { return '#' }

func (liteProtocol) HandshakeCommand() string { return ">H#" }
func (liteProtocol) IsHandshakeReply(resp string) bool {
	return resp == "*HGeminiFlatPanelLite#"
}

func (liteProtocol) FormatCommand(letter byte, value int, hasValue bool) string {
	if !hasValue {
		return fmt.Sprintf(">%c#", letter)
	}
	return fmt.Sprintf(">%c%d#", letter, value)
}

func (liteProtocol) SupportsCover() bool            { return false }
func (liteProtocol) SupportsBeep() bool             { return true }
func (liteProtocol) SupportsBrightnessMode() bool   { return true }
func (liteProtocol) SupportsFirmwareQuery() bool    { return true }
func (liteProtocol) SupportsHeater() bool           { return false }
func (liteProtocol) SupportsAutoCalibrate() bool    { return false } // Lite has no cover/motor at all
func (liteProtocol) IsAsyncCover() bool             { return false }
func (liteProtocol) HasReadyQuery() bool            { return false }
func (liteProtocol) SupportsHalt() bool             { return false }
func (liteProtocol) SupportsPositionSetReply() bool { return false } // no cover/motor at all; unreachable
func (liteProtocol) JogScaleFactor() float64        { return 1.0 }   // no motor at all; unreachable

// ParseStatus: Lite has no cover/motor fields at all. Its >S# reply is
// "*SLMB#" — light (0/1), brightness-mode (0 low/1 high), beep (0/1). Motor/cover are
// hardcoded since there's no physical cover to report on; serial.go maps
// SupportsCover()==false to ASCOM CoverState "NotPresent" regardless of this value.
func (liteProtocol) ParseStatus(resp string) (StatusFrame, bool) {
	if len(resp) < 5 || resp[0] != '*' || resp[1] != 'S' {
		return StatusFrame{}, false
	}
	l, err := strconv.Atoi(string(resp[2]))
	if err != nil {
		return StatusFrame{}, false
	}
	return StatusFrame{
		MotorRunning: false,
		LightOn:      l != 0,
		CoverState:   2, // unused once SupportsCover()==false is honored by the caller
	}, true
}

func (liteProtocol) ParseBrightness(resp string) (int, bool) {
	return parseUnpaddedBrightness(resp)
}

// IsOpenConfirmed/IsCloseConfirmed are never reached in practice (serial.go refuses to
// send open/close at all once SupportsCover() is false) but are defined for interface
// completeness.
func (liteProtocol) IsOpenConfirmed(resp string) bool  { return false }
func (liteProtocol) IsCloseConfirmed(resp string) bool { return false }
