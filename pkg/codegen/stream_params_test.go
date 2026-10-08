package codegen

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestStreamSnapshotOwnersSkipUnreadFields(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Types: []*ast.TypeDecl{{Name: "Input", Fields: []*ast.FieldDecl{
		{Name: "untyped"},
		{Name: "secret", Type: &ast.TypeRef{Name: "Bytes"}, Directives: []*ast.Directive{{Name: "hidden"}}},
		{Name: "private", Type: &ast.TypeRef{Name: "JSON"}, Directives: []*ast.Directive{{Name: "internal"}}},
	}}}}}}
	var source strings.Builder
	writeStreamInputOwners(&source, result, []streamInfo{{isNative: true, params: []*ast.ParamDecl{{Name: "input", Type: &ast.TypeRef{Name: "Input"}}}}}, nil)
	if got := source.String(); got != "func ownStreamInputInput(value *Input) {\n}\n\n" {
		t.Fatalf("snapshot copied fields excluded by input readers: %s", got)
	}
}

func TestGeneratedStructuredStreamSnapshot(t *testing.T) {
	child := &ast.ModelDecl{Name: "Child", Fields: []*ast.FieldDecl{testField("data", "Bytes"), testField("document", "JSON")}}
	input := &ast.ModelDecl{Name: "Input", Fields: []*ast.FieldDecl{
		testField("name", "String"), testField("data", "Bytes"), testField("document", "JSON"),
		{Name: "child", Type: &ast.TypeRef{Name: "Child", Nullable: true}},
		{Name: "children", Type: &ast.TypeRef{Name: "Child", IsList: true}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Child": {"data": 1, "document": 2}, "Input": {"name": 1, "data": 2, "document": 3, "child": 4, "children": 5}})
	var source strings.Builder
	source.WriteString(`package generated
import("bytes";"encoding/json";"fmt";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/jsonutil";"github.com/light-speak/luxo/pkg/lux/schema")
`)
	for _, model := range []*ast.ModelDecl{child, input} {
		generateModel(&source, model, nil)
		g.generateReadLuxo(&source, model, nil)
		g.generateReadColumnar(&source, model, nil)
		g.generateReadJSONInput(&source, model, nil)
	}
	var streams []streamInfo
	for _, list := range []bool{false, true} {
		for _, nullable := range []bool{false, true} {
			name := fmt.Sprintf("Shape%t%t", list, nullable)
			si := streamInfo{apiName: name, isNative: true, params: []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Input", IsList: list, Nullable: nullable}}}}
			streams = append(streams, si)
			writeStreamParameterSnapshot(&source, si, nil)
			fmt.Fprintf(&source, "func read%s(req *api.Request) ([]Input,error) {var snapshot %sStreamParams; if err:=snapshot.readRequest(req);err!=nil{return nil,err};", name, name)
			switch {
			case list:
				source.WriteString("return snapshot.Value,nil}\n")
			case nullable:
				source.WriteString("if snapshot.Value==nil{return nil,nil};return []Input{*snapshot.Value},nil}\n")
			default:
				source.WriteString("return []Input{snapshot.Value},nil}\n")
			}
		}
	}
	result := &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{child, input}}}}
	writeStreamInputOwners(&source, result, streams, nil)
	metadata := string(g.generateSchemaFile(result, "generated", nil))
	source.WriteString(metadata[strings.Index(metadata, "func RegisterSchema"):])
	runGeneratedProtocolPackage(t, source.String(), structuredStreamSnapshotAssertions)
}

const structuredStreamSnapshotAssertions = `package generated
import("bytes";"encoding/json";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/schema")
func TestStructuredOwnership(t *testing.T){
    for _,binary:=range []bool{false,true}{for i,read:=range []func(*api.Request)([]Input,error){readShapefalsefalse,readShapefalsetrue,readShapetruefalse,readShapetruetrue}{
        raw:=[]byte("{\"name\":\"東京\",\"data\":\"AQID\",\"document\":{\"n\":9223372036854775807},\"child\":{\"data\":\"AQID\",\"document\":null},\"children\":[{\"data\":\"AQID\",\"document\":true}]}")
        registry:=schema.New();RegisterSchema(registry)
        wire,err:=registry.JSONToBinary("Input",raw);if err!=nil{t.Fatal(err)}
        input:=raw;if i>=2{input=append(append([]byte{'['},raw...),']')}
        req:=&api.Request{Params:map[string]json.RawMessage{"value":input}}
        if binary{req.SetBinaryParams([]string{"value"},1);if i>=2{req.SetParamSlot(0,api.BinaryMessages{wire})}else{req.SetParamSlot(0,api.BinaryMessage(wire))}}
        values,err:=read(req);if err!=nil{t.Fatal(err)}
        clear(raw);clear(input);clear(wire);api.PutRequest(req)
        if len(values)!=1{t.Fatalf("wrong count %d",len(values))};value:=values[0]
        if value.Name!="東京"||!bytes.Equal(value.Data,[]byte{1,2,3})||string(value.Document)!="{\"n\":9223372036854775807}"||value.Child==nil||!bytes.Equal(value.Child.Data,[]byte{1,2,3})||string(value.Child.Document)!="null"||len(value.Children)!=1||!bytes.Equal(value.Children[0].Data,[]byte{1,2,3}){t.Fatalf("borrowed field: %#v",value)}
    }}
}
`

func TestGeneratedNativeStreamPreparedACK(t *testing.T) {
	file := &ast.File{Name: "stream.luxo", Enums: []*ast.EnumDecl{{Name: "State", Values: []string{"OPEN", "CLOSED"}}}, APIs: []*ast.ApiDecl{{
		Name: "watch", ReturnType: &ast.TypeRef{Name: "Int"}, Params: []*ast.ParamDecl{
			{Name: "state", Type: &ast.TypeRef{Name: "State"}},
			{Name: "limit", Type: &ast.TypeRef{Name: "Int"}, Default: &ast.Literal{Kind: token.Int, Value: "7"}},
		}, Directives: []*ast.Directive{{Name: "native"}, {Name: "stream"}, {Name: "auth", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Admin"}}}}},
	}}}
	var proxy strings.Builder
	writeGatewayRPCStreamProxy(&proxy, []moduleInfo{{nativeStreams: []string{"watch"}}})
	assertions := nativePreparedACKAssertions + proxy.String()
	if os.Getenv("LUXO_STREAM_PREPARE_BENCHMARK") == "1" {
		assertions = strings.Replace(assertions, `"bytes";`, `"bytes";"encoding/json";`, 1) + nativePreparedLifecycleBenchmark
	}
	runGeneratedProtocolPackage(t, string(generateStreamFile(&semantic.Result{Files: []*ast.File{file}}, "generated")), assertions)
}

const nativePreparedACKAssertions = `package generated
import("bytes";"context";"fmt";"net";"net/http";"net/http/httptest";"strings";"sync/atomic";"testing";"time";
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/schema";
"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/luvia";
"github.com/light-speak/luxo/pkg/lux/auth";"nhooyr.io/websocket")
type State string
func(s State)IsValid()bool{return s=="OPEN"||s=="CLOSED"}
type claims string
func(c claims)ID()int64{return 42}
func(c claims)Int(string)int64{return 42}
func(c claims)String(string)string{return string(c)}
type preparedResolver struct{calls atomic.Int32}
func(r *preparedResolver)HandleWatch(_ context.Context,p *WatchStreamParams,_ any,s *api.TypedStream[int64])error {r.calls.Add(1);return s.Send(p.Limit)}
func preparedTestRouter(role string)(*api.Router,*preparedResolver,error){
    router:=api.NewRouter()
    router.Schema.RegisterEnum(&schema.Enum{Name:"State",Values:[]string{"OPEN","CLOSED"}})
    if err:=router.Schema.RegisterAPI(&schema.API{ID:1,Name:"watch",Stream:true,ReturnType:"Int",Params:[]schema.Param{
        {ID:1,Name:"state",Type:schema.FieldEnum,TypeName:"State"},{ID:2,Name:"limit",Type:schema.FieldInt,HasDefault:true},
    }});err!=nil{return nil,nil,err}
    if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{return nil,nil,err}
    router.IdentityExtractor=func(context.Context)any{if role==""{return nil};return claims(role)}
    resolver:=&preparedResolver{};err:=RegisterStreams(router,nil,resolver)
    return router,resolver,err
}
func preparedTestDeployment(t *testing.T,forward bool)(*api.Router,*preparedResolver,*api.Router){
    t.Helper()
    backend,resolver,err:=preparedTestRouter("");if err!=nil{t.Fatal(err)}
    hooks:=luvia.New().Router
    backend.IdentityExtractor,backend.InternalRequestContext=hooks.IdentityExtractor,hooks.InternalRequestContext
    if !forward{return backend,resolver,backend}
    if err:=backend.Prepare();err!=nil{t.Fatal(err)}
    listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
    service:=rpc.NewServer(backend)
    done:=make(chan error,1);go func(){done<-service.Serve(listener)}()
    client:=rpc.NewClient(listener.Addr().String())
    t.Cleanup(func(){client.Close();service.Close();if err:=<-done;err!=nil{t.Error(err)}})
    gateway,_,err:=preparedTestRouter("");if err!=nil{t.Fatal(err)}
    gateway.IdentityExtractor=hooks.IdentityExtractor
    gateway.HandleStreamForwarded("watch",rpcStreamProxy(client,gateway,"watch"))
    return gateway,resolver,backend
}
func TestPreparedACK(t *testing.T){
    t.Setenv("JWT_SECRET","generated-stream-test-secret")
    t.Setenv("JWT_EXPIRES","1h");t.Setenv("JWT_REFRESH","false");t.Setenv("JWT_REFRESH_EXPIRES","1h")
    auth.ResetConfig();t.Cleanup(auth.ResetConfig)
    cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
    for _,forward:=range []bool{false,true}{for _,binary:=range []bool{false,true}{for _,sample:=range []struct{role,state string;limit,want,code int}{
        {"Admin","OPEN",0,7,0},{"Admin","OPEN",9,9,0},{"Admin","UNKNOWN",9,0,400},{"Member","OPEN",9,0,403},{"","OPEN",9,0,401},
    }{t.Run(fmt.Sprintf("gateway=%t/binary=%t/%s/%s/%d",forward,binary,sample.role,sample.state,sample.limit),func(t *testing.T){
        router,resolver,backend:=preparedTestDeployment(t,forward)
        if err:=router.Validate();err!=nil{t.Fatal(err)}
        server:=httptest.NewServer(luvia.AuthMiddleware(cfg,router));defer server.Close()
        ctx,cancel:=context.WithTimeout(t.Context(),3*time.Second);defer cancel()
        options:=&websocket.DialOptions{HTTPHeader:make(http.Header)}
        if sample.role!=""{token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return fmt.Appendf(dst,"{\"id\":42,\"role\":%q}",sample.role),nil});if err!=nil{t.Fatal(err)};options.HTTPHeader.Set("Authorization","Bearer "+token)}
        conn,_,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(server.URL,"http"),options);if err!=nil{t.Fatal(err)};defer conn.CloseNow()
        mode:=websocket.MessageText
        suffix:="";if sample.limit!=0{suffix=fmt.Sprintf(",\"limit\":%d",sample.limit)}
        frame:=[]byte(fmt.Sprintf("{\"$sub\":\"watch\",\"$id\":1,\"state\":%q%s}",sample.state,suffix))
        if binary{
            params:=map[string]any{"state":sample.state};if sample.limit!=0{params["limit"]=int64(sample.limit)}
            body,err:=api.EncodeBinaryRequest(1,nil,params,[]api.ParamMeta{{Name:"state",Type:"Enum",FieldID:1},{Name:"limit",Type:"Int",FieldID:2}});if err!=nil{t.Fatal(err)}
            mode=websocket.MessageBinary;frame=append([]byte{api.BinaryFrameSubscribe,1},body...)
        }
        if err:=conn.Write(ctx,mode,frame);err!=nil{t.Fatal(err)}
        _,ack,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
        if sample.code!=0 {
            if binary{if len(ack)<3||ack[0]!=api.BinaryFrameSubscribeError{t.Fatalf("accepted invalid subscription: %x",ack)};failure,err:=api.DecodeBinaryError(ack[2:],0);if err!=nil||failure.Code!=sample.code{t.Fatalf("error %v %v",failure,err)}}else if !bytes.Contains(ack,[]byte(fmt.Sprintf("\"code\":%d",sample.code))){t.Fatalf("accepted invalid subscription: %s",ack)}
            if resolver.calls.Load()!=0||router.Streams.SubCount("watch")!=0||backend.Streams.SubCount("watch")!=0{t.Fatal("rejected subscription started handler")};return
        }
        if binary{if !bytes.Equal(ack,[]byte{api.BinaryFrameSubscribeSuccess,1}){t.Fatalf("invalid ACK: %x",ack)}}else if !bytes.Contains(ack,[]byte("\"ok\":true")){t.Fatalf("invalid ACK: %s",ack)}
        _,data,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
        if binary{if !bytes.Equal(data,[]byte{api.BinaryFrameStream,1,byte(sample.want*2)}){t.Fatalf("value: %x",data)}}else if !bytes.Contains(data,[]byte(fmt.Sprintf("\"data\":%d",sample.want))){t.Fatalf("value: %s",data)}
        if resolver.calls.Load()!=1{t.Fatal("owning handler did not run exactly once")}
    })}}}
}
`

// Establish a generated lifecycle baseline; this is not an old/new comparison.
const nativePreparedLifecycleBenchmark = `
func BenchmarkStreamPreparationLifecycle(b *testing.B){
    for _,binary:=range []bool{false,true}{b.Run(fmt.Sprintf("binary=%t",binary),func(b *testing.B){
        router,_,err:=preparedTestRouter("Admin");if err!=nil{b.Fatal(err)}
        req:=&api.Request{API:"watch",Params:map[string]json.RawMessage{"state":[]byte("\"OPEN\""),"limit":[]byte("9")}}
        if binary{req.SetBinaryParams([]string{"state","limit"},2);req.SetParamSlot(0,"OPEN");req.SetParamSlot(1,int64(9))}
        if err:=router.Validate();err!=nil{b.Fatal(err)}
        b.ReportAllocs()
        for b.Loop(){
            stream,err:=router.OpenInternalStream(b.Context(),req);if err!=nil{b.Fatal(err)}
            count:=0
            for data:=range stream.Messages(){if !bytes.Equal(data,[]byte{18}){b.Fatalf("bad data: %x",data)};count++}
            stream.Close();if count!=1||stream.Err()!=nil{b.Fatalf("bad termination: %d %v",count,stream.Err())}
        }
    })}
}
`

func TestGeneratedStreamParameterSnapshot(t *testing.T) {
	declaration := &ast.ApiDecl{Name: "watch", Params: []*ast.ParamDecl{
		{Name: "state", Type: &ast.TypeRef{Name: "State"}},
		{Name: "limit", Type: &ast.TypeRef{Name: "Int"}, Default: &ast.Literal{Kind: token.Int, Value: "7"}},
		{Name: "data", Type: &ast.TypeRef{Name: "Bytes"}},
		{Name: "document", Type: &ast.TypeRef{Name: "JSON"}},
		{Name: "chunks", Type: &ast.TypeRef{Name: "Bytes", IsList: true, Nullable: true}, Default: &ast.Literal{Kind: token.Null, Value: "null"}},
		{Name: "documents", Type: &ast.TypeRef{Name: "JSON", IsList: true, Nullable: true}, Default: &ast.Literal{Kind: token.Null, Value: "null"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "Bytes", Nullable: true}, Default: &ast.Literal{Kind: token.Null, Value: "null"}},
	}}
	var source strings.Builder
	source.WriteString(`package generated
import ("bytes";"encoding/json";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
`)
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	writeStreamParameterSnapshot(&source, streamInfo{apiName: "watch", params: declaration.Params}, map[string]bool{"State": true})
	runGeneratedProtocolPackage(t, source.String(), streamParameterAssertions)
}

const streamParameterAssertions = `package generated
import("bytes";"encoding/json";"testing";"github.com/light-speak/luxo/pkg/lux/api")
func TestSnapshot(t *testing.T) {
    for _,binary:=range []bool{false,true} {
        data:=[]byte{1,2,3};document:=json.RawMessage("{\"n\":9223372036854775807}")
        req:=&api.Request{Params:map[string]json.RawMessage{"state":[]byte("\"OPEN\""),"data":[]byte("\"AQID\""),"document":document}}
        if binary {req.Params=nil;req.SetBinaryParams([]string{"state","limit","data","document","chunks","documents","optional"},7);req.SetParamSlot(0,"OPEN");req.SetParamSlot(2,data);req.SetParamSlot(3,document);req.SetParamSlot(4,[][]byte{data});req.SetParamSlot(5,[]json.RawMessage{document});req.SetParamSlot(6,data)}
        var got WatchStreamParams
        if err:=got.readRequest(req);err!=nil {t.Fatal(err)}
        clear(data);clear(document);api.PutRequest(req)
        if got.State!=StateOPEN || got.Limit!=7 || !bytes.Equal(got.Data,[]byte{1,2,3}) || string(got.Document)!="{\"n\":9223372036854775807}" {t.Fatalf("snapshot not owned: %#v",got)}
        if binary {if len(got.Chunks)!=1||!bytes.Equal(got.Chunks[0],got.Data)||len(got.Documents)!=1||!bytes.Equal(got.Documents[0],got.Document)||got.Optional==nil||!bytes.Equal(*got.Optional,got.Data){t.Fatalf("nested bytes borrowed: %#v",got)}} else if got.Chunks!=nil||got.Documents!=nil||got.Optional!=nil{t.Fatal("nullable defaults changed")}
    }
}
func TestSnapshotRejectsInvalidValues(t *testing.T) {
    for _,state:=range []string{"UNKNOWN","OPEN"} {
        req:=&api.Request{Params:map[string]json.RawMessage{"state":[]byte("\""+state+"\""),"limit":[]byte("null"),"data":[]byte("\"AQID\""),"document":[]byte("{}")}}
        var got WatchStreamParams
        if err:=got.readRequest(req);err==nil {t.Fatal("invalid input accepted")}
    }
}
`
