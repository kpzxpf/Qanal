// cmd/relay/main.go — standalone relay server (no Wails, runs in Docker or on a VPS).
// Configure via env vars (see internal/config/config.go).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"Qanal/internal/config"
	"Qanal/internal/delivery"
	"Qanal/internal/infrastructure"
	"Qanal/internal/usecase"
)

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

	h := delivery.NewHandler(svc, hub)
	srv := &http.Server{
		Addr:        cfg.Addr,
		Handler:     h.Router(),
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		slog.Info("qanal relay started", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("relay server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down relay")
	cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
	h.Close()
	repo.Close()
	slog.Info("relay stopped")
}
