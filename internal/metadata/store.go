package metadata

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
)

var ErrNotFound = errors.New("metadata record not found")

const (
	payloadBucket = "payloads"
)

type PayloadStatus uint8

const (
	StatusLocal    PayloadStatus = 1 << 0
	StatusCache    PayloadStatus = 1 << 1
	StatusCorrupt  PayloadStatus = 1 << 4
	StatusReadonly PayloadStatus = 1 << 5

	backupShift = 2
	backupMask  = PayloadStatus(0b11 << backupShift)
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

func (s PayloadStatus) IsCorrupt() bool {
	return s&StatusCorrupt != 0
}

func (s PayloadStatus) IsReadonly() bool {
	return s&StatusReadonly != 0
}

func (s PayloadStatus) Backup() BackupStatus {
	return BackupStatus((s & backupMask) >> backupShift)
}

func (s PayloadStatus) WithCache(enabled bool) PayloadStatus {
	if enabled {
		return s | StatusCache
	}
	return s &^ StatusCache
}

func (s PayloadStatus) WithReadonly(enabled bool) PayloadStatus {
	if enabled {
		return s | StatusReadonly
	}
	return s &^ StatusReadonly
}

func (s PayloadStatus) WithBackup(backup BackupStatus) PayloadStatus {
	return (s &^ backupMask) | PayloadStatus(backup&0b11)<<backupShift
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
	Hash      string        `json:"hash"`
	Size      int64         `json:"size"`
	Status    PayloadStatus `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
}

type Stats struct {
	PayloadCount int64 `json:"payload_count"`
	TotalBytes   int64 `json:"total_bytes"`
	CachedCount  int64 `json:"cached_count"`
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

func (s *Store) init() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(payloadBucket))
		return err
	})
}
