package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"vaps/internal/blobstore"
	"vaps/internal/metadata"
)

const maxVCSMetadataBodyBytes = 64 * 1024

type vcsMetadataRequest struct {
	PayloadHash string            `json:"payload_hash"`
	VCSType     string            `json:"vcs_type"`
	Repo        string            `json:"repo"`
	Revision    string            `json:"revision"`
	Path        string            `json:"path"`
	AssetID     string            `json:"asset_id"`
	Metadata    map[string]string `json:"metadata"`
}

func (h *Handler) metadataV2(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getVCSMetadata(w, r)
	case http.MethodPost:
		h.postVCSMetadata(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) metadataExistsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAuth(w, r, false); !ok {
		return
	}
	h.exists(w, r)
}

func (h *Handler) postVCSMetadata(w http.ResponseWriter, r *http.Request) {
	if h.meta == nil {
		http.Error(w, "metadata is not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxVCSMetadataBodyBytes))
	if err != nil {
		http.Error(w, "metadata body too large", http.StatusRequestEntityTooLarge)
		return
	}
	var request vcsMetadataRequest
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	request.PayloadHash = strings.ToLower(strings.TrimSpace(request.PayloadHash))
	request.VCSType = strings.TrimSpace(request.VCSType)
	request.Repo = strings.TrimSpace(request.Repo)
	request.Revision = strings.TrimSpace(request.Revision)
	request.Path = strings.TrimSpace(request.Path)
	if !blobstore.ValidHash(request.PayloadHash) {
		http.Error(w, "invalid payload_hash", http.StatusBadRequest)
		return
	}
	if request.VCSType == "" || request.Repo == "" || request.Revision == "" || request.Path == "" {
		http.Error(w, "vcs_type, repo, revision, and path are required", http.StatusBadRequest)
		return
	}
	record := metadata.VCSRecord{
		PayloadHash: request.PayloadHash,
		VCSType:     request.VCSType,
		Repo:        request.Repo,
		Revision:    request.Revision,
		Path:        request.Path,
		AssetID:     strings.TrimSpace(request.AssetID),
		Metadata:    request.Metadata,
		ReportedAt:  time.Now().UTC(),
		RemoteIP:    clientIP(r),
	}
	if err := h.meta.PutVCSRecord(record); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, record)
}

func (h *Handler) getVCSMetadata(w http.ResponseWriter, r *http.Request) {
	if h.meta == nil {
		writeJSON(w, http.StatusOK, []metadata.VCSRecord{})
		return
	}
	payloadHash := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("payload_hash")))
	if payloadHash == "" {
		http.Error(w, "payload_hash is required", http.StatusBadRequest)
		return
	}
	if !blobstore.ValidHash(payloadHash) {
		http.Error(w, "invalid payload_hash", http.StatusBadRequest)
		return
	}
	records, err := h.meta.QueryVCSByPayloadHash(payloadHash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, records)
}
