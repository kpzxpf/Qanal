package transfer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PeerInfo is shown to the sender so they can share it with the recipient.
type PeerInfo struct {
	LAN   string `json:"lan"`   // local-network address: 192.168.x.x:PORT
	WAN   string `json:"wan"`   // internet address: x.x.x.x:PORT (empty if STUN failed)
	Code  string `json:"code"`  // 8-char session auth token
	Key   string `json:"key"`   // base64url AES-256 key
	Relay string `json:"relay"` // embedded relay URL for signaling + fallback
}

// PeerServer is the sender-side TCP listener for direct P2P transfers.
type PeerServer struct {
	listener net.Listener
	filePath string
	chunkMB  int
	key      []byte
	Info     *PeerInfo
}

// StartPeer creates a TCP listener on a random port, queries STUN servers to
// discover the public WAN address, registers with the relay rendezvous for
// signaling, and returns credentials immediately.
// relayURL is the embedded relay (e.g. http://192.168.1.5:8080).
// Call Serve() in a goroutine to wait for and handle the receiver connection.
func StartPeer(filePath, relayURL string, chunkMB int) (*PeerServer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	codeSrc := make([]byte, 5)
	if _, err := rand.Read(codeSrc); err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(codeSrc)[:8]

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	port := ln.Addr().(*net.TCPAddr).Port

	// STUN: discover external IP:port (blocks up to 3 s — acceptable at session start).
	wanAddr, _ := DiscoverWANAddr(port) // empty on failure; not fatal

	info := &PeerInfo{
		LAN:   fmt.Sprintf("%s:%d", GetLocalIP(), port),
		WAN:   wanAddr,
		Code:  code,
		Key:   base64.RawURLEncoding.EncodeToString(key),
		Relay: relayURL,
	}

	// Register with rendezvous so the receiver can find us and trigger hole punch.
	if relayURL != "" {
		go registerWithRendezvous(relayURL, code, wanAddr, info.LAN)
	}

	return &PeerServer{
		listener: ln,
		filePath: filePath,
		chunkMB:  chunkMB,
		key:      key,
		Info:     info,
	}, nil
}

// registerWithRendezvous POST-s sender info to the relay so the receiver can
// find us for signaling. Fire-and-forget; errors are non-fatal.
func registerWithRendezvous(relayURL, code, wan, lan string) {
	body := jsonMarshal(map[string]string{"code": code, "wan": wan, "lan": lan})
	post(relayURL+"/api/v1/p2p/register", body)
}

// pipeChunk carries one encrypted chunk through the pipeline goroutine.
type pipeChunk struct {
	data     []byte
	plainLen int64
	err      error
}

// Serve waits for the receiver connection — either via direct Accept() or via
// hole-punch outbound connect — then streams the file directly.
// Times out after 10 minutes if no receiver connects.
// Cancelling ctx interrupts the Accept() wait and any active transmission.
func (s *PeerServer) Serve(ctx context.Context, progress ProgressFn) error {
	listenerDone := make(chan struct{})
	defer close(listenerDone)
	go func() {
		select {
		case <-ctx.Done():
			s.listener.Close()
		case <-listenerDone:
		}
	}()
	defer s.listener.Close()

	s.listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Minute))

	// If we have a relay URL, concurrently watch for receiver via rendezvous
	// and then punch toward them. This creates a NAT mapping on our side so
	// the receiver's inbound TCP connect can get through.
	if s.Info.Relay != "" {
		go s.holePunchWhenReady(ctx)
	}

	conn, err := s.listener.Accept()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("waiting for receiver: %w", err)
	}
	defer conn.Close()

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetWriteBuffer(8 * 1024 * 1024)
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	// ── Handshake ────────────────────────────────────────────────────────────
	codeBuf := make([]byte, 8)
	if _, err := io.ReadFull(conn, codeBuf); err != nil {
		return fmt.Errorf("read auth code: %w", err)
	}
	if string(codeBuf) != s.Info.Code {
		conn.Write([]byte("ERR:bad_code\n"))
		return fmt.Errorf("receiver sent wrong code")
	}

	f, stat, err := openFileInfo(s.filePath)
	if err != nil {
		conn.Write([]byte("ERR:file_error\n"))
		return err
	}
	defer f.Close()

	fileSize := stat.Size()
	chunkSize := int64(s.chunkMB) * 1024 * 1024
	totalChunks := int((fileSize + chunkSize - 1) / chunkSize)
	if totalChunks == 0 {
		totalChunks = 1
	}

	type metaMsg struct {
		FileName    string `json:"f"`
		FileSize    int64  `json:"s"`
		TotalChunks int    `json:"n"`
		ChunkSize   int64  `json:"c"`
	}
	meta, _ := json.Marshal(metaMsg{stat.Name(), fileSize, totalChunks, chunkSize})
	conn.Write(append(meta, '\n'))

	// ── Encryption pipeline ──────────────────────────────────────────────────
	// Producer encrypts next chunk while consumer transmits current one,
	// overlapping CPU-bound encryption with network I/O.
	pipeline := make(chan pipeChunk, 3)
	go func() {
		defer close(pipeline)
		for i := 0; i < totalChunks; i++ {
			select {
			case <-ctx.Done():
				return
			default:
			}
			offset := int64(i) * chunkSize
			end := min(offset+chunkSize, fileSize)
			plain := make([]byte, end-offset)

			if _, err := f.ReadAt(plain, offset); err != nil && err != io.EOF {
				pipeline <- pipeChunk{err: fmt.Errorf("read chunk %d: %w", i, err)}
				return
			}
			enc, err := encryptChunk(s.key, i, plain)
			if err != nil {
				pipeline <- pipeChunk{err: fmt.Errorf("encrypt chunk %d: %w", i, err)}
				return
			}
			pipeline <- pipeChunk{data: enc, plainLen: end - offset}
		}
	}()

	// ── Transmit ─────────────────────────────────────────────────────────────
	startTime := time.Now()
	var bytesDone int64
	sizeBuf := make([]byte, 4)

	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pc, ok := <-pipeline
		if !ok {
			break
		}
		if pc.err != nil {
			return pc.err
		}

		binary.BigEndian.PutUint32(sizeBuf, uint32(len(pc.data)))
		if _, err := conn.Write(sizeBuf); err != nil {
			return fmt.Errorf("send chunk %d size: %w", i, err)
		}
		if _, err := conn.Write(pc.data); err != nil {
			return fmt.Errorf("send chunk %d data: %w", i, err)
		}

		bytesDone += pc.plainLen
		if progress != nil {
			elapsed := time.Since(startTime).Seconds()
			var spd int64
			if elapsed > 0 {
				spd = int64(float64(bytesDone) / elapsed)
			}
			progress(ProgressEvent{
				Done: i + 1, Total: totalChunks,
				BytesDone: bytesDone, TotalBytes: fileSize,
				SpeedBPS: spd,
			})
		}
	}
	return nil
}

