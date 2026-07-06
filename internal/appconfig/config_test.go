package appconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"vaps/internal/appconfig"
	"vaps/internal/version"
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
	if cfg.AuthDB != filepath.Join(binDir, "data", "auth.db") {
		t.Fatalf("AuthDB = %q, want data/auth.db under binary dir", cfg.AuthDB)
	}
	if cfg.UploadDB != filepath.Join(binDir, "data", "uploads.db") {
		t.Fatalf("UploadDB = %q, want data/uploads.db under binary dir", cfg.UploadDB)
	}
	if cfg.Upload.Dir != filepath.Join(binDir, "data", "uploads") {
		t.Fatalf("Upload.Dir = %q, want data/uploads under binary dir", cfg.Upload.Dir)
	}
	if cfg.Cache.Bytes != 64*1024*1024 {
		t.Fatalf("Cache.Bytes = %d, want 64MiB", cfg.Cache.Bytes)
	}
	if cfg.Cache.MaxObjectBytes != 4*1024*1024 {
		t.Fatalf("Cache.MaxObjectBytes = %d, want 4MiB", cfg.Cache.MaxObjectBytes)
	}
	if time.Duration(cfg.StatusLogInterval) != time.Minute {
		t.Fatalf("StatusLogInterval = %s, want 1m", cfg.StatusLogInterval)
	}
	if len(cfg.Backup.Backend) != 0 {
		t.Fatalf("Backup.Backend = %v, want empty", cfg.Backup.Backend)
	}
	if time.Duration(cfg.Backup.FlushInterval) != time.Minute {
		t.Fatalf("Backup.FlushInterval = %s, want 1m", cfg.Backup.FlushInterval)
	}
	if cfg.Backup.MaxPending != 50 {
		t.Fatalf("Backup.MaxPending = %d, want 50", cfg.Backup.MaxPending)
	}
	if cfg.Backup.SVN.Bin != "svn" {
		t.Fatalf("Backup.SVN.Bin = %q, want svn", cfg.Backup.SVN.Bin)
	}
	if cfg.Backup.SVN.MuccBin != "svnmucc" {
		t.Fatalf("Backup.SVN.MuccBin = %q, want svnmucc", cfg.Backup.SVN.MuccBin)
	}
}

func TestFromArgsUsesFlags(t *testing.T) {
	binDir := t.TempDir()
	dataDir := filepath.Join(binDir, "vaps")
	cfg, err := appconfig.FromArgsWithBaseDir([]string{
		"--addr", "127.0.0.1:9000",
		"--data-dir", dataDir,
		"--cache-bytes", "1024",
		"--cache-max-object-bytes", "128",
		"--status-log-interval", "2m",
		"--backup-backend", "svn",
		"--backup-flush-interval", "5s",
		"--backup-max-pending", "7",
		"--backup-svn-url", "https://svn.example/repo",
		"--backup-svn-bin", "custom-svn",
		"--backup-svn-mucc-bin", "custom-svnmucc",
	}, binDir)
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Fatalf("Addr = %q, want 127.0.0.1:9000", cfg.Addr)
	}
	if cfg.DataDir != dataDir {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, dataDir)
	}
	if cfg.MetadataDB != filepath.Join(binDir, "data", "metadata.db") {
		t.Fatalf("MetadataDB = %q", cfg.MetadataDB)
	}
	if cfg.Cache.Bytes != 1024 {
		t.Fatalf("Cache.Bytes = %d, want 1024", cfg.Cache.Bytes)
	}
	if cfg.Cache.MaxObjectBytes != 128 {
		t.Fatalf("Cache.MaxObjectBytes = %d, want 128", cfg.Cache.MaxObjectBytes)
	}
	if time.Duration(cfg.StatusLogInterval) != 2*time.Minute {
		t.Fatalf("StatusLogInterval = %s, want 2m", cfg.StatusLogInterval)
	}
	if !reflect.DeepEqual(cfg.Backup.Backend, []string{"svn"}) {
		t.Fatalf("Backup.Backend = %v, want [svn]", cfg.Backup.Backend)
	}
	if time.Duration(cfg.Backup.FlushInterval) != 5*time.Second {
		t.Fatalf("Backup.FlushInterval = %s, want 5s", cfg.Backup.FlushInterval)
	}
	if cfg.Backup.MaxPending != 7 {
		t.Fatalf("Backup.MaxPending = %d, want 7", cfg.Backup.MaxPending)
	}
	if cfg.Backup.SVN.URL != "https://svn.example/repo" {
		t.Fatalf("Backup.SVN.URL = %q", cfg.Backup.SVN.URL)
	}
	if cfg.Backup.SVN.Bin != "custom-svn" {
		t.Fatalf("Backup.SVN.Bin = %q, want custom-svn", cfg.Backup.SVN.Bin)
	}
	if cfg.Backup.SVN.MuccBin != "custom-svnmucc" {
		t.Fatalf("Backup.SVN.MuccBin = %q, want custom-svnmucc", cfg.Backup.SVN.MuccBin)
	}
}

