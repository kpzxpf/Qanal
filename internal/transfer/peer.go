package transfer

import (
	"bufio"
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
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// tcpBufSize — TCP socket buffer for high-throughput transfers (8 MiB).
const tcpBufSize = 8 * 1024 * 1024

// maxFrameLen — maximum allowed encrypted chunk size (sanity guard).
const maxFrameLen = 256 * 1024 * 1024 // 256 MiB

// PeerInfo holds connection details shared with the receiver via the share link.
type PeerInfo struct {
	LAN   string `json:"lan"`
	WAN   string `json:"wan"` // public IP:port; empty if unreachable
	Code  string `json:"code"`
	Key   string `json:"key"`
	Upnp  bool   `json:"upnp"`  // UPnP port mapping succeeded
	Cgnat bool   `json:"cgnat"` // ISP CGNAT detected; WAN transfer blocked
}

// PeerServer is the sender-side TCP listener.
type PeerServer struct {
	listener net.Listener
	filePath string
	chunkMB  int
	key      []byte
	Info     *PeerInfo
}

// metaMsg is the file metadata sent from sender to receiver after successful auth.
type metaMsg struct {
	FileName    string `json:"f"`
	FileSize    int64  `json:"s"`
	TotalChunks int    `json:"n"`
	ChunkSize   int64  `json:"c"`
}

// writeLenFrame sends [4-byte BE length][data] over conn in one syscall.
func writeLenFrame(conn net.Conn, data []byte) error {
	pkt := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(pkt, uint32(len(data)))
	copy(pkt[4:], data)
	_, err := conn.Write(pkt)
	return err
}

// readLenFrame reads [4-byte BE length][data] from r.
func readLenFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxFrameLen {
		return nil, fmt.Errorf("invalid frame length: %d", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}

// StartPeer opens a TCP listener, maps the port via UPnP, and discovers the
// public IP via STUN. Returns immediately; call Serve to handle a transfer.
//
// WAN address strategy:
//   - UPnP maps TCP port X → external IP:X (router forwards to us).
//     ExternalPort = localPort by design, so wanAddr port = localPort always.
//   - STUN discovers the true public IP (may differ from UPnP router IP under CGNAT).
//   - If UPnP succeeded: wanAddr = stunIP:port (or upnpIP:port if STUN failed).
//   - CGNAT = upnpIP ≠ stunIP (ISP has another NAT layer; UPnP mapping useless).
func StartPeer(filePath string, chunkMB int) (*PeerServer, error) {
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

	// UPnP and STUN run concurrently; wait up to 6 s for both.
	type wanResult struct{ ip, via string }
	ch := make(chan wanResult, 2)

	go func() {
		ip, err := MapUPnPPort(port)
		if err != nil {
			slog.Debug("UPnP unavailable", "err", err)
			ch <- wanResult{"", "upnp"}
			return
		}
		ch <- wanResult{ip, "upnp"}
	}()
	go func() {
		addr, err := DiscoverWANAddr(port)
		if err != nil {
			slog.Debug("STUN unavailable", "err", err)
			ch <- wanResult{"", "stun"}
			return
		}
		// DiscoverWANAddr returns "stunIP:stunPort" (UDP port).
		// Extract only the IP; the correct TCP port is always our listener port.
		host, _, parseErr := net.SplitHostPort(addr)
		if parseErr != nil {
			host = addr
		}
		ch <- wanResult{host, "stun"}
	}()

	var upnpIP, stunIP string
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	for got := 0; got < 2; {
		select {
		case r := <-ch:
			got++
			switch r.via {
			case "upnp":
				upnpIP = r.ip
			case "stun":
				stunIP = r.ip
			}
		case <-timer.C:
			got = 2
		}
	}

	// Choose WAN address.
	//
	// The UPnP mapping guarantees that TCP connections to externalIP:port reach us,
	// where externalIP = upnpIP. STUN tells us the true public IP so we can detect
	// CGNAT (ISP has another NAT; our UPnP mapping on the home router is useless).
	//
	// Best address to put in the share link:
	//   • UPnP + no CGNAT → upnpIP:port (port forwarded, receiver can reach us)
	//   • UPnP + CGNAT     → stunIP:port  (flag CGNAT warning; port NOT reachable)
	//   • Only STUN        → stunIP:port  (no port mapping; may work on public IPs)
	//   • Neither          → empty (WAN unavailable)
	var wanAddr string
	upnpOK := upnpIP != ""
	var cgnat bool

	if upnpOK {
		if stunIP != "" && stunIP != upnpIP {
			// Router's external IP ≠ true public IP → CGNAT detected.
			cgnat = true
			slog.Warn("CGNAT detected: internet P2P will not work",
				"router_ip", upnpIP, "public_ip", stunIP)
			// Still populate WAN for UI; receiver will see the warning.
			wanAddr = fmt.Sprintf("%s:%d", stunIP, port)
		} else {
			// No CGNAT: use UPnP router IP (STUN IP same or unavailable).
			wanAddr = fmt.Sprintf("%s:%d", upnpIP, port)
		}
	} else if stunIP != "" {
		// UPnP unavailable but we have a public IP (direct internet connection?).
		wanAddr = fmt.Sprintf("%s:%d", stunIP, port)
		slog.Info("UPnP unavailable, using STUN IP for WAN; port NOT forwarded", "addr", wanAddr)
	}

	info := &PeerInfo{
		LAN:   fmt.Sprintf("%s:%d", GetLocalIP(), port),
		WAN:   wanAddr,
		Code:  code,
		Key:   base64.RawURLEncoding.EncodeToString(key),
		Upnp:  upnpOK,
		Cgnat: cgnat,
	}

	return &PeerServer{
		listener: ln,
		filePath: filePath,
		chunkMB:  chunkMB,
		key:      key,
		Info:     info,
	}, nil
}

// Close stops the peer server.
func (s *PeerServer) Close() {
	s.listener.Close()
}

// Serve handles exactly one receiver.
//
// Protocol:
//
//	Receiver → Sender : [8 bytes auth code]
//	Sender   → Receiver: [4-byte len]["ERR:xxx"] on failure  OR
//	                     [4-byte len][JSON metadata] on success
//	Data loop (sender → receiver):
//	  [4-byte encLen][AES-256-GCM encrypted chunk] × totalChunks
//
// Chunks are pipelined: goroutine encrypts chunk N+1 while the main
// goroutine sends chunk N, keeping both CPU and network busy.
func (s *PeerServer) Serve(ctx context.Context, progress ProgressFn) error {
	// Close listener when context is cancelled so Accept() unblocks.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			s.listener.Close()
		case <-stop:
		}
	}()

	_ = s.listener.(*net.TCPListener).SetDeadline(time.Now().Add(15 * time.Minute))

	// Accept connections until one passes authentication.
	// Internet NAT devices occasionally reset connections immediately after
	// the TCP handshake; keep retrying until we get a valid auth code.
	var conn net.Conn
	for {
		c, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("accept: %w", err)
		}
		tuneConn(c)

		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
		var codeBuf [8]byte
		if _, err := io.ReadFull(c, codeBuf[:]); err != nil {
			slog.Info("p2p: pre-auth read failed, retrying", "err", err)
			c.Close()
			continue
		}
		_ = c.SetDeadline(time.Time{})

		if string(codeBuf[:]) != s.Info.Code {
			_ = writeLenFrame(c, []byte("ERR:bad_code"))
			c.Close()
			return fmt.Errorf("bad auth code received")
		}
		conn = c
		break
	}
	defer conn.Close()

	// Open source file.
	f, stat, err := openFileInfo(s.filePath)
	if err != nil {
		_ = writeLenFrame(conn, []byte("ERR:file_error"))
		return err
	}
	defer f.Close()

	fileSize := stat.Size()
	chunkSize := int64(s.chunkMB) * 1024 * 1024
	totalChunks := int((fileSize + chunkSize - 1) / chunkSize)
	if totalChunks == 0 {
		totalChunks = 1
	}

	// Send metadata frame.
	meta, _ := json.Marshal(metaMsg{stat.Name(), fileSize, totalChunks, chunkSize})
	if err := writeLenFrame(conn, meta); err != nil {
		return fmt.Errorf("send metadata: %w", err)
	}

	slog.Info("p2p: sending", "file", stat.Name(), "size", fileSize, "chunks", totalChunks)

	// Pipeline producer: reads file → encrypts → pushes to channel.
	// Allows encryption of chunk N+1 to overlap with network I/O of chunk N.
	type pipeItem struct {
		enc   []byte
		plain int64
		err   error
	}
	pipeline := make(chan pipeItem, 2)
	go func() {
		defer close(pipeline)
		for ci := 0; ci < totalChunks; ci++ {
			if ctx.Err() != nil {
				pipeline <- pipeItem{err: ctx.Err()}
				return
			}
			offset := int64(ci) * chunkSize
			end := min(offset+chunkSize, fileSize)
			buf := make([]byte, end-offset)
			if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
				pipeline <- pipeItem{err: fmt.Errorf("read chunk %d: %w", ci, err)}
				return
			}
			enc, err := encryptChunk(s.key, ci, buf)
			if err != nil {
				pipeline <- pipeItem{err: fmt.Errorf("encrypt chunk %d: %w", ci, err)}
				return
			}
			pipeline <- pipeItem{enc: enc, plain: end - offset}
		}
	}()

	var bytesDone, doneChunks atomic.Int64
	startTime := time.Now()
	w := bufio.NewWriterSize(conn, 256*1024)
	var sizeBuf [4]byte

	for ci := 0; ; ci++ {
		it, ok := <-pipeline
		if !ok {
			break
		}
		if it.err != nil {
			return it.err
		}
		binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(it.enc)))
		if _, err := w.Write(sizeBuf[:]); err != nil {
			return fmt.Errorf("send chunk %d size: %w", ci, err)
		}
		if _, err := w.Write(it.enc); err != nil {
			return fmt.Errorf("send chunk %d data: %w", ci, err)
		}
		bytesDone.Add(it.plain)
		doneChunks.Add(1)
		if progress != nil {
			bd := bytesDone.Load()
			elapsed := time.Since(startTime).Seconds()
			var spd int64
			if elapsed > 0 {
				spd = int64(float64(bd) / elapsed)
			}
			progress(ProgressEvent{
				Done:       int(doneChunks.Load()),
				Total:      totalChunks,
				BytesDone:  bd,
				TotalBytes: fileSize,
				SpeedBPS:   spd,
			})
		}
	}
	return w.Flush()
}

