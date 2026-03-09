package delivery

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	maxTransfersPerWindow = 10
	windowDuration        = time.Minute
	cleanupInterval       = 5 * time.Minute
	maxTrackedIPs         = 10_000 // cap against DDoS memory exhaustion
)

type windowEntry struct {
	count   int
	resetAt time.Time
}

type ipLimiter struct {
	mu      sync.Mutex
	windows map[string]*windowEntry
	stopCh  chan struct{}
}

func newIPLimiter() *ipLimiter {
	l := &ipLimiter{
		windows: make(map[string]*windowEntry),
		stopCh:  make(chan struct{}),
	}
	go l.cleanup()
	return l
}

func (l *ipLimiter) Close() {
	close(l.stopCh)
}

func (l *ipLimiter) Allow(remoteAddr string) bool {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	w, ok := l.windows[ip]
	if !ok || now.After(w.resetAt) {
		if !ok && len(l.windows) >= maxTrackedIPs {
			l.evictOldest()
		}
		l.windows[ip] = &windowEntry{count: 1, resetAt: now.Add(windowDuration)}
		return true
	}
	w.count++
	return w.count <= maxTransfersPerWindow
}

// evictOldest removes the entry with the earliest resetAt time.
// Called with l.mu held.
func (l *ipLimiter) evictOldest() {
	var oldestIP string
	var oldestTime time.Time
	for ip, w := range l.windows {
		if oldestIP == "" || w.resetAt.Before(oldestTime) {
			oldestIP = ip
			oldestTime = w.resetAt
		}
	}
	if oldestIP != "" {
		delete(l.windows, oldestIP)
	}
}

func (l *ipLimiter) cleanup() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			l.mu.Lock()
			for ip, w := range l.windows {
				if now.After(w.resetAt) {
					delete(l.windows, ip)
				}
			}
			l.mu.Unlock()
		case <-l.stopCh:
			return
		}
	}
}

func (h *Handler) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.limiter.Allow(r.RemoteAddr) {
			w.Header().Set("Retry-After", "60")
			jsonError(w, "rate limit exceeded: max 10 transfers per minute per IP", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
