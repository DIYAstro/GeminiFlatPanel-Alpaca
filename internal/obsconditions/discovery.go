package obsconditions

import (
	"geminiflatpanel/internal/alpacadiscovery"
	"geminiflatpanel/internal/logger"
)

// DiscoveredDevice is one ObservingConditions device found on the local network via
// Alpaca UDP discovery.
type DiscoveredDevice struct {
	BaseURL      string `json:"baseUrl"` // e.g. "http://192.168.1.50:11111", ready to use as observingConditionsUrl
	DeviceNumber int    `json:"deviceNumber"`
	DeviceName   string `json:"deviceName"`
}

// Discover broadcasts an Alpaca discovery request on the local network
// (internal/alpacadiscovery) and returns every ObservingConditions device found among
// the responders' exposed Alpaca servers.
func Discover() ([]DiscoveredDevice, error) {
	responders, err := alpacadiscovery.Broadcast()
	if err != nil {
		return nil, err
	}

	logger.Debug("Discovery: %d Alpaca server(s) responded to the UDP broadcast: %v", len(responders), responders)

	var devices []DiscoveredDevice
	for _, baseURL := range responders {
		// Best-effort: a responder that fails the follow-up management API query
		// (slow, TCP-unreachable despite answering UDP, etc.) is just skipped
		// rather than failing the whole scan.
		found, err := alpacadiscovery.QueryConfiguredDevices(baseURL, "ObservingConditions")
		if err != nil {
			logger.Debug("Discovery: failed to query %s for its configured devices: %v", baseURL, err)
			continue
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
