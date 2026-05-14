package backup

import (
	"context"
	"io"
	"strings"
	"testing"
)

const (
	metadataHelloHash   = "ea8f163db38682925e4491c5e58d4bb3506ef8c1"
	metadataGoodbyeHash = "7c211433f02071597741e6ff5a8ea34789abbf43"
)

func TestMetadataBackendInitializesFromBackendList(t *testing.T) {
	backend := &fakeMetadataBackend{
		listed: []Object{{Hash: metadataHelloHash, Path: metadataPath(metadataHelloHash)}},
	}
	metadata, err := NewMetadataBackend(context.Background(), backend)
	if err != nil {
		t.Fatalf("NewMetadataBackend returned error: %v", err)
	}
	if backend.listCalls != 1 {
		t.Fatalf("list calls = %d, want 1", backend.listCalls)
	}

	exists, err := metadata.Exists(context.Background(), metadataHelloHash)
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if !exists {
		t.Fatalf("Exists = false, want true for listed payload")
	}
	if backend.existsCalls != 0 {
		t.Fatalf("backend Exists calls = %d, want 0", backend.existsCalls)
	}

	missing, err := metadata.Exists(context.Background(), metadataGoodbyeHash)
	if err != nil {
		t.Fatalf("missing Exists returned error: %v", err)
	}
	if missing {
		t.Fatalf("Exists = true, want false for unlisted payload")
	}

	objects, err := metadata.List(context.Background())
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(objects) != 1 || objects[0].Hash != metadataHelloHash {
		t.Fatalf("objects = %#v", objects)
	}
}

func TestEmptyMetadataBackendRefreshesOnDemand(t *testing.T) {
	backend := &fakeMetadataBackend{
		listed: []Object{{Hash: metadataHelloHash, Path: metadataPath(metadataHelloHash)}},
	}
	metadata := NewEmptyMetadataBackend(backend)

	exists, err := metadata.Exists(context.Background(), metadataHelloHash)
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if exists {
		t.Fatalf("Exists = true before refresh, want false")
	}
	if err := metadata.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	exists, err = metadata.Exists(context.Background(), metadataHelloHash)
	if err != nil {
		t.Fatalf("Exists after refresh returned error: %v", err)
	}
	if !exists {
		t.Fatalf("Exists = false after refresh, want true")
	}
}

func TestMetadataBackendSkipsCachedPayloadOnPut(t *testing.T) {
	backend := &fakeMetadataBackend{
		listed: []Object{{Hash: metadataHelloHash, Path: metadataPath(metadataHelloHash)}},
	}
	metadata, err := NewMetadataBackend(context.Background(), backend)
	if err != nil {
		t.Fatalf("NewMetadataBackend returned error: %v", err)
	}

	object, err := metadata.Put(context.Background(), metadataHelloHash, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	if object.Hash != metadataHelloHash {
		t.Fatalf("object = %#v", object)
	}
	if backend.putCalls != 0 {
		t.Fatalf("backend Put calls = %d, want 0", backend.putCalls)
	}
}

func TestMetadataBackendSkipsCachedPayloadsAndUpdatesAfterBatch(t *testing.T) {
	backend := &fakeMetadataBackend{
		listed: []Object{{Hash: metadataHelloHash, Path: metadataPath(metadataHelloHash)}},
	}
	metadata, err := NewMetadataBackend(context.Background(), backend)
	if err != nil {
		t.Fatalf("NewMetadataBackend returned error: %v", err)
	}

	objects, err := metadata.PutBatch(context.Background(), []Payload{
		{Hash: metadataHelloHash, Reader: strings.NewReader("hello")},
		{Hash: metadataGoodbyeHash, Reader: strings.NewReader("goodbye")},
	})
	if err != nil {
		t.Fatalf("PutBatch returned error: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("len(objects) = %d, want 2: %#v", len(objects), objects)
	}
	if len(backend.batches) != 1 || len(backend.batches[0]) != 1 || backend.batches[0][0] != metadataGoodbyeHash {
		t.Fatalf("backend batches = %#v, want only missing hash", backend.batches)
	}

	exists, err := metadata.Exists(context.Background(), metadataGoodbyeHash)
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if !exists {
		t.Fatalf("Exists = false, want true after PutBatch")
	}
}

type fakeMetadataBackend struct {
	listed      []Object
	listCalls   int
	existsCalls int
	putCalls    int
	batches     [][]string
}

func (f *fakeMetadataBackend) Name() string { return "fake" }

func (f *fakeMetadataBackend) Exists(context.Context, string) (bool, error) {
	f.existsCalls++
	return false, nil
}

func (f *fakeMetadataBackend) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeMetadataBackend) Put(ctx context.Context, hash string, reader io.Reader) (Object, error) {
	f.putCalls++
	objects, err := f.PutBatch(ctx, []Payload{{Hash: hash, Reader: reader}})
	if err != nil {
		return Object{}, err
	}
	return objects[0], nil
}

func (f *fakeMetadataBackend) PutBatch(_ context.Context, payloads []Payload) ([]Object, error) {
	batch := make([]string, 0, len(payloads))
	objects := make([]Object, 0, len(payloads))
	for _, payload := range payloads {
		batch = append(batch, payload.Hash)
		objects = append(objects, Object{Hash: payload.Hash, Path: metadataPath(payload.Hash)})
	}
	f.batches = append(f.batches, batch)
	return objects, nil
}

func (f *fakeMetadataBackend) List(context.Context) ([]Object, error) {
	f.listCalls++
	return append([]Object(nil), f.listed...), nil
}

func metadataPath(hash string) string {
	return hash[:2] + "/" + hash[2:4] + "/" + hash[4:6] + "/" + hash[6:] + ".upayload"
}
