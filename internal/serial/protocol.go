package serial

import (
	"strconv"
	"strings"
)

// StatusFrame is the protocol-agnostic result of parsing a device's >S# reply.
// Fields that a given revision's frame doesn't carry (e.g. Lite has no cover, Rev2
// doesn't embed calibration angles in *S) are simply left at their zero value / nil.
type StatusFrame struct {
	MotorRunning bool
	LightOn      bool
	CoverState   int // raw device value: 0 moving, 1 closed, 2 open, 3 timed out

	// Pro only, decoded from the trailing bytes of its *S reply. nil when not
	// reported/parseable.
	ClosedAngle *int
	OpenAngle   *int
	// DewRaw is a single unconfirmed status digit, presumed dew-heater related —
	// read-only, logged for correlation while testing, and never acted on.
	DewRaw *int
}

// Protocol abstracts the differences between Gemini Flat Panel firmware revisions, so
// the connection/queueing machinery in serial.go stays revision-agnostic. Each known
// revision gets its own file (protocol_rev2.go, protocol_lite.go, protocol_pro.go) —
// see docs/serial_communication.md for the full protocol reference every implementation
// is built from.
type Protocol interface {
	// Name is the human-readable revision name used in logs and the UI.
	Name() string
	// ConfigKey is the value stored in proxy_config.json's panelRevision field.
	ConfigKey() string
	// Terminator is the byte that ends every response frame. '#' for every
	// currently-supported revision; the method still exists as a real abstraction
	// point in case a future revision needs something else.
	Terminator() byte

	// HandshakeCommand is the command sent to identify the device.
	HandshakeCommand() string
	// IsHandshakeReply reports whether resp is this revision's expected handshake reply.
	IsHandshakeReply(resp string) bool

	// FormatCommand builds a full command string for the given command letter, with an
	// optional value (hasValue=false for commands like >H# / >L# that take none).
	FormatCommand(letter byte, value int, hasValue bool) string

	SupportsCover() bool
	SupportsBeep() bool
	SupportsBrightnessMode() bool
	SupportsFirmwareQuery() bool
	// SupportsHeater reports whether this revision accepts the dew-heater power command
	// (>Wxx#, x=0-100). Pro only.
	SupportsHeater() bool
	// SupportsAutoCalibrate reports whether this revision accepts the automatic
	// closed/open-position learning commands (>N#/>R#, Pro only) — a ~22s round trip
	// per command, driving the motor to the physical hard stop and measuring it,
	// unlike SetClosedPosition/SetOpenedPosition's "record wherever the motor
	// currently is".
	SupportsAutoCalibrate() bool
	// IsAsyncCover reports whether open/close commands only acknowledge receipt
	// immediately, with actual completion observed later via >S# polling (Pro only).
	IsAsyncCover() bool
	// HasReadyQuery reports whether >A# is a real command on this revision (Rev2 only);
	// other revisions are treated as always-ready once connected.
	HasReadyQuery() bool
	// SupportsHalt reports whether this revision has a real command to interrupt an
	// in-progress cover move (>K#, Pro only). K stops the motor within ~1-2s of a
	// normal Open/Close (not just during the N#/R# auto-calibration sweep, despite the
	// vendor's own Windows driver only exposing a Halt button there), and the panel's
	// calibration survives untouched. See HaltCover's doc comment for the one real
	// caveat (>S#'s own cover-state classification is briefly unreliable right after a
	// halt). False for Rev2 and Lite, whose command dispatch tables have no K handler.
	SupportsHalt() bool
	// SupportsPositionSetReply reports whether >F#/>E# (SetClosedPosition/
	// SetOpenedPosition) send a reply at all. True for Pro (*F<adc>#/*E<adc>#). False
	// for Rev2, where both are documented "NC" (no reply) -- without this,
	// SetClosedPosition/SetOpenedPosition on Rev2 would always time out waiting for a
	// reply the firmware never sends, leaving the "device_ready" >A# check permanently
	// stuck at "not calibrated" since the position could never actually be saved.
	SupportsPositionSetReply() bool
	// JogScaleFactor is the ratio between >M<n>#'s argument and the resulting raw
	// position delta (same units as closedAngle/openAngle/currentMotorAngle). 1.0 (no
	// correction, argument passed straight through) for any revision whose jog
	// protocol isn't fully understood -- which also means >M is still treated as
	// sending no reply there (see hasResponse()).
	JogScaleFactor() float64

	ParseStatus(resp string) (StatusFrame, bool)
	ParseBrightness(resp string) (int, bool)

	IsOpenConfirmed(resp string) bool
	IsCloseConfirmed(resp string) bool
}

