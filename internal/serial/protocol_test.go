package serial

import (
	"testing"
	"time"
)

// Literal strings in these tests are taken directly from docs/serial_communication.md.

func TestHandshakes(t *testing.T) {
	cases := []struct {
		proto Protocol
		cmd   string
		reply string
	}{
		{rev2Protocol{}, ">H#", "*HGeminiFlatPanel#"},
		{liteProtocol{}, ">H#", "*HGeminiFlatPanelLite#"},
		{proProtocol{}, ">H#", "*HGeminiFlatPanelPro#"},
	}
	for _, c := range cases {
		t.Run(c.proto.Name(), func(t *testing.T) {
			if got := c.proto.HandshakeCommand(); got != c.cmd {
				t.Errorf("HandshakeCommand() = %q, want %q", got, c.cmd)
			}
			if !c.proto.IsHandshakeReply(c.reply) {
				t.Errorf("IsHandshakeReply(%q) = false, want true", c.reply)
			}
			// A reply belonging to a different revision must never match.
			if c.proto.IsHandshakeReply("*HSomethingElse#") {
				t.Errorf("IsHandshakeReply matched an unrelated reply")
			}
		})
	}

	// Cross-check: no revision accepts another revision's handshake reply.
	replies := map[string]string{
		"Rev2": "*HGeminiFlatPanel#",
		"Lite": "*HGeminiFlatPanelLite#",
		"Pro":  "*HGeminiFlatPanelPro#",
	}
	for _, a := range knownProtocols {
		for name, reply := range replies {
			if name == a.Name() {
				continue
			}
			if a.IsHandshakeReply(reply) {
				t.Errorf("%s.IsHandshakeReply(%q) = true, want false (that's %s's reply)", a.Name(), reply, name)
			}
		}
	}
}

func TestFormatCommand(t *testing.T) {
	cases := []struct {
		name     string
		proto    Protocol
		letter   byte
		value    int
		hasValue bool
		want     string
	}{
		{"rev2 no value", rev2Protocol{}, 'L', 0, false, ">L#"},
		{"rev2 value", rev2Protocol{}, 'B', 128, true, ">B128#"},
		{"lite no value", liteProtocol{}, 'D', 0, false, ">D#"},
		{"lite value", liteProtocol{}, 'Y', 1, true, ">Y1#"},
		{"pro no value", proProtocol{}, 'O', 0, false, ">O#"},
		{"pro value", proProtocol{}, 'B', 254, true, ">B254#"},
		// Heater command (dew-heater power percent, 0-100).
		{"pro heater min", proProtocol{}, 'W', 0, true, ">W0#"},
		{"pro heater max", proProtocol{}, 'W', 100, true, ">W100#"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.proto.FormatCommand(c.letter, c.value, c.hasValue); got != c.want {
				t.Errorf("FormatCommand(%q,%d,%v) = %q, want %q", c.letter, c.value, c.hasValue, got, c.want)
			}
		})
	}
}

func TestParseStatusRev2(t *testing.T) {
	// "*S<id>MLC#" layout.
	proto := rev2Protocol{}
	frame, ok := proto.ParseStatus("*S19011#")
	if !ok {
		t.Fatalf("ParseStatus failed to parse a valid frame")
	}
	if frame.MotorRunning != false || frame.LightOn != true || frame.CoverState != 1 {
		t.Errorf("got %+v, want motor=false light=true cover=1", frame)
	}

	if _, ok := proto.ParseStatus("*S00011#"); ok {
		t.Errorf("ParseStatus accepted an invalid device id (00)")
	}
	if _, ok := proto.ParseStatus("*X19011#"); ok {
		t.Errorf("ParseStatus accepted a non-status frame")
	}
}

func TestParseStatusLite(t *testing.T) {
	// "*SLMB#": light=1, brightness-mode=0, beep=1.
	frame, ok := liteProtocol{}.ParseStatus("*S101#")
	if !ok {
		t.Fatalf("ParseStatus failed to parse a valid Lite frame")
	}
	if !frame.LightOn {
		t.Errorf("got LightOn=false, want true")
	}
	if frame.MotorRunning {
		t.Errorf("got MotorRunning=true, want false (Lite has no motor)")
	}
}

