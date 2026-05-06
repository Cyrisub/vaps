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
	backupedAt := time.Unix(456, 0).UTC()
	record := metadata.Payload{
		Hash:       helloHash,
		Size:       5,
		Status:     metadata.StatusLocal.WithBackup(true),
		CreatedAt:  &createdAt,
		BackupedAt: &backupedAt,
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
	if got.CreatedAt == nil || !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %v, want %s", got.CreatedAt, createdAt)
	}
	if got.BackupedAt == nil || !got.BackupedAt.Equal(backupedAt) {
		t.Fatalf("BackupedAt = %v, want %s", got.BackupedAt, backupedAt)
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

func TestStatsCountsPayloadsAndBytes(t *testing.T) {
	store, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	records := []metadata.Payload{
		{Hash: helloHash, Size: 5, Status: metadata.StatusLocal, CreatedAt: timePtr(time.Unix(1, 0).UTC())},
		{Hash: "7c211433f02071597741e6ff5a8ea34789abbf43", Size: 7, Status: metadata.StatusLocal.WithCache(true), CreatedAt: timePtr(time.Unix(2, 0).UTC())},
	}
	for _, record := range records {
		if err := store.PutPayload(record); err != nil {
			t.Fatalf("PutPayload returned error: %v", err)
		}
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}
	if stats.PayloadCount != 2 {
		t.Fatalf("PayloadCount = %d, want 2", stats.PayloadCount)
	}
	if stats.TotalBytes != 12 {
		t.Fatalf("TotalBytes = %d, want 12", stats.TotalBytes)
	}
	if stats.CachedCount != 1 {
		t.Fatalf("CachedCount = %d, want 1", stats.CachedCount)
	}
}

func TestQueryPayloadsFiltersAndPaginates(t *testing.T) {
	store, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	backedUp := true
	minSize := int64(8)
	maxSize := int64(15)
	records := []metadata.Payload{
		{Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 5, Status: metadata.StatusLocal, CreatedAt: timePtr(time.Unix(1, 0).UTC())},
		{Hash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbabc1", Size: 10, Status: metadata.StatusLocal.WithCache(true).WithBackup(true), CreatedAt: timePtr(time.Unix(2, 0).UTC()), BackupedAt: timePtr(time.Unix(4, 0).UTC())},
		{Hash: "ccccccccccccccccccccccccccccccccccccabc2", Size: 20, Status: metadata.StatusLocal.WithBackup(true), CreatedAt: timePtr(time.Unix(3, 0).UTC()), BackupedAt: timePtr(time.Unix(5, 0).UTC())},
	}
	for _, record := range records {
		if err := store.PutPayload(record); err != nil {
			t.Fatalf("PutPayload returned error: %v", err)
		}
	}

	filtered, err := store.QueryPayloads(metadata.PayloadQuery{
		HashContains: "abc",
		RequireCache: true,
		Backup:       &backedUp,
		MinSize:      &minSize,
		MaxSize:      &maxSize,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("QueryPayloads returned error: %v", err)
	}
	if filtered.Total != 1 {
		t.Fatalf("filtered Total = %d, want 1", filtered.Total)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].Hash != records[1].Hash {
		t.Fatalf("filtered Items = %+v, want only %s", filtered.Items, records[1].Hash)
	}

	paged, err := store.QueryPayloads(metadata.PayloadQuery{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("QueryPayloads page returned error: %v", err)
	}
	if paged.Total != 3 {
		t.Fatalf("paged Total = %d, want 3", paged.Total)
	}
	if got := []string{paged.Items[0].Hash, paged.Items[1].Hash}; got[0] != records[1].Hash || got[1] != records[2].Hash {
		t.Fatalf("paged hashes = %v, want second and third records", got)
	}
}

func TestPayloadStatusBitfield(t *testing.T) {
	status := metadata.StatusLocal.
		WithCache(true).
		WithBackup(true)

	if !status.HasLocal() {
		t.Fatalf("HasLocal = false, want true")
	}
	if !status.HasCache() {
		t.Fatalf("HasCache = false, want true")
	}
	if !status.HasBackup() {
		t.Fatalf("HasBackup = false, want true")
	}
	status = status.WithBackup(false)
	if status.HasBackup() {
		t.Fatalf("HasBackup = true, want false")
	}
}

func TestRuntimeBackupStatusStrings(t *testing.T) {
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
		if got := tt.backup.String(); got != tt.want {
			t.Fatalf("backup %d String() = %q, want %q", tt.backup, got, tt.want)
		}
	}
}

func TestPayloadStatusMarshalsAsNumber(t *testing.T) {
	encoded, err := json.Marshal(metadata.Payload{
		Hash:         helloHash,
		Size:         5,
		Status:       metadata.StatusLocal.WithBackup(true),
		BackupStatus: metadata.BackupPending,
		CreatedAt:    timePtr(time.Unix(123, 0).UTC()),
		BackupedAt:   timePtr(time.Unix(456, 0).UTC()),
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal map returned error: %v", err)
	}
	want := float64(metadata.StatusLocal.WithBackup(true))
	if decoded["status"] != want {
		t.Fatalf("encoded status = %v, want %v", decoded["status"], want)
	}
	if _, ok := decoded["BackupStatus"]; ok {
		t.Fatalf("encoded payload contains BackupStatus, want runtime field omitted")
	}
	if _, ok := decoded["backup_status"]; ok {
		t.Fatalf("encoded payload contains backup_status, want runtime field omitted")
	}
	if decoded["created_at"] == nil {
		t.Fatalf("encoded payload missing created_at")
	}
	if decoded["backuped_at"] == nil {
		t.Fatalf("encoded payload missing backuped_at")
	}
}

func TestPayloadTimeFieldsCanBeNull(t *testing.T) {
	encoded, err := json.Marshal(metadata.Payload{Hash: helloHash, Size: 5, Status: metadata.StatusLocal})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal map returned error: %v", err)
	}
	if value, ok := decoded["created_at"]; !ok || value != nil {
		t.Fatalf("created_at = %v, want null", value)
	}
	if value, ok := decoded["backuped_at"]; !ok || value != nil {
		t.Fatalf("backuped_at = %v, want null", value)
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
