package backup

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestQueueFlushesWhenMaxPendingReached(t *testing.T) {
	backend := &fakeQueueBackend{}
	opened := map[string]string{"a": "one", "b": "two"}
	backuped := []string{}
	queue := NewQueue(backend, func(hash string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(opened[hash])), nil
	}, QueueConfig{MaxPending: 2}, QueueCallbacks{
		OnBackuped: func(hash string) { backuped = append(backuped, hash) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue.Start(ctx)

	if err := queue.Enqueue(context.Background(), "a"); err != nil {

		t.Fatalf("Enqueue a returned error: %v", err)
	}
	if len(backend.batches) != 0 {
		t.Fatalf("batch count = %d, want 0 before threshold", len(backend.batches))
	}
	if err := queue.Enqueue(context.Background(), "b"); err != nil {
		t.Fatalf("Enqueue b returned error: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for len(backend.batches) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(backend.batches) != 1 {
		t.Fatalf("batch count = %d, want 1", len(backend.batches))
	}
	if strings.Join(backend.batches[0], ",") != "a,b" {
		t.Fatalf("batch = %#v, want [a b]", backend.batches[0])
	}
	if strings.Join(backuped, ",") != "a,b" {
		t.Fatalf("backuped = %#v, want [a b]", backuped)
	}
}

func TestQueueDeduplicatesPendingHashes(t *testing.T) {
	backend := &fakeQueueBackend{}
	queue := NewQueue(backend, func(hash string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(hash)), nil
	}, QueueConfig{}, QueueCallbacks{})

	_ = queue.Enqueue(context.Background(), "a")
	_ = queue.Enqueue(context.Background(), "a")
	objects, err := queue.Flush(context.Background())
	if err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if len(objects) != 1 || objects[0].Hash != "a" {
		t.Fatalf("objects = %#v", objects)
	}
	if len(backend.batches) != 1 || len(backend.batches[0]) != 1 {
		t.Fatalf("batches = %#v", backend.batches)
	}
}

type fakeQueueBackend struct {
	batches [][]string
}

func (f *fakeQueueBackend) Name() string { return "fake" }

func (f *fakeQueueBackend) Exists(context.Context, string) (bool, error) { return false, nil }

func (f *fakeQueueBackend) Put(ctx context.Context, hash string, reader io.Reader) (Object, error) {
	objects, err := f.PutBatch(ctx, []Payload{{Hash: hash, Reader: reader}})
	if err != nil {
		return Object{}, err
	}
	return objects[0], nil
}

func (f *fakeQueueBackend) PutBatch(_ context.Context, payloads []Payload) ([]Object, error) {
	batch := []string{}
	objects := []Object{}
	for _, payload := range payloads {
		batch = append(batch, payload.Hash)
		objects = append(objects, Object{Hash: payload.Hash, Path: payload.Hash})
	}
	f.batches = append(f.batches, batch)
	return objects, nil
}

func (f *fakeQueueBackend) List(context.Context) ([]Object, error) { return nil, nil }