func TestParseStatusPro(t *testing.T) {
	// motor=0 (stopped), light=0 (off), cover=2 (open), trailing "0D76C405O" ==
	// dew-flag 0, closed-angle 76, open-angle 405.
	frame, ok := proProtocol{}.ParseStatus("*S0M0L2C0D76C405O#")
	if !ok {
		t.Fatalf("ParseStatus failed to parse the real Pro capture")
	}
	if frame.MotorRunning {
		t.Errorf("MotorRunning = true, want false")
	}
	if frame.LightOn {
		t.Errorf("LightOn = true, want false")
	}
	if frame.CoverState != 2 {
		t.Errorf("CoverState = %d, want 2 (open)", frame.CoverState)
	}
	if frame.DewRaw == nil || *frame.DewRaw != 0 {
		t.Errorf("DewRaw = %v, want pointer to 0", frame.DewRaw)
	}
	if frame.ClosedAngle == nil || *frame.ClosedAngle != 76 {
		t.Errorf("ClosedAngle = %v, want pointer to 76", frame.ClosedAngle)
	}
	if frame.OpenAngle == nil || *frame.OpenAngle != 405 {
		t.Errorf("OpenAngle = %v, want pointer to 405", frame.OpenAngle)
	}

	// A frame without the trailing calibration segment should still parse the core
	// motor/light/cover fields, just without angles.
	frame2, ok2 := proProtocol{}.ParseStatus("*S1M1L0C#")
	if !ok2 {
		t.Fatalf("ParseStatus failed on a frame without trailing calibration bytes")
	}
	if !frame2.MotorRunning || !frame2.LightOn || frame2.CoverState != 0 {
		t.Errorf("got %+v, want motor=true light=true cover=0", frame2)
	}
	if frame2.ClosedAngle != nil || frame2.OpenAngle != nil {
		t.Errorf("expected nil angles when no trailing segment is present, got %+v", frame2)
	}

	// Real hardware capture during a close move: motor digit is "2", not just 0/1.
	// Must still parse — motor is a plain running/not-running flag, not
	// range-validated — rather than being rejected outright.
	frame3, ok3 := proProtocol{}.ParseStatus("*S2M0L0C0D45C700O#")
	if !ok3 {
		t.Fatalf("ParseStatus rejected a real captured in-motion frame with motor=2")
	}
	if !frame3.MotorRunning {
		t.Errorf("motor=2 should still be treated as running, got MotorRunning=false")
	}
	if frame3.LightOn {
		t.Errorf("LightOn = true, want false")
	}
	if frame3.CoverState != 0 {
		t.Errorf("CoverState = %d, want 0 (moving)", frame3.CoverState)
	}
}

func TestParseBrightness(t *testing.T) {
	cases := []struct {
		proto Protocol
		resp  string
		want  int
	}{
		{rev2Protocol{}, "*J128#", 128},
		{liteProtocol{}, "*J254#", 254},
		{proProtocol{}, "*J0#", 0},
	}
	for _, c := range cases {
		t.Run(c.proto.Name(), func(t *testing.T) {
			got, ok := c.proto.ParseBrightness(c.resp)
			if !ok {
				t.Fatalf("ParseBrightness(%q) failed", c.resp)
			}
			if got != c.want {
				t.Errorf("ParseBrightness(%q) = %d, want %d", c.resp, got, c.want)
			}
		})
	}

	// Pro requires >= 4 chars — a too-short reply must fail.
	if _, ok := (proProtocol{}).ParseBrightness("*J#"); ok {
		t.Errorf("Pro ParseBrightness accepted a too-short reply")
	}
}

func TestOpenCloseConfirmation(t *testing.T) {
	if !(rev2Protocol{}).IsOpenConfirmed("*OOpened#") {
		t.Errorf("Rev2 did not accept its own open confirmation")
	}
	if !(rev2Protocol{}).IsCloseConfirmed("*CClosed#") {
		t.Errorf("Rev2 did not accept its own close confirmation")
	}
	if (rev2Protocol{}).IsOpenConfirmed("*O405#") {
		t.Errorf("Rev2 wrongly accepted Pro-style reached-angle confirmation")
	}

	// Pro: reached-angle acknowledgements.
	if !(proProtocol{}).IsOpenConfirmed("*O405#") {
		t.Errorf("Pro did not accept an open confirmation carrying a reached angle, *O405#")
	}
	if !(proProtocol{}).IsCloseConfirmed("*C70#") {
		t.Errorf("Pro did not accept a close confirmation carrying a reached angle, *C70#")
	}

	if (liteProtocol{}).IsOpenConfirmed("*OOpened#") || (liteProtocol{}).IsCloseConfirmed("*CClosed#") {
		t.Errorf("Lite must never confirm an open/close (it has no cover)")
	}
}

