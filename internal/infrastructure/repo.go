package infrastructure

import (
	"Qanal/internal/domain"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// FileTransferRepo stores transfer metadata as JSON files on disk with a
// write-through in-memory cache. All reads hit the cache (O(1)); writes go
// to both cache and disk for durability. The cache is hydrated from disk on
// startup so previously created transfers survive restarts.
//
// MarkChunkUploaded enqueues disk writes asynchronously so upload workers
// are not serialized around disk I/O — only the in-memory update is locked.
// Call Close() during shutdown to flush all pending writes.
type FileTransferRepo struct {
	mu      sync.RWMutex
	baseDir string
	cache   map[string]*domain.Transfer
	writeQ  chan *domain.Transfer // buffered async write queue
	stopCh  chan struct{}
}

func NewFileTransferRepo(baseDir string) (*FileTransferRepo, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	r := &FileTransferRepo{
		baseDir: baseDir,
		cache:   make(map[string]*domain.Transfer),
		writeQ:  make(chan *domain.Transfer, 512),
		stopCh:  make(chan struct{}),
	}
	r.hydrate()
	go r.writeWorker()
	return r, nil
}

// Close flushes all pending async metadata writes and stops the write worker.
// Should be called during application shutdown after the HTTP server stops.
func (r *FileTransferRepo) Close() {
	close(r.stopCh)
}

// writeWorker drains the write queue sequentially, preserving write order
// per transfer. On Close(), it processes any remaining queued writes before
// exiting to ensure no metadata is lost.
func (r *FileTransferRepo) writeWorker() {
	for {
		select {
		case t := <-r.writeQ:
			if err := r.writeToDisk(t); err != nil {
				slog.Warn("async meta write failed", "code", t.Code, "err", err)
			}
		case <-r.stopCh:
			// Drain remaining writes before exiting.
			for {
				select {
				case t := <-r.writeQ:
					if err := r.writeToDisk(t); err != nil {
						slog.Warn("shutdown meta write failed", "code", t.Code, "err", err)
					}
				default:
					return
				}
			}
		}
	}
}

// hydrate loads existing transfers from disk into the cache on startup.
func (r *FileTransferRepo) hydrate() {
	entries, err := os.ReadDir(r.baseDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := r.readFromDisk(e.Name())
		if err == nil {
			r.cache[e.Name()] = t
		}
	}
}

func (r *FileTransferRepo) metaPath(code string) string {
	return filepath.Join(r.baseDir, code, "meta.json")
}

func (r *FileTransferRepo) readFromDisk(code string) (*domain.Transfer, error) {
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

func (r *FileTransferRepo) writeToDisk(t *domain.Transfer) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(r.metaPath(t.Code), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (r *FileTransferRepo) Save(t *domain.Transfer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	dir := filepath.Join(r.baseDir, t.Code)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := r.writeToDisk(t); err != nil {
		return err
	}
	r.cache[t.Code] = cloneTransfer(t)
	return nil
}

// FindByCode returns from the in-memory cache — no disk I/O on the hot path.
func (r *FileTransferRepo) FindByCode(code string) (*domain.Transfer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, ok := r.cache[code]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return cloneTransfer(t), nil
}

func (r *FileTransferRepo) MarkChunkUploaded(code string, index int) (*domain.Transfer, error) {
	r.mu.Lock()
	t, ok := r.cache[code]
	if !ok {
		r.mu.Unlock()
		return nil, domain.ErrNotFound
	}
	if index < 0 || index >= len(t.Uploaded) {
		r.mu.Unlock()
		return nil, domain.ErrInvalidIndex
	}
	t.Uploaded[index] = true
	t.Status = domain.StatusActive
	result := cloneTransfer(t)
	tForDisk := cloneTransfer(t)
	r.mu.Unlock()

	// Enqueue disk write without holding the lock — upload workers proceed immediately.
	// If the queue is full (very high chunk rate), fall back to an inline write.
	select {
	case r.writeQ <- tForDisk:
	default:
		if err := r.writeToDisk(tForDisk); err != nil {
			slog.Warn("inline meta write fallback", "code", code, "err", err)
		}
	}

	return result, nil
}

func (r *FileTransferRepo) UpdateStatus(code string, status domain.Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.cache[code]
	if !ok {
		return domain.ErrNotFound
	}
	t.Status = status
	return r.writeToDisk(t)
}

func (r *FileTransferRepo) ListAll() ([]*domain.Transfer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*domain.Transfer, 0, len(r.cache))
	for _, t := range r.cache {
		result = append(result, cloneTransfer(t))
	}
	return result, nil
}

func (r *FileTransferRepo) Delete(code string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.cache, code)
	return os.RemoveAll(filepath.Join(r.baseDir, code))
}

// cloneTransfer returns a deep copy so callers cannot mutate the cached object.
func cloneTransfer(t *domain.Transfer) *domain.Transfer {
	c := *t
	c.Uploaded = make([]bool, len(t.Uploaded))
	copy(c.Uploaded, t.Uploaded)
	return &c
}
