package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type switchStore struct {
	*FilesystemStore
	failure error
}

func (s *switchStore) Head(ctx context.Context, hash string) (Info, error) {
	if s.failure != nil {
		return Info{}, s.failure
	}
	return s.FilesystemStore.Head(ctx, hash)
}
func (s *switchStore) Open(ctx context.Context, hash string) (io.ReadCloser, Info, error) {
	if s.failure != nil {
		return nil, Info{}, s.failure
	}
	return s.FilesystemStore.Open(ctx, hash)
}
func (s *switchStore) OpenRange(ctx context.Context, hash string, start, end int64) (io.ReadCloser, Info, error) {
	if s.failure != nil {
		return nil, Info{}, s.failure
	}
	return s.FilesystemStore.OpenRange(ctx, hash, start, end)
}
func (s *switchStore) List(ctx context.Context) ([]Info, error) {
	if s.failure != nil {
		return nil, s.failure
	}
	return s.FilesystemStore.List(ctx)
}

func TestMultiPartialWriteRetryAndFallback(t *testing.T) {
	first, info, data := filesystemFixture(t)
	second, _, _ := filesystemFixture(t)
	primary := &switchStore{FilesystemStore: first}
	replica := &switchStore{FilesystemStore: second, failure: errors.New("offline")}
	multi, err := NewMulti([]Backend{{"s3", primary}, {"smb", replica}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := multi.Put(ctx, info, bytes.NewBuffer(data)); err == nil {
		t.Fatal("acknowledged partial write")
	}
	if _, err := first.Head(ctx, info.Hash); err != nil {
		t.Fatalf("primary not retained: %v", err)
	}
	replica.failure = nil
	if _, err := multi.Put(ctx, info, bytes.NewBuffer(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Head(ctx, info.Hash); err != nil {
		t.Fatalf("retry did not repair replica: %v", err)
	}
	primary.failure = errors.New("primary offline")
	head, err := multi.Head(ctx, info.Hash)
	if err != nil || head.Backend != "smb" {
		t.Fatalf("fallback Head = %+v, %v", head, err)
	}
	r, opened, err := multi.OpenRange(ctx, info.Hash, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(r)
	r.Close()
	if opened.Backend != "smb" || !bytes.Equal(part, data[1:6]) {
		t.Fatalf("fallback range = %+v, %q", opened, part)
	}
	if _, err := multi.List(ctx); err != nil {
		t.Fatalf("fallback list: %v", err)
	}
}

func TestETagsAreScopedToBackend(t *testing.T) {
	if ETagMatches("old", "", Info{Backend: "s3", ETag: "changed"}) {
		t.Fatal("ignored legacy primary ETag mismatch")
	}
	if !ETagMatches("cos-etag", "s3", Info{Backend: "smb", ETag: "sha256:abc"}) {
		t.Fatal("compared ETags across providers")
	}
	if ETagMatches("old", "smb", Info{Backend: "smb", ETag: "changed"}) {
		t.Fatal("ignored same-backend ETag mismatch")
	}
}
