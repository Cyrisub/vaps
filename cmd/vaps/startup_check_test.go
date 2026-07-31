package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"vaps/internal/metadata"
	"vaps/internal/objectstore"
)

func TestValidateMetadataPayloads(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	payload := metadata.Payload{
		Hash:     hash,
		Checksum: "01020304",
		Size:     12,
	}

	tests := []struct {
		name     string
		headInfo objectstore.Info
		headErr  error
		wantErr  string
	}{
		{
			name: "matching object",
			headInfo: objectstore.Info{
				Hash:     hash,
				ETag:     "etag-1",
				Checksum: "01020304",
				Size:     12,
			},
		},
		{
			name:    "missing object",
			headErr: objectstore.ErrNotFound,
			wantErr: "missing from S3",
		},
		{
			name: "mismatched hash",
			headInfo: objectstore.Info{
				Hash:     "fedcba9876543210fedcba9876543210fedcba98",
				Checksum: "01020304",
				Size:     12,
			},
			wantErr: "does not match S3 object hash",
		},
		{
			name: "mismatched checksum",
			headInfo: objectstore.Info{
				Hash:     hash,
				Checksum: "05060708",
				Size:     12,
			},
			wantErr: "does not match S3 object checksum",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta, err := metadata.Open(t.TempDir() + "/metadata.db")
			if err != nil {
				t.Fatal(err)
			}
			defer meta.Close()
			if err := meta.PutPayload(payload); err != nil {
				t.Fatal(err)
			}

			objects := &startupCheckObjectStore{
				headInfo: test.headInfo,
				headErr:  test.headErr,
			}
			err = validateMetadataPayloads(context.Background(), meta, objects)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateMetadataPayloads() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateMetadataPayloads() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestValidateMetadataPayloadsUsesWorkers(t *testing.T) {
	meta, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()

	const payloadCount = 4
	for i := range payloadCount {
		if err := meta.PutPayload(metadata.Payload{
			Hash:     fmt.Sprintf("%040d", i),
			Checksum: "01020304",
			Size:     12,
		}); err != nil {
			t.Fatal(err)
		}
	}

	objects := &startupCheckObjectStore{
		headInfo: objectstore.Info{
			ETag:     "etag-1",
			Checksum: "01020304",
			Size:     12,
		},
		delay: 50 * time.Millisecond,
	}
	err = validateMetadataPayloadsWithOptions(context.Background(), meta, objects, metadataValidationOptions{
		Workers:          payloadCount,
		RequestTimeout:   time.Second,
		ProgressInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("validateMetadataPayloadsWithOptions() error = %v", err)
	}
	objects.mu.Lock()
	maxActive := objects.maxActive
	objects.mu.Unlock()
	if maxActive < 2 {
		t.Fatalf("metadata validation used %d concurrent workers, want at least 2", maxActive)
	}
}

func TestNormalizeMetadataValidationWorkers(t *testing.T) {
	if got := normalizeMetadataValidationOptions(metadataValidationOptions{Workers: 0}).Workers; got != 1 {
		t.Fatalf("workers=0 normalized to %d, want 1", got)
	}
	if got := normalizeMetadataValidationOptions(metadataValidationOptions{Workers: -1}).Workers; got != physicalCoreCount() {
		t.Fatalf("workers=-1 normalized to %d, want physical core count %d", got, physicalCoreCount())
	}
}

func TestValidateMetadataPayloadsUsesETagList(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	meta, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	if err := meta.PutPayload(metadata.Payload{
		Hash:     hash,
		ETag:     "etag-1",
		Checksum: "01020304",
		Size:     12,
	}); err != nil {
		t.Fatal(err)
	}

	objects := &startupCheckListObjectStore{
		startupCheckObjectStore: &startupCheckObjectStore{},
		listInfo: []objectstore.Info{{
			Hash: hash,
			ETag: "etag-1",
			Size: 12,
		}},
	}
	if err := validateMetadataPayloadsWithOptions(context.Background(), meta, objects, metadataValidationOptions{
		Workers:          1,
		RequestTimeout:   time.Second,
		ProgressInterval: time.Hour,
	}); err != nil {
		t.Fatalf("validateMetadataPayloadsWithOptions() error = %v", err)
	}
	if objects.headCalls != 0 {
		t.Fatalf("Head calls = %d, want 0 for stored ETag", objects.headCalls)
	}
}

func TestValidateMetadataPayloadsBackfillsMissingETag(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	meta, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	if err := meta.PutPayload(metadata.Payload{
		Hash:     hash,
		Checksum: "01020304",
		Size:     12,
	}); err != nil {
		t.Fatal(err)
	}

	objects := &startupCheckListObjectStore{
		startupCheckObjectStore: &startupCheckObjectStore{
			headInfo: objectstore.Info{
				Hash:     hash,
				ETag:     "etag-1",
				Checksum: "01020304",
				Size:     12,
			},
		},
		listInfo: []objectstore.Info{{Hash: hash, ETag: "etag-1", Size: 12}},
	}
	if err := validateMetadataPayloadsWithOptions(context.Background(), meta, objects, metadataValidationOptions{
		Workers:          1,
		RequestTimeout:   time.Second,
		ProgressInterval: time.Hour,
	}); err != nil {
		t.Fatalf("validateMetadataPayloadsWithOptions() error = %v", err)
	}
	record, err := meta.GetPayload(hash)
	if err != nil || record.ETag != "etag-1" {
		t.Fatalf("metadata ETag = %q, error = %v", record.ETag, err)
	}
	if objects.headCalls != 1 {
		t.Fatalf("Head calls = %d, want 1 for ETag backfill", objects.headCalls)
	}
}

type startupCheckObjectStore struct {
	headInfo  objectstore.Info
	headErr   error
	delay     time.Duration
	mu        sync.Mutex
	active    int
	maxActive int
	headCalls int
}

func (s *startupCheckObjectStore) Head(ctx context.Context, hash string) (objectstore.Info, error) {
	s.mu.Lock()
	s.headCalls++
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return objectstore.Info{}, ctx.Err()
		}
	}
	info := s.headInfo
	if info.Hash == "" {
		info.Hash = hash
	}
	return info, s.headErr
}

type startupCheckListObjectStore struct {
	*startupCheckObjectStore
	listInfo []objectstore.Info
}

func (s *startupCheckListObjectStore) List(context.Context) ([]objectstore.Info, error) {
	return s.listInfo, nil
}

func (s *startupCheckObjectStore) Put(context.Context, objectstore.Info, io.Reader) (objectstore.Info, error) {
	return objectstore.Info{}, nil
}

func (s *startupCheckObjectStore) Open(context.Context, string) (io.ReadCloser, objectstore.Info, error) {
	return nil, objectstore.Info{}, errors.New("not implemented")
}
