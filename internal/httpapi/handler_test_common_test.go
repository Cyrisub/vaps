package httpapi_test

import (
	"testing"

	"vaps/internal/iohash"
	"vaps/internal/metadata"
)

func ioHash(payload []byte) string {
	return iohash.SumHex(payload)
}

func openMetadata(t *testing.T) *metadata.Store {
	t.Helper()
	store, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close metadata: %v", err)
		}
	})
	return store
}
