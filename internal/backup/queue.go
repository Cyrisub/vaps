package backup

import (
	"context"
	"io"
	"log"
	"sync"
	"time"
)

type OpenFunc func(hash string) (io.ReadCloser, error)

type QueueConfig struct {
	Interval   time.Duration
	MaxPending int
}

type QueueCallbacks struct {
	OnPending  func(hash string)
	OnBackuped func(hash string)
	OnFailed   func(hash string, err error)
}

type Queue struct {
	backend   Backend
	open      OpenFunc
	interval  time.Duration
	max       int
	callbacks QueueCallbacks

	mu      sync.Mutex
	pending map[string]struct{}
	order   []string
	flushCh chan struct{}
	doneCh  chan struct{}
}

func NewQueue(backend Backend, open OpenFunc, cfg QueueConfig, callbacks QueueCallbacks) *Queue {
	if cfg.Interval < 0 {
		cfg.Interval = 0
	}
	return &Queue{
		backend:   backend,
		open:      open,
		interval:  cfg.Interval,
		max:       cfg.MaxPending,
		callbacks: callbacks,
		pending:   map[string]struct{}{},
		flushCh:   make(chan struct{}, 1),
		doneCh:    make(chan struct{}),
	}
}

func (q *Queue) Name() string {
	return q.backend.Name()
}

func (q *Queue) Exists(ctx context.Context, hash string) (bool, error) {
	return q.backend.Exists(ctx, hash)
}

func (q *Queue) Put(ctx context.Context, hash string, reader io.Reader) (Object, error) {
	return q.backend.Put(ctx, hash, reader)
}

func (q *Queue) List(ctx context.Context) ([]Object, error) {
	return q.backend.List(ctx)
}

func (q *Queue) Start(ctx context.Context) {
	go q.loop(ctx)
}

func (q *Queue) Close(ctx context.Context) error {
	select {
	case <-q.doneCh:
	default:
	}
	_, err := q.Flush(ctx)
	return err
}

func (q *Queue) Enqueue(ctx context.Context, hash string) error {
	shouldFlush := false
	added := false
	q.mu.Lock()
	if _, exists := q.pending[hash]; !exists {
		q.pending[hash] = struct{}{}
		q.order = append(q.order, hash)
		added = true
		shouldFlush = q.max > 0 && len(q.order) >= q.max
	}
	pendingCount := len(q.order)
	q.mu.Unlock()

	if added {
		log.Printf("backup pending queued backend=%q hash=%s pending=%d", q.Name(), hash, pendingCount)
		if q.callbacks.OnPending != nil {
			q.callbacks.OnPending(hash)
		}
	}
	if shouldFlush {
		q.triggerFlush()
	}
	return ctx.Err()
}

func (q *Queue) Flush(ctx context.Context) ([]Object, error) {
	hashes := q.drain()
	if len(hashes) == 0 {
		return nil, nil
	}
	log.Printf("backup flush started backend=%q count=%d", q.Name(), len(hashes))
	started := time.Now()
	objects, err := q.putHashes(ctx, hashes)
	if err != nil {
		for _, hash := range hashes {
			q.markFailed(hash, err)
		}
		log.Printf("backup flush failed backend=%q count=%d duration=%s error=%q", q.Name(), len(hashes), time.Since(started).Truncate(time.Millisecond), err)
		return objects, err
	}
	for _, object := range objects {
		q.markBackuped(object.Hash)
	}
	log.Printf("backup flush completed backend=%q count=%d stored=%d duration=%s", q.Name(), len(hashes), len(objects), time.Since(started).Truncate(time.Millisecond))
	return objects, nil
}

func (q *Queue) loop(ctx context.Context) {
	defer close(q.doneCh)
	var ticker *time.Ticker
	var tick <-chan time.Time
	if q.interval > 0 {
		ticker = time.NewTicker(q.interval)
		tick = ticker.C
		defer ticker.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = q.Flush(flushCtx)
			cancel()
			return
		case <-tick:
			_, _ = q.Flush(ctx)
		case <-q.flushCh:
			_, _ = q.Flush(ctx)
		}
	}
}

func (q *Queue) drain() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.order) == 0 {
		return nil
	}
	hashes := append([]string(nil), q.order...)
	q.order = nil
	q.pending = map[string]struct{}{}
	return hashes
}

func (q *Queue) triggerFlush() {
	select {
	case q.flushCh <- struct{}{}:
	default:
	}
}

func (q *Queue) putHashes(ctx context.Context, hashes []string) ([]Object, error) {
	payloads := make([]Payload, 0, len(hashes))
	readers := make([]io.Closer, 0, len(hashes))
	defer func() {
		for _, reader := range readers {
			_ = reader.Close()
		}
	}()

	for _, hash := range hashes {
		reader, err := q.open(hash)
		if err != nil {
			q.markFailed(hash, err)
			continue
		}
		readers = append(readers, reader)
		payloads = append(payloads, Payload{Hash: hash, Reader: reader})
	}
	if len(payloads) == 0 {
		return nil, nil
	}
	if batch, ok := q.backend.(BatchBackend); ok {
		return batch.PutBatch(ctx, payloads)
	}
	objects := make([]Object, 0, len(payloads))
	for _, payload := range payloads {
		object, err := q.backend.Put(ctx, payload.Hash, payload.Reader)
		if err != nil {
			return objects, err
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func (q *Queue) markBackuped(hash string) {
	if q.callbacks.OnBackuped != nil {
		q.callbacks.OnBackuped(hash)
	}
}

func (q *Queue) markFailed(hash string, err error) {
	if q.callbacks.OnFailed != nil {
		q.callbacks.OnFailed(hash, err)
	}
}
