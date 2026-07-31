package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"vaps/internal/utils"
)

const (
	metadataHash       = "vaps-iohash"
	metadataChecksum   = "vaps-checksum"
	metadataLegacyHash = "vaps-blake3"
	metadataSize       = "vaps-size"
)

// S3Config configures an AWS S3 or S3-compatible object store.
type S3Config struct {
	Bucket         string
	Region         string
	Endpoint       string
	Prefix         string
	ForcePathStyle bool
	Credentials    S3Credentials
}

// S3Store stores each payload at <prefix>/<iohash>.
type S3Store struct {
	client *s3.Client
	bucket string
	prefix string

	statsMu       sync.Mutex
	statsCache    ObjectStats
	statsCachedAt time.Time
}

func NewS3(ctx context.Context, cfg S3Config) (*S3Store, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("s3 bucket must not be empty")
	}
	if strings.TrimSpace(cfg.Region) == "" {
		return nil, errors.New("s3 region must not be empty")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("s3 endpoint must not be empty")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) ||
		parsed.Hostname() == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, errors.New("s3 endpoint must be a valid http(s) URL")
	}
	secretID := strings.TrimSpace(cfg.Credentials.SecretID)
	if secretID == "" || strings.TrimSpace(cfg.Credentials.SecretKey) == "" {
		return nil, errors.New("s3 credentials must not be empty")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load aws configuration: %w", err)
	}
	awsCfg.Credentials = awscredentials.NewStaticCredentialsProvider(
		secretID,
		strings.TrimSpace(cfg.Credentials.SecretKey),
		"",
	)
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = cfg.ForcePathStyle
		options.BaseEndpoint = aws.String(endpoint)
	})
	return NewS3WithClient(client, cfg)
}

// NewS3WithClient is exposed for tests and custom credential wiring.
func NewS3WithClient(client *s3.Client, cfg S3Config) (*S3Store, error) {
	if client == nil {
		return nil, errors.New("s3 client must not be nil")
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("s3 bucket must not be empty")
	}
	return &S3Store{
		client: client,
		bucket: strings.TrimSpace(cfg.Bucket),
		prefix: strings.Trim(strings.TrimSpace(cfg.Prefix), "/"),
	}, nil
}

func (s *S3Store) Head(ctx context.Context, hash string) (Info, error) {
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key(hash)),
	})
	if err != nil {
		if isNotFound(err) {
			return Info{}, ErrNotFound
		}
		return Info{}, fmt.Errorf("head s3 object %q: %w", hash, err)
	}
	return parseInfo(hash, normalizeETag(aws.ToString(output.ETag)), output.Metadata, aws.ToInt64(output.ContentLength))
}

func (s *S3Store) Put(ctx context.Context, info Info, reader io.Reader) (Info, error) {
	if err := validateInfo(info); err != nil {
		return Info{}, err
	}
	output, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s.key(info.Hash)),
		Body:          reader,
		ContentLength: aws.Int64(info.Size),
		Metadata: map[string]string{
			metadataHash:     info.Hash,
			metadataChecksum: info.Checksum,
			metadataSize:     strconv.FormatInt(info.Size, 10),
		},
	})
	if err != nil {
		return Info{}, fmt.Errorf("put s3 object %q: %w", info.Hash, err)
	}
	info.ETag = normalizeETag(aws.ToString(output.ETag))
	return info, nil
}

func (s *S3Store) Open(ctx context.Context, hash string) (io.ReadCloser, Info, error) {
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key(hash)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, Info{}, ErrNotFound
		}
		return nil, Info{}, fmt.Errorf("get s3 object %q: %w", hash, err)
	}
	info, err := parseInfo(hash, normalizeETag(aws.ToString(output.ETag)), output.Metadata, aws.ToInt64(output.ContentLength))
	if err != nil {
		_ = output.Body.Close()
		return nil, Info{}, err
	}
	return output.Body, info, nil
}

