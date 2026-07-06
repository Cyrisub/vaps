package httpapi_test

import (
	"testing"

	"vaps/internal/httpapi"
)

func TestNormalizeClientIP(t *testing.T) {
	cases := map[string]string{
		"::1":              "localhost",
		"[::1]":            "localhost",
		"127.0.0.1":        "localhost",
		"::ffff:127.0.0.1": "localhost",
		"10.0.0.2":         "10.0.0.2",
		"  ::1  ":          "localhost",
		"":                 "",
	}
	for in, want := range cases {
		if got := httpapi.NormalizeClientIP(in); got != want {
			t.Fatalf("NormalizeClientIP(%q) = %q, want %q", in, got, want)
		}
	}
}
