// Package objectstore provides the durable payload storage abstraction.
package objectstore

import (
	"context"
	"errors"
	"io"
)

var (
	ErrNotFound          = errors.New("object not found")
	ErrIntegrityMismatch = errors.New("object metadata does not match payload")
)

// Info is the immutable identity of a stored payload.
type Info struct {
	Hash           string
	ETag           string
	Checksum       string
	LegacyChecksum string
	Size           int64
}

type ObjectStats struct {
	TotalBytes  int64
	ObjectCount int64
	Pending     bool
}

// Store is the authoritative, synchronous payload store. A successful Put
// means the backend has durably accepted the object.
type Store interface {
	Head(context.Context, string) (Info, error)
	Put(context.Context, Info, io.Reader) (Info, error)
	Open(context.Context, string) (io.ReadCloser, Info, error)
}

// ListStore lists objects under the store's configured prefix.
type ListStore interface {
	List(context.Context) ([]Info, error)
}

type StatsStore interface {
	Stats(context.Context) (ObjectStats, error)
}

// RangeStore is implemented by backends that can stream a byte range without
// reading the entire object into application memory.
type RangeStore interface {
	OpenRange(context.Context, string, int64, int64) (io.ReadCloser, Info, error)
}