func TestFromArgsLoadsTOMLConfig(t *testing.T) {
	binDir := t.TempDir()
	configPath := writeConfig(t, binDir, `
addr = "127.0.0.1:9000"
data_dir = "payloads"
metadata_db = "meta/vaps.db"
log_dir = "logz"
log_retention_days = 3
status_log_interval = "30s"

[cache]
bytes = 2048
max_object_bytes = 256

[backup]
backend = ["svn"]
flush_interval = "10s"
max_pending = 3

[backup.svn]
url = "https://svn.example/repo"
bin = "custom-svn"
mucc_bin = "custom-svnmucc"
`)

	cfg, err := appconfig.FromArgsWithBaseDir([]string{"-c", configPath}, binDir)
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Fatalf("Addr = %q", cfg.Addr)
	}
	if cfg.DataDir != filepath.Join(binDir, "payloads") {
		t.Fatalf("DataDir = %q", cfg.DataDir)
	}
	if cfg.MetadataDB != filepath.Join(binDir, "meta", "vaps.db") {
		t.Fatalf("MetadataDB = %q", cfg.MetadataDB)
	}
	if cfg.LogDir != filepath.Join(binDir, "logz") {
		t.Fatalf("LogDir = %q", cfg.LogDir)
	}
	if cfg.LogRetentionDays != 3 {
		t.Fatalf("LogRetentionDays = %d, want 3", cfg.LogRetentionDays)
	}
	if cfg.Cache.Bytes != 2048 || cfg.Cache.MaxObjectBytes != 256 {
		t.Fatalf("Cache = %#v", cfg.Cache)
	}
	if time.Duration(cfg.StatusLogInterval) != 30*time.Second {
		t.Fatalf("StatusLogInterval = %s", cfg.StatusLogInterval)
	}
	if cfg.Backup.SVN.MuccBin != "custom-svnmucc" {
		t.Fatalf("Backup.SVN.MuccBin = %q", cfg.Backup.SVN.MuccBin)
	}
	if time.Duration(cfg.Backup.FlushInterval) != 10*time.Second {
		t.Fatalf("Backup.FlushInterval = %s", cfg.Backup.FlushInterval)
	}
	if cfg.Backup.MaxPending != 3 {
		t.Fatalf("Backup.MaxPending = %d", cfg.Backup.MaxPending)
	}
}

func TestFromArgsCommandLineOverridesTOMLConfig(t *testing.T) {
	binDir := t.TempDir()
	configPath := writeConfig(t, binDir, `
addr = ":8588"

[cache]
bytes = 2048

[backup]
backend = ["svn"]

[backup.svn]
url = "https://svn.example/old"
`)

	cfg, err := appconfig.FromArgsWithBaseDir([]string{
		"--config", configPath,
		"--addr", "127.0.0.1:9001",
		"--cache-bytes", "4096",
		"--backup-svn-url", "https://svn.example/new",
	}, binDir)
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9001" {
		t.Fatalf("Addr = %q", cfg.Addr)
	}
	if cfg.Cache.Bytes != 4096 {
		t.Fatalf("Cache.Bytes = %d, want 4096", cfg.Cache.Bytes)
	}
	if cfg.Backup.SVN.URL != "https://svn.example/new" {
		t.Fatalf("Backup.SVN.URL = %q", cfg.Backup.SVN.URL)
	}
}

func TestFromArgsLoadsTOMLConfigHumanReadableBytes(t *testing.T) {
	binDir := t.TempDir()
	configPath := writeConfig(t, binDir, `
[cache]
bytes = "2MiB"
max_object_bytes = "512KiB"

[upload]
direct_max_bytes = 4096
`)

	cfg, err := appconfig.FromArgsWithBaseDir([]string{"--config", configPath}, binDir)
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.Cache.Bytes.Int64() != 2*1024*1024 {
		t.Fatalf("Cache.Bytes = %d, want 2MiB", cfg.Cache.Bytes.Int64())
	}
	if cfg.Cache.MaxObjectBytes.Int64() != 512*1024 {
		t.Fatalf("Cache.MaxObjectBytes = %d, want 512KiB", cfg.Cache.MaxObjectBytes.Int64())
	}
	if cfg.Upload.DirectMaxBytes.Int64() != 4096 {
		t.Fatalf("Upload.DirectMaxBytes = %d, want 4096", cfg.Upload.DirectMaxBytes.Int64())
	}
}

