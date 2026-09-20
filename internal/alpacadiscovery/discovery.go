// Package alpacadiscovery implements the client side of Alpaca's UDP discovery
// protocol (the same protocol this project's own server answers via
// internal/alpaca/discovery.go: UDP port 32227, the literal message
// "alpacadiscovery1", a {"AlpacaPort": N} JSON reply), plus a helper for asking a
// discovered server which devices of a given type it hosts. Shared by every Alpaca
// *client* this project has (internal/obsconditions, internal/dewheaterswitch) so the
// broadcast/socket handling exists exactly once.
package alpacadiscovery

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"geminiflatpanel/internal/logger"
)

// ConfiguredDevice is one device of a requested DeviceType found on a discovered
// Alpaca server.
type ConfiguredDevice struct {
	BaseURL      string // e.g. "http://192.168.1.50:11111", ready to use as a client's base URL
	DeviceNumber int
	DeviceName   string
}

const (
	discoveryPort    = 32227
	discoveryMessage = "alpacadiscovery1"
	discoveryWindow  = 2 * time.Second
)

// Broadcast sends an Alpaca UDP discovery request on every local network interface
// (plus a direct localhost probe) and returns the base URL of every distinct Alpaca
// server that responded.
//
// A single socket bound to the wildcard address is not enough: Microsoft's own
// Winsock documentation confirms a 255.255.255.255 broadcast from such a socket is
// only actually sent out the OS's single highest-priority interface, never all of
// them. On any machine with more than one active NIC (Wi-Fi + Ethernet, a VPN
// adapter, etc.) that can silently mean the packet never reaches the interface the
// target device is actually on, even though the send call itself reports success —
// exactly the "no devices found" symptom this fixes. Binding a separate socket to
// each interface's own local address forces each broadcast out that specific NIC.
func Broadcast() ([]string, error) {
	localIPs := localIPv4Addresses()
	if len(localIPs) == 0 {
		// Interface enumeration failed for some reason -- fall back to a single
		// wildcard-bound socket rather than sending nothing at all.
		logger.Debug("Discovery: could not enumerate local network interfaces, falling back to a single wildcard broadcast.")
		localIPs = []string{""}
	}

	results := make(chan string, 64)
	var wg sync.WaitGroup
	for _, ip := range localIPs {
		wg.Add(1)
		go func(localIP string) {
			defer wg.Done()
			broadcastFromInterface(localIP, results)
		}(ip)
	}

	// Always also probe localhost directly, in addition to the interface
	// broadcasts above: a device running on this same machine is a common setup
	// (e.g. testing against a local Alpaca simulator), but Windows' loopback
	// interface has no "broadcast" capability at all (confirmed live: it lacks the
	// broadcast flag other NICs have), so a real 255.255.255.255 broadcast can't be
	// relied on to reach a responder bound to 127.0.0.1/0.0.0.0 on this host. A
	// plain unicast probe sidesteps that entirely.
	wg.Add(1)
	go func() {
		defer wg.Done()
		probeLocalhost(results)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	seen := make(map[string]bool)
	var hosts []string
	for host := range results {
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	return hosts, nil
}

// localIPv4Addresses returns every local, non-loopback IPv4 address across all
// currently-up network interfaces.
func localIPv4Addresses() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		logger.Debug("Discovery: failed to enumerate network interfaces: %v", err)
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue
			}
			ips = append(ips, ip4.String())
		}
	}
	return ips
}

// broadcastFromInterface sends one discovery broadcast from a socket bound to
// localIP (or the wildcard address if localIP is "") and streams every distinct
// responder's base URL into results until discoveryWindow elapses.
func broadcastFromInterface(localIP string, results chan<- string) {
	localAddr, err := net.ResolveUDPAddr("udp4", localIP+":0")
	if err != nil {
		logger.Debug("Discovery: failed to resolve local address '%s': %v", localIP, err)
		return
	}
	broadcastAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("255.255.255.255:%d", discoveryPort))
	if err != nil {
		return
	}
	sendAndCollectReplies(localAddr, broadcastAddr, results, fmt.Sprintf("broadcast from %s", localIP))
}

// probeLocalhost sends a plain unicast discovery request to 127.0.0.1, for a device
// running on this same machine — see the comment in Broadcast for why the interface
// broadcasts above can't be relied on to cover this case too.
func probeLocalhost(results chan<- string) {
	localAddr, err := net.ResolveUDPAddr("udp4", ":0")
	if err != nil {
		logger.Debug("Discovery: failed to resolve a local address for the localhost probe: %v", err)
		return
	}
	targetAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("127.0.0.1:%d", discoveryPort))
	if err != nil {
		return
	}
	sendAndCollectReplies(localAddr, targetAddr, results, "localhost probe")
}

// sendAndCollectReplies binds a UDP socket at localAddr, sends the discovery message
// to targetAddr, and streams every valid Alpaca discovery reply's base URL into
// results until discoveryWindow elapses. logContext is only used for debug logging.
func sendAndCollectReplies(localAddr, targetAddr *net.UDPAddr, results chan<- string, logContext string) {
	conn, err := net.ListenUDP("udp4", localAddr)
	if err != nil {
		// Common and expected for e.g. link-local/disconnected adapters -- debug only.
		logger.Debug("Discovery: failed to bind UDP socket (%s): %v", logContext, err)
		return
	}
	defer conn.Close()

	if _, err := conn.WriteTo([]byte(discoveryMessage), targetAddr); err != nil {
		logger.Debug("Discovery: failed to send (%s): %v", logContext, err)
		return
	}

	conn.SetReadDeadline(time.Now().Add(discoveryWindow))
	buffer := make([]byte, 1024)
	for {
		n, remoteAddr, err := conn.ReadFrom(buffer)
		if err != nil {
			return // read deadline reached -- this probe's discovery window is over
		}

		var resp struct {
			AlpacaPort int `json:"AlpacaPort"`
		}
		if err := json.Unmarshal(buffer[:n], &resp); err != nil {
			continue // not a valid Alpaca discovery reply, ignore
		}

		udpAddr, ok := remoteAddr.(*net.UDPAddr)
		if !ok || resp.AlpacaPort <= 0 {
			continue
		}
		results <- fmt.Sprintf("http://%s:%d", udpAddr.IP.String(), resp.AlpacaPort)
	}
}

// QueryConfiguredDevices asks one discovered Alpaca server what devices it hosts and
// filters for the given DeviceType (e.g. "ObservingConditions", "Switch") — an exact,
// case-insensitive match against the Alpaca management API's own DeviceType string.
func QueryConfiguredDevices(baseURL, deviceType string) ([]ConfiguredDevice, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/management/v1/configureddevices")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var envelope struct {
		Value []struct {
			DeviceName   string `json:"DeviceName"`
			DeviceType   string `json:"DeviceType"`
			DeviceNumber int    `json:"DeviceNumber"`
		} `json:"Value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}

	var found []ConfiguredDevice
	for _, d := range envelope.Value {
		if strings.EqualFold(d.DeviceType, deviceType) {
			found = append(found, ConfiguredDevice{
				BaseURL:      baseURL,
				DeviceNumber: d.DeviceNumber,
				DeviceName:   d.DeviceName,
			})
		}
	}
	return found, nil
}
