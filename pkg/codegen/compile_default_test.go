package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedTypeObjectDefaults(t *testing.T) {
	runGeneratedFixture(t, `type Snapshot { count: Int = 42 label: String = "guest" }
fn fromDefaults(): Snapshot { return Snapshot {} }
fn fromExplicit(): Snapshot { return Snapshot { count: 0, label: "" } }
api readDefaults(): Snapshot { return fromDefaults() }
`, typeObjectDefaultAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"Snapshot": {"count": 1, "label": 2}},
		APIs:        map[string]int{"readDefaults": 1},
	})
}

const typeObjectDefaultAssertions = `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")

func TestConstructedDefaults(t *testing.T){
 app:=&App{};got,err:=app.fromDefaults(t.Context());if err!=nil||got.Count!=42||got.Label!="guest"{t.Error("declared defaults were replaced by zero values",got,err)}
 explicit,err:=app.fromExplicit(t.Context());if err!=nil||explicit.Count!=0||explicit.Label!=""{t.Error("explicit zero fields were overwritten",explicit,err)}
}
func TestTypeInputDefaults(t *testing.T){
 var fromJSON Snapshot;if err:=fromJSON.ReadJSONInput([]byte("{}"));err!=nil||fromJSON.Count!=42||fromJSON.Label!="guest"{t.Error("missing JSON fields lost defaults",fromJSON,err)}
 var fromBinary Snapshot;reader:=codec.NewDecoder([]byte{0,0});fromBinary.ReadLuxoInput(reader);if err:=reader.Finish();err!=nil||fromBinary.Count!=42||fromBinary.Label!="guest"{t.Error("missing Binary fields lost defaults",fromBinary,err)}
 var projected Snapshot;reader=codec.NewDecoder([]byte{0,0});projected.ReadLuxo(reader);if err:=reader.Finish();err!=nil||projected.Count!=0||projected.Label!=""{t.Error("projection decoding invented fields",projected,err)}
}
func TestConstructedDefaultsHTTP(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"readDefaults\",\"$select\":\"count,label\"}");if binary{mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(1,mask,nil,nil);if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));if binary{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  if binary{var got Snapshot;decoder:=codec.NewDecoder(response.Body.Bytes());got.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil||got.Count!=42||got.Label!="guest"{t.Error("Binary defaults",got,err)}}else if response.Body.String()!="{\"data\":{\"count\":42,\"label\":\"guest\"}}"{t.Error("JSON defaults",response.Body.String())}
 }
}
`

func TestGeneratedAPIParameterCapacity(t *testing.T) {
	for _, count := range []int{16, 17, 65} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			params := make([]string, count)
			ids := make(map[string]int, count)
			for i := range params {
				name := "p" + strconv.Itoa(i)
				params[i] = name + ": Int = 0"
				ids[name] = i + 1
			}
			last := "p" + strconv.Itoa(count-1)
			params[count-1] = last + ": Int = 7"
			source := "api read(" + strings.Join(params, ", ") + "): Int { return p0 + " + last + " }\n" +
				"api watch(" + strings.Join(params, ", ") + "): Int @stream @native"
			var proxy strings.Builder
			writeGatewayRPCStreamProxy(&proxy, []moduleInfo{{nativeStreams: []string{"watch"}}})
			assertions := strings.ReplaceAll(generatedParameterCapacityAssertions+proxy.String(), "PARAM_COUNT", strconv.Itoa(count))
			assertions = strings.ReplaceAll(assertions, "LAST_PARAM", last)
			assertions = strings.ReplaceAll(assertions, "LAST_FIELD", "P"+strconv.Itoa(count-1))
			runGeneratedFixture(t, source, assertions, StableIDs{
				APIs: map[string]int{"read": 1, "watch": 2}, APIParams: map[string]map[string]int{"read": ids, "watch": ids},
			})
		})
	}
}

