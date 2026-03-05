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

// GetLocalIP returns the first non-loopback IPv4 address of this machine.
func GetLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
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
