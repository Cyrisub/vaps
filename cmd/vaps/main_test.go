package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"vaps/internal/backup"
	"vaps/internal/metadata"
)

const mergeBackupHash = "ea8f163db38682925e4491c5e58d4bb3506ef8c1"

func TestMergeBackupMetadataCreatesBackupOnlyRecord(t *testing.T) {
	meta := openMainMetadata(t)
	backend := &fakeMainBackup{objects: []backup.Object{{Hash: mergeBackupHash, Path: mainMetadataPath(mergeBackupHash)}}}

	if err := mergeBackupMetadata(context.Background(), meta, backend); err != nil {
		t.Fatalf("mergeBackupMetadata returned error: %v", err)
	}
	record, err := meta.GetPayload(mergeBackupHash)
	if err != nil {
		t.Fatalf("GetPayload returned error: %v", err)
	}
	if record.Status != metadata.StatusBackup {
		t.Fatalf("Status = %v, want backup only", record.Status)
	}
	if record.BackupStatus != metadata.Backuped {
		t.Fatalf("BackupStatus = %v, want backuped", record.BackupStatus)
	}
	if record.Size != 0 {
		t.Fatalf("Size = %d, want unknown zero size", record.Size)
	}
}

func TestMergeBackupMetadataPreservesExistingLocalRecord(t *testing.T) {
	meta := openMainMetadata(t)
	createdAt := metadataTime(123)
	if err := meta.PutPayload(metadata.Payload{Hash: mergeBackupHash, Size: 5, Status: metadata.StatusLocal, CreatedAt: createdAt}); err != nil {
		t.Fatalf("PutPayload returned error: %v", err)
	}
	backend := &fakeMainBackup{objects: []backup.Object{{Hash: mergeBackupHash, Path: mainMetadataPath(mergeBackupHash)}}}

	if err := mergeBackupMetadata(context.Background(), meta, backend); err != nil {
		t.Fatalf("mergeBackupMetadata returned error: %v", err)
	}
	record, err := meta.GetPayload(mergeBackupHash)
	if err != nil {
		t.Fatalf("GetPayload returned error: %v", err)
	}
	if !record.Status.HasLocal() || !record.Status.HasBackup() {
		t.Fatalf("Status = %v, want local and backup", record.Status)
	}
	if record.Size != 5 {
		t.Fatalf("Size = %d, want preserved local size", record.Size)
	}
}

func openMainMetadata(t *testing.T) *metadata.Store {
	t.Helper()
	store, err := metadata.Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func metadataTime(unix int64) *time.Time {
	value := time.Unix(unix, 0).UTC()
	return &value
}

func mainMetadataPath(hash string) string {
	return hash[:2] + "/" + hash[2:4] + "/" + hash[4:6] + "/" + hash[6:] + ".upayload"
}

type fakeMainBackup struct {
	objects []backup.Object
}

func (f *fakeMainBackup) Name() string { return "fake" }

func (f *fakeMainBackup) Exists(context.Context, string) (bool, error) { return false, nil }

func (f *fakeMainBackup) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeMainBackup) Put(context.Context, string, io.Reader) (backup.Object, error) {
	return backup.Object{}, nil
}

func (f *fakeMainBackup) List(context.Context) ([]backup.Object, error) {
	return append([]backup.Object(nil), f.objects...), nil
}
