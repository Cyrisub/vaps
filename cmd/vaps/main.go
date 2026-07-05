package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"vaps/internal/appconfig"
	"vaps/internal/auth"
	"vaps/internal/backup"
	"vaps/internal/blobstore"
	"vaps/internal/cache"
	"vaps/internal/httpapi"
	"vaps/internal/logfile"
	"vaps/internal/metadata"
	"vaps/internal/uploadsession"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := appconfig.FromArgs(nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	logWriter, err := setupLogging(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	writeStartupLog(logWriter, time.Now())

	exitCode := 1
	defer func() {
		stop()
		log.Printf("vaps stopped exit_code=%d", exitCode)
		_ = logWriter.Close()
	}()

	meta, err := metadata.Open(cfg.MetadataDB)
	if err != nil {
		log.Print(err)
		return 1
	}
	defer meta.Close()

	store := blobstore.New(cfg.DataDir)
	lru := cache.New(cfg.Cache.Bytes, cfg.Cache.MaxObjectBytes)
	backupBackend, err := setupBackup(ctx, cfg, store, meta)
	if err != nil {
		log.Print(err)
		return 1
	}
	authStore, err := auth.Open(cfg.AuthDB, time.Duration(cfg.Auth.ExpireTime))
	if err != nil {
		log.Print(err)
		return 1
	}
	defer authStore.Close()
	uploadsDir := filepath.Join(cfg.DataDir, "uploads")
	uploads, err := uploadsession.Open(cfg.UploadDB, uploadsDir, time.Duration(cfg.Upload.Expiration), time.Duration(cfg.Upload.CleanupInterval))
	if err != nil {
		log.Print(err)
		return 1
	}
	defer uploads.Close()
	startUploadCleanup(ctx, uploads, time.Duration(cfg.Upload.CleanupInterval))
	handler := httpapi.AccessLog(httpapi.NewV2(store, meta, lru, backupBackend, authStore, uploads, httpapi.Options{
		AuthExpireTime:        time.Duration(cfg.Auth.ExpireTime),
		UploadDirectMaxBytes:  cfg.Upload.DirectMaxBytes,
		UploadExpiration:      time.Duration(cfg.Upload.Expiration),
		UploadCleanupInterval: time.Duration(cfg.Upload.CleanupInterval),
	}))
	startStatusLogger(ctx, time.Duration(cfg.StatusLogInterval), meta, lru)

	server := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	log.Printf("vaps listening on %s with data dir %s and metadata db %s", listenURL(cfg.Addr), cfg.DataDir, cfg.MetadataDB)
	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print(err)
			return 1
		}
	case <-ctx.Done():
		log.Print("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Print(err)
			return 1
		}
	}
	flushBackup(backupBackend)
	exitCode = 0
	return exitCode
}

func setupBackup(ctx context.Context, cfg appconfig.Config, store *blobstore.Store, meta *metadata.Store) (backup.Backend, error) {
	var backend backup.Backend
	switch strings.ToLower(strings.TrimSpace(cfg.Backup.Backend)) {
	case "", "none":
		return nil, nil
	case "svn":
		created, err := backup.NewSVNBackend(backup.SVNConfig{
			URL:        cfg.Backup.SVN.URL,
			SVNBin:     cfg.Backup.SVN.Bin,
			SVNMuccBin: cfg.Backup.SVN.MuccBin,
		})
		if err != nil {
			return nil, err
		}
		backend = created
	default:
		return nil, fmt.Errorf("unsupported backup backend %q", cfg.Backup.Backend)
	}
	metadataBackend := backup.NewEmptyMetadataBackend(backend)
	startBackupMetadataRefresh(ctx, meta, metadataBackend)
	queue := backup.NewQueue(metadataBackend, func(hash string) (io.ReadCloser, error) {
		reader, _, err := store.Open(hash)
		return reader, err
	}, backup.QueueConfig{
		Interval:   time.Duration(cfg.Backup.FlushInterval),
		MaxPending: cfg.Backup.MaxPending,
	}, backup.QueueCallbacks{
		OnPending:  func(hash string) { markBackupPending(meta, hash) },
		OnBackuped: func(hash string) { markBackuped(meta, hash) },
		OnFailed:   func(hash string, err error) { markBackupFailed(meta, hash, err) },
	})
	queue.Start(ctx)
	return queue, nil
}

