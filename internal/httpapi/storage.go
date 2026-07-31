package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"vaps/internal/blobstore"
	"vaps/internal/metadata"
	"vaps/internal/objectstore"
	"vaps/internal/utils"
)

// putPayloadDurably verifies an upload before synchronously committing it to
// the authoritative object store. The local blob store is only a cache.
func (h *Handler) putPayloadDurably(ctx context.Context, hash, checksum string, reader io.Reader) (blobstore.Info, error) {
	staged, err := h.store.Stage(hash, checksum, reader)
	if err != nil {
		return blobstore.Info{}, err
	}
	defer staged.Discard()

	created := false
	etag := ""
	if h.objects != nil {
		remote, headErr := h.objects.Head(ctx, hash)
		if headErr == nil {
			etag = remote.ETag
		}
		switch {
		case errors.Is(headErr, objectstore.ErrNotFound):
			file, err := staged.Open()
			if err != nil {
				return blobstore.Info{}, err
			}
			putInfo, putErr := h.objects.Put(ctx, objectstore.Info{
				Hash:     staged.Hash,
				Checksum: staged.Checksum,
				Size:     staged.Size,
			}, file)
			closeErr := file.Close()
			if putErr != nil {
				return blobstore.Info{}, putErr
			}
			if closeErr != nil {
				return blobstore.Info{}, closeErr
			}
			created = true
			etag = putInfo.ETag
		case headErr != nil:
			return blobstore.Info{}, headErr
		case remote.LegacyChecksum != "":
			_, migratedETag, err := h.reconcileLegacyObject(ctx, hash, staged.Checksum)
			if err != nil {
				return blobstore.Info{}, err
			}
			if migratedETag != "" {
				etag = migratedETag
			}
		case remote.Checksum != staged.Checksum || remote.Size != staged.Size:
			return blobstore.Info{}, fmt.Errorf("%w: existing object does not match upload", objectstore.ErrIntegrityMismatch)
		}
	}

	info := staged.Info
	info.Created = created
	if cached, publishErr := h.store.Publish(staged); publishErr == nil {
		info = cached
		if h.objects != nil {
			info.Created = created
		}
	} else {
		log.Printf("local cache publish failed hash=%s error=%q", hash, publishErr)
	}
	info.ETag = etag
	return info, nil
}

func (h *Handler) commitPayloadMetadata(info blobstore.Info) error {
	if h.meta == nil {
		return nil
	}
	now := time.Now().UTC()
	status := metadata.StatusStored
	if exists, _, err := h.store.Exists(info.Hash); err == nil && exists {
		status = metadata.StatusCached
	} else if err != nil {
		return err
	}
	record, err := h.meta.GetPayload(info.Hash)
	if errors.Is(err, metadata.ErrNotFound) {
		return h.meta.PutPayload(metadata.Payload{
			Hash:      info.Hash,
			ETag:      info.ETag,
			Checksum:  info.Checksum,
			Size:      info.Size,
			Status:    status,
			CreatedAt: &now,
		})
	}
	if err != nil {
		return err
	}
	if record.Checksum != "" && (record.Checksum != info.Checksum || record.Size != info.Size) {
		return fmt.Errorf("%w: metadata does not match upload", objectstore.ErrIntegrityMismatch)
	}
	record.Checksum = info.Checksum
	if info.ETag != "" {
		record.ETag = info.ETag
	}
	record.Size = info.Size
	record.Status = status
	if record.CreatedAt == nil {
		record.CreatedAt = &now
	}
	return h.meta.PutPayload(record)
}

func validateRemotePayload(record metadata.Payload, remote objectstore.Info) error {
	if remote.Hash != record.Hash {
		return fmt.Errorf(
			"%w: remote object hash %q does not match metadata hash %q",
			objectstore.ErrIntegrityMismatch,
			remote.Hash,
			record.Hash,
		)
	}
	if remote.Checksum != record.Checksum {
		return fmt.Errorf(
			"%w: remote object checksum %q does not match metadata checksum %q",
			objectstore.ErrIntegrityMismatch,
			remote.Checksum,
			record.Checksum,
		)
	}
	if remote.Size != record.Size {
		return fmt.Errorf(
			"%w: remote object size %d does not match metadata size %d",
			objectstore.ErrIntegrityMismatch,
			remote.Size,
			record.Size,
		)
	}
	if record.ETag != "" && remote.ETag != record.ETag {
		return fmt.Errorf(
			"%w: remote object ETag %q does not match metadata ETag %q",
			objectstore.ErrIntegrityMismatch,
			remote.ETag,
			record.ETag,
		)
	}
	return nil
}

