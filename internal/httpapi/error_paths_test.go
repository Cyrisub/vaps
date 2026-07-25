package httpapi

import "testing"

func TestIsMonitoredErrorPath(t *testing.T) {
	cases := map[string]bool{
		"/favicon.ico":            false,
		"/robots.txt":             false,
		"/health":                 true,
		"/dashboard":              true,
		"/dashboard/errors/query": true,
		"/v2/auth/request":        true,
		"/v2/payload/pull":        true,
	}
	for path, want := range cases {
		if got := isMonitoredErrorPath(path); got != want {
			t.Fatalf("isMonitoredErrorPath(%q) = %v, want %v", path, got, want)
		}
	}
}
