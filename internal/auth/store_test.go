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

func TestListAllAndStats(t *testing.T) {
	store, err := auth.Open(t.TempDir()+"/auth.db", time.Minute)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.Issue("client-a", "127.0.0.1", "agent-a", nil); err != nil {
		t.Fatalf("Issue client-a: %v", err)
	}
	issued, err := store.Issue("client-b", "127.0.0.2", "agent-b", auth.ClientInfo{"hostname": "dev"})
	if err != nil {
		t.Fatalf("Issue client-b: %v", err)
	}
	if err := store.Revoke(issued.Token); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	tokens, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("len(tokens) = %d, want 2", len(tokens))
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalCount != 2 || stats.ActiveCount != 1 || stats.RevokedCount != 1 {
		t.Fatalf("stats = %#v", stats)
	}
}


func TestIssueReusesActiveTokenForSameClientAndIP(t *testing.T) {
	store, err := auth.Open(t.TempDir()+"/auth.db", time.Minute)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	first, err := store.Issue("client-1", "127.0.0.1", "agent-a", auth.ClientInfo{"hostname": "dev"})
	if err != nil {
		t.Fatalf("first Issue: %v", err)
	}
	second, err := store.Issue("client-1", "127.0.0.1", "agent-b", auth.ClientInfo{"hostname": "dev-2"})
	if err != nil {
		t.Fatalf("second Issue: %v", err)
	}
	if second.Token != first.Token {
		t.Fatalf("same client_id+ip issued different tokens")
	}
	if second.ExpiresAt.Before(first.ExpiresAt) {
		t.Fatalf("reused expires_at = %v, want >= %v", second.ExpiresAt, first.ExpiresAt)
	}

	otherIP, err := store.Issue("client-1", "10.0.0.2", "agent-a", nil)
	if err != nil {
		t.Fatalf("other IP Issue: %v", err)
	}
	if otherIP.Token == first.Token {
		t.Fatalf("different IP reused token, want new token")
	}

	otherClient, err := store.Issue("client-2", "127.0.0.1", "agent-a", nil)
	if err != nil {
		t.Fatalf("other client Issue: %v", err)
	}
	if otherClient.Token == first.Token {
		t.Fatalf("different client_id reused token, want new token")
	}

	tokens, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	active := 0
	for _, token := range tokens {
		if auth.TokenStatus(token, time.Now().UTC()) == "active" {
			active++
		}
	}
	if active != 3 {
		t.Fatalf("active tokens = %d, want 3 (reused pair + other IP + other client)", active)
	}

	if err := store.Revoke(first.Token); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	afterRevoke, err := store.Issue("client-1", "127.0.0.1", "agent-c", nil)
	if err != nil {
		t.Fatalf("Issue after revoke: %v", err)
	}
	if afterRevoke.Token == first.Token {
		t.Fatalf("revoked token was reused")
	}
}
