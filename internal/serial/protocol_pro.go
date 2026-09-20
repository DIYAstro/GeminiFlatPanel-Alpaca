package serial

import (
	"fmt"
	"strconv"
	"strings"
)

// proProtocol implements the newest "Pro"/"V3" Gemini Flat Panel firmware — the same
// generation Gemini Astro ships on both the "Standard" and the heated "Plus" panel.
// Its open/close is asynchronous: the command only acknowledges receipt (with the
// reached-position number, not fixed text), and actual completion must be observed
// via later >S# polling.
type proProtocol struct{}

func (proProtocol) Name() string      { return "Pro" }
func (proProtocol) ConfigKey() string { return "pro" }
func (proProtocol) Terminator() byte  { return '#' }

func (proProtocol) HandshakeCommand() string { return ">H#" }
func (proProtocol) IsHandshakeReply(resp string) bool {
	return resp == "*HGeminiFlatPanelPro#"
}

func (proProtocol) FormatCommand(letter byte, value int, hasValue bool) string {
	if !hasValue {
		return fmt.Sprintf(">%c#", letter)
	}
	return fmt.Sprintf(">%c%d#", letter, value)
}

func (proProtocol) SupportsCover() bool { return true }

// SupportsBeep: same class of gap as SupportsBrightnessMode below — INDI's
// GeminiFlatpanelProAdapter never implements >Tx# for Pro (setBeep hardcoded to return
// false), so this project initially followed suit and could never silence the device's
// beep. Confirmed live it actually works: >T0# then closing the cover was silent,
// >T1# then closing beeped audibly, on the same real Pro unit.
func (proProtocol) SupportsBeep() bool { return true }

// SupportsBrightnessMode: contrary to INDI's GeminiFlatpanelProAdapter (which never
// implements >Yx# for Pro and reports this as false), real Pro hardware DOES support
// it — confirmed live: toggling >Y0#/>Y1# with the identical internal PWM value (>B200#
// in both banks) produced a visibly different brightness. Gemini's own vendor driver
// exposes this as a working "High-BM"/"Low-BM" toggle.
func (proProtocol) SupportsBrightnessMode() bool { return true }
func (proProtocol) SupportsFirmwareQuery() bool  { return true }

// SupportsHeater: Pro has a dew-heater output (physically a USB-C port, not the barrel
// jack Gemini's website describes). No command for it exists in INDI or AlpacaBridge
// (same class of gap as brightness mode/beep above). The command is >Wxx#, x=0-100,
// unpadded, no reply.
func (proProtocol) SupportsHeater() bool { return true }

// SupportsAutoCalibrate: >N#/>R#, each a ~22s round trip. The firmware drives the
// motor to the physical hard stop itself, measures it, and replies with the
// newly-measured angle (*N71#/*R813#).
func (proProtocol) SupportsAutoCalibrate() bool { return true }
func (proProtocol) IsAsyncCover() bool          { return true }
func (proProtocol) HasReadyQuery() bool         { return false }

// SupportsHalt: >K# genuinely stops an in-progress motor move, confirmed live against
// real Pro hardware (both during a normal Open and, separately, the auto-calibration
// sweep) -- see HaltCover's doc comment in serial.go for the caveat found alongside it.
func (proProtocol) SupportsHalt() bool { return true }

// SupportsPositionSetReply: true -- >F#/>E# reply *F<adc>#/*E<adc>#, per Gemini's own
// protocol documentation for this firmware.
func (proProtocol) SupportsPositionSetReply() bool { return true }

