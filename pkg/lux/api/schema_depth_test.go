package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

type schemaDepthCall struct {
	name   string
	body   string
	status int
	want   string
}

func schemaDepthRow(depth, generic int) []byte {
	e := codec.NewEncoder(nil)
	e.WriteVarint(0)
	if depth > 1 {
		e.WriteFieldHeader(1)
		e.WritePresent()
		e.WriteBytes(schemaDepthRow(depth-1, generic))
	} else {
		e.WriteFieldHeader(2)
		e.WriteBytes([]byte(strings.Repeat("[", generic) + "0" + strings.Repeat("]", generic)))
	}
	e.WriteEnd()
	return e.Bytes()
}

func schemaDepthJSON(depth, generic int) string {
	raw := `{"payload":` + strings.Repeat("[", generic) + "0" + strings.Repeat("]", generic) + "}"
	for level := 1; level < depth; level++ {
		raw = `{"child":` + raw + "}"
	}
	return raw
}

func schemaDepthRouter(t *testing.T) (*Router, []schemaDepthCall, *atomic.Int32) {
	t.Helper()
	rt := NewRouter()
	if err := rt.Schema.RegisterType(&schema.TypeDecl{Name: "DepthNode", Fields: []schema.Field{
		{ID: 1, Name: "child", Type: schema.FieldModel, TypeName: "DepthNode", Nullable: true, Relation: true},
		{ID: 2, Name: "payload", Type: schema.FieldJSON},
	}}); err != nil {
		t.Fatal(err)
	}
	var calls []schemaDepthCall
	for _, sample := range []struct {
		name           string
		depth, generic int
		status         int
	}{
		{"legal", 64, 128, 200}, {"model65", 65, 128, 500}, {"json129", 1, 129, 500},
	} {
		for _, kind := range []string{"row", "column", "page"} {
			name := sample.name + kind
			wire := schemaDepthRow(sample.depth, sample.generic)
			if kind != "row" {
				wire = append([]byte{1}, wire...)
			}
			if kind == "page" {
				for range 3 {
					wire = codec.AppendSvarint(wire, 1)
				}
			}
			if err := rt.Schema.RegisterAPI(&schema.API{ID: len(calls) + 1, Name: name, ReturnType: "DepthNode", ReturnList: kind != "row", Paginated: kind == "page"}); err != nil {
				t.Fatal(err)
			}
			rt.Handle(name, func(_ context.Context, req *Request) error { req.Buf.B = append(req.Buf.B, wire...); return nil })
			want := schemaDepthJSON(sample.depth, sample.generic)
			if kind != "row" {
				want = "[" + want + "]"
			}
			if kind == "page" {
				want = `{"items":` + want + `,"total":1,"page":1,"pageSize":1}`
			}
			calls = append(calls, schemaDepthCall{name, `{"$api":"` + name + `","$select":"payload"}`, sample.status, want})
			got, err := rt.StreamPayloadJSON(name, wire)
			if sample.status == 200 {
				if err != nil || !json.Valid(got) {
					t.Fatal("stream converter rejected legal budget", err)
				}
			} else if err == nil || len(got) != 0 {
				t.Fatal("stream converter exposed an invalid payload")
			}
		}
	}
	if err := rt.Schema.RegisterAPI(&schema.API{ID: 10, Name: "input", ReturnType: "Int", Params: []schema.Param{{ID: 1, Name: "input", Type: schema.FieldModel, TypeName: "DepthNode"}}}); err != nil {
		t.Fatal(err)
	}
	count := new(atomic.Int32)
	rt.Handle("input", func(_ context.Context, req *Request) error {
		count.Add(1)
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 7)
		return nil
	})
	for _, sample := range []struct {
		name, raw string
		status    int
	}{
		{"inputlegal", schemaDepthJSON(64, 128), 200},
		{"inputmodel65", schemaDepthJSON(65, 128), 400},
		{"inputjson129", schemaDepthJSON(1, 129), 400},
		{"inputdiscarded65", `{"child":` + schemaDepthJSON(64, 128) + `,"child":null}`, 400},
		{"inputdiscardedBusiness", `{"child":{"payload":0,"unknown":false},"child":null}`, 200},
	} {
		calls = append(calls, schemaDepthCall{sample.name, `{"$api":"input","input":` + sample.raw + "}", sample.status, "7"})
	}
	if err := rt.Registry.RegisterSchemaAPIs(rt.Schema); err != nil {
		t.Fatal(err)
	}
	return rt, calls, count
}

func TestSchemaDepthRealHTTPAndWebSocket(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2%t", h2), func(t *testing.T) {
			router, calls, count := schemaDepthRouter(t)
			server := httptest.NewUnstartedServer(router)
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			for _, call := range calls {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, strings.NewReader(call.body))
				if err != nil {
					t.Fatal(err)
				}
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(call.name, err)
				}
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				wantMajor := 1
				if h2 {
					wantMajor = 2
				}
				if readErr != nil || response.StatusCode != call.status || response.ProtoMajor != wantMajor || !json.Valid(body) {
					t.Fatalf("%s: HTTP%d status%d: %s / %v", call.name, response.ProtoMajor, response.StatusCode, body, readErr)
				}
				checkSchemaDepthReply(t, call, body)
			}
			t.Logf("HTTP2=%t: %d complete requests", h2, len(calls))
			if count.Load() != 2 {
				t.Fatal("invalid input reached handler", count.Load())
			}
			if !h2 {
				checkSchemaDepthWS(t, server, calls)
				if count.Load() != 4 {
					t.Fatal("WS invalid input reached handler", count.Load())
				}
			}
		})
	}
}

func checkSchemaDepthWS(t *testing.T, server *httptest.Server, calls []schemaDepthCall) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(server.URL, "https"), &websocket.DialOptions{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for _, call := range calls {
		body := append([]byte(`{"$id":1,`), []byte(call.body[1:])...)
		if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
			t.Fatal(err)
		}
		kind, payload, err := conn.Read(ctx)
		if err != nil || kind != websocket.MessageText || !json.Valid(payload) {
			t.Fatal(call.name, string(payload), err)
		}
		checkSchemaDepthReply(t, call, payload)
		if call.status == 200 {
			if !bytes.Contains(payload, []byte(`"data":`)) {
				t.Fatal("missing WS success", call.name, string(payload))
			}
		} else if !bytes.Contains(payload, []byte(fmt.Sprintf(`"code":%d`, call.status))) {
			t.Fatal("missing WS error", call.name, string(payload))
		}
	}
	t.Logf("WSS: %d complete calls", len(calls))
}

func checkSchemaDepthReply(t *testing.T, call schemaDepthCall, payload []byte) {
	t.Helper()
	var reply struct {
		Data json.RawMessage
		Code int
	}
	if err := json.Unmarshal(payload, &reply); err != nil {
		t.Fatal(call.name, err)
	}
	if call.status == 200 {
		if string(reply.Data) != call.want {
			t.Fatalf("%s: response value changed: %s", call.name, reply.Data)
		}
	} else if len(reply.Data) > 0 || reply.Code != call.status {
		t.Fatalf("%s: partial data or wrong error: %s", call.name, payload)
	}
}
