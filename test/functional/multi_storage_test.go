package functional_test

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestFunctionalBinaryMultiplePersistentBackends(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary functional test in short mode")
	}
	binary := functionalBinary(t)
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	path := func(name string) string { return strconv.Quote(filepath.ToSlash(filepath.Join(root, name))) }
	config := fmt.Sprintf(`addr = %q
data_dir = %s
metadata_db = %s
auth_db = %s
upload_db = %s
log_dir = %s
[upload]
dir = %s
[storage]
replication_dir = %s
replication_retry_interval = "20ms"
[storage.local]
dir = %s
[[storage.backends]]
name = "disk"
type = "filesystem"
dir = %s
[[storage.backends]]
name = "smb"
type = "filesystem"
dir = %s
`, addr, path("data"), path("data/metadata.db"), path("data/auth.db"), path("data/uploads.db"), path("logs"), path("uploads"), path("queue"), path("cache"), path("preferred"), path("replica"))
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "--config", configPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	client := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := client.Get(baseURL + "/health")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("multi-backend process did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	data := []byte("multi-backend-binary-functional-payload")
	hash := ioHash(data)
	push := authRequest(t, client, http.MethodPost, urlf(baseURL, "/v2/payload/push?iohash=%s&checksum=%s", hash, checksum(data)), "", bytes.NewReader(data))
	requireStatus(t, push, http.StatusCreated)
	readBody(t, push)
	objectDir := filepath.Join(hash[:2], hash[2:4], hash)
	replicaPath := filepath.Join(root, "replica", objectDir, "payload")
	deadline = time.Now().Add(5 * time.Second)
	for {
		stored, err := os.ReadFile(replicaPath)
		if err == nil && bytes.Equal(stored, data) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("async replica missing: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Simulate losing the preferred copy and start the pull from an empty cache.
	if err := os.RemoveAll(filepath.Join(root, "preferred", objectDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "cache", "blobs", hash[:2], hash[2:4], hash+".upayload")); err != nil {
		t.Fatal(err)
	}
	pull := authRequest(t, client, http.MethodGet, urlf(baseURL, "/v2/payload/pull?iohash=%s", hash), "", nil)
	requireStatus(t, pull, http.StatusOK)
	if !bytes.Equal(readBody(t, pull), data) {
		t.Fatal("binary failed to read fallback replica")
	}
}
