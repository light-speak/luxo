package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

const awaitFailureSource = `
model Counter @noTime { id: Int @id value: Int }
fn explode(mode: Int): Result<Int> @native
fn ordinary(mode: Int): Int { val value = await { explode(mode)? } return value }
fn typed(mode: Int): Int { val value: Int? = await { explode(mode)? } return value ?: 0 }
fn standalone(mode: Int): Int { var result = 0 await { val value = explode(mode)? result = value } return result }
`

func TestGeneratedAwaitNativePanic(t *testing.T)  { runAwaitFailureFixture(t, 1) }
func TestGeneratedAwaitNativeGoexit(t *testing.T) { runAwaitFailureFixture(t, 2) }

func runAwaitFailureFixture(t *testing.T, mode int) {
	t.Helper()
	source := `package generated
import("context";"runtime";"testing")
type awaitFailureResolver struct{}
func(*awaitFailureResolver)Explode(ctx context.Context,mode int64)(int64,error){switch mode{case 1:panic("private native panic");case 2:runtime.Goexit()};return 42,nil}
func TestAwaitFailure(t *testing.T){
 app:=&App{Resolver:&awaitFailureResolver{}}
 for _,call:=range []func(context.Context,int64)(int64,error){app.ordinary,app.typed,app.standalone}{
  if value,err:=call(t.Context(),MODE);err==nil{t.Fatal("native abnormal exit became success",value)}
  if value,err:=call(t.Context(),0);err!=nil||value!=42{t.Fatal("later call damaged",value,err)}
 }
}
`
	marker := "1"
	if mode == 2 {
		marker = "2"
	}
	source = strings.ReplaceAll(source, "MODE", marker)
	runGeneratedFixture(t, awaitFailureSource, source, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedAwaitFailureTransports(t *testing.T) {
	runGeneratedFixture(t, awaitFailureSource+`
api run(mode: Int): Int { return ordinary(mode) }
`, `package generated
import("bytes";"context";"encoding/json";"errors";"net";"net/http/httptest";"runtime";"strconv";"sync/atomic";"testing";"time";"nhooyr.io/websocket";
 "github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/api")
type transportAwaitResolver struct{}
type hostileAwaitPanic struct{}
func(hostileAwaitPanic)Error()string{panic("must not format native panic")}
func(*transportAwaitResolver)Explode(ctx context.Context,mode int64)(int64,error){switch mode{case 1:panic("private native panic");case 2:runtime.Goexit();case 3:panic(hostileAwaitPanic{});case 4:panic(nil)};return 42,nil}
type awaitListener struct{net.Listener;accepted atomic.Int64}
func(l *awaitListener)Accept()(net.Conn,error){connection,err:=l.Listener.Accept();if err==nil{l.accepted.Add(1)};return connection,err}
func TestAwaitTransports(t *testing.T){
 router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:&transportAwaitResolver{}});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 counted:=&awaitListener{Listener:listener};server:=rpc.NewServer(router);done:=make(chan error,1)
 go func(){done<-server.Serve(counted)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close();id:=router.Schema.APIs["run"].ID
 for mode:=int64(1);mode<=4;mode++{for _,transport:=range []string{"JSON","Binary","LRPC","LRPCTrace"}{for _,value:=range []int64{mode,0}{
  var params codec.Encoder;params.WriteFieldInt(1,value);params.WriteEnd()
  want:=500;if value==0{want=200}
  if transport=="LRPC"||transport=="LRPCTrace"{
   ctx:=t.Context();if transport=="LRPCTrace"{ctx,_=api.WithRemoteTrace(ctx,"await-native-exit","",false)}
   data,callErr:=client.CallWithMaskContext(ctx,"",id,nil,params.Bytes())
   if want==200{if callErr!=nil||!bytes.Equal(data,codec.AppendSvarint(nil,42)){t.Fatal("RPC recovery lost",data,callErr)}}else{failure,ok:=errors.AsType[*luxerrors.AppError](callErr);if !ok||failure.Code!=500||!failure.Internal||len(data)!=0{t.Fatal("RPC abnormal exit succeeded",data,callErr)}}
  }else{
   body:=[]byte("{\"$api\":\"run\",\"mode\":"+strconv.FormatInt(value,10)+"}")
   if transport=="Binary"{body=codec.AppendVarint(nil,uint64(id));body=codec.AppendVarint(body,0);body=append(body,params.Bytes()...)}
   request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));response:=httptest.NewRecorder()
   request.Header.Set("Content-Type","application/json");if transport=="Binary"{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
   router.ServeHTTP(response,request)
   if response.Code!=want||bytes.Contains(response.Body.Bytes(),[]byte("private native panic")){t.Fatal("HTTP abnormal exit/privacy",transport,value,response.Code,response.Body.String())}
   if want==200&&transport=="Binary"&&!bytes.Equal(response.Body.Bytes(),codec.AppendSvarint(nil,42)){t.Fatal("Binary success damaged",response.Body.Bytes())}
  }
 }}}

 httpServer:=httptest.NewServer(router);defer httpServer.Close()
 for _,binary:=range []bool{false,true}{
  ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
  connection,_,err:=websocket.Dial(ctx,"ws"+httpServer.URL[4:]+"/luvia",nil);if err!=nil{cancel();t.Fatal(err)}
  sequence:=uint64(0)
  for mode:=int64(1);mode<=4;mode++{for _,value:=range []int64{mode,0}{
   sequence++;messageType:=websocket.MessageText
   data:=[]byte("{\"$api\":\"run\",\"$id\":"+strconv.FormatUint(sequence,10)+",\"mode\":"+strconv.FormatInt(value,10)+"}")
   if binary{messageType=websocket.MessageBinary;var params codec.Encoder;params.WriteFieldInt(1,value);params.WriteEnd();data=codec.AppendVarint([]byte{api.BinaryFrameCallRequest},sequence);data=codec.AppendVarint(data,uint64(id));data=codec.AppendVarint(data,0);data=append(data,params.Bytes()...)}
   if err:=connection.Write(ctx,messageType,data);err!=nil{t.Fatal(err)}
   received,payload,err:=connection.Read(ctx);if err!=nil||received!=messageType{t.Fatal("WS connection lost",received,err)}
   if bytes.Contains(payload,[]byte("private native panic")){t.Fatal("WS leaked native panic")}
   if binary{
    want:=byte(api.BinaryFrameCallError);if value==0{want=api.BinaryFrameCallSuccess}
    if len(payload)<3||payload[0]!=want||payload[1]!=byte(sequence){t.Fatal("wrong WS result identity",payload)}
    if value==0{if !bytes.Equal(payload[2:],codec.AppendSvarint(nil,42)){t.Fatal("WS success damaged",payload)}}else{failure,err:=api.DecodeBinaryError(payload[2:],0);if err!=nil||failure.Code!=500{t.Fatal("WS abnormal exit succeeded",failure,err)}}
   }else{
    var envelope map[string]json.RawMessage;if err:=json.Unmarshal(payload,&envelope);err!=nil{t.Fatal(err)}
    if string(envelope["$id"])!=strconv.FormatUint(sequence,10){t.Fatal("WS correlation lost",string(payload))}
    if value==0{if string(envelope["data"])!="42"{t.Fatal("WS success damaged",string(payload))}}else if string(envelope["code"])!="500"{t.Fatal("WS abnormal exit succeeded",string(payload))}
   }
  }}
  connection.CloseNow();cancel()
 }
 if counted.accepted.Load()!=1{t.Fatal("failure lost reusable RPC connection",counted.accepted.Load())}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestCompiledAwaitAggregateAbnormalExit(t *testing.T) {
	c := newCompiler(makeModels("Entry"))
	count := func() ast.Expr {
		return &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Entry"}, Field: "count"}}
	}
	c.compileStmt(&ast.ValStmt{Names: []string{"first", "second"}, Value: &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: count()}, &ast.ExprStmt{Expr: count()}}}}})
	body := compilerOut(c)
	if !strings.Contains(body, ".AggregateBatch(gctx,") {
		t.Fatal("fixture did not fuse aggregates", body)
	}
	runGeneratedProtocolPackage(t, `package generated
import("context";"errors";"runtime";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/api")
type App struct{Entry *failingAggregate}
type failingAggregate struct{mode int}
func(q *failingAggregate)Where(...lux.Condition)*failingAggregate{return q}
func(q *failingAggregate)AggregateBatch(context.Context,...lux.AggregateSpec)([]int64,error){switch q.mode{case 1:panic("private aggregate");case 2:runtime.Goexit()};return []int64{21,21},nil}
func aggregate(ctx context.Context,mode int)error{
 app:=&App{Entry:&failingAggregate{mode:mode}}
`+body+`
 if first!=21||second!=21{return errors.New("invalid aggregate result")};return nil
}
`, `package generated
import("errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestAggregateAbnormalExit(t *testing.T){for _,mode:=range []int{1,2}{err:=aggregate(t.Context(),mode);failure,ok:=errors.AsType[*luxerrors.AppError](err);if !ok||failure.Code!=500||!failure.Internal{t.Fatal("aggregate abnormal exit succeeded",err)};if err:=aggregate(t.Context(),0);err!=nil{t.Fatal("later aggregate damaged",err)}}}
`)
}
