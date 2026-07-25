package blobstore_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vaps/internal/blobstore"
	"vaps/internal/utils"
)

const helloHash = "ea8f163db38682925e4491c5e58d4bb3506ef8c1"

func TestPutStoresPayloadByHash(t *testing.T) {
	store := blobstore.New(t.TempDir())

	info, err := store.Put(helloHash, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	if info.Hash != helloHash {
		t.Fatalf("Hash = %q, want %q", info.Hash, helloHash)
	}
	if info.Size != 5 {
		t.Fatalf("Size = %d, want 5", info.Size)
	}
	if info.Created != true {
		t.Fatalf("Created = false, want true for first write")
	}

	got, err := readAll(store, helloHash)
	if err != nil {
		t.Fatalf("read stored payload: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("stored payload = %q, want %q", string(got), "hello")
	}
}

func TestPutRejectsHashMismatchAndDoesNotCreateBlob(t *testing.T) {
	store := blobstore.New(t.TempDir())

	_, err := store.Put(helloHash, strings.NewReader("goodbye"))
	if !errors.Is(err, blobstore.ErrHashMismatch) {
		t.Fatalf("Put error = %v, want ErrHashMismatch", err)
	}

	exists, _, err := store.Exists(helloHash)
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if exists {
		t.Fatalf("Exists = true, want false after rejected write")
	}
}

func TestPutExistingPayloadDoesNotOverwrite(t *testing.T) {
	store := blobstore.New(t.TempDir())

	first, err := store.Put(helloHash, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("first Put returned error: %v", err)
	}
	second, err := store.Put(helloHash, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("second Put returned error: %v", err)
	}

	if !first.Created {
		t.Fatalf("first Created = false, want true")
	}
	if second.Created {
		t.Fatalf("second Created = true, want false")
	}
	got, err := readAll(store, helloHash)
	if err != nil {
		t.Fatalf("read stored payload: %v", err)
	}
	if !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("stored payload = %q, want hello", string(got))
	}
}

func TestPathForHashShardsByPrefix(t *testing.T) {
	root := t.TempDir()
	store := blobstore.New(root)

	got, err := store.Path(helloHash)
	if err != nil {
		t.Fatalf("Path returned error: %v", err)
	}

	want := filepath.Join(root, "blobs", "ea", "8f", helloHash+".upayload")
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestRelativePathUsesBlobShardRule(t *testing.T) {
	got, err := blobstore.RelativePath(helloHash)
	if err != nil {
		t.Fatalf("RelativePath returned error: %v", err)
	}
	want := "blobs/ea/8f/" + helloHash + ".upayload"
	if got != want {
		t.Fatalf("RelativePath = %q, want %q", got, want)
	}
}

func TestRejectsInvalidHash(t *testing.T) {
	store := blobstore.New(t.TempDir())

	_, err := store.Put("not-a-valid-iohash", strings.NewReader("hello"))
	if !errors.Is(err, blobstore.ErrInvalidHash) {
		t.Fatalf("Put error = %v, want ErrInvalidHash", err)
	}
}

func TestPutCleansTemporaryFileOnReadError(t *testing.T) {
	root := t.TempDir()
	store := blobstore.New(root)

	_, err := store.Put(helloHash, errReader{})
	if err == nil {
		t.Fatalf("Put error = nil, want read error")
	}

	tmpEntries, readErr := os.ReadDir(filepath.Join(root, "tmp"))
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("ReadDir tmp returned error: %v", readErr)
	}
	if len(tmpEntries) != 0 {
		t.Fatalf("tmp contains %d entries, want 0", len(tmpEntries))
	}
}

func TestStoreEvictsOldestCacheEntryAboveLimit(t *testing.T) {
	root := t.TempDir()
	store := blobstore.NewWithMaxBytes(root, 5)
	if _, err := store.Put(helloHash, strings.NewReader("hello")); err != nil {
		t.Fatalf("put first payload: %v", err)
	}
	secondHash := utils.IoHashSumHex([]byte("world!!"))
	if _, err := store.Put(secondHash, strings.NewReader("world!!")); err != nil {
		t.Fatalf("put second payload: %v", err)
	}
	firstExists, _, err := store.Exists(helloHash)
	if err != nil {
		t.Fatal(err)
	}
	secondExists, _, err := store.Exists(secondHash)
	if err != nil {
		t.Fatal(err)
	}
	if firstExists || secondExists {
		t.Fatalf("oversized cache entries must be evicted, got first=%t second=%t", firstExists, secondExists)
	}
}

func readAll(store *blobstore.Store, hash string) ([]byte, error) {
	reader, _, err := store.Open(hash)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
