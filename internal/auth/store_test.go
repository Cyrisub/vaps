package auth_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"vaps/internal/auth"
)

func TestSourceRegistrationIsPermanentAndReused(t *testing.T) {
	path := t.TempDir() + "/auth.db"
	store, err := auth.Open(path, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Issue("client-1", "127.0.0.1", "agent-a", auth.ClientInfo{"hostname": "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == "" || first.ExpiresAt != nil {
		t.Fatalf("registration = %#v", first)
	}
	second, err := store.Issue("client-1", "::1", "agent-b", auth.ClientInfo{"hostname": "dev-2"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Token != first.Token {
		t.Fatal("same client and normalized IP did not reuse token")
	}
	other, err := store.Issue("client-1", "10.0.0.2", "agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if other.Token == first.Token {
		t.Fatal("different IP reused token")
	}
	otherClient, err := store.Issue("client-2", "127.0.0.1", "agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if otherClient.Token == first.Token {
		t.Fatal("different client reused token")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = auth.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.Validate(first.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !record.ExpiresAt.IsZero() || record.UserAgent != "agent-b" || record.ClientInfo["hostname"] != "dev-2" {
		t.Fatalf("persisted source = %#v", record)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalCount != 3 || stats.TrackedCount != 3 || stats.ExpiredCount != 0 || stats.RevokedCount != 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestObserveIsConcurrentAndDoesNotPersistActivity(t *testing.T) {
	path := t.TempDir() + "/auth.db"
	store, err := auth.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := store.Issue("client", "10.0.0.1", "agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	hash := auth.HashToken(issued.Token)
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			if _, err := store.Observe(hash, "10.0.0.2", "new-agent"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	tokens, err := store.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	activity := tokens[0].Activity
	if activity.RequestCount != 64 || activity.LastSeenAt == nil || activity.RemoteIP != "10.0.0.2" || activity.UserAgent != "new-agent" {
		t.Fatalf("activity = %#v", activity)
	}
	if _, err := store.Observe(auth.HashToken("unknown"), "x", "y"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("unknown observe = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = auth.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tokens, err = store.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].Activity.RequestCount != 0 || tokens[0].Activity.LastSeenAt != nil {
		t.Fatalf("activity should reset on restart: %#v", tokens)
	}
	deleted, err := store.DeleteAll()
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteAll = %d, %v", deleted, err)
	}
	if _, err := store.Validate(issued.Token); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("lookup after delete = %v", err)
	}
	if _, err := store.Issue("new-client", "10.0.0.1", "agent", nil); err != nil {
		t.Fatal(err)
	}
}
