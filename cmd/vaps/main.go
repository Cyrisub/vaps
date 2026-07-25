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
	"strings"
	"syscall"
	"time"

	"vaps/internal/appconfig"
	"vaps/internal/auth"
	"vaps/internal/blobstore"
	"vaps/internal/cache"
	"vaps/internal/httpapi"
	"vaps/internal/metadata"
	"vaps/internal/objectstore"
	"vaps/internal/uploadsession"
	"vaps/internal/utils"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := appconfig.FromArgs(nil)
	if errors.Is(err, appconfig.ErrHelp) {
		return 0
	}
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

	store := blobstore.NewWithMaxBytes(cfg.Storage.Local.Dir, cfg.Storage.Local.MaxCacheBytes.Int64())
	objects, err := objectstore.NewS3(ctx, objectstore.S3Config{
		Bucket:         cfg.Storage.S3.Bucket,
		Region:         cfg.Storage.S3.Region,
		Endpoint:       cfg.Storage.S3.Endpoint,
		Prefix:         cfg.Storage.S3.Prefix,
		ForcePathStyle: cfg.Storage.S3.ForcePathStyle,
	})
	if err != nil {
		log.Print(err)
		return 1
	}
	lru := cache.New(cfg.Cache.Bytes.Int64(), cfg.Cache.MaxObjectBytes.Int64())
	authStore, err := auth.Open(cfg.AuthDB, time.Duration(cfg.Auth.ExpireTime))
	if err != nil {
		log.Print(err)
		return 1
	}
	defer authStore.Close()
	uploads, err := uploadsession.Open(cfg.UploadDB, cfg.Upload.Dir, time.Duration(cfg.Upload.Expiration), time.Duration(cfg.Upload.CleanupInterval))
	if err != nil {
		log.Print(err)
		return 1
	}
	defer uploads.Close()
	startUploadCleanup(ctx, uploads, time.Duration(cfg.Upload.CleanupInterval))
	startedAt := time.Now().UTC()
	handler := httpapi.NewV2(store, objects, meta, lru, authStore, uploads, httpapi.Options{
		AuthExpireTime:        time.Duration(cfg.Auth.ExpireTime),
		UploadDirectMaxBytes:  cfg.Upload.DirectMaxBytes.Int64(),
		UploadExpiration:      time.Duration(cfg.Upload.Expiration),
		UploadCleanupInterval: time.Duration(cfg.Upload.CleanupInterval),
		ListenAddr:            cfg.Addr,
		DataDir:               cfg.DataDir,
		MetadataDB:            cfg.MetadataDB,
		AuthDB:                cfg.AuthDB,
		UploadDB:              cfg.UploadDB,
		UploadDir:             cfg.Upload.Dir,
		LogDir:                cfg.LogDir,
		LogRetentionDays:      cfg.LogRetentionDays,
		StatusLogInterval:     time.Duration(cfg.StatusLogInterval),
		CacheBytes:            cfg.Cache.Bytes.Int64(),
		CacheMaxObjectBytes:   cfg.Cache.MaxObjectBytes.Int64(),
		StartedAt:             startedAt,
	}).HTTPHandler()
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
	exitCode = 0
	return exitCode
}

func setupLogging(cfg appconfig.Config) (io.WriteCloser, error) {
	writer, err := utils.NewDailyRotatingWriter(cfg.LogDir, cfg.LogRetentionDays)
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
