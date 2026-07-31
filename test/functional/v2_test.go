package functional_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"vaps/internal/metadata"
	"vaps/internal/testserver"
)

func TestFunctionalHealth(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	resp, err := srv.Client.Get(url(srv.URL, "/health"))
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	requireStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	if string(body) != "ok\n" {
		t.Fatalf("body = %q, want ok newline", body)
	}
}

func TestFunctionalV2AuthFlow(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)

	expireResp := authRequest(t, srv.Client, http.MethodGet, url(srv.URL, "/v2/auth/expire"), token, nil)
	requireStatus(t, expireResp, http.StatusOK)
	readBody(t, expireResp)

	pull := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", ioHash([]byte("missing"))), token, nil)
	requireStatus(t, pull, http.StatusUnauthorized)
	readBody(t, pull)
}

func TestFunctionalV2AuthParkAndWake(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)

	parkResp := authRequest(t, srv.Client, http.MethodGet, url(srv.URL, "/v2/auth/park"), token, nil)
	requireStatus(t, parkResp, http.StatusOK)
	readBody(t, parkResp)

	pull := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", ioHash([]byte("missing"))), token, nil)
	requireStatus(t, pull, http.StatusUnauthorized)
	readBody(t, pull)

	woken := requestToken(t, srv.Client, srv.URL)
	if woken != token {
		t.Fatalf("wake returned different token")
	}
	pullAfterWake := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", ioHash([]byte("missing"))), woken, nil)
	requireStatus(t, pullAfterWake, http.StatusNotFound)
	readBody(t, pullAfterWake)
}

func TestFunctionalV2DirectPushPullAndRange(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("hello-functional-range")
	hash := ioHash(payload)

	push := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s", hash, checksum(payload)), token, bytes.NewReader(payload))
	requireStatus(t, push, http.StatusCreated)
	readBody(t, push)

	head := authRequest(t, srv.Client, http.MethodHead, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, head, http.StatusOK)
	if head.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", head.Header.Get("Accept-Ranges"))
	}
	readBody(t, head)

	rangeReq, err := http.NewRequest(http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), nil)
	if err != nil {
		t.Fatalf("new range request: %v", err)
	}
	rangeReq.Header.Set("Authorization", bearer(token))
	rangeReq.Header.Set("Range", "bytes=0-2")
	rangeResp, err := srv.Client.Do(rangeReq)
	if err != nil {
		t.Fatalf("range GET: %v", err)
	}
	requireStatus(t, rangeResp, http.StatusPartialContent)
	if got := rangeResp.Header.Get("Content-Range"); got != "bytes 0-2/22" {
		t.Fatalf("Content-Range = %q, want bytes 0-2/22", got)
	}
	rangeBody := readBody(t, rangeResp)
	if !bytes.Equal(rangeBody, payload[:3]) {
		t.Fatalf("range body = %q, want %q", rangeBody, payload[:3])
	}

	full := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, full, http.StatusOK)
	fullBody := readBody(t, full)
	if !bytes.Equal(fullBody, payload) {
		t.Fatalf("full body = %q, want %q", fullBody, payload)
	}
}

func TestFunctionalV2PushOptions(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)

	req, err := http.NewRequest(http.MethodOptions, url(srv.URL, "/v2/payload/push"), nil)
	if err != nil {
		t.Fatalf("new OPTIONS request: %v", err)
	}
	req.Header.Set("Authorization", bearer(token))
	resp, err := srv.Client.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	requireStatus(t, resp, http.StatusNoContent)
	readBody(t, resp)
	if resp.Header.Get("Tus-Resumable") != "1.0.0" {
		t.Fatalf("Tus-Resumable = %q, want 1.0.0", resp.Header.Get("Tus-Resumable"))
	}
	if resp.Header.Get("Upload-Direct-Max-Bytes") == "" {
		t.Fatalf("Upload-Direct-Max-Bytes header is missing")
	}
}

func TestFunctionalV2MetadataExistsAndVCS(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("functional-meta")
	hash := ioHash(payload)

	push := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s", hash, checksum(payload)), token, bytes.NewReader(payload))
	requireStatus(t, push, http.StatusCreated)
	readBody(t, push)

	vcsBody := `{"payload_hash":"` + hash + `","vcs_type":"svn","repo":"repo","revision":"123","path":"/Content/Foo.uasset","asset_id":"Foo"}`
	vcsPost, err := srv.Client.Post(url(srv.URL, "/v2/metadata"), "application/json", strings.NewReader(vcsBody))
	if err != nil {
		t.Fatalf("POST /v2/metadata: %v", err)
	}
	requireStatus(t, vcsPost, http.StatusCreated)
	readBody(t, vcsPost)

	vcsGet, err := srv.Client.Get(urlf(srv.URL, "/v2/metadata?payload_hash=%s", hash))
	if err != nil {
		t.Fatalf("GET /v2/metadata: %v", err)
	}
	requireStatus(t, vcsGet, http.StatusOK)
	var records []metadata.VCSRecord
	if err := json.Unmarshal(readBody(t, vcsGet), &records); err != nil {
		t.Fatalf("decode vcs records: %v", err)
	}
	if len(records) != 1 || records[0].Path != "/Content/Foo.uasset" {
		t.Fatalf("records = %#v", records)
	}

	existsResp := jsonRequest(t, srv.Client, http.MethodPost, url(srv.URL, "/v2/metadata/exists"), token, map[string][]string{
		"hashes": {hash, ioHash([]byte("missing"))},
	})
	requireStatus(t, existsResp, http.StatusOK)
	var existsBody struct {
		Items map[string]struct {
			Exists bool  `json:"exists"`
			Size   int64 `json:"size"`
		} `json:"items"`
	}
	if err := json.Unmarshal(readBody(t, existsResp), &existsBody); err != nil {
		t.Fatalf("decode exists response: %v", err)
	}
	if !existsBody.Items[hash].Exists {
		t.Fatalf("expected hash to exist: %#v", existsBody.Items)
	}
	if existsBody.Items[ioHash([]byte("missing"))].Exists {
		t.Fatalf("expected missing hash to not exist: %#v", existsBody.Items)
	}
}

