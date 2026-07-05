package metadata

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

const vcsBucket = "vcs_metadata"

type VCSRecord struct {
	Key         string            `json:"key"`
	PayloadHash string            `json:"payload_hash"`
	VCSType     string            `json:"vcs_type"`
	Repo        string            `json:"repo"`
	Revision    string            `json:"revision"`
	Path        string            `json:"path"`
	AssetID     string            `json:"asset_id,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	ReportedAt  time.Time         `json:"reported_at"`
	RemoteIP    string            `json:"remote_ip,omitempty"`
}

func vcsKey(record VCSRecord) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s",
		record.PayloadHash,
		strings.ToLower(record.VCSType),
		record.Repo,
		record.Revision,
		record.Path,
	)
}

func (s *Store) PutVCSRecord(record VCSRecord) error {
	record.Key = vcsKey(record)
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(vcsBucket))
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(record.Key), encoded)
	})
}

func (s *Store) QueryVCSByPayloadHash(payloadHash string) ([]VCSRecord, error) {
	payloadHash = strings.ToLower(strings.TrimSpace(payloadHash))
	var records []VCSRecord
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(vcsBucket))
		if bucket == nil {
			return nil
		}
		prefix := []byte(payloadHash + "|")
		return bucket.ForEach(func(key, value []byte) error {
			if !strings.HasPrefix(string(key), string(prefix)) {
				return nil
			}
			var record VCSRecord
			if err := json.Unmarshal(value, &record); err != nil {
				return err
			}
			records = append(records, record)
			return nil
		})
	})
	return records, err
}

func (s *Store) CountVCSByPayloadHash(payloadHash string) (int, error) {
	records, err := s.QueryVCSByPayloadHash(payloadHash)
	return len(records), err
}
