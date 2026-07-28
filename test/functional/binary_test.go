package functional_test

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestFunctionalBinaryProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary functional test in short mode")
	}

	binary := filepath.Join(t.TempDir(), "vaps")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/vaps")
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build vaps: %v\n%s", err, out)
	}

	root := t.TempDir()
	s3 := newBinaryS3Server(t)
	dataDir := filepath.Join(root, "data")
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	cmd := exec.Command(binary,
		"--addr", addr,
		"--data-dir", dataDir,
		"--log-dir", logDir,
		"--status-log-interval", "0s",
		"--storage-s3-bucket", "functional-test",
		"--storage-s3-region", "us-east-1",
		"--storage-s3-endpoint", s3.URL,
		"--storage-s3-force-path-style=true",
	)
	cmd.Env = append(os.Environ(), "VAPS_STORAGE_S3_SECRETID=functional-test", "VAPS_STORAGE_S3_SECRETKEY=functional-test")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start vaps: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	client := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(stderr)
			t.Fatalf("vaps did not become ready on %s; stderr=%q", baseURL, buf.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	token := requestToken(t, client, baseURL)
	payload := []byte("binary-process-functional")
	hash := ioHash(payload)

	push := authRequest(t, client, http.MethodPost, urlf(baseURL, "/v2/payload/push?iohash=%s", hash), token, bytes.NewReader(payload))
	requireStatus(t, push, http.StatusCreated)
	readBody(t, push)

	pull := authRequest(t, client, http.MethodGet, urlf(baseURL, "/v2/payload/pull?iohash=%s", hash), token, nil)
	requireStatus(t, pull, http.StatusOK)
	if !bytes.Equal(readBody(t, pull), payload) {
		t.Fatalf("pull body mismatch from binary process")
	}
}

type binaryS3Server struct {
	*httptest.Server
	mu      sync.Mutex
	objects map[string]binaryS3Object
}

type binaryS3Object struct {
	data     []byte
	metadata http.Header
}

func newBinaryS3Server(t *testing.T) *binaryS3Server {
	t.Helper()
	fake := &binaryS3Server{objects: map[string]binaryS3Object{}}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.Close)
	return fake
}

func (s *binaryS3Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Path
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		metadata := make(http.Header)
		for _, name := range []string{"X-Amz-Meta-Vaps-Iohash", "X-Amz-Meta-Vaps-Blake3", "X-Amz-Meta-Vaps-Size"} {
			metadata.Set(name, r.Header.Get(name))
		}
		s.objects[key] = binaryS3Object{data: data, metadata: metadata}
		w.WriteHeader(http.StatusOK)
	case http.MethodHead, http.MethodGet:
		object, ok := s.objects[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		for name, values := range object.metadata {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(object.data)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(object.data)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("could not find project root from %s", wd)
		}
		wd = parent
	}
}