func startBackupMetadataRefresh(ctx context.Context, meta *metadata.Store, backend *backup.MetadataBackend) {
	go func() {
		started := time.Now()
		log.Printf("backup metadata refresh started backend=%q", backend.Name())
		if err := backend.Refresh(ctx); err != nil {
			log.Printf("backup metadata refresh failed backend=%q duration=%s error=%q", backend.Name(), time.Since(started).Truncate(time.Millisecond), err)
			return
		}
		if err := mergeBackupMetadata(ctx, meta, backend); err != nil {
			log.Printf("backup metadata merge failed backend=%q duration=%s error=%q", backend.Name(), time.Since(started).Truncate(time.Millisecond), err)
			return
		}
		log.Printf("backup metadata refresh completed backend=%q count=%d duration=%s", backend.Name(), backend.Count(), time.Since(started).Truncate(time.Millisecond))
	}()
}

func mergeBackupMetadata(ctx context.Context, meta *metadata.Store, backend backup.Backend) error {
	if meta == nil || backend == nil {
		return nil
	}
	objects, err := backend.List(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, object := range objects {
		record, err := meta.GetPayload(object.Hash)
		if errors.Is(err, metadata.ErrNotFound) {
			if err := meta.PutPayload(metadata.Payload{
				Hash:         object.Hash,
				Status:       metadata.StatusBackup,
				BackupStatus: metadata.Backuped,
				BackupedAt:   &now,
			}); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if record.Status.HasBackup() && record.BackupStatus == metadata.Backuped {
			continue
		}
		record.Status = record.Status.WithBackup(true)
		record.BackupStatus = metadata.Backuped
		if record.BackupedAt == nil {
			record.BackupedAt = &now
		}
		if err := meta.PutPayload(record); err != nil {
			return err
		}
	}
	log.Printf("backup metadata merged records=%d", len(objects))
	return nil
}

func flushBackup(backend backup.Backend) {
	flusher, ok := backend.(interface {
		Flush(context.Context) ([]backup.Object, error)
	})
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := flusher.Flush(ctx); err != nil {
		log.Printf("backup final flush failed error=%q", err)
	}
}

func markBackupPending(meta *metadata.Store, hash string) {
	record, err := meta.GetPayload(hash)
	if err != nil {
		return
	}
	record.BackupStatus = metadata.BackupPending
	_ = meta.PutPayload(record)
}

func markBackuped(meta *metadata.Store, hash string) {
	record, err := meta.GetPayload(hash)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	record.Status = record.Status.WithBackup(true)
	record.BackupStatus = metadata.Backuped
	record.BackupedAt = &now
	_ = meta.PutPayload(record)
}

func markBackupFailed(meta *metadata.Store, hash string, err error) {
	log.Printf("backup payload failed hash=%s error=%q", hash, err)
	record, getErr := meta.GetPayload(hash)
	if getErr != nil {
		return
	}
	record.BackupStatus = metadata.BackupFailed
	_ = meta.PutPayload(record)
}

func setupLogging(cfg appconfig.Config) (io.WriteCloser, error) {
	writer, err := logfile.NewDailyRotatingWriter(cfg.LogDir, cfg.LogRetentionDays)
	if err != nil {
		return nil, err
	}
	log.SetOutput(io.MultiWriter(os.Stderr, writer))
	return writer, nil
}

func writeStartupLog(fileWriter io.Writer, startedAt time.Time) {
	_, _ = fileWriter.Write([]byte("\n"))
	log.Printf("=== vaps startup started_at=%s ===", startedAt.Format(time.RFC3339))
}

func listenURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return "http://localhost" + addr
		}
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func startUploadCleanup(ctx context.Context, uploads *uploadsession.Store, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := uploads.CleanupExpired(); err != nil {
					log.Printf("upload cleanup failed error=%q", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func startStatusLogger(ctx context.Context, interval time.Duration, meta *metadata.Store, lru *cache.Cache) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				logStatus(meta, lru)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func logStatus(meta *metadata.Store, lru *cache.Cache) {
	metadataStats, err := meta.Stats()
	cacheStats := lru.Stats()
	if err != nil {
		log.Printf("status metadata_error=%q cache_entries=%d cache_used_bytes=%d cache_hits=%d cache_misses=%d", err, cacheStats.Entries, cacheStats.UsedBytes, cacheStats.Hits, cacheStats.Misses)
		return
	}
	log.Printf(
		"status payload_count=%d total_bytes=%d cached_count=%d cache_entries=%d cache_used_bytes=%d cache_max_bytes=%d cache_hits=%d cache_misses=%d",
		metadataStats.PayloadCount,
		metadataStats.TotalBytes,
		metadataStats.CachedCount,
		cacheStats.Entries,
		cacheStats.UsedBytes,
		cacheStats.MaxBytes,
		cacheStats.Hits,
		cacheStats.Misses,
	)
}
