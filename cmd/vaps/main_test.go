package main

import "testing"

func TestListenURL(t *testing.T) {
	for _, test := range []struct{ addr, want string }{
		{":8588", "http://localhost:8588"},
		{"127.0.0.1:8588", "http://127.0.0.1:8588"},
		{"example:8588", "http://example:8588"},
	} {
		if got := listenURL(test.addr); got != test.want {
			t.Fatalf("listenURL(%q) = %q, want %q", test.addr, got, test.want)
		}
	}
}
