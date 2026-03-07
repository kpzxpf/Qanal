package transfer_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"testing"

	"Qanal/internal/transfer"
)

func TestPeerSendReceiveRoundTrip(t *testing.T) {
	// 3 MB of random bytes (non-compressible) to stress both code paths.
	content := make([]byte, 3*1024*1024)
	rand.Read(content)

	srcFile, err := os.CreateTemp(t.TempDir(), "peer-src-*.bin")
	if err != nil {
		t.Fatalf("create source file: %v", err)
	}
	srcFile.Write(content)
	srcFile.Close()

	ps, err := transfer.StartPeer(srcFile.Name(), 1) // 1 MB chunks → 3 chunks
	if err != nil {
		t.Fatalf("StartPeer: %v", err)
	}

	ctx := context.Background()
	serveErr := make(chan error, 1)
	go func() { serveErr <- ps.Serve(ctx, nil) }()

	outDir := t.TempDir()
	outPath, err := transfer.PeerReceive(ctx, ps.Info.LAN, ps.Info.Code, ps.Info.Key, outDir, nil)
	if err != nil {
		t.Fatalf("PeerReceive: %v", err)
	}
	if err := <-serveErr; err != nil {
		t.Fatalf("Serve: %v", err)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("P2P round-trip data mismatch (%d vs %d bytes)", len(got), len(content))
	}
}

func TestPeerSendReceiveCompressible(t *testing.T) {
	content := bytes.Repeat([]byte("hello world from qanal "), 50000)

	srcFile, _ := os.CreateTemp(t.TempDir(), "peer-comp-*.bin")
	srcFile.Write(content)
	srcFile.Close()

	ps, err := transfer.StartPeer(srcFile.Name(), 1)
	if err != nil {
		t.Fatalf("StartPeer: %v", err)
	}

	ctx := context.Background()
	serveErr := make(chan error, 1)
	go func() { serveErr <- ps.Serve(ctx, nil) }()

	outDir := t.TempDir()
	outPath, err := transfer.PeerReceive(ctx, ps.Info.LAN, ps.Info.Code, ps.Info.Key, outDir, nil)
	if err != nil {
		t.Fatalf("PeerReceive compressible: %v", err)
	}
	if err := <-serveErr; err != nil {
		t.Fatalf("Serve compressible: %v", err)
	}

	got, _ := os.ReadFile(outPath)
	if !bytes.Equal(got, content) {
		t.Error("compressible P2P round-trip mismatch")
	}
}

func TestPeerProgressCallback(t *testing.T) {
	content := bytes.Repeat([]byte("z"), 3*1024*1024)
	srcFile, _ := os.CreateTemp(t.TempDir(), "peer-prog-*.bin")
	srcFile.Write(content)
	srcFile.Close()

	ps, _ := transfer.StartPeer(srcFile.Name(), 1)

	ctx := context.Background()
	senderEvents := 0
	receiverEvents := 0
	serveErr := make(chan error, 1)

	go func() {
		serveErr <- ps.Serve(ctx, func(e transfer.ProgressEvent) {
			senderEvents++
		})
	}()

	outDir := t.TempDir()
	_, err := transfer.PeerReceive(ctx, ps.Info.LAN, ps.Info.Code, ps.Info.Key, outDir, func(e transfer.ProgressEvent) {
		receiverEvents++
	})
	if err != nil {
		t.Fatalf("PeerReceive: %v", err)
	}
	<-serveErr

	if senderEvents == 0 {
		t.Error("expected sender progress events")
	}
	if receiverEvents == 0 {
		t.Error("expected receiver progress events")
	}
}

func TestPeerWrongCode(t *testing.T) {
	srcFile, _ := os.CreateTemp(t.TempDir(), "peer-code-*.bin")
	srcFile.WriteString("data")
	srcFile.Close()

	ps, _ := transfer.StartPeer(srcFile.Name(), 1)

	ctx := context.Background()
	serveErr := make(chan error, 1)
	go func() { serveErr <- ps.Serve(ctx, nil) }()

	_, err := transfer.PeerReceive(ctx, ps.Info.LAN, "WRONGCOD", ps.Info.Key, t.TempDir(), nil)
	if err == nil {
		t.Error("expected error for wrong auth code")
	}

	if serveErr := <-serveErr; serveErr == nil {
		t.Error("expected Serve to return error for bad code")
	}
}

func TestPeerClose(t *testing.T) {
	srcFile, _ := os.CreateTemp(t.TempDir(), "peer-close-*.bin")
	srcFile.WriteString("data")
	srcFile.Close()

	ps, err := transfer.StartPeer(srcFile.Name(), 1)
	if err != nil {
		t.Fatalf("StartPeer: %v", err)
	}

	ctx := context.Background()
	serveErr := make(chan error, 1)
	go func() { serveErr <- ps.Serve(ctx, nil) }()

	ps.Close()

	if err := <-serveErr; err == nil {
		t.Error("expected Serve to return error after Close()")
	}
}

func TestPeerContextCancellation(t *testing.T) {
	srcFile, _ := os.CreateTemp(t.TempDir(), "peer-ctx-*.bin")
	srcFile.WriteString("data")
	srcFile.Close()

	ps, _ := transfer.StartPeer(srcFile.Name(), 1)

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- ps.Serve(ctx, nil) }()

	cancel()

	if err := <-serveErr; err == nil {
		t.Error("expected Serve to return error after context cancellation")
	}
}

func TestPeerReceiveInvalidKey(t *testing.T) {
	_, err := transfer.PeerReceive(context.Background(), "127.0.0.1:1", "ABCD1234", "not-valid-key!!!", t.TempDir(), nil)
	if err == nil {
		t.Error("expected error for invalid key")
	}
}

func TestPeerReceiveShortKey(t *testing.T) {
	shortKey := base64.RawURLEncoding.EncodeToString([]byte("tooshort"))
	_, err := transfer.PeerReceive(context.Background(), "127.0.0.1:1", "ABCD1234", shortKey, t.TempDir(), nil)
	if err == nil {
		t.Error("expected error for key shorter than 32 bytes")
	}
}
