package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedNativeStreamPreservesEncodingIdentity(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Name: "native.luxo", Models: []*ast.ModelDecl{{Name: "Post", Fields: []*ast.FieldDecl{testField("id", "Int")}}}, APIs: []*ast.ApiDecl{
		{Name: "watch", ReturnType: &ast.TypeRef{Name: "Post"}, Directives: []*ast.Directive{{Name: "native"}, {Name: "stream"}}},
	}}}}
	runGeneratedProtocolPackage(t, string(generateStreamFile(result, "generated")), `package generated
import("context";"fmt";"testing";"github.com/light-speak/luxo/pkg/lux/api")
type identityKey struct{}
type Post struct{expected any}
func(p Post)WriteLuxo(buf *api.ResponseBuf,_ []byte)error{
 if buf.Identity!=p.expected{return fmt.Errorf("stream encoding identity lost: got %v want %v",buf.Identity,p.expected)}
 buf.AppendByte(1);return nil
}
type identityResolver struct{}
func(*identityResolver)HandleWatch(_ context.Context,_ *WatchStreamParams,identity any,stream *api.TypedStream[Post])error{
 for range 2{if err:=stream.Send(Post{expected:identity});err!=nil{return err}};return nil
}
func TestNativeEncodingIdentity(t *testing.T){
 router:=api.NewRouter();router.IdentityExtractor=func(ctx context.Context)any{return ctx.Value(identityKey{})}
 if err:=RegisterStreams(router,nil,&identityResolver{});err!=nil{t.Fatal(err)}
 for _,identity:=range []any{int64(42),nil,int64(99)}{
  ctx:=t.Context();if identity!=nil{ctx=context.WithValue(ctx,identityKey{},identity)}
  sub,err:=router.OpenInternalStream(ctx,&api.Request{API:"watch",BinaryMode:true});if err!=nil{t.Fatal(err)}
  count:=0;for range sub.Messages(){count++};sub.Close()
  if err:=sub.Err();err!=nil||count!=2{t.Fatal("encoding failed",identity,count,err)}
 }
}
`)
}

func TestGeneratedNativeStreamReturnsTypedErrors(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Name: "native.luxo", APIs: []*ast.ApiDecl{
		{Name: "watch", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}, {Name: "stream"}}},
		{Name: "watchPrivate", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}, {Name: "stream"}, {Name: "auth"}}},
	}}}}
	runGeneratedProtocolPackage(t, string(generateStreamFile(result, "generated")), nativeStreamAssertions)
}

