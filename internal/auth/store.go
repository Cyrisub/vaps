// Package auth stores optional client source registrations. Tokens are tracing
// identifiers only: they never expire and do not authorize access.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

var ErrNotFound = errors.New("source token not found")

const tokenBucket = "auth_tokens"

type ClientInfo map[string]string

type Token struct {
	Hash       string     `json:"-"`
	Token      string     `json:"token,omitempty"`
	ClientID   string     `json:"client_id"`
	RemoteIP   string     `json:"remote_ip"`
	UserAgent  string     `json:"user_agent"`
	ClientInfo ClientInfo `json:"client_info"`
	CreatedAt  time.Time  `json:"created_at"`

	// Retained to read existing databases; lifecycle fields have no effect.
	ExpiresAt time.Time  `json:"expires_at"`
	ParkedAt  *time.Time `json:"parked_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	Activity  Activity   `json:"-"`
}

// Activity is process-local so tracing does not write to bbolt on every request.
type Activity struct {
	LastSeenAt   *time.Time
	RequestCount uint64
	RemoteIP     string
	UserAgent    string
}

type Stats struct {
	TotalCount   int `json:"total_count"`
	TrackedCount int `json:"tracked_count"`
	// Legacy counters remain for clients reading dashboard statistics.
	ActiveCount  int `json:"active_count"`
	ParkedCount  int `json:"parked_count"`
	ExpiredCount int `json:"expired_count"`
	RevokedCount int `json:"revoked_count"`
}

type IssueResult struct {
	Token string `json:"token"`
	// Null explicitly signals that the source identifier has no expiry.
	ExpiresAt *time.Time `json:"expires_at"`
}

type Store struct {
	db         *bbolt.DB
	activityMu sync.RWMutex
	activity   map[string]Activity
}

// Open accepts the legacy expiry argument for existing callers, but ignores it.
func Open(path string, _ time.Duration) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, activity: make(map[string]Activity)}
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(tokenBucket))
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Issue(clientID, remoteIP, userAgent string, clientInfo ClientInfo) (IssueResult, error) {
	var result IssueResult
	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(tokenBucket))
		var record Token
		if err := bucket.ForEach(func(k, v []byte) error {
			var candidate Token
			if err := json.Unmarshal(v, &candidate); err != nil {
				return err
			}
			if candidate.ClientID == clientID && sameClientIP(candidate.RemoteIP, remoteIP) &&
				candidate.Token != "" && (record.Token == "" || candidate.CreatedAt.After(record.CreatedAt)) {
				candidate.Hash = string(k)
				record = candidate
			}
			return nil
		}); err != nil {
			return err
		}
		if record.Token == "" {
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				return err
			}
			record = Token{Token: hex.EncodeToString(raw), ClientID: clientID, CreatedAt: time.Now().UTC()}
			record.Hash = HashToken(record.Token)
		}
		record.RemoteIP = remoteIP
		if userAgent != "" {
			record.UserAgent = userAgent
		}
		if clientInfo != nil {
			record.ClientInfo = clientInfo
		}
		record.ExpiresAt = time.Time{}
		record.ParkedAt = nil
		record.RevokedAt = nil
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte(record.Hash), encoded); err != nil {
			return err
		}
		result.Token = record.Token
		return nil
	})
	return result, err
}

// Validate is a lookup only, including for legacy expired/parked/revoked records.
func (s *Store) Validate(token string) (Token, error) {
	return s.LookupByHash(HashToken(token))
}

// Observe resolves a registered source and counts a request without a DB write.
// Unknown tokens are never registered implicitly.
func (s *Store) Observe(hash, remoteIP, userAgent string) (Token, error) {
	var record Token
	err := s.db.View(func(tx *bbolt.Tx) error {
		var err error
		record, err = readToken(tx, hash)
		if err != nil {
			return err
		}
		s.activityMu.Lock()
		now := time.Now().UTC()
		activity := s.activity[hash]
		activity.RequestCount++
		activity.LastSeenAt = &now
		activity.RemoteIP = remoteIP
		activity.UserAgent = userAgent
		s.activity[hash] = activity
		s.activityMu.Unlock()
		record.Activity = activity
		return nil
	})
	return record, err
}

func (s *Store) DeleteAll() (int, error) {
	deleted := 0
	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(tokenBucket))
		deleted = bucket.Stats().KeyN
		if err := tx.DeleteBucket([]byte(tokenBucket)); err != nil {
			return err
		}
		if _, err := tx.CreateBucket([]byte(tokenBucket)); err != nil {
			return err
		}
		s.activityMu.Lock()
		clear(s.activity)
		s.activityMu.Unlock()
		return nil
	})
	return deleted, err
}

func (s *Store) ListAll() ([]Token, error) {
	var tokens []Token
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(tokenBucket)).ForEach(func(k, v []byte) error {
			var record Token
			if err := json.Unmarshal(v, &record); err != nil {
				return err
			}
			record.Hash = string(k)
			s.activityMu.RLock()
			record.Activity = s.activity[record.Hash]
			s.activityMu.RUnlock()
			tokens = append(tokens, record)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(tokens, func(i, j int) bool {
		return tokens[i].CreatedAt.After(tokens[j].CreatedAt)
	})
	return tokens, nil
}

func (s *Store) Stats() (Stats, error) {
	tokens, err := s.ListAll()
	if err != nil {
		return Stats{}, err
	}
	return Stats{TotalCount: len(tokens), TrackedCount: len(tokens), ActiveCount: len(tokens)}, nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) LookupByHash(hash string) (Token, error) {
	var record Token
	err := s.db.View(func(tx *bbolt.Tx) error {
		var err error
		record, err = readToken(tx, hash)
		return err
	})
	return record, err
}

func readToken(tx *bbolt.Tx, hash string) (Token, error) {
	value := tx.Bucket([]byte(tokenBucket)).Get([]byte(hash))
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

func sameClientIP(a, b string) bool {
	return normalizeClientIP(a) == normalizeClientIP(b)
}

func normalizeClientIP(ip string) string {
	ip = strings.TrimSpace(ip)
	ip = strings.TrimPrefix(ip, "[")
	ip = strings.TrimSuffix(ip, "]")
	switch strings.ToLower(ip) {
	case "localhost", "::1", "127.0.0.1", "::ffff:127.0.0.1":
		return "localhost"
	default:
		return ip
	}
}
