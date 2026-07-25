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
)

// putPayloadDurably verifies an upload before synchronously committing it to
// the authoritative object store. The local blob store is only a cache.
func (h *Handler) putPayloadDurably(ctx context.Context, hash string, reader io.Reader) (blobstore.Info, error) {
	staged, err := h.store.Stage(hash, reader)
	if err != nil {
		return blobstore.Info{}, err
	}
	defer staged.Discard()

	created := false
	if h.objects != nil {
		remote, headErr := h.objects.Head(ctx, hash)
		switch {
		case errors.Is(headErr, objectstore.ErrNotFound):
			file, err := staged.Open()
			if err != nil {
				return blobstore.Info{}, err
			}
			putErr := h.objects.Put(ctx, objectstore.Info{
				Hash:        staged.Hash,
				ContentHash: staged.ContentHash,
				Size:        staged.Size,
			}, file)
			closeErr := file.Close()
			if putErr != nil {
				return blobstore.Info{}, putErr
			}
			if closeErr != nil {
				return blobstore.Info{}, closeErr
			}
			created = true
		case headErr != nil:
			return blobstore.Info{}, headErr
		case remote.ContentHash != staged.ContentHash || remote.Size != staged.Size:
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
			Hash:        info.Hash,
			ContentHash: info.ContentHash,
			Size:        info.Size,
			Status:      status,
			CreatedAt:   &now,
		})
	}
	if err != nil {
		return err
	}
	if record.ContentHash != "" && (record.ContentHash != info.ContentHash || record.Size != info.Size) {
		return fmt.Errorf("%w: metadata does not match upload", objectstore.ErrIntegrityMismatch)
	}
	record.ContentHash = info.ContentHash
	record.Size = info.Size
	record.Status = status
	if record.CreatedAt == nil {
		record.CreatedAt = &now
	}
	return h.meta.PutPayload(record)
}

func (h *Handler) payloadRecord(ctx context.Context, hash string) (metadata.Payload, error) {
	if h.meta == nil {
		return metadata.Payload{}, nil
	}
	record, err := h.meta.GetPayload(hash)
	if !errors.Is(err, metadata.ErrNotFound) {
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
	now := time.Now().UTC()
	record = metadata.Payload{
		Hash:        remote.Hash,
		ContentHash: remote.ContentHash,
		Size:        remote.Size,
		Status:      metadata.StatusStored,
		CreatedAt:   &now,
	}
	if err := h.meta.PutPayload(record); err != nil {
		return metadata.Payload{}, err
	}
	return record, nil
}
