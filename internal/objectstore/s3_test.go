package objectstore

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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
	info, err := parseInfo(hash, "etag-1", map[string]string{
		metadataHash:     hash,
		metadataChecksum: "9a71bb4c",
		metadataSize:     "5",
	}, 5)
	if err != nil {
		t.Fatalf("parseInfo returned error: %v", err)
	}
	if info.Hash != hash || info.ETag != "etag-1" || info.Checksum != "9a71bb4c" || info.Size != 5 {
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
			info, err := parseInfo(hash, "", values, 5)
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

func TestS3ListPaginatesAndNormalizesETag(t *testing.T) {
	const (
		firstHash  = "0123456789abcdef0123456789abcdef01234567"
		secondHash = "fedcba9876543210fedcba9876543210fedcba98"
	)
	responses := []string{
		`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Name>bucket</Name><Prefix>Payload/</Prefix><KeyCount>1</KeyCount><MaxKeys>1</MaxKeys>
<IsTruncated>true</IsTruncated><NextContinuationToken>next-page</NextContinuationToken>
<Contents><Key>Payload/` + firstHash + `</Key><ETag>"etag-1"</ETag><Size>5</Size></Contents>
</ListBucketResult>`,
		`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Name>bucket</Name><Prefix>Payload/</Prefix><KeyCount>1</KeyCount><MaxKeys>1</MaxKeys>
<IsTruncated>false</IsTruncated>
<Contents><Key>Payload/` + secondHash + `</Key><ETag>"etag-2"</ETag><Size>7</Size></Contents>
</ListBucketResult>`,
	}
	client := s3.NewFromConfig(aws.Config{
		Region: "us-east-1",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			body := responses[0]
			responses = responses[1:]
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})},
	}, func(options *s3.Options) {
		options.BaseEndpoint = aws.String("https://s3.example")
		options.UsePathStyle = true
	})
	store, err := NewS3WithClient(client, S3Config{Bucket: "bucket", Prefix: "Payload"})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(objects) != 2 ||
		objects[0].Hash != firstHash || objects[0].ETag != "etag-1" ||
		objects[1].Hash != secondHash || objects[1].ETag != "etag-2" {
		t.Fatalf("List() = %#v", objects)
	}
}

func TestS3StatsDoesNotBlockOnListing(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	store := newTestS3Store(t, func(_ *http.Request) (*http.Response, error) {
		startOnce.Do(func() { close(started) })
		<-release
		return listObjectsResponse(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Name>bucket</Name><Prefix>Payload/</Prefix><KeyCount>1</KeyCount><MaxKeys>1</MaxKeys>
<IsTruncated>false</IsTruncated>
<Contents><Key>Payload/` + hash + `</Key><ETag>"etag-1"</ETag><Size>5</Size></Contents>
</ListBucketResult>`)
	})

	done := make(chan struct{})
	var stats ObjectStats
	var err error
	go func() {
		stats, err = store.Stats(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stats blocked on COS listing")
	}
	if err != nil || !stats.Pending || stats.ObjectCount != 0 || stats.TotalBytes != 0 {
		t.Fatalf("Stats() = %#v, %v, want pending empty stats", stats, err)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("COS listing did not start")
	}
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats, err = store.Stats(context.Background())
		if err == nil && !stats.Pending && stats.ObjectCount == 1 && stats.TotalBytes == 5 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Stats() = %#v, %v, want populated cache", stats, err)
}

func newTestS3Store(t *testing.T, roundTrip roundTripFunc) *S3Store {
	t.Helper()
	client := s3.NewFromConfig(aws.Config{
		Region:     "us-east-1",
		HTTPClient: &http.Client{Transport: roundTrip},
	}, func(options *s3.Options) {
		options.BaseEndpoint = aws.String("https://s3.example")
		options.UsePathStyle = true
	})
	store, err := NewS3WithClient(client, S3Config{Bucket: "bucket", Prefix: "Payload"})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func listObjectsResponse(body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
