package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vaps/internal/cache"
	"vaps/internal/metadata"
)

func TestHTTPMeterExcludesDashboardPaths(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), nil, nil)

	for i := 0; i < 3; i++ {
		dash := httptest.NewRecorder()
		handler.ServeHTTP(dash, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
		if dash.Code != http.StatusOK {
			t.Fatalf("dashboard status = %d, want 200", dash.Code)
		}
	}
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}

	statsResponse := httptest.NewRecorder()
	handler.ServeHTTP(statsResponse, httptest.NewRequest(http.MethodGet, "/dashboard/stats", nil))
	if statsResponse.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200", statsResponse.Code)
	}
	var stats struct {
		HTTP struct {
			Requests struct {
				Count int64 `json:"count"`
			} `json:"requests"`
		} `json:"http"`
	}
	if err := json.Unmarshal(statsResponse.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.HTTP.Requests.Count != 1 {
		t.Fatalf("http request count = %d, want 1 (health only, dashboard excluded)", stats.HTTP.Requests.Count)
	}
}

func TestDashboardAuthClearEndpoints(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), nil, nil)
	_ = requestToken(t, handler)

	clearExpired := httptest.NewRecorder()
	handler.ServeHTTP(clearExpired, httptest.NewRequest(http.MethodPost, "/dashboard/auth/clear-expired", nil))
	if clearExpired.Code != http.StatusOK {
		t.Fatalf("clear-expired status = %d, want 200", clearExpired.Code)
	}

	clearAll := httptest.NewRecorder()
	handler.ServeHTTP(clearAll, httptest.NewRequest(http.MethodPost, "/dashboard/auth/clear-all", nil))
	if clearAll.Code != http.StatusOK {
		t.Fatalf("clear-all status = %d, want 200", clearAll.Code)
	}
	var result struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(clearAll.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode clear-all: %v", err)
	}
	if result.Deleted < 1 {
		t.Fatalf("clear-all deleted = %d, want >= 1", result.Deleted)
	}

	query := httptest.NewRecorder()
	handler.ServeHTTP(query, httptest.NewRequest(http.MethodGet, "/dashboard/auth/query", nil))
	var authResult struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(query.Body.Bytes(), &authResult); err != nil {
		t.Fatalf("decode auth query: %v", err)
	}
	if authResult.Total != 0 {
		t.Fatalf("auth total after clear-all = %d, want 0", authResult.Total)
	}
}

func TestDashboardAuthAndErrorsPages(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), nil, nil)

	authPage := httptest.NewRecorder()
	handler.ServeHTTP(authPage, httptest.NewRequest(http.MethodGet, "/dashboard/auth", nil))
	if authPage.Code != http.StatusOK {
		t.Fatalf("auth page status = %d, want 200", authPage.Code)
	}
	if !bytes.Contains(authPage.Body.Bytes(), []byte("Auth Browser")) {
		t.Fatalf("auth page missing title")
	}

	errorsPage := httptest.NewRecorder()
	handler.ServeHTTP(errorsPage, httptest.NewRequest(http.MethodGet, "/dashboard/errors", nil))
	if errorsPage.Code != http.StatusOK {
		t.Fatalf("errors page status = %d, want 200", errorsPage.Code)
	}
	if !bytes.Contains(errorsPage.Body.Bytes(), []byte("Error Log")) {
		t.Fatalf("errors page missing title")
	}
}