func TestCapabilities(t *testing.T) {
	cases := []struct {
		proto                                                                            Protocol
		cover, beep, brightnessMode, firmwareQuery, heater, autoCalibrate, async, readyQ bool
	}{
		{rev2Protocol{}, true, true, true, true, false, false, false, true},
		{liteProtocol{}, false, true, true, true, false, false, false, false},
		{proProtocol{}, true, true, true, true, true, true, true, false},
	}
	for _, c := range cases {
		t.Run(c.proto.Name(), func(t *testing.T) {
			if got := c.proto.SupportsCover(); got != c.cover {
				t.Errorf("SupportsCover() = %v, want %v", got, c.cover)
			}
			if got := c.proto.SupportsBeep(); got != c.beep {
				t.Errorf("SupportsBeep() = %v, want %v", got, c.beep)
			}
			if got := c.proto.SupportsBrightnessMode(); got != c.brightnessMode {
				t.Errorf("SupportsBrightnessMode() = %v, want %v", got, c.brightnessMode)
			}
			if got := c.proto.SupportsFirmwareQuery(); got != c.firmwareQuery {
				t.Errorf("SupportsFirmwareQuery() = %v, want %v", got, c.firmwareQuery)
			}
			if got := c.proto.SupportsHeater(); got != c.heater {
				t.Errorf("SupportsHeater() = %v, want %v", got, c.heater)
			}
			if got := c.proto.SupportsAutoCalibrate(); got != c.autoCalibrate {
				t.Errorf("SupportsAutoCalibrate() = %v, want %v", got, c.autoCalibrate)
			}
			if got := c.proto.IsAsyncCover(); got != c.async {
				t.Errorf("IsAsyncCover() = %v, want %v", got, c.async)
			}
			if got := c.proto.HasReadyQuery(); got != c.readyQ {
				t.Errorf("HasReadyQuery() = %v, want %v", got, c.readyQ)
			}
		})
	}
}

func TestComputeBankedBrightness(t *testing.T) {
	// Low bank, no prior high mode: straight passthrough, no switch needed.
	res := computeBankedBrightness(100, 15, false)
	if res.InternalValue != 100 || res.NeedsSwitch || res.NewHighMode {
		t.Errorf("low-bank case: got %+v", res)
	}

	// Crossing from low to high bank requires a switch.
	res = computeBankedBrightness(255, 15, false)
	if !res.NeedsSwitch || !res.NewHighMode {
		t.Errorf("low->high transition: got %+v, want NeedsSwitch=true NewHighMode=true", res)
	}
	// 255 is the first high-bank step: internalVal should equal highBankStartValue.
	if res.InternalValue != 15 {
		t.Errorf("first high-bank step: internalVal = %d, want 15", res.InternalValue)
	}

	// Top of the high-bank range (510) must map to the device's real max (255).
	res = computeBankedBrightness(510, 15, true)
	if res.NeedsSwitch {
		t.Errorf("already in high mode: NeedsSwitch = true, want false")
	}
	if res.InternalValue != 255 {
		t.Errorf("max high-bank value: internalVal = %d, want 255", res.InternalValue)
	}

	// Staying in high bank for a second call needs no further switch.
	res = computeBankedBrightness(400, 15, true)
	if res.NeedsSwitch {
		t.Errorf("staying in high mode: NeedsSwitch = true, want false")
	}

	// Dropping back to low bank requires a switch back.
	res = computeBankedBrightness(0, 15, true)
	if !res.NeedsSwitch || res.NewHighMode {
		t.Errorf("high->low transition: got %+v, want NeedsSwitch=true NewHighMode=false", res)
	}
}

