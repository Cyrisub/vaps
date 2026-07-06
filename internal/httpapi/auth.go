package httpapi

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"vaps/internal/auth"
)

type authRequestBody struct {
	ClientID   string            `json:"client_id"`
	ClientInfo map[string]string `json:"client_info"`
}

type authExpireResponse struct {
	Expired bool `json:"expired"`
}

func (h *Handler) authRequest(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		http.Error(w, "auth is not configured", http.StatusServiceUnavailable)
		return
	}
	var body authRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	clientID := strings.TrimSpace(body.ClientID)
	if clientID == "" {
		http.Error(w, "client_id is required", http.StatusBadRequest)
		return
	}
	result, err := h.auth.Issue(clientID, clientIP(r), r.UserAgent(), auth.ClientInfo(body.ClientInfo))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) authExpire(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		http.Error(w, "auth is not configured", http.StatusServiceUnavailable)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	if err := h.auth.Revoke(token); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, authExpireResponse{Expired: true})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return NormalizeClientIP(r.RemoteAddr)
	}
	return NormalizeClientIP(host)
}

func formatHTTPTime(unix int64) string {
	return time.Unix(unix, 0).UTC().Format(http.TimeFormat)
}

func formatExpires(sessionExpires time.Time) int64 {
	return sessionExpires.UTC().Unix()
}

var errUnauthorized = errors.New("unauthorized")
