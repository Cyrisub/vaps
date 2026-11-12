package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vaps/internal/blobstore"
	"vaps/internal/httpapi"
	"vaps/internal/metadata"
	"vaps/internal/objectstore"
	"vaps/internal/uploadsession"
)

func multiStorageHandler(t *testing.T, cacheDir string, stores objectstore.Store, meta *metadata.Store) http.Handler {
	t.Helper()
	dir := t.TempDir()
	uploads, err := uploadsession.Open(filepath.Join(dir, "uploads.db"), filepath.Join(dir, "uploads"), time.Hour, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { uploads.Close() })
	return httpapi.NewV2(blobstore.New(cacheDir), stores, meta, nil, nil, uploads, httpapi.Options{}).HTTPHandler()
}

func TestMirroredHTTPRetriesPartialWriteAndReadsReplicaWithDifferentETag(t *testing.T) {
	primary := newMemoryObjectStore()
	secondary := newMemoryObjectStore()
	secondary.putErr = errors.New("SMB offline")
	stores, err := objectstore.NewMulti([]objectstore.Backend{{Name: "s3", Store: primary}, {Name: "smb", Store: secondary}})
	if err != nil {
		t.Fatal(err)
	}
	meta := openMetadata(t)
	cacheDir := t.TempDir()
	handler := multiStorageHandler(t, cacheDir, stores, meta)
	data := []byte("mirrored-http-payload")
	hash, sum := ioHash(data), checksum(data)
	push := func() *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+sum, bytes.NewReader(data)))
		return r
	}
	if response := push(); response.Code != http.StatusInternalServerError {
		t.Fatalf("partial write = %d: %s", response.Code, response.Body.String())
	}
	if _, err := meta.GetPayload(hash); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("failed upload committed metadata: %v", err)
	}
	secondary.putErr = nil
	if response := push(); response.Code != http.StatusOK {
		t.Fatalf("retry = %d: %s", response.Code, response.Body.String())
	}
	if _, err := secondary.Head(context.Background(), hash); err != nil {
		t.Fatalf("duplicate retry failed to fill replica: %v", err)
	}
	// The old primary ETag must still be enforced for reads from that backend,
	// while a different provider's ETag must not block a verified replica.
	record, _ := meta.GetPayload(hash)
	record.ETag, record.ETagBackend = "cos-etag", "s3"
	if err := meta.PutPayload(record); err != nil {
		t.Fatal(err)
	}
	primary.mu.Lock()
	delete(primary.objects, hash)
	primary.mu.Unlock()
	secondary.mu.Lock()
	object := secondary.objects[hash]
	object.info.ETag = "smb-etag"
	secondary.objects[hash] = object
	secondary.mu.Unlock()
	path, _ := blobstore.New(cacheDir).Path(hash)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for _, partial := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, "/v2/payload/pull?iohash="+hash, nil)
		if partial {
			r.Header.Set("Range", "bytes=1-6")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		want, code := data, http.StatusOK
		if partial {
			want, code = data[1:7], http.StatusPartialContent
		}
		if response.Code != code || !bytes.Equal(response.Body.Bytes(), want) {
			t.Fatalf("fallback = %d: %q", response.Code, response.Body.Bytes())
		}
	}
	stored, _ := meta.GetPayload(hash)
	if stored.ETagBackend != "smb" || stored.ETag != "smb-etag" {
		t.Fatalf("fallback ETag not scoped: %+v", stored)
	}
}

func TestFilesystemHTTPFallbackVerifiesBytesAndStartupMetadata(t *testing.T) {
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	first, err := objectstore.NewFilesystem(firstRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := objectstore.NewFilesystem(secondRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	stores, _ := objectstore.NewMulti([]objectstore.Backend{{Name: "disk", Store: first}, {Name: "smb", Store: second}})
	meta := openMetadata(t)
	handler := multiStorageHandler(t, t.TempDir(), stores, meta)
	data := []byte("filesystem-fallback-integration")
	hash := ioHash(data)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+checksum(data), bytes.NewReader(data)))
	if response.Code != http.StatusCreated {
		t.Fatalf("upload = %d: %s", response.Code, response.Body.String())
	}
	// A fresh handler has an empty disk cache, so this exercises remote fallback.
	if err := os.RemoveAll(filepath.Join(firstRoot, hash[:2])); err != nil {
		t.Fatal(err)
	}
	handler = multiStorageHandler(t, t.TempDir(), stores, meta)
	server := httptest.NewServer(handler)
	defer server.Close()
	res, err := server.Client().Get(server.URL + "/v2/payload/pull?iohash=" + hash)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Equal(body, data) {
		t.Fatalf("TCP fallback = %d: %q", res.StatusCode, body)
	}
}
