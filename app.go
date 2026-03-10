package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"Qanal/internal/transfer"

	qrcode "github.com/skip2/go-qrcode"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails application — all exported methods are bound to the frontend.
type App struct {
	ctx   context.Context
	peers peerManager
}

// peerManager owns the lifecycle of one active P2P sender session.
type peerManager struct {
	mu     sync.Mutex
	server *transfer.PeerServer
	cancel context.CancelFunc
}

func (m *peerManager) set(ps *transfer.PeerServer, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.server = ps
	m.cancel = cancel
}

func (m *peerManager) clear(ps *transfer.PeerServer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == ps {
		m.server = nil
		m.cancel = nil
	}
}

func (m *peerManager) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		m.server.Close()
		m.server = nil
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	wailsruntime.OnFileDrop(ctx, func(x, y int, paths []string) {
		if len(paths) > 0 {
			wailsruntime.EventsEmit(ctx, "file:dropped", map[string]string{"path": paths[0]})
		}
	})
}

func (a *App) shutdown(_ context.Context) {
	a.peers.stop()
}

// ─── File dialogs ─────────────────────────────────────────────────────────────

func (a *App) SelectFile() string {
	path, _ := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select file to send",
	})
	return path
}

func (a *App) SelectDirectory() string {
	path, _ := wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Choose save location",
	})
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			return home + string(os.PathSeparator) + "Downloads"
		}
	}
	return path
}

func (a *App) GetFileInfo(path string) (*transfer.FileInfo, error) {
	return transfer.GetFileInfo(path)
}

// ─── P2P direct transfer ──────────────────────────────────────────────────────

// StartPeerSend opens a TCP listener, maps the port via UPnP, and discovers
// the WAN address via STUN. Returns credentials immediately so the sender can
// share the link. Call StopPeerSend to cancel.
func (a *App) StartPeerSend(filePath string, chunkMB int) (*transfer.PeerInfo, error) {
	a.peers.stop()

	ps, err := transfer.StartPeer(filePath, chunkMB)
	if err != nil {
		return nil, err
	}

	peerCtx, cancelPeer := context.WithCancel(a.ctx)
	a.peers.set(ps, cancelPeer)

	go func() {
		progressFn := func(e transfer.ProgressEvent) {
			wailsruntime.EventsEmit(a.ctx, "transfer:progress", e)
		}

		err := ps.Serve(peerCtx, progressFn)
		a.peers.clear(ps)
		cancelPeer()

		if err != nil && peerCtx.Err() == nil {
			slog.Error("p2p: serve error", "err", err)
			wailsruntime.EventsEmit(a.ctx, "transfer:error", map[string]string{"message": err.Error()})
		} else if err == nil {
			wailsruntime.EventsEmit(a.ctx, "transfer:complete", map[string]string{
				"code": ps.Info.Code,
				"key":  ps.Info.Key,
			})
		}
	}()

	return ps.Info, nil
}

// StopPeerSend cancels the active P2P send session.
func (a *App) StopPeerSend() {
	a.peers.stop()
}

// PeerReceive connects to a PeerServer using the share link credentials,
// downloads and decrypts the file to outputDir.
func (a *App) PeerReceive(peerAddr, code, keyB64, outputDir string) (string, error) {
	outPath, err := transfer.PeerReceive(a.ctx, peerAddr, code, keyB64, outputDir, func(e transfer.ProgressEvent) {
		wailsruntime.EventsEmit(a.ctx, "transfer:progress", e)
	})
	if err != nil {
		wailsruntime.EventsEmit(a.ctx, "transfer:error", map[string]string{"message": err.Error()})
		return "", err
	}
	wailsruntime.EventsEmit(a.ctx, "transfer:complete", map[string]string{"path": outPath})
	return outPath, nil
}

// ─── Utilities ────────────────────────────────────────────────────────────────

// GenerateQRCode returns a data:image/png;base64 URI for the given content.
func (a *App) GenerateQRCode(content string) (string, error) {
	png, err := qrcode.Encode(content, qrcode.Medium, 200)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// FormatBytes formats bytes as human-readable string (e.g. "1.5 GB").
func (a *App) FormatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(1024), 0
	for n := b / 1024; n >= 1024; n /= 1024 {
		div *= 1024
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
