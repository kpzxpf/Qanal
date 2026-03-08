package transfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Send uploads filePath to serverURL in parallel encrypted chunks.
// Cancelling ctx immediately aborts all in-flight HTTP requests and workers.
func Send(ctx context.Context, serverURL, filePath string, chunkMB, workers int, progress ProgressFn) (*SendResult, error) {
	f, stat, err := openFileInfo(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fileSize := stat.Size()
	fileName := stat.Name()
	chunkSize := int64(chunkMB) * 1024 * 1024
	totalChunks := int((fileSize + chunkSize - 1) / chunkSize)
	if totalChunks == 0 {
		totalChunks = 1
	}

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	keyB64 := base64.RawURLEncoding.EncodeToString(keyBytes)

	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			MaxIdleConnsPerHost:   workers * 2,
			MaxConnsPerHost:       workers * 2,
			DisableCompression:    true, // data is AES-encrypted (random bytes), gzip adds overhead
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}

	code, err := createTransfer(ctx, client, serverURL, fileName, fileSize, totalChunks, chunkSize)
	if err != nil {
		return nil, err
	}

	// Pool of plaintext buffers — avoids repeated large allocations.
	plainPool := &sync.Pool{
		New: func() any {
			b := make([]byte, chunkSize)
			return &b
		},
	}

	// Worker cancellation: first error cancels remaining workers.
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		firstErr  error
		errOnce   sync.Once
		uploaded  int64
		bytesDone int64
		startTime = time.Now()
		nextIdx   = int64(-1)
		wg        sync.WaitGroup
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
				if i >= totalChunks {
					return
				}

				chunkOffset := int64(i) * chunkSize
				chunkEnd := min(chunkOffset+chunkSize, fileSize)
				chunkLen := chunkEnd - chunkOffset

				bufPtr := plainPool.Get().(*[]byte)
				plain := (*bufPtr)[:chunkLen]

				if _, err := f.ReadAt(plain, chunkOffset); err != nil && err != io.EOF {
					plainPool.Put(bufPtr)
					setErr(fmt.Errorf("read chunk %d: %w", i, err))
					return
				}

				enc, err := encryptChunk(keyBytes, i, plain)
				plainPool.Put(bufPtr)

				if err != nil {
					setErr(fmt.Errorf("encrypt chunk %d: %w", i, err))
					return
				}

				if err := uploadChunk(workerCtx, client, serverURL, code, i, enc); err != nil {
					setErr(err)
					return
				}

				n := atomic.AddInt64(&uploaded, 1)
				bd := atomic.AddInt64(&bytesDone, chunkLen)
				if progress != nil {
					elapsed := time.Since(startTime).Seconds()
					var spd int64
					if elapsed > 0 {
						spd = int64(float64(bd) / elapsed)
					}
					progress(ProgressEvent{
						Done:       int(n),
						Total:      totalChunks,
						BytesDone:  bd,
						TotalBytes: fileSize,
						SpeedBPS:   spd,
					})
				}
			}
		}()
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if err := completeTransfer(ctx, client, serverURL, code); err != nil {
		return nil, err
	}
	return &SendResult{Code: code, Key: keyB64}, nil
}

func createTransfer(ctx context.Context, client *http.Client, serverURL, fileName string, fileSize int64, totalChunks int, chunkSize int64) (string, error) {
	type req struct {
		FileName    string `json:"fileName"`
		FileSize    int64  `json:"fileSize"`
		TotalChunks int    `json:"totalChunks"`
		ChunkSize   int64  `json:"chunkSize"`
	}
	type resp struct {
		Code string `json:"code"`
	}
	body, _ := json.Marshal(req{fileName, fileSize, totalChunks, chunkSize})
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/v1/transfers", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	r, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("create transfer: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(r.Body)
		return "", fmt.Errorf("server error %d: %s", r.StatusCode, string(b))
	}
	var out resp
	json.NewDecoder(r.Body).Decode(&out)
	return out.Code, nil
}

func uploadChunk(ctx context.Context, client *http.Client, serverURL, code string, index int, data []byte) error {
	url := fmt.Sprintf("%s/api/v1/transfers/%s/chunks/%d", serverURL, code, index)
	for attempt := 0; attempt < 5; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/octet-stream")
		r, err := client.Do(req)
		if err == nil {
			r.Body.Close()
			if r.StatusCode == http.StatusOK {
				return nil
			}
		}

		if attempt == 4 {
			return fmt.Errorf("upload chunk %d failed after 5 attempts", index)
		}

		select {
		case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func completeTransfer(ctx context.Context, client *http.Client, serverURL, code string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/v1/transfers/"+code+"/complete", nil)
	req.Header.Set("Content-Type", "application/json")
	r, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("complete transfer: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(r.Body)
		return fmt.Errorf("complete transfer: server error %d: %s", r.StatusCode, string(b))
	}
	return nil
}
