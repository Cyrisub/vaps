//go:build !vaps_legacy_v1

package httpapi

import "net/http"

func (h *Handler) serveLegacyV1(w http.ResponseWriter, r *http.Request) bool {
	return false
}
