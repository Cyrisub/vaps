package auth_test

import (
	"errors"
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

func TestParkAndWakeOnIssue(t *testing.T) {
	store, err := auth.Open(t.TempDir()+"/auth.db", time.Minute)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issued, err := store.Issue("client-1", "127.0.0.1", "agent-a", auth.ClientInfo{"hostname": "dev"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := store.Park(issued.Token); err != nil {
		t.Fatalf("Park: %v", err)
	}
	if _, err := store.Validate(issued.Token); !errors.Is(err, auth.ErrParked) {
		t.Fatalf("Validate after park err = %v, want ErrParked", err)
	}
	if _, err := store.Refresh(issued.Token); !errors.Is(err, auth.ErrParked) {
		t.Fatalf("Refresh after park err = %v, want ErrParked", err)
	}

	tokens, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(tokens) != 1 || auth.TokenStatus(tokens[0], time.Now().UTC()) != "parked" {
		t.Fatalf("status after park = %#v", tokens)
	}
	if tokens[0].ParkedAt == nil {
		t.Fatalf("ParkedAt is nil after park")
	}

	parkedPastExpiry := tokens[0]
	parkedPastExpiry.ExpiresAt = time.Now().UTC().Add(-2 * time.Hour)
	if auth.TokenStatus(parkedPastExpiry, time.Now().UTC()) != "parked" {
		t.Fatalf("parked token with past ExpiresAt should stay parked")
	}

	woken, err := store.Issue("client-1", "127.0.0.1", "agent-b", auth.ClientInfo{"hostname": "dev-2"})
	if err != nil {
		t.Fatalf("Issue wake: %v", err)
	}
	if woken.Token != issued.Token {
		t.Fatalf("wake issued different token")
	}
	record, err := store.Validate(woken.Token)
	if err != nil {
		t.Fatalf("Validate after wake: %v", err)
	}
	if record.ParkedAt != nil {
		t.Fatalf("ParkedAt still set after wake")
	}
	if auth.TokenStatus(record, time.Now().UTC()) != "active" {
		t.Fatalf("status after wake = %s, want active", auth.TokenStatus(record, time.Now().UTC()))
	}
	if !record.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("expires_at after wake = %v, want in the future", record.ExpiresAt)
	}

	if err := store.Park(woken.Token); err != nil {
		t.Fatalf("second Park: %v", err)
	}
	if err := store.Park(woken.Token); !errors.Is(err, auth.ErrParked) {
		t.Fatalf("Park already parked err = %v, want ErrParked", err)
	}

	otherIP, err := store.Issue("client-1", "10.0.0.2", "agent-a", nil)
	if err != nil {
		t.Fatalf("other IP Issue: %v", err)
	}
	if otherIP.Token == woken.Token {
		t.Fatalf("different IP woke parked token")
	}
}
