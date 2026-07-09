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
	"time"

	"go.etcd.io/bbolt"
)

var (
	ErrNotFound = errors.New("auth token not found")
	ErrExpired  = errors.New("auth token expired")
	ErrRevoked  = errors.New("auth token revoked")
	ErrParked   = errors.New("auth token parked")
)

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
	ExpiresAt  time.Time  `json:"expires_at"`
	ParkedAt   *time.Time `json:"parked_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type Stats struct {
	TotalCount   int `json:"total_count"`
	ActiveCount  int `json:"active_count"`
	ParkedCount  int `json:"parked_count"`
	ExpiredCount int `json:"expired_count"`
	RevokedCount int `json:"revoked_count"`
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
	var result IssueResult
	err := s.db.Update(func(tx *bbolt.Tx) error {
		now := time.Now().UTC()
		if existing, ok, err := s.findByStatusLocked(tx, clientID, remoteIP, now, "active"); err != nil {
			return err
		} else if ok {
			if existing.Token != "" {
				existing.ExpiresAt = now.Add(s.expireTime)
				existing.RemoteIP = remoteIP
				existing.ParkedAt = nil
				if userAgent != "" {
					existing.UserAgent = userAgent
				}
				if clientInfo != nil {
					existing.ClientInfo = clientInfo
				}
				if err := s.putLocked(tx, existing); err != nil {
					return err
				}
				result = IssueResult{Token: existing.Token, ExpiresAt: existing.ExpiresAt}
				return nil
			}
			// Legacy records without a stored plaintext token cannot be reused.
			revokedAt := now
			existing.RevokedAt = &revokedAt
			if err := s.putLocked(tx, existing); err != nil {
				return err
			}
		}

		if existing, ok, err := s.findByStatusLocked(tx, clientID, remoteIP, now, "parked"); err != nil {
			return err
		} else if ok {
			if existing.Token != "" {
				existing.ParkedAt = nil
				existing.ExpiresAt = now.Add(s.expireTime)
				existing.RemoteIP = remoteIP
				if userAgent != "" {
					existing.UserAgent = userAgent
				}
				if clientInfo != nil {
					existing.ClientInfo = clientInfo
				}
				if err := s.putLocked(tx, existing); err != nil {
					return err
				}
				result = IssueResult{Token: existing.Token, ExpiresAt: existing.ExpiresAt}
				return nil
			}
			revokedAt := now
			existing.RevokedAt = &revokedAt
			if err := s.putLocked(tx, existing); err != nil {
				return err
			}
		}

		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		token := hex.EncodeToString(raw)
		record := Token{
			Hash:       hashToken(token),
			Token:      token,
			ClientID:   clientID,
			RemoteIP:   remoteIP,
			UserAgent:  userAgent,
			ClientInfo: clientInfo,
			CreatedAt:  now,
			ExpiresAt:  now.Add(s.expireTime),
		}
		if err := s.putLocked(tx, record); err != nil {
			return err
		}
		result = IssueResult{Token: token, ExpiresAt: record.ExpiresAt}
		return nil
	})
	return result, err
}

func (s *Store) Validate(token string) (Token, error) {
	record, err := s.get(hashToken(token))
	if err != nil {
		return Token{}, err
	}
	switch TokenStatus(record, time.Now().UTC()) {
	case "revoked":
		return Token{}, ErrRevoked
	case "parked":
		return Token{}, ErrParked
	case "expired":
		return Token{}, ErrExpired
	default:
		return record, nil
	}
}

func (s *Store) Refresh(token string) (time.Time, error) {
	hash := hashToken(token)
	var expiresAt time.Time
	err := s.db.Update(func(tx *bbolt.Tx) error {
		record, err := s.getLocked(tx, hash)
		if err != nil {
			return err
		}
		switch TokenStatus(record, time.Now().UTC()) {
		case "revoked":
			return ErrRevoked
		case "parked":
			return ErrParked
		case "expired":
			return ErrExpired
		}
		record.ExpiresAt = time.Now().UTC().Add(s.expireTime)
		expiresAt = record.ExpiresAt
		return s.putLocked(tx, record)
	})
	return expiresAt, err
}

func (s *Store) Park(token string) error {
	hash := hashToken(token)
	return s.db.Update(func(tx *bbolt.Tx) error {
		record, err := s.getLocked(tx, hash)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		switch TokenStatus(record, now) {
		case "revoked":
			return ErrRevoked
		case "parked":
			return ErrParked
		case "expired":
			return ErrExpired
		}
		record.ParkedAt = &now
		return s.putLocked(tx, record)
	})
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
		record.ParkedAt = nil
		return s.putLocked(tx, record)
	})
}

func (s *Store) DeleteExpired() (int, error) {
	deleted := 0
	now := time.Now().UTC()
	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(tokenBucket))
		if bucket == nil {
			return nil
		}
		var keys [][]byte
		if err := bucket.ForEach(func(k, v []byte) error {
			var record Token
			if err := json.Unmarshal(v, &record); err != nil {
				return err
			}
			record.Hash = string(k)
			if TokenStatus(record, now) == "expired" {
				keys = append(keys, append([]byte(nil), k...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, key := range keys {
			if err := bucket.Delete(key); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	return deleted, err
}

func (s *Store) DeleteAll() (int, error) {
	deleted := 0
	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(tokenBucket))
		if bucket == nil {
			return nil
		}
		deleted = bucket.Stats().KeyN
		return tx.DeleteBucket([]byte(tokenBucket))
	})
	if err != nil {
		return 0, err
	}
	if err := s.init(); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func (s *Store) ListAll() ([]Token, error) {
	var tokens []Token
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(tokenBucket))
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(k, v []byte) error {
			var record Token
			if err := json.Unmarshal(v, &record); err != nil {
				return err
			}
			record.Hash = string(k)
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
	stats := Stats{TotalCount: len(tokens)}
	now := time.Now().UTC()
	for _, token := range tokens {
		switch TokenStatus(token, now) {
		case "active":
			stats.ActiveCount++
		case "parked":
			stats.ParkedCount++
		case "expired":
			stats.ExpiredCount++
		case "revoked":
			stats.RevokedCount++
		}
	}
	return stats, nil
}

func TokenStatus(token Token, now time.Time) string {
	if token.RevokedAt != nil {
		return "revoked"
	}
	if token.ParkedAt != nil {
		return "parked"
	}
	if now.After(token.ExpiresAt) {
		return "expired"
	}
	return "active"
}

func HashToken(token string) string {
	return hashToken(token)
}

func (s *Store) LookupByHash(hash string) (Token, error) {
	return s.get(hash)
}

func (s *Store) findByStatusLocked(tx *bbolt.Tx, clientID, remoteIP string, now time.Time, status string) (Token, bool, error) {
	bucket := tx.Bucket([]byte(tokenBucket))
	if bucket == nil {
		return Token{}, false, nil
	}
	var best Token
	found := false
	err := bucket.ForEach(func(k, v []byte) error {
		var record Token
		if err := json.Unmarshal(v, &record); err != nil {
			return err
		}
		record.Hash = string(k)
		if record.ClientID != clientID || !sameClientIP(record.RemoteIP, remoteIP) {
			return nil
		}
		if TokenStatus(record, now) != status {
			return nil
		}
		if !found || record.CreatedAt.After(best.CreatedAt) {
			best = record
			found = true
		}
		return nil
	})
	if err != nil {
		return Token{}, false, err
	}
	return best, found, nil
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

func sameClientIP(a, b string) bool {
	return normalizeClientIP(a) == normalizeClientIP(b)
}

func normalizeClientIP(ip string) string {
	ip = strings.TrimSpace(ip)
	ip = strings.TrimPrefix(ip, "[")
	ip = strings.TrimSuffix(ip, "]")
	if ip == "" {
		return ""
	}
	switch strings.ToLower(ip) {
	case "localhost", "::1", "127.0.0.1", "::ffff:127.0.0.1":
		return "localhost"
	default:
		return ip
	}
}
