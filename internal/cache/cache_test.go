package cache_test

import (
	"bytes"
	"testing"

	"vaps/internal/cache"
)

func TestCacheStoresAndReturnsCopy(t *testing.T) {
	c := cache.New(16, 16)
	payload := []byte("hello")

	if !c.Add("a", payload) {
		t.Fatalf("Add returned false, want true")
	}
	payload[0] = 'x'

	got, ok := c.Get("a")
	if !ok {
		t.Fatalf("Get ok = false, want true")
	}
	if !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("Get = %q, want hello", string(got))
	}
	got[0] = 'y'

	again, ok := c.Get("a")
	if !ok {
		t.Fatalf("second Get ok = false, want true")
	}
	if !bytes.Equal(again, []byte("hello")) {
		t.Fatalf("second Get = %q, want hello", string(again))
	}
}

func TestCacheEvictsLeastRecentlyUsedByBytes(t *testing.T) {
	c := cache.New(6, 6)

	c.Add("a", []byte("aa"))
	c.Add("b", []byte("bb"))
	if _, ok := c.Get("a"); !ok {
		t.Fatalf("expected a to exist before eviction")
	}
	c.Add("c", []byte("ccc"))

	if _, ok := c.Get("b"); ok {
		t.Fatalf("b exists after eviction, want evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatalf("a missing, want retained as recently used")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatalf("c missing, want retained")
	}
}

func TestCacheRejectsOversizedObjects(t *testing.T) {
	c := cache.New(16, 4)

	if c.Add("large", []byte("hello")) {
		t.Fatalf("Add returned true for oversized object")
	}
	if _, ok := c.Get("large"); ok {
		t.Fatalf("oversized object was cached")
	}
}

func TestCacheCanStoreRespectsCapacityAndObjectLimit(t *testing.T) {
	c := cache.New(16, 4)

	if !c.CanStore(4) {
		t.Fatalf("CanStore(4) = false, want true")
	}
	if c.CanStore(5) {
		t.Fatalf("CanStore(5) = true, want false")
	}
	if cache.New(0, 4).CanStore(1) {
		t.Fatalf("disabled cache CanStore(1) = true, want false")
	}
}
