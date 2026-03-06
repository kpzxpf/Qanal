package delivery_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Qanal/internal/delivery"
	"Qanal/internal/domain"
	"Qanal/internal/usecase"

	"github.com/gorilla/websocket"
)

// ─── Mock service ─────────────────────────────────────────────────────────────

type mockSvc struct {
	initiateResp *usecase.InitiateResponse
	initiateErr  error
	infoResp     *usecase.TransferInfo
	infoErr      error
	uploadErr    error
	downloadRC   io.ReadCloser
	downloadSize int64
	downloadErr  error
	completeErr  error
	deleteErr    error
}

func (m *mockSvc) Initiate(req usecase.InitiateRequest) (*usecase.InitiateResponse, error) {
	return m.initiateResp, m.initiateErr
}
func (m *mockSvc) GetInfo(code string) (*usecase.TransferInfo, error) {
	return m.infoResp, m.infoErr
}
func (m *mockSvc) UploadChunk(code string, index int, r io.Reader) error {
	return m.uploadErr
}
func (m *mockSvc) DownloadChunk(code string, index int) (io.ReadCloser, int64, error) {
	return m.downloadRC, m.downloadSize, m.downloadErr
}
func (m *mockSvc) CompleteTransfer(code string) error {
	return m.completeErr
}
func (m *mockSvc) DeleteTransfer(code string) error {
	return m.deleteErr
}

// ─── Mock hub ─────────────────────────────────────────────────────────────────

type mockHub struct{ broadcasts []any }

func (m *mockHub) Broadcast(_ string, msg any)         { m.broadcasts = append(m.broadcasts, msg) }
func (m *mockHub) ServeWS(_ *websocket.Conn, _ string) {}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newHandler(svc *mockSvc) (http.Handler, *mockHub) {
	hub := &mockHub{}
	h := delivery.NewHandler(svc, hub)
	return h.Router(), hub
}

