package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// computeFileHash computes the SHA-256 hex digest of f from its current position.
// The file offset is not reset — callers using ReadAt afterwards are unaffected.
func computeFileHash(f *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("sha256 compute: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyFileHash opens path, computes SHA-256, and compares against expected.
func verifyFileHash(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("sha256 read: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != expected {
		return fmt.Errorf("integrity check failed: expected SHA-256 %.16s…, got %.16s…", expected, got)
	}
	return nil
}
