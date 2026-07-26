package logreader_test

import (
	"os"
	"path/filepath"
	"testing"

	"vaps/internal/logreader"
)

func TestReaderGroupsSessionsAcrossDailyFiles(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "vaps-2026-07-25.log", ""+
		"2026/07/25 23:59:58 === vaps startup started_at=2026-07-25T23:59:58Z ===\n"+
		"2026/07/25 23:59:59 listening\n")
	writeLog(t, dir, "vaps-2026-07-26.log", ""+
		"2026/07/26 00:00:01 upload failed error=\"bad hash\"\n"+
		"2026/07/26 00:00:02 vaps stopped exit_code=0\n"+
		"2026/07/26 01:00:00 === vaps startup started_at=2026-07-26T01:00:00Z ===\n"+
		"2026/07/26 01:00:01 status payload_count=1\n")

	reader := logreader.New(dir)
	sessions, err := reader.Sessions(logreader.Query{Status: "all", Limit: 10})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions.Items) != 2 || sessions.Items[0].Status != "active" || sessions.Items[1].Status != "ended" {
		t.Fatalf("sessions = %#v", sessions.Items)
	}
	if sessions.Items[1].LogCount != 4 {
		t.Fatalf("ended log count = %d, want 4", sessions.Items[1].LogCount)
	}

	logs, err := reader.Log(logreader.Query{SessionID: sessions.Items[1].ID, Level: "error", Limit: 10})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(logs.Items) != 1 || logs.Items[0].Level != "ERROR" {
		t.Fatalf("error logs = %#v", logs.Items)
	}
}

func TestReaderQueryPaginationAndInvalidDirectory(t *testing.T) {
	reader := logreader.New(filepath.Join(t.TempDir(), "missing"))
	result, err := reader.Sessions(logreader.Query{Status: "active", Limit: 1, Offset: 3})
	if err != nil {
		t.Fatalf("Sessions missing directory: %v", err)
	}
	if result.Total != 0 || len(result.Items) != 0 {
		t.Fatalf("empty result = %#v", result)
	}
}

func writeLog(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}
