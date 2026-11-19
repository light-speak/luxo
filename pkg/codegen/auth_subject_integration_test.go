package codegen

import (
	"strings"
	"testing"
)

func TestGeneratedAuthSubjectWebSocket(t *testing.T) {
	var proxy strings.Builder
	writeGatewayRPCStreamProxy(&proxy, []moduleInfo{{nativeStreams: []string{"userEvents", "adminEvents"}}})
	writeGatewayProxy(&proxy)
	runGeneratedFixture(t, `model User @noTime @withAuth(stores: [id], default: true) { id: Int @id }
model Admin @noTime @withAuth(stores: [id]) { id: String @id }
model Record @noTime { id: Int @id secret: String? @visible(my.id == 42) }
api record(): Record { return Record { id: 1, secret: "subject-secret" } }
event Changed(value: Int)
api user(): Int @auth { return my.id }
api admin(): String @auth(subject: Admin) { return my.id }
api userEvents(): Int @auth @stream(Changed) { true }
api adminEvents(): Int @auth(subject: Admin) @stream(Changed) { true }`, authSubjectWebSocketAssertions+proxy.String(),
		StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}, "Admin": {"id": 1}, "Record": {"id": 1, "secret": 2}}, EventFields: map[string]map[string]int{"Changed": {"value": 1}}})
}

