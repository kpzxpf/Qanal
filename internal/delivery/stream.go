package delivery

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// streamHub implements a zero-storage streaming relay: sender and receiver
// are connected in real-time through an in-memory io.Pipe — no disk I/O at
// the relay server. Ideal for inter-city transfers where both parties are
// online simultaneously.
//
// Protocol:
//
//	POST /api/v1/stream/{code}  — sender pushes encrypted data stream
//	GET  /api/v1/stream/{code}  — receiver pulls the stream live
//
// The relay holds no file data; chunks flow directly from sender's TCP socket
// to receiver's TCP socket with a kernel-level buffer. Latency is bounded by
// network RTT rather than disk throughput.
type streamHub struct {
	mu    sync.Mutex
	slots map[string]*streamSlot
}

type streamSlot struct {
	pr        *io.PipeReader
	pw        *io.PipeWriter
	once      sync.Once     // ensures only one receiver claims the slot
	readyC    chan struct{} // closed by the first receiver to connect
	createdAt time.Time
}

func newStreamHub() *streamHub {
	h := &streamHub{slots: make(map[string]*streamSlot)}
	go h.cleanup()
	return h
}

// waitAndPipe is called by the sender handler.
// It registers a slot for code, waits up to timeout for a receiver to connect,
// then copies body into the pipe (which the receiver reads from).
func (h *streamHub) waitAndPipe(code string, body io.Reader, timeout time.Duration) error {
	pr, pw := io.Pipe()
	slot := &streamSlot{
		pr:        pr,
		pw:        pw,
		readyC:    make(chan struct{}),
		createdAt: time.Now(),
	}

	h.mu.Lock()
	if _, exists := h.slots[code]; exists {
		h.mu.Unlock()
		pr.Close()
		pw.Close()
		return fmt.Errorf("stream slot already exists for code %s", code)
	}
	h.slots[code] = slot
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.slots, code)
		h.mu.Unlock()
		pw.CloseWithError(io.ErrClosedPipe)
	}()

	// Wait for receiver to connect before starting to read body.
	select {
	case <-slot.readyC:
	case <-time.After(timeout):
		return fmt.Errorf("no receiver connected within %s", timeout)
	}

	// Stream body to pipe; receiver reads the other end concurrently.
	if _, err := io.Copy(pw, body); err != nil {
		pw.CloseWithError(err)
		return fmt.Errorf("stream to receiver: %w", err)
	}
	return pw.Close()
}

// subscribe is called by the receiver handler.
// It polls for the sender's slot (up to timeout), signals readiness,
// then copies the pipe into w.
func (h *streamHub) subscribe(code string, w io.Writer, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var slot *streamSlot
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
		return fmt.Errorf("no sender connected for code %s", code)
	}

	// Only the first receiver may claim the slot.
	claimed := false
	slot.once.Do(func() {
		close(slot.readyC)
		claimed = true
	})
	if !claimed {
		return fmt.Errorf("another receiver already connected for code %s", code)
	}

	if _, err := io.Copy(w, slot.pr); err != nil {
		return fmt.Errorf("receive stream: %w", err)
	}
	return nil
}

// cleanup removes orphaned slots (sender connected, no receiver, slot expired).
func (h *streamHub) cleanup() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		h.mu.Lock()
		for code, slot := range h.slots {
			if slot.createdAt.Before(cutoff) {
				slog.Warn("stream: cleaning up orphaned slot", "code", code)
				slot.pw.CloseWithError(io.ErrClosedPipe)
				delete(h.slots, code)
			}
		}
		h.mu.Unlock()
	}
}
