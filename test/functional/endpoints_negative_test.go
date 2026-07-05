package functional_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestFunctionalNegativeUnknownRoute(t *testing.T) {
	srv := startServer(t)
	resp, err := srv.Client.Get(url(srv.URL, "/v2/unknown"))
	if err != nil {
		t.Fatalf("GET unknown: %v", err)
	}
	requireStatus(t, resp, http.StatusNotFound)
	readBody(t, resp)
}

func TestFunctionalNegativeAuthRequest(t *testing.T) {
	srv := startServer(t)

	invalidJSON, err := srv.Client.Post(url(srv.URL, "/v2/auth/request"), "application/json", strings.NewReader(`{`))
	if err != nil {
		t.Fatalf("POST invalid json: %v", err)
	}
	requireStatus(t, invalidJSON, http.StatusBadRequest)
	readBody(t, invalidJSON)

	missingClient, err := srv.Client.Post(url(srv.URL, "/v2/auth/request"), "application/json", strings.NewReader(`{"client_info":{"hostname":"x"}}`))
	if err != nil {
		t.Fatalf("POST missing client_id: %v", err)
	}
	requireStatus(t, missingClient, http.StatusBadRequest)
	readBody(t, missingClient)
}

func TestFunctionalNegativeAuthExpire(t *testing.T) {
	srv := startServer(t)

	missingToken, err := srv.Client.Get(url(srv.URL, "/v2/auth/expire"))
	if err != nil {
		t.Fatalf("GET expire without token: %v", err)
	}
	requireStatus(t, missingToken, http.StatusBadRequest)
	readBody(t, missingToken)

	unknownToken, err := srv.Client.Get(urlf(srv.URL, "/v2/auth/expire?token=%s", "not-a-real-token"))
	if err != nil {
		t.Fatalf("GET expire unknown token: %v", err)
	}
	requireStatus(t, unknownToken, http.StatusOK)
	readBody(t, unknownToken)
}

func TestFunctionalNegativePullAuthAndValidation(t *testing.T) {
	srv := startServer(t)
	token := requestToken(t, srv.Client, srv.URL)
	hash := ioHash([]byte("pull-negative"))

	noAuth, err := srv.Client.Get(urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash))
	if err != nil {
		t.Fatalf("GET without auth: %v", err)
	}
	requireStatus(t, noAuth, http.StatusUnauthorized)
	readBody(t, noAuth)

	badHash := authRequest(t, srv.Client, http.MethodGet, url(srv.URL, "/v2/payload/pull?iohash=bad"), token, nil)
	requireStatus(t, badHash, http.StatusBadRequest)
	readBody(t, badHash)

	missing := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, missing, http.StatusNotFound)
	readBody(t, missing)

	methodNotAllowed := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, methodNotAllowed, http.StatusMethodNotAllowed)
	readBody(t, methodNotAllowed)
}

func TestFunctionalNegativePullRange(t *testing.T) {
	srv := startServer(t)
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("range-negative-test")
	hash := ioHash(payload)

	push := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s", hash), token, bytes.NewReader(payload))
	requireStatus(t, push, http.StatusCreated)
	readBody(t, push)

	badRangeReq, err := http.NewRequest(http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), nil)
	if err != nil {
		t.Fatalf("new range request: %v", err)
	}
	badRangeReq.Header.Set("Authorization", bearer(token))
	badRangeReq.Header.Set("Range", "bytes=9999-10000")
	badRange, err := srv.Client.Do(badRangeReq)
	if err != nil {
		t.Fatalf("GET bad range: %v", err)
	}
	requireStatus(t, badRange, http.StatusRequestedRangeNotSatisfiable)
	readBody(t, badRange)

	invalidRangeReq, err := http.NewRequest(http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), nil)
	if err != nil {
		t.Fatalf("new invalid range request: %v", err)
	}
	invalidRangeReq.Header.Set("Authorization", bearer(token))
	invalidRangeReq.Header.Set("Range", "invalid")
	invalidRange, err := srv.Client.Do(invalidRangeReq)
	if err != nil {
		t.Fatalf("GET invalid range: %v", err)
	}
	requireStatus(t, invalidRange, http.StatusBadRequest)
	readBody(t, invalidRange)
}