const authSubjectWebSocketAssertions = `package generated
import("bytes";"context";"encoding/json";"io";"fmt";"net";"net/http";"net/http/httptest";"testing";"time";"sync";
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/luvia";"nhooyr.io/websocket")
func TestSubjectWebSocket(t *testing.T){
 t.Setenv("JWT_SECRET","subject-websocket");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 adminBody,err:=codec.AppendString(nil,"admin-42");if err!=nil{t.Fatal(err)}
 for _,forward:=range []bool{false,true}{for _,binary:=range []bool{false,true}{for _,sample:=range []struct{name,allowed,denied string;sign func()(string,error);jsonValue string;binaryValue []byte}{
  {"User","user","admin",(&User{Id:42}).CreateToken,"42",[]byte{84}},
  {"UserOther","user","admin",(&User{Id:41}).CreateToken,"41",[]byte{82}},
  {"Admin","admin","user",(&Admin{Id:"admin-42"}).CreateToken,"\"admin-42\"",adminBody},
  {"Unknown","","user",func()(string,error){return auth.SignForSubject(cfg,"Unknown",(&User{Id:42}).AppendClaimsJSON)},"",nil},
  {"Unbound","","user",func()(string,error){return auth.Sign(cfg,(&User{Id:42}).AppendClaimsJSON)},"",nil},
  {"Anonymous","","user",func()(string,error){return "",nil},"",nil},
 }{t.Run(fmt.Sprintf("%s/binary=%v/forward=%v",sample.name,binary,forward),func(t *testing.T){
  router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
  if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
  if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
  bus:=event.NewChanBus(8);defer bus.Close();if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
  if err:=router.Prepare();err!=nil{t.Fatal(err)}
  backend:=router
  if forward{router=subjectStreamGateway(t,backend)}
  server:=httptest.NewServer(luvia.AuthMiddleware(cfg,router));defer server.Close()
  token,err:=sample.sign();if err!=nil{t.Fatal(err)}
  ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
  checkSubjectHTTP(t,ctx,server.URL,token,router,binary,sample.allowed,sample.jsonValue,sample.binaryValue)
  checkSubjectVisibilityHTTP(t,ctx,server.URL,token,router,binary,sample.name=="User")
  headers:=make(http.Header);headers.Set("Authorization","Bearer "+token)
  conn,_,err:=websocket.Dial(ctx,"ws"+server.URL[4:],&websocket.DialOptions{HTTPHeader:headers});if err!=nil{t.Fatal(err)};defer conn.CloseNow()
  for _,sub:=range []bool{false,true}{
   suffix:="";if sub{suffix="Events"}
   rejected:=exchangeSubjectFrame(t,ctx,conn,router,binary,sub,sample.denied+suffix,1)
   requireSubjectError(t,rejected,binary,sub,1)
   if router.Streams.SubCount(sample.denied+"Events")!=0||backend.Streams.SubCount(sample.denied+"Events")!=0{t.Fatal("rejected subscription retained")}
   if sample.allowed==""{
    requireSubjectError(t,exchangeSubjectFrame(t,ctx,conn,router,binary,sub,"admin"+suffix,2),binary,sub,2)
    if router.Streams.SubCount("adminEvents")!=0||backend.Streams.SubCount("adminEvents")!=0{t.Fatal("unknown-subject subscription retained")}
    continue
   }
   accepted:=exchangeSubjectFrame(t,ctx,conn,router,binary,sub,sample.allowed+suffix,2)
   if sub{
    if binary{if !bytes.Equal(accepted,[]byte{api.BinaryFrameSubscribeSuccess,2}){t.Fatalf("ACK %x",accepted)}}else{var ack struct{OK bool};if err:=json.Unmarshal(accepted,&ack);err!=nil||!ack.OK{t.Fatalf("ACK %s %v",accepted,err)}}
   }else{
    if binary{want:=append([]byte{api.BinaryFrameCallSuccess,2},sample.binaryValue...);if !bytes.Equal(accepted,want){t.Fatalf("call %x want %x",accepted,want)}}else{var value struct{Data json.RawMessage;Code int};if err:=json.Unmarshal(accepted,&value);err!=nil||string(value.Data)!=sample.jsonValue||value.Code!=0{t.Fatalf("call %s want %s error %v",accepted,sample.jsonValue,err)}}
   }
  }
  if sample.allowed==""{return}
  if err:=bus.Emit(ctx,"Changed",&ChangedEvent{Value:7});err!=nil{t.Fatal(err)}
  _,data,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
  if binary{if !bytes.Equal(data,[]byte{api.BinaryFrameStream,2,14}){t.Fatalf("stream %x",data)}}else{var value struct{Data int};if err:=json.Unmarshal(data,&value);err!=nil||value.Data!=7{t.Fatalf("stream %s %v",data,err)}}
 })}}}
}
func TestSubjectGatewayStreamExpiry(t *testing.T){
 t.Setenv("JWT_SECRET","subject-stream-expiry");t.Setenv("JWT_EXPIRES","3s")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{t.Run(fmt.Sprintf("binary=%v",binary),func(t *testing.T){
  backend:=luvia.New().Router
  if err:=RegisterSchema(backend.Schema);err!=nil{t.Fatal(err)}
  if err:=backend.Registry.RegisterSchemaAPIs(backend.Schema);err!=nil{t.Fatal(err)}
  if err:=RegisterHandlers(backend,&App{});err!=nil{t.Fatal(err)}
  bus:=event.NewChanBus(8);defer bus.Close()
  if err:=RegisterStreams(backend,bus,nil);err!=nil{t.Fatal(err)}
  if err:=backend.Prepare();err!=nil{t.Fatal(err)}
  gateway:=subjectStreamGateway(t,backend)
  server:=httptest.NewServer(luvia.AuthMiddleware(cfg,gateway));defer server.Close()
  ctx,cancel:=context.WithTimeout(t.Context(),10*time.Second);defer cancel()
  expired:=subscribeExpiringSubject(t,ctx,server.URL,gateway,binary);defer expired.CloseNow()
  _,_,err:=expired.Read(ctx)
  if websocket.CloseStatus(err)!=api.WebSocketTokenExpired{t.Fatalf("expected expiry close, got %v",err)}
  timer:=time.NewTicker(time.Millisecond);defer timer.Stop()
  for gateway.Streams.SubCount("userEvents")!=0||backend.Streams.SubCount("userEvents")!=0{
   select{case <-timer.C:case <-ctx.Done():t.Fatal("expiry retained gateway or service subscription",ctx.Err())}
  }
  renewed:=subscribeExpiringSubject(t,ctx,server.URL,gateway,binary);defer renewed.CloseNow()
  if err:=bus.Emit(ctx,"Changed",&ChangedEvent{Value:9});err!=nil{t.Fatal(err)}
  _,data,err:=renewed.Read(ctx);if err!=nil{t.Fatal("new subscription failed after expiry",err)}
  if binary{if !bytes.Equal(data,[]byte{api.BinaryFrameStream,1,18}){t.Fatalf("new subscription value %x",data)}}else{
   var value struct{Data int};if err:=json.Unmarshal(data,&value);err!=nil||value.Data!=9{t.Fatalf("new subscription value %s %v",data,err)}
  }
 })}
}
func subscribeExpiringSubject(t *testing.T,ctx context.Context,url string,gateway *api.Router,binary bool)*websocket.Conn{
 t.Helper();token,err:=(&User{Id:42}).CreateToken();if err!=nil{t.Fatal(err)}
 headers:=make(http.Header);headers.Set("Authorization","Bearer "+token)
 conn,_,err:=websocket.Dial(ctx,"ws"+url[4:],&websocket.DialOptions{HTTPHeader:headers});if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){conn.CloseNow()})
 ack:=exchangeSubjectFrame(t,ctx,conn,gateway,binary,true,"userEvents",1)
 if binary{if !bytes.Equal(ack,[]byte{api.BinaryFrameSubscribeSuccess,1}){t.Fatalf("ACK %x",ack)}}else{
  var value struct{OK bool};if err:=json.Unmarshal(ack,&value);err!=nil||!value.OK{t.Fatalf("ACK %s %v",ack,err)}
 }
 return conn
}

func checkSubjectHTTP(t *testing.T,ctx context.Context,url,token string,router *api.Router,binary bool,allowed,raw string,encoded []byte){
 t.Helper()
 for _,name:=range []string{"user","admin"}{
  body:=[]byte(fmt.Sprintf("{\"$api\":%q}",name));kind:="application/json"
  if binary{var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)};kind="application/x-luxo"}
  req,err:=http.NewRequestWithContext(ctx,"POST",url,bytes.NewReader(body));if err!=nil{t.Fatal(err)}
  req.Header.Set("Authorization","Bearer "+token);req.Header.Set("Content-Type",kind)
  if binary{req.Header.Set("X-Luxo-Mode","binary")}
  response,err:=http.DefaultClient.Do(req);if err!=nil{t.Fatal(err)}
  data,readErr:=io.ReadAll(response.Body);closeErr:=response.Body.Close();if readErr!=nil||closeErr!=nil{t.Fatal(readErr,closeErr)}
  want:=401;if name==allowed{want=200}
  if response.StatusCode!=want{t.Fatalf("HTTP %s status=%d want=%d body=%s",name,response.StatusCode,want,data)}
  if want!=200{continue}
  if binary{if !bytes.Equal(data,encoded){t.Fatalf("HTTP %s body=%x want=%x",name,data,encoded)}}else{
   var value struct{Data json.RawMessage};if err:=json.Unmarshal(data,&value);err!=nil||string(value.Data)!=raw{t.Fatalf("HTTP %s body=%s error=%v",name,data,err)}
  }
 }
}
func checkSubjectVisibilityHTTP(t *testing.T,ctx context.Context,url,token string,router *api.Router,binary,visible bool){
 t.Helper()
 body:=[]byte("{\"$api\":\"record\",\"$select\":\"id,secret\"}");kind:="application/json"
 if binary{
  mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
  body,err=api.EncodeBinaryRequest(router.Schema.APIs["record"].ID,mask,nil,nil);if err!=nil{t.Fatal(err)};kind="application/x-luxo"
 }
 req,err:=http.NewRequestWithContext(ctx,"POST",url,bytes.NewReader(body));if err!=nil{t.Fatal(err)}
 req.Header.Set("Content-Type",kind);if token!=""{req.Header.Set("Authorization","Bearer "+token)}
 if binary{req.Header.Set("X-Luxo-Mode","binary")}
 response,err:=http.DefaultClient.Do(req);if err!=nil{t.Fatal(err)}
 data,readErr:=io.ReadAll(response.Body);closeErr:=response.Body.Close();if readErr!=nil||closeErr!=nil{t.Fatal(readErr,closeErr)}
 if response.StatusCode!=200{t.Fatalf("public record: %d %s",response.StatusCode,data)}
 var value Record
 if binary{dec:=codec.NewDecoder(data);value.ReadLuxo(dec);if dec.Err()!=nil{t.Fatal(dec.Err())}}else{
  var envelope struct{Data Record};if err:=json.Unmarshal(data,&envelope);err!=nil{t.Fatal(err)};value=envelope.Data
 }
 if value.Id!=1 || (value.Secret!=nil)!=visible{t.Fatalf("record=%+v visible=%v body=%x",value,visible,data)}
 if visible && *value.Secret!="subject-secret"{t.Fatal("identity changed visible value")}
 if bytes.Contains(data,[]byte("subject-secret"))!=visible{t.Fatal("hidden bytes leaked")}
}
func subjectStreamGateway(t *testing.T,backend *api.Router)*api.Router{
 t.Helper()
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 service:=rpc.NewServer(backend);done:=make(chan error,1);go func(){done<-service.Serve(listener)}()
 client:=rpc.NewClient(listener.Addr().String())
 t.Cleanup(func(){client.Close();service.Close();if err:=<-done;err!=nil{t.Error(err)}})
 gateway:=luvia.New().Router
 if err:=RegisterSchema(gateway.Schema);err!=nil{t.Fatal(err)}
 if err:=gateway.Registry.RegisterSchemaAPIs(gateway.Schema);err!=nil{t.Fatal(err)}
 for _,name:=range []string{"user","admin","record"}{gateway.Handle(name,proxyHandler(client,gateway.Schema,name,map[string]*rpc.Client{"backend":client}))}
 for _,name:=range []string{"userEvents","adminEvents"}{gateway.HandleStreamForwarded(name,rpcStreamProxy(client,gateway,name))}
 if err:=gateway.Prepare();err!=nil{t.Fatal(err)}
 return gateway
}
func exchangeSubjectFrame(t *testing.T,ctx context.Context,conn *websocket.Conn,router *api.Router,binary,sub bool,name string,id byte)[]byte{
 t.Helper();kind:=websocket.MessageText;key:="$api";if sub{key="$sub"}
 body:=[]byte(fmt.Sprintf("{%q:%q,\"$id\":%d}",key,name,id))
 if binary{
  payload,err:=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}
  frame:=api.BinaryFrameCallRequest;if sub{frame=api.BinaryFrameSubscribe};body=append([]byte{frame,id},payload...);kind=websocket.MessageBinary
 }
 if err:=conn.Write(ctx,kind,body);err!=nil{t.Fatal(err)}
 got,body,err:=conn.Read(ctx);if err!=nil||got!=kind{t.Fatal(got,err)};return body
}
func requireSubjectError(t *testing.T,body []byte,binary,sub bool,id byte){
 t.Helper()
 if binary{
  kind:=api.BinaryFrameCallError;if sub{kind=api.BinaryFrameSubscribeError}
  if len(body)<3||body[0]!=kind||body[1]!=id{t.Fatalf("error frame %x",body)}
  failure,err:=api.DecodeBinaryError(body[2:],0);if err!=nil||failure.Code!=401{t.Fatalf("error %v %v",failure,err)}
 }else{var failure struct{Code int};if err:=json.Unmarshal(body,&failure);err!=nil||failure.Code!=401{t.Fatalf("error %s %v",body,err)}}
}
`

