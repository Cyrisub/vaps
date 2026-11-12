package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type unavailableStore struct {
	*FilesystemStore
	offline atomic.Bool
	gate    <-chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (s *unavailableStore) Head(ctx context.Context, hash string) (Info, error) {
	if s.offline.Load() {
		return Info{}, errors.New("SMB offline")
	}
	if s.gate != nil {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-ctx.Done():
			return Info{}, ctx.Err()
		case <-s.gate:
		}
	}
	return s.FilesystemStore.Head(ctx, hash)
}

func awaitReplication(t *testing.T, store *AsyncStore) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for store.ReplicationStatus().PendingObjects != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("replication stuck: %+v", store.ReplicationStatus())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAsyncPersistsFailedJobsAndResumesAfterRestart(t *testing.T) {
	primary, info, data := filesystemFixture(t)
	secondary, _, _ := filesystemFixture(t)
	replica := &unavailableStore{FilesystemStore: secondary}
	replica.offline.Store(true)
	backends := []Backend{{"s3", primary}, {"smb", replica}}
	queue := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	store, err := NewAsync(ctx, backends, queue, 2, 20*time.Millisecond)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, info, bytes.NewReader(data)); err != nil {
		cancel()
		t.Fatal(err)
	}
	if store.ReplicationStatus().PendingObjects != 1 {
		t.Fatal("upload not durably queued")
	}
	if _, err := primary.Head(ctx, info.Hash); err != nil {
		t.Fatal(err)
	}
	cancel()
	store.Wait()
	jobs, err := store.queue.List(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("restart lost queued bytes: %v, %v", jobs, err)
	}
	replica.offline.Store(false)
	ctx, cancel = context.WithCancel(context.Background())
	restarted, err := NewAsync(ctx, backends, queue, 2, 20*time.Millisecond)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { cancel(); restarted.Wait() }()
	awaitReplication(t, restarted)
	r, _, err := secondary.Open(ctx, info.Hash)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(body, data) {
		t.Fatalf("replica = %q", body)
	}
	jobs, err = restarted.queue.List(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("completed queue = %v, %v", jobs, err)
	}
	// An object already on the preferred backend before this process started
	// must not be copied unless it is explicitly uploaded again.
	old := info
	old.Hash = strings.Repeat("cd", 20)
	if _, err := primary.Put(ctx, old, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := secondary.Head(ctx, old.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unexpected historical synchronization: %v", err)
	}
}

func TestAsyncSlowReplicaDoesNotBlockUploads(t *testing.T) {
	primary, info, data := filesystemFixture(t)
	secondary, _, _ := filesystemFixture(t)
	gate := make(chan struct{})
	replica := &unavailableStore{FilesystemStore: secondary, gate: gate, entered: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	store, err := NewAsync(ctx, []Backend{{"s3", primary}, {"smb", replica}}, t.TempDir(), 1, 10*time.Millisecond)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { cancel(); store.Wait() }()
	if _, err := store.Put(ctx, info, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-replica.entered:
	case <-time.After(time.Second):
		t.Fatal("worker never reached replica")
	}
	second := info
	second.Hash = strings.Repeat("cd", 20)
	result := make(chan error, 1)
	go func() { _, err := store.Put(ctx, second, bytes.NewReader(data)); result <- err }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow replica blocked a new upload")
	}
	close(gate)
	awaitReplication(t, store)
}

func TestAsyncRejectsCorruptQueueInput(t *testing.T) {
	primary, info, data := filesystemFixture(t)
	secondary, _, _ := filesystemFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	store, err := NewAsync(ctx, []Backend{{"s3", primary}, {"smb", secondary}}, t.TempDir(), 1, time.Second)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { cancel(); store.Wait() }()
	info.Checksum = "00000000"
	if _, err := store.Put(ctx, info, bytes.NewReader(data)); !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("bad upload = %v", err)
	}
	if _, err := primary.Head(ctx, info.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("corrupt input reached primary: %v", err)
	}
}
