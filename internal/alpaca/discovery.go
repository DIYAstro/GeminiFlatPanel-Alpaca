package alpaca

import (
	"bytes"
	"fmt"
	"net"
	"time"

	"geminiflatpanel/internal/config"
	"geminiflatpanel/internal/logger"
)

// RespondToDiscovery listens for Alpaca discovery packets on UDP port 32227
// and responds with the server's listening port. It periodically monitors the config
// and restarts/stops the UDP listener dynamically if settings change.
func RespondToDiscovery() {
	var currentConn *net.UDPConn
	var currentAddr string
	var currentEnabled bool

	for {
		conf := config.Get()
		enabled := conf.EnableAlpacaDiscovery
		listenAddr := conf.ListenAddress
		udpAddress := fmt.Sprintf("%s:32227", listenAddr)

		// Check if config has changed
		if enabled != currentEnabled || (enabled && udpAddress != currentAddr) {
			// Clean up old connection if it exists
			if currentConn != nil {
				currentConn.Close()
				currentConn = nil
				logger.Info("Alpaca discovery responder stopped.")
			}

			currentEnabled = enabled
			currentAddr = udpAddress

			if enabled {
				addr, err := net.ResolveUDPAddr("udp4", udpAddress)
				if err != nil {
					logger.Error("Discovery: Could not resolve UDP address '%s': %v", udpAddress, err)
					currentEnabled = false // Reset so we retry next loop
				} else {
					conn, err := net.ListenUDP("udp4", addr)
					if err != nil {
						logger.Error("Discovery: Could not listen on UDP address '%s': %v", udpAddress, err)
						logger.Info("HINT: This may be caused by another Alpaca application running, or a permissions issue.")
						currentEnabled = false // Reset so we retry next loop
					} else {
						currentConn = conn
						logger.Info("Alpaca discovery responder started on UDP address '%s'.", udpAddress)
						
						// Start packet handling goroutine for this connection
						go handleDiscoveryPackets(conn)
					}
				}
			}
		}

		time.Sleep(2 * time.Second)
	}
}

func handleDiscoveryPackets(conn *net.UDPConn) {
	defer conn.Close()
	discoveryMsg := []byte("alpacadiscovery1")
	buffer := make([]byte, 1024)

	for {
		n, remoteAddr, err := conn.ReadFrom(buffer)
		if err != nil {
			// conn.Close() was called, terminate goroutine gracefully
			return
		}

		if bytes.Equal(buffer[:n], discoveryMsg) {
			logger.Debug("Discovery: Request received from %s", remoteAddr)

			// Get the current network port from the config
			port := config.Get().NetworkPort
			response := fmt.Sprintf(`{"AlpacaPort": %d}`, port)

			_, err := conn.WriteTo([]byte(response), remoteAddr)
			if err != nil {
				logger.Error("Discovery: Failed to send response to %s: %v", remoteAddr, err)
			} else {
				logger.Debug("Discovery: Sent response '%s' to %s", response, remoteAddr)
			}
		}
	}
}
