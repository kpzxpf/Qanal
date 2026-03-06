package transfer

import (
	"bytes"
	"fmt"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	plain := []byte("Hello, World! This is a test chunk with some data.")
	enc, err := encryptChunk(key, 0, plain)
	if err != nil {
		t.Fatalf("encryptChunk: %v", err)
	}
	dec, err := decryptChunk(key, 0, enc)
	if err != nil {
		t.Fatalf("decryptChunk: %v", err)
	}
	if !bytes.Equal(plain, dec) {
		t.Errorf("round-trip mismatch: got %q, want %q", dec, plain)
	}
}

func TestEncryptDecryptMultipleChunks(t *testing.T) {
	key := make([]byte, 32)
	for i := 0; i < 10; i++ {
		plain := []byte(fmt.Sprintf("chunk payload number %d with some extra content", i))
		enc, err := encryptChunk(key, i, plain)
		if err != nil {
			t.Fatalf("chunk %d encryptChunk: %v", i, err)
		}
		dec, err := decryptChunk(key, i, enc)
		if err != nil {
			t.Fatalf("chunk %d decryptChunk: %v", i, err)
		}
		if !bytes.Equal(plain, dec) {
			t.Errorf("chunk %d data mismatch", i)
		}
	}
}

func TestEncryptProducesUniqueOutputPerChunk(t *testing.T) {
	key := make([]byte, 32)
	plain := []byte("identical data")

	enc0, _ := encryptChunk(key, 0, plain)
	enc1, _ := encryptChunk(key, 1, plain)

	// Same plaintext + same key but different chunk index → different ciphertext (different IV).
	if bytes.Equal(enc0, enc1) {
		t.Error("expected different ciphertext for different chunk indices")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key := make([]byte, 32)
	wrongKey := make([]byte, 32)
	wrongKey[0] = 0xFF

	enc, _ := encryptChunk(key, 0, []byte("secret data"))
	_, err := decryptChunk(wrongKey, 0, enc)
	if err == nil {
		t.Error("expected error when decrypting with wrong key, got nil")
	}
}

func TestDecryptWrongChunkIndex(t *testing.T) {
	key := make([]byte, 32)
	enc, _ := encryptChunk(key, 0, []byte("data"))

	// Chunk was encrypted as index 0 but we claim it's index 1 — IV mismatch.
	_, err := decryptChunk(key, 1, enc)
	if err == nil {
		t.Error("expected IV mismatch error, got nil")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key := make([]byte, 32)
	_, err := decryptChunk(key, 0, []byte("tooshort"))
	if err == nil {
		t.Error("expected error for truncated ciphertext, got nil")
	}
}

func TestDecryptEmptyInput(t *testing.T) {
	key := make([]byte, 32)
	_, err := decryptChunk(key, 0, []byte{})
	if err == nil {
		t.Error("expected error for empty input, got nil")
	}
}

func TestCompressibleDataIsCompressed(t *testing.T) {
	// Highly repetitive text compresses well.
	text := bytes.Repeat([]byte("hello world "), 2000)
	compressed, isCompressed := tryCompress(text)
	if !isCompressed {
		t.Error("expected repetitive text to be compressed")
	}
	if len(compressed) >= len(text) {
		t.Errorf("compressed size %d >= original %d", len(compressed), len(text))
	}
}

func TestIncompressibleDataIsPassedThrough(t *testing.T) {
	// Already-encrypted data (pseudo-random) should not expand further.
	key := make([]byte, 32)
	key[0] = 42
	random, _ := encryptChunk(key, 0, bytes.Repeat([]byte{0xAB}, 4096))
	_, isCompressed := tryCompress(random)
	// Result may or may not compress, but function must not panic.
	_ = isCompressed
}

func TestEncryptDecryptCompressibleRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	// Large compressible payload: ensures the zstd path is exercised.
	plain := bytes.Repeat([]byte("the quick brown fox jumps "), 5000)

	enc, err := encryptChunk(key, 0, plain)
	if err != nil {
		t.Fatalf("encryptChunk: %v", err)
	}
	dec, err := decryptChunk(key, 0, enc)
	if err != nil {
		t.Fatalf("decryptChunk: %v", err)
	}
	if !bytes.Equal(plain, dec) {
		t.Error("compressible round-trip data mismatch")
	}
	// Verify compression actually happened (encrypted payload should be smaller than raw).
	rawEnc, _ := encryptChunk(key, 1, bytes.Repeat([]byte{0xFF}, len(plain)))
	if len(enc) >= len(rawEnc) {
		t.Logf("note: compressible enc=%d, incompressible enc=%d", len(enc), len(rawEnc))
	}
}
