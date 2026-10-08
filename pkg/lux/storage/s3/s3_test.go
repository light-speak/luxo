package s3

import (
	"bytes"
	"context"
	"fmt"
	"github.com/light-speak/luxo/pkg/lux/storage"
	"github.com/minio/minio-go/v7"
	"io"
	"net/url"
	"os"
	"strings"
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
	// Should not error — protocol is stripped
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
	key := storage.GenerateKey("test", "file", "hello.bin")
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
