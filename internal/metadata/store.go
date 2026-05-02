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
	LocalCommitted = "committed"

	payloadBucket = "payloads"
)

type Payload struct {
	Hash      string    `json:"hash"`
	Size      int64     `json:"size"`
	Local     string    `json:"local"`
	CreatedAt time.Time `json:"created_at"`
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

func (s *Store) init() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(payloadBucket))
		return err
	})
}
