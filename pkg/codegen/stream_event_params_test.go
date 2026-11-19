package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedEventStreamNestedIdentityVisibility(t *testing.T) {
	runGeneratedFixture(t, `
type Detail { id: Int secret: String? @visible(my.id == 42) }
type Envelope { detail: Detail }
event Wrapped(payload: Envelope)
api watchNested(): Envelope @stream(Wrapped) { true }
`, `package generated
import("bytes";"context";"encoding/json";"fmt";"testing";"github.com/light-speak/luxo/pkg/lux/auth";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/event")
type identityKey struct{}
func TestNestedEventVisibility(t *testing.T){
 router:=api.NewRouter();router.IdentityExtractor=func(ctx context.Context)any{return ctx.Value(identityKey{})}
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 bus:=event.NewChanBus(8);defer bus.Close()
 if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
 child,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{1},[]codec.SelectionMaskChild{{FieldID:1,Mask:child}});if err!=nil{t.Fatal(err)}
 var subscribers []*api.InternalStream
 for _,id:=range []int64{42,99,42,99}{
  cfg:=&auth.Config{Secret:"nested-event-identity-test",Expires:time.Hour}
  token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return fmt.Appendf(dst,"{\"id\":%d}",id),nil});if err!=nil{t.Fatal(err)}
  identity,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
  ctx:=context.WithValue(t.Context(),identityKey{},identity)
  selection:=mask
  if len(subscribers)>=2{
   public,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)}
   selection,err=codec.AppendSelectionMask(nil,[]byte{1},[]codec.SelectionMaskChild{{FieldID:1,Mask:public}});if err!=nil{t.Fatal(err)}
  }
  sub,err:=router.OpenInternalStream(ctx,&api.Request{API:"watchNested",FieldMask:selection});if err!=nil{t.Fatal(err)}
  defer sub.Close();subscribers=append(subscribers,sub)
 }
 secret:="nested-private"
 if err:=bus.Emit(t.Context(),"Wrapped",&WrappedEvent{Payload:Envelope{Detail:&Detail{Id:1,Secret:&secret}}});err!=nil{t.Fatal(err)}
 for index,sub:=range subscribers{
  select{case body,ok:=<-sub.Messages():
   if !ok{t.Fatal("stream closed",sub.Err())}
   if bytes.Contains(body,[]byte(secret))!=(index==0){t.Fatalf("nested identity lost for %d: %x",index,body)}
   text,err:=router.StreamPayloadJSON("watchNested",body);if err!=nil{t.Fatal(err)}
   var decoded struct{Detail struct{ID int64;Secret *string}}
   if err:=json.Unmarshal(text,&decoded);err!=nil{t.Fatal(err)}
   if decoded.Detail.ID!=1||(decoded.Detail.Secret!=nil)!=(index==0){t.Fatalf("nested JSON selection/identity lost for %d: %s",index,text)}
  case <-time.After(time.Second):t.Fatal("event missing")}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Detail": {"id": 1, "secret": 2}, "Envelope": {"detail": 1}}, EventFields: map[string]map[string]int{"Wrapped": {"payload": 1}}})
}

func eventVisibilityIDs() StableIDs {
	return StableIDs{APIs: map[string]int{"watch": 1}, ModelFields: map[string]map[string]int{"Profile": {"id": 1, "secret": 2, "anonymous": 3}}, EventFields: map[string]map[string]int{"Changed": {"payload": 1}}}
}

func TestGeneratedEventStreamIdentityVisibility(t *testing.T) {
	runGeneratedFixture(t, eventVisibilitySource, eventVisibilityAssertions, eventVisibilityIDs())
}

func TestGeneratedNATSEventStreamIdentityVisibility(t *testing.T) {
	ensureGeneratedNATS(t)
	assertions := strings.ReplaceAll(eventVisibilityAssertions, "event.NewChanBus(8)", "newNATSVisibilityBus(t)")
	assertions = strings.Replace(assertions, `;"github.com/light-speak/luxo/pkg/lux/event"`, "", 1)
	runGeneratedFixture(t, eventVisibilitySource, assertions, eventVisibilityIDs(), `package generated
import("os";"testing";"github.com/light-speak/luxo/pkg/lux/event";natsevent "github.com/light-speak/luxo/pkg/lux/nats/event")
func newNATSVisibilityBus(t *testing.T)event.Bus{
 t.Helper();address:=os.Getenv("NATS_URL");if address==""{t.Fatal("missing isolated NATS endpoint")}
 bus,err:=natsevent.NewNATSBus(address);if err!=nil{t.Fatal(err)};return bus
}
`)
}

const eventVisibilitySource = `
type Profile { id: Int secret: String? @visible(my.id == 42) anonymous: String? @visible(my.id == 0) }
event Changed(payload: Profile)
api watch(): Profile @stream(Changed) { true }
`

const eventVisibilityAssertions = `package generated
import("bytes";"context";"fmt";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia";"testing";"time";"net";"net/http";"net/http/httptest";"strings";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/rpc";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"nhooyr.io/websocket")
func visibilityConfig()*auth.Config{return &auth.Config{Secret:"event-visibility-test",Expires:time.Hour}}
func visibilityToken(t *testing.T,id int64)string{
 t.Helper();token,err:=auth.Sign(visibilityConfig(),func(dst []byte)([]byte,error){return fmt.Appendf(dst,"{\"id\":%d}",id),nil});if err!=nil{t.Fatal(err)};return token
}
func visibilityClaims(t *testing.T,id int64)auth.Claims{
 t.Helper();claims,err:=auth.Verify(visibilityConfig(),visibilityToken(t,id));if err!=nil{t.Fatal(err)};return claims
}
type streamIdentityKey struct{}
func TestEventVisibility(t *testing.T){
 router:=api.NewRouter();router.IdentityExtractor=func(ctx context.Context)any{return ctx.Value(streamIdentityKey{})}
 bus:=event.NewChanBus(8);defer bus.Close()
 if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
 subscribers:=make([]*api.InternalStream,0,5)
 for index,id:=range []int64{42,99,-1,99,42,0}{
  ctx:=t.Context();if id>=0{ctx=context.WithValue(ctx,streamIdentityKey{},visibilityClaims(t,id))}
  var mask []byte;if index==3||index==4{var err error;mask,err=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)}}
  sub,err:=router.OpenInternalStream(ctx,&api.Request{API:"watch",BinaryMode:true,FieldMask:mask});if err!=nil{t.Fatal(err)}
  defer sub.Close();subscribers=append(subscribers,sub)
 }
 secret,anonymous:="private-for-42","anonymous-only"
 if err:=bus.Emit(t.Context(),"Changed",&ChangedEvent{Payload:Profile{Id:1,Secret:&secret,Anonymous:&anonymous}});err!=nil{t.Fatal(err)}
 var shared []byte
 for index,sub:=range subscribers{
  select{case body,ok:=<-sub.Messages():
   if !ok{t.Fatal("stream closed",sub.Err())}
   if bytes.Contains(body,[]byte(secret))!=(index==0){t.Errorf("subscriber %d visibility wrong: %x",index,body)}
   if bytes.Contains(body,[]byte(anonymous))!=(index==5){t.Errorf("subscriber %d anonymous visibility wrong: %x",index,body)}
   if index==3{shared=body};if index==4&&(len(body)==0||len(shared)==0||&body[0]!=&shared[0]){t.Fatal("public projection not reused")}
  case <-time.After(time.Second):t.Fatal("missing event")}
 }
}
func TestEventVisibilityLRPC(t *testing.T){
 router:=api.NewRouter();router.IdentityExtractor=func(ctx context.Context)any{return ctx.Value(streamIdentityKey{})}
 router.InternalRequestContext=func(ctx context.Context,token string)(context.Context,error){
  claims,err:=auth.Verify(visibilityConfig(),token);if err!=nil{return nil,luxerrors.Unauthorized};return context.WithValue(ctx,streamIdentityKey{},claims),nil
 }
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 bus:=event.NewChanBus(8);defer bus.Close()
 if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
 mask,err:=codec.AppendSelectionMask(nil,[]byte{7},nil);if err!=nil{t.Fatal(err)}
 rejected,err:=client.SubscribeContext(ctx,"unverified",1,mask,nil)
 if rejected!=nil{rejected.Close();t.Fatal("invalid token acknowledged")}
 failure,ok:=err.(*luxerrors.AppError)
 if !ok||failure.Code!=401||router.Streams.SubCount("watch")!=0{t.Fatal("invalid token did not fail as unauthorized",err)}
 var subscriptions []*rpc.Subscription
 for _,token:=range []string{visibilityToken(t,42),visibilityToken(t,99),"",visibilityToken(t,0)}{
  sub,err:=client.SubscribeContext(ctx,token,1,mask,nil);if err!=nil{t.Fatal(err)}
  defer sub.Close();subscriptions=append(subscriptions,sub)
 }
 if router.Streams.SubCount("watch")!=4{t.Fatal("missing subscriptions")}
 secret,anonymous:="private-for-42","anonymous-only"
 if err:=bus.Emit(ctx,"Changed",&ChangedEvent{Payload:Profile{Id:1,Secret:&secret,Anonymous:&anonymous}});err!=nil{t.Fatal(err)}
 for index,sub:=range subscriptions{
  select{case body,ok:=<-sub.Messages():
   if !ok{t.Fatal("stream closed",sub.Err())}
   if bytes.Contains(body,[]byte(secret))!=(index==0)||bytes.Contains(body,[]byte(anonymous))!=(index==3){t.Fatalf("identity %d received wrong fields: %x",index,body)}
  case <-ctx.Done():t.Fatal(ctx.Err())}
 }
}
func TestEventVisibilityWebSocket(t *testing.T){
 for _,binary:=range []bool{false,true}{t.Run(map[bool]string{false:"json",true:"binary"}[binary],func(t *testing.T){
  router:=api.NewRouter();router.IdentityExtractor=func(ctx context.Context)any{return ctx.Value(streamIdentityKey{})}
  if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
  if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
  bus:=event.NewChanBus(8);defer bus.Close()
  if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
  router.IdentityExtractor=luvia.New().Router.IdentityExtractor
  server:=httptest.NewServer(luvia.AuthMiddleware(visibilityConfig(),router));defer server.Close()
  ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
  var connections []*websocket.Conn
  for _,id:=range []int64{42,99,-1,0}{
   headers:=make(http.Header);if id>=0{headers.Set("Authorization","Bearer "+visibilityToken(t,id))}
   conn,_,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(server.URL,"http"),&websocket.DialOptions{HTTPHeader:headers});if err!=nil{t.Fatal(err)}
   defer conn.CloseNow();connections=append(connections,conn)
   kind,frame:=websocket.MessageText,[]byte("{\"$id\":1,\"$sub\":\"watch\",\"$select\":\"id,secret,anonymous\"}")
   if binary{
    mask,err:=codec.AppendSelectionMask(nil,[]byte{7},nil);if err!=nil{t.Fatal(err)}
    body,err:=api.EncodeBinaryRequest(1,mask,nil,nil);if err!=nil{t.Fatal(err)}
    kind,frame=websocket.MessageBinary,append([]byte{api.BinaryFrameSubscribe,1},body...)
   }
   if err:=conn.Write(ctx,kind,frame);err!=nil{t.Fatal(err)}
   _,ack,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
   if binary{if !bytes.Equal(ack,[]byte{api.BinaryFrameSubscribeSuccess,1}){t.Fatalf("bad ACK %x",ack)}}else if !bytes.Contains(ack,[]byte("\"ok\":true")){t.Fatalf("bad ACK %s",ack)}
  }
  secret,anonymous:="private-for-42","anonymous-only"
  if err:=bus.Emit(ctx,"Changed",&ChangedEvent{Payload:Profile{Id:1,Secret:&secret,Anonymous:&anonymous}});err!=nil{t.Fatal(err)}
  for index,conn:=range connections{
   kind,body,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
   if binary{if kind!=websocket.MessageBinary||len(body)<3||body[0]!=api.BinaryFrameStream||body[1]!=1{t.Fatalf("bad data %x",body)}}else if kind!=websocket.MessageText||!bytes.Contains(body,[]byte("\"data\":")){t.Fatalf("bad data %s",body)}
   if bytes.Contains(body,[]byte(secret))!=(index==0)||bytes.Contains(body,[]byte(anonymous))!=(index==3){t.Fatalf("identity %d received wrong fields: %s",index,body)}
  }
 })}
}
`

func TestGeneratedStreamDurationLiteral(t *testing.T) {
	runGeneratedFixture(t, `event Elapsed(value: Duration)
