package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"vaps/internal/objectstore"
)

func TestAsyncHTTPAcceptsNewPayloadWithUnavailableSMBMount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	primary, err := objectstore.NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := objectstore.NewFilesystem(filepath.Join(t.TempDir(), "unmounted"), "//missing/share")
	if err != nil {
		t.Fatal(err)
	}
	store, err := objectstore.NewAsync(ctx, []objectstore.Backend{{Name: "s3", Store: primary}, {Name: "smb", Store: secondary}}, t.TempDir(), 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); store.Wait() }()
	handler := multiStorageHandler(t, t.TempDir(), store, openMetadata(t))
	data := []byte("upload-while-SMB-unmounted")
	hash := ioHash(data)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+checksum(data), bytes.NewReader(data)))
	if response.Code != http.StatusCreated {
		t.Fatalf("asynchronous upload = %d: %s", response.Code, response.Body.String())
	}
	if _, err := primary.Head(ctx, hash); err != nil {
		t.Fatalf("preferred copy missing: %v", err)
	}
	if store.ReplicationStatus().PendingObjects != 1 {
		t.Fatal("secondary write not queued")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard/info.json", nil))
	var snapshot struct {
		Config struct {
			Replication struct {
				Pending int `json:"pending_objects"`
			} `json:"replication"`
		} `json:"config"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Config.Replication.Pending != 1 {
		t.Fatalf("pending queue missing from dashboard: %s", response.Body.String())
	}
}