func TestFunctionalNegativePushAuthAndValidation(t *testing.T) {
	srv := startServer(t)
	token := requestToken(t, srv.Client, srv.URL)
	hash := ioHash([]byte("expected-bytes"))

	noAuth, err := srv.Client.Post(urlf(srv.URL, "/v2/payload/push?iohash=%s", hash), "application/octet-stream", bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatalf("POST without auth: %v", err)
	}
	requireStatus(t, noAuth, http.StatusUnauthorized)
	readBody(t, noAuth)

	hashMismatch := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s", hash), token, bytes.NewReader([]byte("wrong")))
	requireStatus(t, hashMismatch, http.StatusBadRequest)
	readBody(t, hashMismatch)

	badHash := authRequest(t, srv.Client, http.MethodPost, url(srv.URL, "/v2/payload/push?iohash=bad"), token, bytes.NewReader([]byte("x")))
	requireStatus(t, badHash, http.StatusBadRequest)
	readBody(t, badHash)

	oversized := bytes.Repeat([]byte("x"), 8*1024*1024+1)
	tooLarge := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s", ioHash(oversized)), token, bytes.NewReader(oversized))
	requireStatus(t, tooLarge, http.StatusRequestEntityTooLarge)
	readBody(t, tooLarge)

	patchNoUploadID := authRequest(t, srv.Client, http.MethodPatch, urlf(srv.URL, "/v2/payload/push?iohash=%s", hash), token, bytes.NewReader([]byte("x")))
	requireStatus(t, patchNoUploadID, http.StatusBadRequest)
	readBody(t, patchNoUploadID)
}

func TestFunctionalNegativeMetadata(t *testing.T) {
	srv := startServer(t)
	token := requestToken(t, srv.Client, srv.URL)

	existsNoAuth := jsonRequest(t, srv.Client, http.MethodPost, url(srv.URL, "/v2/metadata/exists"), "", map[string][]string{
		"hashes": {ioHash([]byte("x"))},
	})
	requireStatus(t, existsNoAuth, http.StatusUnauthorized)
	readBody(t, existsNoAuth)

	existsEmpty := jsonRequest(t, srv.Client, http.MethodPost, url(srv.URL, "/v2/metadata/exists"), token, map[string][]string{
		"hashes": {},
	})
	requireStatus(t, existsEmpty, http.StatusBadRequest)
	readBody(t, existsEmpty)

	existsBadHash := jsonRequest(t, srv.Client, http.MethodPost, url(srv.URL, "/v2/metadata/exists"), token, map[string][]string{
		"hashes": {"not-a-hash"},
	})
	requireStatus(t, existsBadHash, http.StatusBadRequest)
	readBody(t, existsBadHash)

	vcsBadJSON, err := srv.Client.Post(url(srv.URL, "/v2/metadata"), "application/json", strings.NewReader(`{`))
	if err != nil {
		t.Fatalf("POST vcs invalid json: %v", err)
	}
	requireStatus(t, vcsBadJSON, http.StatusBadRequest)
	readBody(t, vcsBadJSON)

	vcsMissingFields, err := srv.Client.Post(url(srv.URL, "/v2/metadata"), "application/json", strings.NewReader(`{"payload_hash":"`+ioHash([]byte("x"))+`"}`))
	if err != nil {
		t.Fatalf("POST vcs missing fields: %v", err)
	}
	requireStatus(t, vcsMissingFields, http.StatusBadRequest)
	readBody(t, vcsMissingFields)

	getMissingHash, err := srv.Client.Get(url(srv.URL, "/v2/metadata"))
	if err != nil {
		t.Fatalf("GET vcs without payload_hash: %v", err)
	}
	requireStatus(t, getMissingHash, http.StatusBadRequest)
	readBody(t, getMissingHash)

	getBadHash, err := srv.Client.Get(url(srv.URL, "/v2/metadata?payload_hash=bad"))
	if err != nil {
		t.Fatalf("GET vcs bad hash: %v", err)
	}
	requireStatus(t, getBadHash, http.StatusBadRequest)
	readBody(t, getBadHash)
}

func TestFunctionalNegativeDashboardQuery(t *testing.T) {
	srv := startServer(t)

	badLimit, err := srv.Client.Get(url(srv.URL, "/dashboard/metadata/query?limit=-1"))
	if err != nil {
		t.Fatalf("GET query bad limit: %v", err)
	}
	requireStatus(t, badLimit, http.StatusBadRequest)
	readBody(t, badLimit)

	badStatus, err := srv.Client.Get(url(srv.URL, "/dashboard/metadata/query?status=unknown"))
	if err != nil {
		t.Fatalf("GET query bad status: %v", err)
	}
	requireStatus(t, badStatus, http.StatusBadRequest)
	readBody(t, badStatus)

	badSizeRange, err := srv.Client.Get(url(srv.URL, "/dashboard/metadata/query?min_size=10&max_size=5"))
	if err != nil {
		t.Fatalf("GET query bad size range: %v", err)
	}
	requireStatus(t, badSizeRange, http.StatusBadRequest)
	readBody(t, badSizeRange)
}
