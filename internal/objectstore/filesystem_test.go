package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func filesystemFixture(t *testing.T) (*FilesystemStore, Info, []byte) {
	t.Helper()
	s, err := NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("persistent filesystem payload")
	info := Info{Hash: strings.Repeat("ab", 20), Size: int64(len(data)), Checksum: fmt.Sprintf("%08x", crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli)))}
	return s, info, data
}

func TestFilesystemAtomicIntegrityAndRanges(t *testing.T) {
	s, info, data := filesystemFixture(t)
	ctx := context.Background()
	bad := info
	bad.Checksum = "00000000"
	if _, err := s.Put(ctx, bad, bytes.NewReader(data)); !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("bad checksum: %v", err)
	}
	if _, err := s.Head(ctx, info.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial object visible: %v", err)
	}
	stored, err := s.Put(ctx, info, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored.ETag, "sha256:") {
		t.Fatalf("ETag = %s", stored.ETag)
	}
	r, _, err := s.OpenRange(ctx, info.Hash, 2, 8)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(part, data[2:9]) {
		t.Fatalf("range = %q", part)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("List = %v, %v", items, err)
	}
	dir, _ := s.objectDir(info.Hash)
	corrupt := bytes.Repeat([]byte("x"), len(data))
	if err := os.WriteFile(filepath.Join(dir, "payload"), corrupt, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open(ctx, info.Hash); !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("corrupt read = %v", err)
	}
}

func TestFilesystemConcurrentDuplicateAndConflictingWrites(t *testing.T) {
	s, info, data := filesystemFixture(t)
	var wg sync.WaitGroup
	errorsCh := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Put(context.Background(), info, bytes.NewReader(data))
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	other := []byte("different payload")
	conflict := info
	conflict.Size = int64(len(other))
	conflict.Checksum = fmt.Sprintf("%08x", crc32.Checksum(other, crc32.MakeTable(crc32.Castagnoli)))
	if _, err := s.Put(context.Background(), conflict, bytes.NewReader(other)); !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("conflicting write = %v", err)
	}
}

func TestFilesystemRequiresMountBeforeCreatingDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "unmounted", "vaps")
	s, err := NewFilesystem(root, "//missing/share")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Head(context.Background(), strings.Repeat("ab", 20)); err == nil {
		t.Fatal("accessed missing SMB mount")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created unmounted directory: %v", err)
	}
}

func TestFilesystemRejectsTraversalAndCancellation(t *testing.T) {
	s, _, _ := filesystemFixture(t)
	if _, err := s.Head(context.Background(), "../../etc/passwd"); err == nil {
		t.Fatal("accepted traversal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List cancellation = %v", err)
	}
}
