package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"vaps/internal/metadata"
	"vaps/internal/objectstore"
)

func TestDirectPushCommitsAuthoritativeStoreBeforeSuccess(t *testing.T) {
	meta := openMetadata(t)
	objects := newMemoryObjectStore()
	handler := newV2Handler(t, meta, nil, objects)
	token := requestToken(t, handler)
	payload := []byte("durable-s3-payload")
	hash := ioHash(payload)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash, bytes.NewReader(payload))
	request.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201; body=%q", response.Code, response.Body.String())
	}
	stored, err := objects.Head(context.Background(), hash)
	if err != nil || stored.Size != int64(len(payload)) || stored.ContentHash == "" {
		t.Fatalf("authoritative object = %#v, %v", stored, err)
	}
	record, err := meta.GetPayload(hash)
	if err != nil || record.ContentHash != stored.ContentHash || record.Status != metadata.StatusCached {
		t.Fatalf("metadata record = %#v, %v", record, err)
	}
}

func TestDirectPushFailsWhenAuthoritativeStoreFails(t *testing.T) {
	meta := openMetadata(t)
	objects := newMemoryObjectStore()
	objects.putErr = errors.New("s3 unavailable")
	handler := newV2Handler(t, meta, nil, objects)
	token := requestToken(t, handler)
	payload := []byte("not-durable")
	hash := ioHash(payload)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash, bytes.NewReader(payload))
	request.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("push status = %d, want 500; body=%q", response.Code, response.Body.String())
	}
	if _, err := meta.GetPayload(hash); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("metadata error = %v, want no record", err)
	}
}

func TestDirectPushRejectsExistingHashWithDifferentDigest(t *testing.T) {
	objects := newMemoryObjectStore()
	payload := []byte("collision-check")
	hash := ioHash(payload)
	objects.objects[hash] = memoryObject{info: objectstore.Info{Hash: hash, ContentHash: "different", Size: int64(len(payload))}}
	handler := newV2Handler(t, openMetadata(t), nil, objects)
	token := requestToken(t, handler)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash, bytes.NewReader(payload))
	request.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("push status = %d, want 409; body=%q", response.Code, response.Body.String())
	}
}

type memoryObjectStore struct {
	mu      sync.Mutex
	objects map[string]memoryObject
	putErr  error
}

type memoryObject struct {
	info objectstore.Info
	data []byte
}

func newMemoryObjectStore() *memoryObjectStore {
	return &memoryObjectStore{objects: map[string]memoryObject{}}
}

func (s *memoryObjectStore) Head(_ context.Context, hash string) (objectstore.Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[hash]
	if !ok {
		return objectstore.Info{}, objectstore.ErrNotFound
	}
	return object.info, nil
}

func (s *memoryObjectStore) Put(_ context.Context, info objectstore.Info, reader io.Reader) error {
	if s.putErr != nil {
		return s.putErr
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.objects[info.Hash] = memoryObject{info: info, data: data}
	s.mu.Unlock()
	return nil
}

func (s *memoryObjectStore) Open(_ context.Context, hash string) (io.ReadCloser, objectstore.Info, error) {
	s.mu.Lock()
	object, ok := s.objects[hash]
	s.mu.Unlock()
	if !ok {
		return nil, objectstore.Info{}, objectstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(object.data)), object.info, nil
}

func (s *memoryObjectStore) OpenRange(ctx context.Context, hash string, start, end int64) (io.ReadCloser, objectstore.Info, error) {
	reader, info, err := s.Open(ctx, hash)
	if err != nil {
		return nil, objectstore.Info{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, objectstore.Info{}, err
	}
	if start < 0 || end < start || end >= int64(len(data)) {
		return nil, objectstore.Info{}, errors.New("invalid range")
	}
	return io.NopCloser(bytes.NewReader(data[start : end+1])), info, nil
}
