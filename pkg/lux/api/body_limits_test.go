package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"nhooyr.io/websocket"
	"strings"
	"testing"
	"time"
)

func TestDefaultRequestBodyLimit(t *testing.T) {
	if MaxRequestBodySize != 2<<20 || MaxJSONBodySize != 2<<20 {
		t.Fatal("public request defaults must be 2MiB")
	}
	for _, binary := range []bool{false, true} {
		for _, length := range []int64{-1, 1, (2 << 20) + 1} {
			router := NewRouterWithOptions(RouterOptions{})
			body := &countedBody{Reader: bytes.NewReader(bytes.Repeat([]byte{'x'}, (2<<20)+1))}
			req := httptest.NewRequest(http.MethodPost, "/luvia", nil)
			req.Body = body
			req.ContentLength = length
			if binary {
				req.Header.Set("X-Luxo-Mode", "binary")
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("binary=%v length=%d status=%d", binary, length, res.Code)
			}
			if length > 2<<20 && body.read != 0 {
				t.Fatal("oversized declared body was read")
			}
		}
	}
}

type countedBody struct {
	io.Reader
	read int
}

func (b *countedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (*countedBody) Close() error { return nil }

func TestExplicitBodyLimits(t *testing.T) {
	router := NewRouterWithOptions(RouterOptions{BodyLimits: BodyLimits{RequestBytes: 32, ResponseBytes: 64}})
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	if router.BodyLimits().RequestBytes != 32 || router.BodyLimits().ResponseBytes != 64 {
		t.Fatal("limits changed")
	}
	for _, binary := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader(make([]byte, 33)))
		req.ContentLength = -1
		if binary {
			req.Header.Set("X-Luxo-Mode", "binary")
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != 413 {
			t.Fatal(res.Code)
		}
	}
	bad := NewRouterWithOptions(RouterOptions{BodyLimits: BodyLimits{RequestBytes: -1}})
	if err := bad.Prepare(); err == nil {
		t.Fatal("negative limit accepted")
	}
}

func TestBodyLimitExactAndBorrowed(t *testing.T) {
	for _, size := range []int{31, 32, 33} {
		data := bytes.Repeat([]byte{'x'}, size)
		for _, reader := range []io.ReadCloser{io.NopCloser(bytes.NewReader(data)), NewBufferedBody(data)} {
			got, pool, err := readTransportBodyLimit(reader, 32)
			if pool != nil {
				putBody(pool)
			}
			if size <= 32 && (err != nil || len(got) != size) {
				t.Fatalf("size=%d: %v", size, err)
			}
			if size > 32 && err == nil {
				t.Fatal("oversize accepted")
			}
		}
	}
}

func TestResponseLimitIndependentOfRequest(t *testing.T) {
	router := NewRouterWithOptions(RouterOptions{BodyLimits: BodyLimits{RequestBytes: 32, ResponseBytes: 64}})
	for _, size := range []int{64, 65} {
		req := &Request{API: "test"}
		res := httptest.NewRecorder()
		router.executeHTTPHandler(res, httptest.NewRequest(http.MethodPost, "/", nil), req,
			func(_ context.Context, req *Request) error {
				req.Buf.B = append(req.Buf.B, make([]byte, size)...)
				return nil
			}, true, nil, nil, time.Time{})
		want := 200
		if size > 64 {
			want = 500
		}
		if res.Code != want {
			t.Fatalf("size=%d status=%d", size, res.Code)
		}
	}
}

func TestWebSocketRequestLimitIsPerMessage(t *testing.T) {
	router := NewRouterWithOptions(RouterOptions{BodyLimits: BodyLimits{RequestBytes: 64}})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	// Each valid envelope fits; their cumulative size exceeds the message limit.
	for range 4 {
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"missing"}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.Read(ctx); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.Write(ctx, websocket.MessageText, bytes.Repeat([]byte{'x'}, 65))
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("oversized message accepted")
	}
}

func TestWebSocketResponseLimitIsExplicit(t *testing.T) {
	router := NewRouterWithOptions(RouterOptions{BodyLimits: BodyLimits{ResponseBytes: 1}})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"missing"}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("unexpected close: %v", err)
	}
}