func (h *Handler) recordPayloadETag(record *metadata.Payload, remote objectstore.Info) error {
	if h.meta == nil || record.ETag != "" || remote.ETag == "" {
		return nil
	}
	record.ETag = remote.ETag
	return h.meta.PutPayload(*record)
}

func (h *Handler) payloadRecord(ctx context.Context, hash string) (metadata.Payload, error) {
	if h.meta == nil {
		return metadata.Payload{}, nil
	}
	record, err := h.meta.GetPayload(hash)
	if !errors.Is(err, metadata.ErrNotFound) {
		if err == nil && record.Checksum == "" && h.objects != nil {
			remote, headErr := h.objects.Head(ctx, hash)
			if headErr != nil {
				return metadata.Payload{}, headErr
			}
			changed := false
			switch {
			case remote.Checksum != "":
				record.Checksum = remote.Checksum
				record.LegacyChecksum = ""
				changed = true
			case remote.LegacyChecksum != "":
				checksum, etag, migrateErr := h.reconcileLegacyObject(ctx, hash, "")
				if migrateErr != nil {
					return metadata.Payload{}, migrateErr
				}
				record.Checksum = checksum
				record.LegacyChecksum = ""
				if etag != "" {
					record.ETag = etag
				}
				changed = true
			}
			if record.ETag == "" && remote.ETag != "" {
				record.ETag = remote.ETag
				changed = true
			}
			if changed {
				if err := h.meta.PutPayload(record); err != nil {
					return metadata.Payload{}, err
				}
			}
		}
		return record, err
	}
	if h.objects == nil {
		return metadata.Payload{}, os.ErrNotExist
	}
	remote, err := h.objects.Head(ctx, hash)
	if errors.Is(err, objectstore.ErrNotFound) {
		return metadata.Payload{}, os.ErrNotExist
	}
	if err != nil {
		return metadata.Payload{}, err
	}
	if remote.LegacyChecksum != "" {
		checksum, etag, migrateErr := h.reconcileLegacyObject(ctx, hash, "")
		if migrateErr != nil {
			return metadata.Payload{}, migrateErr
		}
		remote.Checksum = checksum
		remote.LegacyChecksum = ""
		if etag != "" {
			remote.ETag = etag
		}
	}
	now := time.Now().UTC()
	record = metadata.Payload{
		Hash:      remote.Hash,
		ETag:      remote.ETag,
		Checksum:  remote.Checksum,
		Size:      remote.Size,
		Status:    metadata.StatusStored,
		CreatedAt: &now,
	}
	if err := h.meta.PutPayload(record); err != nil {
		return metadata.Payload{}, err
	}
	return record, nil
}

func (h *Handler) reconcileLegacyObject(ctx context.Context, hash, expectedChecksum string) (string, string, error) {
	if h.objects == nil {
		return "", "", errors.New("object store is not configured")
	}
	reader, remote, err := h.objects.Open(ctx, hash)
	if err != nil {
		return "", "", err
	}
	hasher := utils.NewChecksum()
	size, copyErr := io.Copy(hasher, reader)
	closeErr := reader.Close()
	if copyErr != nil {
		return "", "", copyErr
	}
	if closeErr != nil {
		return "", "", closeErr
	}
	if size != remote.Size {
		return "", "", fmt.Errorf("%w: legacy object size does not match metadata", objectstore.ErrIntegrityMismatch)
	}
	checksum := utils.ChecksumDigestHex(hasher)
	if expectedChecksum != "" && checksum != expectedChecksum {
		return "", "", fmt.Errorf("%w: legacy object checksum does not match upload", objectstore.ErrIntegrityMismatch)
	}

	reader, _, err = h.objects.Open(ctx, hash)
	if err != nil {
		return "", "", err
	}
	putInfo, putErr := h.objects.Put(ctx, objectstore.Info{
		Hash:     hash,
		Checksum: checksum,
		Size:     size,
	}, reader)
	closeErr = reader.Close()
	if putErr != nil {
		return "", "", putErr
	}
	if closeErr != nil {
		return "", "", closeErr
	}
	return checksum, putInfo.ETag, nil
}
