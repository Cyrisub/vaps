package appconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vaps/internal/appconfig"
	"vaps/internal/version"
)

func TestFromArgsUsesStorageDefaults(t *testing.T) {
	binDir := t.TempDir()
	cfg, err := appconfig.FromArgsWithBaseDir(nil, binDir)
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.Storage.S3.Bucket != "vaps-payloads" || cfg.Storage.S3.Region != "us-east-1" {
		t.Fatalf("Storage.S3 = %#v", cfg.Storage.S3)
	}
	if cfg.Storage.S3.Prefix != "payloads" {
		t.Fatalf("Storage.S3.Prefix = %q", cfg.Storage.S3.Prefix)
	}
	if cfg.Storage.Local.Dir != filepath.Join(binDir, "data") {
		t.Fatalf("Storage.Local.Dir = %q", cfg.Storage.Local.Dir)
	}
	if cfg.Storage.Local.MaxCacheBytes.Int64() != 100*1024*1024*1024 {
		t.Fatalf("Storage.Local.MaxCacheBytes = %d", cfg.Storage.Local.MaxCacheBytes)
	}
}

func TestFromArgsLoadsAndOverridesStorageConfig(t *testing.T) {
	binDir := t.TempDir()
	configPath := writeConfig(t, binDir, `
[storage.s3]
bucket = "test-bucket"
region = "ap-southeast-1"
endpoint = "https://minio.example"
prefix = "/vaps/"
force_path_style = true

[storage.local]
dir = "cache"
max_cache_bytes = "2MiB"
`)
	cfg, err := appconfig.FromArgsWithBaseDir([]string{
		"--config", configPath,
		"--storage-s3-bucket", "override-bucket",
		"--storage-local-max-cache-bytes", "3MiB",
	}, binDir)
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.Storage.S3.Bucket != "override-bucket" || cfg.Storage.S3.Prefix != "vaps" || !cfg.Storage.S3.ForcePathStyle {
		t.Fatalf("Storage.S3 = %#v", cfg.Storage.S3)
	}
	if cfg.Storage.Local.Dir != filepath.Join(binDir, "cache") || cfg.Storage.Local.MaxCacheBytes.Int64() != 3*1024*1024 {
		t.Fatalf("Storage.Local = %#v", cfg.Storage.Local)
	}
}

func TestFromArgsRejectsMissingS3Configuration(t *testing.T) {
	for _, content := range []string{
		"[storage.s3]\nbucket = \"\"",
		"[storage.s3]\nregion = \"\"",
		"[storage.local]\ndir = \"\"",
	} {
		dir := t.TempDir()
		_, err := appconfig.FromArgsWithBaseDir([]string{"--config", writeConfig(t, dir, content)}, dir)
		if err == nil {
			t.Fatalf("FromArgsWithBaseDir(%q) error = nil", content)
		}
	}
}

func TestFromArgsRejectsLegacyBackupConfiguration(t *testing.T) {
	_, err := appconfig.FromArgsWithBaseDir([]string{"--backup-backend", "svn"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error = %v, want unknown legacy backup flag", err)
	}
}

func TestFromArgsResolvesPathsAndKeepsExistingOptions(t *testing.T) {
	binDir := t.TempDir()
	cfg, err := appconfig.FromArgsWithBaseDir([]string{
		"--data-dir", "payloads",
		"--metadata-db", "meta/vaps.db",
		"--storage-local-dir", "cache",
		"--cache-bytes", "1MiB",
		"--upload-direct-max-bytes", "2MiB",
	}, binDir)
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.DataDir != filepath.Join(binDir, "payloads") || cfg.MetadataDB != filepath.Join(binDir, "meta", "vaps.db") {
		t.Fatalf("paths = data:%q metadata:%q", cfg.DataDir, cfg.MetadataDB)
	}
	if cfg.Storage.Local.Dir != filepath.Join(binDir, "cache") {
		t.Fatalf("Storage.Local.Dir = %q", cfg.Storage.Local.Dir)
	}
	if cfg.Cache.Bytes.Int64() != 1024*1024 || cfg.Upload.DirectMaxBytes.Int64() != 2*1024*1024 {
		t.Fatalf("size options were not parsed")
	}
}

func TestFromArgsRejectsUnknownTOMLField(t *testing.T) {
	path := writeConfig(t, t.TempDir(), "[backup]\nbackend = []")
	_, err := appconfig.FromArgsWithBaseDir([]string{"--config", path}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v, want unknown field", err)
	}
}

func TestFromArgsReturnsHelpAndVersion(t *testing.T) {
	if _, err := appconfig.FromArgsWithBaseDir([]string{"--help"}, t.TempDir()); err != appconfig.ErrHelp {
		t.Fatalf("help error = %v", err)
	}
	version.Version = "0.0.1"
	version.Channel = "pre-release"
	if _, err := appconfig.FromArgsWithBaseDir([]string{"--version"}, t.TempDir()); !errors.Is(err, appconfig.ErrHelp) {
		t.Fatalf("version error = %v", err)
	}
}

func TestDurationRemainsUsable(t *testing.T) {
	cfg, err := appconfig.FromArgsWithBaseDir([]string{"--status-log-interval", "2m"}, t.TempDir())
	if err != nil || time.Duration(cfg.StatusLogInterval) != 2*time.Minute {
		t.Fatalf("StatusLogInterval = %s, error = %v", cfg.StatusLogInterval, err)
	}
}

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
