package functional_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"vaps/internal/testserver"
)

func requireDashboardPage(t *testing.T, client *http.Client, baseURL, path, marker string) {
	t.Helper()
	resp, err := client.Get(url(baseURL, path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	requireStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	if !bytes.Contains(body, []byte(marker)) {
		t.Fatalf("%s page missing %q", path, marker)
	}
}

func TestFunctionalDashboardPages(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})

	requireDashboardPage(t, srv.Client, srv.URL, "/dashboard", "Overview")
	requireDashboardPage(t, srv.Client, srv.URL, "/dashboard/metadata", "Payloads")
	requireDashboardPage(t, srv.Client, srv.URL, "/dashboard/auth", "Auth Browser")
	requireDashboardPage(t, srv.Client, srv.URL, "/dashboard/errors", "Error Log")
	requireDashboardPage(t, srv.Client, srv.URL, "/dashboard/telemetry", "Telemetry")
	requireDashboardPage(t, srv.Client, srv.URL, "/dashboard/info", "Server Info")

	css, err := srv.Client.Get(url(srv.URL, "/dashboard/static/dashboard.css"))
	if err != nil {
		t.Fatalf("GET /dashboard/static/dashboard.css: %v", err)
	}
	requireStatus(t, css, http.StatusOK)
	if !bytes.Contains(readBody(t, css), []byte("--theme_g0")) {
		t.Fatalf("dashboard.css missing theme variables")
	}

	js, err := srv.Client.Get(url(srv.URL, "/dashboard/static/dashboard.js"))
	if err != nil {
		t.Fatalf("GET /dashboard/static/dashboard.js: %v", err)
	}
	requireStatus(t, js, http.StatusOK)
	if !bytes.Contains(readBody(t, js), []byte("buildShell")) {
		t.Fatalf("dashboard.js missing shell builder")
	}
}

func TestFunctionalDashboardInfoJSON(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})

	resp, err := srv.Client.Get(url(srv.URL, "/dashboard/info.json"))
	if err != nil {
		t.Fatalf("GET /dashboard/info.json: %v", err)
	}
	requireStatus(t, resp, http.StatusOK)
	var info struct {
		Version string  `json:"version"`
		Channel string  `json:"channel"`
		UptimeS float64 `json:"uptime_s"`
		Runtime struct {
			GOOS      string `json:"goos"`
			GoVersion string `json:"go_version"`
		} `json:"runtime"`
		Process struct {
			PID int `json:"pid"`
		} `json:"process"`
		Listen struct {
			Addr string `json:"addr"`
		} `json:"listen"`
	}
	if err := json.Unmarshal(readBody(t, resp), &info); err != nil {
		t.Fatalf("decode info.json: %v", err)
	}
	if info.Version == "" || info.Channel == "" {
		t.Fatalf("version/channel empty: %#v", info)
	}
	if info.UptimeS < 0 {
		t.Fatalf("uptime_s = %v, want >= 0", info.UptimeS)
	}
	if info.Runtime.GOOS == "" || info.Runtime.GoVersion == "" {
		t.Fatalf("runtime incomplete: %#v", info.Runtime)
	}
	if info.Process.PID < 1 {
		t.Fatalf("pid = %d, want > 0", info.Process.PID)
	}
	if info.Listen.Addr == "" {
		t.Fatalf("listen addr empty")
	}
}

func TestFunctionalDashboardStatsAndQuery(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("dashboard-stats")
	hash := ioHash(payload)

	push := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s", hash, checksum(payload)), token, bytes.NewReader(payload))
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
		Pull struct {
			Hits   int64 `json:"hits"`
			Misses int64 `json:"misses"`
		} `json:"pull"`
		Auth struct {
			TotalCount  int `json:"total_count"`
			ActiveCount int `json:"active_count"`
		} `json:"auth"`
		HTTP struct {
			Requests struct {
				Count int64 `json:"count"`
			} `json:"requests"`
		} `json:"http"`
	}
	if err := json.Unmarshal(readBody(t, statsResp), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.Metadata.PayloadCount < 1 {
		t.Fatalf("payload_count = %d, want >= 1", stats.Metadata.PayloadCount)
	}
	if stats.Auth.TotalCount < 1 || stats.Auth.ActiveCount < 1 {
		t.Fatalf("auth stats = %#v", stats.Auth)
	}
	if stats.HTTP.Requests.Count < 1 {
		t.Fatalf("http request count = %d, want >= 1", stats.HTTP.Requests.Count)
	}
	if stats.Cache.Entries < 1 {
		t.Fatalf("cache entries = %d, want >= 1 after pull", stats.Cache.Entries)
	}
	if stats.Pull.Hits < 1 {
		t.Fatalf("pull hits = %d, want >= 1 after pull", stats.Pull.Hits)
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

	sortResp, err := srv.Client.Get(url(srv.URL, "/dashboard/metadata/query?sort=size&order=asc&limit=10"))
	if err != nil {
		t.Fatalf("GET sorted metadata query: %v", err)
	}
	requireStatus(t, sortResp, http.StatusOK)
	var sorted struct {
		Items []struct {
			Size int64 `json:"size"`
		} `json:"items"`
	}
	if err := json.Unmarshal(readBody(t, sortResp), &sorted); err != nil {
		t.Fatalf("decode sorted query: %v", err)
	}
	for i := 1; i < len(sorted.Items); i++ {
		if sorted.Items[i].Size < sorted.Items[i-1].Size {
			t.Fatalf("metadata not sorted by size asc: %#v", sorted.Items)
		}
	}
}