func TestGeneratedAuthSubjectCRUDRejectsBeforeDatabase(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime @crud @withAuth(stores: [id], default: true) { id: Int @id }
model Admin @noTime @crud @withAuth(stores: [id]) { id: Int @id }`, `package generated
import("bytes";"github.com/light-speak/luxo/pkg/lux/codec";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestCRUDSubjectBeforeParams(t *testing.T){
 t.Setenv("JWT_SECRET","crud-subject");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,issuer:=range []struct{name string;sign func()(string,error)}{
  {"User",(&User{Id:42}).CreateToken},{"Admin",(&Admin{Id:42}).CreateToken},
 }{
  token,err:=issuer.sign();if err!=nil{t.Fatal(err)}
  for _,model:=range []string{"User","Admin"}{if model==issuer.name{continue};for _,op:=range []string{"get","update","delete"}{for _,binary:=range []bool{false,true}{
   name:=op+model;entry:=router.Schema.APIs[name];if entry==nil{t.Fatalf("missing CRUD %s",name)}
   body:=[]byte("{\"$api\":\""+name+"\",\"$select\":\"id\",\"id\":1}");kind:="application/json"
   if binary{mask,e:=codec.AppendSelectionMask(nil,[]byte{1},nil);if e!=nil{t.Fatal(e)};body,err=api.EncodeBinaryRequest(entry.ID,mask,map[string]any{"id":int64(1)},router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)};kind="application/x-luxo"}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type",kind);req.Header.Set("Authorization","Bearer "+token)
   if binary{req.Header.Set("X-Luxo-Mode","binary")};out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   want:=401
   if out.Code!=want{t.Fatalf("%s issuer=%s binary=%v status=%d want=%d body=%s",name,issuer.name,binary,out.Code,want,out.Body)}
  }}}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}, "Admin": {"id": 1}}})
}