const generatedParameterCapacityAssertions = `package generated
import("sync/atomic";"fmt";"testing";"bytes";"context";"io";"net";"net/http";"net/http/httptest";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/luvia";"nhooyr.io/websocket")
var capacityCalls atomic.Int64
type capacityResolver struct{}
func (*capacityResolver) HandleWatch(_ context.Context,p *WatchStreamParams,_ any,stream *api.TypedStream[int64])error{capacityCalls.Add(1);return stream.Send(p.P0+p.LAST_FIELD)}
func TestAcceptedDefinitionRuns(t *testing.T){
 router:=api.NewRouterWithOptions(api.RouterOptions{})
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 if err:=RegisterStreams(router,nil,&capacityResolver{});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatalf("accepted source cannot start: %v",err)}
 server:=httptest.NewServer(router);defer server.Close()
 client:=server.Client();client.Timeout=5*time.Second
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 backend:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-backend.Serve(listener)}()
 defer func(){backend.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 remote:=rpc.NewClient(listener.Addr().String());defer remote.Close()
 gateway:=api.NewRouterWithOptions(api.RouterOptions{})
 if err:=RegisterSchema(gateway.Schema);err!=nil{t.Fatal(err)}
 if err:=gateway.Registry.RegisterSchemaAPIs(gateway.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterStreams(gateway,nil,nil);err!=nil{t.Fatalf("pre-registered gateway conflict: %v",err)}
 gateway.HandleStreamForwarded("watch",rpcStreamProxy(remote,gateway,"watch"))
 if err:=gateway.Prepare();err!=nil{t.Fatal(err)}
 forwarded:=httptest.NewServer(gateway);defer forwarded.Close()
 checkCapacityStreamRejects(t,server.URL)
 checkCapacityStreamRejects(t,forwarded.URL)

 for _,supplied:=range []bool{false,true}{
  params:=[]byte{0};jsonBody:=[]byte("{\"$api\":\"read\"}");want:=int64(7)
  if supplied{
   params=codec.AppendVarint(nil,1);params=codec.AppendSvarint(params,5)
   params=codec.AppendVarint(params,PARAM_COUNT);params=codec.AppendSvarint(params,42);params=append(params,0)
   jsonBody=[]byte("{\"$api\":\"read\",\"p0\":5,\"LAST_PARAM\":42}");want=47
  }
  for _,binary:=range []bool{false,true}{
   body:=jsonBody;if binary{body=append([]byte{1,0},params...)}
   request,err:=http.NewRequestWithContext(t.Context(),"POST",server.URL,bytes.NewReader(body));if err!=nil{t.Fatal(err)}
   if binary{request.Header.Set("X-Luxo-Mode","binary")}
   response,err:=client.Do(request);if err!=nil{t.Fatal(err)}
   raw,err:=io.ReadAll(response.Body);response.Body.Close();if err!=nil{t.Fatal(err)}
   expected:=[]byte("{\"data\":7}");if supplied{expected=[]byte("{\"data\":47}")};if binary{expected=codec.AppendSvarint(nil,want)}
   if response.StatusCode!=200||!bytes.Equal(raw,expected){t.Fatalf("supplied=%v binary=%v status=%d body=%q",supplied,binary,response.StatusCode,raw)}
  }
  checkCapacityStream(t,server.URL,params,supplied,want)
  checkCapacityStream(t,forwarded.URL,params,supplied,want)
  ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
  raw,err:=remote.CallWithMaskContext(ctx,"",1,nil,params);cancel();if err!=nil{t.Fatal(err)}
  got,n:=codec.ReadSvarint(raw,0);if got!=want||n!=len(raw){t.Fatalf("LRPC got %x want %d",raw,want)}
 }
}
func checkCapacityStream(t *testing.T,url string,params []byte,supplied bool,want int64){
 t.Helper()
 for _,binary:=range []bool{false,true}{
  ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
  conn,_,err:=websocket.Dial(ctx,"ws"+url[4:],nil);if err!=nil{cancel();t.Fatal(err)}
  func(){defer cancel();defer conn.CloseNow()
   kind:=websocket.MessageText;frame:=[]byte("{\"$sub\":\"watch\",\"$id\":1}")
   if supplied{frame=[]byte("{\"$sub\":\"watch\",\"$id\":1,\"p0\":5,\"LAST_PARAM\":42}")}
   if binary{kind=websocket.MessageBinary;frame=append([]byte{api.BinaryFrameSubscribe,1,2,0},params...)}
   if err:=conn.Write(ctx,kind,frame);err!=nil{t.Fatal(err)}
   _,ack,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
   if binary{if !bytes.Equal(ack,[]byte{api.BinaryFrameSubscribeSuccess,1}){t.Fatalf("ACK %x",ack)}}else if !bytes.Contains(ack,[]byte("\"ok\":true")){t.Fatalf("ACK %s",ack)}
   _,data,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
   expected:=[]byte("\"data\":7");if supplied{expected=[]byte("\"data\":47")}
   if binary{expected=codec.AppendSvarint([]byte{api.BinaryFrameStream,1},want);if !bytes.Equal(data,expected){t.Fatalf("data %x",data)}}else if !bytes.Contains(data,expected){t.Fatalf("data %s",data)}
   _,end,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
   expectedEnd:=[]byte("{\"$end\":1,\"ok\":true}");if binary{expectedEnd=[]byte{api.BinaryFrameStreamComplete,1}}
   if !bytes.Equal(end,expectedEnd){t.Fatalf("end %q",end)}
  }()
 }
}
func checkCapacityStreamRejects(t *testing.T,url string){
 t.Helper()
 for _,binary:=range []bool{false,true}{
  ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
  conn,_,err:=websocket.Dial(ctx,"ws"+url[4:],nil);if err!=nil{cancel();t.Fatal(err)}
  func(){defer cancel();defer conn.CloseNow()
   before:=capacityCalls.Load()
   kind:=websocket.MessageText;frame:=[]byte("{\"$sub\":\"watch\",\"$id\":1,\"LAST_PARAM\":\"invalid\"}")
   if binary{kind=websocket.MessageBinary;frame=codec.AppendVarint([]byte{api.BinaryFrameSubscribe,1,2,0},PARAM_COUNT)}
   if err:=conn.Write(ctx,kind,frame);err!=nil{t.Fatal(err)}
   _,reply,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
   if binary{
    if len(reply)<3||reply[0]!=api.BinaryFrameSubscribeError||reply[1]!=1{t.Fatalf("invalid input ACK: %x",reply)}
    failure,err:=api.DecodeBinaryError(reply[2:],0);if err!=nil||failure.Code!=400{t.Fatalf("error %v / %v",failure,err)}
   }else if !bytes.Contains(reply,[]byte("\"code\":400")){t.Fatalf("invalid input ACK: %s",reply)}
   if capacityCalls.Load()!=before{t.Fatal("invalid parameter reached native handler")}
  }()
 }
}
`

