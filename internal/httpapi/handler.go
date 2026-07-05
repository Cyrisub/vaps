package httpapi

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"vaps/internal/auth"
	"vaps/internal/backup"
	"vaps/internal/blobstore"
	"vaps/internal/cache"
	"vaps/internal/metadata"
	"vaps/internal/uploadsession"
)

type Handler struct {
	store   *blobstore.Store
	meta    *metadata.Store
	cache   *cache.Cache
	backup  backup.Backend
	auth    *auth.Store
	uploads *uploadsession.Store
	opts    Options
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

type backupListResponse struct {
	Items []backup.Object `json:"items"`
	Total int             `json:"total"`
}

type dashboardStats struct {
	Metadata metadata.Stats `json:"metadata"`
	Cache    cache.Stats    `json:"cache"`
}

type metadataQueryResponse struct {
	Items  []metadataQueryItem `json:"items"`
	Total  int64               `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type metadataQueryItem struct {
	Hash            string                 `json:"hash"`
	Size            int64                  `json:"size"`
	SizeHuman       string                 `json:"size_human"`
	Status          metadata.PayloadStatus `json:"status"`
	StatusFlags     metadataStatusFlags    `json:"status_flags"`
	Backup          string                 `json:"backup"`
	VCSCount        int                    `json:"vcs_count"`
	CreatedAt       *time.Time             `json:"created_at"`
	CreatedAtHuman  string                 `json:"created_at_human"`
	BackupedAt      *time.Time             `json:"backuped_at"`
	BackupedAtHuman string                 `json:"backuped_at_human"`
}

type metadataStatusFlags struct {
	Local  bool `json:"local"`
	Cache  bool `json:"cache"`
	Backup bool `json:"backup"`
}

var errLocalPayloadMissing = errors.New("metadata record exists but local payload is missing")

//go:embed dashboard.html
var dashboardHTML string

//go:embed metadata.html
var metadataDashboardHTML string

func New(store *blobstore.Store) http.Handler {
	return &Handler{store: store, opts: defaultOptions()}
}

func NewWithMetadata(store *blobstore.Store, meta *metadata.Store) http.Handler {
	return &Handler{store: store, meta: meta, opts: defaultOptions()}
}

func NewWithCache(store *blobstore.Store, lru *cache.Cache) http.Handler {
	return &Handler{store: store, cache: lru, opts: defaultOptions()}
}

func NewWithMetadataAndCache(store *blobstore.Store, meta *metadata.Store, lru *cache.Cache) http.Handler {
	return &Handler{store: store, meta: meta, cache: lru, opts: defaultOptions()}
}

func NewWithMetadataCacheAndBackup(store *blobstore.Store, meta *metadata.Store, lru *cache.Cache, backupBackend backup.Backend) http.Handler {
	return &Handler{store: store, meta: meta, cache: lru, backup: backupBackend, opts: defaultOptions()}
}

func NewV2(store *blobstore.Store, meta *metadata.Store, lru *cache.Cache, backupBackend backup.Backend, authStore *auth.Store, uploads *uploadsession.Store, opts Options) http.Handler {
	if opts.AuthExpireTime <= 0 {
		opts.AuthExpireTime = defaultOptions().AuthExpireTime
	}
	if opts.UploadDirectMaxBytes <= 0 {
		opts.UploadDirectMaxBytes = defaultOptions().UploadDirectMaxBytes
	}
	if opts.UploadExpiration <= 0 {
		opts.UploadExpiration = defaultOptions().UploadExpiration
	}
	if opts.UploadCleanupInterval <= 0 {
		opts.UploadCleanupInterval = defaultOptions().UploadCleanupInterval
	}
	return &Handler{
		store:   store,
		meta:    meta,
		cache:   lru,
		backup:  backupBackend,
		auth:    authStore,
		uploads: uploads,
		opts:    opts,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		h.health(w)
	case r.Method == http.MethodGet && r.URL.Path == "/dashboard":
		h.dashboard(w)
	case r.Method == http.MethodGet && r.URL.Path == "/dashboard/stats":
		h.dashboardStats(w)
	case r.Method == http.MethodGet && r.URL.Path == "/dashboard/metadata":
		h.metadataDashboard(w)
	case r.Method == http.MethodGet && r.URL.Path == "/dashboard/metadata/query":
		h.metadataQuery(w, r)
	case r.URL.Path == "/v2/auth/request" && r.Method == http.MethodPost:
		h.authRequest(w, r)
	case r.URL.Path == "/v2/auth/expire" && r.Method == http.MethodGet:
		h.authExpire(w, r)
	case r.URL.Path == "/v2/payload/pull":
		h.pullPayload(w, r)
	case r.URL.Path == "/v2/payload/push":
		h.pushPayload(w, r)
	case r.URL.Path == "/v2/metadata/exists" && r.Method == http.MethodPost:
		h.metadataExistsV2(w, r)
	case r.URL.Path == "/v2/metadata" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.metadataV2(w, r)
	default:
		if h.serveLegacyV1(w, r) {
			return
		}
		http.NotFound(w, r)
	}
}

func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &accessLogResponseWriter{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(recorder, r)
		log.Printf(
			"access method=%s path=%q status=%d bytes=%d duration=%s remote_addr=%q user_agent=%q",
			r.Method,
			r.URL.RequestURI(),
			recorder.status,
			recorder.bytes,
			time.Since(started).Truncate(time.Microsecond),
			r.RemoteAddr,
			r.UserAgent(),
		)
	})
}

type accessLogResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *accessLogResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *accessLogResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func (h *Handler) health(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (h *Handler) dashboard(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, dashboardHTML)
}

func (h *Handler) metadataDashboard(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, metadataDashboardHTML)
}

func (h *Handler) dashboardStats(w http.ResponseWriter) {
	stats, err := h.collectDashboardStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) metadataQuery(w http.ResponseWriter, r *http.Request) {
	query, err := parseMetadataQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.meta == nil {
		writeJSON(w, http.StatusOK, metadataQueryResponse{Items: []metadataQueryItem{}, Limit: query.Limit, Offset: query.Offset})
		return
	}
	result, err := h.meta.QueryPayloads(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]metadataQueryItem, 0, len(result.Items))
	for _, payload := range result.Items {
		items = append(items, h.enrichMetadataQueryItem(newMetadataQueryItem(payload)))
	}
	writeJSON(w, http.StatusOK, metadataQueryResponse{
		Items:  items,
		Total:  result.Total,
		Limit:  result.Limit,
		Offset: result.Offset,
	})
}

func parseMetadataQuery(r *http.Request) (metadata.PayloadQuery, error) {
	values := r.URL.Query()
	query := metadata.PayloadQuery{
		HashContains: strings.ToLower(strings.TrimSpace(values.Get("q"))),
		Limit:        100,
	}
	if values.Has("limit") {
		limit, err := parseNonNegativeInt(values.Get("limit"), "limit")
		if err != nil {
			return metadata.PayloadQuery{}, err
		}
		query.Limit = limit
	}
	if query.Limit > 500 {
		query.Limit = 500
	}
	if values.Has("offset") {
		offset, err := parseNonNegativeInt(values.Get("offset"), "offset")
		if err != nil {
			return metadata.PayloadQuery{}, err
		}
		query.Offset = offset
	}
	if values.Has("min_size") {
		minSize, err := parseNonNegativeInt64(values.Get("min_size"), "min_size")
		if err != nil {
			return metadata.PayloadQuery{}, err
		}
		query.MinSize = &minSize
	}
	if values.Has("max_size") {
		maxSize, err := parseNonNegativeInt64(values.Get("max_size"), "max_size")
		if err != nil {
			return metadata.PayloadQuery{}, err
		}
		query.MaxSize = &maxSize
	}
	if query.MinSize != nil && query.MaxSize != nil && *query.MaxSize < *query.MinSize {
		return metadata.PayloadQuery{}, errors.New("max_size must be greater than or equal to min_size")
	}
	if err := applyStatusFilters(&query, values["status"]); err != nil {
		return metadata.PayloadQuery{}, err
	}
	backupValue := strings.TrimSpace(strings.ToLower(values.Get("backup")))
	if backupValue != "" && backupValue != "all" {
		backup, err := parseBackupFilter(values.Get("backup"))
		if err != nil {
			return metadata.PayloadQuery{}, err
		}
		query.Backup = &backup
	}
	return query, nil
}

func applyStatusFilters(query *metadata.PayloadQuery, values []string) error {
	for _, value := range splitQueryValues(values) {
		switch value {
		case "", "all":
		case "local":
			query.RequireLocal = true
		case "cache":
			query.RequireCache = true
		case "backup":
			backup := true
			query.Backup = &backup
		default:
			return errors.New("status must be one of local, cache, backup")
		}
	}
	return nil
}

func parseBackupFilter(value string) (bool, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "backuped", "backed", "backed_up":
		return true, nil
	case "none":
		return false, nil
	default:
		return false, errors.New("backup must be one of none, backuped")
	}
}

func splitQueryValues(values []string) []string {
	parts := []string{}
	for _, value := range values {
		for part := range strings.SplitSeq(value, ",") {
			parts = append(parts, strings.TrimSpace(strings.ToLower(part)))
		}
	}
	return parts
}

func parseNonNegativeInt(value, name string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return 0, errors.New(name + " must be a non-negative integer")
	}
	return parsed, nil
}

func parseNonNegativeInt64(value, name string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New(name + " must be a non-negative integer")
	}
	return parsed, nil
}

func newMetadataQueryItem(payload metadata.Payload) metadataQueryItem {
	backup := payload.BackupStatus
	if backup == metadata.BackupNone && payload.Status.HasBackup() {
		backup = metadata.Backuped
	}
	item := metadataQueryItem{
		Hash:      payload.Hash,
		Size:      payload.Size,
		SizeHuman: formatHumanBytes(payload.Size),
		Status:    payload.Status,
		StatusFlags: metadataStatusFlags{
			Local:  payload.Status.HasLocal(),
			Cache:  payload.Status.HasCache(),
			Backup: payload.Status.HasBackup(),
		},
		Backup:          backup.String(),
		CreatedAt:       payload.CreatedAt,
		CreatedAtHuman:  formatHumanTime(payload.CreatedAt),
		BackupedAt:      payload.BackupedAt,
		BackupedAtHuman: formatHumanTime(payload.BackupedAt),
	}
	return item
}

func (h *Handler) enrichMetadataQueryItem(item metadataQueryItem) metadataQueryItem {
	if h.meta == nil {
		return item
	}
	count, err := h.meta.CountVCSByPayloadHash(item.Hash)
	if err != nil {
		return item
	}
	item.VCSCount = count
	return item
}

func formatHumanBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return strconv.FormatInt(value, 10) + " B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	scaled := float64(value) / unit
	unitIndex := 0
	for scaled >= unit && unitIndex < len(units)-1 {
		scaled /= unit
		unitIndex++
	}
	return strconv.FormatFloat(scaled, 'f', 1, 64) + " " + units[unitIndex]
}

func formatHumanTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.Local().Format("Jan 2, 2006 15:04:05 MST")
}

func (h *Handler) headPayload(w http.ResponseWriter, r *http.Request) {
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
		reader, info, err = h.openPayloadWithMetadata(r.Context(), hash)
	} else {
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
	_ = reader.Close()
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
		if _, metadataErr := h.meta.GetPayload(hash); errors.Is(metadataErr, metadata.ErrNotFound) {
			err = os.ErrNotExist
		} else if metadataErr != nil {
			err = metadataErr
		} else if cached, ok := h.cache.Get(hash); ok {
			info = blobstore.Info{Hash: hash, Size: int64(len(cached))}
			setPayloadHeaders(w, info)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return
		} else {
			reader, info, err = h.openPayloadWithMetadata(r.Context(), hash)
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
		now := time.Now().UTC()
		record, err := h.meta.GetPayload(info.Hash)
		if errors.Is(err, metadata.ErrNotFound) {
			record = metadata.Payload{
				Hash:      info.Hash,
				Size:      info.Size,
				Status:    metadata.StatusLocal,
				CreatedAt: &now,
			}
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else {
			record.Size = info.Size
			record.Status |= metadata.StatusLocal
			if record.CreatedAt == nil {
				record.CreatedAt = &now
			}
		}
		if err := h.meta.PutPayload(record); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if h.backup != nil {
		if err := h.queueBackup(r.Context(), info.Hash); err != nil {
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

func (h *Handler) listBackupPayloads(w http.ResponseWriter, r *http.Request) {
	if h.backup == nil {
		http.Error(w, "backup backend is not configured", http.StatusServiceUnavailable)
		return
	}
	items, err := h.backup.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, backupListResponse{Items: items, Total: len(items)})
}

func (h *Handler) putBackupPayload(w http.ResponseWriter, r *http.Request) {
	if h.backup == nil {
		http.Error(w, "backup backend is not configured", http.StatusServiceUnavailable)
		return
	}
	hash, ok := queryHash(w, r)
	if !ok {
		return
	}
	exists, _, err := h.store.Exists(hash)
	if h.meta != nil {
		exists, _, err = h.existsWithMetadata(hash)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !exists {
		http.NotFound(w, r)
		return
	}
	object, err := h.backupLocalPayload(r.Context(), hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, object)
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
		if record.Status.HasBackup() {
			return true, blobstore.Info{Hash: hash, Size: record.Size}, nil
		}
		return false, blobstore.Info{}, errLocalPayloadMissing
	}
	if record.Status.HasLocal() && info.Size != record.Size {
		return false, blobstore.Info{}, errors.New("metadata size does not match local payload")
	}
	return true, info, nil
}

func (h *Handler) openPayloadWithMetadata(ctx context.Context, hash string) (io.ReadCloser, blobstore.Info, error) {
	record, err := h.meta.GetPayload(hash)
	if errors.Is(err, metadata.ErrNotFound) {
		return nil, blobstore.Info{}, os.ErrNotExist
	}
	if err != nil {
		return nil, blobstore.Info{}, err
	}
	exists, info, err := h.store.Exists(hash)
	if err != nil {
		return nil, blobstore.Info{}, err
	}
	if exists {
		if record.Status.HasLocal() && info.Size != record.Size {
			return nil, blobstore.Info{}, errors.New("metadata size does not match local payload")
		}
		reader, info, err := h.store.Open(hash)
		return reader, info, err
	}
	if !record.Status.HasBackup() {
		return nil, blobstore.Info{}, errLocalPayloadMissing
	}
	return h.restorePayloadFromBackup(ctx, hash)
}

func (h *Handler) restorePayloadFromBackup(ctx context.Context, hash string) (io.ReadCloser, blobstore.Info, error) {
	if h.backup == nil {
		return nil, blobstore.Info{}, os.ErrNotExist
	}
	backupReader, err := h.backup.Open(ctx, hash)
	if err != nil {
		return nil, blobstore.Info{}, err
	}
	defer backupReader.Close()
	info, err := h.store.Put(hash, backupReader)
	if err != nil {
		return nil, blobstore.Info{}, err
	}
	h.markRestoredLocal(info)
	return h.store.Open(hash)
}

func (h *Handler) queueBackup(ctx context.Context, hash string) error {
	if queue, ok := h.backup.(backup.EnqueueBackend); ok {
		h.markBackupPending(hash)
		return queue.Enqueue(ctx, hash)
	}
	_, err := h.backupLocalPayload(ctx, hash)
	return err
}

func (h *Handler) backupLocalPayload(ctx context.Context, hash string) (backup.Object, error) {
	reader, _, err := h.store.Open(hash)
	if err != nil {
		return backup.Object{}, err
	}
	defer reader.Close()
	object, err := h.backup.Put(ctx, hash, reader)
	if err != nil {
		return backup.Object{}, err
	}
	h.markBackuped(hash)
	return object, nil
}

func (h *Handler) markBackupPending(hash string) {
	if h.meta == nil {
		return
	}
	record, err := h.meta.GetPayload(hash)
	if err != nil {
		return
	}
	record.BackupStatus = metadata.BackupPending
	_ = h.meta.PutPayload(record)
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

func (h *Handler) markRestoredLocal(info blobstore.Info) {
	if h.meta == nil {
		return
	}
	record, err := h.meta.GetPayload(info.Hash)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	record.Size = info.Size
	record.Status |= metadata.StatusLocal
	if record.CreatedAt == nil {
		record.CreatedAt = &now
	}
	_ = h.meta.PutPayload(record)
}

func (h *Handler) markBackuped(hash string) {
	if h.meta == nil {
		return
	}
	record, err := h.meta.GetPayload(hash)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	record.Status = record.Status.WithBackup(true)
	record.BackupStatus = metadata.Backuped
	record.BackupedAt = &now
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