func TestGeneratedStreamBridgeDrainsBeforeTerminalResult(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import (
    "bytes"
    "context"
    "fmt"
    "sync"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
    "github.com/light-speak/luxo/pkg/lux/schema"
    "github.com/light-speak/luxo/pkg/lux/rpc"
    "github.com/light-speak/luxo/pkg/lux/luvia"
)
`)
	writeGatewayRPCStreamProxy(&source, []moduleInfo{{nativeStreams: []string{"watch"}}})
	writeGatewayProxy(&source)
	runGeneratedProtocolPackage(t, source.String(), streamBridgeAssertions)
}

const nativeStreamAssertions = `package generated
import (
    "context"
    "errors"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
    luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)
type nativeResolver struct{}
func (*nativeResolver) HandleWatch(ctx context.Context, params *WatchStreamParams, identity any, stream *api.TypedStream[int64]) error {
    if err := stream.Send(9223372036854775807); err != nil { return err }
    return luxerrors.Conflict
}
func (*nativeResolver) HandleWatchPrivate(ctx context.Context, params *WatchPrivateStreamParams, identity any, stream *api.TypedStream[int64]) error {
    return errors.New("anonymous subscriber reached private handler")
}
func TestNativeLifecycle(t *testing.T) {
    router := api.NewRouter()
    if err := RegisterStreams(router, nil, &nativeResolver{}); err != nil { t.Fatal(err) }
    for _, name := range []string{"watch", "watchPrivate"} {
        sub, err := router.OpenInternalStream(t.Context(), &api.Request{API:name})
        if name == "watchPrivate" {
            if sub != nil || !errors.Is(err,luxerrors.Unauthorized) {t.Fatalf("private stream accepted before authorization: %v",err)}
            continue
        }
        if err != nil { t.Fatal(err) }
        count := 0
        for payload := range sub.Messages() {
            dec := codec.NewDecoder(payload)
            if dec.ReadInt() != 9223372036854775807 || dec.Err() != nil || dec.Offset() != len(payload) {
                t.Fatalf("lost typed Int64 stream payload: %x", payload)
            }
            count++
        }
        sub.Close()
        want := luxerrors.Conflict
        wantCount := 1
        if count != wantCount || !errors.Is(sub.Err(), want) { t.Fatalf("%s: count=%d result=%v",name,count,sub.Err()) }
    }
}
`

const streamBridgeAssertions = `package generated
import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "net"
    "net/http/httptest"
    "strings"
    "testing"
    "time"
    "github.com/light-speak/luxo/pkg/lux/api"
    luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
    "github.com/light-speak/luxo/pkg/lux/rpc"
    "github.com/light-speak/luxo/pkg/lux/schema"
    "nhooyr.io/websocket"
)
const precisionData = "{\"id\":9223372036854775807,\"decimal\":0.123456789012345678901234567890}"
type rawErrorData struct{}
func (rawErrorData) AppendJSON(dst []byte) ([]byte,error) { return append(dst, precisionData...),nil }
func (rawErrorData) AppendText(dst []byte, field string) ([]byte,bool) { return dst,false }
func TestBridgeRejectsBeforeWebSocketACK(t *testing.T){
 for _,binary:=range []bool{false,true}{
  backend:=api.NewRouter();backend.Schema.RegisterAPI(&schema.API{ID:1,Name:"watch",Stream:true,ReturnType:"Int"});backend.Registry.RegisterSchemaAPIs(backend.Schema)
  backend.HandleStreamPrepared("watch",func(context.Context,*api.Request,any)(api.PreparedStreamHandler,error){return nil,luxerrors.New("Rejected",422,"error.rejected").WithData(rawErrorData{})})
  client:=startStreamBackend(t,backend)
  gateway:=api.NewRouter();gateway.Schema.RegisterAPI(&schema.API{ID:1,Name:"watch",Stream:true,ReturnType:"Int"});gateway.Registry.RegisterSchemaAPIs(gateway.Schema)
  gateway.HandleStreamForwarded("watch",rpcStreamProxy(client,gateway,"watch"))
  server:=httptest.NewServer(gateway);defer server.Close()
  ctx,cancel:=context.WithTimeout(t.Context(),3*time.Second);defer cancel()
  conn,_,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(server.URL,"http"),nil);if err!=nil{t.Fatal(err)};defer conn.CloseNow()
  mode,frame:=websocket.MessageText,[]byte("{\"$id\":1,\"$sub\":\"watch\"}")
  if binary{mode=websocket.MessageBinary;frame=[]byte{api.BinaryFrameSubscribe,1,1,0,0}}
  if err:=conn.Write(ctx,mode,frame);err!=nil{t.Fatal(err)}
  _,ack,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
  if binary{
   if len(ack)<3||ack[0]!=api.BinaryFrameSubscribeError{t.Fatalf("remote rejection acknowledged as success: %x",ack)}
   failure,err:=api.DecodeBinaryError(ack[2:],0);if err!=nil||failure.Code!=422{t.Fatalf("lost rejection: %v %v",failure,err)}
  }else if !bytes.Contains(ack,[]byte("\"code\":422"))||!bytes.Contains(ack,[]byte(precisionData)){t.Fatalf("remote rejection acknowledged as success: %s",ack)}
 }
}
func TestBridgeTerminalOrder(t *testing.T) {
    for _, failure := range []bool{false,true} {
        backend := api.NewRouter()
        backend.Registry.Register("watch",1)
        backend.Registry.RegisterParams("watch",nil)
        backend.HandleStreamNative("watch",func(_ context.Context,_ *api.StreamParams,_ any,stream *api.Stream) error {
            for i:=range 64 { if err:=stream.Send([]byte{byte(i)});err!=nil{return err} }
            if failure {return luxerrors.New("Rejected",422,"error.rejected").WithData(rawErrorData{})}
            return nil
        })
        client := startStreamBackend(t,backend)
        gateway:=api.NewRouter()
        gateway.Schema.RegisterAPI(&schema.API{ID:1,Name:"watch",Stream:true})
        gateway.HandleStreamForwarded("watch",rpcStreamProxy(client,gateway,"watch"))
        ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
        sub,err:=gateway.OpenInternalStream(ctx,&api.Request{API:"watch"})
        if err!=nil{cancel();t.Fatal(err)}
        count:=0
        for payload:=range sub.Messages(){if len(payload)!=1||payload[0]!=byte(count){t.Fatalf("message %d: %v",count,payload)};count++}
        sub.Close();cancel()
        if count!=64{t.Fatalf("only %d messages relayed",count)}
        if failure {
            app,ok:=errors.AsType[*luxerrors.AppError](sub.Err())
            if !ok||app.Code!=422||app.Data==nil{t.Fatalf("error lost: %v",sub.Err())}
            raw,err:=app.Data.AppendJSON(nil)
            if err!=nil||string(raw)!=precisionData{t.Fatalf("raw precision lost: %s, %v",raw,err)}
        }else if sub.Err()!=nil{t.Fatal(sub.Err())}
    }
}
func TestBridgeRejectsEmptyRPCStreamData(t *testing.T) {
 backend:=api.NewRouter()
 if err:=backend.Registry.Register("watch",1);err!=nil{t.Fatal(err)}
 backend.HandleStreamNative("watch",func(_ context.Context,_ *api.StreamParams,_ any,stream *api.Stream)error{
  if err:=stream.Send([]byte{0});err!=nil{return err};return stream.Send(nil)
 })
 client:=startStreamBackend(t,backend)
 gateway:=api.NewRouter()
 if err:=gateway.Schema.RegisterAPI(&schema.API{ID:1,Name:"watch",Stream:true,ReturnType:"Int"});err!=nil{t.Fatal(err)}
 gateway.HandleStreamForwarded("watch",rpcStreamProxy(client,gateway,"watch"))
 ctx,cancel:=context.WithTimeout(t.Context(),3*time.Second);defer cancel()
 sub,err:=gateway.OpenInternalStream(ctx,&api.Request{API:"watch"});if err!=nil{t.Fatal(err)};defer sub.Close()
 count:=0
 for message:=range sub.Messages(){if count!=0||!bytes.Equal(message,[]byte{0}){t.Fatalf("invalid data forwarded: %x",message)};count++}
 if count!=1||sub.Err()==nil{t.Fatalf("protocol failure became normal completion: count=%d error=%v",count,sub.Err())}
 if gateway.Streams.SubCount("watch")!=0{t.Fatal("failed forwarded stream retained subscription")}
}

