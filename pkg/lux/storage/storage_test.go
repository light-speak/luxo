package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
)

// ========== Config Validation ==========

func TestConfigValidateMissingAccessKey(t *testing.T) {
	cfg := Config{SecretKey: "secret", Bucket: "bucket"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing access key")
	}
	if !strings.Contains(err.Error(), "access key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidateMissingSecretKey(t *testing.T) {
	cfg := Config{AccessKey: "access", Bucket: "bucket"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing secret key")
	}
	if !strings.Contains(err.Error(), "secret key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidateMissingBucket(t *testing.T) {
	cfg := Config{AccessKey: "access", SecretKey: "secret"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing bucket")
	}
	if !strings.Contains(err.Error(), "bucket") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidateOk(t *testing.T) {
	cfg := Config{AccessKey: "access", SecretKey: "secret", Bucket: "bucket"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ========== ConfigFromEnv ==========

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"STORAGE_ENDPOINT":   "minio:9000",
		"STORAGE_REGION":     "us-east-1",
		"STORAGE_ACCESS_KEY": "ak",
		"STORAGE_SECRET_KEY": "sk",
		"STORAGE_BUCKET":     "mybucket",
		"STORAGE_PUBLIC_URL": "https://cdn.example.com",
		"STORAGE_USE_SSL":    "true",
	}
	cfg := ConfigFromEnv(func(key string) string { return env[key] })

	if cfg.Endpoint != "minio:9000" {
		t.Errorf("endpoint = %q, want %q", cfg.Endpoint, "minio:9000")
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("region = %q, want %q", cfg.Region, "us-east-1")
	}
	if cfg.AccessKey != "ak" {
		t.Errorf("access key = %q, want %q", cfg.AccessKey, "ak")
	}
	if cfg.SecretKey != "sk" {
		t.Errorf("secret key = %q, want %q", cfg.SecretKey, "sk")
	}
	if cfg.Bucket != "mybucket" {
		t.Errorf("bucket = %q, want %q", cfg.Bucket, "mybucket")
	}
	if cfg.PublicURL != "https://cdn.example.com" {
		t.Errorf("public url = %q, want %q", cfg.PublicURL, "https://cdn.example.com")
	}
	if !cfg.UseSSL {
		t.Error("use ssl = false, want true")
	}
}

func TestConfigFromEnvUseSSLVariants(t *testing.T) {
	// "1" also means true
	cfg := ConfigFromEnv(func(key string) string {
		if key == "STORAGE_USE_SSL" {
			return "1"
		}
		return ""
	})
	if !cfg.UseSSL {
		t.Error("use ssl should be true for '1'")
	}

	// anything else is false
	cfg = ConfigFromEnv(func(key string) string {
		if key == "STORAGE_USE_SSL" {
			return "false"
		}
		return ""
	})
	if cfg.UseSSL {
		t.Error("use ssl should be false for 'false'")
	}
}

// ========== GenerateKey ==========

func TestGenerateKeyFormat(t *testing.T) {
	key := GenerateKey("user", "avatar", "photo.png")

	if !strings.HasPrefix(key, "user/avatar/") {
		t.Errorf("key %q should start with user/avatar/", key)
	}
	if !strings.HasSuffix(key, ".png") {
		t.Errorf("key %q should end with .png", key)
	}
	// UUID part should be 36 chars
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		t.Fatalf("key %q should have 3 parts", key)
	}
	uuidPart := strings.TrimSuffix(parts[2], ".png")
	if len(uuidPart) != 36 {
		t.Errorf("uuid part %q should be 36 chars", uuidPart)
	}
}

func TestGenerateKeyNoExt(t *testing.T) {
	key := GenerateKey("post", "file", "README")
	if !strings.HasPrefix(key, "post/file/") {
		t.Errorf("key %q should start with post/file/", key)
	}
	// No extension — no dot at end
	parts := strings.Split(key, "/")
	if strings.Contains(parts[2], ".") {
		t.Errorf("key %q should not have extension", key)
	}
}

func TestGenerateKeyUniqueness(t *testing.T) {
	k1 := GenerateKey("m", "f", "a.jpg")
	k2 := GenerateKey("m", "f", "a.jpg")
	if k1 == k2 {
		t.Error("two generated keys should not be equal")
	}
}

// ========== ParseSize ==========

func TestParseSizeBytes(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"100b", 100},
		{"100", 100},
		{"0b", 0},
	}
	for _, tt := range tests {
		got, err := ParseSize(tt.input)
		if err != nil {
			t.Errorf("ParseSize(%q): %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseSizeKB(t *testing.T) {
	got, err := ParseSize("5kb")
	if err != nil {
		t.Fatal(err)
	}
	if got != 5*1024 {
		t.Errorf("got %d, want %d", got, 5*1024)
	}
}

func TestParseSizeMB(t *testing.T) {
	got, err := ParseSize("10mb")
	if err != nil {
		t.Fatal(err)
	}
	if got != 10*1024*1024 {
		t.Errorf("got %d, want %d", got, 10*1024*1024)
	}
}

func TestParseSizeGB(t *testing.T) {
	got, err := ParseSize("2gb")
	if err != nil {
		t.Fatal(err)
	}
	if got != 2*1024*1024*1024 {
		t.Errorf("got %d, want %d", got, int64(2*1024*1024*1024))
	}
}

func TestParseSizeUpperCase(t *testing.T) {
	got, err := ParseSize("5MB")
	if err != nil {
		t.Fatal(err)
	}
	if got != 5*1024*1024 {
		t.Errorf("got %d, want %d", got, 5*1024*1024)
	}
}

func TestParseSizeEmpty(t *testing.T) {
	_, err := ParseSize("")
	if err == nil {
		t.Fatal("expected error for empty string")
	}
}

func TestParseSizeInvalidNumber(t *testing.T) {
	_, err := ParseSize("abcmb")
	if err == nil {
		t.Fatal("expected error for invalid number")
	}
}

func TestParseSizeUnknownUnit(t *testing.T) {
	_, err := ParseSize("5tb")
	if err == nil {
		t.Fatal("expected error for unknown unit")
	}
}

func TestParseSizeWhitespace(t *testing.T) {
	got, err := ParseSize("  5mb  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != 5*1024*1024 {
		t.Errorf("got %d, want %d", got, 5*1024*1024)
	}
}

// ========== MatchContentType ==========

func TestMatchContentTypeExact(t *testing.T) {
	if !MatchContentType("image/png", "image/png") {
		t.Error("exact match should return true")
	}
}

func TestMatchContentTypeWildcard(t *testing.T) {
	if !MatchContentType("image/png", "image/*") {
		t.Error("image/* should match image/png")
	}
	if MatchContentType("text/plain", "image/*") {
		t.Error("image/* should not match text/plain")
	}
}

func TestMatchContentTypeMultiple(t *testing.T) {
	if !MatchContentType("image/png", "image/*, application/pdf") {
		t.Error("should match first pattern")
	}
	if !MatchContentType("application/pdf", "image/*, application/pdf") {
		t.Error("should match second pattern")
	}
	if MatchContentType("text/plain", "image/*, application/pdf") {
		t.Error("should not match any pattern")
	}
}

func TestMatchContentTypeAllWildcard(t *testing.T) {
	if !MatchContentType("anything/here", "*/*") {
		t.Error("*/* should match everything")
	}
}

func TestMatchContentTypeEmpty(t *testing.T) {
	if !MatchContentType("anything/here", "") {
		t.Error("empty accept should match everything")
	}
}

func TestMatchContentTypeAllWildcardInList(t *testing.T) {
	if !MatchContentType("video/mp4", "image/png, */*") {
		t.Error("*/* in list should match everything")
	}
}

// ========== Mock-based Storage Interface Tests ==========

// mockStorage implements Storage for testing.
type mockStorage struct {
	uploads map[string]string // key → url
	deleted []string
}

func newMockStorage() *mockStorage {
	return &mockStorage{uploads: make(map[string]string)}
}

func (m *mockStorage) Upload(_ context.Context, bucket, key string, _ io.Reader, _ string, _ int64) (string, error) {
	url := "https://mock/" + bucket + "/" + key
	m.uploads[key] = url
	return url, nil
}

func (m *mockStorage) Delete(_ context.Context, _ string, key string) error {
	m.deleted = append(m.deleted, key)
	delete(m.uploads, key)
	return nil
}

func (m *mockStorage) URL(_ context.Context, bucket, key string) (string, error) {
	return "https://mock/" + bucket + "/" + key, nil
}

func (m *mockStorage) Close() error {
	return nil
}

func TestStorageInterfaceUpload(t *testing.T) {
	var s Storage = newMockStorage()
	ctx := context.Background()
	url, err := s.Upload(ctx, "mybucket", "user/avatar/abc.png", strings.NewReader("data"), "image/png", 4)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://mock/mybucket/user/avatar/abc.png" {
		t.Errorf("url = %q", url)
	}
}

func TestStorageInterfaceDelete(t *testing.T) {
	ms := newMockStorage()
	var s Storage = ms
	ctx := context.Background()
	_, _ = s.Upload(ctx, "b", "key1", strings.NewReader(""), "", 0)
	if err := s.Delete(ctx, "b", "key1"); err != nil {
		t.Fatal(err)
	}
	if len(ms.deleted) != 1 || ms.deleted[0] != "key1" {
		t.Error("delete not recorded")
	}
}

func TestStorageInterfaceURL(t *testing.T) {
	var s Storage = newMockStorage()
	url, err := s.URL(context.Background(), "b", "k")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://mock/b/k" {
		t.Errorf("url = %q", url)
	}
}

func TestStorageInterfaceClose(t *testing.T) {
	var s Storage = newMockStorage()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// ========== NewS3Storage validation ==========

func TestNewS3StorageMissingConfig(t *testing.T) {
	_, err := NewS3Storage(Config{})
	if err == nil {
		t.Fatal("expected error for empty config")
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
	endpoint := getTestEnv("STORAGE_ENDPOINT")
	if endpoint == "" {
		t.Skip("STORAGE_ENDPOINT not set, skipping integration test")
	}
	cfg := Config{
		Endpoint:  endpoint,
		AccessKey: getTestEnv("STORAGE_ACCESS_KEY"),
		SecretKey: getTestEnv("STORAGE_SECRET_KEY"),
		Bucket:    getTestEnv("STORAGE_BUCKET"),
		PublicURL: getTestEnv("STORAGE_PUBLIC_URL"),
	}
	s, err := NewS3Storage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	// Ensure bucket exists
	if err := s.EnsureBucket(ctx, ""); err != nil {
		t.Fatal(err)
	}

	key := GenerateKey("test", "file", "hello.txt")
	url, err := s.Upload(ctx, "", key, strings.NewReader("hello world"), "text/plain", 11)
	if err != nil {
		t.Fatal(err)
	}
	if url == "" {
		t.Error("upload returned empty url")
	}

	gotURL, err := s.URL(ctx, "", key)
	if err != nil {
		t.Fatal(err)
	}
	if gotURL == "" {
		t.Error("URL returned empty")
	}

	if err := s.Delete(ctx, "", key); err != nil {
		t.Fatal(err)
	}
}

// getTestEnv reads an environment variable for integration tests.
func getTestEnv(key string) string {
	return os.Getenv(key)
}
