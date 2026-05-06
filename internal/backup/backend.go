package backup

import (
	"context"
	"io"
)

// Object identifies a payload stored by a backup backend.
type Object struct {
	Hash string `json:"hash"`
	Path string `json:"path"`
}

// Payload provides payload bytes to a backup backend.
type Payload struct {
	Hash   string
	Reader io.Reader
}

// Backend is the common contract for all payload backup implementations.
type Backend interface {
	Name() string
	Exists(ctx context.Context, hash string) (bool, error)
	Put(ctx context.Context, hash string, reader io.Reader) (Object, error)
	List(ctx context.Context) ([]Object, error)
}

// BatchBackend can store multiple payloads in one backend transaction.
type BatchBackend interface {
	PutBatch(ctx context.Context, payloads []Payload) ([]Object, error)
}

// EnqueueBackend accepts payload hashes for deferred backup.
type EnqueueBackend interface {
	Enqueue(ctx context.Context, hash string) error
}