// OpenRange streams an inclusive byte range. The returned Info always
// describes the full object, not the selected range.
func (s *S3Store) OpenRange(ctx context.Context, hash string, start, end int64) (io.ReadCloser, Info, error) {
	if start < 0 || end < start {
		return nil, Info{}, errors.New("invalid object range")
	}
	info, err := s.Head(ctx, hash)
	if err != nil {
		return nil, Info{}, err
	}
	if end >= info.Size {
		return nil, Info{}, errors.New("object range exceeds size")
	}
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key(hash)),
		Range:  aws.String(fmt.Sprintf("bytes=%d-%d", start, end)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, Info{}, ErrNotFound
		}
		return nil, Info{}, fmt.Errorf("get s3 object range %q: %w", hash, err)
	}
	return output.Body, info, nil
}

func (s *S3Store) List(ctx context.Context) ([]Info, error) {
	var objects []Info
	var continuationToken *string
	prefix := s.objectPrefix()
	for {
		output, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("list s3 objects with prefix %q: %w", prefix, err)
		}
		for _, object := range output.Contents {
			key := aws.ToString(object.Key)
			hash := strings.TrimPrefix(key, prefix)
			if hash == "" || strings.Contains(hash, "/") {
				continue
			}
			objects = append(objects, Info{
				Hash: hash,
				ETag: normalizeETag(aws.ToString(object.ETag)),
				Size: aws.ToInt64(object.Size),
			})
		}
		if !aws.ToBool(output.IsTruncated) {
			return objects, nil
		}
		if output.NextContinuationToken == nil || aws.ToString(output.NextContinuationToken) == "" {
			return nil, errors.New("list s3 objects returned no continuation token")
		}
		continuationToken = output.NextContinuationToken
	}
}

func (s *S3Store) Stats(ctx context.Context) (ObjectStats, error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if !s.statsCachedAt.IsZero() && time.Since(s.statsCachedAt) < 30*time.Second {
		return s.statsCache, nil
	}

	var stats ObjectStats
	var continuationToken *string
	prefix := s.objectPrefix()
	for {
		output, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return ObjectStats{}, fmt.Errorf("list s3 objects with prefix %q: %w", prefix, err)
		}
		for _, object := range output.Contents {
			stats.ObjectCount++
			stats.TotalBytes += aws.ToInt64(object.Size)
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		if output.NextContinuationToken == nil || aws.ToString(output.NextContinuationToken) == "" {
			return ObjectStats{}, errors.New("list s3 objects returned no continuation token")
		}
		continuationToken = output.NextContinuationToken
	}
	s.statsCache = stats
	s.statsCachedAt = time.Now()
	return stats, nil
}

func (s *S3Store) key(hash string) string {
	return s.objectPrefix() + hash
}

func (s *S3Store) objectPrefix() string {
	if s.prefix == "" {
		return ""
	}
	return s.prefix + "/"
}

func parseInfo(hash, etag string, metadata map[string]string, contentLength int64) (Info, error) {
	checksum := metadata[metadataChecksum]
	if metadata[metadataHash] != hash {
		return Info{}, fmt.Errorf("%w: hash metadata is missing or invalid", ErrIntegrityMismatch)
	}
	size, err := strconv.ParseInt(metadata[metadataSize], 10, 64)
	if err != nil || size < 0 || size != contentLength {
		return Info{}, fmt.Errorf("%w: size metadata is missing or invalid", ErrIntegrityMismatch)
	}
	if utils.ChecksumValid(checksum) {
		return Info{Hash: hash, ETag: etag, Checksum: checksum, Size: size}, nil
	}
	legacyChecksum := metadata[metadataLegacyHash]
	if legacyChecksum == "" {
		return Info{}, fmt.Errorf("%w: checksum metadata is missing or invalid", ErrIntegrityMismatch)
	}
	return Info{Hash: hash, ETag: etag, LegacyChecksum: legacyChecksum, Size: size}, nil
}

func normalizeETag(etag string) string {
	return strings.Trim(etag, `"`)
}

func validateInfo(info Info) error {
	if !utils.IoHashValid(info.Hash) || !utils.ChecksumValid(info.Checksum) || info.Size < 0 {
		return errors.New("invalid object info")
	}
	return nil
}

func isNotFound(err error) bool {
	var responseError *smithyhttp.ResponseError
	return errors.As(err, &responseError) && responseError.HTTPStatusCode() == 404
}
