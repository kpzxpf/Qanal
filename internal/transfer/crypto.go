package transfer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// encryptChunk compresses plain (if beneficial), then encrypts with AES-256-GCM.
//
// Wire format: IV (12 bytes) | AES-GCM ciphertext of [flag(1) | data...]
//
//	IV layout: rand(8 bytes) || BigEndian(chunkIndex, 4 bytes)
//	flag = flagRaw(0)  → data is original plaintext
//	flag = flagZstd(1) → data is zstd-compressed plaintext
//
// The random prefix guarantees IV uniqueness even if the same key were ever
// reused. The chunk index suffix enables tamper/reorder detection in decryptChunk.
func encryptChunk(key []byte, chunkIndex int, plain []byte) ([]byte, error) {
	// Try compression — transparent speedup for compressible data.
	data, isCompressed := tryCompress(plain)
	flag := flagRaw
	if isCompressed {
		flag = flagZstd
	}

	// Build inner payload: [1 byte flag][data]
	payload := make([]byte, 1+len(data))
	payload[0] = flag
	copy(payload[1:], data)

	// AES-256-GCM
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// IV = rand(8B) || BigEndian(chunkIndex, 4B)
	// Random prefix: unique per encryption even across key reuse.
	// Index suffix: enables chunk-order tamper detection on decryption.
	iv := make([]byte, 12)
	if _, err := rand.Read(iv[:8]); err != nil {
		return nil, fmt.Errorf("generate IV: %w", err)
	}
	binary.BigEndian.PutUint32(iv[8:], uint32(chunkIndex))

	ct := gcm.Seal(nil, iv, payload, nil)
	out := make([]byte, 12+len(ct))
	copy(out[:12], iv)
	copy(out[12:], ct)
	return out, nil
}

// decryptChunk reverses encryptChunk: decrypts then decompresses if needed.
func decryptChunk(key []byte, chunkIndex int, enc []byte) ([]byte, error) {
	if len(enc) < 12+1+16 { // IV + flag + GCM tag
		return nil, fmt.Errorf("encrypted chunk too short: %d bytes", len(enc))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	iv := enc[:12]
	// Verify the chunk-index suffix of the IV (last 4 bytes) to detect
	// chunk reordering or tampering before AES-GCM open.
	var expected [4]byte
	binary.BigEndian.PutUint32(expected[:], uint32(chunkIndex))
	for i := 0; i < 4; i++ {
		if iv[8+i] != expected[i] {
			return nil, fmt.Errorf("chunk %d: IV mismatch (wrong order or tampered)", chunkIndex)
		}
	}

	payload, err := gcm.Open(nil, iv, enc[12:], nil)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM failed (wrong key or corrupted data): %w", err)
	}

	flag := payload[0]
	data := payload[1:]

	if flag == flagZstd {
		return decompressZstd(data)
	}
	return data, nil
}
