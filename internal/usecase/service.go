package usecase

import (
	"Qanal/internal/domain"
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

// ProgressBroadcaster is implemented by the WebSocket hub.
type ProgressBroadcaster interface {
	Broadcast(code string, msg any)
}

type Config struct {
	MaxFileSize  int64
	MaxChunkSize int64
	TransferTTL  time.Duration
}

type Service struct {
	repo  domain.TransferRepo
	store domain.ChunkStore
	hub   ProgressBroadcaster
	cfg   Config
}

func NewService(repo domain.TransferRepo, store domain.ChunkStore, hub ProgressBroadcaster, cfg Config) *Service {
	return &Service{repo: repo, store: store, hub: hub, cfg: cfg}
}

// --- DTOs ---

type InitiateRequest struct {
	FileName    string `json:"fileName"`
	FileSize    int64  `json:"fileSize"`
	TotalChunks int    `json:"totalChunks"`
	ChunkSize   int64  `json:"chunkSize"`
}

type InitiateResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type TransferInfo struct {
	Code           string        `json:"code"`
	FileName       string        `json:"fileName"`
	FileSize       int64         `json:"fileSize"`
	TotalChunks    int           `json:"totalChunks"`
	ChunkSize      int64         `json:"chunkSize"`
	UploadedChunks int           `json:"uploadedChunks"`
	UploadedMap    []bool        `json:"uploadedMap"`
	Status         domain.Status `json:"status"`
	ExpiresAt      time.Time     `json:"expiresAt"`
}

type ProgressEvent struct {
	Type        string `json:"type"`
	ChunkIndex  int    `json:"chunkIndex"`
	Uploaded    int    `json:"uploaded"`
	TotalChunks int    `json:"totalChunks"`
}

// --- Methods ---

func (s *Service) Initiate(req InitiateRequest) (*InitiateResponse, error) {
	if req.FileSize > s.cfg.MaxFileSize {
		return nil, fmt.Errorf("%w: max %d bytes", domain.ErrFileTooLarge, s.cfg.MaxFileSize)
	}
	if req.ChunkSize > s.cfg.MaxChunkSize {
		return nil, fmt.Errorf("%w: max %d bytes", domain.ErrChunkTooLarge, s.cfg.MaxChunkSize)
	}
	if req.TotalChunks <= 0 || req.TotalChunks > 100000 {
		return nil, fmt.Errorf("totalChunks must be between 1 and 100000")
	}

	req.FileName = sanitizeFilename(req.FileName)

	code, err := generateCode()
	if err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}
	t := &domain.Transfer{
		Code:        code,
		FileName:    req.FileName,
		FileSize:    req.FileSize,
		TotalChunks: req.TotalChunks,
		ChunkSize:   req.ChunkSize,
		Status:      domain.StatusPending,
		Uploaded:    make([]bool, req.TotalChunks),
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(s.cfg.TransferTTL),
	}
	if err := s.repo.Save(t); err != nil {
		return nil, fmt.Errorf("save transfer: %w", err)
	}
	return &InitiateResponse{Code: code, ExpiresAt: t.ExpiresAt}, nil
}

func (s *Service) GetInfo(code string) (*TransferInfo, error) {
	t, err := s.repo.FindByCode(code)
	if err != nil {
		return nil, err
	}
	if t.IsExpired() && t.Status != domain.StatusComplete {
		return nil, domain.ErrTransferExpired
	}
	return toInfo(t), nil
}

func (s *Service) UploadChunk(code string, index int, r io.Reader) error {
	t, err := s.repo.FindByCode(code)
	if err != nil {
		return err
	}
	if t.Status == domain.StatusComplete {
		return domain.ErrTransferDone
	}
	if t.IsExpired() {
		return domain.ErrTransferExpired
	}
	if index < 0 || index >= t.TotalChunks {
		return domain.ErrInvalidIndex
	}

	if err := s.store.Write(code, index, r); err != nil {
		return fmt.Errorf("write chunk: %w", err)
	}

	updated, err := s.repo.MarkChunkUploaded(code, index)
	if err != nil {
		return fmt.Errorf("mark chunk: %w", err)
	}

	s.hub.Broadcast(code, ProgressEvent{
		Type:        "chunk_uploaded",
		ChunkIndex:  index,
		Uploaded:    updated.UploadedCount(),
		TotalChunks: updated.TotalChunks,
	})
	return nil
}

func (s *Service) DownloadChunk(code string, index int) (io.ReadCloser, int64, error) {
	t, err := s.repo.FindByCode(code)
	if err != nil {
		return nil, 0, err
	}
	if index < 0 || index >= t.TotalChunks {
		return nil, 0, domain.ErrInvalidIndex
	}
	if !t.Uploaded[index] {
		return nil, 0, fmt.Errorf("chunk %d not yet uploaded", index)
	}
	return s.store.Open(code, index)
}

func (s *Service) CompleteTransfer(code string) error {
	t, err := s.repo.FindByCode(code)
	if err != nil {
		return err
	}
	uploaded := t.UploadedCount()
	if uploaded != t.TotalChunks {
		return fmt.Errorf("not all chunks uploaded: %d/%d", uploaded, t.TotalChunks)
	}
	return s.repo.UpdateStatus(code, domain.StatusComplete)
}

func (s *Service) DeleteTransfer(code string) error {
	if err := s.store.DeleteTransfer(code); err != nil {
		slog.Warn("delete chunks failed", "code", code, "err", err)
	}
	return s.repo.Delete(code)
}

// CleanupExpired periodically removes expired transfers until ctx is cancelled.
func (s *Service) CleanupExpired(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			transfers, err := s.repo.ListAll()
			if err != nil {
				continue
			}
			for _, t := range transfers {
				if t.IsExpired() {
					slog.Info("cleaning up expired transfer", "code", t.Code, "status", t.Status)
					_ = s.DeleteTransfer(t.Code)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// --- Helpers ---

func generateCode() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)[:8], nil
}

// sanitizeFilename removes path components and characters unsafe on common
// filesystems (Windows / Linux), preventing path traversal and injection.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r < 32 || r == 127:
			// strip control chars
		case r == '<' || r == '>' || r == ':' || r == '"' ||
			r == '/' || r == '\\' || r == '|' || r == '?' || r == '*':
			sb.WriteRune('_')
		default:
			sb.WriteRune(r)
		}
	}
	result := strings.TrimSpace(sb.String())
	if result == "" || result == "." || result == ".." {
		result = "file"
	}
	return result
}

func toInfo(t *domain.Transfer) *TransferInfo {
	return &TransferInfo{
		Code:           t.Code,
		FileName:       t.FileName,
		FileSize:       t.FileSize,
		TotalChunks:    t.TotalChunks,
		ChunkSize:      t.ChunkSize,
		UploadedChunks: t.UploadedCount(),
		UploadedMap:    t.Uploaded,
		Status:         t.Status,
		ExpiresAt:      t.ExpiresAt,
	}
}
