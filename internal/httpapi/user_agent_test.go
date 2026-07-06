package httpapi_test

import (
	"testing"

	"vaps/internal/httpapi"
)

func TestParseUEUserAgent(t *testing.T) {
	parsed := httpapi.ParseUserAgent(`NFDemo/UE5-CL-0 (http-eventloop) Windows/10.0.22631.1.256.64bit`)
	if !parsed.Parsed {
		t.Fatalf("expected parsed=true")
	}
	if parsed.Project != "NFDemo" {
		t.Fatalf("project = %q, want NFDemo", parsed.Project)
	}
	if parsed.Engine != "UE5-CL-0" {
		t.Fatalf("engine = %q, want UE5-CL-0", parsed.Engine)
	}
	if parsed.System != "Windows" {
		t.Fatalf("system = %q, want Windows", parsed.System)
	}
	if parsed.SystemDetail != "10.0.22631.1.256.64bit" {
		t.Fatalf("system_detail = %q", parsed.SystemDetail)
	}
	if parsed.Transport != "http-eventloop" {
		t.Fatalf("transport = %q, want http-eventloop", parsed.Transport)
	}

	browser := httpapi.ParseUserAgent(`Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36`)
	if browser.Parsed {
		t.Fatalf("browser UA should not parse as UE client")
	}
	if browser.Raw == "" {
		t.Fatalf("raw should be preserved")
	}
}
