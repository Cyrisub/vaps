package testserver

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"

	"vaps/internal/objectstore"
)

// FakeObjectStore is the authoritative in-memory S3 substitute used by
// functional tests. It has the same synchronous durability contract as S3.
type FakeObjectStore struct {
	mu      sync.Mutex
	objects map[string]fakeObject
}

type fakeObject struct {
	info objectstore.Info
	data []byte
}

func NewFakeObjectStore() *FakeObjectStore {
	return &FakeObjectStore{objects: map[string]fakeObject{}}
}

func (f *FakeObjectStore) Head(_ context.Context, hash string) (objectstore.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	object, ok := f.objects[hash]
	if !ok {
		return objectstore.Info{}, objectstore.ErrNotFound
	}
	return object.info, nil
}

func (f *FakeObjectStore) Put(_ context.Context, info objectstore.Info, reader io.Reader) (objectstore.Info, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return objectstore.Info{}, err
	}
	f.mu.Lock()
	f.objects[info.Hash] = fakeObject{info: info, data: append([]byte(nil), data...)}
	f.mu.Unlock()
	return info, nil
}

func (f *FakeObjectStore) Open(_ context.Context, hash string) (io.ReadCloser, objectstore.Info, error) {
	f.mu.Lock()
	object, ok := f.objects[hash]
	f.mu.Unlock()
	if !ok {
		return nil, objectstore.Info{}, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(object.data)), object.info, nil
}

func (f *FakeObjectStore) OpenRange(ctx context.Context, hash string, start, end int64) (io.ReadCloser, objectstore.Info, error) {
	reader, info, err := f.Open(ctx, hash)
	if err != nil {
		return nil, objectstore.Info{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, objectstore.Info{}, err
	}
	if start < 0 || end < start || end >= int64(len(data)) {
		return nil, objectstore.Info{}, os.ErrInvalid
	}
	return io.NopCloser(bytes.NewReader(data[start : end+1])), info, nil
}

func (f *FakeObjectStore) Stats(_ context.Context) (objectstore.ObjectStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var stats objectstore.ObjectStats
	for _, object := range f.objects {
		stats.ObjectCount++
		stats.TotalBytes += int64(len(object.data))
	}
	return stats, nil
}
