package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/light-speak/luxo/pkg/lux/storage"
	"github.com/minio/minio-go/v7"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ========== NewS3Storage validation ==========

func TestNewS3StorageMissingConfig(t *testing.T) {
	_, err := NewS3Storage(Config{})
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestNewS3StorageInvalidEndpoint(t *testing.T) {
	s, err := NewS3Storage(Config{
		Endpoint:  "http://127.0.0.1:9000/invalid/path",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "test",
	})
	if err == nil || s != nil || !strings.Contains(err.Error(), "init s3 client") {
		t.Fatalf("invalid endpoint must return an initialization error: storage=%v err=%v", s, err)
	}
}

func TestNewS3StorageStripProtocol(t *testing.T) {
	s, err := NewS3Storage(Config{
		Endpoint:  "https://minio:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// The explicit scheme is preserved even when UseSSL has its zero value.
	if s.client.EndpointURL().Scheme != "https" {
		t.Fatal("explicit HTTPS endpoint lost TLS")
	}
}

func TestNewS3StorageHttpProtocol(t *testing.T) {
	s, err := NewS3Storage(Config{
		Endpoint:  "http://minio:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
}

func TestNewS3StorageDefaultEndpoint(t *testing.T) {
	s, err := NewS3Storage(Config{
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if endpoint := s.client.EndpointURL(); endpoint.Scheme != "https" || endpoint.Host != "s3.amazonaws.com" {
		t.Fatalf("default AWS endpoint must use HTTPS: %s", endpoint)
	}
}

func TestS3RejectsInvalidBucketNames(t *testing.T) {
	config := Config{Endpoint: "http://127.0.0.1:9000", AccessKey: "test-access", SecretKey: "test-secret", Bucket: "test-bucket"}
	backend, err := NewS3Storage(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	for _, bucket := range []string{"../other", "bucket/name", "bucket?download=1", "bucket#fragment", "bucket\x00"} {
		t.Run(bucket, func(t *testing.T) {
			if value, err := backend.URL(t.Context(), bucket, "object"); err == nil || value != "" {
				t.Errorf("invalid bucket produced URL %q: %v", value, err)
			}
			invalid := config
			invalid.Bucket = bucket
			if value, err := NewS3Storage(invalid); err == nil || value != nil {
				if value != nil {
					_ = value.Close()
				}
				t.Fatalf("invalid default bucket accepted: %v", err)
			}
		})
	}
}

func TestS3URLRejectsMissingDefaultBucket(t *testing.T) {
	backend := &S3Storage{publicURL: "https://cdn.example"}
	if value, err := backend.URL(t.Context(), "", "object"); err == nil || value != "" {
		t.Fatalf("missing bucket produced URL %q: %v", value, err)
	}
}

func TestS3StorageBuildURLWithPublicURL(t *testing.T) {
	s := &S3Storage{
		bucket:    "test",
		publicURL: "https://cdn.example.com",
	}
	url := s.buildURL("test", "user/avatar/abc.png")
	if url != "https://cdn.example.com/user/avatar/abc.png" {
		t.Errorf("url = %q", url)
	}
}

func TestS3StorageBuildURLTrailingSlash(t *testing.T) {
	// PublicURL trailing slash is trimmed in constructor
	s, err := NewS3Storage(Config{
		Endpoint:  "minio:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "test",
		PublicURL: "https://cdn.example.com/",
	})
	if err != nil {
		t.Fatal(err)
	}
	url := s.buildURL("test", "k")
	if url != "https://cdn.example.com/k" {
		t.Errorf("url = %q, want https://cdn.example.com/k", url)
	}
}

func TestS3StorageClose(t *testing.T) {
	s, err := NewS3Storage(Config{
		Endpoint:  "minio:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestS3StorageURLDefaultBucket(t *testing.T) {
	s, err := NewS3Storage(Config{
		Endpoint:  "minio:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "default",
		PublicURL: "https://cdn.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	url, err := s.URL(context.Background(), "", "key")
	if err != nil {
		t.Fatal(err)
	}
	// Empty bucket should fallback to default
	if url != "https://cdn.example.com/key" {
		t.Errorf("url = %q", url)
	}
}

func TestS3StorageBuildURLFallback(t *testing.T) {
	// No publicURL — fallback to endpoint URL
	s, err := NewS3Storage(Config{
		Endpoint:  "minio:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	url := s.buildURL("mybucket", "user/avatar/abc.png")
	// Should contain bucket and key in fallback URL
	if !strings.Contains(url, "mybucket/user/avatar/abc.png") {
		t.Errorf("fallback url = %q, should contain bucket/key", url)
	}
}

func TestS3StorageURLWithExplicitBucket(t *testing.T) {
	s, err := NewS3Storage(Config{
		Endpoint:  "minio:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "default",
		PublicURL: "https://cdn.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	url, err := s.URL(context.Background(), "custom-bucket", "key")
	if err != nil {
		t.Fatal(err)
	}
	// custom-bucket differs from default, so bucket is included in path
	if url != "https://cdn.example.com/custom-bucket/key" {
		t.Errorf("url = %q", url)
	}
}

// ========== Mock s3Client for unit-testing S3Storage methods ==========

// mockS3Client implements s3Client for unit tests.
type mockS3Client struct {
	putObjectFn    func(ctx context.Context, bucket, key string, reader io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error)
	removeObjectFn func(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error
	bucketExistsFn func(ctx context.Context, bucket string) (bool, error)
	makeBucketFn   func(ctx context.Context, bucket string, opts minio.MakeBucketOptions) error
	endpointURL    *url.URL
}

func (m *mockS3Client) PutObject(ctx context.Context, bucket, key string, reader io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	return m.putObjectFn(ctx, bucket, key, reader, size, opts)
}

func (m *mockS3Client) RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error {
	return m.removeObjectFn(ctx, bucket, key, opts)
}

func (m *mockS3Client) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return m.bucketExistsFn(ctx, bucket)
}

func (m *mockS3Client) MakeBucket(ctx context.Context, bucket string, opts minio.MakeBucketOptions) error {
	return m.makeBucketFn(ctx, bucket, opts)
}

func (m *mockS3Client) EndpointURL() *url.URL {
	return m.endpointURL
}

// newTestS3Storage builds an S3Storage with a mock client.
func newTestS3Storage(mc *mockS3Client, bucket, publicURL string) *S3Storage {
	return &S3Storage{
		client:    mc,
		bucket:    bucket,
		publicURL: publicURL,
	}
}

// newTestS3StorageWithRegion builds an S3Storage with a mock client and region.
func newTestS3StorageWithRegion(mc *mockS3Client, bucket, publicURL, region string) *S3Storage {
	return &S3Storage{
		client:    mc,
		bucket:    bucket,
		region:    region,
		publicURL: publicURL,
	}
}

// ========== Upload tests ==========

func TestS3StorageUploadSuccess(t *testing.T) {
	mc := &mockS3Client{
		putObjectFn: func(_ context.Context, bucket, key string, _ io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
			return minio.UploadInfo{Bucket: bucket, Key: key}, nil
		},
		endpointURL: &url.URL{Scheme: "http", Host: "minio:9000"},
	}
	s := newTestS3Storage(mc, "default", "https://cdn.example.com")
	ctx := context.Background()

	u, err := s.Upload(ctx, "mybucket", "obj/key.txt", strings.NewReader("hello"), "text/plain", 5)
	if err != nil {
		t.Fatal(err)
	}
	// mybucket differs from default bucket, so bucket is included in path
	if u != "https://cdn.example.com/mybucket/obj/key.txt" {
		t.Errorf("url = %q", u)
	}
}

func TestS3StorageUploadError(t *testing.T) {
	mc := &mockS3Client{
		putObjectFn: func(_ context.Context, _, _ string, _ io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
			return minio.UploadInfo{}, fmt.Errorf("network down")
		},
	}
	s := newTestS3Storage(mc, "default", "")
	_, err := s.Upload(context.Background(), "b", "k", strings.NewReader(""), "", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network down") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestS3StorageUploadEmptyBucketFallback(t *testing.T) {
	var capturedBucket string
	mc := &mockS3Client{
		putObjectFn: func(_ context.Context, bucket, _ string, _ io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
			capturedBucket = bucket
			return minio.UploadInfo{}, nil
		},
		endpointURL: &url.URL{Scheme: "http", Host: "localhost:9000"},
	}
	s := newTestS3Storage(mc, "fallback-bucket", "https://cdn.example.com")

	_, err := s.Upload(context.Background(), "", "key.txt", strings.NewReader(""), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if capturedBucket != "fallback-bucket" {
		t.Errorf("bucket = %q, want %q", capturedBucket, "fallback-bucket")
	}
}

func TestS3StorageUploadEmptyContentType(t *testing.T) {
	mc := &mockS3Client{
		putObjectFn: func(_ context.Context, _, _ string, _ io.Reader, _ int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			if opts.ContentType != "" {
				t.Errorf("expected empty ContentType, got %q", opts.ContentType)
			}
			return minio.UploadInfo{}, nil
		},
		endpointURL: &url.URL{Scheme: "http", Host: "localhost:9000"},
	}
	s := newTestS3Storage(mc, "b", "https://cdn.example.com")
	_, _ = s.Upload(context.Background(), "b", "k", strings.NewReader(""), "", 0)
}

// ========== Delete tests ==========

func TestS3StorageDeleteSuccess(t *testing.T) {
	mc := &mockS3Client{
		removeObjectFn: func(_ context.Context, _, _ string, _ minio.RemoveObjectOptions) error {
			return nil
		},
	}
	s := newTestS3Storage(mc, "default", "")

	if err := s.Delete(context.Background(), "mybucket", "key"); err != nil {
		t.Fatal(err)
	}
}

func TestS3StorageDeleteError(t *testing.T) {
	mc := &mockS3Client{
		removeObjectFn: func(_ context.Context, _, _ string, _ minio.RemoveObjectOptions) error {
			return fmt.Errorf("permission denied")
		},
	}
	s := newTestS3Storage(mc, "default", "")

	err := s.Delete(context.Background(), "b", "k")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestS3StorageDeleteEmptyBucketFallback(t *testing.T) {
	var capturedBucket string
	mc := &mockS3Client{
		removeObjectFn: func(_ context.Context, bucket, _ string, _ minio.RemoveObjectOptions) error {
			capturedBucket = bucket
			return nil
		},
	}
	s := newTestS3Storage(mc, "fallback-bucket", "")

	if err := s.Delete(context.Background(), "", "key"); err != nil {
		t.Fatal(err)
	}
	if capturedBucket != "fallback-bucket" {
		t.Errorf("bucket = %q, want %q", capturedBucket, "fallback-bucket")
	}
}

// ========== EnsureBucket tests ==========

func TestS3StorageEnsureBucketExists(t *testing.T) {
	makeCalled := false
	mc := &mockS3Client{
		bucketExistsFn: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
		makeBucketFn: func(_ context.Context, _ string, _ minio.MakeBucketOptions) error {
			makeCalled = true
			return nil
		},
	}
	s := newTestS3Storage(mc, "default", "")

	if err := s.EnsureBucket(context.Background(), "existing"); err != nil {
		t.Fatal(err)
	}
	if makeCalled {
		t.Error("MakeBucket should not be called when bucket exists")
	}
}

func TestS3StorageEnsureBucketCreatesNew(t *testing.T) {
	var createdBucket string
	mc := &mockS3Client{
		bucketExistsFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		makeBucketFn: func(_ context.Context, bucket string, _ minio.MakeBucketOptions) error {
			createdBucket = bucket
			return nil
		},
	}
	s := newTestS3Storage(mc, "default", "")

	if err := s.EnsureBucket(context.Background(), "new-bucket"); err != nil {
		t.Fatal(err)
	}
	if createdBucket != "new-bucket" {
		t.Errorf("created bucket = %q, want %q", createdBucket, "new-bucket")
	}
}

func TestS3StorageEnsureBucketCheckError(t *testing.T) {
	mc := &mockS3Client{
		bucketExistsFn: func(_ context.Context, _ string) (bool, error) {
			return false, fmt.Errorf("network error")
		},
	}
	s := newTestS3Storage(mc, "default", "")

	err := s.EnsureBucket(context.Background(), "b")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network error") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestS3StorageEnsureBucketCreateError(t *testing.T) {
	mc := &mockS3Client{
		bucketExistsFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		makeBucketFn: func(_ context.Context, _ string, _ minio.MakeBucketOptions) error {
			return fmt.Errorf("access denied")
		},
	}
	s := newTestS3Storage(mc, "default", "")

	err := s.EnsureBucket(context.Background(), "b")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestS3StorageEnsureBucketEmptyFallback(t *testing.T) {
	var checkedBucket string
	mc := &mockS3Client{
		bucketExistsFn: func(_ context.Context, bucket string) (bool, error) {
			checkedBucket = bucket
			return true, nil
		},
		makeBucketFn: func(_ context.Context, _ string, _ minio.MakeBucketOptions) error {
			return nil
		},
	}
	s := newTestS3Storage(mc, "fallback-bucket", "")

	if err := s.EnsureBucket(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if checkedBucket != "fallback-bucket" {
		t.Errorf("checked bucket = %q, want %q", checkedBucket, "fallback-bucket")
	}
}

// ========== EnsureBucket region propagation ==========

func TestS3StorageEnsureBucketPassesRegion(t *testing.T) {
	var capturedRegion string
	mc := &mockS3Client{
		bucketExistsFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		makeBucketFn: func(_ context.Context, _ string, opts minio.MakeBucketOptions) error {
			capturedRegion = opts.Region
			return nil
		},
	}
	s := newTestS3StorageWithRegion(mc, "default", "", "ap-southeast-1")

	if err := s.EnsureBucket(context.Background(), "new-bucket"); err != nil {
		t.Fatal(err)
	}
	if capturedRegion != "ap-southeast-1" {
		t.Errorf("region = %q, want %q", capturedRegion, "ap-southeast-1")
	}
}

func TestS3StorageEnsureBucketEmptyRegion(t *testing.T) {
	var capturedRegion string
	mc := &mockS3Client{
		bucketExistsFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		makeBucketFn: func(_ context.Context, _ string, opts minio.MakeBucketOptions) error {
			capturedRegion = opts.Region
			return nil
		},
	}
	s := newTestS3Storage(mc, "default", "")

	if err := s.EnsureBucket(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if capturedRegion != "" {
		t.Errorf("region = %q, want empty", capturedRegion)
	}
}

// ========== buildURL with non-default bucket ==========

func TestS3StorageURLPreservesObjectKey(t *testing.T) {
	for _, public := range []string{"", "https://cdn.example.com/files"} {
		for _, bucket := range []string{"default", "other"} {
			for _, key := range []string{"folder/name?download=1#part", "folder/100% ready.txt", "目录/图像.png"} {
				s := newTestS3Storage(&mockS3Client{
					endpointURL: &url.URL{Scheme: "https", Host: "storage.example.com"},
				}, "default", public)
				got, err := s.URL(t.Context(), bucket, key)
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := url.Parse(got)
				if err != nil {
					t.Fatalf("invalid URL %q: %v", got, err)
				}
				prefix := "/" + bucket
				if public != "" {
					prefix = "/files"
					if bucket != "default" {
						prefix += "/" + bucket
					}
				}
				if parsed.Path != prefix+"/"+key || parsed.RawQuery != "" || parsed.Fragment != "" {
					t.Fatalf("object key changed in URL: %q -> %q", key, got)
				}
			}
		}
	}
}

func TestS3StorageBuildURLNonDefaultBucket(t *testing.T) {
	s := &S3Storage{
		bucket:    "default",
		publicURL: "https://cdn.example.com",
	}
	url := s.buildURL("other-bucket", "key.txt")
	if url != "https://cdn.example.com/other-bucket/key.txt" {
		t.Errorf("url = %q, want https://cdn.example.com/other-bucket/key.txt", url)
	}
}

func TestS3StorageBuildURLDefaultBucketNoPrefix(t *testing.T) {
	s := &S3Storage{
		bucket:    "mybucket",
		publicURL: "https://cdn.example.com",
	}
	url := s.buildURL("mybucket", "key.txt")
	if url != "https://cdn.example.com/key.txt" {
		t.Errorf("url = %q, want https://cdn.example.com/key.txt", url)
	}
}

func TestS3StorageBuildURLEmptyBucketNoPrefix(t *testing.T) {
	s := &S3Storage{
		bucket:    "default",
		publicURL: "https://cdn.example.com",
	}
	url := s.buildURL("", "key.txt")
	if url != "https://cdn.example.com/key.txt" {
		t.Errorf("url = %q, want https://cdn.example.com/key.txt", url)
	}
}

// ========== Integration tests (skipped without STORAGE_ENDPOINT) ==========

func TestS3IntegrationObjectURLRoundTrip(t *testing.T) {
	if os.Getenv("STORAGE_ENDPOINT") == "" {
		t.Skip("STORAGE_ENDPOINT not set, skipping integration test")
	}
	cfg := storage.ConfigFromEnv(os.Getenv)
	cfg.Bucket = fmt.Sprintf("luxo-url-%d", time.Now().UnixNano())
	cfg.PublicURL = ""
	s, err := NewS3Storage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := s.EnsureBucket(ctx, ""); err != nil {
		t.Fatal(err)
	}
	client := s.client.(*minio.Client)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := client.RemoveBucket(cleanup, cfg.Bucket); err != nil {
			t.Error(err)
		}
	})
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`, cfg.Bucket)
	if err := client.SetBucketPolicy(ctx, cfg.Bucket, policy); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"folder/name?download=1#part", "folder/100% ready.txt", "目录/图像.png"} {
		t.Run(key, func(t *testing.T) { checkS3ObjectURL(t, ctx, s, key) })
	}
}

func checkS3ObjectURL(t *testing.T, ctx context.Context, s *S3Storage, key string) {
	t.Helper()
	const payload = "exact object bytes"
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := s.Delete(cleanup, "", key); err != nil {
			t.Error(err)
		}
	})
	address, err := s.Upload(ctx, "", key, strings.NewReader(payload), "text/plain", int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(len(payload)+1)))
	if err != nil || response.StatusCode != http.StatusOK || string(body) != payload {
		t.Fatalf("URL resolved to another object: status=%d body=%q err=%v", response.StatusCode, body, err)
	}
}

func TestS3IntegrationUploadDeleteRoundTrip(t *testing.T) {
	if os.Getenv("STORAGE_ENDPOINT") == "" {
		t.Skip("STORAGE_ENDPOINT not set, skipping integration test")
	}
	cfg := storage.ConfigFromEnv(os.Getenv)
	s, err := NewS3Storage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Ensure bucket exists
	for range 2 {
		if err := s.EnsureBucket(ctx, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, data, contentType string
		size                    int64
	}{
		{"text", "hello world", "text/plain", 11},
		{"empty", "", "application/octet-stream", 0},
		{"binary", "\x00\xff\x01\x80", "application/octet-stream", 4},
		{"unicode", "中文内容", "text/plain; charset=utf-8", 12},
		{"stream", strings.Repeat("stream", 16384), "application/octet-stream", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkS3RoundTrip(t, ctx, s, tc.data, tc.contentType, tc.size)
		})
	}
}

func checkS3RoundTrip(t *testing.T, ctx context.Context, s *S3Storage, data, contentType string, size int64) {
	t.Helper()
	key, err := storage.GenerateKey("test", "file", "hello.bin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Delete(cleanupCtx, "", key); err != nil {
			t.Error("cleanup uploaded object", err)
		}
	})
	uploadURL, err := s.Upload(ctx, "", key, strings.NewReader(data), contentType, size)
	if err != nil {
		t.Fatal(err)
	}
	gotURL, err := s.URL(ctx, "", key)
	if err != nil || gotURL == "" || gotURL != uploadURL {
		t.Fatalf("URL=%q upload URL=%q err=%v", gotURL, uploadURL, err)
	}
	client := s.client.(*minio.Client)
	checkS3Object(t, ctx, client, s.bucket, key, data, contentType)
	if err := s.Delete(ctx, "", key); err != nil {
		t.Fatal(err)
	}
	_, err = client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if minio.ToErrorResponse(err).Code != "NoSuchKey" {
		t.Fatalf("deleted object must be absent, got %v", err)
	}
}

func checkS3Object(t *testing.T, ctx context.Context, client *minio.Client, bucket, key, data, contentType string) {
	t.Helper()
	object, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	got, err := io.ReadAll(io.LimitReader(object, int64(len(data))+1))
	if err != nil || !bytes.Equal(got, []byte(data)) {
		t.Fatalf("stored bytes differ: size=%d want=%d err=%v", len(got), len(data), err)
	}
	info, err := client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil || info.Size != int64(len(data)) || info.ContentType != contentType {
		t.Fatalf("stored metadata: %+v err=%v", info, err)
	}
}

// BenchmarkS3ObjectURL compares the previous unescaped URL with the corrected path.
// The previous implementation is not functionally equivalent for escaped keys.
func BenchmarkS3ObjectURL(b *testing.B) {
	for _, sample := range []struct{ name, key string }{
		{"plain", "user/avatar/01234567.png"},
		{"escaped", "folder/100% ready?download=1#part.txt"},
		{"unicode", "目录/图像.png"},
	} {
		for _, current := range []bool{false, true} {
			mode := "before"
			if current {
				mode = "current"
			}
			b.Run(sample.name+"/"+mode, func(b *testing.B) {
				s := &S3Storage{bucket: "test", publicURL: "https://cdn.example.com"}
				b.ReportAllocs()
				for b.Loop() {
					var result string
					if current {
						result = s.buildURL("test", sample.key)
					} else {
						result = s.publicURL + "/" + sample.key
					}
					if len(result) == 0 {
						b.Fatal("empty URL")
					}
				}
			})
		}
	}
}

func TestExplicitHTTPSEndpointDoesNotSendPlainHTTP(t *testing.T) {
	var plainHTTP atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHTTP.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	backend, err := NewS3Storage(Config{
		Endpoint: "https" + strings.TrimPrefix(server.URL, "http"),
		Region:   "us-east-1", AccessKey: "test-access", SecretKey: "test-secret",
		Bucket: "test-bucket",
	})
	// A conflicting configuration may be rejected, but it must never downgrade HTTPS.
	if err != nil {
		return
	}
	defer backend.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_ = backend.EnsureBucket(ctx, "")
	if plainHTTP.Load() {
		t.Fatal("explicit HTTPS endpoint sent a signed request over plain HTTP")
	}
	if backend.client.EndpointURL().Scheme != "https" {
		t.Fatal("explicit HTTPS endpoint was downgraded")
	}
}

func TestS3EndpointTransportSelection(t *testing.T) {
	for _, endpoint := range []string{"minio:9000", "http://minio:9000", "https://minio:9000", ""} {
		for _, useSSL := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ssl=%t", endpoint, useSSL), func(t *testing.T) {
				backend, err := NewS3Storage(Config{Endpoint: endpoint, UseSSL: useSSL,
					AccessKey: "test-access", SecretKey: "test-secret", Bucket: "test-bucket"})
				if err != nil {
					t.Fatal(err)
				}
				defer backend.Close()
				expected := "http"
				if useSSL || endpoint == "" || strings.HasPrefix(endpoint, "https://") {
					expected = "https"
				}
				if got := backend.client.EndpointURL().Scheme; got != expected {
					t.Fatalf("scheme = %s, want %s", got, expected)
				}
			})
		}
	}
}

func TestS3OperationsPropagateCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, *S3Storage) error
	}{
		{"upload", func(ctx context.Context, s *S3Storage) error {
			_, err := s.Upload(ctx, "", "object", strings.NewReader("payload"), "text/plain", 7)
			return err
		}},
		{"delete", func(ctx context.Context, s *S3Storage) error { return s.Delete(ctx, "", "object") }},
		{"bucket", func(ctx context.Context, s *S3Storage) error { return s.EnsureBucket(ctx, "") }},
	} {
		t.Run(tc.name, func(t *testing.T) { testS3Cancellation(t, tc.call) })
	}
}

func testS3Cancellation(t *testing.T, call func(context.Context, *S3Storage) error) {
	t.Helper()
	entered := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
			stopped <- struct{}{}
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	s, err := NewS3Storage(Config{Endpoint: server.URL, Region: "us-east-1",
		AccessKey: "test-access", SecretKey: "test-secret", Bucket: "test-bucket"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- call(ctx, s) }()
	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("request failed before reaching backend: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach backend")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("operation ignored cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("backend request remained active")
	}
}

func TestS3CloseReleasesIdleConnection(t *testing.T) {
	closed := make(chan struct{}, 2)
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	server.Start()
	defer server.Close()
	cfg := Config{Endpoint: server.URL, Region: "us-east-1", AccessKey: "key", SecretKey: "secret", Bucket: "test"}
	s, err := NewS3Storage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sibling, err := NewS3Storage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sibling.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := s.EnsureBucket(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := sibling.EnsureBucket(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 2 {
		t.Fatal("expected independent client connections")
	}
	select {
	case <-closed:
		t.Fatal("connection closed before storage.Close; fixture did not retain an idle connection")
	default:
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("storage.Close left its idle HTTP connection open")
	}
	if err := sibling.EnsureBucket(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 2 {
		t.Fatal("closing one storage discarded another storage connection")
	}
}

func TestS3TransportInitializationFailure(t *testing.T) {
	original := minio.DefaultTransport
	t.Cleanup(func() { minio.DefaultTransport = original })
	failure := errors.New("transport initialization failed")
	minio.DefaultTransport = func(bool) (*http.Transport, error) { return nil, failure }
	backend, err := NewS3Storage(Config{Endpoint: "localhost:9000", AccessKey: "key", SecretKey: "secret", Bucket: "test"})
	if backend != nil || !errors.Is(err, failure) {
		t.Fatalf("backend=%v err=%v", backend, err)
	}
}

func TestS3CloseWithoutTransport(t *testing.T) {
	if err := (&S3Storage{client: &mockS3Client{}}).Close(); err != nil {
		t.Fatal(err)
	}
}

type s3OwnedUploadReader struct {
	reader     io.Reader
	bytesRead  atomic.Int64
	activeRead atomic.Int32
	closes     atomic.Int32
}

func (r *s3OwnedUploadReader) Read(buffer []byte) (int, error) {
	r.activeRead.Add(1)
	defer r.activeRead.Add(-1)
	n, err := r.reader.Read(buffer)
	r.bytesRead.Add(int64(n))
	return n, err
}

func (r *s3OwnedUploadReader) Close() error {
	r.closes.Add(1)
	return nil
}

type s3UploadHTTPFixture struct {
	storage  *S3Storage
	server   *httptest.Server
	entered  chan struct{}
	done     chan struct{}
	closed   chan struct{}
	requests atomic.Int32
}

func newS3UploadHTTPFixture(t *testing.T, handler http.HandlerFunc) *s3UploadHTTPFixture {
	t.Helper()
	fixture := &s3UploadHTTPFixture{entered: make(chan struct{}), done: make(chan struct{}, 16), closed: make(chan struct{}, 16)}
	fixture.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fixture.requests.Add(1) == 1 {
			close(fixture.entered)
		}
		defer func() { fixture.done <- struct{}{} }()
		if r.Method != http.MethodPut || r.URL.Path != "/test-bucket/object" {
			t.Errorf("unexpected upload request: %s %s", r.Method, r.URL.Path)
		}
		handler(w, r)
	}))
	fixture.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed || state == http.StateHijacked {
			fixture.closed <- struct{}{}
		}
	}
	fixture.server.Start()
	t.Cleanup(fixture.server.Close)
	backend, err := NewS3Storage(Config{Endpoint: fixture.server.URL, Region: "us-east-1",
		AccessKey: "test-access", SecretKey: "test-secret", Bucket: "test-bucket"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.storage = backend
	t.Cleanup(func() { _ = backend.Close() })
	return fixture
}

func (f *s3UploadHTTPFixture) finish(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-f.done:
	case <-ctx.Done():
		t.Fatal("upload handler did not terminate")
	}
	if f.requests.Load() != 1 {
		t.Fatalf("upload made %d requests, expected one", f.requests.Load())
	}
	if err := f.storage.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.closed:
	case <-ctx.Done():
		t.Fatal("upload connection did not terminate")
	}
	f.server.Close()
	t.Logf("upload operation, handler, connection and listener terminated: %s", f.server.URL)
}

func assertS3UploadReaderOwned(t *testing.T, reader *s3OwnedUploadReader) {
	t.Helper()
	if reader.closes.Load() != 0 || reader.activeRead.Load() != 0 {
		t.Fatalf("caller reader was closed or remains active: closes=%d active=%d", reader.closes.Load(), reader.activeRead.Load())
	}
	if err := reader.Close(); err != nil || reader.closes.Load() != 1 {
		t.Fatalf("caller could not close its reader exactly once: err=%v closes=%d", err, reader.closes.Load())
	}
}

func TestS3UploadRetainsCallerReaderOwnership(t *testing.T) {
	for _, name := range []string{"Reader", "ReadCloser"} {
		t.Run(name, func(t *testing.T) {
			fixture := newS3UploadHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
				}
				w.Header().Set("ETag", `"test-etag"`)
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			const payload = "caller-owned upload bytes"
			reader := &s3OwnedUploadReader{reader: strings.NewReader(payload)}
			var input io.Reader = reader
			if name == "Reader" {
				input = struct{ io.Reader }{reader}
			}
			address, err := fixture.storage.Upload(ctx, "", "object", input, "text/plain", int64(len(payload)))
			if err != nil || address != fixture.server.URL+"/test-bucket/object" || reader.bytesRead.Load() != int64(len(payload)) {
				t.Fatalf("upload = %q, %v, read=%d", address, err, reader.bytesRead.Load())
			}
			fixture.finish(t, ctx)
			assertS3UploadReaderOwned(t, reader)
		})
	}
}

type s3UploadFailingReader struct {
	prefix  *strings.Reader
	entered <-chan struct{}
	ctx     context.Context
	failure error
}

func (r *s3UploadFailingReader) Read(buffer []byte) (int, error) {
	if r.prefix.Len() > 0 {
		return r.prefix.Read(buffer)
	}
	select {
	case <-r.entered:
		return 0, r.failure
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}

func TestS3UploadReaderErrorPreservesCauseAndOwnership(t *testing.T) {
	fixture := newS3UploadHTTPFixture(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	failure := errors.New("upload reader failed after its prefix")
	const prefix = "partial payload"
	reader := &s3OwnedUploadReader{reader: &s3UploadFailingReader{
		prefix: strings.NewReader(prefix), entered: fixture.entered, ctx: ctx, failure: failure,
	}}
	address, err := fixture.storage.Upload(ctx, "", "object", reader, "text/plain", int64(len(prefix)+64))
	if address != "" || !errors.Is(err, failure) || reader.bytesRead.Load() != int64(len(prefix)) {
		t.Fatalf("upload error = %q, %v, read=%d", address, err, reader.bytesRead.Load())
	}
	fixture.finish(t, ctx)
	assertS3UploadReaderOwned(t, reader)
}

func TestS3UploadEarlyHTTPResponsePreservesOwnership(t *testing.T) {
	fixture := newS3UploadHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		connection, output, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := output.WriteString("HTTP/1.1 200 OK\r\nETag: \"unfinished"); err != nil {
			t.Error(err)
		}
		if err := output.Flush(); err != nil {
			t.Error(err)
		}
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const payload = "complete request before the incomplete response"
	reader := &s3OwnedUploadReader{reader: strings.NewReader(payload)}
	address, err := fixture.storage.Upload(ctx, "", "object", reader, "text/plain", int64(len(payload)))
	if address != "" || err == nil || ctx.Err() != nil || reader.bytesRead.Load() != int64(len(payload)) {
		t.Fatalf("truncated HTTP response accepted: address=%q err=%v read=%d", address, err, reader.bytesRead.Load())
	}
	fixture.finish(t, ctx)
	assertS3UploadReaderOwned(t, reader)
}
