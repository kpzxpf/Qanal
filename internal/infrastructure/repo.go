package infrastructure

import (
	"Qanal/internal/domain"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileTransferRepo stores transfer metadata as JSON files on disk.
type FileTransferRepo struct {
	mu      sync.Mutex
	baseDir string
}

func NewFileTransferRepo(baseDir string) (*FileTransferRepo, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	return &FileTransferRepo{baseDir: baseDir}, nil
}

func (r *FileTransferRepo) metaPath(code string) string {
	return filepath.Join(r.baseDir, code, "meta.json")
}

func (r *FileTransferRepo) Save(t *domain.Transfer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	dir := filepath.Join(r.baseDir, t.Code)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.metaPath(t.Code), data, 0644)
}

func (r *FileTransferRepo) FindByCode(code string) (*domain.Transfer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readLocked(code)
}

func (r *FileTransferRepo) readLocked(code string) (*domain.Transfer, error) {
	data, err := os.ReadFile(r.metaPath(code))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	var t domain.Transfer
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *FileTransferRepo) writeLocked(t *domain.Transfer) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.metaPath(t.Code), data, 0644)
}

func (r *FileTransferRepo) MarkChunkUploaded(code string, index int) (*domain.Transfer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, err := r.readLocked(code)
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(t.Uploaded) {
		return nil, domain.ErrInvalidIndex
	}
	t.Uploaded[index] = true
	t.Status = domain.StatusActive
	if err := r.writeLocked(t); err != nil {
		return nil, err
	}
	return t, nil
}

func (r *FileTransferRepo) UpdateStatus(code string, status domain.Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, err := r.readLocked(code)
	if err != nil {
		return err
	}
	t.Status = status
	return r.writeLocked(t)
}

func (r *FileTransferRepo) ListAll() ([]*domain.Transfer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entries, err := os.ReadDir(r.baseDir)
	if err != nil {
		return nil, err
	}
	var transfers []*domain.Transfer
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := r.readLocked(e.Name())
		if err != nil {
			continue // skip corrupt entries
		}
		transfers = append(transfers, t)
	}
	return transfers, nil
}

func (r *FileTransferRepo) Delete(code string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return os.RemoveAll(filepath.Join(r.baseDir, code))
}
