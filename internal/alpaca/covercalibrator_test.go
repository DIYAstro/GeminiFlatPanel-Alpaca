package alpaca

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHandleCoverCalibratorOnAndOff(t *testing.T) {
	api := NewAPI("dev")
	// Note: serial connection is mocked or we can just see how the request parsing handles things.
	// Since checkConnection will return false if serial is not connected, let's look at that.
	
	// Create request with query parameters (GET/PUT)
	req := httptest.NewRequest("PUT", "/api/v1/covercalibrator/0/calibratoron", nil)
	w := httptest.NewRecorder()
	
	// Let's test request parsing via Handler middleware.
	handler := Handler(api.HandleCoverCalibratorOn)
	
	// Test 1: Missing Brightness parameter
	handler(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
	
	// Test 2: Valid Brightness parameter in URL query
	req = httptest.NewRequest("PUT", "/api/v1/covercalibrator/0/calibratoron?Brightness=100", nil)
	w = httptest.NewRecorder()
	handler = Handler(func(w http.ResponseWriter, r *http.Request) {
		valStr, ok, err := GetFormValueStrict(r, "Brightness")
		if err != nil || !ok || valStr != "100" {
			t.Errorf("Expected Brightness=100, got valStr=%s, ok=%t, err=%v", valStr, ok, err)
		}
	})
	handler(w, req)

	// Test 3: Valid Brightness parameter in Body
	form := url.Values{}
	form.Add("Brightness", "150")
	req = httptest.NewRequest("PUT", "/api/v1/covercalibrator/0/calibratoron", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	handler = Handler(func(w http.ResponseWriter, r *http.Request) {
		valStr, ok, err := GetFormValueStrict(r, "Brightness")
		if err != nil || !ok || valStr != "150" {
			t.Errorf("Expected Brightness=150 in body, got valStr=%s, ok=%t, err=%v", valStr, ok, err)
		}
	})
	handler(w, req)

	// Test 4: Valid Brightness parameter in JSON Body
	jsonBody := `{"Brightness": 200, "ClientTransactionID": 888}`
	req = httptest.NewRequest("PUT", "/api/v1/covercalibrator/0/calibratoron", strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler = Handler(func(w http.ResponseWriter, r *http.Request) {
		valStr, ok, err := GetFormValueStrict(r, "Brightness")
		if err != nil || !ok || valStr != "200" {
			t.Errorf("Expected Brightness=200 in JSON body, got valStr=%s, ok=%t, err=%v", valStr, ok, err)
		}
		txID := GetClientTransactionID(r)
		if txID != 888 {
			t.Errorf("Expected ClientTransactionID=888, got %d", txID)
		}
	})
	handler(w, req)

	// Test 5: Invalid parameter casing (Brightness vs brightness)
	req = httptest.NewRequest("PUT", "/api/v1/covercalibrator/0/calibratoron?brightness=200", nil)
	w = httptest.NewRecorder()
	handler = Handler(func(w http.ResponseWriter, r *http.Request) {
		_, _, err := GetFormValueStrict(r, "Brightness")
		if err == nil {
			t.Error("Expected error for incorrect parameter casing, but got nil")
		}
	})
	handler(w, req)
}

// TestHandleConnectedNotLatchedTrueOnFailedConnect locks down a real bug: a PUT
// Connected=true while the serial device isn't actually connected must fail (as it
// already did), but must also NOT leave driverConnected latched true — otherwise a
// later successful background reconnect (independent of this request) would make a
// subsequent GET Connected report true even though this client's own connect attempt
// never succeeded and it never retried. The test process has no real serial device, so
// serial.IsConnected() is reliably false here without any mocking.
func TestHandleConnectedNotLatchedTrueOnFailedConnect(t *testing.T) {
	api := NewAPI("dev")
	handler := Handler(api.HandleConnected)

	form := url.Values{}
	form.Add("Connected", "true")
	req := httptest.NewRequest("PUT", "/api/v1/covercalibrator/0/connected", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler(w, req)

	var putResp Response
	if err := json.NewDecoder(w.Result().Body).Decode(&putResp); err != nil {
		t.Fatalf("decoding PUT response: %v", err)
	}
	if putResp.ErrorNumber == 0 {
		t.Fatalf("PUT Connected=true while serial is disconnected: expected an Alpaca error, got none")
	}

	getReq := httptest.NewRequest("GET", "/api/v1/covercalibrator/0/connected", nil)
	getW := httptest.NewRecorder()
	handler(getW, getReq)

	var getResp ValueResponse
	if err := json.NewDecoder(getW.Result().Body).Decode(&getResp); err != nil {
		t.Fatalf("decoding GET response: %v", err)
	}
	if connected, ok := getResp.Value.(bool); !ok || connected {
		t.Errorf("GET Connected after a failed PUT Connected=true = %v, want false", getResp.Value)
	}
}