func TestProtocolByConfigKey(t *testing.T) {
	if p := protocolByConfigKey("pro"); p == nil || p.Name() != "Pro" {
		t.Errorf("protocolByConfigKey(\"pro\") = %v, want Pro", p)
	}
	if p := protocolByConfigKey("auto"); p != nil {
		t.Errorf("protocolByConfigKey(\"auto\") = %v, want nil", p)
	}
	if p := protocolByConfigKey("nonsense"); p != nil {
		t.Errorf("protocolByConfigKey(\"nonsense\") = %v, want nil", p)
	}
}

func TestKnownProtocolsOrderMatchesINDIProbeOrder(t *testing.T) {
	want := []string{"Rev2", "Lite", "Pro"}
	if len(knownProtocols) != len(want) {
		t.Fatalf("knownProtocols has %d entries, want %d", len(knownProtocols), len(want))
	}
	for i, p := range knownProtocols {
		if p.Name() != want[i] {
			t.Errorf("knownProtocols[%d].Name() = %q, want %q", i, p.Name(), want[i])
		}
	}
}

// TestShouldBlockLightForOpenCover locks down a real bug: on a panel without a
// motorized cover (e.g. Lite), the "block light when cover is open" safety setting must
// be a no-op — there's no cover-open state to guard against, and coverState never gets
// updated away from its zero-value "unknown" for such panels, which used to make the
// block permanent.
func TestShouldBlockLightForOpenCover(t *testing.T) {
	cases := []struct {
		name          string
		brightness    int
		blockSetting  bool
		supportsCover bool
		coverState    int
		want          bool
	}{
		{"no-cover panel never blocks, even with the setting on and an 'open-ish' state", 100, true, false, 4, false},
		{"cover panel blocks while open", 100, true, true, 2, true},
		{"cover panel allows while closed", 100, true, true, 1, false},
		{"setting off never blocks", 100, false, true, 2, false},
		{"turning the light off is never blocked", 0, true, true, 2, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldBlockLightForOpenCover(c.brightness, c.blockSetting, c.supportsCover, c.coverState); got != c.want {
				t.Errorf("ShouldBlockLightForOpenCover(%d, %v, %v, %d) = %v, want %v",
					c.brightness, c.blockSetting, c.supportsCover, c.coverState, got, c.want)
			}
		})
	}
}

// TestEffectiveMaxBrightness locks down a real bug: SetBrightness and the Alpaca
// MaxBrightness endpoint (covercalibrator.go's effectiveMaxBrightness) both delegate to
// this one function, so they can't independently drift apart on the 254 cap or its
// condition.
func TestEffectiveMaxBrightness(t *testing.T) {
	cases := []struct {
		name          string
		configuredMax int
		bankCapable   bool
		want          int
	}{
		{"bank-capable panel keeps the configured ceiling above 254", 510, true, 510},
		{"non-bank-capable panel is clamped to 254", 510, false, 254},
		{"non-bank-capable panel below 254 is left alone", 200, false, 200},
		{"non-bank-capable panel exactly at 254 is left alone", 254, false, 254},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EffectiveMaxBrightness(c.configuredMax, c.bankCapable); got != c.want {
				t.Errorf("EffectiveMaxBrightness(%d, %v) = %d, want %d", c.configuredMax, c.bankCapable, got, c.want)
			}
		})
	}
}

// TestAscomCoverStateFromRaw locks down a real bug: GetCoverState() and
// GetLiveStatus() both delegate to this one mapping, so the Alpaca CoverState endpoint
// and the web dashboard's live status can't disagree about the same physical condition.
func TestAscomCoverStateFromRaw(t *testing.T) {
	cases := []struct {
		name     string
		hasCover bool
		raw      int
		want     int
	}{
		{"no cover is always NotPresent, regardless of raw state", false, 1, 0},
		{"raw 0 (moving) maps to ASCOM Moving", true, 0, 2},
		{"raw 1 (closed) maps to ASCOM Closed", true, 1, 1},
		{"raw 2 (open) maps to ASCOM Open", true, 2, 3},
		{"raw 3 (timed out) maps to ASCOM Unknown", true, 3, 4},
		{"raw 4 (unknown) maps to ASCOM Unknown", true, 4, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ascomCoverStateFromRaw(c.hasCover, c.raw); got != c.want {
				t.Errorf("ascomCoverStateFromRaw(%v, %d) = %d, want %d", c.hasCover, c.raw, got, c.want)
			}
		})
	}
}

