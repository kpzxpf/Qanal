package transfer_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"Qanal/internal/transfer"
)

// minimalRelayServer is an in-process HTTP relay that mimics the real relay API.
// It stores encrypted chunks in memory so Send() and Receive() can be tested
// without starting a full Wails application.
type minimalRelayServer struct {
	mu        sync.Mutex
	transfers map[string]*relayTransfer
	mux       *http.ServeMux
}

type relayTransfer struct {
	fileName    string
	fileSize    int64
	totalChunks int
	chunkSize   int64
	chunks      map[int][]byte
	complete    bool
}

func newRelayServer() *minimalRelayServer {
	s := &minimalRelayServer{
		transfers: make(map[string]*relayTransfer),
		mux:       http.NewServeMux(),
	}
	s.mux.HandleFunc("/api/v1/transfers", s.handleCreate)
	s.mux.HandleFunc("/api/v1/transfers/", s.handleTransfer)
	return s
}

func (s *minimalRelayServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *minimalRelayServer) handleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		FileName    string `json:"fileName"`
		FileSize    int64  `json:"fileSize"`
		TotalChunks int    `json:"totalChunks"`
		ChunkSize   int64  `json:"chunkSize"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	code := fmt.Sprintf("TEST%04d", len(s.transfers))
	s.mu.Lock()
	s.transfers[code] = &relayTransfer{
		fileName:    req.FileName,
		fileSize:    req.FileSize,
		totalChunks: req.TotalChunks,
		chunkSize:   req.ChunkSize,
		chunks:      make(map[int][]byte),
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"code": code})
}

func (s *minimalRelayServer) handleTransfer(w http.ResponseWriter, r *http.Request) {
	// Routes: /api/v1/transfers/{code}
	//         /api/v1/transfers/{code}/complete
	//         /api/v1/transfers/{code}/chunks/{index}
	path := r.URL.Path[len("/api/v1/transfers/"):]

	var code, rest string
	for i, c := range path {
		if c == '/' {
			code = path[:i]
			rest = path[i+1:]
			break
		}
	}
	if code == "" {
		code = path
	}

	s.mu.Lock()
	tr, ok := s.transfers[code]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	switch {
	case rest == "" && r.Method == http.MethodGet:
		// GET /transfers/{code}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"fileName":    tr.fileName,
			"fileSize":    tr.fileSize,
			"totalChunks": tr.totalChunks,
			"chunkSize":   tr.chunkSize,
		})

	case rest == "complete" && r.Method == http.MethodPost:
		s.mu.Lock()
		tr.complete = true
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "complete"})

	case len(rest) > 7 && rest[:7] == "chunks/":
		var idx int
		fmt.Sscanf(rest[7:], "%d", &idx)

		switch r.Method {
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			tr.chunks[idx] = data
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case http.MethodGet:
			s.mu.Lock()
			data, exists := tr.chunks[idx]
			s.mu.Unlock()
			if !exists {
				http.Error(w, "chunk not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(data)
		}
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestSendAndReceiveRoundTrip(t *testing.T) {
	relay := newRelayServer()
	srv := httptest.NewServer(relay)
	defer srv.Close()

	// Create a temp source file with known content.
	content := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 1000)
	srcFile, err := os.CreateTemp(t.TempDir(), "src-*.bin")
	if err != nil {
		t.Fatalf("create source file: %v", err)
	}
	srcFile.Write(content)
	srcFile.Close()

	ctx := context.Background()

	// Send the file via the relay server (1 MB chunks, 2 workers).
	result, err := transfer.Send(ctx, srv.URL, srcFile.Name(), 1, 2, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Code == "" || result.Key == "" {
		t.Fatal("Send returned empty code or key")
	}

	// Receive into a temp output directory.
	outDir := t.TempDir()
	outPath, err := transfer.Receive(ctx, srv.URL, result.Code, result.Key, outDir, 2, nil)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	// Verify the received file matches the original.
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("received content mismatch: got %d bytes, want %d bytes", len(got), len(content))
	}
}

func TestSendProgressCallback(t *testing.T) {
	relay := newRelayServer()
	srv := httptest.NewServer(relay)
	defer srv.Close()

	content := bytes.Repeat([]byte("x"), 3*1024*1024) // 3 MB → 3 chunks of 1 MB
	srcFile, _ := os.CreateTemp(t.TempDir(), "src-*.bin")
	srcFile.Write(content)
	srcFile.Close()

	var callCount int
	_, err := transfer.Send(context.Background(), srv.URL, srcFile.Name(), 1, 1, func(e transfer.ProgressEvent) {
		callCount++
		if e.Total != 3 {
			t.Errorf("progress.Total = %d, want 3", e.Total)
		}
		if e.TotalBytes != int64(len(content)) {
			t.Errorf("progress.TotalBytes = %d, want %d", e.TotalBytes, len(content))
		}
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if callCount == 0 {
		t.Error("expected progress callback to be called at least once")
	}
}

func TestSendContextCancellation(t *testing.T) {
	// Hang the relay server so we can cancel mid-transfer.
	block := make(chan struct{})
	hangServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/transfers" {
			// Allow transfer creation but block chunk uploads.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"code": "HANGTEST"})
			return
		}
		// Block all other requests.
		<-block
	}))
	defer hangServer.Close()
	defer close(block)

	content := bytes.Repeat([]byte("y"), 512*1024) // 512 KB
	srcFile, _ := os.CreateTemp(t.TempDir(), "src-*.bin")
	srcFile.Write(content)
	srcFile.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := transfer.Send(ctx, hangServer.URL, srcFile.Name(), 1, 1, nil)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestReceiveInvalidKey(t *testing.T) {
	outDir := t.TempDir()
	_, err := transfer.Receive(context.Background(), "http://localhost:1", "CODE", "not-base64!!!", outDir, 1, nil)
	if err == nil {
		t.Error("expected error for invalid key")
	}
}

func TestReceiveShortKey(t *testing.T) {
	outDir := t.TempDir()
	shortKey := base64.RawURLEncoding.EncodeToString([]byte("tooshort"))
	_, err := transfer.Receive(context.Background(), "http://localhost:1", "CODE", shortKey, outDir, 1, nil)
	if err == nil {
		t.Error("expected error for key shorter than 32 bytes")
	}
}

func TestSendEmptyFile(t *testing.T) {
	relay := newRelayServer()
	srv := httptest.NewServer(relay)
	defer srv.Close()

	srcFile, _ := os.CreateTemp(t.TempDir(), "empty-*.bin")
	srcFile.Close()

	result, err := transfer.Send(context.Background(), srv.URL, srcFile.Name(), 1, 1, nil)
	if err != nil {
		t.Fatalf("Send empty file: %v", err)
	}

	outDir := t.TempDir()
	outPath, err := transfer.Receive(context.Background(), srv.URL, result.Code, result.Key, outDir, 1, nil)
	if err != nil {
		t.Fatalf("Receive empty file: %v", err)
	}

	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("expected 0-byte file, got %d bytes", info.Size())
	}
}

func TestGetLocalIP(t *testing.T) {
	ip := transfer.GetLocalIP()
	if ip == "" {
		t.Error("GetLocalIP returned empty string")
	}
}

func TestGetFileInfo(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "info-*.txt")
	f.WriteString("hello")
	f.Close()

	info, err := transfer.GetFileInfo(f.Name())
	if err != nil {
		t.Fatalf("GetFileInfo: %v", err)
	}
	if info.Size != 5 {
		t.Errorf("size = %d, want 5", info.Size)
	}
	if info.Path != f.Name() {
		t.Errorf("path = %q, want %q", info.Path, f.Name())
	}
}

func TestGetFileInfoDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := transfer.GetFileInfo(dir)
	if err == nil {
		t.Error("expected error for directory path, got nil")
	}
}

func TestGetFileInfoNotExist(t *testing.T) {
	_, err := transfer.GetFileInfo(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestSendReceiveLargerFile(t *testing.T) {
	relay := newRelayServer()
	srv := httptest.NewServer(relay)
	defer srv.Close()

	// 8 MB file — 4 chunks of 2 MB, 4 workers.
	content := make([]byte, 8*1024*1024)
	rand.Read(content)

	srcFile, _ := os.CreateTemp(t.TempDir(), "large-*.bin")
	srcFile.Write(content)
	srcFile.Close()

	result, err := transfer.Send(context.Background(), srv.URL, srcFile.Name(), 2, 4, nil)
	if err != nil {
		t.Fatalf("Send large: %v", err)
	}

	outDir := t.TempDir()
	outPath, err := transfer.Receive(context.Background(), srv.URL, result.Code, result.Key, outDir, 4, nil)
	if err != nil {
		t.Fatalf("Receive large: %v", err)
	}

	got, _ := os.ReadFile(outPath)
	if !bytes.Equal(got, content) {
		t.Error("large file round-trip data mismatch")
	}
}
