package usecase_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"Qanal/internal/domain"
	"Qanal/internal/usecase"
)

// ─── Mock implementations ────────────────────────────────────────────────────

type mockRepo struct {
	transfers map[string]*domain.Transfer
	saveErr   error
}

func newMockRepo() *mockRepo {
	return &mockRepo{transfers: make(map[string]*domain.Transfer)}
}

func (m *mockRepo) Save(t *domain.Transfer) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	cloned := *t
	uploaded := make([]bool, len(t.Uploaded))
	copy(uploaded, t.Uploaded)
	cloned.Uploaded = uploaded
	m.transfers[t.Code] = &cloned
	return nil
}

func (m *mockRepo) FindByCode(code string) (*domain.Transfer, error) {
	t, ok := m.transfers[code]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cloned := *t
	uploaded := make([]bool, len(t.Uploaded))
	copy(uploaded, t.Uploaded)
	cloned.Uploaded = uploaded
	return &cloned, nil
}

func (m *mockRepo) MarkChunkUploaded(code string, index int) (*domain.Transfer, error) {
	t, ok := m.transfers[code]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if index < 0 || index >= len(t.Uploaded) {
		return nil, domain.ErrInvalidIndex
	}
	t.Uploaded[index] = true
	cloned := *t
	uploaded := make([]bool, len(t.Uploaded))
	copy(uploaded, t.Uploaded)
	cloned.Uploaded = uploaded
	return &cloned, nil
}

func (m *mockRepo) UpdateStatus(code string, status domain.Status) error {
	t, ok := m.transfers[code]
	if !ok {
		return domain.ErrNotFound
	}
	t.Status = status
	return nil
}

func (m *mockRepo) ListAll() ([]*domain.Transfer, error) {
	result := make([]*domain.Transfer, 0, len(m.transfers))
	for _, t := range m.transfers {
		result = append(result, t)
	}
	return result, nil
}

func (m *mockRepo) Delete(code string) error {
	delete(m.transfers, code)
	return nil
}

// ─── mockStore ───────────────────────────────────────────────────────────────

type mockStore struct {
	chunks   map[string][]byte
	writeErr error
	openErr  error
}

func newMockStore() *mockStore {
	return &mockStore{chunks: make(map[string][]byte)}
}

func (m *mockStore) Write(code string, index int, r io.Reader) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.chunks[fmt.Sprintf("%s/%d", code, index)] = data
	return nil
}

