package functional_test

import (
	"bytes"
	"net/http"
	"testing"
)

func TestFunctionalOptionalSourceTokens(t *testing.T) {
	for _, token := range []string{"", "unknown-source-token"} {
		t.Run("token="+token, func(t *testing.T) {
			srv := startServer(t)
			payload := []byte("optional-source-payload")
			hash := ioHash(payload)
			push := authRequest(t, srv.Client, http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s", hash, checksum(payload)), token, bytes.NewReader(payload))
			requireStatus(t, push, http.StatusCreated)
			readBody(t, push)
			if got := pullBytes(t, srv.Client, srv.URL, token, hash); !bytes.Equal(got, payload) {
				t.Fatal("anonymous pull mismatch")
			}
			head := authRequest(t, srv.Client, http.MethodHead, urlf(srv.URL, "/v2/payload/pull?iohash=%s", hash), token, nil)
			requireStatus(t, head, http.StatusOK)
			readBody(t, head)
			exists := jsonRequest(t, srv.Client, http.MethodPost, url(srv.URL, "/v2/metadata/exists"), token, map[string][]string{"hashes": {hash}})
			requireStatus(t, exists, http.StatusOK)
			readBody(t, exists)
			options := authRequest(t, srv.Client, http.MethodOptions, url(srv.URL, "/v2/payload/push"), token, nil)
			requireStatus(t, options, http.StatusNoContent)
			readBody(t, options)
			// Resume an upload across requests with and without source labels.
			tusPayload := []byte("anonymous-tus")
			createReq, err := http.NewRequest(http.MethodPost, urlf(srv.URL, "/v2/payload/push?iohash=%s&checksum=%s", ioHash(tusPayload), checksum(tusPayload)), nil)
			if err != nil {
				t.Fatal(err)
			}
			createReq.Header.Set("Tus-Resumable", "1.0.0")
			createReq.Header.Set("Upload-Length", "13")
			if token != "" {
				createReq.Header.Set("Authorization", bearer(token))
			}
			created, err := srv.Client.Do(createReq)
			if err != nil {
				t.Fatal(err)
			}
			requireStatus(t, created, http.StatusCreated)
			location := created.Header.Get("Location")
			readBody(t, created)
			patchReq, err := http.NewRequest(http.MethodPatch, srv.URL+location, bytes.NewReader(tusPayload))
			if err != nil {
				t.Fatal(err)
			}
			patchReq.Header.Set("Tus-Resumable", "1.0.0")
			patchReq.Header.Set("Upload-Offset", "0")
			patchReq.Header.Set("Content-Type", "application/offset+octet-stream")
			patched, err := srv.Client.Do(patchReq)
			if err != nil {
				t.Fatal(err)
			}
			requireStatus(t, patched, http.StatusCreated)
			readBody(t, patched)
			if got := pullBytes(t, srv.Client, srv.URL, "", ioHash(tusPayload)); !bytes.Equal(got, tusPayload) {
				t.Fatal("anonymous tus mismatch")
			}
		})
	}
}