func TestGeneratedErrorArgumentOrder(t *testing.T) {
	runGeneratedFixture(t, `
error Rejected(first: Int, second: Int) { code: 422 message: "error.rejected" }
model Counter @noTime { id: Int @id value: Int }
fn advance(counter: Counter): Int { counter.value += 1 return counter.value }
fn reordered(): Int { throw Rejected(second: 2, first: 1) }
fn evaluated(counter: Counter): Int { throw Rejected(first: counter.value, second: advance(counter)) }
`, `package generated
import("errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestErrorArguments(t *testing.T){
 app:=&App{};counter:=&Counter{Value:1}
 _,first:=app.reordered(t.Context());_,second:=app.evaluated(t.Context(),counter)
 for _,err:=range []error{first,second}{
  var business *luxerrors.AppError;if !errors.As(err,&business){t.Fatal(err)}
  data,ok:=business.Data.(*RejectedErrorData);if !ok||data.field0!=1||data.field1!=2{t.Fatal(business.Data)}
 }
 if counter.Value!=2{t.Fatal(counter.Value)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedInternalErrorPrivacy(t *testing.T) {
	runGeneratedFixture(t, `
error Secret(detail: String) { code: 409 message: "private.message" internal: true }
error Public(detail: String) { code: 422 message: "public.message" internal: false }
error EmptySecret { code: 500 message: "private.empty" internal: true }
error NamedSecret(err: String) { code: 500 message: "private.named" internal: true }
api hiddenFailure(): Int { throw Secret(detail: "private-data") }
api publicFailure(): Int { throw Public(detail: "public-data") }
`, `package generated
import("bytes";"context";"errors";"net";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/rpc";"nhooyr.io/websocket")

func TestInternalPrivacy(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
 for _,tc:=range []struct{name string;status int;private bool}{{"hiddenFailure",409,true},{"publicFailure",422,false}}{
  request:=httptest.NewRequest("POST","/",bytes.NewBufferString("{\"$api\":\""+tc.name+"\"}"));request.Header.Set("Content-Type","application/json")
  if binary { id:=1;if !tc.private{id=2};body,err:=api.EncodeBinaryRequest(id,nil,nil,nil);if err!=nil{t.Fatal(err)};request=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary") }
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);body:=response.Body.Bytes()
  if response.Code!=tc.status{t.Fatal(response.Code,response.Body.String())}
  if tc.private{
   if bytes.Contains(body,[]byte("private"))||!bytes.Contains(body,[]byte("error.internal")){t.Fatal("internal content leaked",response.Body.String())}
  }else if !bytes.Contains(body,[]byte("public-data"))||!bytes.Contains(body,[]byte("public.message")){t.Fatal("public content lost",response.Body.String())}
 }
 }
 testWebSocketPrivacy(t,router)
 testRPCPrivacy(t,router)
 if !NewEmptySecret().Internal||!NewNamedSecret("detail").Internal{t.Fatal("internal constructor variants lost flag")}
 if !NewSecret("detail").Internal||NewPublic("detail").Internal{t.Fatal("internal flag differs from declaration")}
}
func testWebSocketPrivacy(t *testing.T, router *api.Router){
 t.Helper();server:=httptest.NewServer(router);defer server.Close()
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
 conn,_,err:=websocket.Dial(ctx,"ws"+server.URL[4:],nil);if err!=nil{t.Fatal(err)};defer conn.CloseNow()
 for _,binary:=range []bool{false,true}{for _,private:=range []bool{true,false}{
  name,id:="hiddenFailure",1;if !private{name,id="publicFailure",2}
  kind:=websocket.MessageText;frame:=[]byte("{\"$id\":1,\"$api\":\""+name+"\"}")
  if binary{body,err:=api.EncodeBinaryRequest(id,nil,nil,nil);if err!=nil{t.Fatal(err)};frame=append([]byte{api.BinaryFrameCallRequest,1},body...);kind=websocket.MessageBinary}
  if err:=conn.Write(ctx,kind,frame);err!=nil{t.Fatal(err)}
  got,body,err:=conn.Read(ctx);if err!=nil||got!=kind{t.Fatal(got,err)}
  if private{if bytes.Contains(body,[]byte("private"))||!bytes.Contains(body,[]byte("error.internal")){t.Fatalf("WS leaked: %q",body)}}else if !bytes.Contains(body,[]byte("public-data")){t.Fatalf("WS lost public data: %q",body)}
 }}
}
func testRPCPrivacy(t *testing.T,router *api.Router){
 t.Helper();listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
 for _,id:=range []int{1,2}{
  _,err:=client.CallWithMaskContext(ctx,"",id,nil,[]byte{0});var appErr *luxerrors.AppError
  if !errors.As(err,&appErr){t.Fatal(err)}
  if id==1{if !appErr.Internal||appErr.Code!=409||appErr.Data!=nil||appErr.Message!="error.internal"{t.Fatalf("LRPC leaked: %+v",appErr)}}else{
   if appErr.Internal||appErr.Code!=422||appErr.Data==nil||appErr.Message!="public.message"{t.Fatal(appErr)}
   raw,err:=appErr.Data.AppendJSON(nil);if err!=nil||!bytes.Contains(raw,[]byte("public-data")){t.Fatal(string(raw),err)}
  }
 }
}

`, StableIDs{APIs: map[string]int{"hiddenFailure": 1, "publicFailure": 2}})
}

func TestGeneratedPublicServerErrorParity(t *testing.T) {
	runGeneratedFixture(t, `
error Unavailable(detail: String) { code: 503 message: "public.unavailable" internal: false }
api reject(): Int { throw Unavailable(detail: "retry-later") }
`, `package generated
import("bytes";"context";"errors";"net";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/rpc")

func TestPublicParity(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 request:=httptest.NewRequest("POST","/",bytes.NewBufferString("{\"$api\":\"reject\"}"));request.Header.Set("Content-Type","application/json")
 response:=httptest.NewRecorder();router.ServeHTTP(response,request)
 if response.Code!=503||!bytes.Contains(response.Body.Bytes(),[]byte("retry-later")){t.Fatal("HTTP public error differs",response.Body.String())}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
 _,err=client.CallWithMaskContext(ctx,"",1,nil,[]byte{0});var appErr *luxerrors.AppError
 if !errors.As(err,&appErr){t.Fatal(err)}
 if appErr.Internal||appErr.Code!=503||appErr.Message!="public.unavailable"||appErr.Data==nil{t.Fatalf("LRPC changed explicit public error: %+v",appErr)}
 raw,err:=appErr.Data.AppendJSON(nil);if err!=nil||!bytes.Contains(raw,[]byte("retry-later")){t.Fatal(string(raw),err)}
}
`, StableIDs{APIs: map[string]int{"reject": 1}})
}

func TestGeneratedErrorConstructorBindingNames(t *testing.T) {
	runGeneratedFixture(t, `
error Rejected(errors: Int, range: Int, _luxoParam0: Int) { code: 422 message: "rejected" }
fn reject(): Int { throw Rejected(range: 2, errors: 1, _luxoParam0: 3) }
`, `package generated
import("errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")

func TestErrorNames(t *testing.T){
 _,err:=(&App{}).reject(t.Context());var business *luxerrors.AppError;if !errors.As(err,&business){t.Fatal(err)}
 data,ok:=business.Data.(*RejectedErrorData);if !ok||data.field0!=1||data.field1!=2||data.field2!=3{t.Fatal(business.Data)}
 body,err:=data.AppendJSON(nil);if err!=nil||string(body)!="{\"errors\":1,\"range\":2,\"_luxoParam0\":3}"{t.Fatal(string(body),err)}
 text,ok:=data.AppendText(nil,"range");if !ok||string(text)!="2"{t.Fatal(string(text),ok)}
}
`, StableIDs{})
}

func TestGeneratedErrorConstructorDefaults(t *testing.T) {
	runGeneratedFixture(t, `
error Defaulted(value: Int = 7, detail: String? = null) { code: 422 message: "defaulted" }
fn bareDefault(): Int { throw Defaulted }
fn explicitDefault(): Int { throw Defaulted() }
fn nullableField(): Int { throw Defaulted(detail: "text") }
fn guardDefault(): Int { false ?: throw Defaulted return 1 }
`, `package generated
import("context";"errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")

func TestDefaults(t *testing.T){
 app:=&App{}
 for _,call:=range []func(context.Context)(int64,error){app.bareDefault,app.explicitDefault,app.guardDefault}{
  _,err:=call(t.Context());var business *luxerrors.AppError;if !errors.As(err,&business){t.Fatal(err)}
  data,ok:=business.Data.(*DefaultedErrorData);if !ok||data.field0!=7||data.field1!=nil{t.Fatal(business.Data)}
 }
 _,err:=app.nullableField(t.Context());var business *luxerrors.AppError;if !errors.As(err,&business){t.Fatal(err)}
 data,ok:=business.Data.(*DefaultedErrorData);if !ok||data.field0!=7||data.field1==nil||*data.field1!="text"{t.Fatal(business.Data)}
}
`, StableIDs{})
}

func TestGeneratedThrowingDefaultEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
error Rejected(detail: String) { code: 422 message: "error.rejected" }
fn nativeErrorText(): Result<String> @native
fn errorText(): String { return nativeErrorText()? }
fn booleanStatement(value: Boolean): Boolean { value ?: throw Rejected(detail: errorText()) return value }
fn booleanExpression(value: Boolean): Boolean { return value ?: throw Rejected(detail: errorText()) }
fn booleanBang(value: Boolean): Boolean { value !: throw Rejected(detail: errorText()) return !value }
fn booleanNegation(value: Boolean): Boolean { !value ?: throw Rejected(detail: errorText()) return !value }
fn booleanComparison(value: Int): Int { value > 0 ?: throw Rejected(detail: errorText()) return value }
fn nullableStatement(value: Int?): Int { value ?: throw Rejected(detail: errorText()) return value }
fn nullableExpression(value: Int?): Int { return value ?: throw Rejected(detail: errorText()) }
fn nullableBoolean(value: Boolean?): Boolean { return value ?: throw error.NotFound }
fn defaultBoolean(value: Boolean?): Boolean { return value ?: true }
`, throwingDefaultAssertions, StableIDs{})
}

const throwingDefaultAssertions = `package generated
import("context";"errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
type guardResolver struct{calls int;failure error}
func(r *guardResolver)NativeErrorText(context.Context)(string,error){r.calls++;return "detail",r.failure}
func TestThrowingDefault(t *testing.T){
 for _,fail:=range []bool{false,true}{for _,present:=range []bool{false,true}{
  for _,name:=range []string{"booleanStatement","booleanExpression","booleanBang","booleanNegation","booleanComparison","nullableStatement","nullableExpression"}{t.Run(name,func(t *testing.T){
   resolver:=&guardResolver{};if fail{resolver.failure=errors.New("detail failed")};app:=&App{Resolver:resolver}
   var value *int64;if present{value=new(int64(42))};var err error
   switch name{
   case "booleanStatement":var got bool;got,err=app.booleanStatement(t.Context(),present);if err==nil&&!got{t.Fatal("false accepted")}
   case "booleanExpression":var got bool;got,err=app.booleanExpression(t.Context(),present);if err==nil&&!got{t.Fatal("false accepted")}
   case "booleanBang":var got bool;got,err=app.booleanBang(t.Context(),!present);if err==nil&&!got{t.Fatal("true accepted")}
   case "booleanNegation":var got bool;got,err=app.booleanNegation(t.Context(),!present);if err==nil&&!got{t.Fatal("true accepted")}
   case "booleanComparison":var input int64;if present{input=42};var got int64;got,err=app.booleanComparison(t.Context(),input);if err==nil&&got!=42{t.Fatal(got)}
   case "nullableStatement":var got int64;got,err=app.nullableStatement(t.Context(),value);if err==nil&&got!=42{t.Fatal(got)}
   case "nullableExpression":var got int64;got,err=app.nullableExpression(t.Context(),value);if err==nil&&got!=42{t.Fatal(got)}
   }
   if present{if err!=nil||resolver.calls!=0{t.Fatalf("eager error: calls=%d err=%v",resolver.calls,err)};return}
   if resolver.calls!=1{t.Fatalf("calls=%d want 1",resolver.calls)}
   if fail{if !errors.Is(err,resolver.failure){t.Fatal(err)};return}
   var business *luxerrors.AppError;if !errors.As(err,&business)||business.Name!="Rejected"{t.Fatalf("wrong rejection: %v",err)}
  })}
 }}
}
func TestNullableBooleanDefault(t *testing.T){
 app:=&App{}
 for _,value:=range []*bool{nil,new(false),new(true)}{
  got,err:=app.defaultBoolean(t.Context(),value);want:=value==nil||*value
  if err!=nil||got!=want{t.Fatal(got,want,err)}
  got,err=app.nullableBoolean(t.Context(),value)
  if value==nil{if err==nil{t.Fatal("nil accepted")}}else if err!=nil||got!=*value{t.Fatal(got,err)}
 }
}

func BenchmarkGeneratedThrowGuard(b *testing.B) {
 for _,sample:=range []struct{name string;present bool}{{"success",true},{"rejected",false}} {
  b.Run("Boolean/"+sample.name,func(b *testing.B){
   resolver:=&guardResolver{};app:=&App{Resolver:resolver};ctx:=context.Background()
   b.ReportAllocs();b.ResetTimer()
   for b.Loop(){
    got,err:=app.booleanExpression(ctx,sample.present)
    if (err==nil)!=sample.present || err==nil&&!got {b.Fatal("incorrect guard result")}
   }
   b.StopTimer();want:=0;if !sample.present{want=b.N}
   if resolver.calls!=want{b.Fatalf("native calls: got %d, want %d",resolver.calls,want)}
  })
  b.Run("Nullable/"+sample.name,func(b *testing.B){
   resolver:=&guardResolver{};app:=&App{Resolver:resolver};ctx:=context.Background()
   var value *int64;if sample.present{value=new(int64(42))}
   b.ReportAllocs();b.ResetTimer()
   for b.Loop(){
    got,err:=app.nullableExpression(ctx,value)
    if (err==nil)!=sample.present || err==nil&&got!=42 {b.Fatal("incorrect nullable guard result")}
   }
   b.StopTimer();want:=0;if !sample.present{want=b.N}
   if resolver.calls!=want{b.Fatalf("native calls: got %d, want %d",resolver.calls,want)}
  })
 }
}
`

func TestGeneratedDefaultExpressionBoundaries(t *testing.T) {
	runGeneratedFixture(t, `
model Record @noTime { id: Int @id name: String }
type Snapshot { count: Int }
fn recordDefault(value: Record?): Record { return value ?: Record { id: 42, name: "fallback" } }
fn recordPair(value: Record?, fallback: Record?): Record? { return value ?: fallback }
fn nullRecord(): Record { return null ?: Record { id: 42, name: "literal" } }
fn literalRecord(): Record { return Record { id: 42, name: "literal" } ?: Record { id: 7, name: "ignored" } }
fn typeDefault(value: Snapshot?): Snapshot { return value ?: Snapshot { count: 42 } }
fn bytesDefault(value: Bytes?, fallback: Bytes): Bytes { return value ?: fallback }
fn jsonDefault(value: JSON?, fallback: JSON): JSON { return value ?: fallback }
fn emptyListDefault(value: [Int]?): [Int] { return value ?: [] }
fn scalarDefault(value: Int?): Int { return value ?: 42 }
fn recordRequired(value: Record?): Record { return value ?: throw error.NotFound }
fn discardedDefault(value: Int?): Int { value ?: 42 return 7 }
api lookupDefault(id: Int): Record { return Record.where(id: id).first() ?: Record { id: 42, name: "fallback" } }
`, defaultExpressionAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Record": {"id": 1, "name": 2}, "Snapshot": {"count": 1},
	}})
}

