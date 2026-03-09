package delivery

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"Qanal/internal/domain"
	"Qanal/internal/usecase"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
)

// ServicePort is the set of service operations used by the HTTP handler.
// Depending on an interface (not *usecase.Service) keeps delivery testable
// and decouples it from the concrete implementation.
type ServicePort interface {
	Initiate(usecase.InitiateRequest) (*usecase.InitiateResponse, error)
	GetInfo(code string) (*usecase.TransferInfo, error)
	UploadChunk(code string, index int, r io.Reader) error
	DownloadChunk(code string, index int) (io.ReadCloser, int64, error)
	CompleteTransfer(code string) error
	DeleteTransfer(code string) error
}

// HubPort is the WebSocket hub interface used by the handler.
// Using an interface instead of *Hub makes the handler testable in isolation.
type HubPort interface {
	Broadcast(code string, msg any)
	ServeWS(conn *websocket.Conn, code string)
}

var (
	// downloadBufPool avoids a 4 MB allocation on every chunk download.
	downloadBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, 4*1024*1024)
			return &b
		},
	}
)

type Handler struct {
	svc        ServicePort
	hub        HubPort
	limiter    *ipLimiter
	streams    *streamHub     // zero-storage live streaming relay
	rdv        *rendezvousHub // P2P signaling / rendezvous
	publicCORS bool           // when true: allow any origin (web/Docker mode)
}

// HandlerOpts configures optional Handler behaviour.
type HandlerOpts struct {
	// PublicCORS allows any browser origin. Use in Docker/web deployments.
	PublicCORS bool
}

func NewHandler(svc ServicePort, hub HubPort, opts ...HandlerOpts) *Handler {
	h := &Handler{svc: svc, hub: hub, limiter: newIPLimiter(), streams: newStreamHub(), rdv: newRendezvousHub()}
	if len(opts) > 0 {
		h.publicCORS = opts[0].PublicCORS
	}
	return h
}

func (h *Handler) wsUpgrader() websocket.Upgrader {
	if h.publicCORS {
		return websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(*http.Request) bool { return true },
		}
	}
	return websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		// Only allow same-machine browser origins (Wails WebView or localhost dev).
		CheckOrigin: func(r *http.Request) bool {
			return isLocalOrigin(r.Header.Get("Origin"))
		},
	}
}

// Close stops the background goroutines owned by the handler (rate limiter).
func (h *Handler) Close() {
	h.limiter.Close()
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(h.corsMiddleware)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, map[string]string{"status": "ok"}, http.StatusOK)
	})

	// Echoes the caller's external TCP address — used by peers to discover
	// their NAT-mapped address without STUN (TCP-level, not UDP).
	r.Get("/myaddr", func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, map[string]string{"addr": r.RemoteAddr}, http.StatusOK)
	})

	// P2P rendezvous signaling — tiny JSON messages only, no file data.
	r.Post("/api/v1/p2p/register", h.rdv.handleRegister)
	r.Get("/api/v1/p2p/wait/{code}", h.rdv.handleWait)
	r.Post("/api/v1/p2p/meet/{code}", h.rdv.handleMeet)

	r.Route("/api/v1", func(r chi.Router) {
		r.With(h.rateLimitMiddleware).Post("/transfers", h.createTransfer)
		r.Get("/transfers/{code}", h.getTransfer)
		r.Post("/transfers/{code}/complete", h.completeTransfer)
		r.Delete("/transfers/{code}", h.deleteTransfer)
		r.Put("/transfers/{code}/chunks/{index}", h.uploadChunk)
		r.Get("/transfers/{code}/chunks/{index}", h.downloadChunk)
	})

	r.Get("/ws/{code}", h.handleWS)

	// Zero-storage streaming relay: sender and receiver exchange data in real-time
	// without disk I/O. Both must be online simultaneously.
	// POST /api/v1/stream/{code}  — sender pushes the encrypted data stream
	// GET  /api/v1/stream/{code}  — receiver subscribes and pulls the stream
	r.Post("/api/v1/stream/{code}", h.streamSend)
	r.Get("/api/v1/stream/{code}", h.streamRecv)

	return r
}

