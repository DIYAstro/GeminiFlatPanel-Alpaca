// Package dewheaterswitch is a minimal Alpaca Switch (ISwitchV2) *client*, used to
// drive an external dew heater via any generic ASCOM Alpaca switch device instead of
// this project's own built-in Pro heater. Hand-rolled from the Alpaca REST spec, the
// same approach internal/obsconditions already took for ObservingConditions — this
// project still has no outbound-HTTP client library, just per-device-type client
// packages following the same conventions.
package dewheaterswitch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"geminiflatpanel/internal/alpacadiscovery"
)

// DiscoveredDevice is one Switch device found on the local network via Alpaca UDP
// discovery.
type DiscoveredDevice struct {
	BaseURL      string `json:"baseUrl"`
	DeviceNumber int    `json:"deviceNumber"`
	DeviceName   string `json:"deviceName"`
}

// Channel is one writable switch channel on a Switch device, as needed to let the
// user pick one and to auto-detect whether it's a PWM-style rheostat or a plain
// boolean on/off outlet.
type Channel struct {
	ID         int     `json:"id"`
	Name       string  `json:"name"`
	MinValue   float64 `json:"minValue"`
	MaxValue   float64 `json:"maxValue"`
	Step       float64 `json:"step"`
	IsRheostat bool    `json:"isRheostat"`
}

// alpacaEnvelope mirrors the standard Alpaca JSON response shape for a property GET.
// Value is decoded loosely (json.RawMessage) since it can be a number, a bool, or a
// string depending on which property was requested.
type alpacaEnvelope struct {
	Value        json.RawMessage `json:"Value"`
	ErrorNumber  int             `json:"ErrorNumber"`
	ErrorMessage string          `json:"ErrorMessage"`
}

// ASCOM Alpaca error numbers meaning "this property/action isn't implemented by this
// device" -- treated as an expected, named failure rather than a hard error, matching
// how this project's own server side classifies the same codes
// (internal/alpaca/responses.go) and internal/obsconditions/client.go.
const (
	errNotImplemented       = 0x400
	errActionNotImplemented = 0x40C
)

var httpClient = &http.Client{Timeout: 10 * time.Second}
var clientTransactionID uint32

func nextTransactionID() uint32 {
	return atomic.AddUint32(&clientTransactionID, 1)
}

// Discover broadcasts an Alpaca discovery request on the local network
// (internal/alpacadiscovery) and returns every Switch device found among the
// responders' exposed Alpaca servers.
func Discover() ([]DiscoveredDevice, error) {
	responders, err := alpacadiscovery.Broadcast()
	if err != nil {
		return nil, err
	}

	var devices []DiscoveredDevice
	for _, baseURL := range responders {
		found, err := alpacadiscovery.QueryConfiguredDevices(baseURL, "Switch")
		if err != nil {
			continue // best-effort, same as obsconditions.Discover
		}
		for _, d := range found {
			devices = append(devices, DiscoveredDevice{
				BaseURL:      d.BaseURL,
				DeviceNumber: d.DeviceNumber,
				DeviceName:   d.DeviceName,
			})
		}
	}
	return devices, nil
}

// ListChannels queries a Switch device for its writable channels. A channel is
// considered a "rheostat" (PWM-style, 0-100-ish range) using the exact same heuristic
// this project's own server-side heater switch documents
// (internal/alpaca/switchdevice.go): its Min/Max/Step span more than the trivial
// 0/1-step-1 boolean case. Channels that report CanWrite=false are skipped entirely --
// a read-only channel can't be driven as a heater output.
func ListChannels(baseURL string, deviceNumber int) ([]Channel, error) {
	base := strings.TrimRight(baseURL, "/")
	putConnected(base, deviceNumber)

	maxSwitch, err := getIntProperty(base, deviceNumber, "maxswitch", -1)
	if err != nil {
		return nil, fmt.Errorf("maxswitch: %w", err)
	}

	var channels []Channel
	for id := 0; id < maxSwitch; id++ {
		canWrite, err := getBoolProperty(base, deviceNumber, "canwrite", id)
		if err != nil || !canWrite {
			continue
		}
		name, err := getStringProperty(base, deviceNumber, "getswitchname", id)
		if err != nil {
			continue
		}
		minVal, err := getFloatProperty(base, deviceNumber, "minswitchvalue", id)
		if err != nil {
			continue
		}
		maxVal, err := getFloatProperty(base, deviceNumber, "maxswitchvalue", id)
		if err != nil {
			continue
		}
		step, err := getFloatProperty(base, deviceNumber, "switchstep", id)
		if err != nil {
			continue
		}
		channels = append(channels, Channel{
			ID:         id,
			Name:       name,
			MinValue:   minVal,
			MaxValue:   maxVal,
			Step:       step,
			IsRheostat: isRheostat(minVal, maxVal, step),
		})
	}
	return channels, nil
}

// isRheostat reports whether a channel's declared range is anything other than the
// trivial boolean case (0-1, step 1) -- the same rule the ASCOM spec's switch-FAQ
// describes and this project's own switchdevice.go already implements server-side.
func isRheostat(minVal, maxVal, step float64) bool {
	return !(minVal == 0 && maxVal == 1 && step == 1)
}

