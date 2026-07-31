package functional_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFunctionalPositiveDuplicateDirectPush(t *testing.T) {
	srv := startServer(t)
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("duplicate-push")
	hash := ioHash(payload)

	first := pushDirect(t, srv.Client, srv.URL, token, hash, payload)
	if first != http.StatusCreated {
		t.Fatalf("first push status = %d, want %d", first, http.StatusCreated)
	}

	second := pushDirect(t, srv.Client, srv.URL, token, hash, payload)
	if second != http.StatusOK {
		t.Fatalf("second push status = %d, want %d", second, http.StatusOK)
	}
}

func TestFunctionalPositiveTusHeadAndDelete(t *testing.T) {
	srv := startServer(t)
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("tus-head-delete")
	hash := ioHash(payload)
	fileChecksum := checksum(payload)

	createReq, err := http.NewRequest(http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s", hash, fileChecksum), nil)
	if err != nil {
		t.Fatalf("new tus create: %v", err)
	}
	createReq.Header.Set("Authorization", bearer(token))
	createReq.Header.Set("Tus-Resumable", "1.0.0")
	createReq.Header.Set("Upload-Length", "15")
	createResp, err := srv.Client.Do(createReq)
	if err != nil {
		t.Fatalf("tus create: %v", err)
	}
	requireStatus(t, createResp, http.StatusCreated)
	location := createResp.Header.Get("Location")
	readBody(t, createResp)
	uploadID := strings.TrimPrefix(location, "/v2/payload/push?iohash="+hash+"&checksum="+fileChecksum+"&upload_id=")

	headReq, err := http.NewRequest(http.MethodHead, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s&upload_id=%s", hash, fileChecksum, uploadID), nil)
	if err != nil {
		t.Fatalf("new tus head: %v", err)
	}
	headReq.Header.Set("Authorization", bearer(token))
	headResp, err := srv.Client.Do(headReq)
	if err != nil {
		t.Fatalf("tus head: %v", err)
	}
	requireStatus(t, headResp, http.StatusOK)
	if headResp.Header.Get("Upload-Offset") != "0" {
		t.Fatalf("Upload-Offset = %q, want 0", headResp.Header.Get("Upload-Offset"))
	}
	readBody(t, headResp)

	deleteReq, err := http.NewRequest(http.MethodDelete, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s&upload_id=%s", hash, fileChecksum, uploadID), nil)
	if err != nil {
		t.Fatalf("new tus delete: %v", err)
	}
	deleteReq.Header.Set("Authorization", bearer(token))
	deleteReq.Header.Set("Tus-Resumable", "1.0.0")
	deleteResp, err := srv.Client.Do(deleteReq)
	if err != nil {
		t.Fatalf("tus delete: %v", err)
	}
	requireStatus(t, deleteResp, http.StatusNoContent)
	readBody(t, deleteResp)

	pull := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, pull, http.StatusNotFound)
	readBody(t, pull)
}

func TestFunctionalPositiveMetadataGetEmpty(t *testing.T) {
	srv := startServer(t)
	hash := ioHash([]byte("no-vcs-records"))

	resp, err := srv.Client.Get(urlf(srv.URL, "/v2/metadata?payload_hash=%s", hash))
	if err != nil {
		t.Fatalf("GET metadata: %v", err)
	}
	requireStatus(t, resp, http.StatusOK)
	var records []json.RawMessage
	if err := json.Unmarshal(readBody(t, resp), &records); err != nil {
		t.Fatalf("decode records: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %d, want 0", len(records))
	}
}

func TestFunctionalPositivePullHeadBeforeGet(t *testing.T) {
	srv := startServer(t)
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("head-before-get")
	hash := ioHash(payload)

	pushDirect(t, srv.Client, srv.URL, token, hash, payload)

	head := authRequest(t, srv.Client, http.MethodHead, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, head, http.StatusOK)
	if head.Header.Get("Content-Length") == "" {
		t.Fatalf("Content-Length header is missing")
	}
	readBody(t, head)

	got := pullBytes(t, srv.Client, srv.URL, token, hash)
	if !bytes.Equal(got, payload) {
		t.Fatalf("pull body mismatch")
	}
}

func TestFunctionalPositiveInvalidBearerToken(t *testing.T) {
	srv := startServer(t)
	hash := ioHash([]byte("bad-token"))

	resp := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), "not-valid", nil)
	requireStatus(t, resp, http.StatusUnauthorized)
	readBody(t, resp)
}
