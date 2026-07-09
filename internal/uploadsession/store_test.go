package uploadsession_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vaps/internal/uploadsession"
	"vaps/internal/utils"
)

func TestCreateAppendAndComplete(t *testing.T) {
	dir := t.TempDir()
	store, err := uploadsession.Open(dir+"/uploads.db", dir+"/uploads", time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	hash := utils.IoHashSumHex([]byte("hello"))
	session, err := store.Create(hash, uploadsession.KindRegular, 5, nil)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	session, err = store.Append(session.ID, 0, bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	if session.Offset != 5 {
		t.Fatalf("Offset = %d, want 5", session.Offset)
	}
	tempPath := session.TempPath
	if _, err := store.MarkCompleted(session.ID); err != nil {
		t.Fatalf("MarkCompleted returned error: %v", err)
	}
	if _, err := os.Stat(tempPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file still exists after MarkCompleted: %v", err)
	}
	if _, err := store.Get(session.ID); !errors.Is(err, uploadsession.ErrNotFound) {
		t.Fatalf("Get after MarkCompleted = %v, want ErrNotFound", err)
	}
}

func TestCleanupExpiredPurgesCompletedResidue(t *testing.T) {
	dir := t.TempDir()
	uploadsDir := filepath.Join(dir, "uploads")
	store, err := uploadsession.Open(filepath.Join(dir, "uploads.db"), uploadsDir, time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	hash := utils.IoHashSumHex([]byte("hello"))
	session, err := store.Create(hash, uploadsession.KindRegular, 5, nil)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.Append(session.ID, 0, bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	// Simulate a legacy completed session that still has a temp file and DB record.
	now := time.Now().UTC()
	legacy := session
	legacy.CompletedAt = &now
	legacy.UpdatedAt = now
	if err := store.PutForTest(legacy); err != nil {
		t.Fatalf("PutForTest returned error: %v", err)
	}
	if _, err := os.Stat(legacy.TempPath); err != nil {
		t.Fatalf("expected legacy temp file: %v", err)
	}

	if err := store.CleanupExpired(); err != nil {
		t.Fatalf("CleanupExpired returned error: %v", err)
	}
	if _, err := os.Stat(legacy.TempPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy temp file still exists after cleanup: %v", err)
	}
	if _, err := store.GetRawForTest(session.ID); !errors.Is(err, uploadsession.ErrNotFound) {
		t.Fatalf("GetRawForTest after cleanup = %v, want ErrNotFound", err)
	}
}

func TestFinalConcatBuildsCombinedUpload(t *testing.T) {
	dir := t.TempDir()
	store, err := uploadsession.Open(dir+"/uploads.db", dir+"/uploads", time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	hash := utils.IoHashSumHex([]byte("hello"))
	partialA, err := store.Create(hash, uploadsession.KindPartial, 2, nil)
	if err != nil {
		t.Fatalf("Create partial A: %v", err)
	}
	partialA, err = store.Append(partialA.ID, 0, bytes.NewReader([]byte("he")))
	if err != nil {
		t.Fatalf("Append partial A: %v", err)
	}
	partialB, err := store.Create(hash, uploadsession.KindPartial, 3, nil)
	if err != nil {
		t.Fatalf("Create partial B: %v", err)
	}
	partialB, err = store.Append(partialB.ID, 0, bytes.NewReader([]byte("llo")))
	if err != nil {
		t.Fatalf("Append partial B: %v", err)
	}

	final, err := store.BuildFinalFromPartials(hash, []uploadsession.Session{partialA, partialB})
	if err != nil {
		t.Fatalf("BuildFinalFromPartials returned error: %v", err)
	}
	if final.Offset != 5 {
		t.Fatalf("final offset = %d, want 5", final.Offset)
	}
	partialAPath := partialA.TempPath
	partialBPath := partialB.TempPath
	finalPath := final.TempPath
	if _, err := store.MarkCompleted(final.ID); err != nil {
		t.Fatalf("MarkCompleted final: %v", err)
	}
	for _, path := range []string{partialAPath, partialBPath, finalPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temp file still exists after final MarkCompleted (%s): %v", path, err)
		}
	}
	if _, err := store.Get(partialA.ID); !errors.Is(err, uploadsession.ErrNotFound) {
		t.Fatalf("Get partial A after final complete = %v, want ErrNotFound", err)
	}
	if _, err := store.Get(partialB.ID); !errors.Is(err, uploadsession.ErrNotFound) {
		t.Fatalf("Get partial B after final complete = %v, want ErrNotFound", err)
	}
}
