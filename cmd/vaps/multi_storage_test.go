package main

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vaps/internal/appconfig"
	"vaps/internal/metadata"
	"vaps/internal/objectstore"
)

func TestFilesystemOnlyFactoryAndStartupFallback(t *testing.T) {
	root := t.TempDir()
	cfg := appconfig.Config{Storage: appconfig.StorageConfig{ReplicationDir: filepath.Join(root, "queue"), ReplicationWorkers: 1, ReplicationRetryInterval: appconfig.Duration(time.Second), Backends: []appconfig.BackendConfig{
		{Name: "disk", Type: "filesystem", Dir: filepath.Join(root, "one")},
		{Name: "smb", Type: "filesystem", Dir: filepath.Join(root, "two")},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	objects, err := openPersistentStorage(ctx, cfg, root)
	if err != nil {
		t.Fatalf("filesystem-only factory required S3 credentials: %v", err)
	}
	defer func() { cancel(); objects.(*objectstore.AsyncStore).Wait() }()
	data := []byte("startup fallback payload")
	info := objectstore.Info{Hash: strings.Repeat("ab", 20), Checksum: fmt.Sprintf("%08x", crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli))), Size: int64(len(data))}
	if _, err := objects.Put(context.Background(), info, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	meta, err := metadata.Open(filepath.Join(root, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	if err := meta.PutPayload(metadata.Payload{Hash: info.Hash, Checksum: info.Checksum, Size: info.Size, ETag: "different-provider-etag", ETagBackend: "s3"}); err != nil {
		t.Fatal(err)
	}
	if err := validateMetadataPayloads(context.Background(), meta, objects); err != nil {
		t.Fatalf("cross-backend startup validation: %v", err)
	}
	record, err := meta.GetPayload(info.Hash)
	if err != nil || record.ETagBackend != "disk" || !strings.HasPrefix(record.ETag, "sha256:") {
		t.Fatalf("startup ETag backfill = %+v, %v", record, err)
	}
	record.RecordETag("disk", "changed-on-same-backend")
	if err := meta.PutPayload(record); err != nil {
		t.Fatal(err)
	}
	if err := validateMetadataPayloads(context.Background(), meta, objects); err == nil {
		t.Fatal("ignored same-backend ETag mismatch")
	}
}
