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
import("bytes";"context";"errors";"fmt";"net/http/httptest";"os";"os/exec";"strings";"testing";"time";
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/event";"nhooyr.io/websocket")
type eventClaims string
func(eventClaims)ID()int64{return 42}
func(eventClaims)Int(string)int64{return 42}
func(c eventClaims)String(string)string{return string(c)}
type eventResolver struct{}
func(eventResolver)MatchWatchNative(e ChangedEvent,p *WatchNativeStreamParams,_ any)bool{return e.State==p.State&&e.Value==p.Limit}
func TestEventPreparation(t *testing.T){
 for _,binary:=range []bool{false,true}{for _,name:=range []string{"watch","watchNative"}{for _,sample:=range []struct{role,state string;code int}{
  {"Admin","OPEN",0},{"Admin","UNKNOWN",400},{"Member","OPEN",403},{"","OPEN",401},
 }{t.Run(fmt.Sprintf("%s/%s/%s/binary=%t",name,sample.role,sample.state,binary),func(t *testing.T){
  router:=api.NewRouter();router.Schema.RegisterEnum(&schema.Enum{Name:"State",Values:[]string{"OPEN","CLOSED"}})
  for i,n:=range []string{"watch","watchNative"}{router.Schema.RegisterAPI(&schema.API{ID:i+1,Name:n,Stream:true,ReturnType:"Int",Params:[]schema.Param{
   {ID:1,Name:"state",Type:schema.FieldEnum,TypeName:"State"},{ID:2,Name:"limit",Type:schema.FieldInt,HasDefault:true},
  }})}
  router.Registry.RegisterSchemaAPIs(router.Schema)
  router.IdentityExtractor=func(context.Context)any{if sample.role==""{return nil};return eventClaims(sample.role)}
  bus:=event.NewChanBus(16);defer bus.Close();if err:=RegisterStreams(router,bus,eventResolver{});err!=nil{t.Fatal(err)}
  if err:=router.Validate();err!=nil{t.Fatal(err)}
  server:=httptest.NewServer(router);defer server.Close()
  ctx,cancel:=context.WithTimeout(t.Context(),3*time.Second);defer cancel()
  conn,_,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(server.URL,"http"),nil);if err!=nil{t.Fatal(err)};defer conn.CloseNow()
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
func(*failingEventBus)Close(){}
func TestEventRegistrationStartupFailure(t *testing.T){
 if os.Getenv("LUXO_TEST_STREAM_STARTUP_FAILURE")=="1"{runFailedStreamStartup();return}
 command:=exec.Command(os.Args[0],"-test.run=^TestEventRegistrationStartupFailure$")
 command.Env=append(os.Environ(),"LUXO_TEST_STREAM_STARTUP_FAILURE=1")
 output,err:=command.CombinedOutput();var exit *exec.ExitError
 if !errors.As(err,&exit)||exit.ExitCode()!=1||!bytes.Contains(output,[]byte("fatal: register streams: stream watch: register Changed:"))||bytes.Contains(output,[]byte("unexpected ready")){t.Fatalf("startup continued or lost context: %v %s",err,output)}
}
`