func TestFunctionalV2TusUploadWithPatch(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("tus-patch-functional")
	hash := ioHash(payload)
	fileChecksum := checksum(payload)

	createReq, err := http.NewRequest(http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s", hash, fileChecksum), nil)
	if err != nil {
		t.Fatalf("new tus create request: %v", err)
	}
	createReq.Header.Set("Authorization", bearer(token))
	createReq.Header.Set("Tus-Resumable", "1.0.0")
	createReq.Header.Set("Upload-Length", "20")
	createResp, err := srv.Client.Do(createReq)
	if err != nil {
		t.Fatalf("tus create: %v", err)
	}
	requireStatus(t, createResp, http.StatusCreated)
	location := createResp.Header.Get("Location")
	readBody(t, createResp)
	if location == "" {
		t.Fatalf("Location header is empty")
	}

	uploadID := strings.TrimPrefix(location, "/v2/payload/push?iohash="+hash+"&checksum="+fileChecksum+"&upload_id=")
	patchReq, err := http.NewRequest(http.MethodPatch, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s&upload_id=%s", hash, fileChecksum, uploadID), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new tus patch request: %v", err)
	}
	patchReq.Header.Set("Authorization", bearer(token))
	patchReq.Header.Set("Tus-Resumable", "1.0.0")
	patchReq.Header.Set("Upload-Offset", "0")
	patchReq.Header.Set("Content-Type", "application/offset+octet-stream")
	patchResp, err := srv.Client.Do(patchReq)
	if err != nil {
		t.Fatalf("tus patch: %v", err)
	}
	requireStatus(t, patchResp, http.StatusCreated)
	readBody(t, patchResp)

	pull := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, pull, http.StatusOK)
	got := readBody(t, pull)
	if !bytes.Equal(got, payload) {
		t.Fatalf("pull body = %q, want %q", got, payload)
	}
}

func TestFunctionalV2TusCreationWithUpload(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("tus-create-with-upload")
	hash := ioHash(payload)
	fileChecksum := checksum(payload)

	createReq, err := http.NewRequest(http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s", hash, fileChecksum), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new tus create request: %v", err)
	}
	createReq.Header.Set("Authorization", bearer(token))
	createReq.Header.Set("Tus-Resumable", "1.0.0")
	createReq.Header.Set("Upload-Length", "22")
	createResp, err := srv.Client.Do(createReq)
	if err != nil {
		t.Fatalf("tus create with upload: %v", err)
	}
	requireStatus(t, createResp, http.StatusCreated)
	readBody(t, createResp)

	pull := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, pull, http.StatusOK)
	if !bytes.Equal(readBody(t, pull), payload) {
		t.Fatalf("pull body mismatch")
	}
}

func TestFunctionalV2UnauthorizedWithoutToken(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	hash := ioHash([]byte("secret"))

	resp, err := srv.Client.Get(urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash))
	if err != nil {
		t.Fatalf("GET without token: %v", err)
	}
	requireStatus(t, resp, http.StatusUnauthorized)
	readBody(t, resp)
}

func TestFunctionalV2InvalidHash(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)

	resp := authRequest(t, srv.Client, http.MethodGet, url(srv.URL, "/v2/payload/pull?iohash=not-a-hash"), token, nil)
	requireStatus(t, resp, http.StatusBadRequest)
	readBody(t, resp)
}

func TestFunctionalV2MissingPayload(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)
	hash := ioHash([]byte("does-not-exist"))

	resp := authRequest(t, srv.Client, http.MethodGet, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, resp, http.StatusNotFound)
	readBody(t, resp)
}

func TestFunctionalV2PushAcceptsCompressedBytesForUEHash(t *testing.T) {
	srv := testserver.Start(t, testserver.Options{})
	token := requestToken(t, srv.Client, srv.URL)
	hash := ioHash([]byte("expected"))
	payload := []byte("wrong-bytes")

	resp := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s", hash, checksum(payload)), token, bytes.NewReader(payload))
	requireStatus(t, resp, http.StatusCreated)
	readBody(t, resp)
}
