package domain

import "io"

// TransferRepo persists transfer metadata.
type TransferRepo interface {
	Save(t *Transfer) error
	FindByCode(code string) (*Transfer, error)
	MarkChunkUploaded(code string, index int) (*Transfer, error)
	UpdateStatus(code string, status Status) error
	ListAll() ([]*Transfer, error)
	Delete(code string) error
}

// ChunkStore persists chunk binary data.
type ChunkStore interface {
	Write(code string, index int, r io.Reader) error
	Open(code string, index int) (io.ReadCloser, int64, error)
	DeleteTransfer(code string) error
}
