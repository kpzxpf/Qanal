package transfer

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// StreamSend sends filePath to the relay's streaming endpoint without disk storage.
// The relay pipes data directly to a waiting StreamReceive call in real-time.
// Both sender and receiver must be online simultaneously.
//
// onReady is called with (code, key) as soon as credentials are generated —
// before the receiver connects — so the caller can display them to the user immediately.
// StreamSend then blocks until the transfer completes or ctx is cancelled.
func StreamSend(ctx context.Context, serverURL, filePath string, chunkMB int,
	onReady func(code, key string), progress ProgressFn) error {

	f, stat, err := openFileInfo(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	fileSize := stat.Size()

	// Compute SHA-256 before streaming (ReadAt workers unaffected).
	fileHash, err := computeFileHash(f)
	if err != nil {
		return fmt.Errorf("hash file: %w", err)
	}

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	keyB64 := base64.RawURLEncoding.EncodeToString(keyBytes)

	codeSrc := make([]byte, 5)
	if _, err := rand.Read(codeSrc); err != nil {
		return fmt.Errorf("generate code: %w", err)
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(codeSrc)[:8]

	if chunkMB <= 0 {
		chunkMB = 16
	}
	chunkSize := int64(chunkMB) * 1024 * 1024
	totalChunks := int((fileSize + chunkSize - 1) / chunkSize)
	if totalChunks == 0 {
		totalChunks = 1
	}

	// Announce credentials to the caller before the receiver connects.
	onReady(code, keyB64)

	// pr/pw connect the encryption pipeline to the HTTP request body.
	pr, pw := io.Pipe()

	// Producer: encrypts chunks sequentially and writes the framed stream to pw.
	// Uses a 1-ahead pipeline so AES-GCM encryption overlaps with network I/O.
	producerErr := make(chan error, 1)
	go func() {
		defer pw.Close()

		// Wire format: [4-byte meta_len][meta JSON][N × [4-byte chunk_len][chunk bytes]]
		type streamMeta struct {
			FileName    string `json:"f"`
			FileSize    int64  `json:"s"`
			TotalChunks int    `json:"n"`
			ChunkSize   int64  `json:"c"`
			FileHash    string `json:"h,omitempty"`
		}
		meta, _ := json.Marshal(streamMeta{
			FileName:    stat.Name(),
			FileSize:    fileSize,
			TotalChunks: totalChunks,
			ChunkSize:   chunkSize,
			FileHash:    fileHash,
		})
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(meta)))
		if _, err := pw.Write(lenBuf[:]); err != nil {
			producerErr <- err
			return
		}
		if _, err := pw.Write(meta); err != nil {
			producerErr <- err
			return
		}

		// Pipeline: producer goroutine encrypts N+1 while consumer transmits N.
		type pipeItem struct {
			data     []byte
			plainLen int64
			err      error
		}
		pipeline := make(chan pipeItem, 2)
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
					pipeline <- pipeItem{err: fmt.Errorf("read chunk %d: %w", i, err)}
					return
				}
				enc, err := encryptChunk(keyBytes, i, plain)
				if err != nil {
					pipeline <- pipeItem{err: fmt.Errorf("encrypt chunk %d: %w", i, err)}
					return
				}
				pipeline <- pipeItem{data: enc, plainLen: end - offset}
			}
		}()

		startTime := time.Now()
		var bytesDone int64
		for i := 0; ; i++ {
			item, ok := <-pipeline
			if !ok {
				break
			}
			if item.err != nil {
				pw.CloseWithError(item.err)
				producerErr <- item.err
				return
			}
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(item.data)))
			if _, err := pw.Write(lenBuf[:]); err != nil {
				producerErr <- err
				return
			}
			if _, err := pw.Write(item.data); err != nil {
				producerErr <- err
				return
			}
			bytesDone += item.plainLen
			if progress != nil {
				elapsed := time.Since(startTime).Seconds()
				var spd int64
				if elapsed > 0 {
					spd = int64(float64(bytesDone) / elapsed)
				}
				progress(ProgressEvent{
					Done:       i + 1,
					Total:      totalChunks,
					BytesDone:  bytesDone,
					TotalBytes: fileSize,
					SpeedBPS:   spd,
				})
			}
		}
		producerErr <- nil
	}()

	// Send: POST the pipe as the request body.
	// The HTTP client reads from pr and sends to the relay, which pipes to the receiver.
	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			DisableCompression:    true,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 0, // relay won't respond until transfer completes
			ForceAttemptHTTP2:     true,
		},
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		serverURL+"/api/v1/stream/"+code, pr)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/octet-stream")

	resp, err := client.Do(httpReq)
	if err != nil {
		pr.CloseWithError(err)
		<-producerErr
		return fmt.Errorf("stream send: %w", err)
	}
	resp.Body.Close()

	return <-producerErr
}
