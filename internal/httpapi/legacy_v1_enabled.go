//go:build vaps_legacy_v1

package httpapi

import "net/http"

func (h *Handler) serveLegacyV1(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/v1/payload" && r.Method == http.MethodHead:
		h.headPayload(w, r)
		return true
	case r.URL.Path == "/v1/payload" && r.Method == http.MethodGet:
		h.getPayload(w, r)
		return true
	case r.URL.Path == "/v1/payload" && r.Method == http.MethodPut:
		h.putPayload(w, r)
		return true
	case r.URL.Path == "/v1/payload/exists" && r.Method == http.MethodPost:
		h.exists(w, r)
		return true
	case r.URL.Path == "/v1/backup/payloads" && r.Method == http.MethodGet:
		h.listBackupPayloads(w, r)
		return true
	case r.URL.Path == "/v1/backup/payload" && r.Method == http.MethodPut:
		h.putBackupPayload(w, r)
		return true
	default:
		return false
	}
}
