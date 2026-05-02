package httpapi

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"vaps/internal/blobstore"
	"vaps/internal/cache"
	"vaps/internal/metadata"
)

type Handler struct {
	store *blobstore.Store
	meta  *metadata.Store
	cache *cache.Cache
}

type existsRequest struct {
	Hashes []string `json:"hashes"`
}

type existsResponse struct {
	Items map[string]existsItem `json:"items"`
}

type existsItem struct {
	Exists bool  `json:"exists"`
	Size   int64 `json:"size,omitempty"`
}

type putResponse struct {
	Hash   string `json:"hash"`
	Size   int64  `json:"size"`
	Stored bool   `json:"stored"`
}

type dashboardStats struct {
	Metadata metadata.Stats `json:"metadata"`
	Cache    cache.Stats    `json:"cache"`
}

var errLocalPayloadMissing = errors.New("metadata record exists but local payload is missing")

var dashboardTemplate = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>vaps dashboard</title>
</head>
<body>
  <h1>vaps dashboard</h1>
  <p>Read-only payload server statistics.</p>
  <pre id="stats">{{ . }}</pre>
</body>
</html>
`))

func New(store *blobstore.Store) http.Handler {
	return &Handler{store: store}
}

func NewWithMetadata(store *blobstore.Store, meta *metadata.Store) http.Handler {
	return &Handler{store: store, meta: meta}
}

func NewWithCache(store *blobstore.Store, lru *cache.Cache) http.Handler {
	return &Handler{store: store, cache: lru}
}

func NewWithMetadataAndCache(store *blobstore.Store, meta *metadata.Store, lru *cache.Cache) http.Handler {
	return &Handler{store: store, meta: meta, cache: lru}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		h.health(w)
	case r.Method == http.MethodGet && r.URL.Path == "/dashboard":
		h.dashboard(w)
	case r.Method == http.MethodGet && r.URL.Path == "/dashboard/stats":
		h.dashboardStats(w)
	case r.URL.Path == "/v1/payload" && r.Method == http.MethodHead:
		h.headPayload(w, r)
	case r.URL.Path == "/v1/payload" && r.Method == http.MethodGet:
		h.getPayload(w, r)
	case r.URL.Path == "/v1/payload" && r.Method == http.MethodPut:
		h.putPayload(w, r)
	case r.URL.Path == "/v1/payload/exists" && r.Method == http.MethodPost:
		h.exists(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) health(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (h *Handler) dashboard(w http.ResponseWriter) {
	stats, err := h.collectDashboardStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = dashboardTemplate.Execute(w, stats)
}

func (h *Handler) dashboardStats(w http.ResponseWriter) {
	stats, err := h.collectDashboardStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) headPayload(w http.ResponseWriter, r *http.Request) {
	hash, ok := queryHash(w, r)
	if !ok {
		return
	}
	exists, info, err := h.store.Exists(hash)
	if h.meta != nil {
		exists, info, err = h.existsWithMetadata(hash)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !exists {
		http.NotFound(w, r)
		return
	}
	setPayloadHeaders(w, info)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getPayload(w http.ResponseWriter, r *http.Request) {
	hash, ok := queryHash(w, r)
	if !ok {
		return
	}
	var (
		reader io.ReadCloser
		info   blobstore.Info
		err    error
	)
	if h.meta != nil {
		exists, metadataInfo, metadataErr := h.existsWithMetadata(hash)
		if metadataErr != nil {
			err = metadataErr
		} else if !exists {
			err = os.ErrNotExist
		} else if cached, ok := h.cache.Get(hash); ok {
			info = metadataInfo
			setPayloadHeaders(w, info)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return
		} else {
			info = metadataInfo
			reader, _, err = h.store.Open(hash)
		}
	} else {
		if cached, ok := h.cache.Get(hash); ok {
			info = blobstore.Info{Hash: hash, Size: int64(len(cached))}
			setPayloadHeaders(w, info)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return
		}
		reader, info, err = h.store.Open(hash)
	}
	if err != nil {
		if errors.Is(err, blobstore.ErrInvalidHash) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	setPayloadHeaders(w, info)
	w.WriteHeader(http.StatusOK)
	if h.cache.CanStore(info.Size) {
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

func (h *Handler) putPayload(w http.ResponseWriter, r *http.Request) {
	hash, ok := queryHash(w, r)
	if !ok {
		return
	}
	info, err := h.store.Put(hash, r.Body)
	if err != nil {
		if errors.Is(err, blobstore.ErrInvalidHash) || errors.Is(err, blobstore.ErrHashMismatch) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	status := http.StatusOK
	if info.Created {
		status = http.StatusCreated
	}
	if h.meta != nil {
		if err := h.meta.PutPayload(metadata.Payload{
			Hash:      info.Hash,
			Size:      info.Size,
			Status:    metadata.StatusLocal,
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, status, putResponse{Hash: info.Hash, Size: info.Size, Stored: true})
}

func (h *Handler) exists(w http.ResponseWriter, r *http.Request) {
	var request existsRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if len(request.Hashes) == 0 {
		http.Error(w, "hashes must not be empty", http.StatusBadRequest)
		return
	}
	for _, hash := range request.Hashes {
		if !blobstore.ValidHash(hash) {
			http.Error(w, "invalid hash", http.StatusBadRequest)
			return
		}
	}

	response := existsResponse{Items: make(map[string]existsItem, len(request.Hashes))}
	for _, hash := range request.Hashes {
		if _, seen := response.Items[hash]; seen {
			continue
		}
		exists, info, err := h.store.Exists(hash)
		if h.meta != nil {
			exists, info, err = h.existsWithMetadata(hash)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				writeStoreError(w, err)
				return
			}
		}
		if err != nil {
			writeStoreError(w, err)
			return
		}
		item := existsItem{Exists: exists}
		if exists {
			item.Size = info.Size
		}
		response.Items[hash] = item
	}
	writeJSON(w, http.StatusOK, response)
}

func queryHash(w http.ResponseWriter, r *http.Request) (string, bool) {
	hash := r.URL.Query().Get("iohash")
	if !blobstore.ValidHash(hash) {
		http.Error(w, "invalid iohash", http.StatusBadRequest)
		return "", false
	}
	return hash, true
}

func setPayloadHeaders(w http.ResponseWriter, info blobstore.Info) {
	w.Header().Set("ETag", `"`+info.Hash+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	w.Header().Set("Content-Type", "application/octet-stream")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, blobstore.ErrInvalidHash) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func (h *Handler) existsWithMetadata(hash string) (bool, blobstore.Info, error) {
	record, err := h.meta.GetPayload(hash)
	if errors.Is(err, metadata.ErrNotFound) {
		return false, blobstore.Info{}, nil
	}
	if err != nil {
		return false, blobstore.Info{}, err
	}

	exists, info, err := h.store.Exists(hash)
	if err != nil {
		return false, blobstore.Info{}, err
	}
	if !exists {
		return false, blobstore.Info{}, errLocalPayloadMissing
	}
	if info.Size != record.Size {
		return false, blobstore.Info{}, errors.New("metadata size does not match local payload")
	}
	return true, info, nil
}

func (h *Handler) markCached(hash string) {
	if h.meta == nil {
		return
	}
	record, err := h.meta.GetPayload(hash)
	if err != nil {
		return
	}
	record.Status = record.Status.WithCache(true)
	_ = h.meta.PutPayload(record)
}

func (h *Handler) collectDashboardStats() (dashboardStats, error) {
	stats := dashboardStats{}
	if h.meta != nil {
		metadataStats, err := h.meta.Stats()
		if err != nil {
			return dashboardStats{}, err
		}
		stats.Metadata = metadataStats
	}
	stats.Cache = h.cache.Stats()
	return stats, nil
}
