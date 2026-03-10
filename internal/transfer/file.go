package transfer

import (
	"fmt"
	"net"
	"os"
)

// FileInfo is returned by GetFileInfo for the UI to display.
type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Path string `json:"path"`
}

func GetFileInfo(path string) (*FileInfo, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if stat.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file")
	}
	return &FileInfo{
		Name: stat.Name(),
		Size: stat.Size(),
		Path: path,
	}, nil
}

// GetLocalIP returns the best non-loopback IPv4 address for this machine.
// It prefers common LAN ranges (192.168.x.x, 10.x.x.x) over virtual/Docker
// bridge ranges (172.16-31.x.x) to avoid picking Docker adapter addresses.
func GetLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}

	var (
		home      string // 192.168.x.x
		private10 string // 10.x.x.x
		other     string // 172.16-31.x.x or anything else non-loopback
	)

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil {
			continue
		}
		switch {
		case ip4[0] == 192 && ip4[1] == 168:
			if home == "" {
				home = ip4.String()
			}
		case ip4[0] == 10:
			if private10 == "" {
				private10 = ip4.String()
			}
		default:
			if other == "" {
				other = ip4.String()
			}
		}
	}

	if home != "" {
		return home
	}
	if private10 != "" {
		return private10
	}
	if other != "" {
		return other
	}
	return "127.0.0.1"
}

func openFileInfo(path string) (*os.File, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, stat, nil
}
