package httpapi

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"vaps/internal/auth"
	"vaps/internal/reqlog"
)

type errorRecorderResponseWriter struct {
	http.ResponseWriter
	status      int
	body        bytes.Buffer
	wroteHeader bool
}

func (w *errorRecorderResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *errorRecorderResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.status >= http.StatusBadRequest {
		w.body.Write(data)
	}
	n, err := w.ResponseWriter.Write(data)
	return n, err
}

func errorRecorder(h *Handler, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &errorRecorderResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		if recorder.status < http.StatusBadRequest {
			return
		}
		path := requestPath(r)
		if !isMonitoredErrorPath(path) {
			return
		}
		h.recordRequestError(r, recorder.status, recorder.body.String())
	})
}

func (h *Handler) recordRequestError(r *http.Request, status int, message string) {
	if h.reqlog == nil {
		return
	}
	token := bearerToken(r)
	tokenHash := ""
	clientID := ""
	if token != "" {
		tokenHash = auth.HashToken(token)
		if h.auth != nil {
			if record, err := h.auth.LookupByHash(tokenHash); err == nil {
				clientID = record.ClientID
			}
		}
	}
	h.reqlog.Record(reqlog.Record{
		At:        time.Now().UTC(),
		Method:    r.Method,
		Path:      requestPath(r),
		Status:    status,
		Message:   strings.TrimSpace(message),
		RemoteIP:  clientIP(r),
		UserAgent: r.UserAgent(),
		TokenHash: tokenHash,
		ClientID:  clientID,
	})
}

func requestPath(r *http.Request) string {
	if r.URL == nil {
		return ""
	}
	return r.URL.Path
}

func (h *Handler) HTTPHandler() http.Handler {
	return AccessLog(httpMeter(h, errorRecorder(h, h)))
}
