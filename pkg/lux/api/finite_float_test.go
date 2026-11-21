package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func invalidFloatResponseRouter(bits uint64) *Router {
	rt := NewRouter()
	rt.SetDevMode(false)
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "measure", ReturnType: "Float"})
	rt.Registry.Register("measure", 1)
	rt.Handle("measure", func(_ context.Context, req *Request) error {
		// Simulate invalid bytes received from an older upstream service.
		req.Buf.B = binary.LittleEndian.AppendUint64(req.Buf.B, bits)
		return nil
	})
	return rt
}

func TestHTTPFloatConversionFailureIsNotSuccess(t *testing.T) {
	for _, bits := range []uint64{0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000001} {
		rt := invalidFloatResponseRouter(bits)
		metrics := &mockMetrics{}
		rt.SetMetricsCollector(metrics)
		r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"measure"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		rt.ServeHTTP(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("invalid Float returned HTTP %d: %s", w.Code, w.Body)
		}
		assertSafeEncodingError(t, w.Body.Bytes(), false, false)
		if len(metrics.calls) != 1 || !metrics.calls[0].isError {
			t.Fatal("encoding error counted as success")
		}
	}
}

func TestWebSocketFloatConversionFailureRetainsRequestID(t *testing.T) {
	srv := httptest.NewServer(invalidFloatResponseRouter(0x7ff0000000000000))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"$id":7,"$api":"measure"}`)); err != nil {
		t.Fatal(err)
	}
	mode, response, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode != websocket.MessageText || !bytes.Contains(response, []byte(`"$id":7`)) {
		t.Fatalf("lost request ID: %s", response)
	}
	assertSafeEncodingError(t, response, false, false)
}
