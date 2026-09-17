package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

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
    "context"
    "fmt"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/rpc"
    "github.com/light-speak/luxo/pkg/lux/luvia"
)
`)
	writeGatewayRPCStreamProxy(&source, []moduleInfo{{nativeStreams: []string{"watch"}}})
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
func (*nativeResolver) HandleWatch(ctx context.Context, params *api.StreamParams, identity any, stream *api.TypedStream[int64]) error {
    if err := stream.Send(9223372036854775807); err != nil { return err }
    return luxerrors.Conflict
}
func (*nativeResolver) HandleWatchPrivate(ctx context.Context, params *api.StreamParams, identity any, stream *api.TypedStream[int64]) error {
    return errors.New("anonymous subscriber reached private handler")
}
func TestNativeLifecycle(t *testing.T) {
    router := api.NewRouter()
    RegisterStreams(router, nil, &nativeResolver{})
    for _, name := range []string{"watch", "watchPrivate"} {
        sub, err := router.OpenInternalStream(t.Context(), &api.Request{API:name})
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
        if name == "watchPrivate" { want, wantCount = luxerrors.Unauthorized, 0 }
        if count != wantCount || !errors.Is(sub.Err(), want) { t.Fatalf("%s: count=%d result=%v",name,count,sub.Err()) }
    }
}
`

const streamBridgeAssertions = `package generated
import (
    "context"
    "errors"
    "net"
    "testing"
    "time"
    "github.com/light-speak/luxo/pkg/lux/api"
    luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
    "github.com/light-speak/luxo/pkg/lux/rpc"
    "github.com/light-speak/luxo/pkg/lux/schema"
)
const precisionData = "{\"id\":9223372036854775807,\"decimal\":0.123456789012345678901234567890}"
type rawErrorData struct{}
func (rawErrorData) AppendJSON(dst []byte) ([]byte,error) { return append(dst, precisionData...),nil }
func (rawErrorData) AppendText(dst []byte, field string) ([]byte,bool) { return dst,false }
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
        gateway.HandleStreamNative("watch",rpcStreamProxy(client,gateway,"watch"))
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
func startStreamBackend(t *testing.T,router *api.Router)*rpc.Client{
    t.Helper()
    listener,err:=net.Listen("tcp","127.0.0.1:0")
    if err!=nil{t.Fatal(err)}
    server:=rpc.NewServer(router)
    done:=make(chan error,1)
    go func(){done<-server.Serve(listener)}()
    client:=rpc.NewClient(listener.Addr().String())
    t.Cleanup(func(){client.Close();server.Close();if err:=<-done;err!=nil{t.Error(err)}})
    return client
}
func TestBridgeMissingRouteAndCancellation(t *testing.T){
    router:=api.NewRouter()
    router.Schema.RegisterAPI(&schema.API{ID:1,Name:"watch",Stream:true})
    client:=rpc.NewClient("127.0.0.1:1")
    defer client.Close()
    for _,handler:=range []api.StreamHandlerFunc{rpcStreamProxy(nil,router,"watch"),rpcStreamProxy(client,router,"missing")}{
        if err:=handler(t.Context(),nil,nil,nil);err==nil{t.Fatal("missing route silently completed")}
    }
    ctx,cancel:=context.WithCancel(t.Context());cancel()
    router.HandleStreamNative("watch",rpcStreamProxy(client,router,"watch"))
    sub,err:=router.OpenInternalStream(ctx,&api.Request{API:"watch"})
    if err!=nil{t.Fatal(err)}
    for range sub.Messages(){t.Fatal("cancelled bridge produced a message")}
    sub.Close()
    if !errors.Is(sub.Err(),context.Canceled){t.Fatalf("cancellation lost: %v",sub.Err())}
}
`
