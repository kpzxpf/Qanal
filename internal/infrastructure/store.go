package infrastructure

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileChunkStore stores encrypted chunk data as individual files.
type FileChunkStore struct {
	baseDir string
}

func NewFileChunkStore(baseDir string) *FileChunkStore {
	return &FileChunkStore{baseDir: baseDir}
}

func (s *FileChunkStore) chunkPath(code string, index int) string {
	return filepath.Join(s.baseDir, code, fmt.Sprintf("%d.chunk", index))
}

// Write streams data from r directly to disk — no in-memory buffering.
func (s *FileChunkStore) Write(code string, index int, r io.Reader) error {
	path := s.chunkPath(code, index)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create chunk file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 4*1024*1024) // 4 MB copy buffer
	_, err = io.CopyBuffer(f, r, buf)
	return err
}

// Open returns a ReadCloser for the chunk and its size.
func (s *FileChunkStore) Open(code string, index int) (io.ReadCloser, int64, error) {
	path := s.chunkPath(code, index)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, fmt.Errorf("chunk %d not found", index)
		}
		return nil, 0, err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, stat.Size(), nil
}

// DeleteTransfer removes all chunk files for a transfer.
func (s *FileChunkStore) DeleteTransfer(code string) error {
	return os.RemoveAll(filepath.Join(s.baseDir, code))
}
