package uploadsession_test

import (
	"bytes"
	"testing"
	"time"

	"vaps/internal/iohash"
	"vaps/internal/uploadsession"
)

func TestCreateAppendAndComplete(t *testing.T) {
	dir := t.TempDir()
	store, err := uploadsession.Open(dir+"/uploads.db", dir+"/uploads", time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	hash := iohash.SumHex([]byte("hello"))
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
	if _, err := store.MarkCompleted(session.ID); err != nil {
		t.Fatalf("MarkCompleted returned error: %v", err)
	}
}

func TestFinalConcatBuildsCombinedUpload(t *testing.T) {
	dir := t.TempDir()
	store, err := uploadsession.Open(dir+"/uploads.db", dir+"/uploads", time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	hash := iohash.SumHex([]byte("hello"))
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
}