// PeerReceive connects to a PeerServer, authenticates, and downloads the file.
//
// Retries the connection up to 3 times (internet links can be flaky).
// Decrypts each chunk immediately after receipt and writes to the output file.
func PeerReceive(ctx context.Context, peerAddr, code, keyB64, outputDir string, progress ProgressFn) (string, error) {
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return "", fmt.Errorf("invalid key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	const maxAttempts = 3
	var conn net.Conn
	var meta metaMsg

	d := &net.Dialer{Timeout: 15 * time.Second}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * 2 * time.Second
			slog.Info("p2p: retrying connection", "attempt", attempt+1, "in", delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		c, err := d.DialContext(ctx, "tcp", peerAddr)
		if err != nil {
			slog.Info("p2p: dial failed", "attempt", attempt+1, "err", err)
			if attempt < maxAttempts-1 {
				continue
			}
			return "", fmt.Errorf("connect to %s: %w", peerAddr, err)
		}
		tuneConn(c)

		// Send auth code (must arrive within 30 s to avoid sender timeout).
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := c.Write([]byte(code)); err != nil {
			c.Close()
			slog.Info("p2p: send auth failed", "attempt", attempt+1, "err", err)
			if attempt < maxAttempts-1 {
				continue
			}
			return "", fmt.Errorf("send auth: %w", err)
		}

		// Read metadata frame (sender has 30 s to respond).
		metaBytes, err := readLenFrame(c)
		_ = c.SetDeadline(time.Time{})
		if err != nil {
			c.Close()
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			slog.Info("p2p: read metadata failed", "attempt", attempt+1, "err", err)
			if attempt < maxAttempts-1 {
				continue
			}
			return "", fmt.Errorf("read metadata: %w", err)
		}

		if strings.HasPrefix(string(metaBytes), "ERR:") {
			c.Close()
			return "", fmt.Errorf("sender error: %s", strings.TrimPrefix(string(metaBytes), "ERR:"))
		}

		if err := json.Unmarshal(metaBytes, &meta); err != nil {
			c.Close()
			slog.Info("p2p: bad metadata", "attempt", attempt+1, "err", err)
			if attempt < maxAttempts-1 {
				continue
			}
			return "", fmt.Errorf("parse metadata: %w", err)
		}

		conn = c
		break
	}

	if conn == nil {
		return "", fmt.Errorf("failed to connect after %d attempts", maxAttempts)
	}
	defer conn.Close()

	// Cancel connection when context is done.
	connClosed := make(chan struct{})
	defer close(connClosed)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-connClosed:
		}
	}()

	slog.Info("p2p: receiving", "file", meta.FileName, "size", meta.FileSize, "chunks", meta.TotalChunks)

	// Create output file and pre-allocate size.
	outPath := filepath.Join(outputDir, filepath.Base(meta.FileName))
	outFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create output: %w", err)
	}
	defer outFile.Close()
	_ = outFile.Truncate(meta.FileSize)

	var bytesDone, doneChunks atomic.Int64
	startTime := time.Now()
	r := bufio.NewReaderSize(conn, 256*1024)
	var sizeBuf [4]byte

	for ci := 0; ci < meta.TotalChunks; ci++ {
		if ctx.Err() != nil {
			outFile.Close()
			_ = os.Remove(outPath)
			return "", ctx.Err()
		}

		if _, err := io.ReadFull(r, sizeBuf[:]); err != nil {
			outFile.Close()
			_ = os.Remove(outPath)
			return "", fmt.Errorf("read chunk %d size: %w", ci, err)
		}
		encLen := int(binary.BigEndian.Uint32(sizeBuf[:]))
		if encLen == 0 || encLen > maxFrameLen {
			outFile.Close()
			_ = os.Remove(outPath)
			return "", fmt.Errorf("chunk %d: invalid size %d", ci, encLen)
		}

		encData := make([]byte, encLen)
		if _, err := io.ReadFull(r, encData); err != nil {
			outFile.Close()
			_ = os.Remove(outPath)
			return "", fmt.Errorf("read chunk %d data: %w", ci, err)
		}

		plain, err := decryptChunk(key, ci, encData)
		if err != nil {
			outFile.Close()
			_ = os.Remove(outPath)
			return "", fmt.Errorf("decrypt chunk %d: %w", ci, err)
		}

		offset := int64(ci) * meta.ChunkSize
		if _, err := outFile.WriteAt(plain, offset); err != nil {
			outFile.Close()
			_ = os.Remove(outPath)
			return "", fmt.Errorf("write chunk %d: %w", ci, err)
		}

		bytesDone.Add(int64(len(plain)))
		doneChunks.Add(1)
		if progress != nil {
			bd := bytesDone.Load()
			elapsed := time.Since(startTime).Seconds()
			var spd int64
			if elapsed > 0 {
				spd = int64(float64(bd) / elapsed)
			}
			progress(ProgressEvent{
				Done:       int(doneChunks.Load()),
				Total:      meta.TotalChunks,
				BytesDone:  bd,
				TotalBytes: meta.FileSize,
				SpeedBPS:   spd,
			})
		}
	}

	return outPath, nil
}

// tuneConn applies performance TCP socket options.
func tuneConn(conn net.Conn) {
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tc.SetNoDelay(true)
	_ = tc.SetKeepAlive(true)
	_ = tc.SetKeepAlivePeriod(30 * time.Second)
	_ = tc.SetReadBuffer(tcpBufSize)
	_ = tc.SetWriteBuffer(tcpBufSize)
}
