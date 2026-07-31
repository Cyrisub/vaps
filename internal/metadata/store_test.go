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
		t.Fatal(err)
	}
	created := time.Unix(123, 0).UTC()
	record := metadata.Payload{Hash: helloHash, ETag: "etag-1", Checksum: "01020304", Size: 5, Status: metadata.StatusCached, CreatedAt: &created}
	if err := store.PutPayload(record); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := metadata.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetPayload(helloHash)
	if err != nil || got.Hash != record.Hash || got.ETag != record.ETag || got.Checksum != record.Checksum || got.Size != record.Size || got.Status != record.Status || got.CreatedAt == nil || !got.CreatedAt.Equal(*record.CreatedAt) {
		t.Fatalf("GetPayload = %#v, %v; want %#v", got, err, record)
	}
}

func TestGetPayloadReturnsNotFound(t *testing.T) {
	store, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.GetPayload(helloHash); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("GetPayload error = %v", err)
	}
}

func TestStatsAndQueryUseUnifiedStatus(t *testing.T) {
	store, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	records := []metadata.Payload{
		{Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Checksum: "01020304", Size: 5, Status: metadata.StatusStored, CreatedAt: timePtr(time.Unix(1, 0).UTC())},
		{Hash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbabc1", Checksum: "05060708", Size: 10, Status: metadata.StatusCached, CreatedAt: timePtr(time.Unix(2, 0).UTC())},
	}
	for _, record := range records {
		if err := store.PutPayload(record); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := store.Stats()
	if err != nil || stats.PayloadCount != 2 || stats.TotalBytes != 15 || stats.CachedCount != 1 {
		t.Fatalf("Stats = %#v, %v", stats, err)
	}
	result, err := store.QueryPayloads(metadata.PayloadQuery{RequireCache: true, Limit: 10})
	if err != nil || result.Total != 1 || result.Items[0].Hash != records[1].Hash {
		t.Fatalf("QueryPayloads = %#v, %v", result, err)
	}
}

func TestPayloadStatusMarshalsAsSingleEnum(t *testing.T) {
	encoded, err := json.Marshal(metadata.Payload{Hash: helloHash, Checksum: "01020304", Size: 5, Status: metadata.StatusStored})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["status"] != float64(metadata.StatusStored) || decoded["checksum"] != "01020304" {
		t.Fatalf("encoded payload = %#v", decoded)
	}
	if _, ok := decoded["backup_status"]; ok {
		t.Fatalf("backup_status must not be persisted")
	}
}

func TestPayloadUnmarshalsMissingETagAsEmpty(t *testing.T) {
	var payload metadata.Payload
	if err := json.Unmarshal([]byte(`{"hash":"`+helloHash+`","checksum":"01020304","size":5,"status":0}`), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ETag != "" {
		t.Fatalf("missing ETag = %q, want empty", payload.ETag)
	}
}

func TestPayloadUnmarshalsLegacyContentHashForMigration(t *testing.T) {
	var payload metadata.Payload
	if err := json.Unmarshal([]byte(`{"hash":"`+helloHash+`","content_hash":"legacy-blake3","size":5,"status":0}`), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Checksum != "" || payload.LegacyChecksum != "legacy-blake3" {
		t.Fatalf("legacy payload = %#v", payload)
	}
}

func timePtr(value time.Time) *time.Time { return &value }
