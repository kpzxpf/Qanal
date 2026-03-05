package transfer

import (
	"bufio"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PeerInfo is shown to the sender so they can share it with the recipient.
type PeerInfo struct {
	Address string `json:"address"` // IP:PORT the receiver dials
	Code    string `json:"code"`    // 8-char session auth token
	Key     string `json:"key"`     // base64 AES-256 key
}

// PeerServer is the sender-side TCP listener for direct P2P transfers.
type PeerServer struct {
	listener net.Listener
	filePath string
	chunkMB  int
	key      []byte
	Info     *PeerInfo
}

// StartPeer creates a TCP listener on a random port and returns credentials.
// Call Serve() in a goroutine to wait for and handle the receiver connection.
func StartPeer(filePath string, chunkMB int) (*PeerServer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	codeSrc := make([]byte, 5)
	rand.Read(codeSrc)
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(codeSrc)[:8]

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	port := ln.Addr().(*net.TCPAddr).Port
	info := &PeerInfo{
		Address: fmt.Sprintf("%s:%d", GetLocalIP(), port),
		Code:    code,
		Key:     base64.RawURLEncoding.EncodeToString(key),
	}

	return &PeerServer{
		listener: ln,
		filePath: filePath,
		chunkMB:  chunkMB,
		key:      key,
		Info:     info,
	}, nil
}

// Serve waits for exactly one receiver connection, then streams the file directly.
// Times out after 10 minutes if no receiver connects.
func (s *PeerServer) Serve(progress ProgressFn) error {
	defer s.listener.Close()

	// Wait up to 10 minutes for a receiver
	s.listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Minute))
	conn, err := s.listener.Accept()
	if err != nil {
		return fmt.Errorf("waiting for receiver: %w", err)
	}
	defer conn.Close()

	// Tune TCP for large transfers: larger buffers, no Nagle (we write big chunks)
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetWriteBuffer(2 * 1024 * 1024)
		tc.SetNoDelay(false) // batch large chunks into full segments
	}

	// ── Handshake ────────────────────────────────────────────────────────────
	// Receiver sends 8-byte session code for authentication
	codeBuf := make([]byte, 8)
	if _, err := io.ReadFull(conn, codeBuf); err != nil {
		return fmt.Errorf("read auth code: %w", err)
	}
	if string(codeBuf) != s.Info.Code {
		conn.Write([]byte("ERR:bad_code\n"))
		return fmt.Errorf("receiver sent wrong code")
	}

	// Open source file
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

	// Send file metadata as a JSON line
	type metaMsg struct {
		FileName    string `json:"f"`
		FileSize    int64  `json:"s"`
		TotalChunks int    `json:"n"`
		ChunkSize   int64  `json:"c"`
	}
	meta, _ := json.Marshal(metaMsg{stat.Name(), fileSize, totalChunks, chunkSize})
	conn.Write(append(meta, '\n'))

	// ── Stream chunks ────────────────────────────────────────────────────────
	startTime := time.Now()
	var bytesDone int64
	sizeBuf := make([]byte, 4)

	for i := 0; i < totalChunks; i++ {
		offset := int64(i) * chunkSize
		end := min64(offset+chunkSize, fileSize)
		plain := make([]byte, end-offset)

		if _, err := f.ReadAt(plain, offset); err != nil && err != io.EOF {
			return fmt.Errorf("read chunk %d: %w", i, err)
		}

		enc, err := encryptChunk(s.key, i, plain)
		if err != nil {
			return fmt.Errorf("encrypt chunk %d: %w", i, err)
		}

		// Send [4-byte size][encrypted data]
		binary.BigEndian.PutUint32(sizeBuf, uint32(len(enc)))
		if _, err := conn.Write(sizeBuf); err != nil {
			return fmt.Errorf("send chunk %d size: %w", i, err)
		}
		if _, err := conn.Write(enc); err != nil {
			return fmt.Errorf("send chunk %d data: %w", i, err)
		}

		bytesDone += end - offset
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

// ── Receiver side ────────────────────────────────────────────────────────────

// PeerReceive connects to a PeerServer and downloads the file directly.
// Data flows: Sender disk → encrypt → TCP → decrypt → Receiver disk (no relay hop).
func PeerReceive(peerAddr, code, keyB64, outputDir string, progress ProgressFn) (string, error) {
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return "", fmt.Errorf("invalid key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	conn, err := net.DialTimeout("tcp", peerAddr, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("connect to %s: %w", peerAddr, err)
	}
	defer conn.Close()

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetReadBuffer(2 * 1024 * 1024)
		tc.SetNoDelay(false)
	}

	// Send 8-byte auth code
	if _, err := fmt.Fprint(conn, code); err != nil {
		return "", fmt.Errorf("send code: %w", err)
	}

	// Read file metadata (JSON line)
	reader := bufio.NewReaderSize(conn, 4*1024*1024)
	line, err := reader.ReadString('\n')
	if err != nil {
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

	outPath := filepath.Join(outputDir, meta.FileName)
	outFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()
	_ = outFile.Truncate(meta.FileSize) // pre-allocate to avoid fragmentation

	// Receive chunks sequentially, write at correct offset (preserves order)
	startTime := time.Now()
	var bytesDone int64
	sizeBuf := make([]byte, 4)

	for i := 0; i < meta.TotalChunks; i++ {
		// Read 4-byte chunk size
		if _, err := io.ReadFull(reader, sizeBuf); err != nil {
			os.Remove(outPath)
			return "", fmt.Errorf("read chunk %d size: %w", i, err)
		}
		chunkLen := binary.BigEndian.Uint32(sizeBuf)

		// Read encrypted chunk
		encData := make([]byte, chunkLen)
		if _, err := io.ReadFull(reader, encData); err != nil {
			os.Remove(outPath)
			return "", fmt.Errorf("read chunk %d data: %w", i, err)
		}

		// Decrypt (+ decompress if needed)
		plain, err := decryptChunk(key, i, encData)
		if err != nil {
			os.Remove(outPath)
			return "", fmt.Errorf("chunk %d: %w", i, err)
		}

		// Write at correct file offset
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