const defaultExpressionAssertions = `package generated
import("testing";"encoding/json";"bytes";"net/http/httptest"
"github.com/light-speak/luxo/internal/testutil"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/pg"
"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestDefaultValues(t *testing.T){
 app:=&App{};ctx:=t.Context();record:=&Record{Id:7,Name:"present"}
 for _,present:=range []bool{false,true}{
  var value *Record;var snapshot *Snapshot;var integer *int64
  if present{value=record;snapshot=&Snapshot{Count:7};integer=new(int64(7))}
  got,err:=app.recordDefault(ctx,value);want:=int64(42);if present{want=7}
  if err!=nil||got==nil||got.Id!=want||present&&got!=record{t.Fatal("record",got,err)}
  pair,err:=app.recordPair(ctx,value,record);if err!=nil||pair!=record{t.Fatal("pair",pair,err)}
  object,err:=app.typeDefault(ctx,snapshot);if err!=nil||object.Count!=want{t.Fatal("type",object,err)}
  number,err:=app.scalarDefault(ctx,integer);if err!=nil||number!=want{t.Fatal("scalar",number,err)}
  number,err=app.discardedDefault(ctx,integer);if err!=nil||number!=7{t.Fatal("discarded",number,err)}
  required,err:=app.recordRequired(ctx,value);if present&&(err!=nil||required!=record)||!present&&err==nil{t.Fatal("guard",required,err)}
 }
 for _,call:=range []func()(*Record,error){func()(*Record,error){return app.nullRecord(ctx)},func()(*Record,error){return app.literalRecord(ctx)}}{
  got,err:=call();if err!=nil||got==nil||got.Id!=42||got.Name!="literal"{t.Fatal(got,err)}
 }
 for _,value:=range [][]int64{nil,{}, {7}}{
  got,err:=app.emptyListDefault(ctx,value);if err!=nil||got==nil||len(got)!=len(value){t.Fatal("empty list",got,err)}
  if len(value)>0&&&got[0]!=&value[0]{t.Fatal("list default copied present list")}
 }
 for _,present:=range []bool{false,true}{
  data:=[]byte{7};raw:=json.RawMessage("null");var dataPointer *[]byte;var rawPointer *json.RawMessage
  if present{dataPointer=&data;rawPointer=&raw}
  got,err:=app.bytesDefault(ctx,dataPointer,[]byte{42});want:=byte(42);if present{want=7};if err!=nil||len(got)!=1||got[0]!=want{t.Fatal(got,err)}
  actual,err:=app.jsonDefault(ctx,rawPointer,json.RawMessage("{}"));expected:="{}";if present{expected="null"};if err!=nil||string(actual)!=expected{t.Fatal(string(actual),err)}
 }
}
func TestDefaultQuery(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_default"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE records(id bigint PRIMARY KEY,name text NOT NULL); INSERT INTO records VALUES(7,'present')");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 for _,id:=range []int64{7,99}{for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"lookupDefault\",\"$select\":\"id, name\",\"id\":99}");if id==7{body=bytes.ReplaceAll(body,[]byte("99"),[]byte("7"))}
  if binary{
   var fields []byte;for _,field:=range []int{1,2}{fields,err=codec.FieldMaskSet(fields,field);if err!=nil{t.Fatal(err)}}
   selection,err:=codec.AppendSelectionMask(nil,fields,nil);if err!=nil{t.Fatal(err)}
   body,err=api.EncodeBinaryRequest(router.Schema.APIs["lookupDefault"].ID,selection,map[string]any{"id":id},router.Registry.ParamOrder("lookupDefault"));if err!=nil{t.Fatal(err)}
  }
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  want:=int64(42);name:="fallback";if id==7{want=7;name="present"}
  if binary{decoder:=codec.NewDecoder(response.Body.Bytes());var got Record;got.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil||got.Id!=want||got.Name!=name{t.Fatal(got,err)}}else{
   var result struct{Data struct{ID int64 ` + "`json:\"id\"`" + `;Name string ` + "`json:\"name\"`" + `} ` + "`json:\"data\"`" + `};if err:=json.Unmarshal(response.Body.Bytes(),&result);err!=nil||result.Data.ID!=want||result.Data.Name!=name{t.Fatal(response.Body.String(),err)}
  }
 }}
}
`