api watch(): Duration @stream(Elapsed) { it.value == 5m || it.value == 1h }
api watchDay(): Duration @stream(Elapsed) { it.value == 1d }
api watchNegative(): Duration @stream(Elapsed) { it.value == -5m }
`, `package generated
import("testing";"time")
func TestDurationMatchers(t *testing.T) {
 for _, sample := range []struct{value time.Duration; want bool}{
  {5*time.Minute-time.Nanosecond,false},{5*time.Minute,true},{time.Hour-time.Nanosecond,false},{time.Hour,true},
 } {
  if got:=matchWatch(int64(sample.value), &WatchStreamParams{}, nil); got!=sample.want {t.Fatalf("value %v: %v",sample.value,got)}
 }
 if !matchWatchDay(int64(24*time.Hour), &WatchDayStreamParams{}, nil) {t.Fatal("day changed")}
 if !matchWatchNegative(int64(-5*time.Minute), &WatchNegativeStreamParams{}, nil) {t.Fatal("negative duration changed")}
}


`, StableIDs{EventFields: map[string]map[string]int{"Elapsed": {"value": 1}}})
}

func TestFormalGenerationRejectsInvalidStreamMatcher(t *testing.T) {
	value := &ast.Literal{Kind: token.True, Value: "true"}
	for name, body := range map[string]*ast.Block{
		"empty":       {},
		"multiple":    {Stmts: []ast.Stmt{&ast.ExprStmt{Expr: value}, &ast.ExprStmt{Expr: value}}},
		"unsupported": {Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "unsupported"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			input := result(&ast.File{
				Events: []*ast.EventDecl{{Name: "Changed", Params: []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}}}},
				APIs: []*ast.ApiDecl{{Name: "watch", ReturnType: &ast.TypeRef{Name: "Int"}, Body: body,
					Directives: []*ast.Directive{{Name: "stream", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Changed"}}}}}}},
			})
			config := GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"watch": 1}, EventFields: map[string]map[string]int{"Changed": {"value": 1}}}}
			generated, err := Generate(input, "generated", config)
			if err == nil || generated != nil || !strings.Contains(err.Error(), "matcher") {
				t.Fatalf("invalid matcher produced output: %v", err)
			}
		})
	}
}

func TestGeneratedEventStreamTypedPreparation(t *testing.T) {
	file := &ast.File{Name: "events.luxo", Enums: []*ast.EnumDecl{{Name: "State", Values: []string{"OPEN", "CLOSED"}}},
		Events: []*ast.EventDecl{{Name: "Changed", Params: []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}, {Name: "state", Type: &ast.TypeRef{Name: "State"}}}}}}
	for _, native := range []bool{false, true} {
		name := "watch"
		if native {
			name = "watchNative"
		}
		api := &ast.ApiDecl{Name: name, ReturnType: &ast.TypeRef{Name: "Int"}, Params: []*ast.ParamDecl{
			{Name: "state", Type: &ast.TypeRef{Name: "State"}}, {Name: "limit", Type: &ast.TypeRef{Name: "Int"}, Default: &ast.Literal{Kind: token.Int, Value: "7"}},
		}, Directives: []*ast.Directive{{Name: "stream", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Changed"}}}}, {Name: "auth", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Admin"}}}}}}
		if native {
			api.Directives = append(api.Directives, &ast.Directive{Name: "native"})
		} else {
			api.Body = &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BinaryExpr{
				Left: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "state"}, Op: "==", Right: &ast.Ident{Name: "state"},
			}}}}
		}
		file.APIs = append(file.APIs, api)
	}
	source := string(generateStreamFile(&semantic.Result{Files: []*ast.File{file}}, "generated"))
	if strings.Contains(source, "api.StreamParams") || strings.Contains(source, "params.Lookup") {
		t.Fatal("event matcher retained dynamic parameters")
	}
	var startup strings.Builder
	startup.WriteString("\nfunc runFailedStreamStartup() {\nrouter:=api.NewRouter();bus:=event.NewChanBus(1);bus.Close()\n")
	writeStartupRegistration(&startup, "streams", "RegisterStreams(router, bus, eventResolver{})")
	startup.WriteString("fmt.Println(\"unexpected ready\")\n}\n")
	dir := writeProtocolFixture(t, source, eventStreamPreparedAssertions+startup.String())
	if err := os.WriteFile(filepath.Join(dir, "event.gen.go"), []byte(generatedSnapshotSource(t, file)), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-count=1", "-timeout=30s", ".")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated event stream: %v\n%s", err, output)
	}
}

const eventStreamPreparedAssertions = `package generated
import("bytes";"context";"errors";"fmt";"net/http";"net/http/httptest";"os";"os/exec";"strings";"testing";"time";
"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/event";"nhooyr.io/websocket")
type eventResolver struct{}
func(eventResolver)MatchWatchNative(e ChangedEvent,p *WatchNativeStreamParams,_ any)bool{return e.State==p.State&&e.Value==p.Limit}
func TestEventPreparation(t *testing.T){
 cfg:=&auth.Config{Secret:"event-stream-preparation-test",Expires:time.Hour}
 for _,binary:=range []bool{false,true}{for _,name:=range []string{"watch","watchNative"}{for _,sample:=range []struct{role,state string;code int}{
  {"Admin","OPEN",0},{"Admin","UNKNOWN",400},{"Member","OPEN",403},{"","OPEN",401},
 }{t.Run(fmt.Sprintf("%s/%s/%s/binary=%t",name,sample.role,sample.state,binary),func(t *testing.T){
  router:=api.NewRouter();router.Schema.RegisterEnum(&schema.Enum{Name:"State",Values:[]string{"OPEN","CLOSED"}})
  for i,n:=range []string{"watch","watchNative"}{router.Schema.RegisterAPI(&schema.API{ID:i+1,Name:n,Stream:true,ReturnType:"Int",Params:[]schema.Param{
   {ID:1,Name:"state",Type:schema.FieldEnum,TypeName:"State"},{ID:2,Name:"limit",Type:schema.FieldInt,HasDefault:true},
  }})}
  router.Registry.RegisterSchemaAPIs(router.Schema)
  router.IdentityExtractor=luvia.New().Router.IdentityExtractor
  bus:=event.NewChanBus(16);defer bus.Close();if err:=RegisterStreams(router,bus,eventResolver{});err!=nil{t.Fatal(err)}
  if err:=router.Validate();err!=nil{t.Fatal(err)}
  server:=httptest.NewServer(luvia.AuthMiddleware(cfg,router));defer server.Close()
  ctx,cancel:=context.WithTimeout(t.Context(),3*time.Second);defer cancel()
  options:=&websocket.DialOptions{HTTPHeader:make(http.Header)}
  if sample.role!=""{token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return fmt.Appendf(dst,"{\"id\":42,\"role\":%q}",sample.role),nil});if err!=nil{t.Fatal(err)};options.HTTPHeader.Set("Authorization","Bearer "+token)}
  conn,_,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(server.URL,"http"),options);if err!=nil{t.Fatal(err)};defer conn.CloseNow()
  mode:=websocket.MessageText;frame:=[]byte(fmt.Sprintf("{\"$sub\":%q,\"$id\":1,\"state\":%q}",name,sample.state))
  if binary{id:=1;if name=="watchNative"{id=2};body,err:=api.EncodeBinaryRequest(id,nil,map[string]any{"state":sample.state},[]api.ParamMeta{{Name:"state",Type:"Enum",FieldID:1},{Name:"limit",Type:"Int",FieldID:2}});if err!=nil{t.Fatal(err)};mode=websocket.MessageBinary;frame=append([]byte{api.BinaryFrameSubscribe,1},body...)}
  if err:=conn.Write(ctx,mode,frame);err!=nil{t.Fatal(err)};_,ack,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
  if sample.code!=0{
   if binary{if len(ack)<3||ack[0]!=api.BinaryFrameSubscribeError{t.Fatalf("invalid ACK %x",ack)};failure,err:=api.DecodeBinaryError(ack[2:],0);if err!=nil||failure.Code!=sample.code{t.Fatalf("error %v %v",failure,err)}}else if !bytes.Contains(ack,[]byte(fmt.Sprintf("\"code\":%d",sample.code))){t.Fatalf("invalid ACK %s",ack)}
   if router.Streams.SubCount(name)!=0{t.Fatal("rejected input retained")};return
  }
  if binary{if !bytes.Equal(ack,[]byte{api.BinaryFrameSubscribeSuccess,1}){t.Fatalf("ACK %x",ack)}}else if !bytes.Contains(ack,[]byte("\"ok\":true")){t.Fatalf("ACK %s",ack)}
  if err:=bus.Emit(ctx,"Changed",&ChangedEvent{Value:99,State:StateCLOSED});err!=nil{t.Fatal(err)}
  if err:=bus.Emit(ctx,"Changed",&ChangedEvent{Value:7,State:StateOPEN});err!=nil{t.Fatal(err)}
  _,data,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
  if binary{if !bytes.Equal(data,[]byte{api.BinaryFrameStream,1,14}){t.Fatalf("data %x",data)}}else if !bytes.Contains(data,[]byte("\"data\":7")){t.Fatalf("data %s",data)}
 })}}}
}
func TestFailedEventRegistration(t *testing.T){
 if err:=RegisterStreams(api.NewRouter(),nil,eventResolver{});err==nil||!strings.Contains(err.Error(),"watch"){t.Fatalf("nil bus accepted: %v",err)}
 bus:=event.NewChanBus(1);bus.Close()
 if err:=RegisterStreams(api.NewRouter(),bus,eventResolver{});!errors.Is(err,event.ErrBusClosed)||!strings.Contains(err.Error(),"watch"){t.Fatalf("closed bus error lost: %v",err)}
 rejected:=errors.New("subscription rejected")
 partial:=&failingEventBus{failure:rejected}
 if err:=RegisterStreams(api.NewRouter(),partial,eventResolver{});!errors.Is(err,rejected)||!strings.Contains(err.Error(),"watchNative"){t.Fatalf("second listener error lost: %v",err)}
 if partial.calls!=2{t.Fatalf("registration attempts %d",partial.calls)}
}
type failingEventBus struct{calls int;failure error}
func(b *failingEventBus)On(string,event.Handler)error{b.calls++;if b.calls==2{return b.failure};return nil}
func(*failingEventBus)OnQueue(string,string,event.Handler)error{return errors.New("unexpected queue listener")}
func(*failingEventBus)Emit(context.Context,string,any)error{return errors.New("unexpected publish")}
func(*failingEventBus)Close()error{return nil}
func TestEventRegistrationStartupFailure(t *testing.T){
 if os.Getenv("LUXO_TEST_STREAM_STARTUP_FAILURE")=="1"{runFailedStreamStartup();return}
 command:=exec.Command(os.Args[0],"-test.run=^TestEventRegistrationStartupFailure$")
 command.Env=append(os.Environ(),"LUXO_TEST_STREAM_STARTUP_FAILURE=1")
 output,err:=command.CombinedOutput();var exit *exec.ExitError
 if !errors.As(err,&exit)||exit.ExitCode()!=1||!bytes.Contains(output,[]byte("fatal: register streams: stream watch: register Changed:"))||bytes.Contains(output,[]byte("unexpected ready")){t.Fatalf("startup continued or lost context: %v %s",err,output)}
}
`

func TestGeneratedStreamResolvedIdentityString(t *testing.T) {
	runGeneratedFixture(t, `event Named(value: String)
