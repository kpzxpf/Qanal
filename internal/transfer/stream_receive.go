package transfer

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// StreamReceive connects to the relay's streaming endpoint for code and writes
// the decrypted file directly to disk. No data is buffered in RAM or stored
// on the relay — data flows directly from the sender's connection.
func StreamReceive(ctx context.Context, serverURL, code, keyB64, outputDir string, progress ProgressFn) (string, error) {
	keyBytes, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return "", fmt.Errorf("invalid key: %w", err)
	}
	if len(keyBytes) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(keyBytes))
	}

	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			DisableCompression:    true,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 0,
			ForceAttemptHTTP2:     true,
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		serverURL+"/api/v1/stream/"+code, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect to stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("relay error %d: %s", resp.StatusCode, string(b))
	}

	// Read framed metadata.
	var lenBuf [4]byte
	if _, err := io.ReadFull(resp.Body, lenBuf[:]); err != nil {
		return "", fmt.Errorf("read meta length: %w", err)
	}
	metaLen := binary.BigEndian.Uint32(lenBuf[:])
	if metaLen > 64*1024 {
		return "", fmt.Errorf("metadata too large: %d bytes", metaLen)
	}
	metaData := make([]byte, metaLen)
	if _, err := io.ReadFull(resp.Body, metaData); err != nil {
		return "", fmt.Errorf("read metadata: %w", err)
	}

	type streamMeta struct {
		FileName    string `json:"f"`
		FileSize    int64  `json:"s"`
		TotalChunks int    `json:"n"`
		ChunkSize   int64  `json:"c"`
		FileHash    string `json:"h,omitempty"`
	}
	var meta streamMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
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

	for i := 0; i < meta.TotalChunks; i++ {
		select {
		case <-ctx.Done():
			outFile.Close()
			os.Remove(outPath)
			return "", ctx.Err()
		default:
		}

		if _, err := io.ReadFull(resp.Body, lenBuf[:]); err != nil {
			outFile.Close()
			os.Remove(outPath)
			return "", fmt.Errorf("chunk %d size: %w", i, err)
		}
		chunkLen := binary.BigEndian.Uint32(lenBuf[:])
		if chunkLen > 600*1024*1024 {
			outFile.Close()
			os.Remove(outPath)
			return "", fmt.Errorf("chunk %d: suspiciously large (%d bytes)", i, chunkLen)
		}

		enc := make([]byte, chunkLen)
		if _, err := io.ReadFull(resp.Body, enc); err != nil {
			outFile.Close()
			os.Remove(outPath)
			return "", fmt.Errorf("chunk %d data: %w", i, err)
		}

		plain, err := decryptChunk(keyBytes, i, enc)
		if err != nil {
			outFile.Close()
			os.Remove(outPath)
			return "", fmt.Errorf("chunk %d: %w", i, err)
		}

		offset := int64(i) * meta.ChunkSize
		if _, err := outFile.WriteAt(plain, offset); err != nil {
			outFile.Close()
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
				Done:       i + 1,
				Total:      meta.TotalChunks,
				BytesDone:  bytesDone,
				TotalBytes: meta.FileSize,
				SpeedBPS:   spd,
			})
		}
	}

	// Verify SHA-256 integrity.
	if meta.FileHash != "" {
		if err := verifyFileHash(outPath, meta.FileHash); err != nil {
			outFile.Close()
			os.Remove(outPath)
			return "", err
		}
	}

	return outPath, nil
}
