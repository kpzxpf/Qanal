package domain

import (
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("transfer not found")
	ErrInvalidIndex    = errors.New("invalid chunk index")
	ErrTransferDone    = errors.New("transfer already completed")
	ErrFileTooLarge    = errors.New("file exceeds maximum allowed size")
	ErrChunkTooLarge   = errors.New("chunk exceeds maximum allowed size")
	ErrTransferExpired = errors.New("transfer has expired")
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusActive   Status = "active"
	StatusComplete Status = "complete"
	StatusExpired  Status = "expired"
)

type Transfer struct {
	Code        string    `json:"code"`
	FileName    string    `json:"fileName"`
	FileSize    int64     `json:"fileSize"`
	TotalChunks int       `json:"totalChunks"`
	ChunkSize   int64     `json:"chunkSize"`
	FileHash    string    `json:"fileHash,omitempty"` // SHA-256 hex of original file
	Status      Status    `json:"status"`
	Uploaded    []bool    `json:"uploaded"`
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func (t *Transfer) UploadedCount() int {
	n := 0
	for _, v := range t.Uploaded {
		if v {
			n++
		}
	}
	return n
}

func (t *Transfer) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}
