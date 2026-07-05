package uploadsession

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

var (
	ErrNotFound       = errors.New("upload session not found")
	ErrExpired        = errors.New("upload session expired")
	ErrTerminated     = errors.New("upload session terminated")
	ErrOffsetMismatch = errors.New("upload offset mismatch")
	ErrCompleted      = errors.New("upload session already completed")
	ErrInvalidState   = errors.New("upload session invalid state")
)

const sessionBucket = "upload_sessions"

type Kind string

const (
	KindRegular Kind = "regular"
	KindPartial Kind = "partial"
	KindFinal   Kind = "final"
)

type Session struct {
	ID           string     `json:"id"`
	ExpectedHash string     `json:"expected_hash"`
	Kind         Kind       `json:"kind"`
	Length       int64      `json:"length"`
	Offset       int64      `json:"offset"`
	TempPath     string     `json:"temp_path"`
	PartialRefs  []string   `json:"partial_refs,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	TerminatedAt *time.Time `json:"terminated_at,omitempty"`
}

type Store struct {
	db           *bbolt.DB
	tempDir      string
	expiration   time.Duration
	cleanupEvery time.Duration
}

func Open(dbPath, tempDir string, expiration, cleanupEvery time.Duration) (*Store, error) {
	if expiration <= 0 {
		expiration = 24 * time.Hour
	}
	if cleanupEvery <= 0 {
		cleanupEvery = time.Minute
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(dbPath, 0o600, nil)
	if err != nil {
		return nil, err
	}
	store := &Store{
		db:           db,
		tempDir:      tempDir,
		expiration:   expiration,
		cleanupEvery: cleanupEvery,
	}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Expiration() time.Duration {
	return s.expiration
}

func (s *Store) CleanupInterval() time.Duration {
	return s.cleanupEvery
}

func (s *Store) init() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(sessionBucket))
		return err
	})
}

func (s *Store) Create(expectedHash string, kind Kind, length int64, partialRefs []string) (Session, error) {
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	tempPath := filepath.Join(s.tempDir, id+".upload")
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return Session{}, err
	}
	_ = file.Close()

	now := time.Now().UTC()
	session := Session{
		ID:           id,
		ExpectedHash: expectedHash,
		Kind:         kind,
		Length:       length,
		Offset:       0,
		TempPath:     tempPath,
		PartialRefs:  partialRefs,
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(s.expiration),
	}
	if err := s.put(session); err != nil {
		_ = os.Remove(tempPath)
		return Session{}, err
	}
	return session, nil
}

func (s *Store) Get(id string) (Session, error) {
	session, err := s.get(id)
	if err != nil {
		return Session{}, err
	}
	if err := s.ensureActive(session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Store) Append(id string, offset int64, reader io.Reader) (Session, error) {
	var session Session
	err := s.db.Update(func(tx *bbolt.Tx) error {
		current, err := s.getLocked(tx, id)
		if err != nil {
			return err
		}
		if err := ensureActiveLocked(current); err != nil {
			return err
		}
		if current.Kind == KindFinal {
			return ErrInvalidState
		}
		if current.Offset != offset {
			return ErrOffsetMismatch
		}
		file, err := os.OpenFile(current.TempPath, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return err
		}
		written, err := io.Copy(file, reader)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		current.Offset += written
		current.UpdatedAt = time.Now().UTC()
		current.ExpiresAt = current.UpdatedAt.Add(s.expiration)
		session = current
		return s.putLocked(tx, current)
	})
	return session, err
}

func (s *Store) MarkCompleted(id string) (Session, error) {
	var session Session
	err := s.db.Update(func(tx *bbolt.Tx) error {
		current, err := s.getLocked(tx, id)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		current.CompletedAt = &now
		current.UpdatedAt = now
		session = current
		return s.putLocked(tx, current)
	})
	return session, err
}

func (s *Store) Terminate(id string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		current, err := s.getLocked(tx, id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		current.TerminatedAt = &now
		current.UpdatedAt = now
		if err := s.putLocked(tx, current); err != nil {
			return err
		}
		if current.TempPath != "" {
			_ = os.Remove(current.TempPath)
		}
		return nil
	})
}

func (s *Store) OpenTemp(id string) (*os.File, Session, error) {
	session, err := s.Get(id)
	if err != nil {
		return nil, Session{}, err
	}
	file, err := os.Open(session.TempPath)
	if err != nil {
		return nil, Session{}, err
	}
	return file, session, nil
}

func (s *Store) BuildFinalFromPartials(expectedHash string, partials []Session) (Session, error) {
	for _, partial := range partials {
		if partial.Kind != KindPartial {
			return Session{}, fmt.Errorf("session %s is not partial", partial.ID)
		}
		if partial.CompletedAt == nil && partial.Offset != partial.Length {
			return Session{}, fmt.Errorf("partial %s is incomplete", partial.ID)
		}
		if err := s.ensureActive(partial); err != nil {
			return Session{}, err
		}
	}
	refs := make([]string, len(partials))
	var total int64
	for i, partial := range partials {
		refs[i] = partial.ID
		total += partial.Length
	}
	final, err := s.Create(expectedHash, KindFinal, total, refs)
	if err != nil {
		return Session{}, err
	}
	out, err := os.OpenFile(final.TempPath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = s.Terminate(final.ID)
		return Session{}, err
	}
	for _, partial := range partials {
		in, err := os.Open(partial.TempPath)
		if err != nil {
			out.Close()
			_ = s.Terminate(final.ID)
			return Session{}, err
		}
		_, err = io.Copy(out, in)
		_ = in.Close()
		if err != nil {
			out.Close()
			_ = s.Terminate(final.ID)
			return Session{}, err
		}
	}
	if err := out.Close(); err != nil {
		_ = s.Terminate(final.ID)
		return Session{}, err
	}
	final.Offset = total
	return s.refreshOffset(final.ID, total)
}

func (s *Store) refreshOffset(id string, offset int64) (Session, error) {
	var session Session
	err := s.db.Update(func(tx *bbolt.Tx) error {
		current, err := s.getLocked(tx, id)
		if err != nil {
			return err
		}
		current.Offset = offset
		current.UpdatedAt = time.Now().UTC()
		current.ExpiresAt = current.UpdatedAt.Add(s.expiration)
		session = current
		return s.putLocked(tx, current)
	})
	return session, err
}

func (s *Store) CleanupExpired() error {
	var expired []Session
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(sessionBucket))
		now := time.Now().UTC()
		return bucket.ForEach(func(key, value []byte) error {
			var session Session
			if err := json.Unmarshal(value, &session); err != nil {
				return err
			}
			if session.TerminatedAt != nil || session.CompletedAt != nil {
				return nil
			}
			if now.After(session.ExpiresAt) {
				expired = append(expired, session)
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	for _, session := range expired {
		_ = s.Terminate(session.ID)
	}
	return nil
}

func (s *Store) ensureActive(session Session) error {
	return ensureActiveLocked(session)
}

func ensureActiveLocked(session Session) error {
	if session.TerminatedAt != nil {
		return ErrTerminated
	}
	if session.CompletedAt != nil {
		return ErrCompleted
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		return ErrExpired
	}
	return nil
}

func (s *Store) put(session Session) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return s.putLocked(tx, session)
	})
}

func (s *Store) putLocked(tx *bbolt.Tx, session Session) error {
	bucket := tx.Bucket([]byte(sessionBucket))
	encoded, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return bucket.Put([]byte(session.ID), encoded)
}

func (s *Store) get(id string) (Session, error) {
	var session Session
	err := s.db.View(func(tx *bbolt.Tx) error {
		var err error
		session, err = s.getLocked(tx, id)
		return err
	})
	return session, err
}

func (s *Store) getLocked(tx *bbolt.Tx, id string) (Session, error) {
	bucket := tx.Bucket([]byte(sessionBucket))
	value := bucket.Get([]byte(id))
	if value == nil {
		return Session{}, ErrNotFound
	}
	var session Session
	if err := json.Unmarshal(value, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Store) GetByRef(ref string) (Session, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Session{}, ErrNotFound
	}
	if idx := strings.Index(ref, "upload_id="); idx >= 0 {
		ref = ref[idx+len("upload_id="):]
		if amp := strings.IndexByte(ref, '&'); amp >= 0 {
			ref = ref[:amp]
		}
	}
	return s.Get(ref)
}

func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
