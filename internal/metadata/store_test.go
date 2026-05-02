package metadata_test

import (
	"encoding/json"
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
		Status:    metadata.StatusLocal,
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
	if got.Status != record.Status {
		t.Fatalf("Status = %v, want %v", got.Status, record.Status)
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

func TestPayloadStatusBitfield(t *testing.T) {
	status := metadata.StatusLocal.
		WithCache(true).
		WithBackup(metadata.BackupPending).
		WithReadonly(true)

	if !status.HasLocal() {
		t.Fatalf("HasLocal = false, want true")
	}
	if !status.HasCache() {
		t.Fatalf("HasCache = false, want true")
	}
	if got := status.Backup(); got != metadata.BackupPending {
		t.Fatalf("Backup = %v, want %v", got, metadata.BackupPending)
	}
	if !status.IsReadonly() {
		t.Fatalf("IsReadonly = false, want true")
	}
	if status.IsCorrupt() {
		t.Fatalf("IsCorrupt = true, want false")
	}
}

func TestPayloadStatusBackupStates(t *testing.T) {
	tests := []struct {
		backup metadata.BackupStatus
		want   string
	}{
		{backup: metadata.BackupNone, want: "none"},
		{backup: metadata.BackupPending, want: "pending"},
		{backup: metadata.Backuped, want: "backuped"},
		{backup: metadata.BackupFailed, want: "failed"},
	}

	for _, tt := range tests {
		status := metadata.StatusLocal.WithBackup(tt.backup)
		if got := status.Backup(); got != tt.backup {
			t.Fatalf("Backup = %v, want %v", got, tt.backup)
		}
		if got := tt.backup.String(); got != tt.want {
			t.Fatalf("backup %d String() = %q, want %q", tt.backup, got, tt.want)
		}
	}
}

func TestPayloadStatusMarshalsAsNumber(t *testing.T) {
	encoded, err := json.Marshal(metadata.Payload{
		Hash:      helloHash,
		Size:      5,
		Status:    metadata.StatusLocal.WithBackup(metadata.BackupPending),
		CreatedAt: time.Unix(123, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal map returned error: %v", err)
	}
	want := float64(metadata.StatusLocal.WithBackup(metadata.BackupPending))
	if decoded["status"] != want {
		t.Fatalf("encoded status = %v, want %v", decoded["status"], want)
	}
	if _, ok := decoded["storage_status"]; ok {
		t.Fatalf("encoded payload contains storage_status, want single status field")
	}
}