// TestShouldTurnOffLightBeforeOpening locks down a real bug: BlockLightWhenOpen must
// also turn an already-on light off when Open is requested, not just refuse to turn it
// on while already open (that half is ShouldBlockLightForOpenCover, tested above).
func TestShouldTurnOffLightBeforeOpening(t *testing.T) {
	cases := []struct {
		name              string
		blockSetting      bool
		supportsCover     bool
		currentBrightness int
		want              bool
	}{
		{"light on, setting on, has cover -> turn off", true, true, 100, true},
		{"light already off -> nothing to do", true, true, 0, false},
		{"setting off -> never touches the light", false, true, 100, false},
		{"no cover -> never touches the light", true, false, 100, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldTurnOffLightBeforeOpening(c.blockSetting, c.supportsCover, c.currentBrightness); got != c.want {
				t.Errorf("ShouldTurnOffLightBeforeOpening(%v, %v, %d) = %v, want %v",
					c.blockSetting, c.supportsCover, c.currentBrightness, got, c.want)
			}
		})
	}
}

// TestJogMotorUsesFormatCommand locks down a real bug: jogging must honor each
// revision's own framing instead of the hand-built, always-unpadded
// fmt.Sprintf(">M%d#", angle) JogMotor used to send regardless of revision.
// FormatCommand itself is already covered generally by TestFormatCommand; this pins
// the specific case that motivated the fix (Rev2's >M is padded, unlike its other
// commands).
func TestJogMotorUsesFormatCommand(t *testing.T) {
	// Lite/Pro are unpadded — byte-identical to what JogMotor already sent before this
	// fix, so routing it through FormatCommand is a no-op behavior change for them.
	for _, proto := range []Protocol{liteProtocol{}, proProtocol{}} {
		t.Run(proto.Name(), func(t *testing.T) {
			if got := proto.FormatCommand('M', 5, true); got != ">M5#" {
				t.Errorf("FormatCommand('M', 5, true) = %q, want %q", got, ">M5#")
			}
		})
	}
	// Rev2's >M is the one exception to "Rev2 is unpadded": zero-padded to width 3
	// including the sign — every other Rev2 command stays unpadded (already covered by
	// TestFormatCommand's "rev2 value" case).
	rev2Cases := []struct {
		value int
		want  string
	}{
		{45, ">M045#"},
		{10, ">M010#"},
		{1, ">M001#"},
		{-1, ">M-01#"},
		{-10, ">M-10#"},
		{-45, ">M-45#"},
	}
	for _, c := range rev2Cases {
		if got := (rev2Protocol{}).FormatCommand('M', c.value, true); got != c.want {
			t.Errorf("Rev2 FormatCommand('M', %d, true) = %q, want %q", c.value, got, c.want)
		}
	}
}