// SetValue sends SetSwitchValue for a rheostat channel.
func SetValue(baseURL string, deviceNumber, switchID int, value float64) error {
	base := strings.TrimRight(baseURL, "/")
	endpoint := fmt.Sprintf("%s/api/v1/switch/%d/setswitchvalue", base, deviceNumber)
	form := url.Values{
		"Id":                  {strconv.Itoa(switchID)},
		"Value":               {strconv.FormatFloat(value, 'f', -1, 64)},
		"ClientID":            {"1"},
		"ClientTransactionID": {strconv.FormatUint(uint64(nextTransactionID()), 10)},
	}
	return putForm(endpoint, form)
}

// SetState sends SetSwitch for a boolean on/off channel.
func SetState(baseURL string, deviceNumber, switchID int, on bool) error {
	base := strings.TrimRight(baseURL, "/")
	endpoint := fmt.Sprintf("%s/api/v1/switch/%d/setswitch", base, deviceNumber)
	form := url.Values{
		"Id":                  {strconv.Itoa(switchID)},
		"State":               {strconv.FormatBool(on)},
		"ClientID":            {"1"},
		"ClientTransactionID": {strconv.FormatUint(uint64(nextTransactionID()), 10)},
	}
	return putForm(endpoint, form)
}

// GetValue reads back a rheostat channel's current value (GetSwitchValue).
func GetValue(baseURL string, deviceNumber, switchID int) (float64, error) {
	base := strings.TrimRight(baseURL, "/")
	return getFloatProperty(base, deviceNumber, "getswitchvalue", switchID)
}

// GetState reads back a boolean channel's current state (GetSwitch).
func GetState(baseURL string, deviceNumber, switchID int) (bool, error) {
	base := strings.TrimRight(baseURL, "/")
	return getBoolProperty(base, deviceNumber, "getswitch", switchID)
}

func putConnected(base string, deviceNumber int) {
	endpoint := fmt.Sprintf("%s/api/v1/switch/%d/connected", base, deviceNumber)
	form := url.Values{
		"Connected":           {"true"},
		"ClientID":            {"1"},
		"ClientTransactionID": {strconv.FormatUint(uint64(nextTransactionID()), 10)},
	}
	putForm(endpoint, form) // best-effort, same as obsconditions.putConnected
}

func putForm(endpoint string, form url.Values) error {
	req, err := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var env alpacaEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		// Some devices reply with an empty body on success -- not fatal.
		return nil
	}
	if env.ErrorNumber != 0 {
		return fmt.Errorf("device reported error 0x%x: %s", env.ErrorNumber, env.ErrorMessage)
	}
	return nil
}

// getProperty performs the shared GET+decode for every property below. id < 0 omits
// the Id query parameter (used only by maxswitch, which takes none).
func getProperty(base string, deviceNumber int, property string, id int) (alpacaEnvelope, error) {
	endpoint := fmt.Sprintf("%s/api/v1/switch/%d/%s?ClientID=1&ClientTransactionID=%d", base, deviceNumber, property, nextTransactionID())
	if id >= 0 {
		endpoint += fmt.Sprintf("&Id=%d", id)
	}
	resp, err := httpClient.Get(endpoint)
	if err != nil {
		return alpacaEnvelope{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return alpacaEnvelope{}, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	var env alpacaEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return alpacaEnvelope{}, fmt.Errorf("failed to decode response: %w", err)
	}
	if env.ErrorNumber == errNotImplemented || env.ErrorNumber == errActionNotImplemented {
		return alpacaEnvelope{}, fmt.Errorf("not implemented by this device: %s", env.ErrorMessage)
	}
	if env.ErrorNumber != 0 {
		return alpacaEnvelope{}, fmt.Errorf("device reported error 0x%x: %s", env.ErrorNumber, env.ErrorMessage)
	}
	return env, nil
}

func getFloatProperty(base string, deviceNumber int, property string, id int) (float64, error) {
	env, err := getProperty(base, deviceNumber, property, id)
	if err != nil {
		return 0, err
	}
	var value float64
	if err := json.Unmarshal(env.Value, &value); err != nil {
		return 0, fmt.Errorf("non-numeric value %q: %w", string(env.Value), err)
	}
	return value, nil
}

func getIntProperty(base string, deviceNumber int, property string, id int) (int, error) {
	value, err := getFloatProperty(base, deviceNumber, property, id)
	if err != nil {
		return 0, err
	}
	return int(value), nil
}

func getBoolProperty(base string, deviceNumber int, property string, id int) (bool, error) {
	env, err := getProperty(base, deviceNumber, property, id)
	if err != nil {
		return false, err
	}
	var value bool
	if err := json.Unmarshal(env.Value, &value); err != nil {
		return false, fmt.Errorf("non-boolean value %q: %w", string(env.Value), err)
	}
	return value, nil
}

func getStringProperty(base string, deviceNumber int, property string, id int) (string, error) {
	env, err := getProperty(base, deviceNumber, property, id)
	if err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(env.Value, &value); err != nil {
		return "", fmt.Errorf("non-string value %q: %w", string(env.Value), err)
	}
	return value, nil
}
