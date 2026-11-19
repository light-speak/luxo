//go:build !race

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// The race runtime deliberately discards pooled values, so allocation assertions
// run without instrumentation. HTTP lifecycle behavior is race-tested separately.
func TestHTTPCompletionKeepsContextCloneOnStack(t *testing.T) {
	router := newHTTPAbnormalRouter(t, nil)
	request := httpAbnormalRequest(true)
	req := &Request{API: "value"}
	writer := httptest.NewRecorder()
	handler := func(_ context.Context, req *Request) error {
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
		return nil
	}
	invoke := func(r *http.Request) {
		writer.Body.Reset()
		router.executeHTTPHandler(writer, r, req, handler, true, nil, nil, time.Time{})
	}
	base := testing.AllocsPerRun(1000, func() { invoke(request) })
	cloned := testing.AllocsPerRun(1000, func() { invoke(request.WithContext(context.Background())) })
	if cloned != base {
		t.Fatalf("context clone adds %.0f allocations per call: reused=%.0f cloned=%.0f", cloned-base, base, cloned)
	}
}
