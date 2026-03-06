package delivery

import (
	"testing"
)

func TestIPLimiterAllowsUnderLimit(t *testing.T) {
	l := newIPLimiter()

	for i := 0; i < maxTransfersPerWindow; i++ {
		if !l.Allow("127.0.0.1:1234") {
			t.Fatalf("request %d was rejected, expected it to be allowed", i+1)
		}
	}
}

func TestIPLimiterBlocksOverLimit(t *testing.T) {
	l := newIPLimiter()

	for i := 0; i < maxTransfersPerWindow; i++ {
		l.Allow("10.0.0.1:9999")
	}

	if l.Allow("10.0.0.1:9999") {
		t.Error("expected request to be blocked after limit reached")
	}
}

func TestIPLimiterIsolatesIPs(t *testing.T) {
	l := newIPLimiter()

	// Exhaust the limit for IP A.
	for i := 0; i < maxTransfersPerWindow+5; i++ {
		l.Allow("192.168.1.1:1111")
	}

	// IP B should be unaffected.
	if !l.Allow("192.168.1.2:2222") {
		t.Error("IP B should not be affected by IP A's rate limit")
	}
}

func TestIPLimiterHandlesNoPort(t *testing.T) {
	l := newIPLimiter()

	// RemoteAddr without port (unusual but should not panic).
	if !l.Allow("172.16.0.1") {
		t.Error("first request without port should be allowed")
	}
}

func TestIPLimiterAllowsAfterWindowReset(t *testing.T) {
	l := newIPLimiter()

	// Manually set the window entry to an already-expired state.
	l.mu.Lock()
	l.windows["1.2.3.4"] = &windowEntry{count: maxTransfersPerWindow + 1}
	// resetAt is zero, which is in the past, so the next Allow() resets the window.
	l.mu.Unlock()

	if !l.Allow("1.2.3.4:0") {
		t.Error("request should be allowed after window expiry")
	}
}