func (m *mockStore) Open(code string, index int) (io.ReadCloser, int64, error) {
	if m.openErr != nil {
		return nil, 0, m.openErr
	}
	data, ok := m.chunks[fmt.Sprintf("%s/%d", code, index)]
	if !ok {
		return nil, 0, fmt.Errorf("chunk not found")
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (m *mockStore) DeleteTransfer(code string) error {
	for k := range m.chunks {
		if strings.HasPrefix(k, code+"/") {
			delete(m.chunks, k)
		}
	}
	return nil
}

// ─── mockHub ─────────────────────────────────────────────────────────────────

type mockHub struct {
	broadcasts []any
}

func (m *mockHub) Broadcast(_ string, msg any) {
	m.broadcasts = append(m.broadcasts, msg)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func testService(t *testing.T) (*usecase.Service, *mockRepo, *mockStore, *mockHub) {
	t.Helper()
	repo := newMockRepo()
	store := newMockStore()
	hub := &mockHub{}
	svc := usecase.NewService(repo, store, hub, usecase.Config{
		MaxFileSize:  100 * 1024 * 1024 * 1024, // 100 GB
		MaxChunkSize: 500 * 1024 * 1024,        // 500 MB
		TransferTTL:  24 * time.Hour,
	})
	return svc, repo, store, hub
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestInitiate(t *testing.T) {
	svc, repo, _, _ := testService(t)

	resp, err := svc.Initiate(usecase.InitiateRequest{
		FileName:    "test.txt",
		FileSize:    1024,
		TotalChunks: 2,
		ChunkSize:   512,
	})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	if resp.Code == "" {
		t.Error("expected non-empty code")
	}
	if resp.ExpiresAt.IsZero() {
		t.Error("expected non-zero ExpiresAt")
	}
	if len(repo.transfers) != 1 {
		t.Errorf("expected 1 transfer in repo, got %d", len(repo.transfers))
	}
}

func TestInitiateFileTooLarge(t *testing.T) {
	svc, _, _, _ := testService(t)

	_, err := svc.Initiate(usecase.InitiateRequest{
		FileName:    "big.bin",
		FileSize:    200 * 1024 * 1024 * 1024, // 200 GB > 100 GB limit
		TotalChunks: 1,
		ChunkSize:   512,
	})
	if err == nil {
		t.Fatal("expected error for oversized file, got nil")
	}
}

func TestInitiateChunkTooLarge(t *testing.T) {
	svc, _, _, _ := testService(t)

	_, err := svc.Initiate(usecase.InitiateRequest{
		FileName:    "file.bin",
		FileSize:    1024,
		TotalChunks: 1,
		ChunkSize:   600 * 1024 * 1024, // > 500 MB limit
	})
	if err == nil {
		t.Fatal("expected error for oversized chunk, got nil")
	}
}

func TestInitiateInvalidTotalChunks(t *testing.T) {
	svc, _, _, _ := testService(t)

	_, err := svc.Initiate(usecase.InitiateRequest{
		FileName:    "file.bin",
		FileSize:    1024,
		TotalChunks: 0,
		ChunkSize:   512,
	})
	if err == nil {
		t.Fatal("expected error for zero totalChunks, got nil")
	}
}

func TestInitiateSanitizesFilename(t *testing.T) {
	svc, repo, _, _ := testService(t)

	_, err := svc.Initiate(usecase.InitiateRequest{
		FileName:    `../../etc/passwd`,
		FileSize:    100,
		TotalChunks: 1,
		ChunkSize:   100,
	})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}

	// The stored filename must not contain path traversal.
	for _, tr := range repo.transfers {
		if strings.Contains(tr.FileName, "..") || strings.Contains(tr.FileName, "/") {
			t.Errorf("filename not sanitized: %q", tr.FileName)
		}
	}
}

func TestGetInfo(t *testing.T) {
	svc, _, _, _ := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "info.txt", FileSize: 100, TotalChunks: 1, ChunkSize: 100,
	})

	info, err := svc.GetInfo(resp.Code)
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if info.Code != resp.Code {
		t.Errorf("code = %q, want %q", info.Code, resp.Code)
	}
	if info.FileName != "info.txt" {
		t.Errorf("fileName = %q, want info.txt", info.FileName)
	}
}

func TestGetInfoNotFound(t *testing.T) {
	svc, _, _, _ := testService(t)

	_, err := svc.GetInfo("NOTEXIST")
	if err != domain.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetInfoExpired(t *testing.T) {
	repo := newMockRepo()
	store := newMockStore()
	hub := &mockHub{}
	svc := usecase.NewService(repo, store, hub, usecase.Config{
		MaxFileSize:  1e12,
		MaxChunkSize: 1e9,
		TransferTTL:  time.Millisecond, // immediately expires
	})

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "exp.txt", FileSize: 100, TotalChunks: 1, ChunkSize: 100,
	})

	time.Sleep(10 * time.Millisecond)

	_, err := svc.GetInfo(resp.Code)
	if err != domain.ErrTransferExpired {
		t.Errorf("expected ErrTransferExpired, got %v", err)
	}
}

func TestUploadChunk(t *testing.T) {
	svc, _, store, hub := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "up.bin", FileSize: 200, TotalChunks: 2, ChunkSize: 100,
	})

	err := svc.UploadChunk(resp.Code, 0, bytes.NewReader([]byte("chunk data")))
	if err != nil {
		t.Fatalf("UploadChunk: %v", err)
	}

	// Chunk must be stored.
	if _, ok := store.chunks[resp.Code+"/0"]; !ok {
		t.Error("chunk 0 not found in store")
	}

	// Hub must have been notified.
	if len(hub.broadcasts) == 0 {
		t.Error("expected hub broadcast after upload")
	}
}

func TestUploadChunkNotFound(t *testing.T) {
	svc, _, _, _ := testService(t)

	err := svc.UploadChunk("NOTEXIST", 0, bytes.NewReader([]byte("data")))
	if err != domain.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUploadChunkInvalidIndex(t *testing.T) {
	svc, _, _, _ := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "idx.bin", FileSize: 100, TotalChunks: 1, ChunkSize: 100,
	})

	err := svc.UploadChunk(resp.Code, 5, bytes.NewReader([]byte("data")))
	if err != domain.ErrInvalidIndex {
		t.Errorf("expected ErrInvalidIndex, got %v", err)
	}
}

func TestUploadChunkAfterComplete(t *testing.T) {
	svc, _, _, _ := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "done.bin", FileSize: 100, TotalChunks: 1, ChunkSize: 100,
	})
	svc.UploadChunk(resp.Code, 0, bytes.NewReader([]byte("data")))
	svc.CompleteTransfer(resp.Code)

	err := svc.UploadChunk(resp.Code, 0, bytes.NewReader([]byte("data")))
	if err != domain.ErrTransferDone {
		t.Errorf("expected ErrTransferDone, got %v", err)
	}
}

func TestDownloadChunk(t *testing.T) {
	svc, _, _, _ := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "dl.bin", FileSize: 100, TotalChunks: 1, ChunkSize: 100,
	})
	payload := []byte("encrypted chunk content")
	svc.UploadChunk(resp.Code, 0, bytes.NewReader(payload))

	rc, size, err := svc.DownloadChunk(resp.Code, 0)
	if err != nil {
		t.Fatalf("DownloadChunk: %v", err)
	}
	defer rc.Close()

	if size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", size, len(payload))
	}
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, payload) {
		t.Error("downloaded data mismatch")
	}
}

