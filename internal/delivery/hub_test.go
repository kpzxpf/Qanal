package delivery_test

import (
	"context"
	"testing"
	"time"

	"Qanal/internal/delivery"
)

func TestHubBroadcastDoesNotBlock(t *testing.T) {
	hub := delivery.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	// Broadcast to a code with no connected clients — must not block.
	done := make(chan struct{})
	go func() {
		hub.Broadcast("TESTCODE", map[string]string{"type": "ping"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("Broadcast blocked for more than 1 second")
	}
}

func TestHubBroadcastInvalidMessage(t *testing.T) {
	hub := delivery.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	// A channel value cannot be JSON-marshalled — Broadcast must not panic.
	hub.Broadcast("CODE", make(chan int))
}

func TestHubRunCancellation(t *testing.T) {
	hub := delivery.NewHub()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("hub.Run did not return after context cancellation")
	}
}

func TestHubBroadcastFullChannel(t *testing.T) {
	hub := delivery.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	// Flood the broadcast channel (capacity 512) — should not deadlock.
	for i := 0; i < 600; i++ {
		hub.Broadcast("FLOOD", map[string]int{"i": i})
	}
}