// TestJogScaleFactorDefaults locks down a real bug: JogScaleFactor is keyed off the
// *exact* detected firmware version, not just the protocol/revision — disassembly found
// two different Rev2-class firmware builds (408, 405) using two different hardcoded
// constants (31, 60), so applying either one to an unverified build would likely be
// wrong. Every other revision/version combination must keep the original, unverified
// 1:1 pass-through assumption.
func TestJogScaleFactorDefaults(t *testing.T) {
	setVersion := func(v string) {
		firmwareVersionMu.Lock()
		firmwareVersion = v
		firmwareVersionMu.Unlock()
	}
	defer setVersion("unknown") // restore the zero-value default for other tests

	cases := []struct {
		name    string
		proto   Protocol
		version string
		want    float64
	}{
		{"lite, unknown version", liteProtocol{}, "unknown", 1.0},
		{"rev2, unverified version", rev2Protocol{}, "999", 1.0},
		{"rev2, firmware 408 (GM25, live-confirmed)", rev2Protocol{}, "408", 31.0},
		{"rev2, firmware 405 (Plus, disassembly-confirmed)", rev2Protocol{}, "405", 60.0},
		{"pro, unverified version", proProtocol{}, "999", 1.0},
		{"pro, firmware 107", proProtocol{}, "107", 5.4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setVersion(c.version)
			if got := c.proto.JogScaleFactor(); got != c.want {
				t.Errorf("%s.JogScaleFactor() = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestJogCommandValue locks down the fix for that same bug: >M<n>#'s argument must be scaled by
// JogScaleFactor() to reach the caller's intended delta (raw units, same as
// closedAngle/openAngle) instead of being sent straight through — while never rounding
// a non-zero request down to a silent no-op (Pro's factor of 5.4 would otherwise turn
// the dashboard's smallest jog button, angle=1, into >M0#).
func TestJogCommandValue(t *testing.T) {
	cases := []struct {
		name   string
		angle  int
		factor float64
		want   int
	}{
		{"unverified revision: pass through unchanged", 45, 1.0, 45},
		{"unverified revision: negative pass through", -10, 1.0, -10},
		{"zero angle: always zero regardless of factor", 0, 5.4, 0},
		{"pro: scales down", 45, 5.4, 8},   // round(45/5.4) = round(8.33) = 8
		{"pro: scales down, negative", -45, 5.4, -8},
		{"pro: small positive value floors to +1, not 0", 1, 5.4, 1},
		{"pro: small negative value floors to -1, not 0", -1, 5.4, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jogCommandValue(c.angle, c.factor); got != c.want {
				t.Errorf("jogCommandValue(%d, %v) = %d, want %d", c.angle, c.factor, got, c.want)
			}
		})
	}
}

// TestBrightnessFallbackGuess locks down a real bug: the "light is on but our
// brightness cache was reset" fallback must never exceed the panel's actual reachable
// ceiling (previously hardcoded to 255, above non-bank-capable panels' real 0-254
// range, e.g. Pro).
func TestBrightnessFallbackGuess(t *testing.T) {
	cases := []struct {
		name        string
		lastNonZero int
		bankCapable bool
		want        int
	}{
		{"prefers lastNonZero when available", 200, true, 200},
		{"falls back to 128 when lastNonZero is unset", 0, true, 128},
		{"clamped to 254 on a non-bank-capable panel even from lastNonZero", 300, false, 254},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := brightnessFallbackGuess(c.lastNonZero, c.bankCapable); got != c.want {
				t.Errorf("brightnessFallbackGuess(%d, %v) = %d, want %d",
					c.lastNonZero, c.bankCapable, got, c.want)
			}
		})
	}
}

// TestClampToRemaining locks down a real bug: moveCoverAsync's fallback loop must
// never sleep/query longer than the budget actually remaining before its deadline.
func TestClampToRemaining(t *testing.T) {
	cases := []struct {
		name      string
		d         time.Duration
		remaining time.Duration
		want      time.Duration
	}{
		{"plenty of budget left: d is unaffected", 1 * time.Second, 10 * time.Second, 1 * time.Second},
		{"less than d left: clamp to what's left", 1 * time.Second, 300 * time.Millisecond, 300 * time.Millisecond},
		{"deadline already passed: never negative", 1 * time.Second, -1 * time.Second, 0},
		{"exactly d left", 2 * time.Second, 2 * time.Second, 2 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampToRemaining(c.d, c.remaining); got != c.want {
				t.Errorf("clampToRemaining(%v, %v) = %v, want %v", c.d, c.remaining, got, c.want)
			}
		})
	}
}

// TestParseAngleReply locks down the parsing half of the auto-calibrate feature:
// AutoCalibrateClosed/AutoCalibrateOpen read the measured angle back out of
// "*N71#"/"*R813#"-shaped replies via this function.
func TestParseAngleReply(t *testing.T) {
	cases := []struct {
		name string
		resp string
		tag  byte
		want int
		ok   bool
	}{
		{"real capture: closed angle", "*N71#", 'N', 71, true},
		{"real capture: open angle", "*R813#", 'R', 813, true},
		{"wrong tag is rejected", "*R813#", 'N', 0, false},
		{"missing terminator is rejected", "*N71", 'N', 0, false},
		{"too short is rejected", "*N#", 'N', 0, false},
		{"non-numeric body is rejected", "*NXY#", 'N', 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseAngleReply(c.resp, c.tag)
			if ok != c.ok {
				t.Fatalf("parseAngleReply(%q, %q) ok = %v, want %v", c.resp, c.tag, ok, c.ok)
			}
			if ok && got != c.want {
				t.Errorf("parseAngleReply(%q, %q) = %d, want %d", c.resp, c.tag, got, c.want)
			}
		})
	}
}