// Close cancels the peer server (e.g., user cancelled waiting).
func (s *PeerServer) Close() {
	s.listener.Close()
}

// holePunchWhenReady waits for the receiver to appear in the rendezvous, then
// fires UDP punch packets toward them so the sender's NAT entry is created.
// The receiver's subsequent TCP connect then gets forwarded by our NAT.
func (s *PeerServer) holePunchWhenReady(ctx context.Context) {
	// Long-poll: relay blocks until receiver POST /p2p/meet/{code}.
	resp, err := httpGetWithContext(ctx, s.Info.Relay+"/api/v1/p2p/wait/"+s.Info.Code, 6*time.Minute)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var body struct {
		ReceiverWAN string `json:"receiverWAN"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.ReceiverWAN == "" {
		return
	}

	port := s.listener.Addr().(*net.TCPAddr).Port
	slog.Info("hole punching toward receiver", "receiverWAN", body.ReceiverWAN)
	punchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	UDPPunch(punchCtx, port, body.ReceiverWAN, 12*time.Second) //nolint:errcheck
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func jsonMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func post(url string, body []byte) {
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:gosec
	if err == nil {
		resp.Body.Close()
	}
}

func httpGetWithContext(ctx context.Context, url string, timeout time.Duration) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	return client.Do(req)
}

// ── Receiver side ────────────────────────────────────────────────────────────

// PeerReceive connects to a PeerServer and downloads the file.
// Connection is attempted in priority order:
//  1. Direct TCP to peerAddr (LAN or open-port WAN)
//  2. UDP hole punch via relay rendezvous + TCP through punched NAT
//  3. Relay streaming fallback (if relayURL != "")
//
// Cancelling ctx aborts everything.
func PeerReceive(ctx context.Context, peerAddr, code, keyB64, relayURL, outputDir string, progress ProgressFn) (string, error) {
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return "", fmt.Errorf("invalid key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	sendCode := func(conn net.Conn) error {
		_, err := fmt.Fprint(conn, code)
		return err
	}

	// ── Path 1: direct TCP (LAN or open port) ───────────────────────────────
	conn, directErr := tryDirectConnect(ctx, peerAddr)
	if directErr == nil {
		slog.Info("p2p: direct connection", "addr", peerAddr)
		if err := sendCode(conn); err != nil {
			conn.Close()
		} else {
			return receiveViaConn(ctx, conn, key, outputDir, progress)
		}
	}
	slog.Info("p2p: direct connect failed, trying hole punch", "err", directErr)

	// ── Path 2: UDP hole punch + TCP through punched NAT ────────────────────
	if relayURL != "" && peerAddr != "" {
		conn, punchErr := tryHolePunch(ctx, relayURL, peerAddr, code)
		if punchErr == nil {
			slog.Info("p2p: hole punch succeeded")
			if err := sendCode(conn); err != nil {
				conn.Close()
			} else {
				return receiveViaConn(ctx, conn, key, outputDir, progress)
			}
		}
		slog.Info("p2p: hole punch failed, falling back to relay", "err", punchErr)
	}

	// ── Path 3: relay streaming fallback ────────────────────────────────────
	if relayURL != "" {
		slog.Info("p2p: using relay stream fallback")
		return Receive(ctx, relayURL, code, keyB64, outputDir, 4, progress)
	}

	return "", fmt.Errorf("all connection paths failed: %w", directErr)
}

// tryDirectConnect attempts a plain TCP connection with a 5-second timeout.
func tryDirectConnect(ctx context.Context, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return d.DialContext(ctx, "tcp", addr)
}

// tryHolePunch performs UDP hole punching via the relay rendezvous, then
// attempts a TCP connection to peerAddr through the now-open NAT mapping.
func tryHolePunch(ctx context.Context, relayURL, peerWAN, code string) (net.Conn, error) {
	// Discover our own WAN address via STUN (UDP).
	ownWAN, _ := DiscoverWANAddr(0) // port 0 = ephemeral; just need the external IP

	// Tell the relay we're here. The relay will notify the sender (via /wait),
	// which will trigger the sender's UDP punch toward us.
	meetBody := jsonMarshal(map[string]string{
		"wan": ownWAN,
		"lan": fmt.Sprintf("%s:0", GetLocalIP()),
	})
	meetResp, err := http.Post(relayURL+"/api/v1/p2p/meet/"+code, "application/json", bytes.NewReader(meetBody)) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("rendezvous meet: %w", err)
	}
	meetResp.Body.Close()

	// Punch simultaneously — sender is doing the same after /wait returns.
	punchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if err := UDPPunch(punchCtx, 0, peerWAN, 10*time.Second); err != nil {
		slog.Info("p2p: udp punch no confirmation, trying TCP anyway", "err", err)
		// Not fatal — cone NAT may have opened our side even without confirmation.
	}

	// Now attempt TCP to peerWAN — NAT entry created by punch should allow it.
	tCtx, tCancel := context.WithTimeout(ctx, 8*time.Second)
	defer tCancel()
	d := &net.Dialer{Timeout: 8 * time.Second}
	return d.DialContext(tCtx, "tcp", peerWAN)
}

// receiveViaConn performs the auth handshake and file download over an
// already-established net.Conn (reused for both direct and hole-punch paths).
func receiveViaConn(ctx context.Context, conn net.Conn, key []byte, outputDir string, progress ProgressFn) (string, error) {
	defer conn.Close()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetReadBuffer(8 * 1024 * 1024)
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	reader := bufio.NewReaderSize(conn, 4*1024*1024)
	line, err := reader.ReadString('\n')
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("read metadata: %w", err)
	}
	if strings.HasPrefix(line, "ERR:") {
		return "", fmt.Errorf("sender error: %s", strings.TrimSpace(line[4:]))
	}

	type metaMsg struct {
		FileName    string `json:"f"`
		FileSize    int64  `json:"s"`
		TotalChunks int    `json:"n"`
		ChunkSize   int64  `json:"c"`
	}
	var meta metaMsg
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &meta); err != nil {
		return "", fmt.Errorf("parse metadata: %w", err)
	}

	outPath := filepath.Join(outputDir, filepath.Base(meta.FileName))
	outFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()
	_ = outFile.Truncate(meta.FileSize)

	startTime := time.Now()
	var bytesDone int64
	sizeBuf := make([]byte, 4)

	for i := 0; i < meta.TotalChunks; i++ {
		if _, err := io.ReadFull(reader, sizeBuf); err != nil {
			os.Remove(outPath)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("read chunk %d size: %w", i, err)
		}
		chunkLen := binary.BigEndian.Uint32(sizeBuf)

		encData := make([]byte, chunkLen)
		if _, err := io.ReadFull(reader, encData); err != nil {
			os.Remove(outPath)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("read chunk %d data: %w", i, err)
		}

		plain, err := decryptChunk(key, i, encData)
		if err != nil {
			os.Remove(outPath)
			return "", fmt.Errorf("chunk %d: %w", i, err)
		}

		offset := int64(i) * meta.ChunkSize
		if _, err := outFile.WriteAt(plain, offset); err != nil {
			os.Remove(outPath)
			return "", fmt.Errorf("write chunk %d: %w", i, err)
		}

		bytesDone += int64(len(plain))
		if progress != nil {
			elapsed := time.Since(startTime).Seconds()
			var spd int64
			if elapsed > 0 {
				spd = int64(float64(bytesDone) / elapsed)
			}
			progress(ProgressEvent{
				Done: i + 1, Total: meta.TotalChunks,
				BytesDone: bytesDone, TotalBytes: meta.FileSize,
				SpeedBPS: spd,
			})
		}
	}
	return outPath, nil
}
