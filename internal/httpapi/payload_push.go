package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vaps/internal/blobstore"
	"vaps/internal/metadata"
	"vaps/internal/uploadsession"
)

func (h *Handler) pushPayload(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAuth(w, r); !ok {
		return
	}
	if h.uploads == nil {
		http.Error(w, "upload sessions are not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodOptions:
		h.pushOptions(w, r)
	case http.MethodPost:
		if isTusRequest(r) {
			h.tusCreateUpload(w, r)
			return
		}
		h.directPush(w, r)
	case http.MethodHead:
		h.tusHeadUpload(w, r)
	case http.MethodPatch:
		h.tusPatchUpload(w, r)
	case http.MethodDelete:
		h.tusDeleteUpload(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) pushOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Tus-Resumable", "1.0.0")
	w.Header().Set("Tus-Version", "1.0.0")
	w.Header().Set("Tus-Extension", "creation,creation-with-upload,concatenation,expiration,termination,checksum")
	w.Header().Set("Tus-Checksum-Algorithm", "sha1,sha256")
	w.Header().Set("Upload-Direct-Max-Bytes", strconv.FormatInt(h.opts.UploadDirectMaxBytes, 10))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) directPush(w http.ResponseWriter, r *http.Request) {
	hash, ok := queryHash(w, r)
	if !ok {
		return
	}
	if r.ContentLength > h.opts.UploadDirectMaxBytes {
		http.Error(w, "payload exceeds direct upload limit", http.StatusRequestEntityTooLarge)
		return
	}
	limited := http.MaxBytesReader(w, r.Body, h.opts.UploadDirectMaxBytes+1)
	info, err := h.store.Put(hash, limited)
	if err != nil {
		if errors.Is(err, blobstore.ErrInvalidHash) || errors.Is(err, blobstore.ErrHashMismatch) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "payload exceeds direct upload limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if info.Created {
		status = http.StatusCreated
	}
	if err := h.commitPayloadMetadata(r, info); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, status, putResponse{Hash: info.Hash, Size: info.Size, Stored: true})
}

func (h *Handler) tusCreateUpload(w http.ResponseWriter, r *http.Request) {
	hash, ok := queryHash(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(r.Header.Get("Tus-Resumable")) == "" {
		http.Error(w, "missing Tus-Resumable header", http.StatusBadRequest)
		return
	}
	concat := strings.TrimSpace(r.Header.Get("Upload-Concat"))
	if concat != "" {
		h.tusCreateConcat(w, r, hash, concat)
		return
	}
	length, err := parseUploadLength(r.Header.Get("Upload-Length"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session, err := h.uploads.Create(hash, uploadsession.KindRegular, length, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	offset := int64(0)
	if r.ContentLength > 0 {
		session, err = h.appendUploadBody(w, r, session, 0, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		offset = session.Offset
		if session.Offset == session.Length {
			if err := h.finalizeUpload(r, w, session); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			return
		}
	}
	h.writeTusCreated(w, session, offset)
}

func (h *Handler) tusCreateConcat(w http.ResponseWriter, r *http.Request, hash, concat string) {
	if strings.HasPrefix(concat, "partial") {
		session, err := h.uploads.Create(hash, uploadsession.KindPartial, 0, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.writeTusCreated(w, session, 0)
		return
	}
	if !strings.HasPrefix(concat, "final;") {
		http.Error(w, "invalid Upload-Concat header", http.StatusBadRequest)
		return
	}
	refs := strings.Fields(strings.TrimPrefix(concat, "final;"))
	partials := make([]uploadsession.Session, 0, len(refs))
	for _, ref := range refs {
		partial, err := h.uploads.GetByRef(ref)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		partials = append(partials, partial)
	}
	final, err := h.uploads.BuildFinalFromPartials(hash, partials)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.finalizeUpload(r, w, final); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

func (h *Handler) tusHeadUpload(w http.ResponseWriter, r *http.Request) {
	uploadID := strings.TrimSpace(r.URL.Query().Get("upload_id"))
	if uploadID == "" {
		http.Error(w, "upload_id is required", http.StatusBadRequest)
		return
	}
	session, err := h.uploads.Get(uploadID)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
	w.Header().Set("Upload-Length", strconv.FormatInt(session.Length, 10))
	setTusHeaders(w, formatExpires(session.ExpiresAt))
	if session.Kind == uploadsession.KindPartial {
		w.Header().Set("Upload-Concat", "partial")
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) tusPatchUpload(w http.ResponseWriter, r *http.Request) {
	hash, ok := queryHash(w, r)
	if !ok {
		return
	}
	uploadID := strings.TrimSpace(r.URL.Query().Get("upload_id"))
	if uploadID == "" {
		http.Error(w, "upload_id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(r.Header.Get("Tus-Resumable")) == "" {
		http.Error(w, "missing Tus-Resumable header", http.StatusBadRequest)
		return
	}
	offsetHeader := strings.TrimSpace(r.Header.Get("Upload-Offset"))
	offset, err := strconv.ParseInt(offsetHeader, 10, 64)
	if err != nil || offset < 0 {
		http.Error(w, "invalid Upload-Offset", http.StatusBadRequest)
		return
	}
	session, err := h.uploads.Get(uploadID)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	if session.ExpectedHash != hash {
		http.Error(w, "iohash does not match upload session", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if algorithm, expected, err := parseUploadChecksum(r.Header.Get("Upload-Checksum")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if algorithm != "" && !checksumMatches(algorithm, expected, body) {
		http.Error(w, "upload checksum mismatch", http.StatusBadRequest)
		return
	}
	session, err = h.uploads.Append(uploadID, offset, bytes.NewReader(body))
	if err != nil {
		writeUploadError(w, err)
		return
	}
	if session.Offset == session.Length {
		if err := h.finalizeUpload(r, w, session); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
	setTusHeaders(w, formatExpires(session.ExpiresAt))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) tusDeleteUpload(w http.ResponseWriter, r *http.Request) {
	uploadID := strings.TrimSpace(r.URL.Query().Get("upload_id"))
	if uploadID == "" {
		http.Error(w, "upload_id is required", http.StatusBadRequest)
		return
	}
	if err := h.uploads.Terminate(uploadID); err != nil {
		writeUploadError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) appendUploadBody(w http.ResponseWriter, r *http.Request, session uploadsession.Session, offset int64, body io.Reader) (uploadsession.Session, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return uploadsession.Session{}, err
	}
	if algorithm, expected, err := parseUploadChecksum(r.Header.Get("Upload-Checksum")); err != nil {
		return uploadsession.Session{}, err
	} else if algorithm != "" && !checksumMatches(algorithm, expected, data) {
		return uploadsession.Session{}, errors.New("upload checksum mismatch")
	}
	return h.uploads.Append(session.ID, offset, bytes.NewReader(data))
}

func (h *Handler) finalizeUpload(r *http.Request, w http.ResponseWriter, session uploadsession.Session) error {
	file, current, err := h.uploads.OpenTemp(session.ID)
	if err != nil {
		return err
	}
	info, err := h.store.Put(current.ExpectedHash, file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := h.commitPayloadMetadata(r, info); err != nil {
		return err
	}
	if _, err := h.uploads.MarkCompleted(session.ID); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, putResponse{Hash: info.Hash, Size: info.Size, Stored: true})
	return nil
}

func (h *Handler) writeTusCreated(w http.ResponseWriter, session uploadsession.Session, offset int64) {
	location := tusUploadLocation(session.ExpectedHash, session.ID)
	w.Header().Set("Location", location)
	w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
	if session.Length > 0 {
		w.Header().Set("Upload-Length", strconv.FormatInt(session.Length, 10))
	}
	if session.Kind == uploadsession.KindPartial {
		w.Header().Set("Upload-Concat", "partial")
	}
	setTusHeaders(w, formatExpires(session.ExpiresAt))
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) commitPayloadMetadata(r *http.Request, info blobstore.Info) error {
	if h.meta == nil {
		if h.backup != nil {
			return h.queueBackup(r.Context(), info.Hash)
		}
		return nil
	}
	record, err := h.meta.GetPayload(info.Hash)
	if errors.Is(err, metadata.ErrNotFound) {
		now := time.Now().UTC()
		record = metadata.Payload{
			Hash:      info.Hash,
			Size:      info.Size,
			Status:    metadata.StatusLocal,
			CreatedAt: &now,
		}
	} else if err != nil {
		return err
	} else {
		now := time.Now().UTC()
		record.Size = info.Size
		record.Status |= metadata.StatusLocal
		if record.CreatedAt == nil {
			record.CreatedAt = &now
		}
	}
	if err := h.meta.PutPayload(record); err != nil {
		return err
	}
	if h.backup != nil {
		return h.queueBackup(r.Context(), info.Hash)
	}
	return nil
}

func writeUploadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, uploadsession.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, uploadsession.ErrExpired):
		http.Error(w, err.Error(), http.StatusGone)
	case errors.Is(err, uploadsession.ErrTerminated):
		http.Error(w, err.Error(), http.StatusGone)
	case errors.Is(err, uploadsession.ErrOffsetMismatch):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
