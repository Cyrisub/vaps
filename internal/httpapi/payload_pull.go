package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"vaps/internal/blobstore"
	"vaps/internal/metadata"
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
	reader, info, err := h.openPayload(r, hash)
	if err != nil {
		writePayloadOpenError(w, r, err)
		return
	}
	_ = reader.Close()
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
	if h.cache != nil {
		if cached, ok := h.cache.Get(hash); ok {
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
	}
	reader, info, err := h.openPayload(r, hash)
	if err != nil {
		writePayloadOpenError(w, r, err)
		return
	}
	defer reader.Close()
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

func (h *Handler) openPayload(r *http.Request, hash string) (io.ReadCloser, blobstore.Info, error) {
	if h.meta != nil {
		if _, metadataErr := h.meta.GetPayload(hash); errors.Is(metadataErr, metadata.ErrNotFound) {
			return nil, blobstore.Info{}, os.ErrNotExist
		} else if metadataErr != nil {
			return nil, blobstore.Info{}, metadataErr
		}
		return h.openPayloadWithMetadata(r.Context(), hash)
	}
	return h.store.Open(hash)
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
