package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestHTTPResponsesDisallowTransportCaching(t *testing.T) {
	router := NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "read", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.Handle("read", func(_ context.Context, req *Request) error {
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
		return nil
	})
	for _, binary := range []bool{false, true} {
		for _, method := range []string{http.MethodPost, http.MethodGet} {
			for _, valid := range []bool{false, true} {
				body := []byte(`{"$api":"read"}`)
				if binary {
					body = mustEncodeBinaryRequest(t, 1, nil, nil, nil)
				}
				if !valid {
					body = []byte{0xff}
				}
				request := httptest.NewRequest(method, "/luvia", bytes.NewReader(body))
				if binary {
					request.Header.Set("X-Luxo-Mode", "binary")
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if got := response.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("binary=%t method=%s valid=%t: Cache-Control=%q", binary, method, valid, got)
				}
				if valid && method == http.MethodPost && response.Code != http.StatusOK {
					t.Fatalf("success request returned %d: %s", response.Code, response.Body.String())
				}
			}
		}
	}
}