func TestFromArgsUsesFlagsHumanReadableBytes(t *testing.T) {
	cfg, err := appconfig.FromArgsWithBaseDir([]string{
		"--cache-bytes", "1MiB",
		"--upload-direct-max-bytes", "2MiB",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.Cache.Bytes.Int64() != 1024*1024 {
		t.Fatalf("Cache.Bytes = %d, want 1MiB", cfg.Cache.Bytes.Int64())
	}
	if cfg.Upload.DirectMaxBytes.Int64() != 2*1024*1024 {
		t.Fatalf("Upload.DirectMaxBytes = %d, want 2MiB", cfg.Upload.DirectMaxBytes.Int64())
	}
}

func TestFromArgsRejectsUnknownTOMLField(t *testing.T) {
	binDir := t.TempDir()
	configPath := writeConfig(t, binDir, `unexpected = true`)

	_, err := appconfig.FromArgsWithBaseDir([]string{"-c", configPath}, binDir)
	if err == nil {
		t.Fatalf("FromArgsWithBaseDir error = nil, want unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %q, want unknown field", err.Error())
	}
}

func TestFromArgsRejectsSVNBackupWithoutURL(t *testing.T) {
	_, err := appconfig.FromArgsWithBaseDir([]string{"--backup-backend=svn"}, t.TempDir())
	if err == nil {
		t.Fatalf("FromArgsWithBaseDir error = nil, want missing SVN URL error")
	}
	if !strings.Contains(err.Error(), "backup-svn-url is required") {
		t.Fatalf("error = %q, want backup-svn-url required", err.Error())
	}
}

func TestFromArgsNilUsesProcessArgs(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"vaps", "--backup-backend=svn"}

	_, err := appconfig.FromArgs(nil)
	if err == nil {
		t.Fatalf("FromArgs error = nil, want missing SVN URL error")
	}
	if !strings.Contains(err.Error(), "backup-svn-url is required") {
		t.Fatalf("error = %q, want backup-svn-url required", err.Error())
	}
}

func TestFromArgsRejectsUnknownBackupBackend(t *testing.T) {
	_, err := appconfig.FromArgsWithBaseDir([]string{"--backup-backend", "unknown"}, t.TempDir())
	if err == nil {
		t.Fatalf("FromArgsWithBaseDir error = nil, want unknown backup backend error")
	}
	if !strings.Contains(err.Error(), "backup backend must be one of") {
		t.Fatalf("error = %q, want backup backend validation", err.Error())
	}
}

func TestFromArgsAllowsNoneBackupBackend(t *testing.T) {
	cfg, err := appconfig.FromArgsWithBaseDir([]string{"--backup-backend", "none"}, t.TempDir())
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if !reflect.DeepEqual(cfg.Backup.Backend, []string{"none"}) {
		t.Fatalf("Backup.Backend = %v, want [none]", cfg.Backup.Backend)
	}
}

func TestFromArgsAllowsUppercaseBackupBackend(t *testing.T) {
	cfg, err := appconfig.FromArgsWithBaseDir([]string{"--backup-backend", "SVN", "--backup-svn-url", "https://svn.example/repo"}, t.TempDir())
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if !reflect.DeepEqual(cfg.Backup.Backend, []string{"svn"}) {
		t.Fatalf("Backup.Backend = %v, want [svn]", cfg.Backup.Backend)
	}
}

func TestFromArgsAllowsMetadataDBOverride(t *testing.T) {
	metadataDB := filepath.Join(t.TempDir(), "custom.db")
	cfg, err := appconfig.FromArgsWithBaseDir([]string{"--metadata-db", metadataDB}, t.TempDir())
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.MetadataDB != metadataDB {
		t.Fatalf("MetadataDB = %q, want %q", cfg.MetadataDB, metadataDB)
	}
}

func TestFromArgsWithBaseDirResolvesRelativePathsFromBinaryDir(t *testing.T) {
	binDir := t.TempDir()

	cfg, err := appconfig.FromArgsWithBaseDir([]string{"--data-dir", "payloads", "--metadata-db", "meta/vaps.db"}, binDir)
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
	dataDir := filepath.Join(t.TempDir(), "vaps")
	metadataDB := filepath.Join(t.TempDir(), "custom.db")
	cfg, err := appconfig.FromArgsWithBaseDir([]string{"--data-dir", dataDir, "--metadata-db", metadataDB}, t.TempDir())
	if err != nil {
		t.Fatalf("FromArgsWithBaseDir returned error: %v", err)
	}
	if cfg.DataDir != dataDir {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, dataDir)
	}
	if cfg.MetadataDB != metadataDB {
		t.Fatalf("MetadataDB = %q, want %q", cfg.MetadataDB, metadataDB)
	}
}

func TestFromArgsRejectsSingleDashOverride(t *testing.T) {
	_, err := appconfig.FromArgsWithBaseDir([]string{"-addr", ":9000"}, t.TempDir())
	if err == nil {
		t.Fatalf("FromArgsWithBaseDir error = nil, want single-dash override rejection")
	}
}

func TestFromArgsReturnsHelp(t *testing.T) {
	_, err := appconfig.FromArgsWithBaseDir([]string{"--help"}, t.TempDir())
	if err != appconfig.ErrHelp {
		t.Fatalf("FromArgsWithBaseDir error = %v, want ErrHelp", err)
	}
}

func TestFromArgsPrintsVersion(t *testing.T) {
	version.Version = "0.0.1"
	version.Channel = "pre-release"
	_, err := appconfig.FromArgsWithBaseDir([]string{"--version"}, t.TempDir())
	if !errors.Is(err, appconfig.ErrHelp) {
		t.Fatalf("FromArgsWithBaseDir error = %v, want ErrHelp", err)
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
