package codegen

import "testing"

func TestGeneratedServiceRouteBoundary(t *testing.T) {
	const input = `api ping(): Int { return 7 }
fn secret(): Int @service { return 42 }
fn nativeSecret(): Int @service @native`
	runGeneratedFixture(t, input, internalRouteAssertions, StableIDs{
		APIs: map[string]int{"ping": 1, "svc:secret": 2, "svc:nativeSecret": 3},
	})
}

const internalRouteAssertions = `package generated
import("bytes";"context";"fmt";"net";"net/http/httptest";"strings";"sync/atomic";"testing";"time"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc";"nhooyr.io/websocket")

type nativeBoundary struct{ calls atomic.Int64 }
func (r *nativeBoundary) NativeSecret(context.Context)(int64,error){ r.calls.Add(1);return 84,nil }

func routeRouter(t *testing.T) (*api.Router,*nativeBoundary) {
    t.Helper()
    rt:=api.NewRouter()
    resolver:=&nativeBoundary{};app:=&App{Resolver:resolver}
    if err:=RegisterSchema(rt.Schema);err!=nil{t.Fatal(err)}
    if err:=RegisterHandlers(rt,app);err!=nil{t.Fatal(err)}
    if err:=RegisterServiceFns(rt,app);err!=nil{t.Fatal(err)}
    if err:=rt.Prepare();err!=nil{t.Fatal(err)}
    return rt,resolver
}

func TestGeneratedRouteMetadata(t *testing.T) {
    rt,_:=routeRouter(t)
    if rt.Schema.APIs["ping"].Internal||!rt.Schema.APIs["svc:secret"].Internal||!rt.Schema.APIs["svc:nativeSecret"].Internal{t.Fatal("incorrect generated route scope")}
}

func TestGeneratedRouteHTTP(t *testing.T) {
    rt,resolver:=routeRouter(t)
    for _,binary:=range []bool{false,true} {for _,forged:=range []bool{false,true} {for name,id:=range map[string]int{"ping":1,"svc:secret":2,"svc:nativeSecret":3} {
        internal,want:=id!=1,200
        if internal {want=404}
        body:=[]byte(fmt.Sprintf("{\"$api\":%q}",name))
        if binary {var err error;body,err=api.EncodeBinaryRequest(id,nil,nil,nil);if err!=nil{t.Fatal(err)}}
        request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body))
        if binary {request.Header.Set("X-Luxo-Mode","binary")}
        if forged {request.Header.Set("X-Luxo-Internal","true");request.Header.Set("X-Luxo-Client-Key","trusted-peer")}
        response:=httptest.NewRecorder();rt.ServeHTTP(response,request)
        if response.Code!=want {t.Errorf("binary=%v internal=%v: status=%d body=%s",binary,internal,response.Code,response.Body.String())}
        if !internal {expected:=[]byte("{\"data\":7}");if binary{expected=[]byte{14}};if !bytes.Equal(response.Body.Bytes(),expected){t.Errorf("public response changed: %x",response.Body.Bytes())}}
        if resolver.calls.Load()!=0{t.Fatal("public transport executed private native code")}
    }}}
}

func TestGeneratedRouteWS(t *testing.T) {
    router,resolver:=routeRouter(t);srv:=httptest.NewServer(router);defer srv.Close()
    ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
    conn,_,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(srv.URL,"http"),&websocket.DialOptions{HTTPHeader:map[string][]string{"X-Luxo-Internal":{"true"},"X-Luxo-Client-Key":{"trusted-peer"}}})
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
        if resolver.calls.Load()!=0{t.Fatal("WebSocket executed private native code")}
    }}
}

func TestGeneratedRouteRPC(t *testing.T) {
    router,resolver:=routeRouter(t);srv:=rpc.NewServer(router);listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
    done:=make(chan error,1);go func(){done<-srv.Serve(listener)}()
    defer func(){srv.Close();if err:=<-done;err!=nil{t.Error(err)}}()
    client:=rpc.NewClient(listener.Addr().String());defer client.Close()
    for id,want:=range map[int]int64{1:7,2:42,3:84} {
        ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
        data,err:=client.CallWithMaskContext(ctx,"",id,nil,[]byte{0});cancel()
        if err!=nil||!bytes.Equal(data,codec.AppendSvarint(nil,want)){t.Fatalf("RPC %d: %x %v",id,data,err)}
    }
    if resolver.calls.Load()!=1{t.Fatal("internal native RPC did not execute exactly once")}
}
`
