package testserver

import (
	"bytes"
	"context"
	"io"
	"os"

	"vaps/internal/backup"
)

// FakeBackup is an in-memory backup backend for functional tests.
type FakeBackup struct {
	puts     []string
	Enqueued []string
	Payloads map[string][]byte
}

func NewFakeBackup() *FakeBackup {
	return &FakeBackup{Payloads: map[string][]byte{}}
}

func (f *FakeBackup) Name() string { return "fake" }

func (f *FakeBackup) Exists(_ context.Context, hash string) (bool, error) {
	_, ok := f.Payloads[hash]
	return ok, nil
}

func (f *FakeBackup) Open(_ context.Context, hash string) (io.ReadCloser, error) {
	payload, ok := f.Payloads[hash]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(payload)), nil
}

func (f *FakeBackup) Enqueue(_ context.Context, hash string) error {
	f.Enqueued = append(f.Enqueued, hash)
	return nil
}

func (f *FakeBackup) Put(_ context.Context, hash string, reader io.Reader) (backup.Object, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return backup.Object{}, err
	}
	if f.Payloads == nil {
		f.Payloads = map[string][]byte{}
	}
	f.puts = append(f.puts, hash)
	f.Payloads[hash] = data
	return backup.Object{Hash: hash, Path: "blobs/test/" + hash + ".upayload"}, nil
}

func (f *FakeBackup) List(context.Context) ([]backup.Object, error) {
	items := make([]backup.Object, 0, len(f.Payloads))
	for hash := range f.Payloads {
		items = append(items, backup.Object{Hash: hash, Path: "blobs/test/" + hash + ".upayload"})
	}
	return items, nil
}
