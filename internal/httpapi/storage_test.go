package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"vaps/internal/metadata"
	"vaps/internal/objectstore"
)

func TestV2MetadataExistsHeadsCOSConcurrently(t *testing.T) {
	const (
		count     = 32
		headDelay = 80 * time.Millisecond
	)
	objects := newMemoryObjectStore()
	objects.headDelay = headDelay

	hashes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		payload := []byte("exists-concurrent-" + strconv.Itoa(i))
		hash := ioHash(payload)
		sum := checksum(payload)
		objects.objects[hash] = memoryObject{
			info: objectstore.Info{Hash: hash, Checksum: sum, Size: int64(len(payload)), ETag: "etag"},
			data: payload,
		}
		hashes = append(hashes, hash)
	}

	handler := newV2Handler(t, openMetadata(t), nil, objects)
	token := requestToken(t, handler)

	body, err := json.Marshal(map[string][]string{"hashes": hashes})
	if err != nil {
		t.Fatalf("marshal exists body: %v", err)
	}
	started := time.Now()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/metadata/exists", bytes.NewReader(body))
	request.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(response, request)
	elapsed := time.Since(started)
	if response.Code != http.StatusOK {
		t.Fatalf("exists status = %d, want %d; body=%q", response.Code, http.StatusOK, response.Body.String())
	}

	// Serial Heads would take count*headDelay (~2.5s). Concurrent capped at
	// existsLookupConcurrency should finish near one Head RTT.
	if elapsed >= time.Duration(count)*headDelay/2 {
		t.Fatalf("exists elapsed %s; want concurrent COS Heads (<< %s)", elapsed, time.Duration(count)*headDelay)
	}
	if elapsed < headDelay {
		t.Fatalf("exists elapsed %s; want at least one headDelay=%s", elapsed, headDelay)
	}

	var decoded struct {
		Items map[string]struct {
			Exists bool  `json:"exists"`
			Size   int64 `json:"size"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode exists response: %v", err)
	}
	if len(decoded.Items) != count {
		t.Fatalf("items = %d, want %d", len(decoded.Items), count)
	}
	for _, hash := range hashes {
		item, ok := decoded.Items[hash]
		if !ok || !item.Exists || item.Size <= 0 {
			t.Fatalf("item[%s] = %#v", hash, item)
		}
	}
	if objects.headCalls != count {
		t.Fatalf("headCalls = %d, want %d", objects.headCalls, count)
	}
}

func TestDirectPushCommitsAuthoritativeStoreBeforeSuccess(t *testing.T) {
	meta := openMetadata(t)
	objects := newMemoryObjectStore()
	handler := newV2Handler(t, meta, nil, objects)
	token := requestToken(t, handler)
	payload := []byte("durable-s3-payload")
	hash := ioHash(payload)
	fileChecksum := checksum(payload)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+fileChecksum, bytes.NewReader(payload))
	request.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201; body=%q", response.Code, response.Body.String())
	}
	stored, err := objects.Head(context.Background(), hash)
	if err != nil || stored.Size != int64(len(payload)) || stored.Checksum != fileChecksum {
		t.Fatalf("authoritative object = %#v, %v", stored, err)
	}
	record, err := meta.GetPayload(hash)
	if err != nil || record.Checksum != stored.Checksum || record.Status != metadata.StatusCached {
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
	fileChecksum := checksum(payload)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+fileChecksum, bytes.NewReader(payload))
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
	objects.objects[hash] = memoryObject{info: objectstore.Info{Hash: hash, Checksum: "ffffffff", Size: int64(len(payload))}}
	handler := newV2Handler(t, openMetadata(t), nil, objects)
	token := requestToken(t, handler)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/payload/push?iohash="+hash+"&checksum="+checksum(payload), bytes.NewReader(payload))
	request.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("push status = %d, want 409; body=%q", response.Code, response.Body.String())
	}
}

func TestPullValidatesRemoteChecksumBeforeOpen(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	meta := openMetadata(t)
	objects := newMemoryObjectStore()
	objects.objects[hash] = memoryObject{
		info: objectstore.Info{
			Hash:     hash,
			ETag:     "etag-1",
			Checksum: "05060708",
			Size:     5,
		},
		data: []byte("hello"),
	}
	if err := meta.PutPayload(metadata.Payload{
		Hash:     hash,
		ETag:     "etag-1",
		Checksum: "01020304",
		Size:     5,
	}); err != nil {
		t.Fatal(err)
	}
	handler := newV2Handler(t, meta, nil, objects)
	token := requestToken(t, handler)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/payload/pull?iohash="+hash, nil)
	request.Header.Set("Authorization", authHeader(token))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("pull status = %d, want 500", response.Code)
	}
	objects.mu.Lock()
	headCalls, openCalls := objects.headCalls, objects.openCalls
	objects.mu.Unlock()
	if headCalls != 1 || openCalls != 0 {
		t.Fatalf("Head/Open calls = %d/%d, want 1/0", headCalls, openCalls)
	}
}

type memoryObjectStore struct {
	mu        sync.Mutex
	objects   map[string]memoryObject
	putErr    error
	headCalls int
	openCalls int
	headDelay time.Duration
}

type memoryObject struct {
	info objectstore.Info
	data []byte
}

func newMemoryObjectStore() *memoryObjectStore {
	return &memoryObjectStore{objects: map[string]memoryObject{}}
}

func (s *memoryObjectStore) Head(ctx context.Context, hash string) (objectstore.Info, error) {
	if s.headDelay > 0 {
		timer := time.NewTimer(s.headDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return objectstore.Info{}, ctx.Err()
		case <-timer.C:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.headCalls++
	object, ok := s.objects[hash]
	if !ok {
		return objectstore.Info{}, objectstore.ErrNotFound
	}
	return object.info, nil
}

func (s *memoryObjectStore) Put(_ context.Context, info objectstore.Info, reader io.Reader) (objectstore.Info, error) {
	if s.putErr != nil {
		return objectstore.Info{}, s.putErr
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return objectstore.Info{}, err
	}
	s.mu.Lock()
	s.objects[info.Hash] = memoryObject{info: info, data: data}
	s.mu.Unlock()
	return info, nil
}

func (s *memoryObjectStore) Open(_ context.Context, hash string) (io.ReadCloser, objectstore.Info, error) {
	s.mu.Lock()
	s.openCalls++
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

func (s *memoryObjectStore) Stats(_ context.Context) (objectstore.ObjectStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var stats objectstore.ObjectStats
	for _, object := range s.objects {
		stats.ObjectCount++
		stats.TotalBytes += int64(len(object.data))
	}
	return stats, nil
}
