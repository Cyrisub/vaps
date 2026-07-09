package httpapi

import (
	_ "embed"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"vaps/internal/auth"
)

//go:embed auth.html
var authDashboardHTML string

type authQueryItem struct {
	HashPrefix      string            `json:"hash_prefix"`
	TokenHash       string            `json:"token_hash"`
	ClientID        string            `json:"client_id"`
	RemoteIP        string            `json:"remote_ip"`
	UserAgent       string            `json:"user_agent"`
	UserAgentInfo   ParsedUserAgent   `json:"user_agent_info"`
	ClientInfo      map[string]string `json:"client_info"`
	Status          string            `json:"status"`
	CreatedAt       time.Time         `json:"created_at"`
	CreatedAtHuman  string            `json:"created_at_human"`
	ExpiresAt       time.Time         `json:"expires_at"`
	ExpiresAtHuman  string            `json:"expires_at_human"`
	ParkedAt        *time.Time        `json:"parked_at,omitempty"`
	ParkedAtHuman   string            `json:"parked_at_human,omitempty"`
	RevokedAt       *time.Time        `json:"revoked_at,omitempty"`
	RevokedAtHuman  string            `json:"revoked_at_human,omitempty"`
	ErrorCount      int64             `json:"error_count"`
}

type authQueryResponse struct {
	Items  []authQueryItem `json:"items"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

func (h *Handler) authDashboard(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, authDashboardHTML)
}

type authClearResponse struct {
	Deleted int `json:"deleted"`
}

func (h *Handler) authClearExpired(w http.ResponseWriter) {
	if h.auth == nil {
		http.Error(w, "auth is not configured", http.StatusServiceUnavailable)
		return
	}
	deleted, err := h.auth.DeleteExpired()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, authClearResponse{Deleted: deleted})
}

func (h *Handler) authClearAll(w http.ResponseWriter) {
	if h.auth == nil {
		http.Error(w, "auth is not configured", http.StatusServiceUnavailable)
		return
	}
	deleted, err := h.auth.DeleteAll()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, authClearResponse{Deleted: deleted})
}

func (h *Handler) authQuery(w http.ResponseWriter, r *http.Request) {
	query, err := parseAuthQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.auth == nil {
		writeJSON(w, http.StatusOK, authQueryResponse{Items: []authQueryItem{}, Limit: query.Limit, Offset: query.Offset})
		return
	}
	tokens, err := h.auth.ListAll()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	errorCounts := map[string]int64{}
	if h.reqlog != nil {
		errorCounts = h.reqlog.CountByTokenHash()
	}
	now := time.Now().UTC()
	items := make([]authQueryItem, 0, len(tokens))
	for _, token := range tokens {
		status := auth.TokenStatus(token, now)
		if query.Status != "" && status != query.Status {
			continue
		}
		if query.ClientID != "" && !strings.Contains(strings.ToLower(token.ClientID), strings.ToLower(query.ClientID)) {
			continue
		}
		if query.HashPrefix != "" && !strings.HasPrefix(strings.ToLower(token.Hash), strings.ToLower(query.HashPrefix)) {
			continue
		}
		items = append(items, newAuthQueryItem(token, status, errorCounts[token.Hash]))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	total := len(items)
	if query.Offset >= total {
		writeJSON(w, http.StatusOK, authQueryResponse{Items: []authQueryItem{}, Total: total, Limit: query.Limit, Offset: query.Offset})
		return
	}
	end := query.Offset + query.Limit
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, authQueryResponse{
		Items:  items[query.Offset:end],
		Total:  total,
		Limit:  query.Limit,
		Offset: query.Offset,
	})
}

type authQuery struct {
	Status     string
	ClientID   string
	HashPrefix string
	Limit      int
	Offset     int
}

func parseAuthQuery(r *http.Request) (authQuery, error) {
	query := authQuery{Limit: 100}
	if value := strings.TrimSpace(r.URL.Query().Get("status")); value != "" {
		switch value {
		case "active", "parked", "expired", "revoked":
			query.Status = value
		default:
			return authQuery{}, errors.New("status must be active, parked, expired, or revoked")
		}
	}
	query.ClientID = strings.TrimSpace(r.URL.Query().Get("client_id"))
	query.HashPrefix = strings.TrimSpace(r.URL.Query().Get("hash"))
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		limit, err := parseNonNegativeInt(value, "limit")
		if err != nil {
			return authQuery{}, err
		}
		if limit == 0 {
			limit = 100
		}
		if limit > 500 {
			limit = 500
		}
		query.Limit = limit
	}
	if value := strings.TrimSpace(r.URL.Query().Get("offset")); value != "" {
		offset, err := parseNonNegativeInt(value, "offset")
		if err != nil {
			return authQuery{}, err
		}
		query.Offset = offset
	}
	return query, nil
}

func newAuthQueryItem(token auth.Token, status string, errorCount int64) authQueryItem {
	item := authQueryItem{
		HashPrefix:     tokenHashPrefix(token.Hash),
		TokenHash:      token.Hash,
		ClientID:       token.ClientID,
		RemoteIP:       NormalizeClientIP(token.RemoteIP),
		UserAgent:      token.UserAgent,
		UserAgentInfo:  ParseUserAgent(token.UserAgent),
		ClientInfo:     map[string]string(token.ClientInfo),
		Status:         status,
		CreatedAt:      token.CreatedAt,
		CreatedAtHuman: formatDashboardTime(token.CreatedAt),
		ExpiresAt:      token.ExpiresAt,
		ExpiresAtHuman: formatDashboardTime(token.ExpiresAt),
		ErrorCount:     errorCount,
	}
	if token.ParkedAt != nil {
		item.ParkedAt = token.ParkedAt
		item.ParkedAtHuman = formatDashboardTime(*token.ParkedAt)
	}
	if token.RevokedAt != nil {
		item.RevokedAt = token.RevokedAt
		item.RevokedAtHuman = formatDashboardTime(*token.RevokedAt)
	}
	if item.ClientInfo == nil {
		item.ClientInfo = map[string]string{}
	}
	return item
}

func tokenHashPrefix(hash string) string {
	prefix := strings.ToUpper(strings.TrimSpace(hash))
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return prefix
}

func formatDashboardTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
