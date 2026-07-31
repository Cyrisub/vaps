package httpapi_test

import (
	"testing"

	"vaps/internal/metadata"
	"vaps/internal/utils"
)

func ioHash(payload []byte) string {
	return utils.IoHashSumHex(payload)
}

func checksum(payload []byte) string {
	hasher := utils.NewChecksum()
	_, _ = hasher.Write(payload)
	return utils.ChecksumDigestHex(hasher)
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
