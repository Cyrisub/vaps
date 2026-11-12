package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

type ReplicationStatus struct {
	PendingObjects int    `json:"pending_objects"`
	LastError      string `json:"last_error,omitempty"`
}

type ReplicationReporter interface{ ReplicationStatus() ReplicationStatus }

// AsyncStore commits the preferred backend synchronously and durably queues
// immutable bytes locally before acknowledging an upload. Workers write every
// additional backend, retaining the job across failures and process restarts.
type AsyncStore struct {
	*MultiStore
	queue     *FilesystemStore
	workers   int
	retry     time.Duration
	wake      chan struct{}
	mu        sync.Mutex
	pending   map[string]time.Time
	lastError string
	jobLocks  [256]sync.Mutex
	done      chan struct{}
}

func NewAsync(ctx context.Context, backends []Backend, queueDir string, workers int, retry time.Duration) (*AsyncStore, error) {
	multi, err := NewMulti(backends)
	if err != nil {
		return nil, err
	}
	if len(backends) < 2 || workers < 1 || retry <= 0 {
		return nil, errors.New("asynchronous storage requires multiple backends, positive workers and retry interval")
	}
	queue, err := NewFilesystem(queueDir, "")
	if err != nil {
		return nil, fmt.Errorf("open replication queue: %w", err)
	}
	jobs, err := queue.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("load replication queue: %w", err)
	}
	s := &AsyncStore{MultiStore: multi, queue: queue, workers: workers, retry: retry, wake: make(chan struct{}, 1), pending: make(map[string]time.Time), done: make(chan struct{})}
	for _, job := range jobs {
		s.pending[job.Hash] = time.Time{}
	}
	go s.run(ctx)
	return s, nil
}

func (s *AsyncStore) lock(hash string) *sync.Mutex {
	var index byte
	for i := range hash {
		index ^= hash[i]
	}
	return &s.jobLocks[index]
}

func (s *AsyncStore) HeadForWrite(ctx context.Context, hash string) (Info, error) {
	info, err := s.backends[0].Store.Head(ctx, hash)
	info.Backend = s.backends[0].Name
	return info, err
}

func (s *AsyncStore) Put(ctx context.Context, info Info, reader io.Reader) (Info, error) {
	if err := validateInfo(info); err != nil {
		return Info{}, err
	}
	lock := s.lock(info.Hash)
	lock.Lock()
	defer lock.Unlock()
	// Queue.Put verifies CRC32C and flushes bytes and metadata before publishing.
	if _, err := s.queue.Put(ctx, info, reader); err != nil {
		return Info{}, fmt.Errorf("persist replication job: %w", err)
	}
	s.mu.Lock()
	s.pending[info.Hash] = time.Time{}
	s.mu.Unlock()
	stored, err := s.backends[0].Store.Head(ctx, info.Hash)
	if errors.Is(err, ErrNotFound) || (err == nil && stored.LegacyChecksum != "") {
		file, _, openErr := s.queue.Open(ctx, info.Hash)
		if openErr != nil {
			return Info{}, openErr
		}
		stored, err = s.backends[0].Store.Put(ctx, info, file)
		closeErr := file.Close()
		err = errors.Join(err, closeErr)
	} else if err == nil && !samePayload(info, stored) {
		err = ErrIntegrityMismatch
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	if err != nil {
		return Info{}, fmt.Errorf("write preferred backend %s: %w", s.backends[0].Name, err)
	}
	stored.Backend = s.backends[0].Name
	stored.ETags = map[string]string{stored.Backend: stored.ETag}
	return stored, nil
}

func (s *AsyncStore) ReplicationStatus() ReplicationStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ReplicationStatus{PendingObjects: len(s.pending), LastError: s.lastError}
}

// Wait stops only when the context supplied to NewAsync is canceled and all
// active workers have returned. Queued jobs remain on disk for the next start.
func (s *AsyncStore) Wait() { <-s.done }

func (s *AsyncStore) run(ctx context.Context) {
	defer close(s.done)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-timer.C:
		}
		s.mu.Lock()
		hashes := make([]string, 0, len(s.pending))
		for hash, next := range s.pending {
			if !time.Now().Before(next) {
				hashes = append(hashes, hash)
			}
		}
		s.mu.Unlock()
		jobs := make(chan string)
		var wg sync.WaitGroup
		for range s.workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for hash := range jobs {
					if ctx.Err() != nil {
						return
					}
					jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
					err := s.replicate(jobCtx, hash)
					cancel()
					if err != nil && ctx.Err() == nil {
						s.mu.Lock()
						s.lastError = err.Error()
						s.pending[hash] = time.Now().Add(s.retry)
						s.mu.Unlock()
						log.Printf("persistent replication pending hash=%s error=%q", hash, err)
					}
				}
			}()
		}
	feed:
		for _, hash := range hashes {
			select {
			case jobs <- hash:
			case <-ctx.Done():
				break feed
			}
		}
		close(jobs)
		wg.Wait()
		if ctx.Err() != nil {
			return
		}
		timer.Reset(s.retry)
	}
}

func (s *AsyncStore) replicate(ctx context.Context, hash string) error {
	info, err := s.queue.Head(ctx, hash)
	if err != nil {
		return err
	}
	// Retry the preferred backend too: a process may have crashed after queueing
	// the bytes and before completing its preferred write.
	var failures []error
	for _, backend := range s.backends {
		stored, err := backend.Store.Head(ctx, hash)
		if err == nil && stored.LegacyChecksum == "" {
			if !samePayload(info, stored) {
				failures = append(failures, fmt.Errorf("backend %s: %w", backend.Name, ErrIntegrityMismatch))
			}
			continue
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			failures = append(failures, fmt.Errorf("backend %s: %w", backend.Name, err))
			continue
		}
		file, _, err := s.queue.Open(ctx, hash)
		if err != nil {
			return err
		}
		_, err = backend.Store.Put(ctx, info, file)
		closeErr := file.Close()
		if err := errors.Join(err, closeErr); err != nil {
			failures = append(failures, fmt.Errorf("replicate backend %s: %w", backend.Name, err))
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	lock := s.lock(hash)
	lock.Lock()
	defer lock.Unlock()
	if err := s.queue.remove(hash); err != nil {
		return fmt.Errorf("complete replication job: %w", err)
	}
	s.mu.Lock()
	delete(s.pending, hash)
	if len(s.pending) == 0 {
		s.lastError = ""
	}
	s.mu.Unlock()
	log.Printf("persistent replication completed hash=%s backends=%d", hash, len(s.backends))
	return nil
}

var _ MirroredStore = (*AsyncStore)(nil)
