package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"Qanal/internal/config"
	"Qanal/internal/delivery"
	"Qanal/internal/infrastructure"
	"Qanal/internal/transfer"
	"Qanal/internal/usecase"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails application — all exported methods are bound to the frontend.
type App struct {
	ctx           context.Context
	server        *http.Server
	port          int
	peers         peerManager
	cleanupCancel context.CancelFunc
	handler       *delivery.Handler
	repo          *infrastructure.FileTransferRepo
}

// peerManager owns the lifecycle of a single active P2P sender session.
// It is extracted from App to respect SRP: App is the Wails entry-point;
// peerManager handles peer-session state exclusively.
type peerManager struct {
	mu     sync.Mutex
	server *transfer.PeerServer
}

func (m *peerManager) start(filePath string, chunkMB int) (*transfer.PeerInfo, *transfer.PeerServer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.server != nil {
		m.server.Close()
		m.server = nil
	}
	ps, err := transfer.StartPeer(filePath, chunkMB)
	if err != nil {
		return nil, nil, err
	}
	m.server = ps
	return ps.Info, ps, nil
}

func (m *peerManager) compareAndClear(ps *transfer.PeerServer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == ps {
		m.server = nil
	}
}

func (m *peerManager) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		m.server.Close()
		m.server = nil
	}
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.startRelayServer(); err != nil {
		slog.Error("relay server failed", "err", err)
	}
}

func (a *App) shutdown(ctx context.Context) {
	if a.cleanupCancel != nil {
		a.cleanupCancel()
	}
	if a.server != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.server.Shutdown(shutCtx)
	}
	// Close handler after server stops accepting requests (stops rate limiter goroutine).
	if a.handler != nil {
		a.handler.Close()
	}
	// Close repo after server shutdown — flushes pending async meta writes.
	if a.repo != nil {
		a.repo.Close()
	}
	a.peers.stop()
}

// ─── Embedded relay server ────────────────────────────────────────────────────

func (a *App) startRelayServer() error {
	cfg := config.Load()
	a.port = findFreePort(8080)
	cfg.Addr = fmt.Sprintf(":%d", a.port)

	repo, err := infrastructure.NewFileTransferRepo(cfg.StoragePath)
	if err != nil {
		return fmt.Errorf("init storage: %w", err)
	}
	a.repo = repo
	store := infrastructure.NewFileChunkStore(cfg.StoragePath)
	hub := delivery.NewHub()
	cleanupCtx, cancel := context.WithCancel(context.Background())
	a.cleanupCancel = cancel
	go hub.Run(cleanupCtx)

	svc := usecase.NewService(repo, store, hub, usecase.Config{
		MaxFileSize:  cfg.MaxFileSize,
		MaxChunkSize: cfg.MaxChunkSize,
		TransferTTL:  cfg.TransferTTL,
	})
	go svc.CleanupExpired(cleanupCtx, 5*time.Minute)

	h := delivery.NewHandler(svc, hub)
	a.handler = h
	a.server = &http.Server{
		Addr:        cfg.Addr,
		Handler:     h.Router(),
		IdleTimeout: 120 * time.Second,
	}
	go func() {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("relay server", "err", err)
		}
	}()
	return nil
}

// findFreePort returns the first available TCP port starting from start.
// If no port is free within 20 attempts it returns start (will fail on listen).
func findFreePort(start int) int {
	for port := start; port < start+20; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln.Close()
			return port
		}
	}
	return start
}

// GetLocalServerURL returns the LAN URL of the embedded relay server.
func (a *App) GetLocalServerURL() string {
	return fmt.Sprintf("http://%s:%d", transfer.GetLocalIP(), a.port)
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

// ─── Relay transfer ───────────────────────────────────────────────────────────

// SendFile uploads the file via the embedded relay server.
func (a *App) SendFile(serverURL, filePath string, chunkMB, workers int) (*transfer.SendResult, error) {
	result, err := transfer.Send(a.ctx, serverURL, filePath, chunkMB, workers, func(e transfer.ProgressEvent) {
		wailsruntime.EventsEmit(a.ctx, "transfer:progress", e)
	})
	if err != nil {
		wailsruntime.EventsEmit(a.ctx, "transfer:error", map[string]string{"message": err.Error()})
		return nil, err
	}
	wailsruntime.EventsEmit(a.ctx, "transfer:complete", map[string]string{"code": result.Code, "key": result.Key})
	return result, nil
}

// ReceiveFile downloads and decrypts via the relay server.
func (a *App) ReceiveFile(serverURL, code, keyB64, outputDir string, workers int) (string, error) {
	outPath, err := transfer.Receive(a.ctx, serverURL, code, keyB64, outputDir, workers, func(e transfer.ProgressEvent) {
		wailsruntime.EventsEmit(a.ctx, "transfer:progress", e)
	})
	if err != nil {
		wailsruntime.EventsEmit(a.ctx, "transfer:error", map[string]string{"message": err.Error()})
		return "", err
	}
	wailsruntime.EventsEmit(a.ctx, "transfer:complete", map[string]string{"path": outPath})
	return outPath, nil
}

// ─── P2P direct transfer ──────────────────────────────────────────────────────

// StartPeerSend opens a TCP listener and returns credentials immediately.
// It then waits in the background for a receiver to connect and stream the file.
// Progress is emitted via "transfer:progress" / "transfer:complete" / "transfer:error".
func (a *App) StartPeerSend(filePath string, chunkMB int) (*transfer.PeerInfo, error) {
	info, ps, err := a.peers.start(filePath, chunkMB)
	if err != nil {
		return nil, err
	}

	go func() {
		err := ps.Serve(a.ctx, func(e transfer.ProgressEvent) {
			wailsruntime.EventsEmit(a.ctx, "transfer:progress", e)
		})

		a.peers.compareAndClear(ps)

		if err != nil {
			wailsruntime.EventsEmit(a.ctx, "transfer:error", map[string]string{"message": err.Error()})
		} else {
			wailsruntime.EventsEmit(a.ctx, "transfer:complete", map[string]string{
				"code": ps.Info.Code,
				"key":  ps.Info.Key,
			})
		}
	}()

	return info, nil
}

// StopPeerSend cancels waiting for a P2P receiver.
func (a *App) StopPeerSend() {
	a.peers.stop()
}

// PeerReceive connects directly to a PeerServer and downloads the file.
// No relay server involved — maximum speed.
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

// FormatBytes formats bytes as human-readable string.
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
