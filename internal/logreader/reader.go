package logreader

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLimit = 100
	maxLimit     = 500
)

var (
	startupPattern = regexp.MustCompile(`=== vaps startup started_at=([^ ]+) ===`)
	stoppedPattern = regexp.MustCompile(`vaps stopped exit_code=(-?\d+)`)
	logTimeLayout  = "2006/01/02 15:04:05"
)

type Session struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	LogCount  int        `json:"log_count"`
}

type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

type Query struct {
	Status    string
	SessionID string
	Search    string
	Level     string
	Limit     int
	Offset    int
	Newest    bool
}

type Result[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type Reader struct {
	dir string
}

func New(dir string) *Reader {
	return &Reader{dir: dir}
}

func (r *Reader) Sessions(query Query) (Result[Session], error) {
	sessions, _, err := r.read()
	if err != nil {
		return Result[Session]{}, err
	}
	status := strings.ToLower(strings.TrimSpace(query.Status))
	search := strings.ToLower(strings.TrimSpace(query.Search))
	filtered := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		if status != "" && status != "all" && session.Status != status {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(session.ID), search) {
			continue
		}
		filtered = append(filtered, session)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].StartedAt.After(filtered[j].StartedAt)
	})
	return page(filtered, query.Limit, query.Offset), nil
}

func (r *Reader) Log(query Query) (Result[Entry], error) {
	sessions, entries, err := r.read()
	if err != nil {
		return Result[Entry]{}, err
	}
	session, ok := sessionByID(sessions, query.SessionID)
	if !ok {
		return Result[Entry]{}, fmt.Errorf("session %q not found", query.SessionID)
	}
	search := strings.ToLower(strings.TrimSpace(query.Search))
	level := strings.ToUpper(strings.TrimSpace(query.Level))
	filtered := make([]Entry, 0, session.LogCount)
	for _, entry := range entries[query.SessionID] {
		if level != "" && level != "ALL" && entry.Level != level {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(entry.Message), search) {
			continue
		}
		filtered = append(filtered, entry)
	}
	if query.Newest {
		reverse(filtered)
	}
	return page(filtered, query.Limit, query.Offset), nil
}

func sessionByID(sessions []Session, id string) (Session, bool) {
	for _, session := range sessions {
		if session.ID == id {
			return session, true
		}
	}
	return Session{}, false
}

func (r *Reader) read() ([]Session, map[string][]Entry, error) {
	if strings.TrimSpace(r.dir) == "" {
		return []Session{}, map[string][]Entry{}, nil
	}
	files, err := filepath.Glob(filepath.Join(r.dir, "vaps-*.log"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	sessions := []Session{}
	entries := map[string][]Entry{}
	var current *Session
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			timestamp, message := parseLine(scanner.Text())
			if match := startupPattern.FindStringSubmatch(message); match != nil {
				startedAt, parseErr := time.Parse(time.RFC3339, match[1])
				if parseErr != nil {
					startedAt = timestamp
				}
				id := fmt.Sprintf("session-%d", startedAt.UnixNano())
				sessions = append(sessions, Session{ID: id, Status: "active", StartedAt: startedAt})
				current = &sessions[len(sessions)-1]
				entries[id] = append(entries[id], Entry{Timestamp: timestamp, Level: "INFO", Message: message})
				current.LogCount++
				continue
			}
			if current == nil {
				continue
			}
			entry := Entry{Timestamp: timestamp, Level: classify(message), Message: message}
			entries[current.ID] = append(entries[current.ID], entry)
			current.LogCount++
			if match := stoppedPattern.FindStringSubmatch(message); match != nil {
				exitCode, _ := strconv.Atoi(match[1])
				current.Status = "ended"
				current.EndedAt = timePtr(timestamp)
				current.ExitCode = &exitCode
				current = nil
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return nil, nil, scanErr
		}
		if closeErr != nil {
			return nil, nil, closeErr
		}
	}
	return sessions, entries, nil
}

func parseLine(line string) (time.Time, string) {
	if len(line) >= len(logTimeLayout) {
		if timestamp, err := time.ParseInLocation(logTimeLayout, line[:len(logTimeLayout)], time.Local); err == nil {
			return timestamp, strings.TrimSpace(line[len(logTimeLayout):])
		}
	}
	return time.Time{}, strings.TrimSpace(line)
}

func classify(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "panic"), strings.Contains(lower, "fatal"),
		strings.Contains(lower, " failed"), strings.Contains(lower, "error="),
		strings.Contains(lower, " error"):
		return "ERROR"
	case strings.Contains(lower, "warning"), strings.Contains(lower, " warn"),
		strings.Contains(lower, "shutdown signal"):
		return "WARN"
	default:
		return "INFO"
	}
}

func page[T any](items []T, limit, offset int) Result[T] {
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 {
		offset = 0
	}
	result := Result[T]{Items: []T{}, Total: len(items), Limit: limit, Offset: offset}
	if offset >= len(items) {
		return result
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result.Items = append(result.Items, items[offset:end]...)
	return result
}

func reverse[T any](items []T) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
