package s3

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Every socket stays on an owned loopback server, even though the SDK uses its
// default AWS URL. TLS verifies the local certificate with a test-only CA pool.
func s3BoundaryNetwork(t testing.TB) (func(*S3Storage), *atomic.Bool) {
	seenTLS := new(atomic.Bool)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		seenTLS.Store(r.TLS != nil)
		w.WriteHeader(http.StatusOK)
	})
	plain, secure := httptest.NewServer(handler), httptest.NewTLSServer(handler)
	t.Cleanup(plain.Close)
	t.Cleanup(secure.Close)
	roots := x509.NewCertPool()
	roots.AddCert(secure.Certificate())
	host, _, err := net.SplitHostPort(secure.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return func(backend *S3Storage) {
		target := plain.Listener.Addr().String()
		backend.transport.Proxy = nil
		if backend.client.EndpointURL().Scheme == "https" {
			target = secure.Listener.Addr().String()
			if backend.transport.TLSClientConfig == nil {
				backend.transport.TLSClientConfig = &tls.Config{}
			}
			backend.transport.TLSClientConfig.RootCAs = roots
			backend.transport.TLSClientConfig.ServerName = host
		}
		backend.transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, target)
		}
	}, seenTLS
}

func TestDefaultS3EndpointConnectsOverTLS(t *testing.T) {
	route, seenTLS := s3BoundaryNetwork(t)
	backend, err := NewS3Storage(Config{AccessKey: "test-access", SecretKey: "test-secret", Bucket: "test-bucket", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	route(backend)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := backend.EnsureBucket(ctx, ""); err != nil || !seenTLS.Load() {
		t.Fatalf("default AWS request did not use signed TLS: err=%v tls=%t", err, seenTLS.Load())
	}
}

func BenchmarkS3BoundaryInitialization(b *testing.B) {
	for _, sample := range []struct{ name, endpoint, bucket string }{
		{"default", "", "test-bucket"},
		{"http", "http://127.0.0.1:9000", "test-bucket"},
		{"https", "https://127.0.0.1:9000", "test-bucket"},
		{"invalid", "http://127.0.0.1:9000", "../other"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			config := Config{Endpoint: sample.endpoint, Bucket: sample.bucket, AccessKey: "test-access", SecretKey: "test-secret"}
			initial, failure := NewS3Storage(config)
			if initial != nil {
				_ = initial.Close()
			}
			b.ReportAllocs()
			for b.Loop() {
				backend, err := NewS3Storage(config)
				if (err != nil) != (failure != nil) {
					b.Fatal("initialization result changed", err)
				}
				if backend != nil {
					if err := backend.Close(); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkS3BoundaryURL(b *testing.B) {
	for _, sample := range []struct{ name, bucket string }{
		{"default", ""}, {"alternate", "other-bucket"}, {"invalid", "../other"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			backend, err := NewS3Storage(Config{Endpoint: "http://127.0.0.1:9000", Bucket: "test-bucket", AccessKey: "test-access", SecretKey: "test-secret", PublicURL: "https://cdn.example/assets"})
			if err != nil {
				b.Fatal(err)
			}
			defer backend.Close()
			ctx := b.Context()
			_, failure := backend.URL(ctx, sample.bucket, "folder/object.txt")
			b.ReportAllocs()
			for b.Loop() {
				value, err := backend.URL(ctx, sample.bucket, "folder/object.txt")
				if (err != nil) != (failure != nil) || (err == nil && value == "") {
					b.Fatal("URL result changed", value, err)
				}
			}
		})
	}
}

func BenchmarkS3BoundaryNetwork(b *testing.B) {
	route, _ := s3BoundaryNetwork(b)
	for _, fresh := range []bool{false, true} {
		name := "reused"
		if fresh {
			name = "fresh"
		}
		b.Run(name, func(b *testing.B) {
			ctx, cancel := context.WithTimeout(b.Context(), 30*time.Second)
			defer cancel()
			newBackend := func() *S3Storage {
				backend, err := NewS3Storage(Config{AccessKey: "test-access", SecretKey: "test-secret", Bucket: "test-bucket", Region: "us-east-1"})
				if err != nil {
					b.Fatal(err)
				}
				route(backend)
				return backend
			}
			var backend *S3Storage
			if !fresh {
				backend = newBackend()
				defer backend.Close()
				if err := backend.EnsureBucket(ctx, ""); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				current := backend
				if fresh {
					current = newBackend()
				}
				if err := current.EnsureBucket(ctx, ""); err != nil {
					b.Fatal(err)
				}
				if fresh {
					_ = current.Close()
				}
			}
		})
	}
}
