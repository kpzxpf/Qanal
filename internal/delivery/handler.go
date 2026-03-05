package delivery

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"Qanal/internal/domain"
	"Qanal/internal/usecase"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type Handler struct {
	svc *usecase.Service
	hub *Hub
}

func NewHandler(svc *usecase.Service, hub *Hub) *Handler {
	return &Handler{svc: svc, hub: hub}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/transfers", h.createTransfer)
		r.Get("/transfers/{code}", h.getTransfer)
		r.Post("/transfers/{code}/complete", h.completeTransfer)
		r.Delete("/transfers/{code}", h.deleteTransfer)
		r.Put("/transfers/{code}/chunks/{index}", h.uploadChunk)
		r.Get("/transfers/{code}/chunks/{index}", h.downloadChunk)
	})

	r.Get("/ws/{code}", h.handleWS)

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
	buf := make([]byte, 4*1024*1024)
	io.CopyBuffer(w, rc, buf)
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
	conn, err := upgrader.Upgrade(w, r, nil)
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

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
