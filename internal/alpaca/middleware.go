package alpaca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"geminiflatpanel/internal/logger"
)

type contextKey string

const clientTxIDKey contextKey = "ClientTransactionID"

// Handler is a middleware that wraps HTTP handlers to provide Alpaca-specific functionality.
// It parses parameters from URL-encoded form data or JSON body, and stores the ClientTransactionID in the request context.
func Handler(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("HTTP Request: %s %s", r.Method, r.URL.Path)

		// 1. Detect and parse JSON request body if present
		contentType := r.Header.Get("Content-Type")
		isJSON := strings.Contains(strings.ToLower(contentType), "application/json")
		if isJSON {
			// Limit body to 1MB to prevent memory exhaustion DoS
			bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
			if err == nil {
				// Restore body so other handlers/middlewares can read it if needed
				r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

				var jsonMap map[string]interface{}
				if err := json.Unmarshal(bodyBytes, &jsonMap); err == nil {
					if r.Form == nil {
						r.Form = make(url.Values)
					}
					for k, v := range jsonMap {
						var strVal string
						switch val := v.(type) {
						case string:
							strVal = val
						case float64:
							if val == float64(int64(val)) {
								strVal = strconv.FormatInt(int64(val), 10)
							} else {
								strVal = strconv.FormatFloat(val, 'f', -1, 64)
							}
						case bool:
							strVal = strconv.FormatBool(val)
						case nil:
							strVal = ""
						default:
							strVal = fmt.Sprintf("%v", val)
						}
						r.Form.Add(k, strVal)
					}
				}
			}
		}

		// 2. Parse standard URL-encoded form data (URL query string and body if form-encoded)
		if isJSON {
			if r.Form == nil {
				r.Form = make(url.Values)
			}
			// Manually populate query parameters to avoid running standard ParseForm which will attempt
			// to parse the body as form-encoded and return form parsing errors.
			for k, vs := range r.URL.Query() {
				for _, v := range vs {
					r.Form.Add(k, v)
				}
			}
		} else {
			if err := r.ParseForm(); err != nil {
				logger.Debug("ParseForm info (normal for some content types): %v", err)
			}
		}

		// 3. Extract ClientTransactionID
		var clientTxID uint32
		if txIDStr, ok := GetFormValueIgnoreCase(r, "ClientTransactionID"); ok {
			if txID, err := strconv.ParseUint(txIDStr, 10, 32); err == nil {
				clientTxID = uint32(txID)
			}
		}

		// Store in request context
		ctx := context.WithValue(r.Context(), clientTxIDKey, clientTxID)
		r = r.WithContext(ctx)

		fn(w, r)
	}
}

// GetClientTransactionID retrieves the parsed ClientTransactionID from the request context.
func GetClientTransactionID(r *http.Request) uint32 {
	if r == nil {
		return 0
	}
	if val, ok := r.Context().Value(clientTxIDKey).(uint32); ok {
		return val
	}
	return 0
}

// GetFormValueIgnoreCase retrieves the first value for a given key from the request form, case-insensitively.
// The Alpaca specification requires parameter names to be case-insensitive.
func GetFormValueIgnoreCase(r *http.Request, key string) (string, bool) {
	if r.Form == nil {
		return "", false
	}
	for k, values := range r.Form {
		if strings.EqualFold(k, key) {
			if len(values) > 0 {
				return values[0], true
			}
			return "", true // Key exists but has no value.
		}
	}
	return "", false
}

// GetFormValueByAlpacaRule resolves a parameter using the Alpaca spec's asymmetric
// casing rule, confirmed the hard way by a ConformU "Check Alpaca Protocol" run against
// the Switch device: GET query-string parameters must be accepted with any casing (the
// checker sends e.g. GET .../canwrite?iD=0 and requires 200 OK, not 400), while PUT
// body parameters stay strictly case-sensitive — ASCOM's own Remote server applies the
// same split (ASCOMInitiative/ASCOMRemote v6.6.8419 release notes: "The Switch Id
// parameter was previously accepted as 'ID' ... Now only 'Id' is accepted", for PUT).
// Behaves exactly like GetFormValueIgnoreCase on GET, and exactly like
// GetFormValueStrict otherwise.
func GetFormValueByAlpacaRule(r *http.Request, key string) (string, bool, error) {
	if r.Method == http.MethodGet {
		val, ok := GetFormValueIgnoreCase(r, key)
		return val, ok, nil
	}
	return GetFormValueStrict(r, key)
}

// GetFormValueStrict retrieves the value for a key strictly case-sensitively.
// If the key is found case-sensitively, it returns the value, true, and nil.
// If the key is found but has incorrect casing, it returns "", false, and an error.
// If the key is not found at all, it returns "", false, nil.
func GetFormValueStrict(r *http.Request, key string) (string, bool, error) {
	if r.Form == nil {
		return "", false, nil
	}
	
	// Check case-sensitive match
	if values, ok := r.Form[key]; ok {
		if len(values) > 0 {
			return values[0], true, nil
		}
		return "", true, nil
	}
	
	// Check if it exists with incorrect casing
	for k := range r.Form {
		if strings.EqualFold(k, key) {
			return "", false, fmt.Errorf("parameter '%s' has incorrect casing (expected '%s')", k, key)
		}
	}
	
	return "", false, nil
}
