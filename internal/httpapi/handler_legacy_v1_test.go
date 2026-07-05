//go:build vaps_legacy_v1

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vaps/internal/backup"
	"vaps/internal/blobstore"
	"vaps/internal/cache"
	"vaps/internal/httpapi"
	"vaps/internal/metadata"
)

func TestPutHeadAndGetPayload(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	payload := []byte("hello")
	hash := ioHash(payload)

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+hash, bytes.NewReader(payload)))
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d; body=%q", putResponse.Code, http.StatusCreated, putResponse.Body.String())
	}

	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, httptest.NewRequest(http.MethodHead, "/v1/payload?iohash="+hash, nil))
	if headResponse.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", headResponse.Code, http.StatusOK)
	}

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/v1/payload?iohash="+hash, nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", getResponse.Code, http.StatusOK)
	}
	body, err := io.ReadAll(getResponse.Body)
	if err != nil {
		t.Fatalf("read GET body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("GET body = %q, want %q", string(body), string(payload))
	}
}

func TestPutRejectsHashMismatch(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+ioHash([]byte("hello")), bytes.NewReader([]byte("goodbye"))))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestPutExistingPayloadReturnsStoredTrue(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	payload := []byte("hello")
	hash := ioHash(payload)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+hash, bytes.NewReader(payload)))
	if first.Code != http.StatusCreated {
		t.Fatalf("first PUT status = %d, want %d", first.Code, http.StatusCreated)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+hash, bytes.NewReader(payload)))
	if second.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, want %d", second.Code, http.StatusOK)
	}
}

func TestMissingPayloadReturnsNotFound(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	hash := ioHash([]byte("missing"))

	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, httptest.NewRequest(http.MethodHead, "/v1/payload?iohash="+hash, nil))
	if headResponse.Code != http.StatusNotFound {
		t.Fatalf("HEAD status = %d, want %d", headResponse.Code, http.StatusNotFound)
	}
}

func TestExistsReturnsMapByHash(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	existing := ioHash([]byte("hello"))
	missing := ioHash([]byte("missing"))

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+existing, bytes.NewReader([]byte("hello"))))
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d", putResponse.Code, http.StatusCreated)
	}

	requestBody := bytes.NewBufferString(`{"hashes":["` + existing + `","` + missing + `"]}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/payload/exists", requestBody))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", response.Code, http.StatusOK, response.Body.String())
	}
}

func TestInvalidHashReturnsBadRequest(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/payload?iohash=bad", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestHashQueryParameterIsNotAccepted(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/payload?hash="+ioHash([]byte("hello")), nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestPutWritesMetadataRecord(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	handler := httpapi.NewWithMetadata(store, meta)
	payload := []byte("hello")
	hash := ioHash(payload)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+hash, bytes.NewReader(payload)))
	if response.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d; body=%q", response.Code, http.StatusCreated, response.Body.String())
	}

	record, err := meta.GetPayload(hash)
	if err != nil {
		t.Fatalf("GetPayload returned error: %v", err)
	}
	if record.Hash != hash {
		t.Fatalf("metadata hash = %q, want %q", record.Hash, hash)
	}
}

func TestPutQueuesBackupAndMarksMetadataPending(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	backend := &fakeBackup{}
	handler := httpapi.NewWithMetadataCacheAndBackup(store, meta, nil, backend)
	payload := []byte("hello")
	hash := ioHash(payload)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+hash, bytes.NewReader(payload)))
	if response.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d; body=%q", response.Code, http.StatusCreated, response.Body.String())
	}
	if len(backend.enqueued) != 1 || backend.enqueued[0] != hash {
		t.Fatalf("backup enqueued = %#v, want [%s]", backend.enqueued, hash)
	}
}

func TestBackupPayloadEndpointsUseConfiguredBackend(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	backend := &fakeBackup{}
	handler := httpapi.NewWithMetadataCacheAndBackup(store, meta, nil, backend)
	payload := []byte("hello")
	hash := ioHash(payload)
	if _, err := store.Put(hash, bytes.NewReader(payload)); err != nil {
		t.Fatalf("store Put returned error: %v", err)
	}
	if err := meta.PutPayload(metadata.Payload{Hash: hash, Size: int64(len(payload)), Status: metadata.StatusLocal}); err != nil {
		t.Fatalf("PutPayload returned error: %v", err)
	}

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/backup/payload?iohash="+hash, nil))
	if putResponse.Code != http.StatusOK {
		t.Fatalf("backup PUT status = %d, want %d; body=%q", putResponse.Code, http.StatusOK, putResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/v1/backup/payloads", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("backup list status = %d, want %d; body=%q", listResponse.Code, http.StatusOK, listResponse.Body.String())
	}
	var got struct {
		Items []backup.Object `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].Hash != hash {
		t.Fatalf("backup list response = %#v", got)
	}
}

