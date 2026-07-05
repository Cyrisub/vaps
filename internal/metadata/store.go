package metadata

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

var ErrNotFound = errors.New("metadata record not found")

const (
	payloadBucket = "payloads"
)

type PayloadStatus uint8

const (
	StatusLocal  PayloadStatus = 1 << 0
	StatusCache  PayloadStatus = 1 << 1
	StatusBackup PayloadStatus = 1 << 2
)

type BackupStatus uint8

const (
	BackupNone BackupStatus = iota
	BackupPending
	Backuped
	BackupFailed
)

func (s PayloadStatus) HasLocal() bool {
	return s&StatusLocal != 0
}

func (s PayloadStatus) HasCache() bool {
	return s&StatusCache != 0
}

func (s PayloadStatus) HasBackup() bool {
	return s&StatusBackup != 0
}

func (s PayloadStatus) WithCache(enabled bool) PayloadStatus {
	if enabled {
		return s | StatusCache
	}
	return s &^ StatusCache
}

func (s PayloadStatus) WithBackup(enabled bool) PayloadStatus {
	if enabled {
		return s | StatusBackup
	}
	return s &^ StatusBackup
}

func (s BackupStatus) String() string {
	switch s {
	case BackupPending:
		return "pending"
	case Backuped:
		return "backuped"
	case BackupFailed:
		return "failed"
	default:
		return "none"
	}
}

type Payload struct {
	Hash         string        `json:"hash"`
	Size         int64         `json:"size"`
	Status       PayloadStatus `json:"status"`
	BackupStatus BackupStatus  `json:"backup_status,omitempty"`
	CreatedAt    *time.Time    `json:"created_at"`
	BackupedAt   *time.Time    `json:"backuped_at"`
}

type Stats struct {
	PayloadCount int64 `json:"payload_count"`
	TotalBytes   int64 `json:"total_bytes"`
	CachedCount  int64 `json:"cached_count"`
}

type PayloadQuery struct {
	HashContains string
	RequireLocal bool
	RequireCache bool
	Backup       *bool
	MinSize      *int64
	MaxSize      *int64
	Limit        int
	Offset       int
}

type PayloadQueryResult struct {
	Items  []Payload
	Total  int64
	Limit  int
	Offset int
}

type Store struct {
	db *bbolt.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) PutPayload(payload Payload) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(payloadBucket))
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(payload.Hash), encoded)
	})
}

func (s *Store) GetPayload(hash string) (Payload, error) {
	var payload Payload
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(payloadBucket))
		value := bucket.Get([]byte(hash))
		if value == nil {
			return ErrNotFound
		}
		return json.Unmarshal(value, &payload)
	})
	if err != nil {
		return Payload{}, err
	}
	return payload, nil
}

func (s *Store) Stats() (Stats, error) {
	var stats Stats
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(payloadBucket))
		return bucket.ForEach(func(_, value []byte) error {
			var payload Payload
			if err := json.Unmarshal(value, &payload); err != nil {
				return err
			}
			stats.PayloadCount++
			stats.TotalBytes += payload.Size
			if payload.Status.HasCache() {
				stats.CachedCount++
			}
			return nil
		})
	})
	if err != nil {
		return Stats{}, err
	}
	return stats, nil
}

func (s *Store) QueryPayloads(query PayloadQuery) (PayloadQueryResult, error) {
	if query.Offset < 0 {
		query.Offset = 0
	}
	result := PayloadQueryResult{Limit: query.Limit, Offset: query.Offset}
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(payloadBucket))
		return bucket.ForEach(func(_, value []byte) error {
			var payload Payload
			if err := json.Unmarshal(value, &payload); err != nil {
				return err
			}
			if !query.matches(payload) {
				return nil
			}
			result.Total++
			if int(result.Total) <= query.Offset {
				return nil
			}
			if query.Limit > 0 && len(result.Items) >= query.Limit {
				return nil
			}
			result.Items = append(result.Items, payload)
			return nil
		})
	})
	if err != nil {
		return PayloadQueryResult{}, err
	}
	return result, nil
}

func (s *Store) init() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(payloadBucket)); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists([]byte(vcsBucket))
		return err
	})
}

func (q PayloadQuery) matches(payload Payload) bool {
	if q.HashContains != "" && !strings.Contains(payload.Hash, q.HashContains) {
		return false
	}
	if q.RequireLocal && !payload.Status.HasLocal() {
		return false
	}
	if q.RequireCache && !payload.Status.HasCache() {
		return false
	}
	if q.Backup != nil && payload.Status.HasBackup() != *q.Backup {
		return false
	}
	if q.MinSize != nil && payload.Size < *q.MinSize {
		return false
	}
	if q.MaxSize != nil && payload.Size > *q.MaxSize {
		return false
	}
	return true
}
