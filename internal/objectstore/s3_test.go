package objectstore

import (
	"context"
	"strings"
	"testing"
)

func TestNewS3RejectsInvalidEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"",
		"localhost:9000",
		"ftp://minio.example",
		"https://",
		"https://minio.example?access_key=test",
	} {
		_, err := NewS3(context.Background(), S3Config{
			Bucket:   "test-bucket",
			Region:   "us-east-1",
			Endpoint: endpoint,
		})
		if err == nil || !strings.Contains(err.Error(), "s3 endpoint") {
			t.Fatalf("endpoint %q error = %v, want invalid endpoint error", endpoint, err)
		}
	}
}

func TestNewS3RejectsMissingCredentials(t *testing.T) {
	_, err := NewS3(context.Background(), S3Config{
		Bucket:   "test-bucket",
		Region:   "us-east-1",
		Endpoint: "https://s3.amazonaws.com",
	})
	if err == nil || !strings.Contains(err.Error(), "s3 credentials") {
		t.Fatalf("error = %v, want missing credentials error", err)
	}
}

func TestParseInfoUsesCRC32CChecksumMetadata(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	info, err := parseInfo(hash, map[string]string{
		metadataHash:     hash,
		metadataChecksum: "9a71bb4c",
		metadataSize:     "5",
	}, 5)
	if err != nil {
		t.Fatalf("parseInfo returned error: %v", err)
	}
	if info.Hash != hash || info.Checksum != "9a71bb4c" || info.Size != 5 {
		t.Fatalf("parseInfo = %#v", info)
	}
}

func TestParseInfoAcceptsLegacyOrRejectsInvalidChecksumMetadata(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	for name, metadata := range map[string]string{
		"legacy":  "vaps-blake3",
		"invalid": metadataChecksum,
	} {
		t.Run(name, func(t *testing.T) {
			values := map[string]string{
				metadataHash: hash,
				metadataSize: "5",
			}
			if name == "legacy" {
				values[metadata] = "full-blake3"
			} else {
				values[metadata] = "not-a-checksum"
			}
			info, err := parseInfo(hash, values, 5)
			if name == "legacy" {
				if err != nil || info.LegacyChecksum != "full-blake3" {
					t.Fatalf("parseInfo legacy = %#v, %v", info, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseInfo accepted invalid %s metadata", name)
			}
		})
	}
}