api watch(expected: String): String @auth @stream(Named) { my.role == expected && it.value == ("mem" + "ber") }
api watchReverse(expected: String): String @auth @stream(Named) { expected == my.role && ("mem" + "ber") == it.value }
`, `package generated
import("testing";"time";"strconv";"github.com/light-speak/luxo/pkg/lux/auth")
func matcherClaims(t *testing.T,role string)auth.Claims{
 t.Helper();cfg:=&auth.Config{Secret:"resolved-identity-test",Expires:time.Hour}
 token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){dst=append(dst,"{\"role\":"...);dst=strconv.AppendQuote(dst,role);return append(dst,'}'),nil})
 if err!=nil{t.Fatal(err)};claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)};return claims
}
func TestResolvedIdentityMatchers(t *testing.T){
 for _, sample:=range []struct{role,expected,value string;want bool}{
  {"member","member","member",true},
  {"blocked","member","member",false},
  {"member","blocked","member",false},
  {"member","member","blocked",false},
 }{
  if got:=matchWatch(sample.value,&WatchStreamParams{Expected:sample.expected},matcherClaims(t,sample.role));got!=sample.want{t.Fatalf("forward %+v: %v",sample,got)}
  if got:=matchWatchReverse(sample.value,&WatchReverseStreamParams{Expected:sample.expected},matcherClaims(t,sample.role));got!=sample.want{t.Fatalf("reverse %+v: %v",sample,got)}
 }
}
`, StableIDs{EventFields: map[string]map[string]int{"Named": {"value": 1}}})
}

func TestGeneratedStreamRejectsInvalidIdentityReads(t *testing.T) {
	runGeneratedFixture(t, `event Named(value: String)
