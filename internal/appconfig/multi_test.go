package appconfig_test

import (
	"path/filepath"
	"strings"
	"testing"

	"vaps/internal/appconfig"
)

func TestMultipleFilesystemBackendsWithoutS3Credentials(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
[[storage.backends]]
name = "disk"
type = "filesystem"
dir = "persistent"
[[storage.backends]]
name = "smb"
type = "filesystem"
dir = "mounted/vaps"
mount_source = "//server/share"
`)
	cfg, err := appconfig.FromArgsWithBaseDir([]string{"--config", path}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Storage.Backends) != 2 || cfg.Storage.Backends[0].Dir != filepath.Join(dir, "persistent") || cfg.Storage.Backends[1].MountSource != "//server/share" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestInvalidMultipleBackends(t *testing.T) {
	for _, content := range []string{
		`[[storage.backends]]
type = "filesystem"
dir = "disk"`,
		`[[storage.backends]]
name = "one"
type = "unknown"`,
		`[[storage.backends]]
name = "one"
type = "filesystem"`,
		`[[storage.backends]]
name = "one"
type = "s3"`,
		`[[storage.backends]]
name = "one"
type = "filesystem"
dir = "disk"
mount_source = "bad"`,
		strings.Repeat("[[storage.backends]]\nname = \"one\"\ntype = \"filesystem\"\ndir = \"disk\"\n", 2),
	} {
		dir := t.TempDir()
		if _, err := appconfig.FromArgsWithBaseDir([]string{"--config", writeConfig(t, dir, content)}, dir); err == nil {
			t.Fatalf("accepted %s", content)
		}
	}
}
