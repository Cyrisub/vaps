package httpapi

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type authContextKey struct{}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.auth == nil {
		http.Error(w, "auth is not configured", http.StatusServiceUnavailable)
		return "", false
	}
	token := bearerToken(r)
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return "", false
	}
	if _, err := h.auth.Validate(token); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	if _, err := h.auth.Refresh(token); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	return token, true
}

func withAuthToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, authContextKey{}, token)
}

func parseUploadChecksum(header string) (string, []byte, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", nil, nil
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return "", nil, errors.New("invalid Upload-Checksum header")
	}
	algorithm := strings.ToLower(strings.TrimSpace(parts[0]))
	switch algorithm {
	case "sha1", "sha256":
	default:
		return "", nil, fmt.Errorf("unsupported checksum algorithm %q", algorithm)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return "", nil, errors.New("invalid Upload-Checksum encoding")
	}
	return algorithm, raw, nil
}

func checksumMatches(algorithm string, expected []byte, data []byte) bool {
	switch algorithm {
	case "sha1":
		sum := sha1.Sum(data)
		return bytes.Equal(sum[:], expected)
	case "sha256":
		sum := sha256.Sum256(data)
		return bytes.Equal(sum[:], expected)
	default:
		return false
	}
}

func parseUploadLength(header string) (int64, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, errors.New("Upload-Length is required")
	}
	value, err := strconv.ParseInt(header, 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("Upload-Length must be a non-negative integer")
	}
	return value, nil
}

func isTusRequest(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Tus-Resumable")) != "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("Upload-Length")) != "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("Upload-Concat")) != "" {
		return true
	}
	if strings.TrimSpace(r.URL.Query().Get("upload_id")) != "" {
		return true
	}
	switch r.Method {
	case http.MethodPatch, http.MethodDelete:
		return r.URL.Path == "/v2/payload/push"
	case http.MethodHead:
		return r.URL.Path == "/v2/payload/push" && r.URL.Query().Get("upload_id") != ""
	case http.MethodOptions:
		return r.URL.Path == "/v2/payload/push"
	default:
		return false
	}
}

func tusUploadLocation(hash, checksum, uploadID string) string {
	return "/v2/payload/push?iohash=" + hash + "&checksum=" + checksum + "&upload_id=" + uploadID
}

func setTusHeaders(w http.ResponseWriter, expiration int64) {
	w.Header().Set("Tus-Resumable", "1.0.0")
	if expiration > 0 {
		w.Header().Set("Upload-Expires", formatHTTPTime(expiration))
	}
}
