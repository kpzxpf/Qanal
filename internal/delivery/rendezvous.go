package delivery

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

type rdvPeer struct{ WAN, LAN string }

type rdvSlot struct {
	senderWAN string
	senderLAN string
	peerC     chan rdvPeer
	createdAt time.Time
}

type rendezvousHub struct {
	mu    sync.Mutex
	slots map[string]*rdvSlot
}

func newRendezvousHub() *rendezvousHub {
	h := &rendezvousHub{slots: make(map[string]*rdvSlot)}
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			h.mu.Lock()
			for code, s := range h.slots {
				if time.Since(s.createdAt) > 15*time.Minute {
					delete(h.slots, code)
				}
			}
			h.mu.Unlock()
		}
	}()
	return h
}

// POST /api/v1/p2p/register  — sender announces itself
// Body: {"code":"…","wan":"IP:PORT","lan":"IP:PORT"}
func (h *rendezvousHub) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
		WAN  string `json:"wan"`
		LAN  string `json:"lan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		jsonError(w, "bad request", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	h.slots[req.Code] = &rdvSlot{
		senderWAN: req.WAN,
		senderLAN: req.LAN,
		peerC:     make(chan rdvPeer, 1),
		createdAt: time.Now(),
	}
	h.mu.Unlock()
	jsonOK(w, map[string]string{"status": "registered"}, http.StatusOK)
}

// GET /api/v1/p2p/wait/{code}  — sender long-polls until receiver joins (≤ 5 min)
// Response: {"receiverWAN":"…","receiverLAN":"…"}
func (h *rendezvousHub) handleWait(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	h.mu.Lock()
	slot := h.slots[code]
	h.mu.Unlock()
	if slot == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}
	select {
	case peer := <-slot.peerC:
		jsonOK(w, map[string]string{
			"receiverWAN": peer.WAN,
			"receiverLAN": peer.LAN,
		}, http.StatusOK)
	case <-time.After(5 * time.Minute):
		jsonError(w, "timeout", http.StatusGatewayTimeout)
	case <-r.Context().Done():
	}
}

// POST /api/v1/p2p/meet/{code}  — receiver announces itself, gets sender info
// Body: {"wan":"IP:PORT","lan":"IP:PORT"}
// Response: {"senderWAN":"…","senderLAN":"…"}
func (h *rendezvousHub) handleMeet(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var req struct {
		WAN string `json:"wan"`
		LAN string `json:"lan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "bad request", http.StatusBadRequest)
		return
	}

	// Poll briefly — sender might register just after receiver parses the link.
	var slot *rdvSlot
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		slot = h.slots[code]
		h.mu.Unlock()
		if slot != nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if slot == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	// Notify sender (non-blocking; sender may have already timed out).
	select {
	case slot.peerC <- rdvPeer{WAN: req.WAN, LAN: req.LAN}:
	default:
	}

	jsonOK(w, map[string]string{
		"senderWAN": slot.senderWAN,
		"senderLAN": slot.senderLAN,
	}, http.StatusOK)
}
