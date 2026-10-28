package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vaps/internal/auth"
	"vaps/internal/blobstore"
	"vaps/internal/httpapi"
)

func TestRequestSourcesCountSuccessAndErrorsWithoutDashboardPolling(t *testing.T) {
	handler, store := newV2HandlerWithAuth(t, openMetadata(t), nil, nil)
	token := requestToken(t, handler)
	for _, path := range []string{"/health", "/v2/payload/pull?iohash=bad", "/dashboard/info.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "bearer "+token)
		req.RemoteAddr = "10.0.0.9:12345"
		req.Header.Set("User-Agent", "source-test")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	for _, value := range []string{"", "unknown"} {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		if value != "" {
			req.Header.Set("Authorization", authHeader(value))
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	query := httptest.NewRecorder()
	handler.ServeHTTP(query, httptest.NewRequest(http.MethodGet, "/dashboard/auth/query?status=tracked", nil))
	var sources struct {
		Items []struct {
			ClientID      string     `json:"client_id"`
			TokenHash     string     `json:"token_hash"`
			Status        string     `json:"status"`
			RequestCount  uint64     `json:"request_count"`
			LastSeenAt    *time.Time `json:"last_seen_at"`
			LastRemoteIP  string     `json:"last_remote_ip"`
			LastUserAgent string     `json:"last_user_agent"`
			ErrorCount    int        `json:"error_count"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(query.Body.Bytes(), &sources); err != nil {
		t.Fatal(err)
	}
	if sources.Total != 1 || len(sources.Items) != 1 {
		t.Fatalf("sources = %#v", sources)
	}
	source := sources.Items[0]
	if source.ClientID != "test" || source.TokenHash != auth.HashToken(token) || source.Status != "tracked" ||
		source.RequestCount != 2 || source.LastSeenAt == nil || source.LastRemoteIP != "10.0.0.9" || source.LastUserAgent != "source-test" || source.ErrorCount != 1 {
		t.Fatalf("source = %#v", source)
	}
	if strings.Contains(query.Body.String(), `"token":`) {
		t.Fatal("dashboard exposed raw source token")
	}
	clear := httptest.NewRecorder()
	handler.ServeHTTP(clear, httptest.NewRequest(http.MethodPost, "/dashboard/auth/clear-expired", nil))
	if clear.Body.String() != "{\"deleted\":0}\n" {
		t.Fatalf("clear expired = %s", clear.Body.String())
	}
	if _, err := store.Validate(token); err != nil {
		t.Fatalf("clear-expired deleted source: %v", err)
	}
	errors := httptest.NewRecorder()
	handler.ServeHTTP(errors, httptest.NewRequest(http.MethodGet, "/dashboard/errors/query?token_hash="+auth.HashToken(token), nil))
	if !strings.Contains(errors.Body.String(), `"client_id":"test"`) || !strings.Contains(errors.Body.String(), `"remote_ip":"10.0.0.9"`) {
		t.Fatalf("error provenance = %s", errors.Body.String())
	}
}

func TestSourceStoreFailureDoesNotBlockPayloadRequests(t *testing.T) {
	handler, store := newV2HandlerWithAuth(t, openMetadata(t), nil, nil)
	token := requestToken(t, handler)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/payload/pull?iohash="+ioHash([]byte("missing")), nil)
	req.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("tracking failure blocked request: %d", response.Code)
	}
}

func TestMissingSourceStoreAndMalformedHeadersDoNotBlockRequests(t *testing.T) {
	handler := httpapi.NewV2(blobstore.New(t.TempDir()), nil, nil, nil, nil, nil, httpapi.Options{}).HTTPHandler()
	for _, header := range []string{"", "Bearer unknown", "Bearer", "Basic abc", "Bearer a b"} {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v2/payload/pull?iohash="+ioHash([]byte("missing")), nil)
		req.Header.Set("Authorization", header)
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusNotFound {
			t.Fatalf("header %q: got %d", header, response.Code)
		}
	}
}
