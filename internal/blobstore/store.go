package blobstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"vaps/internal/iohash"
	"vaps/internal/platform"
)

var (
	ErrInvalidHash  = errors.New("invalid iohash")
	ErrHashMismatch = errors.New("payload hash does not match requested iohash")
)

type Store struct {
	root string
}

type Info struct {
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	Created bool   `json:"stored"`
}

func New(root string) *Store {
	return &Store{root: root}
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
	target, err := s.Path(hash)
	if err != nil {
		return Info{}, err
	}
	if info, err := s.stat(hash, target); err == nil {
		info.Created = false
		return info, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Info{}, err
	}

	tmpDir := filepath.Join(s.root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return Info{}, err
	}
	tmp, err := os.CreateTemp(tmpDir, "upload-*")
	if err != nil {
		return Info{}, err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := iohash.New()
	size, copyErr := io.Copy(io.MultiWriter(tmp, hasher), reader)
	if copyErr != nil {
		_ = tmp.Close()
		return Info{}, copyErr
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return Info{}, err
	}
	if err := tmp.Close(); err != nil {
		return Info{}, err
	}

	actual := iohash.DigestHex(hasher)
	if actual != hash {
		return Info{}, fmt.Errorf("%w: got %s want %s", ErrHashMismatch, actual, hash)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Info{}, err
	}
	if err := os.Link(tmpPath, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := s.stat(hash, target)
			if statErr != nil {
				return Info{}, statErr
			}
			info.Created = false
			return info, nil
		}
		return Info{}, err
	}
	if err := platform.SyncDir(filepath.Dir(target)); err != nil {
		return Info{}, err
	}

	cleanup = false
	if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Info{}, err
	}
	return Info{Hash: hash, Size: size, Created: true}, nil
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
	return iohash.Valid(hash)
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
