package testserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"vaps/internal/auth"
	"vaps/internal/backup"
	"vaps/internal/blobstore"
	"vaps/internal/cache"
	"vaps/internal/httpapi"
	"vaps/internal/metadata"
	"vaps/internal/uploadsession"
)

// Options configures a functional test server.
type Options struct {
	FakeBackup    bool
	ClientTimeout time.Duration
}

// Server runs vaps over real localhost TCP.
type Server struct {
	URL    string
	Client *http.Client
	Backup *FakeBackup
}

// Start wires the same HTTP stack as cmd/vaps and listens on 127.0.0.1:0.
func Start(t *testing.T, opts Options) *Server {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")

	meta, err := metadata.Open(filepath.Join(dataDir, "metadata.db"))
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	store := blobstore.New(dataDir)
	lru := cache.New(64*1024*1024, 4*1024*1024)

	var backupBackend backup.Backend
	var fake *FakeBackup
	if opts.FakeBackup {
		fake = NewFakeBackup()
		metadataBackend := backup.NewEmptyMetadataBackend(fake)
		queue := backup.NewQueue(metadataBackend, func(hash string) (io.ReadCloser, error) {
			reader, _, err := store.Open(hash)
			return reader, err
		}, backup.QueueConfig{
			Interval:   time.Minute,
			MaxPending: 100,
		}, backup.QueueCallbacks{})
		queue.Start(ctx)
		backupBackend = queue
	}

	authStore, err := auth.Open(filepath.Join(dataDir, "auth.db"), 30*time.Minute)
	if err != nil {
		t.Fatalf("open auth: %v", err)
	}
	uploadsDir := filepath.Join(dataDir, "uploads")
	uploads, err := uploadsession.Open(filepath.Join(dataDir, "uploads.db"), uploadsDir, 24*time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("open uploads: %v", err)
	}

	handler := httpapi.AccessLog(httpapi.NewV2(store, meta, lru, backupBackend, authStore, uploads, httpapi.Options{
		AuthExpireTime:        30 * time.Minute,
		UploadDirectMaxBytes:  8 * 1024 * 1024,
		UploadExpiration:      24 * time.Hour,
		UploadCleanupInterval: time.Minute,
	}))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	httpServer := &http.Server{Handler: handler}
	go func() {
		_ = httpServer.Serve(listener)
	}()

	t.Cleanup(func() {
		cancel()
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
		Backup: fake,
	}
}