func TestDashboardSessionsPageAndQueries(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), nil, nil)

	for _, path := range []string{
		"/dashboard/sessions",
		"/dashboard/?page=sessions&status=all",
	} {
		page := httptest.NewRecorder()
		handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, path, nil))
		if page.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, page.Code)
		}
		if !bytes.Contains(page.Body.Bytes(), []byte("Runtime Sessions")) {
			t.Fatalf("%s missing sessions title", path)
		}
	}

	sessions := httptest.NewRecorder()
	handler.ServeHTTP(sessions, httptest.NewRequest(http.MethodGet, "/dashboard/sessions/query?status=all", nil))
	if sessions.Code != http.StatusOK {
		t.Fatalf("sessions query status = %d, want 200", sessions.Code)
	}
	var sessionResult struct {
		Items []any `json:"items"`
		Total int   `json:"total"`
	}
	if err := json.Unmarshal(sessions.Body.Bytes(), &sessionResult); err != nil {
		t.Fatalf("decode sessions query: %v", err)
	}
	if sessionResult.Total != 0 || len(sessionResult.Items) != 0 {
		t.Fatalf("empty sessions result = %#v", sessionResult)
	}

	badStatus := httptest.NewRecorder()
	handler.ServeHTTP(badStatus, httptest.NewRequest(http.MethodGet, "/dashboard/sessions/query?status=unknown", nil))
	if badStatus.Code != http.StatusBadRequest {
		t.Fatalf("bad sessions status = %d, want 400", badStatus.Code)
	}

	missingID := httptest.NewRecorder()
	handler.ServeHTTP(missingID, httptest.NewRequest(http.MethodGet, "/dashboard/sessions/log", nil))
	if missingID.Code != http.StatusBadRequest {
		t.Fatalf("missing session id status = %d, want 400", missingID.Code)
	}
}

func TestDashboardSharedShellAssets(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), nil, nil)

	css := httptest.NewRecorder()
	handler.ServeHTTP(css, httptest.NewRequest(http.MethodGet, "/dashboard/static/dashboard.css", nil))
	if css.Code != http.StatusOK {
		t.Fatalf("dashboard.css status = %d, want 200", css.Code)
	}
	if ct := css.Header().Get("Content-Type"); !bytes.Contains([]byte(ct), []byte("text/css")) {
		t.Fatalf("dashboard.css content-type = %q, want text/css", ct)
	}
	if !bytes.Contains(css.Body.Bytes(), []byte("--theme_g4")) {
		t.Fatalf("dashboard.css missing theme tokens")
	}
	if !bytes.Contains(css.Body.Bytes(), []byte("details \\203A")) && !bytes.Contains(css.Body.Bytes(), []byte(`details \203A`)) {
		t.Fatalf("dashboard.css missing details affordance")
	}

	js := httptest.NewRecorder()
	handler.ServeHTTP(js, httptest.NewRequest(http.MethodGet, "/dashboard/static/dashboard.js", nil))
	if js.Code != http.StatusOK {
		t.Fatalf("dashboard.js status = %d, want 200", js.Code)
	}
	if ct := js.Header().Get("Content-Type"); !bytes.Contains([]byte(ct), []byte("javascript")) {
		t.Fatalf("dashboard.js content-type = %q, want javascript", ct)
	}
	if !bytes.Contains(js.Body.Bytes(), []byte("vaps-theme")) {
		t.Fatalf("dashboard.js missing theme persistence key")
	}
	if !bytes.Contains(js.Body.Bytes(), []byte("Telemetry")) {
		t.Fatalf("dashboard.js missing Telemetry nav label")
	}
	if !bytes.Contains(js.Body.Bytes(), []byte("Payloads")) {
		t.Fatalf("dashboard.js missing Payloads nav label")
	}
	if !bytes.Contains(js.Body.Bytes(), []byte("Sessions")) {
		t.Fatalf("dashboard.js missing Sessions nav label")
	}
	if !bytes.Contains(js.Body.Bytes(), []byte("Info")) {
		t.Fatalf("dashboard.js missing Info nav label")
	}
	navOrder := []string{
		`label: "Overview"`,
		`label: "Errors"`,
		`label: "Payloads"`,
		`label: "Auth"`,
		`label: "Telemetry"`,
		`label: "Info"`,
	}
	jsBody := js.Body.Bytes()
	prev := -1
	for _, marker := range navOrder {
		idx := bytes.Index(jsBody, []byte(marker))
		if idx < 0 {
			t.Fatalf("dashboard.js missing nav marker %q", marker)
		}
		if idx <= prev {
			t.Fatalf("dashboard.js nav order incorrect around %q", marker)
		}
		prev = idx
	}

	for _, path := range []string{"/dashboard", "/dashboard/sessions", "/dashboard/metadata", "/dashboard/auth", "/dashboard/errors", "/dashboard/telemetry", "/dashboard/info"} {
		page := httptest.NewRecorder()
		handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, path, nil))
		if page.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, page.Code)
		}
		body := page.Body.Bytes()
		if !bytes.Contains(body, []byte("/dashboard/static/dashboard.css")) {
			t.Fatalf("%s missing shared css link", path)
		}
		if !bytes.Contains(body, []byte("/dashboard/static/dashboard.js")) {
			t.Fatalf("%s missing shared js script", path)
		}
		if !bytes.Contains(body, []byte(`data-page=`)) {
			t.Fatalf("%s missing data-page shell marker", path)
		}
	}

	overview := httptest.NewRecorder()
	handler.ServeHTTP(overview, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if !bytes.Contains(overview.Body.Bytes(), []byte("stats-http-panel")) {
		t.Fatalf("overview missing HTTP panel")
	}
	if !bytes.Contains(overview.Body.Bytes(), []byte("/dashboard/telemetry")) {
		t.Fatalf("overview missing telemetry link")
	}
	if !bytes.Contains(overview.Body.Bytes(), []byte("/dashboard/?page=sessions&status=all")) {
		t.Fatalf("overview missing sessions link")
	}
}

