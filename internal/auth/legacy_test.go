package auth

import (
	"encoding/json"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

func TestLegacyLifecycleStatesRemainTraceableAndReusable(t *testing.T) {
	for _, state := range []string{"expired", "parked", "revoked", "hash-only"} {
		t.Run(state, func(t *testing.T) {
			path := t.TempDir() + "/auth.db"
			store, err := Open(path, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			past := time.Now().UTC().Add(-time.Hour)
			record := Token{Hash: HashToken("legacy"), Token: "legacy", ClientID: "old-client", RemoteIP: "127.0.0.1", CreatedAt: past, ExpiresAt: past}
			switch state {
			case "parked":
				record.ParkedAt = &past
			case "revoked":
				record.RevokedAt = &past
			case "hash-only":
				record.Token = ""
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.db.Update(func(tx *bbolt.Tx) error {
				return tx.Bucket([]byte(tokenBucket)).Put([]byte(record.Hash), encoded)
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			beforeStats := store.db.Stats()
			before := beforeStats.TxStats.GetWrite()
			observed, err := store.Observe(record.Hash, "10.0.0.2", "old-agent")
			if err != nil || observed.ClientID != "old-client" {
				t.Fatalf("legacy observe = %#v, %v", observed, err)
			}
			afterStats := store.db.Stats()
			if after := afterStats.TxStats.GetWrite(); after != before {
				t.Fatal("request observation wrote to database")
			}
			reused, err := store.Issue("old-client", "::1", "agent", nil)
			if err != nil {
				t.Fatal(err)
			}
			if state != "hash-only" && reused.Token != "legacy" {
				t.Fatal("legacy token was not reused")
			}
			if state == "hash-only" && reused.Token == "" {
				t.Fatal("hash-only registration needs a new token")
			}
			if reused.ExpiresAt != nil {
				t.Fatal("registration expires")
			}
		})
	}
}
