# Gemini Flat Panel – Serial Protocol Reference

[← Back to main readme](../readme.md)

This project talks to three Gemini flat panel firmware revisions, auto-detected in this
order: **Rev2** (the original revision) → **Lite** (non-motorized) → **Pro** (newest,
motorized, async cover). Each has its own section below.

The Rev2 and Pro sections follow Gemini's own official protocol documentation for
those firmware revisions. No official documentation is available for Lite; its section
instead follows the widely-used open-source INDI project's driver, except for its
light/brightness commands, which are identical to Rev2's and follow that same official
documentation.

## Serial Port Settings (all revisions)
* **Baud Rate:** 9600 Baud
* **Parity:** None
* **Stop Bits:** One
* **Data Bits:** 8
* **DTR / RTS:** left at their default (unsuppressed) state on open, which resets the
  panel's MCU — this recovers a panel left unresponsive by a prior session, and is
  harmless on an already-running one (Open/Close and auto-calibrate calibration
  survive a real power cycle).

---

## Rev2 (the original revision)

| Command | Reply | Notes |
| :--- | :--- | :--- |
| `>H#` | `*HGeminiFlatPanel#` | Handshake / device identification |
| `>V#` | `*Vvvv#` | Firmware version; this project requires $vvv \geq 402$ |
| `>S#` | `*SidMLC#` | Status: `id` is `19` or `99`; `M`=motor (0 stopped, 1 running); `L`=light (0/1); `C`=cover (0 moving, 1 closed, 2 open, 3 timed out) |
| `>L#` | `*Lxxx#` | Light on, `xxx` = brightness 0–255 |
| `>D#` | `*Did#` | Light off |
| `>Bxxx#` | `*Bxxx#` | Set brightness, 0–255 |
| `>J#` | `*Jxxx#` | Get brightness |
| `>O#` | `*OOpened#` | Open cover, blocking, fixed confirmation text |
| `>C#` | `*CClosed#` | Close cover, blocking, fixed confirmation text |
| `>A#` | `*Ax#` | Is a closed/opened position calibrated? `x`=`0` not ready, `1` ready |
| `>Tx#` | *(none)* | Beep: `x`=`0` off, `1` on |
| `>Yx#` | *(none)* | Brightness bank: `x`=`0` low, `1` high |
| `>Mnnn#` | *(none)* | Jog by a signed relative amount (see the scaling note below) |
| `>F#` | *(none)* | Save the current position as Closed |
| `>E#` | *(none)* | Save the current position as Opened |

**Jog scaling (`>Mnnn#`):** unlike Pro (below), Rev2-class firmware computes the jog's
effect using a single fixed multiplier baked into that firmware build, applied
directly to the argument rather than derived from a live sensor reading. This
multiplier is not the same across every Rev2-class build (`31` for firmware `408`,
`60` for firmware `405`), so this project selects it based on the exact detected
firmware version rather than assuming one constant for every Rev2 unit. Firmware
versions other than these two fall back to treating the value as an opaque
pass-through. See `JogScaleFactor()` in `protocol_rev2.go`.

---

## Lite (non-motorized, static panel)

* **Terminator / command format:** same as Rev2 — `#`-terminated, unpadded
* **Handshake:** `>H#` → `*HGeminiFlatPanelLite#`
* **Firmware version:** `>V#` → `*Vvvv#`; this project requires $vvv \geq 205$
* **Status:** `>S#` → `*SLMB#` — **a completely different layout from Rev2/Pro**:
  `L`=light (0/1), `M`=brightness bank (0 low/1 high), `B`=beep (0/1). There is no
  motor/cover/id field at all, since there's no physical cover to report on.
* **Light and brightness:** `>L#`/`>D#`/`>Bxxx#`/`>J#` — **byte-identical to Rev2**:
  same unpadded command format, same reply layout, full 0–510 range including the
  low/high bank crossover at 255.
* **Beep, brightness mode:** same commands as Rev2 (`>Tx#`, `>Yx#`) — supported
* **Open/close/jog/set-position/ready-query:** **not supported.** There is no motor,
  so this project reports the ASCOM `CoverState` as `NotPresent` and refuses any
  cover-related request with a clear error rather than sending a command the firmware
  has no handler for.

---

## Pro (newest, motorized, asynchronous cover)