func do(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// ─── createTransfer ───────────────────────────────────────────────────────────

func TestCreateTransfer_Success(t *testing.T) {
	svc := &mockSvc{
		initiateResp: &usecase.InitiateResponse{Code: "ABCD1234", ExpiresAt: time.Now().Add(time.Hour)},
	}
	router, _ := newHandler(svc)

	body := `{"fileName":"test.txt","fileSize":1024,"totalChunks":1,"chunkSize":1024}`
	w := do(t, router, http.MethodPost, "/api/v1/transfers", body)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["code"] != "ABCD1234" {
		t.Errorf("code = %v, want ABCD1234", resp["code"])
	}
}

func TestCreateTransfer_InvalidJSON(t *testing.T) {
	svc := &mockSvc{}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodPost, "/api/v1/transfers", "not json")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateTransfer_FileTooLarge(t *testing.T) {
	svc := &mockSvc{initiateErr: domain.ErrFileTooLarge}
	router, _ := newHandler(svc)

	body := `{"fileName":"big.bin","fileSize":999999999999999,"totalChunks":1,"chunkSize":1024}`
	w := do(t, router, http.MethodPost, "/api/v1/transfers", body)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

// ─── getTransfer ──────────────────────────────────────────────────────────────

func TestGetTransfer_Success(t *testing.T) {
	svc := &mockSvc{
		infoResp: &usecase.TransferInfo{Code: "TESTCODE", FileName: "f.bin"},
	}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodGet, "/api/v1/transfers/TESTCODE", "")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestGetTransfer_NotFound(t *testing.T) {
	svc := &mockSvc{infoErr: domain.ErrNotFound}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodGet, "/api/v1/transfers/MISSING", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetTransfer_Expired(t *testing.T) {
	svc := &mockSvc{infoErr: domain.ErrTransferExpired}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodGet, "/api/v1/transfers/EXPIRED", "")
	if w.Code != http.StatusGone {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGone)
	}
}

// ─── uploadChunk ──────────────────────────────────────────────────────────────

func TestUploadChunk_Success(t *testing.T) {
	svc := &mockSvc{}
	router, _ := newHandler(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/transfers/TESTCODE/chunks/0", bytes.NewReader([]byte("data")))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestUploadChunk_InvalidIndex(t *testing.T) {
	svc := &mockSvc{}
	router, _ := newHandler(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/transfers/TESTCODE/chunks/notanumber", bytes.NewReader([]byte("data")))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUploadChunk_TransferComplete(t *testing.T) {
	svc := &mockSvc{uploadErr: domain.ErrTransferDone}
	router, _ := newHandler(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/transfers/DONE/chunks/0", bytes.NewReader([]byte("data")))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestUploadChunk_Expired(t *testing.T) {
	svc := &mockSvc{uploadErr: domain.ErrTransferExpired}
	router, _ := newHandler(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/transfers/EXP/chunks/0", bytes.NewReader([]byte("data")))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGone)
	}
}

// ─── downloadChunk ────────────────────────────────────────────────────────────

func TestDownloadChunk_Success(t *testing.T) {
	payload := []byte("chunk binary data")
	svc := &mockSvc{
		downloadRC:   io.NopCloser(bytes.NewReader(payload)),
		downloadSize: int64(len(payload)),
	}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodGet, "/api/v1/transfers/TESTCODE/chunks/0", "")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Error("downloaded body mismatch")
	}
}

func TestDownloadChunk_NotFound(t *testing.T) {
	svc := &mockSvc{downloadErr: domain.ErrNotFound}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodGet, "/api/v1/transfers/MISSING/chunks/0", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// ─── completeTransfer ─────────────────────────────────────────────────────────

func TestCompleteTransfer_Success(t *testing.T) {
	svc := &mockSvc{}
	router, hub := newHandler(svc)

	w := do(t, router, http.MethodPost, "/api/v1/transfers/TESTCODE/complete", "")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(hub.broadcasts) == 0 {
		t.Error("expected hub broadcast on complete")
	}
}

func TestCompleteTransfer_NotFound(t *testing.T) {
	svc := &mockSvc{completeErr: domain.ErrNotFound}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodPost, "/api/v1/transfers/MISSING/complete", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// ─── deleteTransfer ───────────────────────────────────────────────────────────

func TestDeleteTransfer_Success(t *testing.T) {
	svc := &mockSvc{}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodDelete, "/api/v1/transfers/TESTCODE", "")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// ─── uploadChunk extra paths ──────────────────────────────────────────────────

func TestUploadChunk_NotFound(t *testing.T) {
	svc := &mockSvc{uploadErr: domain.ErrNotFound}
	router, _ := newHandler(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/transfers/MISSING/chunks/0", bytes.NewReader([]byte("d")))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUploadChunk_ServiceInvalidIndex(t *testing.T) {
	svc := &mockSvc{uploadErr: domain.ErrInvalidIndex}
	router, _ := newHandler(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/transfers/CODE/chunks/0", bytes.NewReader([]byte("d")))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// ─── downloadChunk extra paths ────────────────────────────────────────────────

func TestDownloadChunk_InvalidIndexBadParam(t *testing.T) {
	svc := &mockSvc{}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodGet, "/api/v1/transfers/CODE/chunks/abc", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDownloadChunk_ErrInvalidIndex(t *testing.T) {
	svc := &mockSvc{downloadErr: domain.ErrInvalidIndex}
	router, _ := newHandler(svc)

	w := do(t, router, http.MethodGet, "/api/v1/transfers/CODE/chunks/0", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// ─── CORS middleware ──────────────────────────────────────────────────────────

func TestCORSLocalOriginAllowed(t *testing.T) {
	svc := &mockSvc{
		initiateResp: &usecase.InitiateResponse{Code: "X"},
	}
	router, _ := newHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", strings.NewReader(`{"fileName":"f","fileSize":1,"totalChunks":1,"chunkSize":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:5173")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("expected CORS header to echo localhost origin, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSExternalOriginBlocked(t *testing.T) {
	svc := &mockSvc{
		initiateResp: &usecase.InitiateResponse{Code: "X"},
	}
	router, _ := newHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", strings.NewReader(`{"fileName":"f","fileSize":1,"totalChunks":1,"chunkSize":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example.com")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if acao := w.Header().Get("Access-Control-Allow-Origin"); acao != "" {
		t.Errorf("expected no CORS header for external origin, got %q", acao)
	}
}

func TestCORSPreflightReturns204(t *testing.T) {
	svc := &mockSvc{}
	router, _ := newHandler(svc)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/transfers", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

// ─── createTransfer rate limiting ─────────────────────────────────────────────

func TestCreateTransfer_ChunkTooLarge(t *testing.T) {
	svc := &mockSvc{initiateErr: domain.ErrChunkTooLarge}
	router, _ := newHandler(svc)

	body := `{"fileName":"f","fileSize":100,"totalChunks":1,"chunkSize":999999999999}`
	w := do(t, router, http.MethodPost, "/api/v1/transfers", body)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}