func TestBridgePoolQuotaAcrossWebSocket(t *testing.T) {
	for _, sample := range []struct {
		name   string
		binary bool
	}{{"json", false}, {"binary", true}} {
		t.Run(sample.name, func(t *testing.T) { testBridgePoolQuota(t, sample.binary) })
	}
}
func testBridgePoolQuota(t *testing.T, binary bool) {
	gateway, deliveries := quotaBridgeRouter(t)
	server := httptest.NewServer(gateway)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	first := bridgeQuotaExchange(t, ctx, conn, binary, "{\"$id\":1,\"$sub\":\"watch\"}", []byte{api.BinaryFrameSubscribe, 1, 1, 0, 0})
	bridgeQuotaACK(t, first, binary, 1)
	firstOutput := bridgeQuotaDelivery(t, ctx, deliveries)
	rejected := bridgeQuotaExchange(t, ctx, conn, binary, "{\"$id\":2,\"$sub\":\"watch\"}", []byte{api.BinaryFrameSubscribe, 2, 1, 0, 0})
	bridgeQuotaBusy(t, rejected, binary)
	ping := bridgeQuotaExchange(t, ctx, conn, binary, "{\"$id\":9,\"$api\":\"ping\"}", []byte{api.BinaryFrameCallRequest, 9, 2, 0, 0})
	if binary {
		if !bytes.Equal(ping, []byte{api.BinaryFrameCallSuccess, 9, 14}) {
			t.Fatalf("reserved unary slot failed: %x", ping)
		}
	} else {
		response := bridgeQuotaJSON(t, ping)
		bridgeQuotaJSONInt(t, response, "$id", 9)
		bridgeQuotaJSONInt(t, response, "data", 7)
	}
	firstOutput <- []byte{14}
	bridgeQuotaData(t, bridgeQuotaRead(t, ctx, conn, binary), binary, 1, 7)
	// The same socket processes unsubscribe synchronously before the next subscribe.
	bridgeQuotaWrite(t, ctx, conn, binary, "{\"$unsub\":1}", []byte{api.BinaryFrameUnsubscribe, 1})
	restored := bridgeQuotaExchange(t, ctx, conn, binary, "{\"$id\":3,\"$sub\":\"watch\"}", []byte{api.BinaryFrameSubscribe, 3, 1, 0, 0})
	bridgeQuotaACK(t, restored, binary, 3)
	restoredOutput := bridgeQuotaDelivery(t, ctx, deliveries)
	restoredOutput <- []byte{18}
	bridgeQuotaData(t, bridgeQuotaRead(t, ctx, conn, binary), binary, 3, 9)
}
func quotaBridgeRouter(t *testing.T) (*api.Router, <-chan chan []byte) {
	t.Helper()
	backend, gateway := api.NewRouter(), api.NewRouter()
	for _, router := range []*api.Router{backend, gateway} {
		for _, definition := range []*schema.API{{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"}, {ID: 2, Name: "ping", ReturnType: "Int"}} {
			if err := router.Schema.RegisterAPI(definition); err != nil {
				t.Fatal(err)
			}
		}
		if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
			t.Fatal(err)
		}
	}
	deliveries := make(chan chan []byte, 2)
	backend.HandleStreamNative("watch", func(ctx context.Context, _ *api.StreamParams, _ any, stream *api.Stream) error {
		output := make(chan []byte, 1)
		select {
		case deliveries <- output:
		case <-ctx.Done():
			return ctx.Err()
		}
		for {
			select {
			case value := <-output:
				if err := stream.Send(value); err != nil {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
	backend.Handle("ping", func(_ context.Context, req *api.Request) error { req.Buf.WriteLuxoInt(7); return nil })
	options := rpc.DefaultPoolOptions()
	options.MaxIdle, options.MaxOpen, options.UnaryReserve = 2, 2, 1
	client := startStreamBackend(t, backend, options)
	gateway.HandleStreamForwarded("watch", rpcStreamProxy(client, gateway, "watch"))
	gateway.Handle("ping", proxyHandler(client, gateway.Schema, "ping", map[string]*rpc.Client{"quota": client}))
	return gateway, deliveries
}
func bridgeQuotaDelivery(t *testing.T, ctx context.Context, deliveries <-chan chan []byte) chan []byte {
	t.Helper()
	select {
	case output := <-deliveries:
		return output
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return nil
	}
}
func bridgeQuotaExchange(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, text string, wire []byte) []byte {
	t.Helper()
	bridgeQuotaWrite(t, ctx, conn, binary, text, wire)
	return bridgeQuotaRead(t, ctx, conn, binary)
}
func bridgeQuotaWrite(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, text string, wire []byte) {
	t.Helper()
	kind, data := websocket.MessageText, []byte(text)
	if binary {
		kind, data = websocket.MessageBinary, wire
	}
	if err := conn.Write(ctx, kind, data); err != nil {
		t.Fatal(err)
	}
}
func bridgeQuotaRead(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool) []byte {
	t.Helper()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := websocket.MessageText
	if binary {
		want = websocket.MessageBinary
	}
	if kind != want {
		t.Fatalf("unexpected WebSocket frame kind %v", kind)
	}
	return data
}
func bridgeQuotaACK(t *testing.T, data []byte, binary bool, id byte) {
	t.Helper()
	if binary {
		if !bytes.Equal(data, []byte{api.BinaryFrameSubscribeSuccess, id}) {
			t.Fatalf("subscription ACK: %x", data)
		}
		return
	}
	response := bridgeQuotaJSON(t, data)
	bridgeQuotaJSONInt(t, response, "$sub", int(id))
	var accepted bool
	if err := json.Unmarshal(response["ok"], &accepted); err != nil || !accepted {
		t.Fatalf("subscription ACK: %s (%v)", data, err)
	}
}
func bridgeQuotaBusy(t *testing.T, data []byte, binary bool) {
	t.Helper()
	if binary {
		if len(data) < 3 || data[0] != api.BinaryFrameSubscribeError || data[1] != 2 {
			t.Fatalf("pool overflow acknowledged: %x", data)
		}
		failure, err := api.DecodeBinaryError(data[2:], 0)
		if err != nil || failure.Code != 503 || failure.Name != "LRPCPoolBusy" {
			t.Fatalf("lost pool rejection: %v %v", failure, err)
		}
	} else {
		response := bridgeQuotaJSON(t, data)
		bridgeQuotaJSONInt(t, response, "$sub", 2)
		bridgeQuotaJSONInt(t, response, "code", 503)
		var name string
		if err := json.Unmarshal(response["error"], &name); err != nil || name != "LRPCPoolBusy" {
			t.Fatalf("lost pool rejection: %s (%v)", data, err)
		}
	}
}
func bridgeQuotaData(t *testing.T, data []byte, binary bool, id byte, value int) {
	t.Helper()
	if binary {
		if !bytes.Equal(data, []byte{api.BinaryFrameStream, id, byte(value * 2)}) {
			t.Fatalf("active stream lost: %x", data)
		}
		return
	}
	response := bridgeQuotaJSON(t, data)
	bridgeQuotaJSONInt(t, response, "$stream", int(id))
	bridgeQuotaJSONInt(t, response, "data", value)
}
func bridgeQuotaJSON(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var response map[string]json.RawMessage
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("WebSocket JSON frame: %s (%v)", data, err)
	}
	return response
}
func bridgeQuotaJSONInt(t *testing.T, response map[string]json.RawMessage, key string, want int) {
	t.Helper()
	var value int
	if err := json.Unmarshal(response[key], &value); err != nil || value != want {
		t.Fatalf("WebSocket JSON %s: got %s want %d (%v)", key, response[key], want, err)
	}
}
func startStreamBackend(t *testing.T,router *api.Router, selected ...rpc.PoolOptions)*rpc.Client{
    t.Helper()
    listener,err:=net.Listen("tcp","127.0.0.1:0")
    if err!=nil{t.Fatal(err)}
    server:=rpc.NewServer(router)
    done:=make(chan error,1)
    go func(){done<-server.Serve(listener)}()
    options:=rpc.DefaultPoolOptions()
    if len(selected)>0{options=selected[0]}
    client,err:=rpc.NewNamedClientWithOptions("stream-fixture",listener.Addr().String(),options)
    if err!=nil{server.Close();<-done;t.Fatal(err)}
    t.Cleanup(func(){client.Close();server.Close();if err:=<-done;err!=nil{t.Error(err)}})
    return client
}
func TestBridgeMissingRouteAndCancellation(t *testing.T){
    router:=api.NewRouter()
    router.Schema.RegisterAPI(&schema.API{ID:1,Name:"watch",Stream:true})
    client:=rpc.NewClient("127.0.0.1:1")
    defer client.Close()
    for _,prepare:=range []api.StreamForwardPrepareFunc{rpcStreamProxy(nil,router,"watch"),rpcStreamProxy(client,router,"missing")}{
        if _,err:=prepare(t.Context(),&api.Request{API:"watch"},nil);err==nil{t.Fatal("missing route silently completed")}
    }
    ctx,cancel:=context.WithCancel(t.Context());cancel()
    router.HandleStreamForwarded("watch",rpcStreamProxy(client,router,"watch"))
    sub,err:=router.OpenInternalStream(ctx,&api.Request{API:"watch"})
    if sub!=nil||!errors.Is(err,context.Canceled){t.Fatalf("cancelled bridge accepted: %v %v",sub,err)}
}
`