func TestDownloadChunkNotUploaded(t *testing.T) {
	svc, _, _, _ := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "notup.bin", FileSize: 100, TotalChunks: 2, ChunkSize: 50,
	})

	_, _, err := svc.DownloadChunk(resp.Code, 1)
	if err == nil {
		t.Error("expected error for not-yet-uploaded chunk, got nil")
	}
}

func TestCompleteTransfer(t *testing.T) {
	svc, repo, _, _ := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "comp.bin", FileSize: 100, TotalChunks: 2, ChunkSize: 50,
	})
	svc.UploadChunk(resp.Code, 0, bytes.NewReader([]byte("a")))
	svc.UploadChunk(resp.Code, 1, bytes.NewReader([]byte("b")))

	if err := svc.CompleteTransfer(resp.Code); err != nil {
		t.Fatalf("CompleteTransfer: %v", err)
	}

	tr := repo.transfers[resp.Code]
	if tr.Status != domain.StatusComplete {
		t.Errorf("status = %q, want complete", tr.Status)
	}
}

func TestCompleteTransferMissingChunks(t *testing.T) {
	svc, _, _, _ := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "partial.bin", FileSize: 100, TotalChunks: 2, ChunkSize: 50,
	})
	svc.UploadChunk(resp.Code, 0, bytes.NewReader([]byte("a")))
	// chunk 1 not uploaded

	err := svc.CompleteTransfer(resp.Code)
	if err == nil {
		t.Error("expected error for missing chunks, got nil")
	}
}

func TestDownloadChunkInvalidIndex(t *testing.T) {
	svc, _, _, _ := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "idx.bin", FileSize: 100, TotalChunks: 1, ChunkSize: 100,
	})

	_, _, err := svc.DownloadChunk(resp.Code, 5)
	if err != domain.ErrInvalidIndex {
		t.Errorf("expected ErrInvalidIndex, got %v", err)
	}
}

func TestDownloadChunkNotFound(t *testing.T) {
	svc, _, _, _ := testService(t)

	_, _, err := svc.DownloadChunk("NOTEXIST", 0)
	if err != domain.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCompleteTransferNotFound(t *testing.T) {
	svc, _, _, _ := testService(t)

	err := svc.CompleteTransfer("NOTFOUND")
	if err != domain.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCleanupExpired(t *testing.T) {
	repo := newMockRepo()
	store := newMockStore()
	hub := &mockHub{}
	svc := usecase.NewService(repo, store, hub, usecase.Config{
		MaxFileSize:  1e12,
		MaxChunkSize: 1e9,
		TransferTTL:  time.Millisecond,
	})

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "exp.bin", FileSize: 100, TotalChunks: 1, ChunkSize: 100,
	})

	time.Sleep(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.CleanupExpired(ctx, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	cancel()

	if _, ok := repo.transfers[resp.Code]; ok {
		t.Error("expected expired transfer to be cleaned up")
	}
}

func TestCleanupRemovesCompletedAfterTTL(t *testing.T) {
	repo := newMockRepo()
	store := newMockStore()
	hub := &mockHub{}
	svc := usecase.NewService(repo, store, hub, usecase.Config{
		MaxFileSize:  1e12,
		MaxChunkSize: 1e9,
		TransferTTL:  time.Millisecond,
	})

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "comp.bin", FileSize: 100, TotalChunks: 1, ChunkSize: 100,
	})
	svc.UploadChunk(resp.Code, 0, bytes.NewReader([]byte("data")))
	svc.CompleteTransfer(resp.Code)

	time.Sleep(10 * time.Millisecond) // let TTL expire

	ctx, cancel := context.WithCancel(context.Background())
	go svc.CleanupExpired(ctx, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	cancel()

	if _, ok := repo.transfers[resp.Code]; ok {
		t.Error("completed+expired transfer should be removed by cleanup")
	}
}

func TestDeleteTransfer(t *testing.T) {
	svc, repo, store, _ := testService(t)

	resp, _ := svc.Initiate(usecase.InitiateRequest{
		FileName: "del.bin", FileSize: 100, TotalChunks: 1, ChunkSize: 100,
	})
	svc.UploadChunk(resp.Code, 0, bytes.NewReader([]byte("data")))

	if err := svc.DeleteTransfer(resp.Code); err != nil {
		t.Fatalf("DeleteTransfer: %v", err)
	}

	if _, ok := repo.transfers[resp.Code]; ok {
		t.Error("transfer still exists in repo after delete")
	}
	if _, ok := store.chunks[resp.Code+"/0"]; ok {
		t.Error("chunk still exists in store after delete")
	}
}
