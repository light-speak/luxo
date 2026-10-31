package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

func invalidUnicodeRequests() []string {
	return []string{
		`{"$api":"text","value":"\uD800"}`,
		`{"$api":"text","\uDC00":1}`,
		`{"$api":"text","raw":{"items":["\uDBFF"]}}`,
		`{"$api":"text","value":"\uD800","value":"valid"}`,
		`{"$api":"text","$select":"\uD800"}`,
		`{"$api":"text","$filters":[{"field":"id","op":"eq","value":"\uDC00"}]}`,
	}
}

func TestHTTPJSONRejectsSurrogatesBeforeDispatch(t *testing.T) {
	for _, body := range invalidUnicodeRequests() {
		t.Run(body, func(t *testing.T) {
			for _, traced := range []bool{false, true} {
				rt := NewRouter()
				rt.SetDevMode(traced)
				called := false
				rt.Handle("text", func(context.Context, *Request) error { called = true; return nil })
				req := makeReq(body)
				if traced {
					req.Header.Set(DebugTraceRequestHeader, "true")
				}
				response := httptest.NewRecorder()
				rt.ServeHTTP(response, req)
				if response.Code != http.StatusBadRequest || called {
					t.Fatalf("malformed Unicode dispatched: status=%d called=%t", response.Code, called)
				}
			}
		})
	}
}

func TestJSONRequestOwnsEscapedKeysAndRawValues(t *testing.T) {
	const body = `{"\u0024api":"text","n\u0061me":"\uD800\uDC00","raw":{"n":123456789012345678901234567890}}`
	got, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if _, err := ParseRequest(makeReq(`{"$api":"xxxx","other":"replacement","raw":{"n":0}}`)); err != nil {
			t.Fatal(err)
		}
	}
	if got.API != "text" || string(got.Params["name"]) != `"\uD800\uDC00"` ||
		string(got.Params["raw"]) != `{"n":123456789012345678901234567890}` {
		t.Fatal("pooled request parsing changed an owned result")
	}
}

func TestRawRequestTransfersOwnedParameterMap(t *testing.T) {
	const body = `{"$id":18446744073709551615,"$api":"text","$select":"id","$filters":[],"$sorters":[],"page":2,"pageSize":10,"value":null,"raw":{"n":123456789012345678901234567890}}`
	raw, err := jsonutil.ParseObject([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	req, err := parseRawRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if req.API != "text" || req.Page != 2 || req.PageSize != 10 || len(req.Select) != 1 {
		t.Fatalf("metadata lost during ownership transfer: %+v", req)
	}
	if len(raw) != 2 || string(raw["value"]) != "null" || string(raw["raw"]) != `{"n":123456789012345678901234567890}` {
		t.Fatalf("protocol fields retained or parameters changed: %v", raw)
	}
	// The internal caller relinquishes the map; no second parameter map is built.
	req.Params["owned"] = json.RawMessage("true")
	if string(raw["owned"]) != "true" {
		t.Fatal("request copied rather than adopted its owned parameter map")
	}
}

func TestRawRequestFailureDoesNotConsumeEnvelope(t *testing.T) {
	for _, extra := range []string{`"$api":null`, `"$api":""`, `"$select":null`, `"page":null`, `"$sorters":[{}]`} {
		raw, err := jsonutil.ParseObject([]byte(`{"$id":1,"$api":"text","value":1,` + extra + `}`))
		if err != nil {
			t.Fatal(err)
		}
		count := len(raw)
		if req, err := parseRawRequest(raw); err == nil || req != nil {
			t.Fatalf("invalid envelope accepted: %s", extra)
		}
		if len(raw) != count || string(raw["$id"]) != "1" || string(raw["value"]) != "1" {
			t.Fatal("failed parsing partially consumed the envelope")
		}
	}
}

func TestJSONRequestDuplicateControlsRemainLastWins(t *testing.T) {
	req, err := ParseRequest(makeReq(`{"$api":null,"\u0024api":"text","$select":null,"$select":"id","page":null,"page":3,"value":false,"v\u0061lue":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.API != "text" || req.Page != 3 || len(req.Select) != 1 || len(req.Params) != 1 || string(req.Params["value"]) != "null" {
		t.Fatalf("duplicate controls changed semantics: %+v", req)
	}
}