func TestWriteErrorReturnAvoidsIntermediateAllocations(t *testing.T) {
	for _, test := range []struct {
		name       string
		inFunction bool
		result     *ast.TypeRef
		indent     string
		want       string
	}{
		{name: "handler", want: "\treturn failure\n"},
		{name: "void function", inFunction: true, indent: "\t", want: "\t\treturn failure\n"},
		{name: "value function", inFunction: true, result: &ast.TypeRef{Name: "Int"}, want: "\treturn _value, failure\n"},
		{name: "handler with result", result: &ast.TypeRef{Name: "Int"}, want: "\treturn failure\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output strings.Builder
			output.Grow(1001 * len(test.want))
			c := compiler{b: &output, indent: "\t", inFunction: test.inFunction, functionResult: test.result}
			allocations := testing.AllocsPerRun(1000, func() {
				c.writeErrorReturn(test.indent, "failure")
			})
			if allocations != 0 {
				t.Fatalf("error return allocated %v times, want 0", allocations)
			}
			if output.String() != strings.Repeat(test.want, 1001) {
				t.Fatal("error return changed generated code")
			}
		})
	}
}

func TestCompileAPIBody_ParamWithDefaultInt(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "listUsers",
		Params: []*ast.ParamDecl{
			{
				Name:    "page",
				Type:    &ast.TypeRef{Name: "Int"},
				Default: &ast.Literal{Kind: token.Int, Value: "1"},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	// Should use Param with error-based fallback
	if !strings.Contains(out, `req.ParamInt("page")`) {
		t.Fatalf("expected ParamInt call, got:\n%s", out)
	}
	// Should have default value fallback
	if !strings.Contains(out, "= 1") {
		t.Fatalf("expected default value 1, got:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithDefaultString(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "search",
		Params: []*ast.ParamDecl{
			{
				Name:    "sort",
				Type:    &ast.TypeRef{Name: "String"},
				Default: &ast.Literal{Kind: token.String, Value: "createdAt"},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamString("sort")`) {
		t.Fatalf("expected ParamString call, got:\n%s", out)
	}
	if !strings.Contains(out, `"createdAt"`) {
		t.Fatalf("expected default string value, got:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithDefaultBool(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "list",
		Params: []*ast.ParamDecl{
			{
				Name:    "active",
				Type:    &ast.TypeRef{Name: "Boolean"},
				Default: &ast.Literal{Kind: token.True, Value: "true"},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamBool("active")`) {
		t.Fatalf("expected ParamBool call, got:\n%s", out)
	}
	if !strings.Contains(out, "= true") {
		t.Fatalf("expected default true, got:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithDefaultFloat(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "calc",
		Params: []*ast.ParamDecl{
			{
				Name:    "rate",
				Type:    &ast.TypeRef{Name: "Float"},
				Default: &ast.Literal{Kind: token.Float, Value: "0.05"},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamFloat("rate")`) {
		t.Fatalf("expected ParamFloat call, got:\n%s", out)
	}
	if !strings.Contains(out, "0.05") {
		t.Fatalf("expected default 0.05, got:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithDefaultCustomType(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "create",
		Params: []*ast.ParamDecl{
			{
				Name:    "input",
				Type:    &ast.TypeRef{Name: "CreateInput"},
				Default: &ast.Ident{Name: "false"}, // Fallback zero value
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	// Both transports use the prepared native message without target reflection.
	if !strings.Contains(out, "var input CreateInput") {
		t.Fatalf("expected var declaration, got:\n%s", out)
	}
	if strings.Contains(out, "req.ParamJSON") {
		t.Fatalf("unexpected arbitrary-target decoder:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamMessage("input", true, false)`) {
		t.Fatalf("expected optional native message extraction, got:\n%s", out)
	}
}

func TestCompileDefaultValueNullUsesNil(t *testing.T) {
	if got := compileDefaultValue(&ast.Literal{Kind: token.Null}, "*Payload", nil); got != "nil" {
		t.Fatalf("null default = %q, want nil", got)
	}
}

func TestCompileDefaultValueDuration(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{value: "5ms", want: "5 * time.Millisecond"},
		{value: "1h", want: "time.Hour"},
		{value: "2d", want: "2 * 24 * time.Hour"},
	}
	for _, tt := range tests {
		if got := compileDefaultValue(&ast.Literal{Kind: token.Duration, Value: tt.value}, "time.Duration", nil); got != tt.want {
			t.Errorf("duration default %s = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestCompileDefaultValueSignedNumber(t *testing.T) {
	value := &ast.UnaryExpr{Op: "-", Value: &ast.Literal{Kind: token.Int, Value: "1"}}
	if got := compileDefaultValue(value, "int64", nil); got != "-1" {
		t.Fatalf("signed default = %q, want -1", got)
	}
	duration := &ast.UnaryExpr{Op: "-", Value: &ast.Literal{Kind: token.Duration, Value: "5s"}}
	if got := compileDefaultValue(duration, "time.Duration", nil); got != "-(5 * time.Second)" {
		t.Fatalf("signed duration default = %q, want %q", got, "-(5 * time.Second)")
	}
}

func TestCompileDefaultValueFalseLiteral(t *testing.T) {
	if got := compileDefaultValue(&ast.Literal{Kind: token.False}, "bool", nil); got != "false" {
		t.Fatalf("false default = %q, want false", got)
	}
}

func TestTypedPermissionConditionRejectsEmptyFalseFragment(t *testing.T) {
	// Reuse the existing fragment shape; parser literals retain their spelling.
	literal := &ast.Literal{Kind: token.False}
	if got := compileTypedPermissionCondition(literal); got != "" {
		t.Fatalf("empty literal fragment produced a permission condition: %q", got)
	}
}

func TestCompileDurationLiteralExpression(t *testing.T) {
	c := newCompiler(nil)
	value := &ast.Literal{Kind: token.Duration, Value: "5s"}
	if got := c.compileExpr(value); got != "5 * time.Second" {
		t.Fatalf("duration expression = %q, want %q", got, "5 * time.Second")
	}
}

func TestCompileAPIBody_NullableParamIsRequired(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "update",
		Params: []*ast.ParamDecl{{
			Name: "note",
			Type: &ast.TypeRef{Name: "String", Nullable: true},
		}},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	if out := b.String(); !strings.Contains(out, `req.ParamIsNull("note")`) || !strings.Contains(out, `req.ParamString("note")`) || strings.Contains(out, `req.HasParam("note")`) {
		t.Fatalf("nullable parameter must remain required:\n%s", out)
	}
}

func TestCompileAPIBodyNullableParamWithNonNullDefault(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "lookup",
		Params: []*ast.ParamDecl{{
			Name:    "label",
			Type:    &ast.TypeRef{Name: "String", Nullable: true},
			Default: &ast.Literal{Kind: token.String, Value: "ready"},
		}},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()
	if !strings.Contains(out, `var _defaultLabel string = "ready"`) ||
		!strings.Contains(out, `var label *string = &_defaultLabel`) ||
		!strings.Contains(out, `if req.HasParam("label")`) || !strings.Contains(out, `req.ParamIsNull("label")`) || !strings.Contains(out, `req.ParamString("label")`) {
		t.Fatalf("nullable non-null default must use a stable pointer:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithEnumDefault(t *testing.T) {
	for _, member := range []string{"admin", "Admin", "ADMIN"} {
		t.Run(member, func(t *testing.T) {
			enums := map[string]bool{"Role": true}
			api := &ast.ApiDecl{
				Name: "list",
				Params: []*ast.ParamDecl{
					{
						Name: "role",
						Type: &ast.TypeRef{Name: "String"},
						Default: &ast.MemberExpr{
							Object: &ast.Ident{Name: "Role"},
							Field:  member,
						},
					},
				},
				Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
			}

			var b strings.Builder
			compileAPIBody(&b, api, nil, enums)
			out := b.String()

			// Enum defaults preserve the declared member's exact case.
			if !strings.Contains(out, "var role string = Role"+member+"\n") {
				t.Fatalf("expected enum default Role%s, got:\n%s", member, out)
			}
		})
	}
}

func TestCompileDefaultValue_Fallbacks(t *testing.T) {
	// Unsupported expr type — should use zero value based on goType
	got := compileDefaultValue(&ast.CallExpr{}, "int64", nil)
	if got != "0" {
		t.Errorf("int64 fallback = %q, want 0", got)
	}
	got = compileDefaultValue(&ast.CallExpr{}, "float64", nil)
	if got != "0" {
		t.Errorf("float64 fallback = %q, want 0", got)
	}
	got = compileDefaultValue(&ast.CallExpr{}, "string", nil)
	if got != `""` {
		t.Errorf("string fallback = %q, want \"\"", got)
	}
	got = compileDefaultValue(&ast.CallExpr{}, "bool", nil)
	if got != "false" {
		t.Errorf("bool fallback = %q, want false", got)
	}
	got = compileDefaultValue(&ast.CallExpr{}, "MyType", nil)
	if got != "MyType{}" {
		t.Errorf("custom type fallback = %q, want MyType{}", got)
	}
}

func TestCompileDefaultValue_IdentTrue(t *testing.T) {
	got := compileDefaultValue(&ast.Ident{Name: "true"}, "bool", nil)
	if got != "true" {
		t.Errorf("got %q, want true", got)
	}
}

func TestCompileDefaultValue_IdentFalse(t *testing.T) {
	got := compileDefaultValue(&ast.Ident{Name: "false"}, "bool", nil)
	if got != "false" {
		t.Errorf("got %q, want false", got)
	}
}
