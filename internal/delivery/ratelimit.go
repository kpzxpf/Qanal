package delivery

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	// maxTransfersPerWindow is the maximum number of new transfers a single IP
	// may initiate within windowDuration. This prevents relay server abuse.
	maxTransfersPerWindow = 10
	windowDuration        = time.Minute
	cleanupInterval       = 5 * time.Minute
)

type windowEntry struct {
	count   int
	resetAt time.Time
}

// ipLimiter is a simple sliding-window rate limiter keyed by client IP.
// It is safe for concurrent use and cleans up stale entries periodically.
// Call Close() to stop the background cleanup goroutine.
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

// Close stops the background cleanup goroutine.
func (l *ipLimiter) Close() {
	close(l.stopCh)
}

// Allow returns true if the request from remoteAddr is within the rate limit.
func (l *ipLimiter) Allow(remoteAddr string) bool {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr // no port present, use as-is
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	w, ok := l.windows[ip]
	if !ok || now.After(w.resetAt) {
		l.windows[ip] = &windowEntry{count: 1, resetAt: now.Add(windowDuration)}
		return true
	}
	w.count++
	return w.count <= maxTransfersPerWindow
}

// cleanup removes expired entries every cleanupInterval to bound memory growth.
// Exits when Close() is called.
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

// rateLimitMiddleware rejects requests that exceed the per-IP rate limit with
// HTTP 429 Too Many Requests and a Retry-After header.
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
