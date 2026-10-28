package httpapi_test

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vaps/internal/auth"
	"vaps/internal/blobstore"
	"vaps/internal/cache"
	"vaps/internal/httpapi"
	"vaps/internal/metadata"
	"vaps/internal/objectstore"
	"vaps/internal/uploadsession"
)

func TestHealthReturnsOK(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.String() != "ok\n" {
		t.Fatalf("body = %q, want ok newline", response.Body.String())
	}
}

func TestDashboardReturnsHTML(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("VAPS Dashboard")) {
		t.Fatalf("dashboard body does not contain title: %q", response.Body.String())
	}
}

func TestAccessLogHidesVerboseAndKeepsWarningsAndErrors(t *testing.T) {
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	defer func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	}()

	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.SetFlags(0)

	handler := httpapi.AccessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dashboard":
			w.WriteHeader(http.StatusOK)
		case "/dashboard/not-found":
			http.NotFound(w, r)
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if logs.Len() != 0 {
		t.Fatalf("verbose access log = %q, want hidden", logs.String())
	}

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/dashboard/not-found", nil))
	if got := logs.String(); !strings.Contains(got, "access method=GET path=\"/dashboard/not-found\" status=404") ||
		!strings.Contains(got, "level=warning") {
		t.Fatalf("warning access log = %q", got)
	}

	logs.Reset()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/internal-error", nil))
	if got := logs.String(); !strings.Contains(got, "access method=GET path=\"/internal-error\" status=500") ||
		!strings.Contains(got, "level=error") {
		t.Fatalf("error access log = %q", got)
	}
}

func TestDashboardMetadataReturnsHTML(t *testing.T) {
	handler := httpapi.NewWithMetadata(blobstore.New(t.TempDir()), openMetadata(t))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard/metadata", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("Payloads")) {
		t.Fatalf("payloads dashboard body does not contain title: %q", response.Body.String())
	}
}

func TestV2LegacyAuthEndpointsDoNotChangeSource(t *testing.T) {
	handler, store := newV2HandlerWithAuth(t, openMetadata(t), nil, nil)
	token := requestToken(t, handler)
	for _, path := range []string{"/v2/auth/expire", "/v2/auth/park"} {
		for _, value := range []string{"", "unknown", token} {
			response := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if value != "" {
				req.Header.Set("Authorization", authHeader(value))
			}
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("%s: status=%d", path, response.Code)
			}
			var flags map[string]bool
			if err := json.Unmarshal(response.Body.Bytes(), &flags); err != nil {
				t.Fatal(err)
			}
			for name, value := range flags {
				if value {
					t.Fatalf("legacy operation %s should be disabled", name)
				}
			}
		}
	}
	if next := requestToken(t, handler); next != token {
		t.Fatal("legacy endpoints changed source token")
	}
	record, err := store.Validate(token)
	if err != nil || record.ParkedAt != nil || record.RevokedAt != nil || !record.ExpiresAt.IsZero() {
		t.Fatalf("source lifecycle changed: %#v, %v", record, err)
	}
}

func TestV2DirectPushPullAndRange(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), cache.New(1024, 1024), nil)
	token := requestToken(t, handler)
	payload := []byte("hello-range")
	hash := ioHash(payload)

	push := httptest.NewRecorder()
	pushReq := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+checksum(payload), bytes.NewReader(payload))
	pushReq.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(push, pushReq)
	if push.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want %d; body=%q", push.Code, http.StatusCreated, push.Body.String())
	}

	head := httptest.NewRecorder()
	headReq := httptest.NewRequest(http.MethodHead, "/v2/payload/pull?iohash="+hash, nil)
	headReq.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(head, headReq)
	if head.Code != http.StatusOK {
		t.Fatalf("head status = %d, want %d", head.Code, http.StatusOK)
	}
	if head.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", head.Header().Get("Accept-Ranges"))
	}

	get := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/v2/payload/pull?iohash="+hash, nil)
	getReq.Header.Set("Authorization", authHeader(token))
	getReq.Header.Set("Range", "bytes=0-2")
	handler.ServeHTTP(get, getReq)
	if get.Code != http.StatusPartialContent {
		t.Fatalf("range GET status = %d, want %d", get.Code, http.StatusPartialContent)
	}
	if got := get.Header().Get("Content-Range"); got != "bytes 0-2/11" {
		t.Fatalf("Content-Range = %q, want bytes 0-2/11", got)
	}
	if !bytes.Equal(get.Body.Bytes(), payload[:3]) {
		t.Fatalf("range body = %q, want %q", get.Body.Bytes(), payload[:3])
	}
}

