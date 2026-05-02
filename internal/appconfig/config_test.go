package appconfig_test

import (
	"testing"

	"vaps/internal/appconfig"
)

func TestFromArgsUsesDefaults(t *testing.T) {
	cfg, err := appconfig.FromArgs(nil)
	if err != nil {
		t.Fatalf("FromArgs returned error: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.DataDir != "data" {
		t.Fatalf("DataDir = %q, want data", cfg.DataDir)
	}
}

func TestFromArgsUsesFlags(t *testing.T) {
	cfg, err := appconfig.FromArgs([]string{"-addr", "127.0.0.1:9000", "-data-dir", "/tmp/vaps"})
	if err != nil {
		t.Fatalf("FromArgs returned error: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Fatalf("Addr = %q, want 127.0.0.1:9000", cfg.Addr)
	}
	if cfg.DataDir != "/tmp/vaps" {
		t.Fatalf("DataDir = %q, want /tmp/vaps", cfg.DataDir)
	}
}
