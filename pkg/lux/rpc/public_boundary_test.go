package rpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/h2transport"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"golang.org/x/net/http2"
	"nhooyr.io/websocket"
)

func TestInternalDispatchCannotBeForgedAcrossPublicTransports(t *testing.T) {
	router, privateCalls := boundaryRouter(t)
	internal := NewClient(startTestRPCServer(t, NewServer(router)))
	t.Cleanup(internal.Close)
	server := boundaryHTTPServer(t, router)
	for _, binary := range []bool{false, true} {
		for _, major := range []int{1, 2} {
			client := boundaryHTTPClient(t, server, major)
			for range 8 {
				boundaryInternalCall(t, internal)
				boundaryHTTPCall(t, client, server.URL, major, binary, 1)
				boundaryHTTPCall(t, client, server.URL, major, binary, 2)
			}
		}
		boundaryWebSocketCalls(t, server, internal, binary)
	}
	if got := privateCalls.Load(); got != 48 {
		t.Fatalf("private handler called outside its 48 trusted RPC requests: %d", got)
	}
}

func boundaryRouter(t *testing.T) (*api.Router, *atomic.Int32) {
	t.Helper()
	router := api.NewRouter()
	privateCalls := new(atomic.Int32)
	for index, name := range []string{"private", "svc:public"} {
		definition := &schema.API{ID: index + 1, Name: name, Internal: index == 0, ReturnType: "Boolean",
			Params: []schema.Param{{ID: 1, Name: "Internal", Type: schema.FieldBool}}}
		if err := router.Schema.RegisterAPI(definition); err != nil {
			t.Fatal(err)
		}
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	handler := func(_ context.Context, req *api.Request) error {
		value, err := req.ParamBool("Internal")
		if err != nil || !value {
			return errors.New("forged Internal input was not read as an ordinary parameter")
		}
		if req.API == "private" {
			privateCalls.Add(1)
		}
		req.Buf.B = codec.AppendBool(req.Buf.B, req.Internal)
		return nil
	}
	router.HandleInternal("private", handler)
	router.Handle("svc:public", handler)
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router, privateCalls
}

func boundaryHTTPServer(t *testing.T, router *api.Router) *httptest.Server {
	t.Helper()
	transport, err := h2transport.NewServer(router)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(router)
	if err := transport.ConfigureHTTPServer(server.Config); err != nil {
		t.Fatal(err)
	}
	server.TLS = server.Config.TLSConfig.Clone()
	server.StartTLS()
	t.Cleanup(func() { _ = transport.Close(); server.Close() })
	return server
}

func boundaryHTTPClient(t *testing.T, server *httptest.Server, major int) *http.Client {
	t.Helper()
	config := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if major == 2 {
		transport := &http2.Transport{TLSClientConfig: config}
		t.Cleanup(transport.CloseIdleConnections)
		return &http.Client{Transport: transport, Timeout: 5 * time.Second}
	}
	transport := &http.Transport{TLSClientConfig: config, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func boundaryInternalCall(t *testing.T, client *Client) {
	t.Helper()
	body, err := client.Call(1, []byte{1, 1, 0})
	if err != nil || !bytes.Equal(body, []byte{1}) {
		t.Fatalf("trusted dispatch did not retain its internal flag: %x, %v", body, err)
	}
}

func boundaryRequestBody(id int, binary bool) []byte {
	if binary {
		return []byte{byte(id), 0, 1, 1, 0}
	}
	name := "private"
	if id == 2 {
		name = "svc:public"
	}
	return []byte(`{"$api":"` + name + `","Internal":true}`)
}

func boundaryHTTPCall(t *testing.T, client *http.Client, endpoint string, major int, binary bool, id int) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), "POST", endpoint, bytes.NewReader(boundaryRequestBody(id, binary)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Luxo-Internal", "true")
	if binary {
		request.Header.Set("X-Luxo-Mode", "binary")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	wantStatus, want := 404, []byte(nil)
	if id == 2 {
		wantStatus, want = 200, []byte(`{"data":false}`)
		if binary {
			want = []byte{0}
		}
	}
	if err != nil || response.ProtoMajor != major || response.StatusCode != wantStatus || id == 2 && !bytes.Equal(body, want) {
		t.Fatalf("HTTP/%d binary=%v id=%d: protocol=%s status=%d body=%q err=%v", major, binary, id, response.Proto, response.StatusCode, body, err)
	}
}

func boundaryWebSocketCalls(t *testing.T, server *httptest.Server, internal *Client, binary bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := boundaryHTTPClient(t, server, 1)
	conn, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(server.URL, "https"), &websocket.DialOptions{
		HTTPClient: client, HTTPHeader: http.Header{"X-Luxo-Internal": []string{"true"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for sequence := 1; sequence <= 16; sequence += 2 {
		boundaryInternalCall(t, internal)
		for id := 1; id <= 2; id++ {
			kind, body := websocket.MessageText, boundaryRequestBody(id, false)
			if binary {
				kind, body = websocket.MessageBinary, append([]byte{1, byte(sequence + id - 1)}, boundaryRequestBody(id, true)...)
			} else {
				body = append([]byte(`{"$id":`+strconv.Itoa(sequence+id-1)+`,`), body[1:]...)
			}
			if err := conn.Write(ctx, kind, body); err != nil {
				t.Fatal(err)
			}
			gotKind, response, err := conn.Read(ctx)
			if err != nil || gotKind != kind {
				t.Fatalf("WS binary=%v id=%d read: %v %v", binary, id, gotKind, err)
			}
			boundaryWSResponse(t, response, binary, sequence+id-1, id)
		}
	}
}

func boundaryWSResponse(t *testing.T, body []byte, binary bool, sequence, id int) {
	t.Helper()
	if !binary {
		var response struct {
			ID   int   `json:"$id"`
			Code int   `json:"code"`
			Data *bool `json:"data"`
		}
		if err := json.Unmarshal(body, &response); err != nil || response.ID != sequence {
			t.Fatalf("WS JSON response %q: %v", body, err)
		}
		if id == 1 && (response.Code != 404 || response.Data != nil) || id == 2 && (response.Code != 0 || response.Data == nil || *response.Data) {
			t.Fatalf("WS JSON forged internal flag or exposed route: %q", body)
		}
		return
	}
	if len(body) < 3 || int(body[1]) != sequence {
		t.Fatalf("WS Binary header: %x", body)
	}
	if id == 2 {
		if !bytes.Equal(body, []byte{2, byte(sequence), 0}) {
			t.Fatalf("WS Binary forged internal flag: %x", body)
		}
		return
	}
	failure, err := api.DecodeBinaryError(body[2:], 0)
	if body[0] != 3 || err != nil || failure.Code != 404 {
		t.Fatalf("WS Binary exposed internal route: %x %v", body, err)
	}
}

func TestSchemaFreeRouterStreamBoundary(t *testing.T) {
	for _, mode := range []string{"lrpc", "json", "binary"} {
		t.Run(mode, func(t *testing.T) {
			router := schemaFreeStreamRouter(t)
			if mode == "lrpc" {
				schemaFreeRPCStreams(t, router)
				return
			}
			server := httptest.NewServer(router)
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			for _, frame := range schemaFreeWSFrames(mode == "binary") {
				if err := conn.Write(ctx, frame.kind, frame.request); err != nil {
					t.Fatal(err)
				}
				if frame.responses == nil {
					requireSchemaFreeWSFailure(t, ctx, conn, frame.kind)
					continue
				}
				for _, want := range frame.responses {
					kind, got, err := conn.Read(ctx)
					if err != nil || kind != frame.kind || !bytes.Equal(got, want) {
						t.Fatalf("request=%q response=%q want=%q kind=%d err=%v", frame.request, got, want, kind, err)
					}
				}
			}
		})
	}
}

func schemaFreeStreamRouter(t *testing.T) *api.Router {
	t.Helper()
	router := api.NewRouter()
	router.Schema = nil
	for index, name := range []string{"regular", "native"} {
		if err := router.Registry.Register(name, index+1); err != nil {
			t.Fatal(err)
		}
		if err := router.Registry.RegisterParams(name, nil); err != nil {
			t.Fatal(err)
		}
	}
	router.Handle("regular", func(_ context.Context, req *api.Request) error {
		if req.BinaryMode {
			req.Buf.B = codec.AppendSvarint(req.Buf.B, 7)
		} else {
			req.Buf.AppendString("7")
		}
		return nil
	})
	router.HandleStreamNative("native", func(context.Context, *api.StreamParams, any, *api.Stream) error { return nil })
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router
}

func schemaFreeRPCStreams(t *testing.T, router *api.Router) {
	t.Helper()
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := client.SubscribeContext(ctx, "", 1, nil, nil)
	failure, ok := errors.AsType[*luxerrors.AppError](err)
	if sub != nil || !ok || failure.Code != 400 {
		t.Fatalf("non-stream API did not return BadRequest: %v %v", sub, err)
	}
	if data, err := client.Call(1, nil); err != nil || !bytes.Equal(data, []byte{14}) {
		t.Fatalf("rejected stream damaged ordinary calls: %x %v", data, err)
	}
	sub, err = client.SubscribeContext(ctx, "", 2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case data, open := <-sub.Messages():
		if open || sub.Err() != nil {
			t.Fatalf("registered native stream did not complete normally: %x %v", data, sub.Err())
		}
	case <-ctx.Done():
		t.Fatal("registered native stream did not complete", ctx.Err())
	}
}

type schemaFreeWSFrame struct {
	kind      websocket.MessageType
	request   []byte
	responses [][]byte
}

func schemaFreeWSFrames(binary bool) []schemaFreeWSFrame {
	if binary {
		return []schemaFreeWSFrame{
			{websocket.MessageBinary, []byte{0x10, 1, 1, 0, 0}, nil},
			{websocket.MessageBinary, []byte{1, 2, 1, 0, 0}, [][]byte{{2, 2, 14}}},
			{websocket.MessageBinary, []byte{0x10, 3, 2, 0, 0}, [][]byte{{0x13, 3}, {0x15, 3}}},
		}
	}
	return []schemaFreeWSFrame{
		{websocket.MessageText, []byte(`{"$sub":"regular","$id":1}`), nil},
		{websocket.MessageText, []byte(`{"$api":"regular","$id":2}`), [][]byte{[]byte(`{"$id":2,"data":7}`)}},
		{websocket.MessageText, []byte(`{"$sub":"native","$id":3}`), [][]byte{[]byte(`{"$sub":3,"ok":true}`), []byte(`{"$end":3,"ok":true}`)}},
	}
}

func requireSchemaFreeWSFailure(t *testing.T, ctx context.Context, conn *websocket.Conn, wantKind websocket.MessageType) {
	t.Helper()
	kind, body, err := conn.Read(ctx)
	if err != nil || kind != wantKind {
		t.Fatalf("non-stream subscription response: kind=%d body=%q err=%v", kind, body, err)
	}
	if kind == websocket.MessageBinary {
		if len(body) < 3 || body[0] != 0x14 || body[1] != 1 {
			t.Fatalf("non-stream subscription error header: %x", body)
		}
		failure, err := api.DecodeBinaryError(body[2:], 0)
		if err != nil || failure.Code != 400 {
			t.Fatalf("non-stream subscription error: %x %v", body, err)
		}
		return
	}
	var response struct {
		ID   int `json:"$sub"`
		Code int `json:"code"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.ID != 1 || response.Code != 400 {
		t.Fatalf("non-stream subscription error: %q %v", body, err)
	}
}
