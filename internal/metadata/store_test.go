package metadata_test

import (
	"errors"
	"testing"
	"time"

	"vaps/internal/metadata"
)

const helloHash = "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"

func TestStorePersistsPayloadRecordAcrossReopen(t *testing.T) {
	path := t.TempDir() + "/metadata.db"

	store, err := metadata.Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	createdAt := time.Unix(123, 0).UTC()
	record := metadata.Payload{
		Hash:      helloHash,
		Size:      5,
		Local:     metadata.LocalCommitted,
		CreatedAt: createdAt,
	}
	if err := store.PutPayload(record); err != nil {
		t.Fatalf("PutPayload returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := metadata.Open(path)
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetPayload(helloHash)
	if err != nil {
		t.Fatalf("GetPayload returned error: %v", err)
	}
	if got.Hash != record.Hash {
		t.Fatalf("Hash = %q, want %q", got.Hash, record.Hash)
	}
	if got.Size != record.Size {
		t.Fatalf("Size = %d, want %d", got.Size, record.Size)
	}
	if got.Local != record.Local {
		t.Fatalf("Local = %q, want %q", got.Local, record.Local)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %s, want %s", got.CreatedAt, createdAt)
	}
}

func TestGetPayloadReturnsNotFound(t *testing.T) {
	store, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	_, err = store.GetPayload(helloHash)
	if !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("GetPayload error = %v, want ErrNotFound", err)
	}
}
