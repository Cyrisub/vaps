package appconfig_test

import (
	"path/filepath"
	"testing"

	"vaps/internal/appconfig"
)

func TestFromArgsUsesDefaults(t *testing.T) {
	binDir := t.TempDir()
	cfg, err := appconfig.FromArgsWithBaseDir(nil, binDir)
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.Addr != ":8588" {
		t.Fatalf("Addr = %q, want :8588", cfg.Addr)
	}
	if cfg.DataDir != filepath.Join(binDir, "data") {
		t.Fatalf("DataDir = %q, want data under binary dir", cfg.DataDir)
	}
	if cfg.MetadataDB != filepath.Join(binDir, "data", "metadata.db") {
		t.Fatalf("MetadataDB = %q, want data/metadata.db under binary dir", cfg.MetadataDB)
	}
}

func TestFromArgsUsesFlags(t *testing.T) {
	cfg, err := appconfig.FromArgsWithBaseDir([]string{"-addr", "127.0.0.1:9000", "-data-dir", "/tmp/vaps"}, t.TempDir())
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Fatalf("Addr = %q, want 127.0.0.1:9000", cfg.Addr)
	}
	if cfg.DataDir != "/tmp/vaps" {
		t.Fatalf("DataDir = %q, want /tmp/vaps", cfg.DataDir)
	}
	if cfg.MetadataDB != "/tmp/vaps/metadata.db" {
		t.Fatalf("MetadataDB = %q, want /tmp/vaps/metadata.db", cfg.MetadataDB)
	}
}

func TestFromArgsAllowsMetadataDBOverride(t *testing.T) {
	cfg, err := appconfig.FromArgsWithBaseDir([]string{"-metadata-db", "/tmp/custom.db"}, t.TempDir())
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.MetadataDB != "/tmp/custom.db" {
		t.Fatalf("MetadataDB = %q, want /tmp/custom.db", cfg.MetadataDB)
	}
}

func TestFromArgsWithBaseDirResolvesRelativePathsFromBinaryDir(t *testing.T) {
	binDir := t.TempDir()

	cfg, err := appconfig.FromArgsWithBaseDir([]string{"-data-dir", "payloads", "-metadata-db", "meta/vaps.db"}, binDir)
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}

	wantDataDir := filepath.Join(binDir, "payloads")
	if cfg.DataDir != wantDataDir {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
	wantMetadataDB := filepath.Join(binDir, "meta", "vaps.db")
	if cfg.MetadataDB != wantMetadataDB {
		t.Fatalf("MetadataDB = %q, want %q", cfg.MetadataDB, wantMetadataDB)
	}
}

func TestFromArgsWithBaseDirKeepsAbsolutePaths(t *testing.T) {
	cfg, err := appconfig.FromArgsWithBaseDir([]string{"-data-dir", "/tmp/vaps", "-metadata-db", "/tmp/custom.db"}, t.TempDir())
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.DataDir != "/tmp/vaps" {
		t.Fatalf("DataDir = %q, want /tmp/vaps", cfg.DataDir)
	}
	if cfg.MetadataDB != "/tmp/custom.db" {
		t.Fatalf("MetadataDB = %q, want /tmp/custom.db", cfg.MetadataDB)
	}
}
