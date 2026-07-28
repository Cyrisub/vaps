package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	metadataHash        = "vaps-iohash"
	metadataContentHash = "vaps-blake3"
	metadataSize        = "vaps-size"
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
	return parseInfo(hash, output.Metadata, aws.ToInt64(output.ContentLength))
}

func (s *S3Store) Put(ctx context.Context, info Info, reader io.Reader) error {
	if err := validateInfo(info); err != nil {
		return err
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s.key(info.Hash)),
		Body:          reader,
		ContentLength: aws.Int64(info.Size),
		Metadata: map[string]string{
			metadataHash:        info.Hash,
			metadataContentHash: info.ContentHash,
			metadataSize:        strconv.FormatInt(info.Size, 10),
		},
	})
	if err != nil {
		return fmt.Errorf("put s3 object %q: %w", info.Hash, err)
	}
	return nil
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
	info, err := parseInfo(hash, output.Metadata, aws.ToInt64(output.ContentLength))
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

func (s *S3Store) key(hash string) string {
	if s.prefix == "" {
		return hash
	}
	return s.prefix + "/" + hash
}

func parseInfo(hash string, metadata map[string]string, contentLength int64) (Info, error) {
	contentHash := metadata[metadataContentHash]
	if metadata[metadataHash] != hash || contentHash == "" {
		return Info{}, fmt.Errorf("%w: hash metadata is missing or invalid", ErrIntegrityMismatch)
	}
	size, err := strconv.ParseInt(metadata[metadataSize], 10, 64)
	if err != nil || size < 0 || size != contentLength {
		return Info{}, fmt.Errorf("%w: size metadata is missing or invalid", ErrIntegrityMismatch)
	}
	return Info{Hash: hash, ContentHash: contentHash, Size: size}, nil
}

func validateInfo(info Info) error {
	if info.Hash == "" || info.ContentHash == "" || info.Size < 0 {
		return errors.New("invalid object info")
	}
	return nil
}

func isNotFound(err error) bool {
	var responseError *smithyhttp.ResponseError
	return errors.As(err, &responseError) && responseError.HTTPStatusCode() == 404
}
