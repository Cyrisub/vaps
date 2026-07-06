package httpapi

import (
	"net/http"
	"time"

	"vaps/internal/httpmeter"
)

type meterResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *meterResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *meterResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func httpMeter(h *Handler, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h == nil || h.meter == nil || isDashboardErrorPath(requestPath(r)) {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		done := h.meter.Begin()
		recorder := &meterResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		var bytesIn int64
		if r.ContentLength > 0 {
			bytesIn = r.ContentLength
		}
		done(httpmeter.Sample{
			Status:   recorder.status,
			Duration: time.Since(started),
			BytesIn:  bytesIn,
			BytesOut: recorder.bytes,
		})
	})
}
