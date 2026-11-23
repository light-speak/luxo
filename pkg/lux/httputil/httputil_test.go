package httputil

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "hello")
		w.Write([]byte("get ok"))
	})
	mux.HandleFunc("POST /post", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ct := r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("ct:" + ct + " body:" + string(body)))
	})
	mux.HandleFunc("PUT /put", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write([]byte("put:" + string(body)))
	})
	mux.HandleFunc("DELETE /delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return httptest.NewServer(mux)
}

func TestGet(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	ctx := context.Background()

	resp, err := Get(ctx, ts.URL+"/get")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Body != "get ok" {
		t.Errorf("got %d %q", resp.StatusCode, resp.Body)
	}
	if resp.Headers.Get("X-Test") != "hello" {
		t.Error("missing header")
	}
}

func TestGetRejectsTruncatedResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("cut"))
	}))
	defer server.Close()
	response, err := Get(t.Context(), server.URL)
	if response != nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated HTTP body returned success: response=%+v err=%v", response, err)
	}
}

func TestDoEnforcesResponseBodyLimit(t *testing.T) {
	for _, sample := range []struct {
		transfer string
		payload  string
	}{{"fixed", "hello"}, {"chunked", "chunk"}, {"gzip", "world"}} {
		t.Run(sample.transfer, func(t *testing.T) {
			t.Parallel()
			transfer, payload := sample.transfer, sample.payload
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if transfer == "gzip" {
					w.Header().Set("Content-Encoding", "gzip")
					compressor := gzip.NewWriter(w)
					_, _ = io.WriteString(compressor, payload)
					_ = compressor.Close()
					return
				}
				if transfer == "chunked" {
					w.(http.Flusher).Flush()
				}
				_, _ = io.WriteString(w, payload)
			}))
			defer server.Close()
			atLimit, err := DoWithResponseLimit(t.Context(), http.MethodGet, server.URL, "", nil, int64(len(payload)))
			if err != nil || atLimit == nil || atLimit.Body != payload {
				t.Fatalf("response at the limit failed: %+v %v", atLimit, err)
			}
			overLimit, err := DoWithResponseLimit(t.Context(), http.MethodGet, server.URL, "", nil, int64(len(payload)-1))
			var limitErr *ResponseBodyLimitError
			if overLimit != nil || !errors.As(err, &limitErr) || limitErr.Limit != int64(len(payload)-1) {
				t.Fatalf("response over the limit returned data or an unrelated error: %+v %v", overLimit, err)
			}
			if got := limitErr.Error(); got != "HTTP response body exceeds 4 bytes" {
				t.Fatalf("incorrect diagnostic: %q", got)
			}
		})
	}
}

func TestDoValidatesResponseBodyLimitBeforeSending(t *testing.T) {
	response, err := DoWithResponseLimit(t.Context(), http.MethodGet, "://bad", "", nil, 0)
	if response != nil || !errors.Is(err, errInvalidResponseBodyLimit) {
		t.Fatalf("unknown error for an invalid limit: %+v %v", response, err)
	}
	if data, err := ReadResponseBody(http.NoBody, -1); data != nil || !errors.Is(err, errInvalidResponseBodyLimit) {
		t.Fatalf("unknown error for an invalid read limit: %v %v", data, err)
	}
	if data, err := ReadResponseBody(strings.NewReader("hello"), math.MaxInt64); string(data) != "hello" || err != nil {
		t.Fatalf("valid limit overflowed the reader: %q %v", data, err)
	}
}

func TestPost(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	ctx := context.Background()

	resp, err := Post(ctx, ts.URL+"/post", `{"name":"lin"}`)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	// Should auto-set Content-Type: application/json
	if resp.Body != `ct:application/json body:{"name":"lin"}` {
		t.Errorf("body = %q", resp.Body)
	}
}

func TestPostEmptyBody(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	ctx := context.Background()

	resp, err := Post(ctx, ts.URL+"/post", "")
	if err != nil {
		t.Fatal(err)
	}
	// Empty body should still send (zero-length, not nil)
	if resp.StatusCode != 201 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestPut(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	ctx := context.Background()

	resp, err := Put(ctx, ts.URL+"/put", "data")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != "put:data" {
		t.Errorf("body = %q", resp.Body)
	}
}

func TestDelete(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	ctx := context.Background()

	resp, err := Delete(ctx, ts.URL+"/delete")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 204 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestDoWithHeaders(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	ctx := context.Background()

	resp, err := Do(ctx, "GET", ts.URL+"/get", "", map[string]string{"X-Custom": "val"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestDoInvalidURL(t *testing.T) {
	_, err := Do(context.Background(), "GET", "://bad", "", nil)
	if err == nil {
		t.Fatal("should error")
	}
}

func TestDoConnectionRefused(t *testing.T) {
	_, err := Get(context.Background(), "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("should error")
	}
}

func TestDoContextCancel(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Get(ctx, ts.URL+"/get")
	if err == nil {
		t.Fatal("should error on cancelled context")
	}
}