func TestGeneratedVisibilityRejectsOtherSubject(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime @withAuth(stores: [id, role], default: true) { id: Int @id role: String }
model Admin @noTime @withAuth(stores: [id, role]) { id: Int @id role: String }
model Record @noTime { id: Int @id secret: String? @visible(my.role == "member") }`, `package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec")
func TestVisibleSubject(t *testing.T){
 t.Setenv("JWT_SECRET","visible-subject");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 secret:="subject-protected-secret";record:=Record{Id:1,Secret:&secret}
 for _,issuer:=range []struct{sign func()(string,error);allowed bool}{
  {(&User{Id:42,Role:"member"}).CreateToken,true},
  {(&Admin{Id:42,Role:"member"}).CreateToken,false},
  {func()(string,error){return auth.Sign(cfg,(&User{Id:42,Role:"member"}).AppendClaimsJSON)},false},
 }{
  token,err:=issuer.sign();if err!=nil{t.Fatal(err)};claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
  for name,write:=range map[string]func(*api.ResponseBuf)error{
   "row":func(buf *api.ResponseBuf)error{return record.WriteLuxo(buf,mask)},
   "pointers":func(buf *api.ResponseBuf)error{return WriteColumnarRecord(buf,[]*Record{&record},mask)},
   "values":func(buf *api.ResponseBuf)error{return WriteColumnarRecordValues(buf,[]Record{record},mask)},
  }{
   buf:=&api.ResponseBuf{Identity:claims};if err:=write(buf);err!=nil{t.Fatal(err)}
   if leaked:=bytes.Contains(buf.B,[]byte(secret));leaked!=issuer.allowed{t.Errorf("%s subject=%s visible=%v want=%v",name,claims.Subject(),leaked,issuer.allowed)}
  }
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "role": 2}, "Admin": {"id": 1, "role": 2}, "Record": {"id": 1, "secret": 2}}})
}

