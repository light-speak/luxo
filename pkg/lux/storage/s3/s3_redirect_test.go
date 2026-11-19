package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

type redirectRequest struct {
	method, path, body string
	signed             bool
	readError          error
}

type redirectFixture struct {
	mu       sync.Mutex
	requests []redirectRequest
	followed atomic.Int64
	recovery atomic.Bool
	status   int
	location string
}

func ownedRedirectServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	address := server.Listener.Addr().String()
	t.Cleanup(func() {
		server.Close()
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			t.Errorf("owned redirect listener remained open: %s", address)
		}
		t.Logf("owned redirect server closed: %s", address)
	})
	return server
}

func (f *redirectFixture) serve(w http.ResponseWriter, r *http.Request) {
	wire, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	body := wire
	if err == nil && strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		body, err = io.ReadAll(httputil.NewChunkedReader(bytes.NewReader(wire)))
	}
	f.mu.Lock()
	f.requests = append(f.requests, redirectRequest{r.Method, r.URL.Path, string(body), r.Header.Get("Authorization") != "", err})
	f.mu.Unlock()
	if r.URL.Path == "/redirected" {
		f.followed.Add(1)
		w.WriteHeader(http.StatusOK)
		return
	}
	if f.recovery.Load() {
		w.Header().Set("ETag", `"owned-etag"`)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}
	w.Header().Set("Location", f.location)
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("X-Amz-Request-Id", "owned-redirect")
	w.WriteHeader(f.status)
	code := "TemporaryRedirect"
	if f.status == http.StatusMovedPermanently {
		code = "PermanentRedirect"
	}
	_, _ = fmt.Fprintf(w, `<Error><Code>%s</Code><Message>owned redirect</Message></Error>`, code)
}

func newRedirectFixture(t *testing.T, status int, absolute bool) (*S3Storage, *redirectFixture) {
	t.Helper()
	f := &redirectFixture{status: status, location: "/redirected"}
	target := ownedRedirectServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.followed.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	if absolute {
		f.location = target.URL + "/redirected"
	}
	origin := ownedRedirectServer(t, http.HandlerFunc(f.serve))
	backend, err := NewS3Storage(Config{Endpoint: origin.URL, Region: "us-east-1", Bucket: "test-bucket", AccessKey: "owned-access", SecretKey: "owned-secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	return backend, f
}

func checkRedirectFailure(t *testing.T, fixture *redirectFixture, err error, method string) {
	t.Helper()
	var response minio.ErrorResponse
	if err == nil || !errors.As(err, &response) || response.StatusCode != fixture.status || response.RequestID != "owned-redirect" {
		t.Fatalf("SDK redirect response lost through adapter: error=%v response=%+v", err, response)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests) != 1 || fixture.followed.Load() != 0 {
		t.Fatalf("SDK followed/replayed redirect: origin=%d destination=%d", len(fixture.requests), fixture.followed.Load())
	}
	request := fixture.requests[0]
	if request.method != method || !request.signed || request.readError != nil {
		t.Fatalf("unexpected original SDK request: %+v", request)
	}
	t.Logf("SDK30x status=%d location=%q method=%s origin=1 followed=0 wrappedStatus=%d", fixture.status, fixture.location, method, response.StatusCode)
}

func TestS3SDKRedirectDeleteAndBucketBoundaries(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, absolute := range []bool{false, true} {
			for _, operation := range []string{"delete", "bucket"} {
				t.Run(fmt.Sprintf("%d/absolute=%t/%s", status, absolute, operation), func(t *testing.T) {
					backend, fixture := newRedirectFixture(t, status, absolute)
					ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
					defer cancel()
					call, method := func() error { return backend.Delete(ctx, "", "object") }, http.MethodDelete
					if operation == "bucket" {
						call, method = func() error { return backend.EnsureBucket(ctx, "") }, http.MethodHead
					}
					checkRedirectFailure(t, fixture, call(), method)
					fixture.recovery.Store(true)
					if err := call(); err != nil {
						t.Fatalf("explicit retry after recovery failed: %v", err)
					}
					fixture.mu.Lock()
					defer fixture.mu.Unlock()
					if len(fixture.requests) != 2 || fixture.requests[1].method != method || fixture.followed.Load() != 0 {
						t.Fatalf("unexpected recovered SDK traffic: %+v", fixture.requests)
					}
				})
			}
		}
	}
}

func checkUploadRedirect(t *testing.T, status int, absolute, seekable bool) {
	t.Helper()
	backend, fixture := newRedirectFixture(t, status, absolute)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	const payload = "owned upload\x00payload"
	reader := func() io.Reader {
		if seekable {
			return strings.NewReader(payload)
		}
		return struct{ io.Reader }{strings.NewReader(payload)}
	}
	value, err := backend.Upload(ctx, "", "object", reader(), "application/octet-stream", int64(len(payload)))
	if value != "" {
		t.Fatal("redirected upload published a success URL", value)
	}
	checkRedirectFailure(t, fixture, err, http.MethodPut)
	fixture.recovery.Store(true)
	value, err = backend.Upload(ctx, "", "object", reader(), "application/octet-stream", int64(len(payload)))
	want, _ := backend.URL(ctx, "", "object")
	if err != nil || value != want || value == "" {
		t.Fatalf("explicit upload retry after recovery: URL=%q want=%q error=%v", value, want, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests) != 2 || fixture.followed.Load() != 0 {
		t.Fatalf("redirect replayed upload bytes: origin=%d followed=%d", len(fixture.requests), fixture.followed.Load())
	}
	for _, request := range fixture.requests {
		if request.method != http.MethodPut || request.body != payload || request.readError != nil || !request.signed {
			t.Fatalf("upload payload changed: %+v", request)
		}
	}
}

func TestS3SDKUpload307And308DoNotReplayBody(t *testing.T) {
	for _, status := range []int{307, 308} {
		for _, absolute := range []bool{false, true} {
			for _, seekable := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/absolute=%t/seekable=%t", status, absolute, seekable), func(t *testing.T) {
					checkUploadRedirect(t, status, absolute, seekable)
				})
			}
		}
	}
}
