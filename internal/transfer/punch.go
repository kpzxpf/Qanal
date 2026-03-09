package transfer

import (
	"context"
	"fmt"
	"net"
	"time"
)

// UDPPunch sends UDP datagrams to peerAddr from localPort simultaneously with
// the peer doing the same. This opens NAT mappings on both sides so that
// subsequent TCP connections from the peer's IP are allowed through.
// Returns when the peer's punch packet is received (bidirectional connectivity
// confirmed) or when ctx is cancelled / timeout expires.
func UDPPunch(ctx context.Context, localPort int, peerAddr string, timeout time.Duration) error {
	pAddr, err := net.ResolveUDPAddr("udp4", peerAddr)
	if err != nil {
		return fmt.Errorf("resolve peer addr: %w", err)
	}

	// Bind to the same port used by the TCP listener so the NAT maps the
	// correct external port. UDP and TCP share the port number space but have
	// independent NAT tables; binding here creates a UDP NAT entry for localPort.
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: localPort})
	if err != nil {
		// Port already in use in UDP namespace — fall back to ephemeral port.
		// We still open a mapping; external port may differ from TCP listener port.
		conn, err = net.ListenUDP("udp4", nil)
		if err != nil {
			return fmt.Errorf("open udp socket: %w", err)
		}
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	conn.SetDeadline(deadline)

	// Send punch packets in background.
	go func() {
		pkt := []byte("QNLPUNCH")
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				conn.WriteToUDP(pkt, pAddr)
				if time.Now().After(deadline) {
					return
				}
			}
		}
	}()

	// Wait for peer's punch packet.
	buf := make([]byte, 32)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("punch timeout: %w", err)
		}
		if from.IP.Equal(pAddr.IP) && n >= 8 && string(buf[:8]) == "QNLPUNCH" {
			return nil // bidirectional UDP connectivity confirmed
		}
	}
}
