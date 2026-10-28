package httpapi

import (
	"context"
	"net/http"

	"vaps/internal/auth"
)

type sourceContextKey struct{}

type requestSource struct {
	TokenHash string
	ClientID  string
}

func sourceFromRequest(r *http.Request) requestSource {
	source, _ := r.Context().Value(sourceContextKey{}).(requestSource)
	return source
}

// requestSources attaches optional provenance without restricting access.
// Dashboard polling is excluded from per-source activity counts.
func requestSources(h *Handler, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var source requestSource
		if token := bearerToken(r); token != "" {
			source.TokenHash = auth.HashToken(token)
			if h.auth != nil {
				var record auth.Token
				var err error
				if isDashboardErrorPath(requestPath(r)) {
					record, err = h.auth.LookupByHash(source.TokenHash)
				} else {
					record, err = h.auth.Observe(source.TokenHash, clientIP(r), r.UserAgent())
				}
				if err == nil {
					source.ClientID = record.ClientID
				}
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), sourceContextKey{}, source))
		next.ServeHTTP(w, r)
	})
}
