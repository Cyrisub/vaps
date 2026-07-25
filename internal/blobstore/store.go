package blobstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"vaps/internal/platform"
	"vaps/internal/utils"
)

var (
	ErrInvalidHash  = errors.New("invalid iohash")
	ErrHashMismatch = errors.New("payload hash does not match requested iohash")
)

type Store struct {
	root          string
	maxCacheBytes int64
	mu            sync.Mutex
}

type Info struct {
	Hash        string `json:"hash"`
	ContentHash string `json:"content_hash,omitempty"`
	Size        int64  `json:"size"`
	Created     bool   `json:"stored"`
}

// Staged is a verified payload that has not yet been published into the local
// cache. It lets the caller durably write the authoritative object before
// making the optional cache entry visible.
type Staged struct {
	Info
	path string
}

func New(root string) *Store {
	return &Store{root: root}
}

// NewWithMaxBytes creates a local cache with an optional byte limit. A zero
// limit retains every cache entry, primarily for backwards-compatible tests.
func NewWithMaxBytes(root string, maxCacheBytes int64) *Store {
	return &Store{root: root, maxCacheBytes: maxCacheBytes}
}

func (s *Store) Path(hash string) (string, error) {
	relativePath, err := RelativePath(hash)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, filepath.FromSlash(relativePath)), nil
}

func RelativePath(hash string) (string, error) {
	if !ValidHash(hash) {
		return "", ErrInvalidHash
	}
	return "blobs/" + hash[:2] + "/" + hash[2:4] + "/" + hash + ".upayload", nil
}

func (s *Store) Put(hash string, reader io.Reader) (Info, error) {
	staged, err := s.Stage(hash, reader)
	if err != nil {
		return Info{}, err
	}
	defer staged.Discard()
	return s.Publish(staged)
}

// Stage streams a payload to a private temporary file and validates both its
// Unreal FIoHash and complete BLAKE3-256 digest.
func (s *Store) Stage(hash string, reader io.Reader) (*Staged, error) {
	if _, err := s.Path(hash); err != nil {
		return nil, err
	}

	tmpDir := filepath.Join(s.root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(tmpDir, "upload-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := utils.NewIoHash()
	size, copyErr := io.Copy(io.MultiWriter(tmp, hasher), reader)
	if copyErr != nil {
		_ = tmp.Close()
		return nil, copyErr
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	actual := utils.IoHashDigestHex(hasher)
	if actual != hash {
		return nil, fmt.Errorf("%w: got %s want %s", ErrHashMismatch, actual, hash)
	}
	cleanup = false
	return &Staged{Info: Info{
		Hash:        hash,
		ContentHash: utils.Blake3DigestHex(hasher),
		Size:        size,
	}, path: tmpPath}, nil
}

// Publish atomically makes a staged payload visible in the local cache.
func (s *Store) Publish(staged *Staged) (Info, error) {
	if staged == nil || staged.path == "" {
		return Info{}, errors.New("invalid staged payload")
	}
	target, err := s.Path(staged.Hash)
	if err != nil {
		return Info{}, err
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Link(staged.path, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := s.stat(staged.Hash, target)
			if statErr != nil {
				return Info{}, statErr
			}
			if info.Size != staged.Size {
				return Info{}, fmt.Errorf("%w: cached size %d does not match staged size %d", ErrHashMismatch, info.Size, staged.Size)
			}
			info.ContentHash = staged.ContentHash
			info.Created = false
			return info, nil
		}
		return Info{}, err
	}
	if err := platform.SyncDir(filepath.Dir(target)); err != nil {
		return Info{}, err
	}

	if err := os.Remove(staged.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Info{}, err
	}
	staged.path = ""
	if err := s.evict(); err != nil {
		return Info{}, err
	}
	return Info{Hash: staged.Hash, ContentHash: staged.ContentHash, Size: staged.Size, Created: true}, nil
}

// Open opens the verified staging file for a one-shot authoritative upload.
func (s *Staged) Open() (*os.File, error) {
	if s == nil || s.path == "" {
		return nil, errors.New("staged payload has been discarded")
	}
	return os.Open(s.path)
}

// Discard removes a staging file. It is safe to call repeatedly.
func (s *Staged) Discard() {
	if s == nil || s.path == "" {
		return
	}
	_ = os.Remove(s.path)
	s.path = ""
}

func (s *Store) Exists(hash string) (bool, Info, error) {
	path, err := s.Path(hash)
	if err != nil {
		return false, Info{}, err
	}
	info, err := s.stat(hash, path)
	if errors.Is(err, os.ErrNotExist) {
		return false, Info{}, nil
	}
	if err != nil {
		return false, Info{}, err
	}
	return true, info, nil
}

func (s *Store) Open(hash string) (io.ReadCloser, Info, error) {
	path, err := s.Path(hash)
	if err != nil {
		return nil, Info{}, err
	}
	info, err := s.stat(hash, path)
	if err != nil {
		return nil, Info{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, Info{}, err
	}
	return file, info, nil
}

func ValidHash(hash string) bool {
	return utils.IoHashValid(hash)
}

func (s *Store) stat(hash, path string) (Info, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return Info{}, err
	}
	if stat.IsDir() {
		return Info{}, fmt.Errorf("payload path is a directory: %s", path)
	}
	return Info{Hash: hash, Size: stat.Size()}, nil
}

type cacheFile struct {
	path    string
	size    int64
	modTime int64
}

func (s *Store) evict() error {
	if s.maxCacheBytes <= 0 {
		return nil
	}
	root := filepath.Join(s.root, "blobs")
	entries := make([]cacheFile, 0)
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		entries = append(entries, cacheFile{path: path, size: info.Size(), modTime: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].modTime != entries[j].modTime {
			return entries[i].modTime < entries[j].modTime
		}
		return entries[i].path < entries[j].path
	})
	for _, entry := range entries {
		if total <= s.maxCacheBytes {
			break
		}
		if err := os.Remove(entry.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		total -= entry.size
	}
	return nil
}
