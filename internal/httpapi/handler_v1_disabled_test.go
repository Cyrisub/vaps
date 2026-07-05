//go:build !vaps_legacy_v1

package httpapi_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"vaps/internal/blobstore"
	"vaps/internal/httpapi"
)

func TestV1RoutesAreDisabledByDefault(t *testing.T) {
	handler := httpapi.New(blobstore.New(t.TempDir()))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/v1/payload?iohash="+ioHash([]byte("hello")), bytes.NewReader([]byte("hello"))))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
