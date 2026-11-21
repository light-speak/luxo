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

func TestRouterRejectsInvalidTextResponse(t *testing.T) {
	for _, raw := range [][]byte{{0xff}, {0xed, 0xa0, 0x80}, {0xe2, 0x82}} {
		for _, traced := range []bool{false, true} {
			rt := NewRouter()
			rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "opaque", ReturnType: "String"})
			rt.Handle("opaque", func(_ context.Context, req *Request) error {
				req.Buf.B = codec.AppendBytes(req.Buf.B, raw)
				return nil
			})
			assertMalformedJSONResponse(t, rt, `{"$api":"opaque"}`, traced)
		}
	}
}

func TestRouterRejectsInvalidTextRequestBeforeHandler(t *testing.T) {
	for _, kind := range []string{"String", "Enum", "Decimal", "Bytes"} {
		for _, list := range []bool{false, true} {
			rt := NewRouter()
			rt.Registry.Register("text", 1)
			rt.Registry.RegisterParams("text", []ParamMeta{{Name: "value", FieldID: 1, Type: kind, IsList: list}})
			called := false
			rt.Handle("text", func(_ context.Context, req *Request) error { called = true; return nil })
			body := []byte{1, 0, 1}
			if list {
				body = append(body, 1)
			}
			body = append(codec.AppendBytes(body, []byte{0xff}), 0)
			r := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader(body))
			r.Header.Set("X-Luxo-Mode", "binary")
			w := httptest.NewRecorder()
			rt.ServeHTTP(w, r)
			want := http.StatusBadRequest
			if kind == "Bytes" {
				want = http.StatusOK
			}
			if w.Code != want || called != (kind == "Bytes") {
				t.Fatalf("%s/list=%v status=%d called=%v: %s", kind, list, w.Code, called, w.Body.String())
			}
		}
	}
}
