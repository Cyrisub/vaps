package httpapi

import (
	_ "embed"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vaps/internal/reqlog"
)

//go:embed errors.html
var errorsDashboardHTML string

type errorQueryItem struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	AtHuman    string    `json:"at_human"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	Message    string    `json:"message"`
	RemoteIP   string    `json:"remote_ip"`
	UserAgent  string    `json:"user_agent"`
	TokenHash  string    `json:"token_hash,omitempty"`
	HashPrefix string    `json:"hash_prefix,omitempty"`
	ClientID   string    `json:"client_id,omitempty"`
}

type errorQueryResponse struct {
	Items  []errorQueryItem `json:"items"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

func (h *Handler) errorsDashboard(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, errorsDashboardHTML)
}

func (h *Handler) errorsQuery(w http.ResponseWriter, r *http.Request) {
	query, err := parseErrorQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.reqlog == nil {
		writeJSON(w, http.StatusOK, errorQueryResponse{Items: []errorQueryItem{}, Limit: query.Limit, Offset: query.Offset})
		return
	}
	result := h.reqlog.Query(query)
	items := make([]errorQueryItem, 0, len(result.Items))
	for _, record := range result.Items {
		items = append(items, newErrorQueryItem(record))
	}
	writeJSON(w, http.StatusOK, errorQueryResponse{
		Items:  items,
		Total:  result.Total,
		Limit:  result.Limit,
		Offset: result.Offset,
	})
}

func parseErrorQuery(r *http.Request) (reqlog.Query, error) {
	values := r.URL.Query()
	query := reqlog.Query{Limit: 100, ExcludeDashboard: true}
	query.TokenHash = strings.TrimSpace(values.Get("token_hash"))
	query.Path = strings.TrimSpace(values.Get("path"))
	if includeDashboard := strings.TrimSpace(strings.ToLower(values.Get("include_dashboard"))); includeDashboard == "1" || includeDashboard == "true" || includeDashboard == "yes" {
		query.ExcludeDashboard = false
	}
	if value := strings.TrimSpace(values.Get("min_status")); value != "" {
		status, err := strconv.Atoi(value)
		if err != nil || status < 400 {
			return reqlog.Query{}, errInvalidStatusFilter("min_status")
		}
		query.MinStatus = status
	}
	if value := strings.TrimSpace(values.Get("max_status")); value != "" {
		status, err := strconv.Atoi(value)
		if err != nil || status < 400 {
			return reqlog.Query{}, errInvalidStatusFilter("max_status")
		}
		query.MaxStatus = status
	}
	if value := strings.TrimSpace(values.Get("limit")); value != "" {
		limit, err := parseNonNegativeInt(value, "limit")
		if err != nil {
			return reqlog.Query{}, err
		}
		if limit == 0 {
			limit = 100
		}
		query.Limit = limit
	}
	if value := strings.TrimSpace(values.Get("offset")); value != "" {
		offset, err := parseNonNegativeInt(value, "offset")
		if err != nil {
			return reqlog.Query{}, err
		}
		query.Offset = offset
	}
	return query, nil
}

func errInvalidStatusFilter(name string) error {
	return &queryError{name + " must be an integer >= 400"}
}

type queryError struct {
	message string
}

func (e *queryError) Error() string {
	return e.message
}

func newErrorQueryItem(record reqlog.Record) errorQueryItem {
	item := errorQueryItem{
		ID:        record.ID,
		At:        record.At,
		AtHuman:   formatDashboardTime(record.At),
		Method:    record.Method,
		Path:      record.Path,
		Status:    record.Status,
		Message:   record.Message,
		RemoteIP:  record.RemoteIP,
		UserAgent: record.UserAgent,
		TokenHash: record.TokenHash,
		ClientID:  record.ClientID,
	}
	if record.TokenHash != "" {
		item.HashPrefix = tokenHashPrefix(record.TokenHash)
	}
	return item
}
