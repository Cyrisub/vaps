package appconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"vaps/internal/appconfig"
	"vaps/internal/version"
)

func TestFromArgsUsesStorageDefaults(t *testing.T) {
	binDir := t.TempDir()
	cfg, err := appconfig.FromArgsWithBaseDir([]string{
		"--storage-s3-endpoint", "https://s3.amazonaws.com",
	}, binDir)
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
	if time.Duration(cfg.Dashboard.InfoRefreshInterval) != 5*time.Second {
		t.Fatalf("Dashboard.InfoRefreshInterval = %s", cfg.Dashboard.InfoRefreshInterval)
	}
	if cfg.Metadata.Workers != -1 ||
		time.Duration(cfg.Metadata.Timeout) != 10*time.Second ||
		time.Duration(cfg.Metadata.ProgressInterval) != 10*time.Second {
		t.Fatalf(
			"metadata validation defaults = %d/%s/%s",
			cfg.Metadata.Workers,
			cfg.Metadata.Timeout,
			cfg.Metadata.ProgressInterval,
		)
	}
}

func TestFromArgsRejectsMissingDefaultS3Endpoint(t *testing.T) {
	_, err := appconfig.FromArgsWithBaseDir(nil, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "storage-s3-endpoint is required") {
		t.Fatalf("error = %v, want missing endpoint error", err)
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
		"[storage.s3]\nendpoint = \"\"",
		"[storage.local]\ndir = \"\"",
	} {
		dir := t.TempDir()
		_, err := appconfig.FromArgsWithBaseDir([]string{"--config", writeConfig(t, dir, content)}, dir)
		if err == nil {
			t.Fatalf("FromArgsWithBaseDir(%q) error = nil", content)
		}
	}
}

func TestFromArgsRejectsInvalidS3Endpoint(t *testing.T) {
	for _, endpoint := range []string{
		"localhost:9000",
		"ftp://minio.example",
		"https://",
		"https://minio.example?access_key=test",
	} {
		dir := t.TempDir()
		content := "[storage.s3]\nendpoint = " + strconv.Quote(endpoint)
		_, err := appconfig.FromArgsWithBaseDir([]string{"--config", writeConfig(t, dir, content)}, dir)
		if err == nil || !strings.Contains(err.Error(), "storage-s3-endpoint") {
			t.Fatalf("endpoint %q error = %v, want invalid endpoint error", endpoint, err)
		}
	}
}

func TestFromArgsRejectsInvalidDashboardRefreshInterval(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfig(t, dir, `
[dashboard]
info_refresh_interval = "0s"

[storage.s3]
endpoint = "https://s3.amazonaws.com"
`)
	_, err := appconfig.FromArgsWithBaseDir([]string{"--config", configPath}, dir)
	if err == nil || !strings.Contains(err.Error(), "dashboard-info-refresh-interval") {
		t.Fatalf("error = %v, want invalid dashboard refresh interval error", err)
	}
}

func TestFromArgsRejectsInvalidMetadataWorkers(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfig(t, dir, `
[metadata]
workers = -2

[storage.s3]
endpoint = "https://s3.amazonaws.com"
`)
	_, err := appconfig.FromArgsWithBaseDir([]string{"--config", configPath}, dir)
	if err == nil || !strings.Contains(err.Error(), "metadata-workers") {
		t.Fatalf("error = %v, want invalid metadata workers error", err)
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
		"--storage-s3-endpoint", "https://s3.amazonaws.com",
		"--cache-bytes", "1MiB",
		"--upload-direct-max-bytes", "2MiB",
		"--metadata-workers", "4",
		"--metadata-timeout", "45s",
		"--metadata-progress-interval", "3s",
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
	if cfg.Metadata.Workers != 4 ||
		time.Duration(cfg.Metadata.Timeout) != 45*time.Second ||
		time.Duration(cfg.Metadata.ProgressInterval) != 3*time.Second {
		t.Fatalf("metadata validation options were not parsed")
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
	cfg, err := appconfig.FromArgsWithBaseDir([]string{
		"--status-log-interval", "2m",
		"--storage-s3-endpoint", "https://s3.amazonaws.com",
		"--dashboard-info-refresh-interval", "7s",
		"--metadata-workers", "6",
		"--metadata-timeout", "45s",
		"--metadata-progress-interval", "4s",
	}, t.TempDir())
	if err != nil ||
		time.Duration(cfg.StatusLogInterval) != 2*time.Minute ||
		time.Duration(cfg.Dashboard.InfoRefreshInterval) != 7*time.Second ||
		cfg.Metadata.Workers != 6 ||
		time.Duration(cfg.Metadata.Timeout) != 45*time.Second ||
		time.Duration(cfg.Metadata.ProgressInterval) != 4*time.Second {
		t.Fatalf("status/dashboard/metadata validation settings = %s/%s/%d/%s/%s, error = %v",
			cfg.StatusLogInterval,
			cfg.Dashboard.InfoRefreshInterval,
			cfg.Metadata.Workers,
			cfg.Metadata.Timeout,
			cfg.Metadata.ProgressInterval,
			err,
		)
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
