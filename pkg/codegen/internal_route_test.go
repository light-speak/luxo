package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedServiceRouteBoundary(t *testing.T) {
	const input = `api ping(): Int { return 7 }
fn secret(): Int @service { return 42 }
fn nativeSecret(): Int @service @native`
	tokens, lexErrors := lexer.New(input, "origin/boundary.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("origin/boundary.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"ping": 1, "svc:secret": 2, "svc:nativeSecret": 3}}})
	source := string(g.generateHandlerFile(result, "generated", nil)) + `
type App struct{ Resolver nativeBoundary }
type nativeBoundary struct{}
func (nativeBoundary) NativeSecret(context.Context)(int64,error){ return 84,nil }
`
	metadata := string(g.generateSchemaFile(result, "generated", nil))
	start := strings.Index(metadata, "func RegisterSchema(")
	if start < 0 {
		t.Fatal("missing generated schema registration")
	}
	runGeneratedProtocolPackage(t, source, internalRouteAssertions+metadata[start:])
}

const internalRouteAssertions = `package generated
import("bytes";"context";"fmt";"net";"net/http/httptest";"strings";"testing";"time"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema";"nhooyr.io/websocket")

func routeRouter() *api.Router {
    rt:=api.NewRouter()
    RegisterSchema(rt.Schema)
    RegisterHandlers(rt,&App{});RegisterServiceFns(rt,&App{})
    return rt
}

func TestGeneratedRouteMetadata(t *testing.T) {
    rt:=routeRouter()
    if rt.Schema.APIs["ping"].Internal||!rt.Schema.APIs["svc:secret"].Internal||!rt.Schema.APIs["svc:nativeSecret"].Internal{t.Fatal("incorrect generated route scope")}
}

func TestGeneratedRouteHTTP(t *testing.T) {
    for _,binary:=range []bool{false,true} {for name,id:=range map[string]int{"ping":1,"svc:secret":2,"svc:nativeSecret":3} {
        internal,want:=id!=1,200
        if internal {want=404}
        body:=[]byte(fmt.Sprintf("{\"$api\":%q}",name))
        if binary {var err error;body,err=api.EncodeBinaryRequest(id,nil,nil,nil);if err!=nil{t.Fatal(err)}}
        request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body))
        if binary {request.Header.Set("X-Luxo-Mode","binary")}
        response:=httptest.NewRecorder();routeRouter().ServeHTTP(response,request)
        if response.Code!=want {t.Errorf("binary=%v internal=%v: status=%d body=%s",binary,internal,response.Code,response.Body.String())}
    }}
}

func TestGeneratedRouteWS(t *testing.T) {
    srv:=httptest.NewServer(routeRouter());defer srv.Close()
    ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
    conn,_,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(srv.URL,"http"),nil)
    if err!=nil{t.Fatal(err)};defer conn.CloseNow()
    for _,binary:=range []bool{false,true} {for _,name:=range []string{"svc:secret","svc:nativeSecret","ping"} {
        id:=map[string]int{"ping":1,"svc:secret":2,"svc:nativeSecret":3}[name];internal:=id!=1
        frame:=[]byte(fmt.Sprintf("{\"$id\":1,\"$api\":%q}",name));kind:=websocket.MessageText
        if binary {body,err:=api.EncodeBinaryRequest(id,nil,nil,nil);if err!=nil{t.Fatal(err)};frame=append([]byte{api.BinaryFrameCallRequest,1},body...);kind=websocket.MessageBinary}
        if err=conn.Write(ctx,kind,frame);err!=nil{t.Fatal(err)}
        _,data,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
        if binary {want:=byte(api.BinaryFrameCallSuccess);if internal{want=api.BinaryFrameCallError};if len(data)==0||data[0]!=want{t.Errorf("binary internal=%v: %x",internal,data)}} else {
            if internal&&!bytes.Contains(data,[]byte("NotFound")){t.Errorf("internal JSON: %s",data)}
            if !internal&&!bytes.Contains(data,[]byte("\"data\":7")){t.Errorf("public JSON: %s",data)}
        }
    }}
}

func TestGeneratedRouteRPC(t *testing.T) {
    srv:=rpc.NewServer(routeRouter());listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
    done:=make(chan error,1);go func(){done<-srv.Serve(listener)}()
    defer func(){srv.Close();if err:=<-done;err!=nil{t.Error(err)}}()
    client:=rpc.NewClient(listener.Addr().String());defer client.Close()
    for id,want:=range map[int]int64{1:7,2:42,3:84} {
        ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
        data,err:=client.CallWithMaskContext(ctx,"",id,nil,[]byte{0});cancel()
        if err!=nil||!bytes.Equal(data,codec.AppendSvarint(nil,want)){t.Fatalf("RPC %d: %x %v",id,data,err)}
    }
}
`
