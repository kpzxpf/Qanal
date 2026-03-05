package transfer

import (
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
func Receive(serverURL, code, keyB64, outputDir string, workers int, progress ProgressFn) (string, error) {
	keyBytes, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return "", fmt.Errorf("invalid key: %w", err)
	}
	if len(keyBytes) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(keyBytes))
	}

	info, err := fetchInfo(serverURL, code)
	if err != nil {
		return "", err
	}

	outPath := filepath.Join(outputDir, info.FileName)
	outFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	// Pre-allocate file size — avoids filesystem fragmentation on large files
	_ = outFile.Truncate(info.FileSize)

	var (
		downloaded int64
		bytesDone  int64
		start      = time.Now()
	)
	nextIdx := int64(-1)

	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	client := &http.Client{Timeout: 0}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&nextIdx, 1))
				if i >= info.TotalChunks {
					return
				}

				enc, err := downloadChunkWithRetry(client, serverURL, code, i)
				if err != nil {
					errCh <- err
					return
				}

				plain, err := decryptChunk(keyBytes, i, enc)
				if err != nil {
					errCh <- fmt.Errorf("chunk %d: %w", i, err)
					return
				}

				offset := int64(i) * info.ChunkSize
				if _, err := outFile.WriteAt(plain, offset); err != nil {
					errCh <- fmt.Errorf("write chunk %d: %w", i, err)
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
	close(errCh)
	for err := range errCh {
		if err != nil {
			_ = os.Remove(outPath)
			return "", err
		}
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

func fetchInfo(serverURL, code string) (*transferInfo, error) {
	r, err := http.Get(serverURL + "/api/v1/transfers/" + code)
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

func downloadChunkWithRetry(client *http.Client, serverURL, code string, index int) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/transfers/%s/chunks/%d", serverURL, code, index)
	for attempt := 0; attempt < 60; attempt++ {
		r, err := client.Get(url)
		if err != nil {
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		if r.StatusCode == http.StatusNotFound {
			r.Body.Close()
			time.Sleep(2 * time.Second) // chunk not yet uploaded — wait
			continue
		}
		if r.StatusCode != http.StatusOK {
			r.Body.Close()
			return nil, fmt.Errorf("download chunk %d: HTTP %d", index, r.StatusCode)
		}
		data, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return data, nil
	}
	return nil, fmt.Errorf("chunk %d: failed after 60 retries", index)
}
