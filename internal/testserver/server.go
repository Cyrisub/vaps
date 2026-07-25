package testserver

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"vaps/internal/auth"
	"vaps/internal/blobstore"
	"vaps/internal/cache"
	"vaps/internal/httpapi"
	"vaps/internal/metadata"
	"vaps/internal/uploadsession"
)

// Options configures a functional test server.
type Options struct {
	ClientTimeout time.Duration
}

// Server runs vaps over real localhost TCP.
type Server struct {
	URL     string
	Client  *http.Client
	Objects *FakeObjectStore
}

// Start wires the same HTTP stack as cmd/vaps and listens on 127.0.0.1:0.
func Start(t *testing.T, opts Options) *Server {
	t.Helper()

	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")

	meta, err := metadata.Open(filepath.Join(dataDir, "metadata.db"))
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	store := blobstore.New(dataDir)
	lru := cache.New(64*1024*1024, 4*1024*1024)

	objects := NewFakeObjectStore()

	authStore, err := auth.Open(filepath.Join(dataDir, "auth.db"), 30*time.Minute)
	if err != nil {
		t.Fatalf("open auth: %v", err)
	}
	uploadsDir := filepath.Join(dataDir, "uploads")
	uploads, err := uploadsession.Open(filepath.Join(dataDir, "uploads.db"), uploadsDir, 24*time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("open uploads: %v", err)
	}

	handler := httpapi.NewV2(store, objects, meta, lru, authStore, uploads, httpapi.Options{
		AuthExpireTime:        30 * time.Minute,
		UploadDirectMaxBytes:  8 * 1024 * 1024,
		UploadExpiration:      24 * time.Hour,
		UploadCleanupInterval: time.Minute,
		ListenAddr:            "127.0.0.1:0",
		DataDir:               dataDir,
		MetadataDB:            filepath.Join(dataDir, "metadata.db"),
		AuthDB:                filepath.Join(dataDir, "auth.db"),
		UploadDB:              filepath.Join(dataDir, "uploads.db"),
		UploadDir:             uploadsDir,
		LogDir:                filepath.Join(dataDir, "logs"),
		LogRetentionDays:      7,
		StatusLogInterval:     time.Minute,
		CacheBytes:            64 * 1024 * 1024,
		CacheMaxObjectBytes:   8 * 1024 * 1024,
		StartedAt:             time.Now().UTC(),
	}).HTTPHandler()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	httpServer := &http.Server{Handler: handler}
	go func() {
		_ = httpServer.Serve(listener)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownCtx)
		_ = uploads.Close()
		_ = authStore.Close()
		_ = meta.Close()
	})

	clientTimeout := 15 * time.Second
	if opts.ClientTimeout > 0 {
		clientTimeout = opts.ClientTimeout
	}

	return &Server{
		URL: "http://" + listener.Addr().String(),
		Client: &http.Client{
			Timeout: clientTimeout,
		},
		Objects: objects,
	}
}
