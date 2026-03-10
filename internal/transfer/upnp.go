package transfer

import (
	"fmt"
	"log/slog"

	"github.com/huin/goupnp/dcps/internetgateway2"
)

// MapUPnPPort attempts to open an inbound TCP port mapping on the NAT router
// via UPnP IGD. Returns the router's external IP address on success so callers
// can build an accurate WAN address for the share link.
// The mapping is leased for 1 hour; re-map if the session lasts longer.
// Returns ("", err) when UPnP is unavailable or the router rejects the mapping.
//
// v1 and v2 probes run in parallel; the first successful result wins.
// The buffered channel guarantees both goroutines exit cleanly regardless of
// whether the caller reads one or two results.
func MapUPnPPort(port int) (externalIP string, err error) {
	localIP := GetLocalIP()

	type result struct {
		ip  string
		err error
	}
	ch := make(chan result, 2) // buffered so goroutines never block on send

	go func() { ip, e := mapUPnPv1(port, localIP); ch <- result{ip, e} }()
	go func() { ip, e := mapUPnPv2(port, localIP); ch <- result{ip, e} }()

	var lastErr error
	for range 2 {
		r := <-ch
		if r.err == nil && r.ip != "" {
			return r.ip, nil // other goroutine writes to buffered ch and exits cleanly
		}
		lastErr = r.err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("UPnP: no gateway found or mapping failed")
}

func mapUPnPv1(port int, localIP string) (string, error) {
	clients, _, err := internetgateway2.NewWANIPConnection1Clients()
	if err != nil || len(clients) == 0 {
		return "", fmt.Errorf("no WANIPConnection1 clients")
	}
	for _, c := range clients {
		if err := c.AddPortMapping("", uint16(port), "TCP", uint16(port), localIP, true, "Qanal", 3600); err != nil {
			continue
		}
		ip, err := c.GetExternalIPAddress()
		if err == nil {
			slog.Info("UPnP mapped (v1)", "port", port, "externalIP", ip)
			return ip, nil
		}
	}
	return "", fmt.Errorf("UPnP v1: mapping failed")
}

func mapUPnPv2(port int, localIP string) (string, error) {
	clients, _, err := internetgateway2.NewWANIPConnection2Clients()
	if err != nil || len(clients) == 0 {
		return "", fmt.Errorf("no WANIPConnection2 clients")
	}
	for _, c := range clients {
		if err := c.AddPortMapping("", uint16(port), "TCP", uint16(port), localIP, true, "Qanal", 3600); err != nil {
			continue
		}
		ip, err := c.GetExternalIPAddress()
		if err == nil {
			slog.Info("UPnP mapped (v2)", "port", port, "externalIP", ip)
			return ip, nil
		}
	}
	return "", fmt.Errorf("UPnP v2: mapping failed")
}