// JogScaleFactor: >M<n>#'s wire argument is physical degrees, which the firmware
// converts to steps itself. This project's own JogMotor() takes its angle argument in
// the same raw units closedAngle/openAngle/currentMotorAngle use, not physical
// degrees, so this factor (~5.4 raw units per degree for firmware 107 — the
// ADC-counts-per-degree of this panel's position sensor) converts between the two
// before sending the command. >M also genuinely replies on this firmware
// (*O<newRawPosition>#, latency scaling with move size up to several seconds for a
// large jog) — see hasResponse() in serial.go, keyed off this same flag.
//
// Unlike Rev2 (see rev2Protocol.JogScaleFactor), this isn't a single hardcoded firmware
// constant derived from a fixed multiply — the M-handler reads the position sensor
// directly, consistent with an async, sensor-driven move rather than a deterministic
// one. Deliberately keyed off the exact firmware version (107, both the Plus and
// non-Plus builds share this handler byte-for-byte) rather than "Pro" generally, since
// Rev2-class firmware uses different fixed constants across different builds (31 vs
// 60) and there's no reason to assume every Pro firmware version shares this same
// ~5.4 ratio either. Firmware versions other than 107 fall back to 1.0 (unchanged
// pass-through, >M still treated as NC).
func (proProtocol) JogScaleFactor() float64 {
	if GetFirmwareVersion() == "107" {
		return 5.4
	}
	return 1.0
}

// ParseStatus: Pro's *S reply carries motor/light/cover at fixed offsets 2/4/6 (not
// the 2-digit-id-then-3-digits layout Rev2 uses), followed by extra trailing bytes: a
// single dew/heater-adjacent digit, then closed- and open-position calibration
// angles. Example: "*S0M0L2C0D76C405O#".
func (proProtocol) ParseStatus(resp string) (StatusFrame, bool) {
	if len(resp) < 7 || resp[0] != '*' || resp[1] != 'S' {
		return StatusFrame{}, false
	}
	m, err1 := strconv.Atoi(string(resp[2]))
	l, err2 := strconv.Atoi(string(resp[4]))
	c, err3 := strconv.Atoi(string(resp[6]))
	if err1 != nil || err2 != nil || err3 != nil {
		return StatusFrame{}, false
	}
	// Motor is treated as a plain "running or not" flag: real hardware has been
	// observed sending values other than 0/1 while actively moving (e.g. "2" during a
	// close), so only light/cover are range-checked.
	if l < 0 || l > 1 || c < 0 || c > 3 {
		return StatusFrame{}, false
	}

	frame := StatusFrame{MotorRunning: m != 0, LightOn: l != 0, CoverState: c}

	// Trailing "0D76C405O"-shaped segment: <dewDigit>D<closedAngle>C<openAngle>O.
	// Starts at offset 8, past the 'C' cover-tag letter that follows the cover digit
	// at offset 6 (i.e. resp[7] == 'C' itself, not part of this segment).
	// Best-effort — never fails the whole parse; a missing or differently-shaped tail
	// just leaves these fields nil.
	if len(resp) > 8 {
		tail := resp[8:]
		if end := strings.IndexByte(tail, '#'); end >= 0 {
			tail = tail[:end]
		}
		if dIdx := strings.IndexByte(tail, 'D'); dIdx > 0 {
			if dew, err := strconv.Atoi(tail[:dIdx]); err == nil {
				frame.DewRaw = &dew
			}
			rest := tail[dIdx+1:]
			if cIdx := strings.IndexByte(rest, 'C'); cIdx >= 0 {
				if closedAngle, err := strconv.Atoi(rest[:cIdx]); err == nil {
					frame.ClosedAngle = &closedAngle
				}
				rest2 := rest[cIdx+1:]
				if oIdx := strings.IndexByte(rest2, 'O'); oIdx >= 0 {
					if openAngle, err := strconv.Atoi(rest2[:oIdx]); err == nil {
						frame.OpenAngle = &openAngle
					}
				}
			}
		}
	}

	return frame, true
}

// ParseBrightness: Pro requires a stricter minimum length (>= 4) than the other
// unpadded-reply revisions but shares the same offset-2 layout.
func (proProtocol) ParseBrightness(resp string) (int, bool) {
	if len(resp) < 4 || resp[0] != '*' || resp[1] != 'J' {
		return 0, false
	}
	return parseUnpaddedBrightness(resp)
}

// IsOpenConfirmed/IsCloseConfirmed: Pro's firmware doesn't send fixed confirmation
// text — any reply starting with *O/*C is accepted (e.g. "*O405#" carrying the reached
// angle).
func (proProtocol) IsOpenConfirmed(resp string) bool {
	return len(resp) >= 2 && resp[0] == '*' && resp[1] == 'O'
}
func (proProtocol) IsCloseConfirmed(resp string) bool {
	return len(resp) >= 2 && resp[0] == '*' && resp[1] == 'C'
}
