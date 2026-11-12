package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"vaps/internal/utils"
)

// FilesystemStore persists immutable objects in a directory, including an SMB mount.
// Payload and metadata are published together by renaming a temporary directory.
type FilesystemStore struct {
	root        string
	mountSource string
}

func NewFilesystem(root, mountSource string) (*FilesystemStore, error) {
	if root == "" {
		return nil, errors.New("filesystem directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &FilesystemStore{root: abs, mountSource: mountSource}
	if err := s.available(); err != nil {
		// An unavailable network replica must not prevent the preferred backend
		// from serving. Every operation rechecks the mount before touching it.
		if mountSource != "" {
			return s, nil
		}
		return nil, err
	}
	if err := mkdirDurable(abs); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FilesystemStore) available() error {
	if s.mountSource == "" {
		return nil
	}
	return checkSMBMount(s.root, s.mountSource)
}

func (s *FilesystemStore) objectDir(hash string) (string, error) {
	if !utils.IoHashValid(hash) {
		return "", errors.New("invalid object hash")
	}
	return filepath.Join(s.root, hash[:2], hash[2:4], hash), nil
}

func (s *FilesystemStore) Head(ctx context.Context, hash string) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if err := s.available(); err != nil {
		return Info{}, err
	}
	dir, err := s.objectDir(hash)
	if err != nil {
		return Info{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "info.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}, fmt.Errorf("%w: invalid filesystem metadata", ErrIntegrityMismatch)
	}
	if validateInfo(info) != nil || info.Hash != hash || !strings.HasPrefix(info.ETag, "sha256:") {
		return Info{}, ErrIntegrityMismatch
	}
	stat, err := os.Stat(filepath.Join(dir, "payload"))
	if err != nil || !stat.Mode().IsRegular() || stat.Size() != info.Size {
		return Info{}, ErrIntegrityMismatch
	}
	return info, nil
}

func (s *FilesystemStore) Put(ctx context.Context, info Info, reader io.Reader) (Info, error) {
	if err := validateInfo(info); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if err := s.available(); err != nil {
		return Info{}, err
	}
	dir, _ := s.objectDir(info.Hash)
	parent := filepath.Dir(dir)
	if err := mkdirDurable(parent); err != nil {
		return Info{}, err
	}
	tmp, err := os.MkdirTemp(parent, ".pending-")
	if err != nil {
		return Info{}, err
	}
	defer os.RemoveAll(tmp)
	file, err := os.OpenFile(filepath.Join(tmp, "payload"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return Info{}, err
	}
	crc := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	digest := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, crc, digest), &contextReader{ctx, reader})
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return Info{}, err
	}
	if n != info.Size || fmt.Sprintf("%08x", crc.Sum32()) != info.Checksum {
		return Info{}, ErrIntegrityMismatch
	}
	info.ETag = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	info.Backend = ""
	data, err := json.Marshal(info)
	if err != nil {
		return Info{}, err
	}
	meta, err := os.OpenFile(filepath.Join(tmp, "info.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return Info{}, err
	}
	_, writeErr := meta.Write(data)
	syncErr = meta.Sync()
	closeErr = meta.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if err := s.available(); err != nil {
		return Info{}, err
	}
	if err := syncObjectDir(tmp); err != nil {
		return Info{}, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		existing, headErr := s.Head(ctx, info.Hash)
		if headErr != nil {
			return Info{}, errors.Join(err, headErr)
		}
		if existing.Size != info.Size || existing.Checksum != info.Checksum || existing.ETag != info.ETag {
			return Info{}, ErrIntegrityMismatch
		}
		return existing, nil
	}
	if err := syncObjectDir(parent); err != nil {
		return Info{}, err
	}
	return info, nil
}

func (s *FilesystemStore) Open(ctx context.Context, hash string) (io.ReadCloser, Info, error) {
	info, err := s.Head(ctx, hash)
	if err != nil {
		return nil, Info{}, err
	}
	dir, _ := s.objectDir(hash)
	file, err := os.Open(filepath.Join(dir, "payload"))
	if err != nil {
		return nil, Info{}, err
	}
	// Verify the actual bytes before serving them, including fallback reads.
	crc := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(crc, digest), &contextReader{ctx, file})
	if err == nil && (n != info.Size || fmt.Sprintf("%08x", crc.Sum32()) != info.Checksum || "sha256:"+hex.EncodeToString(digest.Sum(nil)) != info.ETag) {
		err = ErrIntegrityMismatch
	}
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		file.Close()
		return nil, Info{}, err
	}
	return file, info, nil
}

func (s *FilesystemStore) OpenRange(ctx context.Context, hash string, start, end int64) (io.ReadCloser, Info, error) {
	reader, info, err := s.Open(ctx, hash)
	if err != nil {
		return nil, Info{}, err
	}
	if start < 0 || end < start || end >= info.Size {
		reader.Close()
		return nil, Info{}, errors.New("invalid object range")
	}
	file := reader.(*os.File)
	return &sectionReadCloser{io.NewSectionReader(file, start, end-start+1), file}, info, nil
}

func (s *FilesystemStore) List(ctx context.Context) ([]Info, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	var objects []Info
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".pending-") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == "info.json" {
			info, err := s.Head(ctx, filepath.Base(filepath.Dir(path)))
			if err != nil {
				return err
			}
			objects = append(objects, info)
		}
		return nil
	})
	return objects, err
}

func (s *FilesystemStore) Stats(ctx context.Context) (ObjectStats, error) {
	objects, err := s.List(ctx)
	stats := ObjectStats{ObjectCount: int64(len(objects))}
	for _, info := range objects {
		stats.TotalBytes += info.Size
	}
	return stats, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type sectionReadCloser struct {
	*io.SectionReader
	io.Closer
}

// remove is used only for completed jobs in the local replication outbox.
func (s *FilesystemStore) remove(hash string) error {
	dir, err := s.objectDir(hash)
	if err != nil {
		return err
	}
	parent := filepath.Dir(dir)
	gc, err := os.MkdirTemp(parent, ".pending-gc-")
	if err != nil {
		return err
	}
	if err := os.Remove(gc); err != nil {
		return err
	}
	if err := os.Rename(dir, gc); err != nil {
		return err
	}
	if err := syncObjectDir(parent); err != nil {
		return err
	}
	return os.RemoveAll(gc)
}

func mkdirDurable(path string) error {
	stat, err := os.Stat(path)
	if err == nil {
		if !stat.IsDir() {
			return fmt.Errorf("%s is not a directory", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if err := mkdirDurable(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncObjectDir(parent)
}
