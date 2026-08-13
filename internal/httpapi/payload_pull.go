package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"vaps/internal/blobstore"
	"vaps/internal/objectstore"
)

func (h *Handler) pullPayload(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAuth(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodHead:
		h.headPullPayload(w, r)
	case http.MethodGet:
		h.getPullPayload(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) headPullPayload(w http.ResponseWriter, r *http.Request) {
	hash, ok := queryHash(w, r)
	if !ok {
		return
	}
	memoryChecked := h.cache.Enabled()
	if cached, ok := h.memoryPayload(hash); ok {
		h.pull.recordMemoryHit()
		info := blobstore.Info{Hash: hash, Size: int64(len(cached))}
		setPayloadHeaders(w, info)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		return
	}
	reader, info, fromDisk, err := h.openPayload(r, hash)
	if err != nil {
		writePayloadOpenError(w, r, err)
		return
	}
	_ = reader.Close()
	h.recordPullOrigin(fromDisk, memoryChecked)
	setPayloadHeaders(w, info)
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getPullPayload(w http.ResponseWriter, r *http.Request) {
	hash, ok := queryHash(w, r)
	if !ok {
		return
	}
	rangeHeader := r.Header.Get("Range")
	memoryChecked := h.cache.Enabled()
	if cached, ok := h.memoryPayload(hash); ok {
		h.pull.recordMemoryHit()
		info := blobstore.Info{Hash: hash, Size: int64(len(cached))}
		setPayloadHeaders(w, info)
		if rangeHeader == "" {
			writeFullPayload(w, cached, info.Size)
			return
		}
		parsed, err := parseSingleRangeHeader(rangeHeader, info.Size)
		if err != nil {
			if errors.Is(err, errRangeNotSatisfiable) {
				w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(info.Size, 10))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeRangeResponse(w, cached, info.Size, parsed)
		return
	}
	if rangeHeader != "" {
		reader, info, parsed, handled, err := h.openRemoteRange(r.Context(), hash, rangeHeader)
		if handled {
			if err != nil {
				if errors.Is(err, errRangeNotSatisfiable) {
					w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(info.Size, 10))
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return
				}
				writePayloadOpenError(w, r, err)
				return
			}
			h.pull.recordRemote(memoryChecked)
			defer reader.Close()
			setPayloadHeaders(w, info)
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(parsed.start, 10)+"-"+strconv.FormatInt(parsed.end, 10)+"/"+strconv.FormatInt(info.Size, 10))
			w.Header().Set("Content-Length", strconv.FormatInt(parsed.length(), 10))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.Copy(w, reader)
			return
		}
	}
	reader, info, fromDisk, err := h.openPayload(r, hash)
	if err != nil {
		writePayloadOpenError(w, r, err)
		return
	}
	defer reader.Close()
	h.recordPullOrigin(fromDisk, memoryChecked)
	setPayloadHeaders(w, info)
	if rangeHeader != "" {
		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			http.Error(w, readErr.Error(), http.StatusInternalServerError)
			return
		}
		parsed, err := parseSingleRangeHeader(rangeHeader, info.Size)
		if err != nil {
			if errors.Is(err, errRangeNotSatisfiable) {
				w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(info.Size, 10))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeRangeResponse(w, data, info.Size, parsed)
		if h.cache != nil && h.cache.CanStore(info.Size) && h.cache.Add(hash, data) {
			h.markCached(hash)
		}
		return
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	w.WriteHeader(http.StatusOK)
	if h.cache != nil && h.cache.CanStore(info.Size) {
		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			return
		}
		_, _ = w.Write(data)
		if h.cache.Add(hash, data) {
			h.markCached(hash)
		}
		return
	}
	_, _ = io.Copy(w, reader)
}

func (h *Handler) memoryPayload(hash string) ([]byte, bool) {
	if !h.cache.Enabled() {
		return nil, false
	}
	return h.cache.Get(hash)
}

func (h *Handler) recordPullOrigin(fromDisk, memoryChecked bool) {
	if fromDisk {
		h.pull.recordDiskHit(memoryChecked)
		return
	}
	h.pull.recordRemote(memoryChecked)
}

func (h *Handler) openRemoteRange(ctx context.Context, hash, header string) (io.ReadCloser, blobstore.Info, byteRange, bool, error) {
	store, ok := h.objects.(objectstore.RangeStore)
	if !ok || h.meta == nil {
		return nil, blobstore.Info{}, byteRange{}, false, nil
	}
	cached, _, err := h.store.Exists(hash)
	if err != nil {
		return nil, blobstore.Info{}, byteRange{}, true, err
	}
	if cached {
		return nil, blobstore.Info{}, byteRange{}, false, nil
	}
	record, err := h.payloadRecord(ctx, hash)
	if err != nil {
		return nil, blobstore.Info{}, byteRange{}, true, err
	}
	info := blobstore.Info{Hash: record.Hash, ETag: record.ETag, Checksum: record.Checksum, Size: record.Size}
	parsed, err := parseSingleRangeHeader(header, info.Size)
	if err != nil {
		return nil, info, byteRange{}, true, err
	}
	reader, remote, err := store.OpenRange(ctx, hash, parsed.start, parsed.end)
	if err != nil {
		if errors.Is(err, objectstore.ErrNotFound) {
			err = os.ErrNotExist
		}
		return nil, info, byteRange{}, true, err
	}
	if err := validateRemotePayload(record, remote); err != nil {
		_ = reader.Close()
		return nil, info, byteRange{}, true, err
	}
	if err := h.recordPayloadETag(&record, remote); err != nil {
		_ = reader.Close()
		return nil, info, byteRange{}, true, err
	}
	return reader, info, parsed, true, nil
}

func (h *Handler) openPayload(r *http.Request, hash string) (io.ReadCloser, blobstore.Info, bool, error) {
	if h.meta != nil {
		if _, metadataErr := h.payloadRecord(r.Context(), hash); metadataErr != nil {
			return nil, blobstore.Info{}, false, metadataErr
		}
		return h.openDiskOrRemote(r.Context(), hash)
	}
	reader, info, err := h.store.Open(hash)
	return reader, info, err == nil, err
}

func writePayloadOpenError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, blobstore.ErrInvalidHash) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
