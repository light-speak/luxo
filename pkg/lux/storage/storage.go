// Package storage provides a pluggable interface for S3-compatible object storage.
// Works with AWS S3, MinIO, Cloudflare R2, Aliyun OSS, and any S3-compatible service.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Storage is the pluggable interface for object storage operations.
type Storage interface {
	// Upload stores a file and returns its public URL.
	Upload(ctx context.Context, bucket, key string, data io.Reader, contentType string, size int64) (url string, err error)
	// Delete removes a file by bucket and key.
	Delete(ctx context.Context, bucket, key string) error
	// URL returns the public/presigned URL for a file.
	URL(ctx context.Context, bucket, key string) (string, error)
	// Close releases any resources held by the storage backend.
	Close() error
}

// Config holds S3-compatible storage configuration.
type Config struct {
	Endpoint  string // S3 endpoint (empty = AWS default)
	Region    string // AWS region (e.g., "us-east-1")
	AccessKey string // access key ID
	SecretKey string // secret access key
	Bucket    string // default bucket name
	PublicURL string // base URL for public access (e.g., CDN URL)
	UseSSL    bool   // use HTTPS for endpoint
}

// Validate checks that required fields are set.
func (c *Config) Validate() error {
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

// ConfigFromEnv builds a Config from STORAGE_* environment variables.
// Variables: STORAGE_ENDPOINT, STORAGE_REGION, STORAGE_ACCESS_KEY,
// STORAGE_SECRET_KEY, STORAGE_BUCKET, STORAGE_PUBLIC_URL, STORAGE_USE_SSL.
func ConfigFromEnv(getenv func(string) string) Config {
	useSSL := false
	if v := getenv("STORAGE_USE_SSL"); v == "true" || v == "1" {
		useSSL = true
	}
	return Config{
		Endpoint:  getenv("STORAGE_ENDPOINT"),
		Region:    getenv("STORAGE_REGION"),
		AccessKey: getenv("STORAGE_ACCESS_KEY"),
		SecretKey: getenv("STORAGE_SECRET_KEY"),
		Bucket:    getenv("STORAGE_BUCKET"),
		PublicURL: getenv("STORAGE_PUBLIC_URL"),
		UseSSL:    useSSL,
	}
}

// GenerateKey builds an object key: {model}/{field}/{uuid}.{ext}
func GenerateKey(model, field, filename string) string {
	ext := path.Ext(filename)
	id := uuid.New().String()
	return model + "/" + field + "/" + id + ext
}

// ParseSize parses a human-readable size string (e.g., "5mb", "100kb", "1gb").
// Returns size in bytes. Supported units: b, kb, mb, gb.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, errors.New("storage: empty size string")
	}

	var numStr string
	var unit string
	for i, c := range s {
		if c < '0' || c > '9' {
			numStr = s[:i]
			unit = s[i:]
			break
		}
	}
	if numStr == "" {
		// All digits, no unit — treat as bytes
		numStr = s
		unit = "b"
	}

	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("storage: invalid size number %q: %w", numStr, err)
	}

	switch unit {
	case "b", "":
		return n, nil
	case "kb":
		return n * 1024, nil
	case "mb":
		return n * 1024 * 1024, nil
	case "gb":
		return n * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("storage: unknown size unit %q (supported: b, kb, mb, gb)", unit)
	}
}

// MatchContentType checks if a content type matches an accept pattern.
// Supports exact match ("image/png") and wildcard ("image/*").
func MatchContentType(contentType, accept string) bool {
	if accept == "" || accept == "*/*" {
		return true
	}

	// Split accept into multiple patterns (comma-separated)
	patterns := strings.Split(accept, ",")
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "*/*" {
			return true
		}
		if p == contentType {
			return true
		}
		// Wildcard match: "image/*" matches "image/png"
		if strings.HasSuffix(p, "/*") {
			prefix := p[:len(p)-1] // "image/"
			if strings.HasPrefix(contentType, prefix) {
				return true
			}
		}
	}
	return false
}