func (h *Handler) createTransfer(w http.ResponseWriter, r *http.Request) {
	var req usecase.InitiateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	resp, err := h.svc.Initiate(req)
	if err != nil {
		if errors.Is(err, domain.ErrFileTooLarge) || errors.Is(err, domain.ErrChunkTooLarge) {
			jsonError(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, resp, http.StatusCreated)
}

func (h *Handler) getTransfer(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	info, err := h.svc.GetInfo(code)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			jsonError(w, "transfer not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, domain.ErrTransferExpired) {
			jsonError(w, "transfer expired", http.StatusGone)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, info, http.StatusOK)
}

func (h *Handler) uploadChunk(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	index, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil {
		jsonError(w, "invalid chunk index", http.StatusBadRequest)
		return
	}
	limited := io.LimitReader(r.Body, 510*1024*1024)
	if err := h.svc.UploadChunk(code, index, limited); err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			jsonError(w, "transfer not found", http.StatusNotFound)
		case errors.Is(err, domain.ErrTransferDone):
			jsonError(w, "transfer already complete", http.StatusConflict)
		case errors.Is(err, domain.ErrInvalidIndex):
			jsonError(w, "invalid chunk index", http.StatusBadRequest)
		case errors.Is(err, domain.ErrTransferExpired):
			jsonError(w, "transfer expired", http.StatusGone)
		default:
			slog.Error("upload chunk", "err", err)
			jsonError(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	jsonOK(w, map[string]string{"status": "ok"}, http.StatusOK)
}

func (h *Handler) downloadChunk(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	index, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil {
		jsonError(w, "invalid chunk index", http.StatusBadRequest)
		return
	}
	rc, size, err := h.svc.DownloadChunk(code, index)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			jsonError(w, "transfer not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, domain.ErrInvalidIndex) {
			jsonError(w, "invalid chunk index", http.StatusBadRequest)
			return
		}
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-store")

	bufPtr := downloadBufPool.Get().(*[]byte)
	if _, err := io.CopyBuffer(w, rc, *bufPtr); err != nil {
		// Headers already sent; log for observability only.
		slog.Warn("download chunk copy interrupted", "code", code, "index", index, "err", err)
	}
	downloadBufPool.Put(bufPtr)
}

func (h *Handler) completeTransfer(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if err := h.svc.CompleteTransfer(code); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			jsonError(w, "transfer not found", http.StatusNotFound)
			return
		}
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.hub.Broadcast(code, map[string]string{"type": "transfer_complete"})
	jsonOK(w, map[string]string{"status": "complete"}, http.StatusOK)
}

func (h *Handler) deleteTransfer(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if err := h.svc.DeleteTransfer(code); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"}, http.StatusOK)
}

func (h *Handler) handleWS(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	u := h.wsUpgrader()
	conn, err := u.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	h.hub.ServeWS(conn, code)
}

func jsonOK(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *Handler) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if h.publicCORS {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if isLocalOrigin(origin) && origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalOrigin reports whether an Origin header is from localhost.
// Empty origin (non-browser / direct requests) is always allowed.
// Uses exact hostname comparison to prevent prefix-bypass (e.g. localhost.evil.com).
func isLocalOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname() // strips port
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (h *Handler) streamSend(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if err := h.streams.waitAndPipe(code, r.Body, 5*time.Minute); err != nil {
		slog.Warn("stream send failed", "code", code, "err", err)
		jsonError(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"}, http.StatusOK)
}

func (h *Handler) streamRecv(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	if err := h.streams.subscribe(code, w, 5*time.Minute); err != nil {
		slog.Warn("stream recv failed", "code", code, "err", err)
		// Headers already written — log only, cannot send JSON error.
		return
	}
}