api negative(): String @auth @stream(Named) { my.role != "blocked" }
api negated(): String @auth @stream(Named) { !(my.role == "blocked") }
api skipped(): String @auth @stream(Named) { true || my.role == "member" }
`, `package generated
import("testing";"time";"github.com/light-speak/luxo/pkg/lux/auth")
func TestStrictIdentityMatchers(t *testing.T){
 cfg:=&auth.Config{Secret:"stream-identity-fixture",Expires:time.Hour}
 for _,sample:=range []struct{raw string;want bool}{
  {"",false},{"{}",false},{"{\"role\":null}",false},{"{\"role\":1}",false},
  {"{\"role\":\"blocked\"}",false},{"{\"role\":\"member\"}",true},{"{\"role\":\"\"}",true},
 }{
  var identity any
  if sample.raw!=""{token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)};identity,err=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}}
  if got:=matchNegative(&NegativeStreamParams{},identity);got!=sample.want{t.Errorf("negative %s: %v",sample.raw,got)}
  if got:=matchNegated(&NegatedStreamParams{},identity);got!=sample.want{t.Errorf("negated %s: %v",sample.raw,got)}
  if !matchSkipped(&SkippedStreamParams{},identity){t.Errorf("short circuit %s",sample.raw)}
 }
}
`, StableIDs{EventFields: map[string]map[string]int{"Named": {"value": 1}}})
}

func TestGeneratedStreamAuthorizationUsesStrictClaims(t *testing.T) {
	runGeneratedFixture(t, `event Named(value: String)
