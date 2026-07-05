package functional_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"vaps/internal/utils"
	"vaps/internal/testserver"
)

const (
	smallFileCount = 64
	largeFileCount = 4
	largeFileSize  = 2 * 1024 * 1024
)

func ioHash(payload []byte) string {
	return utils.IoHashSumHex(payload)
}

func bearer(token string) string {
	return "Bearer " + token
}

func startServer(t *testing.T) *testserver.Server {
	t.Helper()
	return testserver.Start(t, testserver.Options{})
}

func startLoadServer(t *testing.T) *testserver.Server {
	t.Helper()
	return testserver.Start(t, testserver.Options{ClientTimeout: 2 * time.Minute})
}

func smallPayload(index int) []byte {
	return []byte(fmt.Sprintf("small-file-%04d-%s", index, strings.Repeat("x", 64+(index%32))))
}

func largePayload(size int) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte('A' + (i % 26))
	}
	return payload
}

func requireStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("%s %s: status = %d, want %d; body=%q", resp.Request.Method, resp.Request.URL, resp.StatusCode, want, body)
	}
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil && len(body) == 0 {
		return nil
	}
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

func requestToken(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	resp, err := client.Post(baseURL+"/v2/auth/request", "application/json", strings.NewReader(`{"client_id":"functional-test","client_info":{"hostname":"localhost"}}`))
	if err != nil {
		t.Fatalf("auth request: %v", err)
	}
	requireStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	var got struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode auth response: %v", err)
	}
	if got.Token == "" {
		t.Fatalf("token is empty")
	}
	return got.Token
}

func authRequest(t *testing.T, client *http.Client, method, rawURL, token string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", bearer(token))
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	return resp
}

func jsonRequest(t *testing.T, client *http.Client, method, rawURL, token string, payload any) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal json: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", bearer(token))
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	return resp
}

func pushDirect(t *testing.T, client *http.Client, baseURL, token, hash string, payload []byte) int {
	t.Helper()
	status, err := pushDirectStatus(client, baseURL, token, hash, payload)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func pushDirectStatus(client *http.Client, baseURL, token, hash string, payload []byte) (int, error) {
	resp, err := doAuthRequest(client, http.MethodPost, urlf(baseURL, "/v2/payload/push?iohash=%s", hash), token, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("push %s: status=%d body=%q", hash[:8], resp.StatusCode, body)
	}
	return resp.StatusCode, nil
}

func pullBytes(t *testing.T, client *http.Client, baseURL, token, hash string) []byte {
	t.Helper()
	got, err := pullBytesStatus(client, baseURL, token, hash)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func pullBytesStatus(client *http.Client, baseURL, token, hash string) ([]byte, error) {
	resp, err := doAuthRequest(client, http.MethodGet, urlf(baseURL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pull %s: status=%d body=%q", hash[:8], resp.StatusCode, body)
	}
	return body, nil
}

func pullHeadStatus(client *http.Client, baseURL, token, hash string) (int, error) {
	resp, err := doAuthRequest(client, http.MethodHead, urlf(baseURL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode, nil
}

func doAuthRequest(client *http.Client, method, rawURL, token string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", bearer(token))
	}
	return client.Do(req)
}

func url(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func urlf(base, format string, args ...any) string {
	return url(base, fmt.Sprintf(format, args...))
}