// knownProtocols lists every supported revision, in the order they are probed during
// auto-detection. Rev1 (the INDI reference driver's oldest adapter) is deliberately not
// included: it was never confirmed by Gemini or tested against real hardware, and this
// project only ships revisions it can actually stand behind — see CHANGELOG.md.
//
// Adding a future revision is meant to be additive: implement Protocol in a new
// protocol_<name>.go file and add one line here — no other file needs to change.
var knownProtocols = []Protocol{
	rev2Protocol{},
	liteProtocol{},
	proProtocol{},
}

// protocolByConfigKey returns the Protocol matching the given proxy_config.json
// panelRevision value, or nil if key is "" / "auto" / unrecognized.
func protocolByConfigKey(key string) Protocol {
	for _, p := range knownProtocols {
		if p.ConfigKey() == key {
			return p
		}
	}
	return nil
}

// --- Shared helpers used by more than one Protocol implementation ---

// parseIdTaggedStatus parses the "*S<2-digit id><motor><light><cover>#" layout Rev2
// uses (docs/serial_communication.md): a 2-digit device ID (19 or 99) followed by three
// single digits. ok=false if the layout or id doesn't match. Kept as its own named
// helper (rather than inlined into protocol_rev2.go) since a future revision could
// plausibly reuse the same layout.
func parseIdTaggedStatus(resp string) (StatusFrame, bool) {
	if len(resp) < 7 || resp[0] != '*' || resp[1] != 'S' {
		return StatusFrame{}, false
	}
	id, err := strconv.Atoi(resp[2:4])
	if err != nil || (id != 19 && id != 99) {
		return StatusFrame{}, false
	}
	m, err1 := strconv.Atoi(string(resp[4]))
	l, err2 := strconv.Atoi(string(resp[5]))
	c, err3 := strconv.Atoi(string(resp[6]))
	if err1 != nil || err2 != nil || err3 != nil {
		return StatusFrame{}, false
	}
	return StatusFrame{
		MotorRunning: m != 0,
		LightOn:      l != 0,
		CoverState:   c,
	}, true
}

// parseUnpaddedBrightness parses a "*Jnnn#" reply where the numeric value runs from
// offset 2 up to the trailing '#' — the layout shared by Rev2/Lite/Pro.
func parseUnpaddedBrightness(resp string) (int, bool) {
	if len(resp) < 3 || resp[0] != '*' || resp[1] != 'J' {
		return 0, false
	}
	end := strings.IndexByte(resp, '#')
	if end <= 2 {
		return 0, false
	}
	v, err := strconv.Atoi(resp[2:end])
	if err != nil {
		return 0, false
	}
	return v, true
}

// bankSwitchResult is the outcome of computeBankedBrightness.
type bankSwitchResult struct {
	InternalValue int  // the 0-254 value to send in >Bnnn#
	NeedsSwitch   bool // whether a >Yx# bank-switch command must be sent first
	NewHighMode   bool // the highMode state after this call
}

// computeBankedBrightness implements this project's core feature: switching between
// low-bank and high-bank PWM without the user having to go into driver settings,
// unlike Gemini's own vendor driver — and unlike INDI, which only exposes it as a
// manual switch property.
//
// It maps a single continuous client-facing brightness value onto the device's real
// low/high-bank PWM range, switching banks automatically only when actually needed
// (tracked via the caller's highMode). Pulled out of SetBrightness() unchanged from
// this project's original Rev2-only logic, so behavior for existing Rev2 users is
// identical — and so Lite (which also supports >Yx#, see protocol_lite.go) gets the
// exact same transparent single-slider experience.
//
//   - brightness <= 254: low bank, sent as-is.
//   - brightness in (254, maxBrightness]: high bank, remapped from [255,maxBrightness]
//     onto [highBankStartValue, 255] so the low/high transition doesn't visibly jump.
func computeBankedBrightness(brightness int, highBankStartValue int, highMode bool) bankSwitchResult {
	if brightness <= 254 {
		return bankSwitchResult{
			InternalValue: brightness,
			NeedsSwitch:   highMode,
			NewHighMode:   false,
		}
	}

	x := highBankStartValue
	if x < 0 {
		x = 0
	}
	if x > 254 {
		x = 254
	}

	steps := brightness - 255
	internalVal := x + (steps*(255-x))/255
	if internalVal > 255 {
		internalVal = 255
	}

	return bankSwitchResult{
		InternalValue: internalVal,
		NeedsSwitch:   !highMode,
		NewHighMode:   true,
	}
}
