package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
)

var (
	ErrNotFound = errors.New("auth token not found")
	ErrExpired  = errors.New("auth token expired")
	ErrRevoked  = errors.New("auth token revoked")
)

const tokenBucket = "auth_tokens"

type ClientInfo map[string]string

type Token struct {
	Hash       string     `json:"-"`
	ClientID   string     `json:"client_id"`
	RemoteIP   string     `json:"remote_ip"`
	UserAgent  string     `json:"user_agent"`
	ClientInfo ClientInfo `json:"client_info"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type IssueResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Store struct {
	db         *bbolt.DB
	expireTime time.Duration
}

func Open(path string, expireTime time.Duration) (*Store, error) {
	if expireTime <= 0 {
		expireTime = 30 * time.Minute
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, expireTime: expireTime}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) ExpireTime() time.Duration {
	return s.expireTime
}

func (s *Store) init() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(tokenBucket))
		return err
	})
}

func (s *Store) Issue(clientID, remoteIP, userAgent string, clientInfo ClientInfo) (IssueResult, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return IssueResult{}, err
	}
	token := hex.EncodeToString(raw)
	hash := hashToken(token)
	now := time.Now().UTC()
	record := Token{
		Hash:       hash,
		ClientID:   clientID,
		RemoteIP:   remoteIP,
		UserAgent:  userAgent,
		ClientInfo: clientInfo,
		CreatedAt:  now,
		ExpiresAt:  now.Add(s.expireTime),
	}
	if err := s.put(record); err != nil {
		return IssueResult{}, err
	}
	return IssueResult{Token: token, ExpiresAt: record.ExpiresAt}, nil
}

func (s *Store) Validate(token string) (Token, error) {
	record, err := s.get(hashToken(token))
	if err != nil {
		return Token{}, err
	}
	if record.RevokedAt != nil {
		return Token{}, ErrRevoked
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		return Token{}, ErrExpired
	}
	return record, nil
}

func (s *Store) Refresh(token string) (time.Time, error) {
	hash := hashToken(token)
	var expiresAt time.Time
	err := s.db.Update(func(tx *bbolt.Tx) error {
		record, err := s.getLocked(tx, hash)
		if err != nil {
			return err
		}
		if record.RevokedAt != nil {
			return ErrRevoked
		}
		if time.Now().UTC().After(record.ExpiresAt) {
			return ErrExpired
		}
		record.ExpiresAt = time.Now().UTC().Add(s.expireTime)
		expiresAt = record.ExpiresAt
		return s.putLocked(tx, record)
	})
	return expiresAt, err
}

func (s *Store) Revoke(token string) error {
	hash := hashToken(token)
	return s.db.Update(func(tx *bbolt.Tx) error {
		record, err := s.getLocked(tx, hash)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		record.RevokedAt = &now
		return s.putLocked(tx, record)
	})
}

func (s *Store) put(record Token) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return s.putLocked(tx, record)
	})
}

func (s *Store) putLocked(tx *bbolt.Tx, record Token) error {
	bucket := tx.Bucket([]byte(tokenBucket))
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return bucket.Put([]byte(record.Hash), encoded)
}

func (s *Store) get(hash string) (Token, error) {
	var record Token
	err := s.db.View(func(tx *bbolt.Tx) error {
		var err error
		record, err = s.getLocked(tx, hash)
		return err
	})
	return record, err
}

func (s *Store) getLocked(tx *bbolt.Tx, hash string) (Token, error) {
	bucket := tx.Bucket([]byte(tokenBucket))
	value := bucket.Get([]byte(hash))
	if value == nil {
		return Token{}, ErrNotFound
	}
	var record Token
	if err := json.Unmarshal(value, &record); err != nil {
		return Token{}, err
	}
	record.Hash = hash
	return record, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
