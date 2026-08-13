package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vaps/internal/cache"
	"vaps/internal/metadata"
	"vaps/internal/objectstore"
)

type dashboardPullStats struct {
	Cache *struct {
		Entries int `json:"entries"`
	} `json:"cache"`
	Pull struct {
		Hits   int64 `json:"hits"`
		Misses int64 `json:"misses"`
		Memory struct {
			Hits   int64 `json:"hits"`
			Misses int64 `json:"misses"`
		} `json:"memory"`
		Disk struct {
			Hits   int64 `json:"hits"`
			Misses int64 `json:"misses"`
		} `json:"disk"`
	} `json:"pull"`
}

func TestPullStatsCountsDiskThenMemoryHits(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), cache.New(1024, 1024), nil)
	token := requestToken(t, handler)
	payload := []byte("cached-pull")
	hash := ioHash(payload)
	pushPayload(t, handler, token, payload)

	pullOnce(t, handler, token, hash, http.StatusOK)
	first := fetchPullStats(t, handler)
	if first.Cache == nil || first.Cache.Entries != 1 {
		t.Fatalf("cache after first pull = %#v, want 1 entry", first.Cache)
	}
	if first.Pull.Hits != 1 || first.Pull.Misses != 0 {
		t.Fatalf("overall after first pull = %#v, want 1 hit", first.Pull)
	}
	if first.Pull.Memory.Hits != 0 || first.Pull.Memory.Misses != 1 {
		t.Fatalf("memory after first pull = %#v, want 0/1", first.Pull.Memory)
	}
	if first.Pull.Disk.Hits != 1 || first.Pull.Disk.Misses != 0 {
		t.Fatalf("disk after first pull = %#v, want 1/0", first.Pull.Disk)
	}

	pullOnce(t, handler, token, hash, http.StatusOK)
	second := fetchPullStats(t, handler)
	if second.Pull.Hits != 2 || second.Pull.Misses != 0 {
		t.Fatalf("overall after second pull = %#v, want 2 hits", second.Pull)
	}
	if second.Pull.Memory.Hits != 1 || second.Pull.Memory.Misses != 1 {
		t.Fatalf("memory after second pull = %#v, want 1/1", second.Pull.Memory)
	}
	if second.Pull.Disk.Hits != 1 || second.Pull.Disk.Misses != 0 {
		t.Fatalf("disk after second pull = %#v, want 1/0", second.Pull.Disk)
	}
}

func TestPullStatsCountsRemoteMissWithoutMemoryCache(t *testing.T) {
	meta := openMetadata(t)
	payload := []byte("remote-only")
	hash := ioHash(payload)
	sum := checksum(payload)
	objects := newMemoryObjectStore()
	objects.objects[hash] = memoryObject{
		info: objectstore.Info{Hash: hash, ETag: "etag-1", Checksum: sum, Size: int64(len(payload))},
		data: payload,
	}
	if err := meta.PutPayload(metadata.Payload{
		Hash:     hash,
		ETag:     "etag-1",
		Checksum: sum,
		Size:     int64(len(payload)),
		Status:   metadata.StatusStored,
	}); err != nil {
		t.Fatal(err)
	}
	handler := newV2Handler(t, meta, nil, objects)
	token := requestToken(t, handler)

	pullOnce(t, handler, token, hash, http.StatusOK)
	stats := fetchPullStats(t, handler)
	if stats.Cache != nil {
		t.Fatalf("cache stats = %#v, want omitted when memory cache is disabled", stats.Cache)
	}
	if stats.Pull.Hits != 0 || stats.Pull.Misses != 1 {
		t.Fatalf("overall = %#v, want 0 hits / 1 miss", stats.Pull)
	}
	if stats.Pull.Memory.Hits != 0 || stats.Pull.Memory.Misses != 0 {
		t.Fatalf("memory = %#v, want zero when cache is disabled", stats.Pull.Memory)
	}
	if stats.Pull.Disk.Hits != 0 || stats.Pull.Disk.Misses != 1 {
		t.Fatalf("disk = %#v, want 0/1", stats.Pull.Disk)
	}
}

func TestPullStatsIgnoresFailedLookups(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), cache.New(1024, 1024), nil)
	token := requestToken(t, handler)
	pullOnce(t, handler, token, ioHash([]byte("missing")), http.StatusNotFound)

	stats := fetchPullStats(t, handler)
	if stats.Pull.Hits != 0 || stats.Pull.Misses != 0 {
		t.Fatalf("failed pull counted: %#v", stats.Pull)
	}
}

func pushPayload(t *testing.T, handler http.Handler, token string, payload []byte) {
	t.Helper()
	hash := ioHash(payload)
	push := httptest.NewRecorder()
	pushReq := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+checksum(payload), bytes.NewReader(payload))
	pushReq.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(push, pushReq)
	if push.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201; body=%q", push.Code, push.Body.String())
	}
}

func pullOnce(t *testing.T, handler http.Handler, token, hash string, wantStatus int) {
	t.Helper()
	pull := httptest.NewRecorder()
	pullReq := httptest.NewRequest(http.MethodGet, "/v2/payload/pull?iohash="+hash, nil)
	pullReq.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(pull, pullReq)
	if pull.Code != wantStatus {
		t.Fatalf("pull status = %d, want %d; body=%q", pull.Code, wantStatus, pull.Body.String())
	}
}

func fetchPullStats(t *testing.T, handler http.Handler) dashboardPullStats {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard/stats", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200; body=%q", response.Code, response.Body.String())
	}
	var stats dashboardPullStats
	if err := json.Unmarshal(response.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	return stats
}
