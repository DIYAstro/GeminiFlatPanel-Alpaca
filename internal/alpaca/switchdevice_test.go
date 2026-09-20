package alpaca

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestCheckSwitchIDCasing locks down a real ConformU-caught bug: a GET request for any
// Switch property (e.g. CanWrite?iD=0) must be accepted regardless of the "Id"
// parameter's casing, while a PUT request (SetSwitch/SetSwitchValue) must keep
// rejecting anything but the exact "Id" casing, per the Alpaca spec's asymmetric rule.
func TestCheckSwitchIDCasing(t *testing.T) {
	api := NewAPI("dev")

	t.Run("GET with correct casing succeeds", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/switch/0/canwrite?Id=0", nil)
		w := httptest.NewRecorder()
		handler := Handler(func(w http.ResponseWriter, r *http.Request) {
			if !api.checkSwitchID(w, r) {
				t.Error("expected checkSwitchID to succeed with correct 'Id' casing on GET")
			}
		})
		handler(w, req)
	})

	t.Run("GET with inverted casing still succeeds", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/switch/0/canwrite?iD=0", nil)
		w := httptest.NewRecorder()
		handler := Handler(func(w http.ResponseWriter, r *http.Request) {
			if !api.checkSwitchID(w, r) {
				t.Error("expected checkSwitchID to succeed with inverted 'iD' casing on GET (Alpaca spec: GET query params are case-insensitive)")
			}
		})
		handler(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Errorf("expected HTTP 200, got %d", w.Result().StatusCode)
		}
	})

	t.Run("GET with lowercase casing still succeeds", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/switch/0/canwrite?id=0", nil)
		w := httptest.NewRecorder()
		handler := Handler(func(w http.ResponseWriter, r *http.Request) {
			if !api.checkSwitchID(w, r) {
				t.Error("expected checkSwitchID to succeed with lowercase 'id' casing on GET")
			}
		})
		handler(w, req)
	})

	t.Run("PUT with inverted casing is rejected", func(t *testing.T) {
		form := url.Values{}
		form.Add("iD", "0")
		req := httptest.NewRequest("PUT", "/api/v1/switch/0/setswitchvalue", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler := Handler(func(w http.ResponseWriter, r *http.Request) {
			if api.checkSwitchID(w, r) {
				t.Error("expected checkSwitchID to reject inverted 'iD' casing on PUT (Alpaca spec: PUT body params are case-sensitive)")
			}
		})
		handler(w, req)
		if w.Result().StatusCode != http.StatusBadRequest {
			t.Errorf("expected HTTP 400, got %d", w.Result().StatusCode)
		}
	})

	t.Run("PUT with correct casing succeeds", func(t *testing.T) {
		form := url.Values{}
		form.Add("Id", "0")
		req := httptest.NewRequest("PUT", "/api/v1/switch/0/setswitchvalue", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler := Handler(func(w http.ResponseWriter, r *http.Request) {
			if !api.checkSwitchID(w, r) {
				t.Error("expected checkSwitchID to succeed with correct 'Id' casing on PUT")
			}
		})
		handler(w, req)
	})
}

// TestHandleSwitchSetSwitchNameNotImplemented locks down the ConformU-caught 404: this
// project had no route for SetSwitchName at all. It's a real ISwitchV2 member, but
// ConformU's own SwitchTester.cs classifies it Required.Optional (same category as
// CoverCalibrator's HaltCover), so a MethodNotImplementedException — HTTP 200 with an
// Alpaca ErrorNumber in the body — is a spec-compliant response, not a 404. The test
// process has no live serial connection, so checkConnection's own 0x40B ("not
// connected") fires before HandleSwitchSetSwitchName's 0x400 — same limitation already
// accepted by TestHandleCoverCalibratorOnAndOff above. That's still exactly what this
// test needs to lock down: a real, structured Alpaca error response (any ASCOM
// ErrorNumber, HTTP 200) instead of the previous blanket 404 "not found".
func TestHandleSwitchSetSwitchNameNotImplemented(t *testing.T) {
	api := NewAPI("dev")

	form := url.Values{}
	form.Add("Id", "0")
	form.Add("Name", "Custom Name")
	req := httptest.NewRequest("PUT", "/api/v1/switch/0/setswitchname", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler := Handler(api.HandleSwitchSetSwitchName)
	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected HTTP 200 (Alpaca convention: domain errors ride in the body), got %d", resp.StatusCode)
	}

	var body struct {
		ErrorNumber  int    `json:"ErrorNumber"`
		ErrorMessage string `json:"ErrorMessage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if body.ErrorNumber == 0 {
		t.Error("expected a non-zero ASCOM ErrorNumber (route must no longer 404)")
	}
	if body.ErrorMessage == "" {
		t.Error("expected a non-empty ErrorMessage")
	}
}
