package reqlog_test

import (
	"testing"
	"time"

	"vaps/internal/reqlog"
)

func TestRecordQueryAndStats(t *testing.T) {
	store := reqlog.New(10)
	store.Record(reqlog.Record{Method: "GET", Path: "/a", Status: 404, Message: "not found"})
	store.Record(reqlog.Record{Method: "POST", Path: "/b", Status: 400, Message: "bad request", TokenHash: "abc"})
	store.Record(reqlog.Record{Method: "GET", Path: "/c", Status: 500, Message: "boom", TokenHash: "abc"})
	store.Record(reqlog.Record{Method: "GET", Path: "/v2/d", Status: 401, Message: "unauthorized", TokenHash: "def"})
	store.Record(reqlog.Record{Method: "GET", Path: "/dashboard/stats", Status: 500, Message: "stats failed"})

	stats := store.Stats()
	if stats.TotalCount != 4 {
		t.Fatalf("panel total_count = %d, want 4", stats.TotalCount)
	}
	if stats.ClientErrors != 3 {
		t.Fatalf("panel client_errors = %d, want 3", stats.ClientErrors)
	}
	if stats.ServerErrors != 1 {
		t.Fatalf("panel server_errors = %d, want 1", stats.ServerErrors)
	}

	allStats := store.StatsFiltered(false)
	if allStats.TotalCount != 5 || allStats.ServerErrors != 2 {
		t.Fatalf("all stats = %#v", allStats)
	}

	result := store.Query(reqlog.Query{TokenHash: "abc", Limit: 10, ExcludeDashboard: true})
	if result.Total != 2 || len(result.Items) != 2 {
		t.Fatalf("token query = %#v, want 2 items", result)
	}

	result = store.Query(reqlog.Query{Limit: 10, ExcludeDashboard: true})
	if result.Total != 4 {
		t.Fatalf("default query total = %d, want 4", result.Total)
	}

	result = store.Query(reqlog.Query{Limit: 10, ExcludeDashboard: false})
	if result.Total != 5 {
		t.Fatalf("include dashboard query total = %d, want 5", result.Total)
	}

	result = store.Query(reqlog.Query{Path: "/v2/d", Limit: 10})
	if result.Total != 1 || result.Items[0].Status != 401 {
		t.Fatalf("path query = %#v", result)
	}

	counts := store.CountByTokenHash()
	if counts["abc"] != 2 || counts["def"] != 1 {
		t.Fatalf("counts = %#v", counts)
	}
}

func TestRecordEvictsOldest(t *testing.T) {
	store := reqlog.New(2)
	store.Record(reqlog.Record{Method: "GET", Path: "/1", Status: 400, At: time.Unix(1, 0).UTC()})
	store.Record(reqlog.Record{Method: "GET", Path: "/2", Status: 400, At: time.Unix(2, 0).UTC()})
	store.Record(reqlog.Record{Method: "GET", Path: "/3", Status: 400, At: time.Unix(3, 0).UTC()})

	result := store.Query(reqlog.Query{Limit: 10})
	if result.Total != 2 {
		t.Fatalf("total = %d, want 2", result.Total)
	}
	if result.Items[0].Path != "/3" || result.Items[1].Path != "/2" {
		t.Fatalf("items = %#v, want /3 then /2", result.Items)
	}
	stats := store.Stats()
	if stats.TotalCount != 3 {
		t.Fatalf("total_count = %d, want 3", stats.TotalCount)
	}
}