func TestV2MetadataExistsAndVCS(t *testing.T) {
	meta := openMetadata(t)
	handler, _ := newV2HandlerWithAuth(t, meta, nil, nil)
	token := requestToken(t, handler)
	payload := []byte("meta")
	hash := ioHash(payload)

	push := httptest.NewRecorder()
	pushReq := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+checksum(payload), bytes.NewReader(payload))
	pushReq.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(push, pushReq)
	if push.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want %d", push.Code, http.StatusCreated)
	}

	vcsBody := `{"payload_hash":"` + hash + `","vcs_type":"svn","repo":"repo","revision":"123","path":"/Content/Foo.uasset","asset_id":"Foo"}`
	vcsPost := httptest.NewRecorder()
	handler.ServeHTTP(vcsPost, httptest.NewRequest(http.MethodPost, "/v2/metadata", strings.NewReader(vcsBody)))
	if vcsPost.Code != http.StatusCreated {
		t.Fatalf("vcs post status = %d, want %d; body=%q", vcsPost.Code, http.StatusCreated, vcsPost.Body.String())
	}

	vcsGet := httptest.NewRecorder()
	handler.ServeHTTP(vcsGet, httptest.NewRequest(http.MethodGet, "/v2/metadata?payload_hash="+hash, nil))
	if vcsGet.Code != http.StatusOK {
		t.Fatalf("vcs get status = %d, want %d", vcsGet.Code, http.StatusOK)
	}
	var records []metadata.VCSRecord
	if err := json.Unmarshal(vcsGet.Body.Bytes(), &records); err != nil {
		t.Fatalf("decode vcs records: %v", err)
	}
	if len(records) != 1 || records[0].Path != "/Content/Foo.uasset" {
		t.Fatalf("records = %#v", records)
	}

	existsReq := bytes.NewBufferString(`{"hashes":["` + hash + `","` + ioHash([]byte("missing")) + `"]}`)
	existsPost := httptest.NewRecorder()
	existsHTTPReq := httptest.NewRequest(http.MethodPost, "/v2/metadata/exists", existsReq)
	existsHTTPReq.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(existsPost, existsHTTPReq)
	if existsPost.Code != http.StatusOK {
		t.Fatalf("exists status = %d, want %d", existsPost.Code, http.StatusOK)
	}
}

func TestV2MetadataExistsDoesNotRefreshSourceExpiry(t *testing.T) {
	handler, authStore := newV2HandlerWithAuth(t, openMetadata(t), nil, nil)
	token := requestToken(t, handler)

	before, err := authStore.Validate(token)
	if err != nil {
		t.Fatalf("validate before exists: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	existsPost := httptest.NewRecorder()
	existsReq := httptest.NewRequest(http.MethodPost, "/v2/metadata/exists", strings.NewReader(`{"hashes":["`+ioHash([]byte("missing"))+`"]}`))
	existsReq.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(existsPost, existsReq)
	if existsPost.Code != http.StatusOK {
		t.Fatalf("exists status = %d, want %d; body=%q", existsPost.Code, http.StatusOK, existsPost.Body.String())
	}

	after, err := authStore.Validate(token)
	if err != nil {
		t.Fatalf("validate after exists: %v", err)
	}
	if !after.ExpiresAt.Equal(before.ExpiresAt) || !after.ExpiresAt.IsZero() {
		t.Fatalf("source expiry changed: before=%s after=%s", before.ExpiresAt, after.ExpiresAt)
	}
}

func TestV2TusUploadCompletes(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), nil, nil)
	token := requestToken(t, handler)
	payload := []byte("tus-upload-content")
	hash := ioHash(payload)

	create := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+checksum(payload), bytes.NewReader(payload))
	createReq.Header.Set("Authorization", authHeader(token))
	createReq.Header.Set("Tus-Resumable", "1.0.0")
	createReq.Header.Set("Upload-Length", "18")
	handler.ServeHTTP(create, createReq)
	if create.Code != http.StatusCreated {
		t.Fatalf("tus create status = %d, want %d; body=%q", create.Code, http.StatusCreated, create.Body.String())
	}

	pull := httptest.NewRecorder()
	pullReq := httptest.NewRequest(http.MethodGet, "/v2/payload/pull?iohash="+hash, nil)
	pullReq.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(pull, pullReq)
	if pull.Code != http.StatusOK {
		t.Fatalf("pull status = %d, want %d; body=%q", pull.Code, http.StatusOK, pull.Body.String())
	}
	if !bytes.Equal(pull.Body.Bytes(), payload) {
		t.Fatalf("pull body = %q, want %q", pull.Body.Bytes(), payload)
	}
}

