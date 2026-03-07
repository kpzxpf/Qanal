package transfer

import (
	"fmt"
	"net"
	"time"

	"github.com/pion/stun/v3"
)

// stunServers are queried in parallel; first successful response wins.
var stunServers = []string{
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
	"stun.cloudflare.com:3478",
}

// DiscoverWANAddr finds the public internet address for the given local TCP port.
// It binds a UDP socket on the same local port number so the NAT entry reflects
// the same port, then sends a STUN Binding Request to each server in parallel.
// Returns "IP:port" on success or an error if all servers fail within 3 s.
func DiscoverWANAddr(localPort int) (string, error) {
	type result struct {
		addr string
		err  error
	}
	ch := make(chan result, len(stunServers))
	for _, srv := range stunServers {
		go func(server string) {
			addr, err := querySTUN(server, localPort)
			ch <- result{addr, err}
		}(srv)
	}

	deadline := time.After(3 * time.Second)
	received := 0
	for {
		select {
		case r := <-ch:
			received++
			if r.err == nil {
				return r.addr, nil
			}
			if received == len(stunServers) {
				return "", fmt.Errorf("all STUN servers failed")
			}
		case <-deadline:
			return "", fmt.Errorf("STUN timeout")
		}
	}
}

// querySTUN sends one STUN Binding Request and returns the external "IP:port".
// Binds UDP on localPort so the NAT mapping matches the TCP listener on the same port.
func querySTUN(server string, localPort int) (string, error) {
	stunAddr, err := net.ResolveUDPAddr("udp4", server)
	if err != nil {
		return "", err
	}

	// Bind on the same port number as the TCP listener.
	// TCP and UDP port namespaces are independent, so this succeeds even though
	// the TCP listener already owns localPort/tcp.
	conn, err := net.DialUDP("udp4", &net.UDPAddr{Port: localPort}, stunAddr)
	if err != nil {
		// Port collision in UDP (rare) – fall back to any ephemeral port.
		// External IP is still discovered; external port may differ from TCP port.
		conn, err = net.DialUDP("udp4", nil, stunAddr)
		if err != nil {
			return "", err
		}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	c, err := stun.NewClient(conn)
	if err != nil {
		return "", err
	}
	defer c.Close()

	var (
		wanAddr string
		callErr error
	)
	msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if err := c.Do(msg, func(ev stun.Event) {
		if ev.Error != nil {
			callErr = ev.Error
			return
		}
		var xa stun.XORMappedAddress
		if err := xa.GetFrom(ev.Message); err != nil {
			callErr = err
			return
		}
		wanAddr = fmt.Sprintf("%s:%d", xa.IP.String(), xa.Port)
	}); err != nil {
		return "", err
	}
	if callErr != nil {
		return "", callErr
	}
	return wanAddr, nil
}
