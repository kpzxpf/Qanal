package transfer

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
)

// encryptChunk compresses plain (if beneficial), then encrypts with AES-256-GCM.
//
// Wire format: IV (12 bytes) | AES-GCM ciphertext of [flag(1) | data...]
//   flag = flagRaw(0)  → data is original plaintext
//   flag = flagZstd(1) → data is zstd-compressed plaintext
func encryptChunk(key []byte, chunkIndex int, plain []byte) ([]byte, error) {
	// Try compression — transparent speedup for compressible data
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

	// Deterministic IV: chunk index (4 bytes BE) + 8 zero bytes
	// Safe because each (key, IV) pair encrypts exactly one chunk.
	iv := make([]byte, 12)
	binary.BigEndian.PutUint32(iv[:4], uint32(chunkIndex))

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
	// Verify IV matches expected chunk index (tamper detection)
	expected := make([]byte, 4)
	binary.BigEndian.PutUint32(expected, uint32(chunkIndex))
	for i := 0; i < 4; i++ {
		if iv[i] != expected[i] {
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
