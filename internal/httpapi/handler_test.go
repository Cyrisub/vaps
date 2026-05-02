package httpapi_test

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"vaps/internal/blobstore"
	"vaps/internal/httpapi"
	"vaps/internal/metadata"
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

func TestPutHeadAndGetPayload(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	payload := []byte("hello")
	hash := ioHash(payload)

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/payload?hash="+hash, bytes.NewReader(payload)))
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d; body=%q", putResponse.Code, http.StatusCreated, putResponse.Body.String())
	}

	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, httptest.NewRequest(http.MethodHead, "/v1/payload?hash="+hash, nil))
	if headResponse.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", headResponse.Code, http.StatusOK)
	}
	if headResponse.Header().Get("ETag") != `"`+hash+`"` {
		t.Fatalf("HEAD ETag = %q, want quoted hash", headResponse.Header().Get("ETag"))
	}
	if headResponse.Header().Get("Content-Length") != "5" {
		t.Fatalf("HEAD Content-Length = %q, want 5", headResponse.Header().Get("Content-Length"))
	}

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/v1/payload?hash="+hash, nil))
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
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/v1/payload?hash="+ioHash([]byte("hello")), bytes.NewReader([]byte("goodbye"))))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestPutExistingPayloadReturnsStoredTrue(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	payload := []byte("hello")
	hash := ioHash(payload)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPut, "/v1/payload?hash="+hash, bytes.NewReader(payload)))
	if first.Code != http.StatusCreated {
		t.Fatalf("first PUT status = %d, want %d", first.Code, http.StatusCreated)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPut, "/v1/payload?hash="+hash, bytes.NewReader(payload)))
	if second.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, want %d", second.Code, http.StatusOK)
	}

	var got putResponse
	if err := json.Unmarshal(second.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Stored {
		t.Fatalf("Stored = false, want true for existing payload")
	}
}

func TestMissingPayloadReturnsNotFound(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	hash := ioHash([]byte("missing"))

	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, httptest.NewRequest(http.MethodHead, "/v1/payload?hash="+hash, nil))
	if headResponse.Code != http.StatusNotFound {
		t.Fatalf("HEAD status = %d, want %d", headResponse.Code, http.StatusNotFound)
	}

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/v1/payload?hash="+hash, nil))
	if getResponse.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want %d", getResponse.Code, http.StatusNotFound)
	}
}

func TestExistsReturnsMapByHash(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	existing := ioHash([]byte("hello"))
	missing := ioHash([]byte("missing"))

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/payload?hash="+existing, bytes.NewReader([]byte("hello"))))
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d", putResponse.Code, http.StatusCreated)
	}

	requestBody := bytes.NewBufferString(`{"hashes":["` + existing + `","` + missing + `"]}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/payload/exists", requestBody))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", response.Code, http.StatusOK, response.Body.String())
	}

	var got existsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Items[existing].Exists {
		t.Fatalf("existing hash marked missing")
	}
	if got.Items[existing].Size != 5 {
		t.Fatalf("existing size = %d, want 5", got.Items[existing].Size)
	}
	if got.Items[missing].Exists {
		t.Fatalf("missing hash marked existing")
	}
}

func TestInvalidHashReturnsBadRequest(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/payload?hash=bad", nil))

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
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/v1/payload?hash="+hash, bytes.NewReader(payload)))
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
	if record.Size != int64(len(payload)) {
		t.Fatalf("metadata size = %d, want %d", record.Size, len(payload))
	}
	if record.Local != metadata.LocalCommitted {
		t.Fatalf("metadata local state = %q, want %q", record.Local, metadata.LocalCommitted)
	}
}

func TestHeadReturnsServerErrorWhenMetadataExistsButLocalFileMissing(t *testing.T) {
	store := blobstore.New(t.TempDir())
	meta := openMetadata(t)
	handler := httpapi.NewWithMetadata(store, meta)
	payload := []byte("hello")
	hash := ioHash(payload)

	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/v1/payload?hash="+hash, bytes.NewReader(payload)))
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
	handler.ServeHTTP(headResponse, httptest.NewRequest(http.MethodHead, "/v1/payload?hash="+hash, nil))
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
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/payload?hash="+hash, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

type existsResponse struct {
	Items map[string]existsItem `json:"items"`
}

type existsItem struct {
	Exists bool  `json:"exists"`
	Size   int64 `json:"size,omitempty"`
}

type putResponse struct {
	Hash   string `json:"hash"`
	Size   int64  `json:"size"`
	Stored bool   `json:"stored"`
}

func ioHash(payload []byte) string {
	sum := sha1.Sum(payload)
	return hex.EncodeToString(sum[:])
}

func openMetadata(t *testing.T) *metadata.Store {
	t.Helper()

	store, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close metadata: %v", err)
		}
	})
	return store
}
