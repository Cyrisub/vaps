package version_test

import (
	"testing"

	"vaps/internal/version"
)

func TestString(t *testing.T) {
	version.Version = "0.0.1-alpha"
	version.Channel = "pre-release"
	if got, want := version.String(), "vaps 0.0.1-alpha (pre-release)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
