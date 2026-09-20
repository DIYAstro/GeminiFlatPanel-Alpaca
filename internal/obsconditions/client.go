// Package obsconditions is a minimal Alpaca ObservingConditions *client* — this
// project has no outbound-HTTP or Alpaca-client code anywhere else (verified before
// writing this: internal/alpaca/ only implements this proxy's own server-side
// devices), so this is hand-rolled from the Alpaca REST spec rather than reusing an
// existing pattern.
//
// Per the ASCOM spec (ascom-standards.org/newdocs/observingconditions.html, checked
// directly): DewPoint is only guaranteed non-throwing when Humidity is also
// implemented (a coupled pair), but Temperature is a *separate*, independently
// optional property — a real device could implement one without the other. So
// Temperature and DewPoint are fetched and error-checked independently below, never
// assumed to come as a package deal.
package obsconditions

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Reading is a single fetched sample from an external ObservingConditions device.
type Reading struct {
	Temperature float64
	DewPoint    float64
}

// alpacaEnvelope mirrors the standard Alpaca JSON response shape for a numeric
// property GET. Value is decoded loosely (json.Number) since some drivers return
// integers for whole-number readings.
type alpacaEnvelope struct {
	Value        json.Number `json:"Value"`
	ErrorNumber  int         `json:"ErrorNumber"`
	ErrorMessage string      `json:"ErrorMessage"`
}

// ASCOM Alpaca error numbers meaning "this property isn't implemented by this
// device" (0x400 NotImplementedException, 0x40C ActionNotImplementedException) —
// treated as an expected, named failure rather than a hard error, matching how this
// project's own server side classifies the same codes (internal/alpaca/responses.go).
const (
	errNotImplemented       = 0x400
	errActionNotImplemented = 0x40C
)

var httpClient = &http.Client{Timeout: 10 * time.Second}
var clientTransactionID uint32

func nextTransactionID() uint32 {
	return atomic.AddUint32(&clientTransactionID, 1)
}

// FetchReading queries an external Alpaca ObservingConditions device (deviceNumber,
// usually 0) at baseURL for Temperature and DewPoint. baseURL is just the host, e.g.
// "http://192.168.1.50:11111" — no trailing slash or /api/... suffix needed.
//
// Returns an error if the device is unreachable, either property is genuinely
// unavailable (ASCOM NotImplemented), or either GET reports a fault. Callers treat any
// error here as "this source isn't usable right now" and fall back accordingly.
func FetchReading(baseURL string, deviceNumber int) (Reading, error) {
	if strings.TrimSpace(baseURL) == "" {
		return Reading{}, fmt.Errorf("no ObservingConditions URL configured")
	}
	base := strings.TrimRight(baseURL, "/")

	// Best-effort "connect" — some Alpaca drivers require an explicit Connected=true
	// PUT before other calls succeed (this project's own CoverCalibrator/Switch
	// devices do, via checkConnection()); others don't care. Either way, a failure
	// here isn't fatal on its own — the property GETs below are the real test of
	// whether this device actually works.
	putConnected(base, deviceNumber)

	temp, err := getFloatProperty(base, deviceNumber, "temperature")
	if err != nil {
		return Reading{}, fmt.Errorf("temperature: %w", err)
	}
	dewPoint, err := getFloatProperty(base, deviceNumber, "dewpoint")
	if err != nil {
		return Reading{}, fmt.Errorf("dewpoint: %w", err)
	}

	return Reading{Temperature: temp, DewPoint: dewPoint}, nil
}

func putConnected(base string, deviceNumber int) {
	endpoint := fmt.Sprintf("%s/api/v1/observingconditions/%d/connected", base, deviceNumber)
	form := url.Values{
		"Connected":           {"true"},
		"ClientID":            {"1"},
		"ClientTransactionID": {strconv.FormatUint(uint64(nextTransactionID()), 10)},
	}
	req, err := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func getFloatProperty(base string, deviceNumber int, property string) (float64, error) {
	endpoint := fmt.Sprintf(
		"%s/api/v1/observingconditions/%d/%s?ClientID=1&ClientTransactionID=%d",
		base, deviceNumber, property, nextTransactionID(),
	)
	resp, err := httpClient.Get(endpoint)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	var env alpacaEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}

	if env.ErrorNumber == errNotImplemented || env.ErrorNumber == errActionNotImplemented {
		return 0, fmt.Errorf("not implemented by this device: %s", env.ErrorMessage)
	}
	if env.ErrorNumber != 0 {
		return 0, fmt.Errorf("device reported error 0x%x: %s", env.ErrorNumber, env.ErrorMessage)
	}

	value, err := env.Value.Float64()
	if err != nil {
		return 0, fmt.Errorf("non-numeric value %q: %w", env.Value.String(), err)
	}
	return value, nil
}
