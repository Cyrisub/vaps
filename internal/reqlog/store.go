package reqlog

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultMaxRecords = 10000

type Record struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	Message   string    `json:"message"`
	RemoteIP  string    `json:"remote_ip"`
	UserAgent string    `json:"user_agent"`
	TokenHash string    `json:"token_hash,omitempty"`
	ClientID  string    `json:"client_id,omitempty"`
}

type Query struct {
	TokenHash        string
	Path             string
	MinStatus        int
	MaxStatus        int
	Limit            int
	Offset           int
	ExcludeDashboard bool
}

type QueryResult struct {
	Items  []Record `json:"items"`
	Total  int64    `json:"total"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
}

type Stats struct {
	TotalCount   int64 `json:"total_count"`
	ClientErrors int64 `json:"client_errors"`
	ServerErrors int64 `json:"server_errors"`
}

type Store struct {
	mu          sync.RWMutex
	records     []Record
	max         int
	nextID      int64
	total       int64
	client      int64
	server      int64
	panelTotal  int64
	panelClient int64
	panelServer int64
}

func New(maxRecords int) *Store {
	if maxRecords <= 0 {
		maxRecords = defaultMaxRecords
	}
	return &Store{max: maxRecords}
}

func (s *Store) Record(record Record) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	s.total++
	dashboard := isDashboardPath(record.Path)
	if record.Status >= 500 {
		s.server++
		if !dashboard {
			s.panelServer++
		}
	} else if record.Status >= 400 {
		s.client++
		if !dashboard {
			s.panelClient++
		}
	}
	if !dashboard {
		s.panelTotal++
	}

	record.ID = s.nextID
	if record.At.IsZero() {
		record.At = time.Now().UTC()
	}
	record.Message = truncateMessage(record.Message, 512)

	s.records = append(s.records, record)
	if len(s.records) > s.max {
		s.records = s.records[len(s.records)-s.max:]
	}
}

func (s *Store) Stats() Stats {
	return s.StatsFiltered(true)
}

func (s *Store) StatsFiltered(excludeDashboard bool) Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if excludeDashboard {
		return Stats{
			TotalCount:   s.panelTotal,
			ClientErrors: s.panelClient,
			ServerErrors: s.panelServer,
		}
	}
	return Stats{
		TotalCount:   s.total,
		ClientErrors: s.client,
		ServerErrors: s.server,
	}
}

func (s *Store) CountByTokenHash() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := map[string]int64{}
	for _, record := range s.records {
		if record.TokenHash == "" {
			continue
		}
		counts[record.TokenHash]++
	}
	return counts
}

func (s *Store) Query(query Query) QueryResult {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}

	s.mu.RLock()
	matched := make([]Record, 0, len(s.records))
	for i := len(s.records) - 1; i >= 0; i-- {
		record := s.records[i]
		if !query.matches(record) {
			continue
		}
		matched = append(matched, record)
	}
	s.mu.RUnlock()

	total := int64(len(matched))
	if offset >= len(matched) {
		return QueryResult{Items: []Record{}, Total: total, Limit: limit, Offset: offset}
	}
	end := offset + limit
	if end > len(matched) {
		end = len(matched)
	}
	items := append([]Record(nil), matched[offset:end]...)
	return QueryResult{Items: items, Total: total, Limit: limit, Offset: offset}
}

func isDashboardPath(path string) bool {
	return strings.HasPrefix(strings.TrimSpace(path), "/dashboard")
}

func (q Query) matches(record Record) bool {
	if q.ExcludeDashboard && isDashboardPath(record.Path) {
		return false
	}
	if q.TokenHash != "" && record.TokenHash != q.TokenHash {
		return false
	}
	if q.Path != "" && !strings.Contains(strings.ToLower(record.Path), strings.ToLower(q.Path)) {
		return false
	}
	if q.MinStatus > 0 && record.Status < q.MinStatus {
		return false
	}
	if q.MaxStatus > 0 && record.Status > q.MaxStatus {
		return false
	}
	return true
}

func truncateMessage(message string, maxLen int) string {
	message = strings.TrimSpace(message)
	if maxLen <= 0 || len(message) <= maxLen {
		return message
	}
	return message[:maxLen] + "..."
}

type TokenErrorCount struct {
	TokenHash string
	Count     int64
}

func SortTokenErrorCounts(counts map[string]int64) []TokenErrorCount {
	items := make([]TokenErrorCount, 0, len(counts))
	for hash, count := range counts {
		items = append(items, TokenErrorCount{TokenHash: hash, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].TokenHash < items[j].TokenHash
		}
		return items[i].Count > items[j].Count
	})
	return items
}
