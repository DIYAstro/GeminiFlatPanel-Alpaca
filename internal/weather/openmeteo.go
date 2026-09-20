// Package weather fetches ambient temperature and dew point from Open-Meteo
// (https://open-meteo.com), a free, keyless weather API. It's the fallback data
// source for internal/dewcontrol, used when no Alpaca ObservingConditions client is
// configured or reachable.
//
// This is a stripped-down, stateless fetch — a fuller Open-Meteo integration would
// typically poll and cache many more metrics for its own ObservingConditions server
// device; this one only ever needs 2 (Temperature, DewPoint) for the heating curve, and
// internal/dewcontrol already owns the polling loop and "last known reading" state, so
// no local service/cache is needed here.
package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Reading is a single fetched weather sample.
type Reading struct {
	Temperature float64
	DewPoint    float64
	Timestamp   time.Time
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Fetch retrieves current temperature and dew point for the given coordinates from
// Open-Meteo's free forecast endpoint. Dew point comes directly from the API
// ("dew_point_2m") — Open-Meteo computes it server-side, so no local Magnus-formula
// derivation is needed here.
func Fetch(lat, lon float64) (Reading, error) {
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.6f&longitude=%.6f&current=temperature_2m,dew_point_2m&timezone=auto",
		lat, lon,
	)

	resp, err := httpClient.Get(url)
	if err != nil {
		return Reading{}, fmt.Errorf("open-meteo request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return Reading{}, fmt.Errorf("open-meteo API error (%d): %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Current struct {
			Temperature float64 `json:"temperature_2m"`
			DewPoint    float64 `json:"dew_point_2m"`
		} `json:"current"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return Reading{}, fmt.Errorf("failed to decode open-meteo response: %w", err)
	}

	return Reading{
		Temperature: apiResp.Current.Temperature,
		DewPoint:    apiResp.Current.DewPoint,
		Timestamp:   time.Now(),
	}, nil
}