func TestHeadReturnsServerErrorWhenMetadataExistsButLocalFileMissing(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	handler := httpapi.NewWithMetadata(store, meta)
	payload := []byte("hello")
	hash := ioHash(payload)

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+hash, bytes.NewReader(payload)))
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d", putResponse.Code, http.StatusCreated)
	}
	path, err := store.Path(hash)
	if err != nil {
		t.Fatalf("Path returned error: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove blob: %v", err)
	}

	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, httptest.NewRequest(http.MethodHead, "/v1/payload?iohash="+hash, nil))
	if headResponse.Code != http.StatusInternalServerError {
		t.Fatalf("HEAD status = %d, want %d", headResponse.Code, http.StatusInternalServerError)
	}
}

func TestGetReturnsNotFoundWhenMetadataMissingEvenIfLocalFileExists(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	handler := httpapi.NewWithMetadata(store, meta)
	payload := []byte("hello")
	hash := ioHash(payload)

	if _, err := store.Put(hash, bytes.NewReader(payload)); err != nil {
		t.Fatalf("store Put returned error: %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/payload?iohash="+hash, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestGetRestoresBackupOnlyPayloadToLocalStore(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	payload := []byte("hello")
	hash := ioHash(payload)
	backend := &fakeBackup{payloads: map[string][]byte{hash: payload}}
	handler := httpapi.NewWithMetadataCacheAndBackup(store, meta, cache.New(1024, 1024), backend)
	backupedAt := time.Unix(123, 0).UTC()
	if err := meta.PutPayload(metadata.Payload{
		Hash:         hash,
		Status:       metadata.StatusBackup,
		BackupStatus: metadata.Backuped,
		BackupedAt:   &backupedAt,
	}); err != nil {
		t.Fatalf("PutPayload returned error: %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/payload?iohash="+hash, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d; body=%q", response.Code, http.StatusOK, response.Body.String())
	}
}

func TestGetFillsCacheAndMarksMetadataCacheBit(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	lru := cache.New(1024, 1024)
	handler := httpapi.NewWithMetadataAndCache(store, meta, lru)
	payload := []byte("hello")
	hash := ioHash(payload)

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+hash, bytes.NewReader(payload)))
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d", putResponse.Code, http.StatusCreated)
	}

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/v1/payload?iohash="+hash, nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", getResponse.Code, http.StatusOK)
	}
	cached, ok := lru.Get(hash)
	if !ok {
		t.Fatalf("cache miss after GET, want cached payload")
	}
	if !bytes.Equal(cached, payload) {
		t.Fatalf("cached payload = %q, want %q", string(cached), string(payload))
	}
}

func TestDashboardStatsReturnsMetadataAndCacheStats(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	lru := cache.New(1024, 1024)
	handler := httpapi.NewWithMetadataAndCache(store, meta, lru)
	payload := []byte("hello")
	hash := ioHash(payload)

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+hash, bytes.NewReader(payload)))
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d", putResponse.Code, http.StatusCreated)
	}
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/v1/payload?iohash="+hash, nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", getResponse.Code, http.StatusOK)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard/stats", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", response.Code, http.StatusOK, response.Body.String())
	}
}

func TestDashboardMetadataQueryFiltersRecords(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	lru := cache.New(1024, 1024)
	handler := httpapi.NewWithMetadataAndCache(store, meta, lru)
	payload := []byte("hello")
	hash := ioHash(payload)

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+hash, bytes.NewReader(payload)))
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d", putResponse.Code, http.StatusCreated)
	}
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/v1/payload?iohash="+hash, nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", getResponse.Code, http.StatusOK)
	}

	response := httptest.NewRecorder()
	path := "/dashboard/metadata/query?q=" + strings.ToUpper(hash[:8]) + "&status=cache&min_size=5&max_size=5&limit=10"
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", response.Code, http.StatusOK, response.Body.String())
	}
}

type fakeBackup struct {
	puts     []string
	enqueued []string
	payloads map[string][]byte
}

func (f *fakeBackup) Name() string { return "fake" }

func (f *fakeBackup) Exists(_ context.Context, hash string) (bool, error) {
	_, ok := f.payloads[hash]
	return ok, nil
}

func (f *fakeBackup) Open(_ context.Context, hash string) (io.ReadCloser, error) {
	payload, ok := f.payloads[hash]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(payload)), nil
}

func (f *fakeBackup) Enqueue(_ context.Context, hash string) error {
	f.enqueued = append(f.enqueued, hash)
	return nil
}

func (f *fakeBackup) Put(_ context.Context, hash string, reader io.Reader) (backup.Object, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return backup.Object{}, err
	}
	if f.payloads == nil {
		f.payloads = map[string][]byte{}
	}
	f.puts = append(f.puts, hash)
	f.payloads[hash] = data
	return backup.Object{Hash: hash, Path: "blobs/test/" + hash + ".upayload"}, nil
}

func (f *fakeBackup) List(context.Context) ([]backup.Object, error) {
	items := make([]backup.Object, 0, len(f.payloads))
	for hash := range f.payloads {
		items = append(items, backup.Object{Hash: hash, Path: "blobs/test/" + hash + ".upayload"})
	}
	return items, nil
}
