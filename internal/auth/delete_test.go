package auth

import (
	"testing"
	"time"
)

func TestDeleteExpiredAndDeleteAll(t *testing.T) {
	store, err := Open(t.TempDir()+"/auth.db", time.Hour)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	first, err := store.Issue("client-a", "127.0.0.1", "agent", nil)
	if err != nil {
		t.Fatalf("Issue first: %v", err)
	}
	second, err := store.Issue("client-b", "10.0.0.2", "agent", nil)
	if err != nil {
		t.Fatalf("Issue second: %v", err)
	}

	record, err := store.get(hashToken(first.Token))
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	record.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if err := store.put(record); err != nil {
		t.Fatalf("force expire: %v", err)
	}

	deleted, err := store.DeleteExpired()
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteExpired deleted = %d, want 1", deleted)
	}
	if _, err := store.Validate(first.Token); err != ErrNotFound {
		t.Fatalf("Validate first after DeleteExpired = %v, want ErrNotFound", err)
	}
	tokens, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(tokens) != 1 || tokens[0].ClientID != "client-b" {
		t.Fatalf("after DeleteExpired tokens = %#v", tokens)
	}
	if _, err := store.Validate(second.Token); err != nil {
		t.Fatalf("second token should remain active: %v", err)
	}

	deleted, err = store.DeleteAll()
	if err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteAll deleted = %d, want 1", deleted)
	}
	tokens, err = store.ListAll()
	if err != nil {
		t.Fatalf("ListAll after DeleteAll: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("len(tokens) = %d, want 0", len(tokens))
	}
}
