package domain_test

import (
	"testing"
	"time"

	"Qanal/internal/domain"
)

func TestIsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"past", time.Now().Add(-time.Hour), true},
		{"future", time.Now().Add(time.Hour), false},
		{"zero", time.Time{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := &domain.Transfer{ExpiresAt: tc.expiresAt}
			if got := tr.IsExpired(); got != tc.want {
				t.Errorf("IsExpired() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUploadedCount(t *testing.T) {
	tests := []struct {
		name     string
		uploaded []bool
		want     int
	}{
		{"none", []bool{false, false, false}, 0},
		{"all", []bool{true, true, true}, 3},
		{"some", []bool{true, false, true, false, true}, 3},
		{"empty", []bool{}, 0},
		{"single true", []bool{true}, 1},
		{"single false", []bool{false}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := &domain.Transfer{Uploaded: tc.uploaded}
			if got := tr.UploadedCount(); got != tc.want {
				t.Errorf("UploadedCount() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTransferStatusConstants(t *testing.T) {
	if domain.StatusPending == domain.StatusActive {
		t.Error("StatusPending and StatusActive must differ")
	}
	if domain.StatusActive == domain.StatusComplete {
		t.Error("StatusActive and StatusComplete must differ")
	}
	if domain.StatusComplete == domain.StatusExpired {
		t.Error("StatusComplete and StatusExpired must differ")
	}
}

func TestDomainErrors(t *testing.T) {
	errors := []error{
		domain.ErrNotFound,
		domain.ErrInvalidIndex,
		domain.ErrTransferDone,
		domain.ErrFileTooLarge,
		domain.ErrChunkTooLarge,
		domain.ErrTransferExpired,
	}
	for i, e := range errors {
		for j, other := range errors {
			if i != j && e == other {
				t.Errorf("domain errors at index %d and %d are identical: %v", i, j, e)
			}
		}
	}
}
