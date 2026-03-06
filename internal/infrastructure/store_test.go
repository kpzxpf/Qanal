package infrastructure_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"Qanal/internal/infrastructure"
)

func newTestStore(t *testing.T) (*infrastructure.FileChunkStore, string) {
	t.Helper()
	dir := t.TempDir()
	return infrastructure.NewFileChunkStore(dir), dir
}

func TestStoreWriteAndOpen(t *testing.T) {
	store, dir := newTestStore(t)

	// The store expects the transfer directory to already exist (created by repo.Save).
	os.MkdirAll(filepath.Join(dir, "TESTCODE"), 0755)

	data := []byte("encrypted chunk data for testing")
	if err := store.Write("TESTCODE", 0, bytes.NewReader(data)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rc, size, err := store.Open("TESTCODE", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()

	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch: got %q, want %q", got, data)
	}
}

func TestStoreWriteMultipleChunks(t *testing.T) {
	store, dir := newTestStore(t)
	os.MkdirAll(filepath.Join(dir, "MULTI"), 0755)

	chunks := [][]byte{
		[]byte("chunk zero data"),
		[]byte("chunk one data"),
		[]byte("chunk two data"),
	}

	for i, data := range chunks {
		if err := store.Write("MULTI", i, bytes.NewReader(data)); err != nil {
			t.Fatalf("Write chunk %d: %v", i, err)
		}
	}

	for i, want := range chunks {
		rc, _, err := store.Open("MULTI", i)
		if err != nil {
			t.Fatalf("Open chunk %d: %v", i, err)
		}
		got, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.Equal(got, want) {
			t.Errorf("chunk %d data mismatch", i)
		}
	}
}

func TestStoreOpenNotFound(t *testing.T) {
	store, _ := newTestStore(t)

	_, _, err := store.Open("NOEXIST", 0)
	if err == nil {
		t.Error("expected error opening non-existent chunk, got nil")
	}
}

func TestStoreDeleteTransfer(t *testing.T) {
	store, dir := newTestStore(t)
	os.MkdirAll(filepath.Join(dir, "DELCODE"), 0755)

	store.Write("DELCODE", 0, bytes.NewReader([]byte("chunk 0")))
	store.Write("DELCODE", 1, bytes.NewReader([]byte("chunk 1")))

	if err := store.DeleteTransfer("DELCODE"); err != nil {
		t.Fatalf("DeleteTransfer: %v", err)
	}

	_, _, err := store.Open("DELCODE", 0)
	if err == nil {
		t.Error("expected error after DeleteTransfer, got nil")
	}

	// Transfer directory should be removed.
	if _, err := os.Stat(filepath.Join(dir, "DELCODE")); !os.IsNotExist(err) {
		t.Error("expected transfer directory to be removed")
	}
}

func TestStoreDeleteNonExistent(t *testing.T) {
	store, _ := newTestStore(t)

	// Deleting a non-existent transfer should not return an error.
	if err := store.DeleteTransfer("NOTHERE"); err != nil {
		t.Errorf("DeleteTransfer on missing dir returned error: %v", err)
	}
}
