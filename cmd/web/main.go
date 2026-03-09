// cmd/web/main.go — web server with embedded React UI.
// Serves the Qanal frontend as a regular web page alongside the relay API.
// Configure via env vars (same as cmd/relay). Always enables public CORS.
//
// Embedded assets: the Dockerfile copies frontend/dist → cmd/web/dist before
// go build, so //go:embed dist resolves correctly.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"Qanal/internal/config"
	"Qanal/internal/delivery"
	"Qanal/internal/infrastructure"
	"Qanal/internal/usecase"
)

//go:embed dist
var assets embed.FS

func main() {
	cfg := config.Load()

	repo, err := infrastructure.NewFileTransferRepo(cfg.StoragePath)
	if err != nil {
		slog.Error("init storage", "err", err)
		os.Exit(1)
	}
	store := infrastructure.NewFileChunkStore(cfg.StoragePath)

	ctx, cancel := context.WithCancel(context.Background())

	hub := delivery.NewHub()
	go hub.Run(ctx)

	svc := usecase.NewService(repo, store, hub, usecase.Config{
		MaxFileSize:  cfg.MaxFileSize,
		MaxChunkSize: cfg.MaxChunkSize,
		TransferTTL:  cfg.TransferTTL,
	})
	go svc.CleanupExpired(ctx, 5*time.Minute)

	// PublicCORS=true: allow any browser origin (server is accessed by IP/domain).
	h := delivery.NewHandler(svc, hub, delivery.HandlerOpts{PublicCORS: true})

	// Static file server for the embedded React app.
	distFS, err := fs.Sub(assets, "dist")
	if err != nil {
		slog.Error("embed dist", "err", err)
		os.Exit(1)
	}
	staticHandler := http.FileServer(http.FS(distFS))

	apiHandler := h.Router()
	combined := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/health" || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/ws/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		// SPA fallback: unknown paths serve index.html.
		if _, err := distFS.Open(strings.TrimPrefix(p, "/")); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			staticHandler.ServeHTTP(w, r2)
			return
		}
		staticHandler.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:        cfg.Addr,
		Handler:     combined,
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		slog.Info("qanal web started", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("web server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down web server")
	cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
	h.Close()
	repo.Close()
	slog.Info("web server stopped")
}
