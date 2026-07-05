package auth_test

import (
	"testing"
	"time"

	"vaps/internal/auth"
)

func TestIssueValidateRefreshAndRevoke(t *testing.T) {
	store, err := auth.Open(t.TempDir()+"/auth.db", time.Minute)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issued, err := store.Issue("client-1", "127.0.0.1", "test-agent", auth.ClientInfo{"hostname": "dev"})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}
	if issued.Token == "" {
		t.Fatalf("token is empty")
	}

	if _, err := store.Validate(issued.Token); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	refreshed, err := store.Refresh(issued.Token)
	if err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if !refreshed.After(issued.ExpiresAt) {
		t.Fatalf("refreshed expires_at = %v, want after %v", refreshed, issued.ExpiresAt)
	}

	if err := store.Revoke(issued.Token); err != nil {
		t.Fatalf("Revoke returned error: %v", err)
	}
	if _, err := store.Validate(issued.Token); err == nil {
		t.Fatalf("Validate after revoke = nil, want error")
	}
}
