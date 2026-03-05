package transfer

import (
	"bytes"
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
// progress is called after each chunk completes; pass nil to skip.
func Send(serverURL, filePath string, chunkMB, workers int, progress ProgressFn) (*SendResult, error) {
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

	// Generate AES-256 key
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	keyB64 := base64.RawURLEncoding.EncodeToString(keyBytes)

	// Create transfer on server
	code, err := createTransfer(serverURL, fileName, fileSize, totalChunks, chunkSize)
	if err != nil {
		return nil, err
	}

	// Upload chunks in parallel
	var (
		uploaded  int64
		bytesDone int64
		startTime = time.Now()
	)
	nextIdx := int64(-1)

	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{Timeout: 0}
			for {
				i := int(atomic.AddInt64(&nextIdx, 1))
				if i >= totalChunks {
					return
				}

				chunkOffset := int64(i) * chunkSize
				chunkEnd := min64(chunkOffset+chunkSize, fileSize)
				plain := make([]byte, chunkEnd-chunkOffset)

				if _, err := f.ReadAt(plain, chunkOffset); err != nil && err != io.EOF {
					errCh <- fmt.Errorf("read chunk %d: %w", i, err)
					return
				}

				enc, err := encryptChunk(keyBytes, i, plain)
				if err != nil {
					errCh <- fmt.Errorf("encrypt chunk %d: %w", i, err)
					return
				}

				if err := uploadChunk(client, serverURL, code, i, enc); err != nil {
					errCh <- err
					return
				}

				n := atomic.AddInt64(&uploaded, 1)
				bd := atomic.AddInt64(&bytesDone, chunkEnd-chunkOffset)
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
	close(errCh)
	for err := range errCh {
		if err != nil {
			return nil, err
		}
	}

	if err := completeTransfer(serverURL, code); err != nil {
		return nil, err
	}
	return &SendResult{Code: code, Key: keyB64}, nil
}

func createTransfer(serverURL, fileName string, fileSize int64, totalChunks int, chunkSize int64) (string, error) {
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
	r, err := http.Post(serverURL+"/api/v1/transfers", "application/json", bytes.NewReader(body))
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

func uploadChunk(client *http.Client, serverURL, code string, index int, data []byte) error {
	url := fmt.Sprintf("%s/api/v1/transfers/%s/chunks/%d", serverURL, code, index)
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
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
		time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	return nil
}

func completeTransfer(serverURL, code string) error {
	r, err := http.Post(serverURL+"/api/v1/transfers/"+code+"/complete", "application/json", nil)
	if err != nil {
		return fmt.Errorf("complete transfer: %w", err)
	}
	r.Body.Close()
	return nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
