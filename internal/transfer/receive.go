package transfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Receive downloads and decrypts a transfer directly to disk using WriteAt
// (parallel workers write to different file offsets — no in-memory assembly).
// After all chunks are written, SHA-256 is verified against the server-stored hash.
// Cancelling ctx immediately aborts all in-flight requests and workers.
func Receive(ctx context.Context, serverURL, code, keyB64, outputDir string, workers int, progress ProgressFn) (string, error) {
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
			MaxIdleConnsPerHost:   workers * 2,
			MaxConnsPerHost:       workers * 2,
			DisableCompression:    true,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}

	info, err := fetchInfo(ctx, client, serverURL, code)
	if err != nil {
		return "", err
	}

	outPath := filepath.Join(outputDir, filepath.Base(info.FileName))
	outFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	_ = outFile.Truncate(info.FileSize)

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		firstErr   error
		errOnce    sync.Once
		downloaded int64
		bytesDone  int64
		start      = time.Now()
		nextIdx    = int64(-1)
		wg         sync.WaitGroup
	)

	setErr := func(err error) {
		errOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-workerCtx.Done():
					return
				default:
				}

				i := int(atomic.AddInt64(&nextIdx, 1))
				if i >= info.TotalChunks {
					return
				}

				enc, err := downloadChunkWithRetry(workerCtx, client, serverURL, code, i)
				if err != nil {
					setErr(err)
					return
				}

				plain, err := decryptChunk(keyBytes, i, enc)
				if err != nil {
					setErr(fmt.Errorf("chunk %d: %w", i, err))
					return
				}

				offset := int64(i) * info.ChunkSize
				if _, err := outFile.WriteAt(plain, offset); err != nil {
					setErr(fmt.Errorf("write chunk %d: %w", i, err))
					return
				}

				n := atomic.AddInt64(&downloaded, 1)
				bd := atomic.AddInt64(&bytesDone, int64(len(plain)))
				if progress != nil {
					elapsed := time.Since(start).Seconds()
					var spd int64
					if elapsed > 0 {
						spd = int64(float64(bd) / elapsed)
					}
					progress(ProgressEvent{
						Done:       int(n),
						Total:      info.TotalChunks,
						BytesDone:  bd,
						TotalBytes: info.FileSize,
						SpeedBPS:   spd,
					})
				}
			}
		}()
	}

	wg.Wait()

	if firstErr != nil {
		_ = os.Remove(outPath)
		return "", firstErr
	}
	if ctx.Err() != nil {
		_ = os.Remove(outPath)
		return "", ctx.Err()
	}

	// Verify SHA-256 integrity of the assembled file.
	if info.FileHash != "" {
		if err := verifyFileHash(outPath, info.FileHash); err != nil {
			_ = os.Remove(outPath)
			return "", err
		}
	}

	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer deleteCancel()
	if err := deleteRelayTransfer(deleteCtx, client, serverURL, code); err != nil {
		slog.Warn("relay cleanup after download failed", "code", code, "err", err)
	}

	return outPath, nil
}

// --- helpers ---

type transferInfo struct {
	FileName    string `json:"fileName"`
	FileSize    int64  `json:"fileSize"`
	TotalChunks int    `json:"totalChunks"`
	ChunkSize   int64  `json:"chunkSize"`
	FileHash    string `json:"fileHash,omitempty"`
}

func fetchInfo(ctx context.Context, client *http.Client, serverURL, code string) (*transferInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/v1/transfers/"+code, nil)
	if err != nil {
		return nil, fmt.Errorf("build fetch request: %w", err)
	}
	r, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch transfer info: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(r.Body)
		return nil, fmt.Errorf("server error %d: %s", r.StatusCode, string(b))
	}
	var info transferInfo
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode transfer info: %w", err)
	}
	return &info, nil
}

func deleteRelayTransfer(ctx context.Context, client *http.Client, serverURL, code string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, serverURL+"/api/v1/transfers/"+code, nil)
	r, err := client.Do(req)
	if err != nil {
		return err
	}
	r.Body.Close()
	return nil
}

func downloadChunkWithRetry(ctx context.Context, client *http.Client, serverURL, code string, index int) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/transfers/%s/chunks/%d", serverURL, code, index)
	for attempt := 0; attempt < 60; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		r, err := client.Do(req)
		if err != nil {
			select {
			case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		if r.StatusCode == http.StatusNotFound {
			r.Body.Close()
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		if r.StatusCode != http.StatusOK {
			r.Body.Close()
			return nil, fmt.Errorf("download chunk %d: HTTP %d", index, r.StatusCode)
		}
		data, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		return data, nil
	}
	return nil, fmt.Errorf("chunk %d: failed after 60 retries", index)
}
