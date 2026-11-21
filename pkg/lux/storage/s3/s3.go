package s3

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/s3utils"
)

// Config uses the portable storage configuration without a runtime conversion.
type Config = storage.Config

var _ storage.Storage = (*S3Storage)(nil)

// s3Client abstracts the minio.Client methods used by S3Storage,
// enabling mock-based unit tests without a real S3 connection.
type s3Client interface {
	PutObject(ctx context.Context, bucket, key string, reader io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error)
	RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error
	BucketExists(ctx context.Context, bucket string) (bool, error)
	MakeBucket(ctx context.Context, bucket string, opts minio.MakeBucketOptions) error
	EndpointURL() *url.URL
}

// S3Storage implements Storage using any S3-compatible backend
// (AWS S3, MinIO, Cloudflare R2, Aliyun OSS, etc.).
type S3Storage struct {
	transport *http.Transport
	client    s3Client
	bucket    string
	region    string // region for bucket creation
	publicURL string // base URL for public access (no trailing slash)
}

// NewS3Storage creates a new S3-compatible storage client.
func NewS3Storage(cfg Config) (*S3Storage, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := s3utils.CheckValidBucketName(cfg.Bucket); err != nil {
		return nil, err
	}

	endpoint := cfg.Endpoint
	secure := cfg.UseSSL || endpoint == "" || strings.HasPrefix(endpoint, "https://")
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	// Default AWS and explicit HTTPS endpoints must never use plaintext.
	// Strip protocol prefix if present
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")

	transport, err := minio.DefaultTransport(secure)
	if err != nil {
		return nil, fmt.Errorf("storage: init s3 transport: %w", err)
	}
	client, err := minio.New(endpoint, &minio.Options{
		Transport: transport,
		Creds:     credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:    secure,
		Region:    cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: init s3 client: %w", err)
	}

	publicURL := strings.TrimRight(cfg.PublicURL, "/")

	return &S3Storage{
		transport: transport,
		client:    client,
		bucket:    cfg.Bucket,
		region:    cfg.Region,
		publicURL: publicURL,
	}, nil
}

// Upload stores a file and returns its public URL.
func (s *S3Storage) Upload(ctx context.Context, bucket, key string, data io.Reader, contentType string, size int64) (string, error) {
	if bucket == "" {
		bucket = s.bucket
	}

	opts := minio.PutObjectOptions{}
	if contentType != "" {
		opts.ContentType = contentType
	}

	_, err := s.client.PutObject(ctx, bucket, key, data, size, opts)
	if err != nil {
		return "", fmt.Errorf("storage: upload %s/%s: %w", bucket, key, err)
	}

	url := s.buildURL(bucket, key)
	return url, nil
}

// Delete removes a file by bucket and key.
func (s *S3Storage) Delete(ctx context.Context, bucket, key string) error {
	if bucket == "" {
		bucket = s.bucket
	}

	err := s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("storage: delete %s/%s: %w", bucket, key, err)
	}
	return nil
}

// URL returns the public URL for a file.
func (s *S3Storage) URL(_ context.Context, bucket, key string) (string, error) {
	if bucket == "" {
		bucket = s.bucket
	}
	// The constructor validates the default; validate explicit alternatives before
	// formatting a path. Otherwise a bucket could become a query or another path.
	if bucket != s.bucket || bucket == "" {
		if err := s3utils.CheckValidBucketName(bucket); err != nil {
			return "", err
		}
	}
	return s.buildURL(bucket, key), nil
}

// Close releases idle connections owned by this storage instance.
func (s *S3Storage) Close() error {
	if s.transport != nil {
		s.transport.CloseIdleConnections()
	}
	return nil
}

// EnsureBucket creates the bucket if it does not exist.
func (s *S3Storage) EnsureBucket(ctx context.Context, bucket string) error {
	if bucket == "" {
		bucket = s.bucket
	}
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("storage: check bucket %s: %w", bucket, err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
			return fmt.Errorf("storage: create bucket %s: %w", bucket, err)
		}
	}
	return nil
}

// buildURL constructs the public URL for an object.
// When publicURL is set and bucket differs from the default, the bucket is
// included in the path so that multi-bucket setups resolve correctly.
func (s *S3Storage) buildURL(bucket, key string) string {
	// Escape path data without cleaning separators or changing the object key.
	key = (&url.URL{Path: key}).EscapedPath()
	if s.publicURL != "" {
		if bucket != "" && bucket != s.bucket {
			return s.publicURL + "/" + bucket + "/" + key
		}
		return s.publicURL + "/" + key
	}
	// Fallback: use endpoint URL from minio client
	return s.client.EndpointURL().String() + "/" + bucket + "/" + key
}