func newV2Handler(t *testing.T, meta *metadata.Store, lru *cache.Cache, objects objectstore.Store) http.Handler {
	t.Helper()
	handler, _ := newV2HandlerWithAuth(t, meta, lru, objects)
	return handler
}

func newV2HandlerWithAuth(t *testing.T, meta *metadata.Store, lru *cache.Cache, objects objectstore.Store) (http.Handler, *auth.Store) {
	return newV2HandlerWithAuthAndCacheBytes(t, meta, lru, objects, 64*1024*1024)
}

func newV2HandlerWithAuthAndCacheBytes(t *testing.T, meta *metadata.Store, lru *cache.Cache, objects objectstore.Store, cacheBytes int64) (http.Handler, *auth.Store) {
	t.Helper()
	dir := t.TempDir()
	authStore, err := auth.Open(filepath.Join(dir, "auth.db"), time.Minute)
	if err != nil {
		t.Fatalf("open auth: %v", err)
	}
	t.Cleanup(func() { _ = authStore.Close() })
	uploads, err := uploadsession.Open(filepath.Join(dir, "uploads.db"), filepath.Join(dir, "uploads"), time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("open uploads: %v", err)
	}
	t.Cleanup(func() { _ = uploads.Close() })
	handler := httpapi.NewV2(blobstore.New(dir), objects, meta, lru, authStore, uploads, httpapi.Options{
		AuthExpireTime:        time.Minute,
		UploadDirectMaxBytes:  8 * 1024 * 1024,
		UploadExpiration:      time.Hour,
		UploadCleanupInterval: time.Minute,
		ListenAddr:            "127.0.0.1:8588",
		DataDir:               dir,
		MetadataDB:            filepath.Join(dir, "metadata.db"),
		AuthDB:                filepath.Join(dir, "auth.db"),
		UploadDB:              filepath.Join(dir, "uploads.db"),
		UploadDir:             filepath.Join(dir, "uploads"),
		LogDir:                filepath.Join(dir, "logs"),
		LogRetentionDays:      7,
		StatusLogInterval:     time.Minute,
		CacheBytes:            cacheBytes,
		CacheMaxObjectBytes:   8 * 1024 * 1024,
		CosEndpoint:           "https://cos.example",
		CosRegion:             "test-region",
		CosBucket:             "test-bucket",
		CosPrefix:             "payloads",
		StartedAt:             time.Now().UTC().Add(-time.Second),
	}).HTTPHandler()
	return handler, authStore
}

func requestToken(t *testing.T, handler http.Handler) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v2/auth/request", strings.NewReader(`{"client_id":"test","client_info":{"hostname":"test"}}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("auth request status = %d, want 200; body=%q", response.Code, response.Body.String())
	}
	var got struct {
		Token     string          `json:"token"`
		ExpiresAt json.RawMessage `json:"expires_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode auth response: %v", err)
	}
	if got.Token == "" {
		t.Fatalf("token is empty")
	}
	if string(got.ExpiresAt) != "null" {
		t.Fatalf("source token should have no expiry: %s", got.ExpiresAt)
	}
	return got.Token
}

func authHeader(token string) string {
	return "Bearer " + token
}
