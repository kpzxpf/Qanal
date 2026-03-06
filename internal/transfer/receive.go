package transfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Receive downloads and decrypts a transfer directly to disk using WriteAt
// (parallel workers write to different file offsets — no in-memory assembly).
// Cancelling ctx immediately aborts all in-flight requests and workers.
func Receive(ctx context.Context, serverURL, code, keyB64, outputDir string, workers int, progress ProgressFn) (string, error) {
	keyBytes, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return "", fmt.Errorf("invalid key: %w", err)
	}
	if len(keyBytes) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(keyBytes))
	}

	info, err := fetchInfo(ctx, serverURL, code)
	if err != nil {
		return "", err
	}

	outPath := filepath.Join(outputDir, filepath.Base(info.FileName))
	outFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	// Pre-allocate file size — avoids filesystem fragmentation on large files.
	_ = outFile.Truncate(info.FileSize)

	// Worker cancellation: first error cancels remaining workers.
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

	client := &http.Client{Timeout: 0}

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

	return outPath, nil
}

// --- helpers ---

type transferInfo struct {
	FileName    string `json:"fileName"`
	FileSize    int64  `json:"fileSize"`
	TotalChunks int    `json:"totalChunks"`
	ChunkSize   int64  `json:"chunkSize"`
}

func fetchInfo(ctx context.Context, serverURL, code string) (*transferInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/v1/transfers/"+code, nil)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch transfer info: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(r.Body)
		return nil, fmt.Errorf("server error %d: %s", r.StatusCode, string(b))
	}
	var info transferInfo
	json.NewDecoder(r.Body).Decode(&info)
	return &info, nil
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
			// Chunk not yet uploaded — wait and retry.
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
