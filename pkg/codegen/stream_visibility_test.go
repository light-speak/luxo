package codegen

import (
	"strings"
	"testing"
)

func TestGeneratedNativeStreamVisibilityTransports(t *testing.T) {
	var proxy strings.Builder
	writeGatewayRPCStreamProxy(&proxy, []moduleInfo{{nativeStreams: []string{"watch", "watchList"}}})
	runGeneratedFixture(t, nativeVisibilitySource, nativeVisibilityAssertions+proxy.String(), nativeVisibilityStableIDs())
}

func nativeVisibilityStableIDs() StableIDs {
	return StableIDs{
		APIs:      map[string]int{"watch": 1, "watchList": 2},
		APIParams: map[string]map[string]int{"watch": {"limit": 1}, "watchList": {"limit": 1}},
		ModelFields: map[string]map[string]int{
			"User": {"id": 1}, "Other": {"id": 1},
			"Detail":  {"secret": 1, "phone": 2},
			"Profile": {"id": 1, "secret": 2, "zero": 3, "phone": 4, "detail": 5},
		},
	}
}

const nativeVisibilitySource = `
model User @noTime @withAuth(stores: [id], default: true) { id: Int @id }
model Other @noTime @withAuth(stores: [id]) { id: Int @id }
type Detail { secret: String? @visible(my.id == 42) phone: String @mask("###****####") }
type Profile {
 id: Int
 secret: String? @visible(my.id == 42)
 zero: String? @visible(my.id == 0)
 phone: String @mask("###****####")
 detail: Detail
}
api watch(limit: Int = 2): Profile @native @stream
api watchList(limit: Int = 2): [Profile] @native @stream
`