func TestDashboardStatsIncludesAuthAndErrors(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), nil, nil)
	token := requestToken(t, handler)

	badPull := httptest.NewRecorder()
	badPullReq := httptest.NewRequest(http.MethodGet, "/v2/payload/pull?iohash=not-a-valid-hash", nil)
	badPullReq.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(badPull, badPullReq)
	if badPull.Code != http.StatusBadRequest {
		t.Fatalf("bad pull status = %d, want 400", badPull.Code)
	}

	badRequest := httptest.NewRecorder()
	handler.ServeHTTP(badRequest, httptest.NewRequest(http.MethodPost, "/v2/auth/request", bytes.NewReader([]byte(`{"client_id":""}`))))
	if badRequest.Code != http.StatusBadRequest {
		t.Fatalf("bad request status = %d, want 400", badRequest.Code)
	}

	statsResponse := httptest.NewRecorder()
	handler.ServeHTTP(statsResponse, httptest.NewRequest(http.MethodGet, "/dashboard/stats", nil))
	if statsResponse.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200", statsResponse.Code)
	}
	var stats struct {
		Auth struct {
			TotalCount  int `json:"total_count"`
			ActiveCount int `json:"active_count"`
		} `json:"auth"`
		Errors struct {
			TotalCount   int64 `json:"total_count"`
			ClientErrors int64 `json:"client_errors"`
		} `json:"errors"`
		HTTP struct {
			Requests struct {
				Count  int64 `json:"count"`
				Active int64 `json:"active"`
			} `json:"requests"`
		} `json:"http"`
	}
	if err := json.Unmarshal(statsResponse.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.Auth.TotalCount < 1 || stats.Auth.ActiveCount < 1 {
		t.Fatalf("auth stats = %#v", stats.Auth)
	}
	if stats.Errors.TotalCount < 2 || stats.Errors.ClientErrors < 2 {
		t.Fatalf("error stats = %#v", stats.Errors)
	}
	if stats.HTTP.Requests.Count < 1 {
		t.Fatalf("http request count = %d, want >= 1", stats.HTTP.Requests.Count)
	}

	authQuery := httptest.NewRecorder()
	handler.ServeHTTP(authQuery, httptest.NewRequest(http.MethodGet, "/dashboard/auth/query?status=active", nil))
	if authQuery.Code != http.StatusOK {
		t.Fatalf("auth query status = %d, want 200", authQuery.Code)
	}
	var authResult struct {
		Items []struct {
			ClientID      string `json:"client_id"`
			ErrorCount    int64  `json:"error_count"`
			TokenHash     string `json:"token_hash"`
			UserAgentInfo struct {
				Parsed  bool   `json:"parsed"`
				Project string `json:"project"`
				System  string `json:"system"`
			} `json:"user_agent_info"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(authQuery.Body.Bytes(), &authResult); err != nil {
		t.Fatalf("decode auth query: %v", err)
	}
	if authResult.Total < 1 {
		t.Fatalf("auth query total = %d, want >= 1", authResult.Total)
	}

	tokenHash := authResult.Items[0].TokenHash
	errorsQuery := httptest.NewRecorder()
	handler.ServeHTTP(errorsQuery, httptest.NewRequest(http.MethodGet, "/dashboard/errors/query?token_hash="+tokenHash, nil))
	if errorsQuery.Code != http.StatusOK {
		t.Fatalf("errors query status = %d, want 200", errorsQuery.Code)
	}
	var errorResult struct {
		Total int64 `json:"total"`
		Items []struct {
			Status int `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(errorsQuery.Body.Bytes(), &errorResult); err != nil {
		t.Fatalf("decode errors query: %v", err)
	}
	if errorResult.Total < 1 {
		t.Fatalf("errors query total = %d, want >= 1", errorResult.Total)
	}
}

func TestDashboardInfoEndpoint(t *testing.T) {
	objects := newMemoryObjectStore()
	objects.objects["test-object"] = memoryObject{data: []byte("payload")}
	handler := newV2Handler(t, openMetadata(t), nil, objects)

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/dashboard/info", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("info page status = %d, want 200", page.Code)
	}
	if !bytes.Contains(page.Body.Bytes(), []byte(`data-page="info"`)) {
		t.Fatalf("info page missing data-page marker")
	}
	if !bytes.Contains(page.Body.Bytes(), []byte("Server Info")) {
		t.Fatalf("info page missing Server Info title")
	}

	infoResponse := httptest.NewRecorder()
	handler.ServeHTTP(infoResponse, httptest.NewRequest(http.MethodGet, "/dashboard/info.json", nil))
	if infoResponse.Code != http.StatusOK {
		t.Fatalf("info json status = %d, want 200", infoResponse.Code)
	}
	var info struct {
		Version   string  `json:"version"`
		Channel   string  `json:"channel"`
		StartedAt string  `json:"started_at"`
		UptimeS   float64 `json:"uptime_s"`
		Runtime   struct {
			GOOS      string `json:"goos"`
			GOARCH    string `json:"goarch"`
			GoVersion string `json:"go_version"`
			NumCPU    int    `json:"num_cpu"`
			DataDisk  struct {
				TotalBytes uint64 `json:"total_bytes"`
				FreeBytes  uint64 `json:"free_bytes"`
			} `json:"data_disk"`
		} `json:"runtime"`
		Process struct {
			PID int `json:"pid"`
		} `json:"process"`
		Listen struct {
			Addr string `json:"addr"`
		} `json:"listen"`
		Paths  map[string]json.RawMessage `json:"paths"`
		Config struct {
			CacheBytes           int64  `json:"cache_bytes"`
			AuthExpireTime       string `json:"auth_expire_time"`
			UploadDirectMaxBytes int64  `json:"upload_direct_max_bytes"`
			InfoRefreshInterval  string `json:"info_refresh_interval"`
			COS                  struct {
				Endpoint    string `json:"endpoint"`
				Region      string `json:"region"`
				Bucket      string `json:"bucket"`
				Prefix      string `json:"prefix"`
				TotalBytes  int64  `json:"total_bytes"`
				ObjectCount int64  `json:"object_count"`
			} `json:"cos"`
		} `json:"config"`
	}
	if err := json.Unmarshal(infoResponse.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if info.Version == "" || info.Channel == "" {
		t.Fatalf("version/channel empty: %#v", info)
	}
	if info.StartedAt == "" || info.UptimeS < 0 {
		t.Fatalf("started/uptime missing: %#v", info)
	}
	if info.Runtime.GOOS == "" || info.Runtime.GOARCH == "" || info.Runtime.GoVersion == "" || info.Runtime.NumCPU < 1 {
		t.Fatalf("runtime incomplete: %#v", info.Runtime)
	}
	if info.Runtime.DataDisk.TotalBytes == 0 || info.Runtime.DataDisk.FreeBytes > info.Runtime.DataDisk.TotalBytes {
		t.Fatalf("data disk incomplete: %#v", info.Runtime.DataDisk)
	}
	if info.Process.PID < 1 {
		t.Fatalf("pid = %d, want > 0", info.Process.PID)
	}
	if info.Listen.Addr == "" {
		t.Fatalf("listen addr empty")
	}
	if len(info.Paths["data_dir"]) == 0 {
		t.Fatalf("data_dir missing")
	}
	for _, name := range []string{"metadata_db", "auth_db", "upload_db"} {
		if _, ok := info.Paths[name]; ok {
			t.Fatalf("paths should not expose %s", name)
		}
	}
	if info.Config.CacheBytes <= 0 ||
		info.Config.AuthExpireTime == "" ||
		info.Config.UploadDirectMaxBytes <= 0 ||
		info.Config.InfoRefreshInterval != "5s" {
		t.Fatalf("config incomplete: %#v", info.Config)
	}
	if info.Config.COS.Endpoint != "https://cos.example" ||
		info.Config.COS.Region != "test-region" ||
		info.Config.COS.Bucket != "test-bucket" ||
		info.Config.COS.Prefix != "payloads" ||
		info.Config.COS.TotalBytes != int64(len("payload")) ||
		info.Config.COS.ObjectCount != 1 {
		t.Fatalf("COS config/stats incomplete: %#v", info.Config.COS)
	}
}

func TestDashboardHidesDisabledCache(t *testing.T) {
	handler, _ := newV2HandlerWithAuthAndCacheBytes(t, openMetadata(t), cache.New(0, 0), nil, 0)

	statsResponse := httptest.NewRecorder()
	handler.ServeHTTP(statsResponse, httptest.NewRequest(http.MethodGet, "/dashboard/stats", nil))
	if statsResponse.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200", statsResponse.Code)
	}
	var stats map[string]json.RawMessage
	if err := json.Unmarshal(statsResponse.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if _, ok := stats["cache"]; ok {
		t.Fatalf("disabled cache should be omitted from dashboard stats")
	}

	infoResponse := httptest.NewRecorder()
	handler.ServeHTTP(infoResponse, httptest.NewRequest(http.MethodGet, "/dashboard/info.json", nil))
	if infoResponse.Code != http.StatusOK {
		t.Fatalf("info status = %d, want 200", infoResponse.Code)
	}
	var info struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(infoResponse.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if _, ok := info.Config["cache_bytes"]; ok {
		t.Fatalf("disabled cache bytes should be omitted from dashboard info")
	}

	overview := httptest.NewRecorder()
	handler.ServeHTTP(overview, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if !bytes.Contains(overview.Body.Bytes(), []byte(`id="cache-details" hidden`)) {
		t.Fatalf("overview should hide disabled cache details")
	}
}

func TestDashboardMetadataQuerySorts(t *testing.T) {
	meta := openMetadata(t)
	handler := newV2Handler(t, meta, nil, nil)

	records := []struct {
		hash string
		size int64
		at   int64
	}{
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 5, 1},
		{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 20, 2},
		{"cccccccccccccccccccccccccccccccccccccccc", 10, 3},
	}
	for _, record := range records {
		created := time.Unix(record.at, 0).UTC()
		if err := meta.PutPayload(metadata.Payload{
			Hash:      record.hash,
			Checksum:  "01020304",
			Size:      record.size,
			Status:    metadata.StatusCached,
			CreatedAt: &created,
		}); err != nil {
			t.Fatalf("PutPayload: %v", err)
		}
	}

	defaultQuery := httptest.NewRecorder()
	handler.ServeHTTP(defaultQuery, httptest.NewRequest(http.MethodGet, "/dashboard/metadata/query?limit=10", nil))
	if defaultQuery.Code != http.StatusOK {
		t.Fatalf("default query status = %d, want 200; body=%q", defaultQuery.Code, defaultQuery.Body.String())
	}
	var defaultResult struct {
		Items []struct {
			Hash        string `json:"hash"`
			Checksum    string `json:"checksum"`
			StatusLabel string `json:"status_label"`
			StatusFlags struct {
				Local  bool `json:"local"`
				Cache  bool `json:"cache"`
				Backup bool `json:"backup"`
			} `json:"status_flags"`
		} `json:"items"`
	}
	if err := json.Unmarshal(defaultQuery.Body.Bytes(), &defaultResult); err != nil {
		t.Fatalf("decode default query: %v", err)
	}
	if len(defaultResult.Items) != 3 || defaultResult.Items[0].Hash != records[2].hash {
		t.Fatalf("default sort items = %#v, want created desc starting with %s", defaultResult.Items, records[2].hash)
	}
	if defaultResult.Items[0].Checksum != "01020304" ||
		defaultResult.Items[0].StatusLabel != "cached" ||
		defaultResult.Items[0].StatusFlags.Local ||
		defaultResult.Items[0].StatusFlags.Cache ||
		defaultResult.Items[0].StatusFlags.Backup {
		t.Fatalf("metadata status = %#v", defaultResult.Items[0])
	}

	sizeAsc := httptest.NewRecorder()
	handler.ServeHTTP(sizeAsc, httptest.NewRequest(http.MethodGet, "/dashboard/metadata/query?sort=size&order=asc&limit=10", nil))
	if sizeAsc.Code != http.StatusOK {
		t.Fatalf("size asc status = %d, want 200; body=%q", sizeAsc.Code, sizeAsc.Body.String())
	}
	var sizeResult struct {
		Items []struct {
			Hash string `json:"hash"`
			Size int64  `json:"size"`
		} `json:"items"`
	}
	if err := json.Unmarshal(sizeAsc.Body.Bytes(), &sizeResult); err != nil {
		t.Fatalf("decode size asc: %v", err)
	}
	if len(sizeResult.Items) != 3 || sizeResult.Items[0].Size != 5 || sizeResult.Items[2].Size != 20 {
		t.Fatalf("size asc items = %#v", sizeResult.Items)
	}

	badSort := httptest.NewRecorder()
	handler.ServeHTTP(badSort, httptest.NewRequest(http.MethodGet, "/dashboard/metadata/query?sort=hash", nil))
	if badSort.Code != http.StatusBadRequest {
		t.Fatalf("bad sort status = %d, want 400", badSort.Code)
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/dashboard/metadata", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("metadata page status = %d, want 200", page.Code)
	}
	if !bytes.Contains(page.Body.Bytes(), []byte(`data-sort="size"`)) || !bytes.Contains(page.Body.Bytes(), []byte(`data-sort="created"`)) {
		t.Fatalf("metadata page missing sortable headers")
	}
	if !bytes.Contains(page.Body.Bytes(), []byte("Payload Metadata")) {
		t.Fatalf("metadata page missing payload detail section")
	}
}

func TestErrorRecorderSkipsNonAPIPathsAndDashboardFilter(t *testing.T) {
	handler := newV2Handler(t, openMetadata(t), nil, nil)

	favicon := httptest.NewRecorder()
	handler.ServeHTTP(favicon, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	if favicon.Code != http.StatusNotFound {
		t.Fatalf("favicon status = %d, want 404", favicon.Code)
	}

	badQuery := httptest.NewRecorder()
	handler.ServeHTTP(badQuery, httptest.NewRequest(http.MethodGet, "/dashboard/metadata/query?limit=-1", nil))
	if badQuery.Code != http.StatusBadRequest {
		t.Fatalf("dashboard query status = %d, want 400", badQuery.Code)
	}

	statsResponse := httptest.NewRecorder()
	handler.ServeHTTP(statsResponse, httptest.NewRequest(http.MethodGet, "/dashboard/stats", nil))
	if statsResponse.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200", statsResponse.Code)
	}
	var stats struct {
		Errors struct {
			TotalCount int64 `json:"total_count"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(statsResponse.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.Errors.TotalCount != 0 {
		t.Fatalf("panel error total = %d, want 0", stats.Errors.TotalCount)
	}

	defaultQuery := httptest.NewRecorder()
	handler.ServeHTTP(defaultQuery, httptest.NewRequest(http.MethodGet, "/dashboard/errors/query", nil))
	if defaultQuery.Code != http.StatusOK {
		t.Fatalf("errors query status = %d, want 200", defaultQuery.Code)
	}
	var defaultResult struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(defaultQuery.Body.Bytes(), &defaultResult); err != nil {
		t.Fatalf("decode default errors query: %v", err)
	}
	if defaultResult.Total != 0 {
		t.Fatalf("default errors query total = %d, want 0", defaultResult.Total)
	}

	includeDashboard := httptest.NewRecorder()
	handler.ServeHTTP(includeDashboard, httptest.NewRequest(http.MethodGet, "/dashboard/errors/query?include_dashboard=1", nil))
	if includeDashboard.Code != http.StatusOK {
		t.Fatalf("include dashboard query status = %d, want 200", includeDashboard.Code)
	}
	var includeResult struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(includeDashboard.Body.Bytes(), &includeResult); err != nil {
		t.Fatalf("decode include dashboard query: %v", err)
	}
	if includeResult.Total != 1 {
		t.Fatalf("include dashboard query total = %d, want 1", includeResult.Total)
	}
}
