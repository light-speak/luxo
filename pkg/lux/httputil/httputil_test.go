package httputil

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	if resp.Headers["X-Test"] != "hello" {
		t.Error("missing header")
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