func TestGeneratedCompoundPermission(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime @withAuth(stores: [id, enabled, score]) { id: Int @id enabled: Boolean score: Float }
api combined(): Int @auth(permission: { !my.enabled && my.id >= 42 && my.score < 1.0 }) { return 7 }
api shortCircuit(): Int @auth(permission: { true || my.enabled }) { return 7 }
api constant(): Int @auth(permission: { true }) { return 7 }
api selfDifferent(): Int @auth(permission: { my.id != my.id }) { return 7 }
api negated(): Int @auth(permission: { !(my.id < 42 || my.enabled) }) { return 7 }`, `package generated
import("bytes";"github.com/light-speak/luxo/pkg/lux/api";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestCompoundPermission(t *testing.T){
 t.Setenv("JWT_SECRET","compound-permission");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,name:=range []string{"constant","shortCircuit"}{
  req:=httptest.NewRequest("POST","/luvia",bytes.NewBufferString("{\"$api\":\""+name+"\"}"));req.Header.Set("Content-Type","application/json")
  out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req);if out.Code!=401{t.Fatalf("%s bypassed authentication: %d",name,out.Code)}
 }
 for _,sample:=range []struct{raw string;combined,negated int}{
  {`+"`"+`{"id":42,"enabled":false,"score":0.5}`+"`"+`,200,200},
  {`+"`"+`{"id":41,"enabled":false,"score":0.5}`+"`"+`,403,403},
  {`+"`"+`{"id":42,"enabled":true,"score":0.5}`+"`"+`,403,403},
  {`+"`"+`{"id":42,"score":0.5}`+"`"+`,403,403},
  {`+"`"+`{"id":42,"enabled":null,"score":0.5}`+"`"+`,403,403},
  {`+"`"+`{"id":"42","enabled":false,"score":0.5}`+"`"+`,403,403},
  {`+"`"+`{}`+"`"+`,403,403},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for name,want:=range map[string]int{"combined":sample.combined,"negated":sample.negated,"shortCircuit":200,"constant":200,"selfDifferent":403}{for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\""+name+"\"}");kind:="application/json"
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)};kind="application/x-luxo"}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type",kind);req.Header.Set("Authorization","Bearer "+token)
   if binary{req.Header.Set("X-Luxo-Mode","binary")}
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req);if out.Code!=want{t.Errorf("%s %s binary=%v status=%d want=%d body=%s",name,sample.raw,binary,out.Code,want,out.Body)}
  }}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "enabled": 2, "score": 3}}})
}

