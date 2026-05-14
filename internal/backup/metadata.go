package backup

import (
	"context"
	"io"
	"log"
	"sort"
	"sync"

	"vaps/internal/blobstore"
)

// MetadataBackend caches which payloads exist in the current backup backend.
type MetadataBackend struct {
	backend Backend

	mu      sync.RWMutex
	objects map[string]Object
}

func NewMetadataBackend(ctx context.Context, backend Backend) (*MetadataBackend, error) {
	metadata := NewEmptyMetadataBackend(backend)
	if err := metadata.Refresh(ctx); err != nil {
		return nil, err
	}
	return metadata, nil
}

func NewEmptyMetadataBackend(backend Backend) *MetadataBackend {
	return &MetadataBackend{
		backend: backend,
		objects: make(map[string]Object),
	}
}

func (m *MetadataBackend) Refresh(ctx context.Context) error {
	objects, err := m.backend.List(ctx)
	if err != nil {
		return err
	}
	for _, object := range objects {
		m.remember(object)
	}
	log.Printf("backup metadata initialized backend=%q listed=%d cached=%d", m.backend.Name(), len(objects), m.Count())
	return nil
}

func (m *MetadataBackend) Count() int {
	m.mu.RLock()
	count := len(m.objects)
	m.mu.RUnlock()
	return count
}

func (m *MetadataBackend) Name() string {
	return m.backend.Name()
}

func (m *MetadataBackend) Exists(_ context.Context, hash string) (bool, error) {
	if !blobstore.ValidHash(hash) {
		return false, blobstore.ErrInvalidHash
	}
	m.mu.RLock()
	_, ok := m.objects[hash]
	m.mu.RUnlock()
	return ok, nil
}

func (m *MetadataBackend) Open(ctx context.Context, hash string) (io.ReadCloser, error) {
	return m.backend.Open(ctx, hash)
}

func (m *MetadataBackend) Put(ctx context.Context, hash string, reader io.Reader) (Object, error) {
	if object, ok := m.lookup(hash); ok {
		return object, nil
	}
	object, err := m.backend.Put(ctx, hash, reader)
	if err != nil {
		return Object{}, err
	}
	m.remember(object)
	return object, nil
}

func (m *MetadataBackend) List(context.Context) ([]Object, error) {
	m.mu.RLock()
	objects := make([]Object, 0, len(m.objects))
	for _, object := range m.objects {
		objects = append(objects, object)
	}
	m.mu.RUnlock()
	sort.Slice(objects, func(i, j int) bool { return objects[i].Hash < objects[j].Hash })
	return objects, nil
}

func (m *MetadataBackend) PutBatch(ctx context.Context, payloads []Payload) ([]Object, error) {
	objects := make([]Object, 0, len(payloads))
	missing := make([]Payload, 0, len(payloads))
	for _, payload := range payloads {
		if object, ok := m.lookup(payload.Hash); ok {
			objects = append(objects, object)
			continue
		}
		missing = append(missing, payload)
	}
	if len(missing) == 0 {
		return objects, nil
	}

	stored, err := m.putMissing(ctx, missing)
	if err != nil {
		return append(objects, stored...), err
	}
	for _, object := range stored {
		m.remember(object)
	}
	return append(objects, stored...), nil
}

func (m *MetadataBackend) lookup(hash string) (Object, bool) {
	m.mu.RLock()
	object, ok := m.objects[hash]
	m.mu.RUnlock()
	return object, ok
}

func (m *MetadataBackend) remember(object Object) {
	if !blobstore.ValidHash(object.Hash) {
		return
	}
	m.mu.Lock()
	m.objects[object.Hash] = object
	m.mu.Unlock()
}

func (m *MetadataBackend) putMissing(ctx context.Context, payloads []Payload) ([]Object, error) {
	if batch, ok := m.backend.(BatchBackend); ok {
		return batch.PutBatch(ctx, payloads)
	}
	objects := make([]Object, 0, len(payloads))
	for _, payload := range payloads {
		object, err := m.backend.Put(ctx, payload.Hash, payload.Reader)
		if err != nil {
			return objects, err
		}
		objects = append(objects, object)
	}
	return objects, nil
}