const nativeVisibilityAssertions = `package generated

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/luvia"
	"github.com/light-speak/luxo/pkg/lux/rpc"
	"net"
	"net/http"
	"net/http/httptest"
	"nhooyr.io/websocket"
	"testing"
	"time"
)

type visibilityResolver struct{}

func (*visibilityResolver) HandleWatch(_ context.Context, params *WatchStreamParams, _ any, stream *api.TypedStream[Profile]) error {
	if params.Limit != 2 {
		return fmt.Errorf("default limit changed: %d", params.Limit)
	}
	for id := int64(1); id <= params.Limit; id++ {
		if err := stream.Send(nativeVisibilityValue(id)); err != nil {
			return err
		}
	}
	return nil
}

func (*visibilityResolver) HandleWatchList(_ context.Context, params *WatchListStreamParams, _ any, stream *api.TypedStream[[]Profile]) error {
	if params.Limit != 2 {
		return fmt.Errorf("default limit changed: %d", params.Limit)
	}
	for range params.Limit {
		if err := stream.Send([]Profile{nativeVisibilityValue(1), nativeVisibilityValue(2)}); err != nil {
			return err
		}
	}
	return nil
}

func nativeVisibilityValue(id int64) Profile {
	secret, zero := "private-for-42", "private-for-zero"
	return Profile{Id: id, Secret: &secret, Zero: &zero, Phone: "13812341234", Detail: &Detail{Secret: &secret, Phone: "13812341234"}}
}

type visibilityCase struct {
	name, token, apiName string
	secret, zero         bool
}

func TestNativeVisibility(t *testing.T) {
	t.Setenv("JWT_SECRET", "native-stream-visibility")
	t.Setenv("JWT_EXPIRES", "1h")
	auth.ResetConfig()
	t.Cleanup(auth.ResetConfig)
	cfg, err := auth.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	tokens := []visibilityCase{{name: "anonymous"}, {name: "User42", secret: true}, {name: "User99"}, {name: "User0", zero: true}, {name: "Other42"}}
	for i, sign := range []func() (string, error){(&User{Id: 42}).CreateToken, (&User{Id: 99}).CreateToken, (&User{Id: 0}).CreateToken, (&Other{Id: 42}).CreateToken} {
		tokens[i+1].token, err = sign()
		if err != nil {
			t.Fatal(err)
		}
	}
	backend := newNativeVisibilityRouter(t)
	client := nativeVisibilityRPC(t, backend)
	gateway := newNativeVisibilityGateway(t, client)
	for _, apiName := range []string{"watch", "watchList"} {
		for _, sample := range tokens {
			sample.apiName = apiName
			t.Run(apiName+"/"+sample.name+"/LRPC", func(t *testing.T) {
				for _, minimal := range []bool{false, true} {
					t.Run(fmt.Sprint("minimal=", minimal), func(t *testing.T) { checkNativeVisibilityRPC(t, backend, client, sample, minimal) })
				}
			})
		}
		for _, route := range []struct {
			name   string
			router *api.Router
		}{{"direct", backend}, {"gateway", gateway}} {
			server := httptest.NewServer(luvia.AuthMiddleware(cfg, route.router))
			defer server.Close()
			for _, binary := range []bool{false, true} {
				for _, sample := range tokens {
					sample.apiName = apiName
					t.Run(fmt.Sprintf("%s/%s/%s/binary=%v", apiName, route.name, sample.name, binary), func(t *testing.T) {
						checkNativeVisibilityWS(t, route.router, server.URL, sample, binary)
					})
				}
			}
		}
	}
}

func newNativeVisibilityGateway(t *testing.T, client *rpc.Client) *api.Router {
	t.Helper()
	gateway := luvia.New().Router
	if err := RegisterSchema(gateway.Schema); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Registry.RegisterSchemaAPIs(gateway.Schema); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"watch", "watchList"} {
		gateway.HandleStreamForwarded(name, rpcStreamProxy(client, gateway, name))
	}
	if err := gateway.Prepare(); err != nil {
		t.Fatal(err)
	}
	return gateway
}

func newNativeVisibilityRouter(t *testing.T) *api.Router {
	t.Helper()
	router := luvia.New().Router
	if err := RegisterSchema(router.Schema); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStreams(router, nil, &visibilityResolver{}); err != nil {
		t.Fatal(err)
	}
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router
}

func nativeVisibilityRPC(t *testing.T, router *api.Router) *rpc.Client {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(router)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	client := rpc.NewClient(listener.Addr().String())
	t.Cleanup(func() {
		client.Close()
		server.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return client
}

func nativeVisibilityMask(t *testing.T, minimal bool) []byte {
	t.Helper()
	fields := byte(31)
	if minimal {
		fields = 1
	}
	child, err := codec.AppendSelectionMask(nil, []byte{3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var children []codec.SelectionMaskChild
	if !minimal {
		children = []codec.SelectionMaskChild{{FieldID: 5, Mask: child}}
	}
	mask, err := codec.AppendSelectionMask(nil, []byte{fields}, children)
	if err != nil {
		t.Fatal(err)
	}
	return mask
}

func checkNativeVisibilityRPC(t *testing.T, router *api.Router, client *rpc.Client, sample visibilityCase, minimal bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := client.SubscribeContext(ctx, sample.token, router.Schema.APIs[sample.apiName].ID, nativeVisibilityMask(t, minimal), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	count := int64(0)
	for body := range sub.Messages() {
		count++
		text, err := router.StreamPayloadJSON(sample.apiName, body)
		if err != nil {
			t.Fatal(err)
		}
		requireNativeVisibility(t, text, body, sample, minimal, count)
	}
	if count != 2 || sub.Err() != nil {
		t.Fatalf("count=%d result=%v", count, sub.Err())
	}
}

func checkNativeVisibilityWS(t *testing.T, router *api.Router, url string, sample visibilityCase, binary bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	headers := make(http.Header)
	if sample.token != "" {
		headers.Set("Authorization", "Bearer "+sample.token)
	}
	conn, _, err := websocket.Dial(ctx, "ws"+url[4:], &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for i, minimal := range []bool{false, true} {
		if !t.Run(fmt.Sprint("minimal=", minimal), func(t *testing.T) {
			id := byte(i + 1)
			kind, body := nativeVisibilitySubscribe(t, router, sample.apiName, id, minimal, binary)
			if err := conn.Write(ctx, kind, body); err != nil {
				t.Fatal(err)
			}
			_, ack, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			requireNativeVisibilityControl(t, ack, id, binary, false)
			for sequence := int64(1); sequence <= 2; sequence++ {
				got, frame, err := conn.Read(ctx)
				if err != nil || got != kind {
					t.Fatal(got, err)
				}
				text := nativeVisibilityMessage(t, router, sample.apiName, frame, id, binary)
				requireNativeVisibility(t, text, frame, sample, minimal, sequence)
			}
			_, end, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			requireNativeVisibilityControl(t, end, id, binary, true)
		}) {
			return
		}
	}
}

func requireNativeVisibilityControl(t *testing.T, frame []byte, id byte, binary, terminal bool) {
	t.Helper()
	if binary {
		kind := api.BinaryFrameSubscribeSuccess
		if terminal {
			kind = api.BinaryFrameStreamComplete
		}
		if !bytes.Equal(frame, []byte{kind, id}) {
			t.Fatalf("control %x", frame)
		}
		return
	}
	key := "$sub"
	if terminal {
		key = "$end"
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(frame, &envelope); err != nil || string(envelope[key]) != fmt.Sprint(id) || string(envelope["ok"]) != "true" {
		t.Fatalf("control %s error=%v", frame, err)
	}
}

func nativeVisibilitySubscribe(t *testing.T, router *api.Router, name string, id byte, minimal, binary bool) (websocket.MessageType, []byte) {
	t.Helper()
	if binary {
		body, err := api.EncodeBinaryRequest(router.Schema.APIs[name].ID, nativeVisibilityMask(t, minimal), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return websocket.MessageBinary, append([]byte{api.BinaryFrameSubscribe, id}, body...)
	}
	selection := "id,secret,zero,phone,detail{secret,phone}"
	if minimal {
		selection = "id"
	}
	return websocket.MessageText, []byte(fmt.Sprintf("{\"$id\":%d,\"$sub\":%q,\"$select\":%q}", id, name, selection))
}

func nativeVisibilityMessage(t *testing.T, router *api.Router, name string, frame []byte, id byte, binary bool) []byte {
	t.Helper()
	if binary {
		if len(frame) < 3 || frame[0] != api.BinaryFrameStream || frame[1] != id {
			t.Fatalf("data %x", frame)
		}
		text, err := router.StreamPayloadJSON(name, frame[2:])
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(frame, &envelope); err != nil || string(envelope["$stream"]) != fmt.Sprint(id) || len(envelope["data"]) == 0 {
		t.Fatalf("data %s error=%v", frame, err)
	}
	return envelope["data"]
}

func requireNativeVisibility(t *testing.T, text, wire []byte, sample visibilityCase, minimal bool, id int64) {
	t.Helper()
	if bytes.Contains(wire, []byte("13812341234")) {
		t.Fatalf("raw phone leaked: %x", wire)
	}
	secret, zero := sample.secret && !minimal, sample.zero && !minimal
	if bytes.Contains(wire, []byte("private-for-42")) != secret || bytes.Contains(wire, []byte("private-for-zero")) != zero {
		t.Fatalf("private bytes for %s: %x", sample.name, wire)
	}
	if sample.apiName == "watchList" {
		var rows []json.RawMessage
		if err := json.Unmarshal(text, &rows); err != nil || len(rows) != 2 {
			t.Fatalf("list %s error=%v", text, err)
		}
		for i, row := range rows {
			requireNativeVisibilityRecord(t, row, sample, minimal, int64(i+1))
		}
		return
	}
	requireNativeVisibilityRecord(t, text, sample, minimal, id)
}

func requireNativeVisibilityRecord(t *testing.T, text []byte, sample visibilityCase, minimal bool, id int64) {
	t.Helper()
	var value struct {
		Id           int64
		Secret, Zero *string
		Phone        string
		Detail       *struct {
			Secret *string
			Phone  string
		}
	}
	if err := json.Unmarshal(text, &value); err != nil || value.Id != id {
		t.Fatalf("value %s error=%v", text, err)
	}
	secret, zero := sample.secret && !minimal, sample.zero && !minimal
	if minimal {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(text, &fields); err != nil || len(fields) != 1 {
			t.Fatalf("unselected fields: %s", text)
		}
		return
	}
	if (value.Secret != nil) != secret || (value.Zero != nil) != zero || value.Phone != "138****1234" || value.Detail == nil {
		t.Fatalf("visibility/mask for %s: %s", sample.name, text)
	}
	if (value.Detail.Secret != nil) != secret || value.Detail.Phone != "138****1234" {
		t.Fatalf("nested visibility/mask for %s: %s", sample.name, text)
	}
	if secret && (*value.Secret != "private-for-42" || *value.Detail.Secret != "private-for-42") {
		t.Fatalf("visible value changed: %s", text)
	}
	if zero && *value.Zero != "private-for-zero" {
		t.Fatalf("zero identity changed: %s", text)
	}
}
`