func TestGeneratedEnumPermissionRejectsUnknown(t *testing.T) {
	runGeneratedFixture(t, `enum Role { MEMBER BLOCKED }
model User @noTime @withAuth(stores: [id, role]) { id: Int @id role: Role }
api allowed(): Int @auth(permission: { my.role != Role.BLOCKED }) { return 7 }
api negated(): Int @auth(permission: { !(my.role == Role.BLOCKED) }) { return 7 }
api combined(): Int @auth(permission: { true && my.role != Role.BLOCKED }) { return 7 }`, `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestEnumPermission(t *testing.T){
 t.Setenv("JWT_SECRET","enum-permission");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw string;want int}{
  {`+"`"+`{"role":"MEMBER"}`+"`"+`,200},{`+"`"+`{"role":"BLOCKED"}`+"`"+`,403},
  {`+"`"+`{"role":"UNKNOWN"}`+"`"+`,403},{`+"`"+`{"role":""}`+"`"+`,403},
  {`+"`"+`{"role":null}`+"`"+`,403},{`+"`"+`{}`+"`"+`,403},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,name:=range []string{"allowed","negated","combined"}{for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\""+name+"\"}");kind:="application/json"
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)};kind="application/x-luxo"}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type",kind);req.Header.Set("Authorization","Bearer "+token)
   if binary{req.Header.Set("X-Luxo-Mode","binary")};out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   if out.Code!=sample.want{t.Errorf("%s %s binary=%v status=%d want=%d body=%s",name,sample.raw,binary,out.Code,sample.want,out.Body)}
  }}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "role": 2}}})
}
