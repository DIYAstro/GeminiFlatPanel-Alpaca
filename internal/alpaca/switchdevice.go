package alpaca

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"geminiflatpanel/internal/serial"
)

// Switch device (ISwitchV2, device number 0): exposes the Pro panel's dew heater as a
// single "rheostat" channel (Id 0) rather than a boolean on/off outlet — per the ASCOM
// spec (https://ascom-standards.org/newdocs/switch-faq.html), a channel becomes a
// rheostat purely by declaring MinSwitchValue/MaxSwitchValue/SwitchStep spanning more
// than the trivial 0/1-step-1 boolean case; clients render it as a slider once they see
// that range. SwitchStep is 10 to match the vendor driver's own "Heater — Power Percent"
// slider, even though the firmware itself accepts any 0-100 value — SetSwitchValue
// still rounds to the nearest step per spec, nothing is actually lost.
//
// Reports interfaceversion 2 (ISwitchV2) — already covers everything used here, same
// convention as CoverCalibrator reporting ICoverCalibratorV1 rather than the newer V2.
//
// This is a thin wrapper: all state lives in serial.SetHeaterPower()/GetHeaterPower(),
// the same functions the existing /api/custom/heater endpoint and dashboard card use.

const heaterSwitchID = 0

func (a *API) HandleSwitchInterfaceVersion(w http.ResponseWriter, r *http.Request) {
	IntResponse(w, r, 2)
}

func (a *API) HandleSwitchSupportedActions(w http.ResponseWriter, r *http.Request) {
	StringListResponse(w, r, []string{})
}

// checkSwitchID reads the required "Id" parameter and validates it's the only channel
// this device has (0). Returns false (having already written an error response) if the
// parameter is missing, unparsable, or out of range. Uses GetFormValueByAlpacaRule
// rather than GetFormValueStrict because this is the one parameter read on both GET
// (every property getter) and PUT (SetSwitch/SetSwitchValue) — GET callers must accept
// any casing, confirmed by ConformU's "Check Alpaca Protocol" test.
func (a *API) checkSwitchID(w http.ResponseWriter, r *http.Request) bool {
	idStr, ok, err := GetFormValueByAlpacaRule(r, "Id")
	if err != nil {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, err.Error())
		return false
	}
	if !ok {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, "Missing Id parameter")
		return false
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id != heaterSwitchID {
		InvalidValueResponse(w, r, 0x401, fmt.Sprintf("Invalid Id '%s': this device has 1 switch (Id 0)", idStr))
		return false
	}
	return true
}

func (a *API) HandleSwitchMaxSwitch(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	IntResponse(w, r, 1)
}

func (a *API) HandleSwitchCanWrite(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	// Honest per-panel capability, same idiom as noCoverError: false on Rev2/Lite,
	// which have no >Wxx# handler at all.
	BoolResponse(w, r, serial.SupportsHeater())
}

func (a *API) HandleSwitchGetName(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	StringResponse(w, r, "Heater Power")
}

func (a *API) HandleSwitchGetDescription(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	StringResponse(w, r, "Pro panel dew-heater output, 0-100% duty cycle")
}

func (a *API) HandleSwitchMinValue(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	FloatResponse(w, r, 0)
}

func (a *API) HandleSwitchMaxValue(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	FloatResponse(w, r, 100)
}

func (a *API) HandleSwitchStep(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	FloatResponse(w, r, 10)
}

func (a *API) HandleSwitchGetValue(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	FloatResponse(w, r, float64(serial.GetHeaterPower()))
}

// HandleSwitchSetValue implements SetSwitchValue: rounds to the nearest 10 (this
// device's SwitchStep) before sending, per the ASCOM spec's rounding requirement.
func (a *API) HandleSwitchSetValue(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if r.Method != "PUT" {
		ErrorResponse(w, r, http.StatusOK, 0x400, "Method not allowed")
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	if !serial.SupportsHeater() {
		ErrorResponse(w, r, http.StatusOK, 0x400, "SetSwitchValue is not implemented: this panel has no dew-heater output.")
		return
	}

	valStr, ok, err := GetFormValueStrict(r, "Value")
	if err != nil {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, err.Error())
		return
	}
	if !ok {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, "Missing Value parameter")
		return
	}
	value, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		InvalidValueResponse(w, r, 0x401, fmt.Sprintf("Invalid Value '%s'", valStr))
		return
	}
	percent := int(math.Round(value/10) * 10)
	if err := serial.SetHeaterPower(percent); err != nil {
		ErrorResponse(w, r, http.StatusOK, 0x500, err.Error())
		return
	}
	EmptyResponse(w, r)
}

// HandleSwitchSetSwitchName implements SetSwitchName. Per the ASCOM ISwitchV2 spec,
// verified directly against ConformU's own SwitchTester.cs (which classifies this
// member Required.Optional — the same category CoverCalibrator's HaltCover already
// uses in this project), a device may throw MethodNotImplementedException here rather
// than actually support renaming. This device's one channel is meaningfully tied to
// what it actually is (the Pro panel's dew heater); a client-supplied rename wouldn't
// change anything useful, so not implemented beats either silently discarding a name
// the client thinks it set (in-memory only) or persisting a config field for it.
func (a *API) HandleSwitchSetSwitchName(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if r.Method != "PUT" {
		ErrorResponse(w, r, http.StatusOK, 0x400, "Method not allowed")
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	ErrorResponse(w, r, http.StatusOK, 0x400, "SetSwitchName is not implemented: this switch's name is fixed.")
}

func (a *API) HandleSwitchGetSwitch(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	// Spec: false only when the value is at MinSwitchValue.
	BoolResponse(w, r, serial.GetHeaterPower() > 0)
}

// HandleSwitchSetSwitch implements the boolean SetSwitch: true jumps to
// MaxSwitchValue (100), false to MinSwitchValue (0), per the ASCOM spec's
// boolean-compatibility requirement for rheostat-type switches.
func (a *API) HandleSwitchSetSwitch(w http.ResponseWriter, r *http.Request) {
	if !a.checkConnection(w, r) {
		return
	}
	if r.Method != "PUT" {
		ErrorResponse(w, r, http.StatusOK, 0x400, "Method not allowed")
		return
	}
	if !a.checkSwitchID(w, r) {
		return
	}
	if !serial.SupportsHeater() {
		ErrorResponse(w, r, http.StatusOK, 0x400, "SetSwitch is not implemented: this panel has no dew-heater output.")
		return
	}

	stateStr, ok, err := GetFormValueStrict(r, "State")
	if err != nil {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, err.Error())
		return
	}
	if !ok {
		ErrorResponse(w, r, http.StatusBadRequest, 0x400, "Missing State parameter")
		return
	}
	state, err := strconv.ParseBool(stateStr)
	if err != nil {
		InvalidValueResponse(w, r, 0x401, fmt.Sprintf("Invalid State '%s'", stateStr))
		return
	}
	percent := 0
	if state {
		percent = 100
	}
	if err := serial.SetHeaterPower(percent); err != nil {
		ErrorResponse(w, r, http.StatusOK, 0x500, err.Error())
		return
	}
	EmptyResponse(w, r)
}
