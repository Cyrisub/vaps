package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"vaps/internal/metadata"
	"vaps/internal/objectstore"
)

func TestValidateMetadataPayloads(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	payload := metadata.Payload{
		Hash:     hash,
		Checksum: "01020304",
		Size:     12,
	}

	tests := []struct {
		name     string
		headInfo objectstore.Info
		headErr  error
		wantErr  string
	}{
		{
			name: "matching object",
			headInfo: objectstore.Info{
				Hash:     hash,
				Checksum: "01020304",
				Size:     12,
			},
		},
		{
			name:    "missing object",
			headErr: objectstore.ErrNotFound,
			wantErr: "missing from S3",
		},
		{
			name: "mismatched hash",
			headInfo: objectstore.Info{
				Hash:     "fedcba9876543210fedcba9876543210fedcba98",
				Checksum: "01020304",
				Size:     12,
			},
			wantErr: "does not match S3 object hash",
		},
		{
			name: "mismatched checksum",
			headInfo: objectstore.Info{
				Hash:     hash,
				Checksum: "05060708",
				Size:     12,
			},
			wantErr: "does not match S3 object checksum",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta, err := metadata.Open(t.TempDir() + "/metadata.db")
			if err != nil {
				t.Fatal(err)
			}
			defer meta.Close()
			if err := meta.PutPayload(payload); err != nil {
				t.Fatal(err)
			}

			objects := &startupCheckObjectStore{
				headInfo: test.headInfo,
				headErr:  test.headErr,
			}
			err = validateMetadataPayloads(context.Background(), meta, objects)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateMetadataPayloads() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateMetadataPayloads() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

type startupCheckObjectStore struct {
	headInfo objectstore.Info
	headErr  error
}

func (s *startupCheckObjectStore) Head(context.Context, string) (objectstore.Info, error) {
	return s.headInfo, s.headErr
}

func (s *startupCheckObjectStore) Put(context.Context, objectstore.Info, io.Reader) error {
	return nil
}

func (s *startupCheckObjectStore) Open(context.Context, string) (io.ReadCloser, objectstore.Info, error) {
	return nil, objectstore.Info{}, errors.New("not implemented")
}