api authenticated(): String @auth @stream(Named) { true }
api owned(id: Int): String @auth(Admin, own: "id") @stream(Named) { true }
`, `package generated
import("testing";"time";"encoding/json";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth")
func TestStreamAuthorization(t *testing.T){
 cfg:=&auth.Config{Secret:"strict-stream-auth",Expires:time.Hour}
 for _,sample:=range []struct{raw string;owns bool}{
  {"{}",false},{"{\"id\":null}",false},{"{\"id\":false}",false},{"{\"id\":\"0\"}",false},
  {"{\"id\":0}",true},{"{\"id\":1}",false},{"{\"role\":\"Admin\"}",true},
 }{
  token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
  for _,identity:=range []any{claims,&claims}{
   req:=&api.Request{Buf:&api.ResponseBuf{},Params:map[string]json.RawMessage{"id":[]byte("0")}}
   if err:=authorizeStreamAuthenticated(req,identity);err!=nil{t.Errorf("valid authenticated claims %s rejected: %v",sample.raw,err)}
   if err:=authorizeStreamOwned(req,identity);(err==nil)!=sample.owns{t.Errorf("own %s error=%v",sample.raw,err)}
  }
 }
 var missing *auth.Claims
 for _,identity:=range []any{nil,missing,"invalid"}{
  if err:=authorizeStreamAuthenticated(&api.Request{Buf:&api.ResponseBuf{}},identity);err==nil{t.Fatal("anonymous stream accepted")}
 }
}
`, StableIDs{EventFields: map[string]map[string]int{"Named": {"value": 1}}})
}
