package functional_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"vaps/internal/testserver"
)

func TestFunctionalDashboardPages(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})

	dashboard, err := srv.Client.Get(url(srv.URL, "/dashboard"))
	if err != nil {
		t.Fatalf("GET /dashboard: %v", err)
	}
	requireStatus(t, dashboard, http.StatusOK)
	if !bytes.Contains(readBody(t, dashboard), []byte("VAPS Dashboard")) {
		t.Fatalf("dashboard page missing title")
	}

	metadataPage, err := srv.Client.Get(url(srv.URL, "/dashboard/metadata"))
	if err != nil {
		t.Fatalf("GET /dashboard/metadata: %v", err)
	}
	requireStatus(t, metadataPage, http.StatusOK)
	if !bytes.Contains(readBody(t, metadataPage), []byte("Metadata Browser")) {
		t.Fatalf("metadata dashboard page missing title")
	}
}

func TestFunctionalDashboardStatsAndQuery(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("dashboard-stats")
	hash := ioHash(payload)

	push := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s", hash), token, bytes.NewReader(payload))
	requireStatus(t, push, http.StatusCreated)
	readBody(t, push)

	pull := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, pull, http.StatusOK)
	readBody(t, pull)

	statsResp, err := srv.Client.Get(url(srv.URL, "/dashboard/stats"))
	if err != nil {
		t.Fatalf("GET /dashboard/stats: %v", err)
	}
	requireStatus(t, statsResp, http.StatusOK)
	var stats struct {
		Metadata struct {
			PayloadCount int64 `json:"payload_count"`
		} `json:"metadata"`
		Cache struct {
			Entries int `json:"entries"`
		} `json:"cache"`
	}
	if err := json.Unmarshal(readBody(t, statsResp), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.Metadata.PayloadCount < 1 {
		t.Fatalf("payload_count = %d, want >= 1", stats.Metadata.PayloadCount)
	}

	queryPath := "/dashboard/metadata/query?q=" + strings.ToUpper(hash[:8]) + "&status=cache&min_size=5&max_size=20&limit=10"
	queryResp, err := srv.Client.Get(url(srv.URL, queryPath))
	if err != nil {
		t.Fatalf("GET /dashboard/metadata/query: %v", err)
	}
	requireStatus(t, queryResp, http.StatusOK)
	var query struct {
		Items []struct {
			Hash string `json:"hash"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(readBody(t, queryResp), &query); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if query.Total < 1 || len(query.Items) < 1 || query.Items[0].Hash != hash {
		t.Fatalf("query response = %#v", query)
	}
}
