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
