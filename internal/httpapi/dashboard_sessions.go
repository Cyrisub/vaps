package httpapi

import (
	_ "embed"
	"errors"
	"io"
	"net/http"
	"strings"

	"vaps/internal/logreader"
)

//go:embed sessions.html
var sessionsDashboardHTML string

func (h *Handler) sessionsDashboard(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, sessionsDashboardHTML)
}

func (h *Handler) sessionsQuery(w http.ResponseWriter, r *http.Request) {
	query, err := parseSessionQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := logreader.New(h.opts.LogDir).Sessions(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) sessionLogQuery(w http.ResponseWriter, r *http.Request) {
	query, err := parseSessionQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if query.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	result, err := logreader.New(h.opts.LogDir).Log(query)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func parseSessionQuery(r *http.Request) (logreader.Query, error) {
	values := r.URL.Query()
	query := logreader.Query{
		Status:    strings.TrimSpace(strings.ToLower(values.Get("status"))),
		SessionID: strings.TrimSpace(values.Get("session_id")),
		Search:    strings.TrimSpace(values.Get("q")),
		Level:     strings.TrimSpace(strings.ToUpper(values.Get("level"))),
		Newest:    true,
		Limit:     100,
	}
	if query.Status == "" {
		query.Status = "all"
	}
	if query.Status != "all" && query.Status != "active" && query.Status != "ended" {
		return logreader.Query{}, errors.New("status must be one of active, ended, all")
	}
	if query.Level != "" && query.Level != "ALL" && query.Level != "INFO" &&
		query.Level != "WARN" && query.Level != "ERROR" {
		return logreader.Query{}, errors.New("level must be one of info, warn, error, all")
	}
	if values.Has("limit") {
		limit, err := parseNonNegativeInt(values.Get("limit"), "limit")
		if err != nil {
			return logreader.Query{}, err
		}
		query.Limit = limit
	}
	if values.Has("offset") {
		offset, err := parseNonNegativeInt(values.Get("offset"), "offset")
		if err != nil {
			return logreader.Query{}, err
		}
		query.Offset = offset
	}
	if order := strings.ToLower(strings.TrimSpace(values.Get("order"))); order != "" {
		switch order {
		case "asc", "oldest":
			query.Newest = false
		case "desc", "newest":
			query.Newest = true
		default:
			return logreader.Query{}, errors.New("order must be one of newest, oldest")
		}
	}
	return query, nil
}
