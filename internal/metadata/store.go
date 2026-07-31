package metadata

import (
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

var (
	ErrNotFound      = errors.New("metadata record not found")
	ErrDashboardBusy = errors.New("dashboard query is already running")
)

const (
	payloadBucket = "payloads"
)

type PayloadStatus uint8

const (
	// StatusStored means the object is durably present in S3 but not currently
	// available from the local disk cache.
	StatusStored PayloadStatus = iota
	// StatusCached means the S3 object also has a local disk-cache entry.
	StatusCached
)

func (s PayloadStatus) IsCached() bool {
	return s == StatusCached
}

type Payload struct {
	Hash           string        `json:"hash"`
	ETag           string        `json:"etag,omitempty"`
	Checksum       string        `json:"checksum"`
	LegacyChecksum string        `json:"-"`
	Size           int64         `json:"size"`
	Status         PayloadStatus `json:"status"`
	CreatedAt      *time.Time    `json:"created_at"`
	LastAccessedAt *time.Time    `json:"last_accessed_at,omitempty"`
}

func (p *Payload) UnmarshalJSON(data []byte) error {
	var value struct {
		Hash           string        `json:"hash"`
		ETag           string        `json:"etag"`
		Checksum       string        `json:"checksum"`
		LegacyChecksum string        `json:"content_hash"`
		Size           int64         `json:"size"`
		Status         PayloadStatus `json:"status"`
		CreatedAt      *time.Time    `json:"created_at"`
		LastAccessedAt *time.Time    `json:"last_accessed_at,omitempty"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = Payload{
		Hash:           value.Hash,
		ETag:           value.ETag,
		Checksum:       value.Checksum,
		LegacyChecksum: value.LegacyChecksum,
		Size:           value.Size,
		Status:         value.Status,
		CreatedAt:      value.CreatedAt,
		LastAccessedAt: value.LastAccessedAt,
	}
	return nil
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
	SortBy       string
	SortOrder    string
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

	dashboardMu       sync.RWMutex
	dashboardPayloads map[string]Payload
	dashboardQueryMu  sync.Mutex
	dashboardQuerying bool
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, dashboardPayloads: make(map[string]Payload)}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.loadDashboardIndex(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) PutPayload(payload Payload) error {
	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(payloadBucket))
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(payload.Hash), encoded)
	})
	if err != nil {
		return err
	}
	s.dashboardMu.Lock()
	s.dashboardPayloads[payload.Hash] = payload
	s.dashboardMu.Unlock()
	return nil
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
	s.dashboardMu.RLock()
	for _, payload := range s.dashboardPayloads {
		stats.PayloadCount++
		stats.TotalBytes += payload.Size
		if payload.Status.IsCached() {
			stats.CachedCount++
		}
	}
	s.dashboardMu.RUnlock()
	return stats, nil
}

func (s *Store) QueryPayloads(query PayloadQuery) (PayloadQueryResult, error) {
	if query.Offset < 0 {
		query.Offset = 0
	}
	sortBy, sortOrder := normalizePayloadSort(query.SortBy, query.SortOrder)
	result := PayloadQueryResult{Limit: query.Limit, Offset: query.Offset}
	s.dashboardQueryMu.Lock()
	if s.dashboardQuerying {
		s.dashboardQueryMu.Unlock()
		return PayloadQueryResult{}, ErrDashboardBusy
	}
	s.dashboardQuerying = true
	s.dashboardQueryMu.Unlock()
	defer func() {
		s.dashboardQueryMu.Lock()
		s.dashboardQuerying = false
		s.dashboardQueryMu.Unlock()
	}()

	matched := make([]Payload, 0)
	s.dashboardMu.RLock()
	for _, payload := range s.dashboardPayloads {
		if query.matches(payload) {
			matched = append(matched, payload)
		}
	}
	s.dashboardMu.RUnlock()
	sortPayloads(matched, sortBy, sortOrder)
	result.Total = int64(len(matched))
	if query.Offset >= len(matched) {
		return result, nil
	}
	end := len(matched)
	if query.Limit > 0 && query.Offset+query.Limit < end {
		end = query.Offset + query.Limit
	}
	result.Items = matched[query.Offset:end]
	return result, nil
}

func normalizePayloadSort(sortBy, sortOrder string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "size":
		sortBy = "size"
	default:
		sortBy = "created"
	}
	switch strings.ToLower(strings.TrimSpace(sortOrder)) {
	case "asc":
		sortOrder = "asc"
	default:
		sortOrder = "desc"
	}
	return sortBy, sortOrder
}

func sortPayloads(items []Payload, sortBy, sortOrder string) {
	desc := sortOrder != "asc"
	sort.SliceStable(items, func(i, j int) bool {
		switch sortBy {
		case "size":
			if items[i].Size != items[j].Size {
				if desc {
					return items[i].Size > items[j].Size
				}
				return items[i].Size < items[j].Size
			}
		default:
			tiNil := items[i].CreatedAt == nil
			tjNil := items[j].CreatedAt == nil
			if tiNil != tjNil {
				return tjNil // nils last
			}
			if !tiNil {
				ti := items[i].CreatedAt.UnixNano()
				tj := items[j].CreatedAt.UnixNano()
				if ti != tj {
					if desc {
						return ti > tj
					}
					return ti < tj
				}
			}
		}
		if desc {
			return items[i].Hash > items[j].Hash
		}
		return items[i].Hash < items[j].Hash
	})
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

func (s *Store) loadDashboardIndex() error {
	return s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(payloadBucket))
		return bucket.ForEach(func(_, value []byte) error {
			var payload Payload
			if err := json.Unmarshal(value, &payload); err != nil {
				return err
			}
			s.dashboardPayloads[payload.Hash] = payload
			return nil
		})
	})
}

func (q PayloadQuery) matches(payload Payload) bool {
	if q.HashContains != "" && !strings.Contains(payload.Hash, q.HashContains) {
		return false
	}
	if q.RequireLocal && !payload.Status.IsCached() {
		return false
	}
	if q.RequireCache && !payload.Status.IsCached() {
		return false
	}
	if q.Backup != nil && !*q.Backup {
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
