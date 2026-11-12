package objectstore

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"sort"
	"vaps/internal/utils"
)

type Backend struct {
	Name  string
	Store Store
}

// MultiStore synchronously mirrors writes to every backend and falls back in
// configuration order for reads. Partial writes are retained and retryable.
type MultiStore struct{ backends []Backend }

func NewMulti(backends []Backend) (*MultiStore, error) {
	if len(backends) == 0 {
		return nil, errors.New("at least one persistent backend is required")
	}
	seen := make(map[string]bool)
	for _, backend := range backends {
		if backend.Name == "" || seen[backend.Name] || backend.Store == nil {
			return nil, errors.New("backend names must be nonempty and unique and stores must exist")
		}
		seen[backend.Name] = true
	}
	return &MultiStore{backends: append([]Backend(nil), backends...)}, nil
}

func (s *MultiStore) Mirrored() bool { return true }

func (s *MultiStore) Head(ctx context.Context, hash string) (Info, error) {
	var failures []error
	missing := true
	for _, backend := range s.backends {
		info, err := backend.Store.Head(ctx, hash)
		if err == nil {
			info.Backend = backend.Name
			return info, nil
		}
		missing = missing && errors.Is(err, ErrNotFound)
		failures = append(failures, fmt.Errorf("backend %s: %w", backend.Name, err))
		if ctx.Err() != nil {
			return Info{}, ctx.Err()
		}
	}
	if missing {
		return Info{}, ErrNotFound
	}
	return Info{}, errors.Join(failures...)
}

func samePayload(a, b Info) bool {
	return a.Hash == b.Hash && a.Checksum == b.Checksum && a.Size == b.Size
}

func (s *MultiStore) Put(ctx context.Context, info Info, reader io.Reader) (Info, error) {
	if err := validateInfo(info); err != nil {
		return Info{}, err
	}
	// Spool once, with a bounded memory footprint, so non-seekable readers work.
	file, err := os.CreateTemp("", "vaps-mirror-*")
	if err != nil {
		return Info{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	crc := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	n, err := io.Copy(io.MultiWriter(file, crc), &contextReader{ctx, reader})
	if err != nil {
		return Info{}, err
	}
	if n != info.Size || fmt.Sprintf("%08x", crc.Sum32()) != info.Checksum {
		return Info{}, ErrIntegrityMismatch
	}
	var primary Info
	etags := make(map[string]string)
	for index, backend := range s.backends {
		if err := ctx.Err(); err != nil {
			return Info{}, err
		}
		stored, err := backend.Store.Head(ctx, info.Hash)
		if err == nil && stored.LegacyChecksum == "" {
			if !samePayload(info, stored) {
				return Info{}, fmt.Errorf("backend %s: %w", backend.Name, ErrIntegrityMismatch)
			}
		} else if errors.Is(err, ErrNotFound) || (err == nil && stored.LegacyChecksum != "") {
			if _, err = file.Seek(0, io.SeekStart); err == nil {
				stored, err = backend.Store.Put(ctx, info, file)
			}
		}
		if err != nil {
			return Info{}, fmt.Errorf("write backend %s: %w", backend.Name, err)
		}
		if index == 0 {
			primary = stored
			primary.Backend = backend.Name
		}
		etags[backend.Name] = stored.ETag
	}
	primary.ETags = etags
	return primary, nil
}

func (s *MultiStore) Open(ctx context.Context, hash string) (io.ReadCloser, Info, error) {
	return s.open(ctx, hash, 0, 0, false)
}

func (s *MultiStore) OpenRange(ctx context.Context, hash string, start, end int64) (io.ReadCloser, Info, error) {
	return s.open(ctx, hash, start, end, true)
}

func (s *MultiStore) open(ctx context.Context, hash string, start, end int64, partial bool) (io.ReadCloser, Info, error) {
	var failures []error
	missing := true
	for _, backend := range s.backends {
		var reader io.ReadCloser
		var info Info
		var err error
		if partial {
			if ranges, ok := backend.Store.(RangeStore); ok {
				reader, info, err = ranges.OpenRange(ctx, hash, start, end)
			} else {
				reader, info, err = backend.Store.Open(ctx, hash)
				if err == nil {
					if start < 0 || end < start || end >= info.Size {
						err = errors.New("invalid object range")
					} else {
						_, err = io.CopyN(io.Discard, reader, start)
					}
					if err == nil {
						reader = &limitedReadCloser{io.LimitReader(reader, end-start+1), reader}
					} else {
						reader.Close()
					}
				}
			}
		} else {
			reader, info, err = backend.Store.Open(ctx, hash)
		}
		if err == nil {
			info.Backend = backend.Name
			return reader, info, nil
		}
		missing = missing && errors.Is(err, ErrNotFound)
		failures = append(failures, fmt.Errorf("read backend %s: %w", backend.Name, err))
		if ctx.Err() != nil {
			return nil, Info{}, ctx.Err()
		}
	}
	if missing {
		return nil, Info{}, ErrNotFound
	}
	return nil, Info{}, errors.Join(failures...)
}

type limitedReadCloser struct {
	io.Reader
	io.Closer
}

func (s *MultiStore) List(ctx context.Context) ([]Info, error) {
	return s.list(ctx)
}

func (s *MultiStore) list(ctx context.Context) ([]Info, error) {
	objects := make(map[string]Info)
	var failures []error
	successes := 0
	for _, backend := range s.backends {
		lister, ok := backend.Store.(ListStore)
		if !ok {
			return nil, fmt.Errorf("backend %s cannot list objects", backend.Name)
		}
		items, err := lister.List(ctx)
		if err != nil {
			failures = append(failures, fmt.Errorf("list backend %s: %w", backend.Name, err))
			log.Printf("storage backend listing failed backend=%s error=%q", backend.Name, err)
			continue
		}
		successes++
		for _, info := range items {
			if !utils.IoHashValid(info.Hash) {
				continue
			}
			if _, exists := objects[info.Hash]; !exists {
				info.Backend = backend.Name
				objects[info.Hash] = info
			}
		}
	}
	if successes == 0 {
		return nil, errors.Join(failures...)
	}
	result := make([]Info, 0, len(objects))
	for _, info := range objects {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Hash < result[j].Hash })
	return result, nil
}

// Stats reports logical objects from the first available backend, not the sum
// of replicas. It retains the S3 backend's asynchronous statistics cache.
func (s *MultiStore) Stats(ctx context.Context) (ObjectStats, error) {
	var failures []error
	for _, backend := range s.backends {
		if store, ok := backend.Store.(StatsStore); ok {
			stats, err := store.Stats(ctx)
			if err == nil {
				return stats, nil
			}
			failures = append(failures, err)
		}
	}
	return ObjectStats{}, errors.Join(failures...)
}
