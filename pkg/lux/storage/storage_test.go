package storage

import (
	"context"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
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

func TestConfigValidatePublicURL(t *testing.T) {
	for _, raw := range []string{"https://cdn.example.com?token=secret", "https://cdn.example.com?", "https://cdn.example.com#fragment", "https://cdn.example.com#", "https://user:secret@cdn.example.com", "javascript:alert(1)", "/files", "//cdn.example.com", "https:///files", "https://cdn.example.com/%zz", "https://cdn.example.com/\n"} {
		t.Run(raw, func(t *testing.T) {
			cfg := Config{AccessKey: "access", SecretKey: "secret", Bucket: "bucket", PublicURL: raw}
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid public base URL accepted")
			} else if strings.Contains(err.Error(), "secret") {
				t.Fatal("validation exposed credentials")
			}
		})
	}
	for _, raw := range []string{"", "https://cdn.example.com", "http://localhost:9000/files/", "https://[::1]:9000/base", "https://cdn.example.com/a%20b"} {
		cfg := Config{AccessKey: "access", SecretKey: "secret", Bucket: "bucket", PublicURL: raw}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("valid public URL %q: %v", raw, err)
		}
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
	key, err := GenerateKey("user", "avatar", "photo.png")
	if err != nil {
		t.Fatal(err)
	}

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
	id, err := uuid.Parse(uuidPart)
	if err != nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 {
		t.Fatal("generated key UUID changed", id, err)
	}
}

func TestGenerateKeyNoExt(t *testing.T) {
	key, err := GenerateKey("post", "file", "README")
	if err != nil {
		t.Fatal(err)
	}
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
	k1, err := GenerateKey("m", "f", "a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	k2, err := GenerateKey("m", "f", "a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 {
		t.Error("two generated keys should not be equal")
	}
}

// ========== ParseSize ==========

func TestParseSizeRange(t *testing.T) {
	for _, tc := range []struct {
		unit  string
		shift uint
	}{{"b", 0}, {"kb", 10}, {"mb", 20}, {"gb", 30}} {
		t.Run(tc.unit, func(t *testing.T) {
			max := int64(math.MaxInt64 >> tc.shift)
			valid := strconv.FormatInt(max, 10) + tc.unit
			if got, err := ParseSize(valid); err != nil || got != max<<tc.shift {
				t.Fatalf("boundary %q: got=%d err=%v", valid, got, err)
			}
			invalid := strconv.FormatUint(uint64(max)+1, 10) + tc.unit
			if got, err := ParseSize(invalid); err == nil || got != 0 {
				t.Fatalf("overflow %q: got=%d err=%v", invalid, got, err)
			}
		})
	}
	if got, err := ParseSize("-1"); err == nil || got != 0 {
		t.Fatalf("negative size accepted: got=%d err=%v", got, err)
	}
}

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

// Preserve the required-field-only validator for a scoped startup comparison.
func validateConfigBeforePublicURL(c *Config) error {
	if c.AccessKey == "" {
		return errors.New("storage: access key is required")
	}
	if c.SecretKey == "" {
		return errors.New("storage: secret key is required")
	}
	if c.Bucket == "" {
		return errors.New("storage: bucket is required")
	}
	return nil
}

func BenchmarkConfigPublicURLValidation(b *testing.B) {
	for _, sample := range []struct{ name, url string }{
		{"unset", ""}, {"cdn", "https://cdn.example.com"}, {"path", "https://cdn.example.com/a%20b/files/"},
	} {
		cfg := Config{AccessKey: "access", SecretKey: "secret", Bucket: "bucket", PublicURL: sample.url}
		for _, version := range []struct {
			name     string
			validate func(*Config) error
		}{
			{"before", validateConfigBeforePublicURL}, {"current", (*Config).Validate},
		} {
			b.Run(sample.name+"/"+version.name, func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if err := version.validate(&cfg); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
