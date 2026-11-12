package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"vaps/internal/appconfig"
	"vaps/internal/httpapi"
	"vaps/internal/objectstore"
)

func openPersistentStorage(ctx context.Context, cfg appconfig.Config, baseDir string) (objectstore.Store, error) {
	backends := cfg.Storage.Backends
	legacy := len(backends) == 0
	if legacy {
		backends = []appconfig.BackendConfig{{Name: "s3", Type: "s3", S3: cfg.Storage.S3}}
	}
	var credentials objectstore.S3Credentials
	for _, backend := range backends {
		if backend.Type == "s3" {
			var err error
			credentials, err = objectstore.LoadS3Credentials(baseDir)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	stores := make([]objectstore.Backend, 0, len(backends))
	for _, backend := range backends {
		var store objectstore.Store
		var err error
		switch backend.Type {
		case "s3":
			store, err = objectstore.NewS3(ctx, objectstore.S3Config{
				Bucket: backend.S3.Bucket, Region: backend.S3.Region, Endpoint: backend.S3.Endpoint,
				Prefix: backend.S3.Prefix, ForcePathStyle: backend.S3.ForcePathStyle, Credentials: credentials,
			})
		case "filesystem":
			store, err = objectstore.NewFilesystem(backend.Dir, backend.MountSource)
		default:
			return nil, fmt.Errorf("unsupported backend %s", backend.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("initialize backend %s: %w", backend.Name, err)
		}
		log.Printf("persistent backend initialized name=%s type=%s", backend.Name, backend.Type)
		stores = append(stores, objectstore.Backend{Name: backend.Name, Store: store})
	}
	if legacy {
		return stores[0].Store, nil
	}
	if len(stores) > 1 {
		return objectstore.NewAsync(ctx, stores, cfg.Storage.ReplicationDir, cfg.Storage.ReplicationWorkers, time.Duration(cfg.Storage.ReplicationRetryInterval))
	}
	return objectstore.NewMulti(stores)
}

func storageBackendInfo(cfg appconfig.Config) []httpapi.StorageBackendInfo {
	var result []httpapi.StorageBackendInfo
	for _, b := range cfg.Storage.Backends {
		location := b.Dir
		if b.Type == "s3" {
			location = b.S3.Endpoint + "/" + b.S3.Bucket + "/" + b.S3.Prefix
		}
		result = append(result, httpapi.StorageBackendInfo{Name: b.Name, Type: b.Type, Location: location})
	}
	return result
}
