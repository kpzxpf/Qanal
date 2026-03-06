package infrastructure_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"Qanal/internal/domain"
	"Qanal/internal/infrastructure"
)

func newTestRepo(t *testing.T) (*infrastructure.FileTransferRepo, string) {
	t.Helper()
	dir := t.TempDir()
	repo, err := infrastructure.NewFileTransferRepo(dir)
	if err != nil {
		t.Fatalf("NewFileTransferRepo: %v", err)
	}
	return repo, dir
}

func sampleTransfer(code string) *domain.Transfer {
	return &domain.Transfer{
		Code:        code,
		FileName:    "test.txt",
		FileSize:    1024,
		TotalChunks: 3,
		ChunkSize:   512,
		Status:      domain.StatusPending,
		Uploaded:    []bool{false, false, false},
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Hour),
	}
}

func TestSaveAndFind(t *testing.T) {
	repo, _ := newTestRepo(t)
	tr := sampleTransfer("ABCD1234")

	if err := repo.Save(tr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := repo.FindByCode("ABCD1234")
	if err != nil {
		t.Fatalf("FindByCode: %v", err)
	}
	if got.FileName != tr.FileName {
		t.Errorf("FileName = %q, want %q", got.FileName, tr.FileName)
	}
	if got.TotalChunks != tr.TotalChunks {
		t.Errorf("TotalChunks = %d, want %d", got.TotalChunks, tr.TotalChunks)
	}
	if got.FileSize != tr.FileSize {
		t.Errorf("FileSize = %d, want %d", got.FileSize, tr.FileSize)
	}
}

func TestFindNotFound(t *testing.T) {
	repo, _ := newTestRepo(t)

	_, err := repo.FindByCode("NOTEXIST")
	if err != domain.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMarkChunkUploaded(t *testing.T) {
	repo, _ := newTestRepo(t)
	repo.Save(sampleTransfer("MARK1234"))

	updated, err := repo.MarkChunkUploaded("MARK1234", 1)
	if err != nil {
		t.Fatalf("MarkChunkUploaded: %v", err)
	}
	if !updated.Uploaded[1] {
		t.Error("expected chunk 1 to be marked uploaded")
	}
	if updated.Uploaded[0] || updated.Uploaded[2] {
		t.Error("expected only chunk 1 to be marked")
	}
	if updated.Status != domain.StatusActive {
		t.Errorf("status = %q, want %q", updated.Status, domain.StatusActive)
	}
}

func TestMarkChunkUploadedInvalidIndex(t *testing.T) {
	repo, _ := newTestRepo(t)
	repo.Save(sampleTransfer("BADIDX11"))

	_, err := repo.MarkChunkUploaded("BADIDX11", 100)
	if err != domain.ErrInvalidIndex {
		t.Errorf("expected ErrInvalidIndex, got %v", err)
	}
}

func TestMarkChunkUploadedNotFound(t *testing.T) {
	repo, _ := newTestRepo(t)

	_, err := repo.MarkChunkUploaded("NOTFOUND", 0)
	if err != domain.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateStatus(t *testing.T) {
	repo, _ := newTestRepo(t)
	repo.Save(sampleTransfer("STATTEST"))

	if err := repo.UpdateStatus("STATTEST", domain.StatusComplete); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := repo.FindByCode("STATTEST")
	if err != nil {
		t.Fatalf("FindByCode after status update: %v", err)
	}
	if got.Status != domain.StatusComplete {
		t.Errorf("status = %q, want %q", got.Status, domain.StatusComplete)
	}
}

func TestUpdateStatusNotFound(t *testing.T) {
	repo, _ := newTestRepo(t)

	err := repo.UpdateStatus("NOTFOUND", domain.StatusComplete)
	if err != domain.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListAll(t *testing.T) {
	repo, _ := newTestRepo(t)
	codes := []string{"CODE0001", "CODE0002", "CODE0003"}
	for _, c := range codes {
		repo.Save(sampleTransfer(c))
	}

	all, err := repo.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListAll length = %d, want 3", len(all))
	}
}

func TestListAllEmpty(t *testing.T) {
	repo, _ := newTestRepo(t)

	all, err := repo.ListAll()
	if err != nil {
		t.Fatalf("ListAll on empty repo: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected empty list, got %d items", len(all))
	}
}

func TestDelete(t *testing.T) {
	repo, _ := newTestRepo(t)
	repo.Save(sampleTransfer("DELTEST"))

	if err := repo.Delete("DELTEST"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := repo.FindByCode("DELTEST")
	if err != domain.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestHydrateOnStartup(t *testing.T) {
	dir := t.TempDir()
	repo1, _ := infrastructure.NewFileTransferRepo(dir)
	tr := sampleTransfer("HYDTEST")
	repo1.Save(tr)

	// New instance pointing to same dir — should hydrate the existing transfer from disk.
	repo2, err := infrastructure.NewFileTransferRepo(dir)
	if err != nil {
		t.Fatalf("second NewFileTransferRepo: %v", err)
	}

	got, err := repo2.FindByCode("HYDTEST")
	if err != nil {
		t.Fatalf("FindByCode after hydrate: %v", err)
	}
	if got.FileName != tr.FileName {
		t.Errorf("hydrated FileName = %q, want %q", got.FileName, tr.FileName)
	}
}

func TestCacheServesDataWithoutDisk(t *testing.T) {
	repo, dir := newTestRepo(t)
	tr := sampleTransfer("CACHETEST")
	repo.Save(tr)

	// Delete the meta.json from disk — the in-memory cache must still serve it.
	os.Remove(filepath.Join(dir, "CACHETEST", "meta.json"))

	got, err := repo.FindByCode("CACHETEST")
	if err != nil {
		t.Fatalf("FindByCode after disk delete: %v", err)
	}
	if got.Code != "CACHETEST" {
		t.Error("expected transfer to be served from cache after disk deletion")
	}
}

func TestFindReturnsCopy(t *testing.T) {
	repo, _ := newTestRepo(t)
	repo.Save(sampleTransfer("COPYTEST"))

	got1, _ := repo.FindByCode("COPYTEST")
	got1.Status = domain.StatusComplete // mutate the returned copy

	got2, _ := repo.FindByCode("COPYTEST")
	if got2.Status == domain.StatusComplete {
		t.Error("FindByCode returned a shared reference instead of a copy")
	}
}