| Command | Reply | Notes |
| :--- | :--- | :--- |
| `>H#` | `*HGeminiFlatPanelPro#` | Handshake |
| `>V#` | `*V107#` | Firmware version; this project accepts any value (no minimum) |
| `>S#` | `*S{ms}M{ls}L{cs}C{dp}D{ca}C{oa}O#` | Status: motor (0 stopped/nonzero moving), light (0/1), cover (0 moving, 1 closed, 2 open, 3 timed out), a dew/heater-adjacent digit, the closed-position calibration value, the open-position calibration value |
| `>L#` | `*Lxxx#` | Light on |
| `>D#` | `*Did#` | Light off |
| `>Bxxx#` | `*Bxxx#` | Set brightness, 0–255 |
| `>J#` | `*Jxxx#` | Get brightness |
| `>O#` | `*O<adc>#` | Open cover. **Asynchronous**: the reply only arrives once the physical move actually finishes (can take up to ~20s), not immediately on receipt — this project polls `>S#` as a fallback rather than trusting a fixed timeout. |
| `>C#` | `*C<adc>#` | Close cover, same asynchronous behavior |
| `>Tx#` | *(none)* | Beep on/off |
| `>X` + 5×3 digits + `#` | *(none)* | Sets the five IR-remote brightness presets. Not used by this project — no IR remote accessory involved. |
| `>Yx#` | *(none)* | Brightness bank (low/high) |
| `>F#` | `*F<adc>#` | Save the current position as Closed |
| `>E#` | `*E<adc>#` | Save the current position as Opened |
| `>M±d#` | `*O<adc>#` | Jog by a relative amount in physical degrees (firmware converts to steps internally, no backlash compensation) — see the scaling note below. Reply uses the `O` token regardless of direction, and only arrives once the move settles. |
| `>R#` | `*R<adc>#` | Auto-calibrate Open: drives to the physical hard stop and measures it, ~20–25s |
| `>N#` | `*N<adc>#` | Auto-calibrate Closed, same behavior |
| `>K#` | *(none)* | Stop the motor immediately. See the Halt notes below — this is not a safe "cancel" during auto-calibrate. |
| `>G#` | `*G<adc>#` | Read the current live position. Not used by this project's own API surface today. |
| `>Wxx#` | *(none)* | Dew heater power, 0–100 percent |
| `>U#` / `>Uxxx#` | `*U<zero>#` | Query/set the MA22 position sensor's zero offset (0–1023). Changing it shifts the stored closed/open calibration values to stay valid. Not exposed as a user-facing setting in this project. |
| `>P#` / `>Pxxx#` | `*P<steps>#` | Query/set the gear-backlash compensation step count. Accepted range is 0–240 in practice (Gemini's documentation states 0–200). Not exposed as a user-facing setting. |

**Jog scaling (`>M±d#`):** the wire argument is physical degrees, which the firmware
converts to motor steps itself. This project's own `JogMotor()` takes its angle
argument in the *same raw units `closedAngle`/`openAngle` use*, not physical degrees, so
it divides by a fixed ratio (~5.4 raw units per degree for firmware `107` — the
ADC-counts-per-degree of this panel's position sensor) before sending the command.
Firmware versions other than `107` fall back to treating the value as an opaque
pass-through. See `JogScaleFactor()` in `protocol_pro.go`.

**Halt (`>K#`) notes:**
* Interrupting a plain Open/Close is safe — the motor stops within ~1–2s and
  `closedAngle`/`openAngle` are unaffected — but the very next `>S#` reply's cover-state
  classification can stay wrong for an extended period afterward (not just a brief
  settling delay). This project tracks that explicitly (`coverStateUnreliable` in
  `internal/serial/serial.go`) rather than trusting the first post-halt status.
* Interrupting an auto-calibrate sweep (`>N#`/`>R#`) is **not** a safe cancel: the
  firmware immediately saves wherever the motor was as the new calibration value,
  unable to distinguish a forced stop from genuinely reaching the hard stop (both use
  the same stall-detection completion path). A fresh, uninterrupted auto-calibrate is
  needed afterward to get a trustworthy value again.

---

## Cross-revision quick reference

| | Rev2 | Lite | Pro |
| :--- | :--- | :--- | :--- |
| Terminator | `#` | `#` | `#` |
| Command padding | unpadded | unpadded | unpadded |
| Handshake | `>H#`→`*HGeminiFlatPanel#` | `>H#`→`*HGeminiFlatPanelLite#` | `>H#`→`*HGeminiFlatPanelPro#` |
| Firmware version | `>V#`, ≥402 required | `>V#`, ≥205 required | `>V#`, any value |
| Ready/calibrated query | `>A#` | — (always ready) | — (no equivalent command exists) |
| Status layout | `*S` id(2) M L C | `*S` L M(bank) B(beep) | `*S` M _ L _ C _ dew _ closed _ open |
| Motorized cover | yes, blocking | no | yes, **asynchronous** |
| Halt/stop command | none known | n/a | `>K#` (see notes above) |
| Beep / brightness bank | supported | supported | supported |
| Dew heater | not supported | not supported | `>Wxx#` |
| Auto-calibrate | not supported | not supported | `>N#`/`>R#` |
| Position-set reply (`>F#`/`>E#`) | none | n/a | replies with the reached position |

See `internal/serial/protocol_test.go` for unit tests covering command formatting and
response parsing against the literal example strings from this document.
