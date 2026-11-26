package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedGatewayLocalizedErrorData(t *testing.T) {
	var proxy strings.Builder
	writeGatewayProxy(&proxy)
	runGeneratedFixture(t, `
error PublicRejected(value: Int, label: String, raw: JSON, amount: Decimal, optional: String?) {
 code: 422 message: "error.precision" internal: false
}
error PrivateRejected(detail: String) { code: 409 message: "error.private" internal: true }
api publicFailure(asString: Boolean): Int @native
api privateFailure(): Int @native
api success(): Int { return 7 }
`, localizedGatewayErrorAssertions+proxy.String(), StableIDs{
		APIs:      map[string]int{"publicFailure": 1, "privateFailure": 2, "success": 3},
		APIParams: map[string]map[string]int{"publicFailure": {"asString": 1}},
	})
}

const localizedGatewayErrorAssertions = `package generated
import("bytes";"context";"encoding/json";"errors";"fmt";"io";"net";"net/http";"net/http/httptest";"os";"path/filepath";"strconv";"strings";"sync";"sync/atomic";"testing";"time"
 "github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
 "github.com/light-speak/luxo/pkg/lux/i18n";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema"
 "github.com/shopspring/decimal";"nhooyr.io/websocket")
const localizedErrorObject="{\"huge\":123456789012345678901234567890,\"fraction\":0.12345678901234567890123456789,\"text\":\"json\"}"
const localizedErrorLabel="\uFEFFlabel \"quoted\" \\ path"
type localizedErrorResolver struct{ calls atomic.Int64 }
func(r *localizedErrorResolver)PublicFailure(_ context.Context,asString bool)(int64,error){r.calls.Add(1);return 0,localizedPublicError(asString)}
func(r *localizedErrorResolver)PrivateFailure(context.Context)(int64,error){r.calls.Add(1);return 0,NewPrivateRejected("PRIVATE_DATA").WithCause(errors.New("PRIVATE_CAUSE"))}
func localizedPublicError(asString bool)*luxerrors.AppError{
 raw:=localizedErrorObject;if asString{raw="\"json-string\""}
 return NewPublicRejected(9007199254740993,localizedErrorLabel,json.RawMessage(raw),decimal.New(12345,-2),nil)
}
type localizedErrorListener struct{net.Listener;accepted atomic.Int64}
func(l *localizedErrorListener)Accept()(net.Conn,error){conn,err:=l.Listener.Accept();if err==nil{l.accepted.Add(1)};return conn,err}
type localizedErrorEnvelope struct{Error string;Code int;Message string;Data json.RawMessage}

func localizedErrorRouter(t *testing.T,resolver *localizedErrorResolver)*api.Router{
 t.Helper();router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:resolver});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)};return router
}
func localizedErrorGateway(t *testing.T,backend *api.Router)(*api.Router,*rpc.Client,*localizedErrorListener){
 t.Helper();ln,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 listener:=&localizedErrorListener{Listener:ln};server:=rpc.NewServer(backend);done:=make(chan error,1)
 go func(){done<-server.Serve(listener)}();client:=rpc.NewClient(ln.Addr().String())
 t.Cleanup(func(){client.Close();server.Close();if err:=<-done;err!=nil{t.Error(err)}})
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 for _,name:=range []string{"publicFailure","privateFailure","success"}{router.Handle(name,proxyHandler(client,router.Schema,name,nil))}
 if err:=router.Prepare();err!=nil{t.Fatal(err)};return router,client,listener
}
func localizedErrorTranslator(t *testing.T)*i18n.Translator{
 t.Helper();cwd,err:=os.Getwd();if err!=nil{t.Fatal(err)};dir:=filepath.Join(cwd,"localized-error-catalog")
 if err:=os.Mkdir(dir,0700);err!=nil{t.Fatal(err)}
 for lang,prefix:=range map[string]string{"en":"Rejected","zh":"拒绝"}{
  text:="error:\n  precision: \""+prefix+" ${value}: ${label}; json=${raw}; amount=${amount}\"\n  private: \"PRIVATE_TRANSLATED\"\n"
  if err:=os.WriteFile(filepath.Join(dir,lang+".yaml"),[]byte(text),0600);err!=nil{t.Fatal(err)}
 }
 translator:=i18n.New("en");if err:=translator.LoadDir(dir);err!=nil{t.Fatal(err)};return translator
}
func localizedErrorRequest(router *api.Router,binary bool,name string,asString bool)[]byte{
 if !binary{body:="{\"$api\":\""+name+"\"";if name=="publicFailure"{body+=",\"asString\":"+strconv.FormatBool(asString)};return []byte(body+"}")}
 var encoded codec.Encoder;encoded.WriteVarint(uint64(router.Schema.APIs[name].ID));encoded.WriteVarint(0)
 if name=="publicFailure"{encoded.WriteFieldBool(1,asString)};encoded.WriteEnd();return encoded.Bytes()
}
func localizedErrorDecode(t *testing.T,binary bool,body []byte)localizedErrorEnvelope{
 t.Helper();if binary{value,err:=api.DecodeBinaryError(body,0);if err!=nil{t.Fatal(err)};return localizedErrorEnvelope{Error:value.Name,Code:value.Code,Message:value.Message,Data:value.Data}}
 var value localizedErrorEnvelope;if err:=json.Unmarshal(body,&value);err!=nil{t.Fatal(err)};return value
}
func requireLocalizedError(t *testing.T,value localizedErrorEnvelope,name,lang string,asString bool){
 t.Helper();if name=="privateFailure"{
  if value.Code!=409||value.Error!="PrivateRejected"||value.Message!="error.internal"||len(value.Data)!=0{t.Fatalf("private error leaked: %+v",value)};return
 }
 raw:=localizedErrorObject;if asString{raw="\"json-string\""};prefix:="Rejected";if lang=="zh-CN"{prefix="拒绝"}
 want:=prefix+" 9007199254740993: "+localizedErrorLabel+"; json="+raw+"; amount=123.45"
 expected,err:=localizedPublicError(asString).Data.AppendJSON(nil);if err!=nil{t.Fatal(err)}
 if value.Code!=422||value.Error!="PublicRejected"||value.Message!=want||!bytes.Equal(value.Data,expected){t.Fatalf("%s: %+v data=%s want message=%q data=%s",lang,value,value.Data,want,expected)}
}
func requireLocalizedBodyPrivacy(t *testing.T,body []byte){
 t.Helper();if bytes.Contains(body,[]byte("PRIVATE_")){t.Fatalf("private diagnostic escaped: %s",body)}
}
func localizedErrorHTTP(t *testing.T,router *api.Router){
 t.Helper();server:=httptest.NewServer(router);defer server.Close();client:=server.Client();client.Timeout=5*time.Second
 for _,lang:=range []string{"zh-CN","en-US","fr",""}{for _,binary:=range []bool{false,true}{
  for _,sample:=range []struct{name string;asString bool}{{"publicFailure",false},{"publicFailure",true},{"privateFailure",false},{"success",false}}{
   body:=localizedErrorRequest(router,binary,sample.name,sample.asString)
   request,err:=http.NewRequestWithContext(t.Context(),http.MethodPost,server.URL,bytes.NewReader(body));if err!=nil{t.Fatal(err)}
   request.Header.Set("Accept-Language",lang);request.Header.Set("Content-Type","application/json")
   if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
   response,err:=client.Do(request);if err!=nil{t.Fatal(err)};data,err:=io.ReadAll(response.Body);response.Body.Close();if err!=nil{t.Fatal(err)}
   requireLocalizedBodyPrivacy(t,data)
   if sample.name=="success"{want:=[]byte("{\"data\":7}");if binary{want=codec.AppendSvarint(nil,7)};if response.StatusCode!=200||!bytes.Equal(data,want){t.Fatalf("HTTP recovery failed: %d %x",response.StatusCode,data)};continue}
   value:=localizedErrorDecode(t,binary,data);if response.StatusCode!=value.Code{t.Fatal("HTTP status differs from error code",response.StatusCode,value.Code)}
   requireLocalizedError(t,value,sample.name,lang,sample.asString)
  }
 }}
}
func localizedErrorWS(t *testing.T,router *api.Router){
 t.Helper();server:=httptest.NewServer(router);defer server.Close()
 for _,lang:=range []string{"zh-CN","en-US","fr",""}{
  ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
  conn,_,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(server.URL,"http"),&websocket.DialOptions{HTTPHeader:http.Header{"Accept-Language":{lang}}});if err!=nil{cancel();t.Fatal(err)}
  localizedErrorWSMessages(t,ctx,conn,router,lang);conn.CloseNow();cancel()
 }
}
func localizedErrorWSMessages(t *testing.T,ctx context.Context,conn *websocket.Conn,router *api.Router,lang string){
 t.Helper();var id byte
 for _,binary:=range []bool{false,true}{for _,sample:=range []struct{name string;asString bool}{{"publicFailure",false},{"publicFailure",true},{"privateFailure",false},{"success",false}}{
  id++;payload:=localizedErrorRequest(router,binary,sample.name,sample.asString);kind:=websocket.MessageText
  frame:=append([]byte("{\"$id\":"+strconv.Itoa(int(id))+","),payload[1:]...)
  if binary{kind=websocket.MessageBinary;frame=append([]byte{api.BinaryFrameCallRequest,id},payload...)}
  if err:=conn.Write(ctx,kind,frame);err!=nil{t.Fatal(err)};got,data,err:=conn.Read(ctx);if err!=nil||got!=kind{t.Fatal(got,err)}
  requireLocalizedBodyPrivacy(t,data)
  if binary{want:=byte(api.BinaryFrameCallError);if sample.name=="success"{want=api.BinaryFrameCallSuccess};if len(data)<3||data[0]!=want||data[1]!=id{t.Fatalf("bad Binary WS reply %x",data)};data=data[2:]}
  if !binary{var envelope map[string]json.RawMessage;if err:=json.Unmarshal(data,&envelope);err!=nil{t.Fatal(err)};var actual byte;if err:=json.Unmarshal(envelope["$id"],&actual);err!=nil||actual!=id{t.Fatal("wrong WS request ID",string(data),err)}}
  if sample.name=="success"{if binary{if !bytes.Equal(data,codec.AppendSvarint(nil,7)){t.Fatal("WS Binary recovery failed",data)}}else{var value struct{Data int};if err:=json.Unmarshal(data,&value);err!=nil||value.Data!=7{t.Fatal("WS JSON recovery failed",string(data),err)}};continue}
  requireLocalizedError(t,localizedErrorDecode(t,binary,data),sample.name,lang,sample.asString)
 }}
}
func localizedErrorRPCKeys(t *testing.T,client *rpc.Client){
 t.Helper();for _,asString:=range []bool{false,true}{var params codec.Encoder;params.WriteFieldBool(1,asString);params.WriteEnd()
  _,err:=client.CallWithMaskContext(t.Context(),"",1,nil,params.Bytes());var failure *luxerrors.AppError
  if !errors.As(err,&failure)||failure.Message!="error.precision"||failure.Data==nil{t.Fatalf("LRPC translated too early: %+v",err)}
  expected,e:=localizedPublicError(asString).Data.AppendJSON(nil);if e!=nil{t.Fatal(e)};actual,e:=failure.Data.AppendJSON(nil);if e!=nil||!bytes.Equal(actual,expected){t.Fatal("LRPC data changed",string(actual),e)}
 }
 _,err:=client.CallWithMaskContext(t.Context(),"",2,nil,[]byte{0});var failure *luxerrors.AppError
 if !errors.As(err,&failure)||!failure.Internal||failure.Message!="error.internal"||failure.Data!=nil{t.Fatal("LRPC private fields escaped",err)}
}
func TestLocalizedGeneratedErrorsAcrossGateway(t *testing.T){
 resolver:=&localizedErrorResolver{};backend:=localizedErrorRouter(t,resolver);gateway,client,listener:=localizedErrorGateway(t,backend)
 translator:=localizedErrorTranslator(t);backend.SetTranslator(translator);gateway.SetTranslator(translator)
 second,secondClient,secondListener:=localizedErrorGateway(t,gateway);second.SetTranslator(translator)
 for _,route:=range []struct{name string;router *api.Router}{{"direct",backend},{"gateway",gateway},{"two-hop",second}}{
  t.Run(route.name+"/HTTP",func(t *testing.T){localizedErrorHTTP(t,route.router)})
  t.Run(route.name+"/WS",func(t *testing.T){localizedErrorWS(t,route.router)})
 }
 localizedErrorRPCKeys(t,client);localizedErrorRPCKeys(t,secondClient)
 if resolver.calls.Load()!=150{t.Fatalf("missing native error executions: %d want 150",resolver.calls.Load())}
 if listener.accepted.Load()!=1||secondListener.accepted.Load()!=1{t.Fatalf("LRPC connection was not reused: %d / %d",listener.accepted.Load(),secondListener.accepted.Load())}
}
`

func TestGeneratedErrorDataUsesRemotePrimaryKey(t *testing.T) {
	for _, keyType := range []string{"String", "UUID"} {
		t.Run(keyType, func(t *testing.T) {
			generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Remote": {"state": 1, "key": 2}}}, Events: &EventContext{
				ModelIDType: map[string]string{"Remote": keyType}, ModelIDField: map[string]string{"Remote": "key"},
				EnumModule: map[string]string{"RemoteState": "remote"},
			}})
			file := &ast.File{Extends: []*ast.ExtendDecl{{Name: "Remote", Fields: []*ast.FieldDecl{
				{Name: "state", Type: &ast.TypeRef{Name: "RemoteState"}},
			}}}, Errors: []*ast.ErrorDecl{{Name: "Rejected", Code: 422, Fields: []*ast.ParamDecl{
				{Name: "remote", Type: &ast.TypeRef{Name: "Remote"}},
			}}}}
			result, err := generator.Generate(&semantic.Result{Files: []*ast.File{file}}, "generated", nil)
			if err != nil {
				t.Fatal(err)
			}
			var model strings.Builder
			generator.generateExtendStub(&model, file.Extends[0], map[string]bool{"RemoteState": true})
			source := appendErrorTestEnum(string(result.Files["error.gen.go"])+model.String(), &ast.EnumDecl{Name: "RemoteState", Values: []string{"OPEN", "CLOSED"}})
			key, declaration := `"abc"`, ""
			if keyType == "UUID" {
				key = "uuid.Nil"
				declaration = `import "github.com/google/uuid"`
				// The generated model lives in a separate file in a real module.
				source = strings.Replace(source, "import (", "import (\n\"github.com/google/uuid\"", 1)
			}
			assertions := fmt.Sprintf(`package generated
import ("strings"; "testing")
%s
func TestRemoteKey(t *testing.T) {
 data := NewRejected(Remote{Key: %s, State: RemoteState("OPEN")}).Data
 raw, err := data.AppendJSON(nil)
 if err != nil || !strings.Contains(string(raw), "\"key\":") || strings.Contains(string(raw), "\"id\":") || !strings.Contains(string(raw), "\"state\":\"OPEN\"") { t.Fatalf("%%s: %%v", raw, err) }
 data.(*RejectedErrorData).field0.State = "FUTURE"
 if raw, err := data.AppendJSON([]byte("prefix:")); err == nil || string(raw) != "prefix:" { t.Fatalf("unknown remote enum: %%s %%v",raw,err) }
 if raw, ok := data.AppendText([]byte("prefix:"),"remote"); ok || string(raw) != "prefix:" { t.Fatalf("unknown remote enum text: %%s %%v",raw,ok) }
}`, declaration, key)
			runGeneratedProtocolPackage(t, source, assertions)
		})
	}
}

func TestGeneratedErrorModelListParameter(t *testing.T) {
	runGeneratedFixture(t, `
model Detail @noTime { id: Int @id level: Int }
model User @noTime @withAuth(stores: [id, names]) { id: Int @id names: [Int] }
error Rejected(details: [Detail]) { code: 422 message: "error.rejected" }
error OptionalRejected(details: [Detail]?) { code: 422 message: "error.optional" }
fn rejectDetails(values: [Detail]): Int { throw Rejected(details: values) }
`, `package generated
import("testing";"bytes";"context";"encoding/json";"net/http/httptest";"net";"sync/atomic";"time";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/errors")
func TestErrorModelList(t *testing.T){
 values:=[]*Detail{{Id:1,Level:42}}
 _,failure:=(&App{}).rejectDetails(t.Context(),values)
 rejected,ok:=failure.(*errors.AppError);if !ok{t.Fatalf("generated throw: %T %v",failure,failure)}
 data:=rejected.Data
 raw,err:=data.AppendJSON(nil)
 if err!=nil||!bytes.Contains(raw,[]byte("\"level\":42")){t.Fatalf("%s: %v",raw,err)}
 values[0]=nil
 if raw,err=data.AppendJSON([]byte("prefix:"));err==nil||string(raw)!="prefix:"{t.Fatalf("nil element %s: %v",raw,err)}
 raw,err=NewRejected([]*Detail{}).Data.AppendJSON(nil)
 if err!=nil||string(raw)!="{\"details\":[]}"{t.Fatalf("empty %s: %v",raw,err)}
 raw,err=NewRejected(nil).Data.AppendJSON(nil)
 if err!=nil||string(raw)!="{\"details\":[]}"{t.Fatalf("required nil %s: %v",raw,err)}
 raw,err=NewOptionalRejected(nil).Data.AppendJSON(nil)
 if err!=nil||string(raw)!="{\"details\":null}"{t.Fatalf("optional nil %s: %v",raw,err)}
 raw,err=NewOptionalRejected([]*Detail{}).Data.AppendJSON(nil)
 if err!=nil||string(raw)!="{\"details\":[]}"{t.Fatalf("optional empty %s: %v",raw,err)}
 raw,err=(&User{}).AppendClaimsJSON(nil)
 if err!=nil||string(raw)!="{\"id\":0,\"names\":[]}"{t.Fatalf("required claims list %s: %v",raw,err)}

}
func TestErrorModelListOverHTTP(t *testing.T){
 router:=api.NewRouter()
 if err:=router.Schema.RegisterAPI(&schema.API{ID:1,Name:"fail",ReturnType:"Int"});err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 var validValue atomic.Bool
 router.Handle("fail",func(ctx context.Context,_ *api.Request)error{
  values:=[]*Detail{{Id:123456789,Level:42}}
  if !validValue.Load(){values=append(values,nil)}
  _,err:=(&App{}).rejectDetails(ctx,values);return err
 })
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 service:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-service.Serve(listener)}()
 defer func(){service.Close();select{case err:=<-done:if err!=nil{t.Error(err)};case <-time.After(5*time.Second):t.Error("LRPC did not stop")}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 gateway:=api.NewRouter();if err:=gateway.Registry.Register("fail",1);err!=nil{t.Fatal(err)}
 gateway.Handle("fail",func(ctx context.Context,_ *api.Request)error{_,err:=client.CallWithMaskContext(ctx,"",1,nil,nil);return err})
 for _,endpoint:=range []*api.Router{router,gateway}{
 for _,binary:=range []bool{false,true}{
  for _,valid:=range []bool{false,true}{
   validValue.Store(valid)
   body:=[]byte("{\"$api\":\"fail\"}")
   if binary{body=[]byte{1,0,0}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body))
   req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("X-Luxo-Mode","binary");req.Header.Set("Content-Type","application/x-luxo")}
   res:=httptest.NewRecorder();endpoint.ServeHTTP(res,req)
   wantStatus:=500;if valid{wantStatus=422}
   if res.Code!=wantStatus{t.Fatalf("binary=%v valid=%v status=%d: %s",binary,valid,res.Code,res.Body)}
   var name,message string;var data json.RawMessage
   if binary{
    e,err:=api.DecodeBinaryError(res.Body.Bytes(),res.Code);if err!=nil{t.Fatal(err)}
    name,message,data=e.Name,e.Message,e.Data
   }else{
    var e struct{Error,Message string;Data json.RawMessage}
    if err:=json.Unmarshal(res.Body.Bytes(),&e);err!=nil{t.Fatal(err)}
    name,message,data=e.Error,e.Message,e.Data
   }
   if valid{if name!="Rejected"||!bytes.Contains(data,[]byte("123456789")){t.Fatalf("valid error lost: %s %s",name,data)}}else{
    if name!="Internal"||message!="error.internal"||len(data)!=0||bytes.Contains(res.Body.Bytes(),[]byte("123456789"))||bytes.Contains(res.Body.Bytes(),[]byte("model list element")){t.Fatalf("partial error escaped: %s",res.Body)}
   }
  }
 }}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Detail": {"id": 1, "level": 2}, "User": {"id": 1, "names": 2}}})
}

func TestGeneratedErrorAndClaimsBytesNullability(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime @withAuth(stores: [id, data, optional, items]) {
 id: Int @id
 data: Bytes
 optional: Bytes?
 items: [Bytes]
}
error Rejected(data: Bytes, optional: Bytes?, items: [Bytes]) { code: 422 message: "error.rejected" }
`, `package generated
import("testing";"strings")
func TestBytesNullability(t *testing.T){
 for _,value:=range [][]byte{nil,{}, {0,255}}{
  for _,present:=range []bool{false,true}{
   var optional *[]byte;if present{optional=&value}
   user:=&User{Id:42,Data:value,Optional:optional,Items:[][]byte{nil,{}}}
   want:= "\"data\":\"\",\"optional\":null,\"items\":[\"\",\"\"]"
   if len(value)!=0{want=strings.Replace(want,"\"data\":\"\"","\"data\":\"AP8=\"",1)}
   if present{part:="\"\"";if len(value)!=0{part="\"AP8=\""};want=strings.Replace(want,"\"optional\":null","\"optional\":"+part,1)}
   for _,appendJSON:=range []func([]byte)([]byte,error){user.AppendClaimsJSON,NewRejected(value,optional,user.Items).Data.AppendJSON}{
    raw,err:=appendJSON(nil);if err!=nil||!strings.Contains(string(raw),want){t.Errorf("bytes=%v present=%v output=%s want=%s: %v",value,present,raw,want,err)}
   }
  }
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "data": 2, "optional": 3, "items": 4}}})
}

func TestGeneratedStaticErrorData(t *testing.T) {
	fields := []*ast.ParamDecl{}
	for _, name := range []string{"Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON", "Details", "Status"} {
		fields = append(fields, &ast.ParamDecl{Name: "value" + name, Type: &ast.TypeRef{Name: name}})
	}
	fields = append(fields,
		&ast.ParamDecl{Name: "optional", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
		&ast.ParamDecl{Name: "values", Type: &ast.TypeRef{Name: "Details", IsList: true, Nullable: true}},
	)
	details := &ast.TypeDecl{Name: "Details", Fields: []*ast.FieldDecl{
		{Name: "value", Type: &ast.TypeRef{Name: "JSON"}},
		{Name: "next", Type: &ast.TypeRef{Name: "Details", Nullable: true}},
		{Name: "hidden", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
	}}
	status := &ast.EnumDecl{Name: "Status", Values: []string{"OPEN", "CLOSED"}}
	file := &ast.File{Types: []*ast.TypeDecl{details}, Enums: []*ast.EnumDecl{status}, Errors: []*ast.ErrorDecl{{Name: "Rejected", Code: 422, Message: "error.rejected", Fields: fields}}}
	var model strings.Builder
	generateTypeStruct(&model, details, map[string]bool{"Status": true})
	source := appendErrorTestEnum(string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated"))+model.String(), status)
	runGeneratedProtocolPackage(t, source, staticErrorDataAssertions)
}

func appendErrorTestEnum(source string, declaration *ast.EnumDecl) string {
	var enum strings.Builder
	generateEnum(&enum, declaration)
	return strings.Replace(source, "import (", "import (\n\"github.com/light-speak/luxo/pkg/lux/codec\"", 1) + enum.String()
}

func TestErrorDataUnsupportedTypeRejected(t *testing.T) {
	var output strings.Builder
	generator := newErrorDataGenerator(&semantic.Result{}, defaultGenerator())
	generator.writeScalar(&output, &ast.TypeRef{Name: "Unsupported"}, "value", false, "\t")
	if got := output.String(); got != "\tw.Reject(\"unsupported error data type: Unsupported\")\n" {
		t.Fatalf("unsupported type was not rejected: %s", got)
	}
}

func TestErrorDataFieldsUseResponsePolicy(t *testing.T) {
	file := &ast.File{
		Types: []*ast.TypeDecl{{Name: "Details", Fields: []*ast.FieldDecl{
			{Name: "code", Type: &ast.TypeRef{Name: "String"}},
			{Name: "privateNote", Type: &ast.TypeRef{Name: "String", Nullable: true}, Directives: []*ast.Directive{{Name: "visible"}}},
			{Name: "label", Type: &ast.TypeRef{Name: "String", Nullable: true}, Directives: []*ast.Directive{{Name: "mask"}}},
			{Name: "labels", Type: &ast.TypeRef{Name: "String", IsList: true}, Directives: []*ast.Directive{{Name: "mask"}}},
		}}},
		Errors: []*ast.ErrorDecl{{Name: "Rejected", Fields: []*ast.ParamDecl{{Name: "details", Type: &ast.TypeRef{Name: "Details"}}}}},
	}
	source := string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated"))
	for _, token := range []string{"value.Code", "str.Mask((*value.Label)", `"github.com/light-speak/luxo/pkg/lux/str"`} {
		if !strings.Contains(source, token) {
			t.Errorf("generated response is missing %q", token)
		}
	}
	if strings.Contains(source, "value.PrivateNote") {
		t.Error("context-free error data includes a conditionally visible field")
	}
	if t.Failed() {
		return
	}
	var model strings.Builder
	generateTypeStruct(&model, file.Types[0], nil)
	runGeneratedProtocolPackage(t, source+model.String(), `package generated
import "testing"

func TestPublicErrorData(t *testing.T) {
	label := "13812345678"
	data := NewRejected(Details{Code: "REVIEW", Label: &label, Labels: []string{label}}).Data
	const want = "{\"code\":\"REVIEW\",\"label\":\"138****5678\",\"labels\":[\"138****5678\"]}"
	if got, err := data.AppendJSON(nil); string(got) != "{\"details\":"+want+"}" || err != nil {
		t.Fatalf("structured data = %s, %v", got, err)
	}
	if got, ok := data.AppendText(nil, "details"); string(got) != want || !ok {
		t.Fatalf("localized data = %s, %v", got, ok)
	}
	data = NewRejected(Details{Code: "REVIEW"}).Data
	if got, err := data.AppendJSON(nil); string(got) != "{\"details\":{\"code\":\"REVIEW\",\"label\":null,\"labels\":[]}}" || err != nil {
		t.Fatalf("absent data = %s, %v", got, err)
	}
}
`)
}

func TestErrorDataProjectionPreservesOwnerPolicy(t *testing.T) {
	const owner = `
model ProjectedRecord @noTime {
	key: String @id
	title: String
	privateLabel: String? @visible { false }
	hiddenLabel: String @hidden
	internalLabel: String @internal
	ownerMask: String @mask("##********")
	sameMask: String @mask
	conflictMask: String @mask("###*******")
	localMask: String
}
model PlainRecord @noTime { key: String @id title: String }
model UnprojectedRecord @noTime { key: String @id title: String @mask }
`
	const consumer = `
extend ProjectedRecord {
	title: String
	privateLabel: String?
	hiddenLabel: String
	internalLabel: String
	ownerMask: String
	sameMask: String @mask
	conflictMask: String @mask("##********")
	localMask: String @mask
}
extend PlainRecord { title: String }
error ProjectionRejected(record: ProjectedRecord) { code: 422 message: "error.projection" }
error PlainRejected(record: PlainRecord) { code: 422 message: "error.plain" }
`
	files := []*ast.File{
		parseStreamEnumSource(t, "origin/owner/record.luxo", owner),
		parseStreamEnumSource(t, "origin/consumer/errors.luxo", consumer),
	}
	analyzed := semantic.AnalyzeModules(files)
	if len(analyzed.Errors) != 0 {
		t.Fatal(analyzed.Errors)
	}
	full, err := semantic.CompilationView(analyzed)
	if err != nil {
		t.Fatal(err)
	}
	ids := StableIDs{ModelFields: map[string]map[string]int{
		"ProjectedRecord": {"key": 1, "title": 2, "privateLabel": 3, "hiddenLabel": 4,
			"internalLabel": 5, "ownerMask": 6, "sameMask": 7, "conflictMask": 8, "localMask": 9},
		"PlainRecord": {"key": 1, "title": 2},
	}}
	events := BuildEventContext(full.Files, "")
	if _, exists := events.modelErrorPolicies["UnprojectedRecord"]; exists {
		t.Fatal("an unprojected model contributed to the owner policy catalog")
	}
	g := mustNewGenerator(t, GeneratorConfig{IDs: ids, Events: events})
	selected := *full
	selected.Files = nil
	for _, file := range full.Files {
		if moduleNameFromFile(file.Name) == "consumer" {
			selected.Files = append(selected.Files, file)
		}
	}
	generated, err := g.Generate(&selected, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedCompilation(t, generated, `package generated

import "testing"

func TestErrorResponsePolicy(t *testing.T) {
	label := "fixture-label"
	guarded := NewProjectionRejected(ProjectedRecord{
		Key: "sample", Title: "sale", PrivateLabel: &label,
		HiddenLabel: label, InternalLabel: label,
		OwnerMask: "ALPHA12345", SameMask: "ABCDEFGHIJK",
		ConflictMask: "ALPHA12345", LocalMask: "ABCDEFGHIJK",
	}).Data
	open := NewPlainRejected(PlainRecord{Key: "sample", Title: "sale"}).Data
	const guardedObject = "{\"key\":\"sample\",\"title\":\"sale\",\"ownerMask\":\"AL********\",\"sameMask\":\"ABC****HIJK\",\"localMask\":\"ABC****HIJK\"}"
	const openObject = "{\"key\":\"sample\",\"title\":\"sale\"}"
	for _, item := range []struct {
		data interface {
			AppendJSON([]byte) ([]byte, error)
			AppendText([]byte, string) ([]byte, bool)
		}
		want string
	}{{guarded, guardedObject}, {open, openObject}} {
		if got, err := item.data.AppendJSON(nil); err != nil || string(got) != "{\"record\":" + item.want + "}" {
			t.Fatalf("structured data %s, %v", got, err)
		}
		if got, ok := item.data.AppendText(nil, "record"); !ok || string(got) != item.want {
			t.Fatalf("localized data %s, %v", got, ok)
		}
	}
}
`)
}

func TestGeneratedErrorDataEnumBoundary(t *testing.T) {
	status := &ast.EnumDecl{Name: "Status", Values: []string{"OPEN", "CLOSED"}}
	details := &ast.TypeDecl{Name: "Details", Fields: []*ast.FieldDecl{
		{Name: "state", Type: &ast.TypeRef{Name: "Status"}},
	}}
	fields := []*ast.ParamDecl{
		{Name: "state", Type: &ast.TypeRef{Name: "Status"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "Status", Nullable: true}},
		{Name: "states", Type: &ast.TypeRef{Name: "Status", IsList: true}},
		{Name: "optionalStates", Type: &ast.TypeRef{Name: "Status", IsList: true, Nullable: true}},
		{Name: "details", Type: &ast.TypeRef{Name: "Details"}},
		{Name: "items", Type: &ast.TypeRef{Name: "Details", IsList: true}},
	}
	file := &ast.File{Enums: []*ast.EnumDecl{status}, Types: []*ast.TypeDecl{details}, Errors: []*ast.ErrorDecl{{Name: "Rejected", Code: 422, Fields: fields}}}
	var model strings.Builder
	generateTypeStruct(&model, details, map[string]bool{"Status": true})
	source := appendErrorTestEnum(string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated"))+model.String(), status)
	runGeneratedProtocolPackage(t, source, errorDataEnumAssertions)
}

const errorDataEnumAssertions = `package generated
import ("bytes"; "context"; "encoding/json"; "net/http"; "net/http/httptest"; "strings"; "testing"; "github.com/light-speak/luxo/pkg/lux/api"; "github.com/light-speak/luxo/pkg/lux/errors")

func enumErrorFixture() *RejectedErrorData {
 open := StatusOPEN
 return NewRejected(open, &open, []Status{open, StatusCLOSED}, []Status{}, Details{State:open}, []Details{{State:open}}).Data.(*RejectedErrorData)
}
func TestUnknownErrorEnumRejected(t *testing.T) {
 fields := []string{"state", "optional", "states", "optionalStates", "details", "items"}
 for _, value := range []Status{"FUTURE", "", "open", Status(string([]byte{255}))} {
  for index, field := range fields {
   data := enumErrorFixture()
   switch index {
   case 0: data.field0 = value
   case 1: data.field1 = &value
   case 2: data.field2 = append(data.field2, value)
   case 3: data.field3 = []Status{StatusOPEN, value}
   case 4: data.field4.State = value
   case 5: data.field5 = append(data.field5, Details{State:value})
   }
   prefix := []byte("prefix:")
   if raw, err := data.AppendJSON(prefix); err == nil || !strings.Contains(err.Error(), "Status") || string(raw) != "prefix:" {
    t.Errorf("field=%s value=%q JSON=%q err=%v", field, value, raw, err)
   }
   if raw, found := data.AppendText(prefix, field); found || string(raw) != "prefix:" {
    t.Errorf("field=%s value=%q text=%q found=%v", field, value, raw, found)
   }
  }
 }
}
func TestDeclaredErrorEnumsAndNull(t *testing.T) {
 data := enumErrorFixture()
 want := "{\"state\":\"OPEN\",\"optional\":\"OPEN\",\"states\":[\"OPEN\",\"CLOSED\"],\"optionalStates\":[],\"details\":{\"state\":\"OPEN\"},\"items\":[{\"state\":\"OPEN\"}]}"
 if raw, err := data.AppendJSON(nil); err != nil || string(raw) != want { t.Fatalf("%s: %v",raw,err) }
 if raw, found := data.AppendText(nil, "state"); !found || string(raw) != "OPEN" { t.Fatalf("%q %v",raw,found) }
 data.field1, data.field3 = nil, nil
 if raw, err := data.AppendJSON(nil); err != nil || !strings.Contains(string(raw), "\"optional\":null") || !strings.Contains(string(raw), "\"optionalStates\":null") { t.Fatalf("%s: %v",raw,err) }
}
func TestInvalidErrorEnumOverHTTP(t *testing.T) {
 for _, binary := range []bool{false,true} {
  for _, valid := range []bool{false,true} {
   router := api.NewRouter()
   router.Registry.Register("fail",1)
   router.Handle("fail",func(context.Context,*api.Request)error {
    data := enumErrorFixture()
    if !valid { data.field0 = "FUTURE" }
    return errors.New("Rejected",422,"error.rejected").WithData(data)
   })
   body := []byte("{\"$api\":\"fail\"}")
   if binary { body = []byte{1,0,0} }
   req := httptest.NewRequest(http.MethodPost,"/luxo",bytes.NewReader(body))
   if binary { req.Header.Set("X-Luxo-Mode","binary") }
   res := httptest.NewRecorder()
   router.ServeHTTP(res,req)
   wantCode,wantName := 500,"Internal"
   if valid { wantCode,wantName = 422,"Rejected" }
   if res.Code != wantCode { t.Fatalf("binary=%v valid=%v status=%d body=%s",binary,valid,res.Code,res.Body.Bytes()) }
   var name string
   var data json.RawMessage
   if binary {
    envelope,err := api.DecodeBinaryError(res.Body.Bytes(),res.Code)
    if err != nil { t.Fatal(err) }
    name,data = envelope.Name,envelope.Data
   } else {
    var envelope struct { Error string; Data json.RawMessage }
    if err:=json.Unmarshal(res.Body.Bytes(),&envelope); err!=nil { t.Fatal(err) }
    name,data = envelope.Error,envelope.Data
   }
   if name != wantName || (len(data)>0) != valid { t.Fatalf("unsafe error: name=%s data=%s",name,data) }
  }
 }
}
`

func TestGeneratedErrorDataSoftModelFields(t *testing.T) {
	model := &ast.ModelDecl{Name: "Record", Directives: []*ast.Directive{{Name: "soft"}}, Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
	}}
	for _, explicit := range []bool{false, true} {
		if explicit {
			model.Fields = append(model.Fields, softDeleteField())
		}
		file := &ast.File{Models: []*ast.ModelDecl{model}, Extends: []*ast.ExtendDecl{{Name: "Record"}}, Errors: []*ast.ErrorDecl{{
			Name: "Rejected", Code: 422, Fields: []*ast.ParamDecl{{Name: "record", Type: &ast.TypeRef{Name: "Record"}}},
		}}}
		var declaration strings.Builder
		generateModel(&declaration, model, nil)
		source := string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated")) + declaration.String()
		// The generated model's DateTime import normally belongs to model.gen.go.
		source = strings.Replace(source, "import (", "import (\n\"time\"", 1)
		runGeneratedProtocolPackage(t, source, `package generated
import ("testing"; "time")
func TestModelData(t *testing.T) {
 value := time.Date(2026,9,15,0,0,0,0,time.UTC)
 for _, deleted := range []*time.Time{nil, &value} {
  data := NewRejected(Record{Id:42,Secret:"private",DeletedAt:deleted}).Data
  got, err := data.AppendJSON(nil)
  want := "{\"record\":{\"id\":42,\"deletedAt\":null}}"
  if deleted != nil { want = "{\"record\":{\"id\":42,\"deletedAt\":\"2026-09-15T00:00:00Z\"}}" }
  if err != nil || string(got) != want { t.Fatalf("%s: %v", got, err) }
 }
}`)
	}
}

const staticErrorDataAssertions = `package generated
import (
 "encoding/json"
 "math"
 "testing"
 "time"
 "github.com/google/uuid"
 "github.com/shopspring/decimal"
)

func errorFixture() *RejectedErrorData {
 return NewRejected(math.MaxInt64, math.Copysign(0,-1), "café\n", true,
  time.Date(2026,9,15,1,2,3,0,time.UTC), time.Duration(math.MinInt64), uuid.Nil,
  decimal.RequireFromString("12345678901234567890.123456789"), []byte{0,255},
  json.RawMessage("{\"n\":123456789012345678901234567890,\"f\":1e-400}"),
  Details{Value:json.RawMessage("-0.000"),Hidden:"secret"}, Status("OPEN"), nil, []Details{}).Data.(*RejectedErrorData)
}
func TestTypedErrorValues(t *testing.T) {
 data := errorFixture()
 raw, err := data.AppendJSON(nil)
 if err != nil { t.Fatal(err) }
 want := "{\"valueInt\":9223372036854775807,\"valueFloat\":-0,\"valueString\":\"café\\n\",\"valueBoolean\":true,\"valueDateTime\":\"2026-09-15T01:02:03Z\",\"valueDuration\":-9223372036854775808,\"valueUUID\":\"00000000-0000-0000-0000-000000000000\",\"valueDecimal\":\"12345678901234567890.123456789\",\"valueBytes\":\"AP8=\",\"valueJSON\":{\"n\":123456789012345678901234567890,\"f\":1e-400},\"valueDetails\":{\"value\":-0.000,\"next\":null},\"valueStatus\":\"OPEN\",\"optional\":null,\"values\":[]}"
 if string(raw) != want { t.Fatalf("%s\nwant %s",raw,want) }
 for _, test := range []struct{key,want string}{
  {"valueInt","9223372036854775807"},{"valueString","café\n"},{"valueStatus","OPEN"},
  {"valueDuration","-9223372036854775808"},{"optional","null"},{"values","[]"},
 } {
  got,found := data.AppendText([]byte("prefix:"),test.key)
  if !found || string(got) != "prefix:"+test.want { t.Fatalf("text %s = %s",test.key,got) }
 }
 if got,found := data.AppendText([]byte("prefix:"),"missing"); found || string(got) != "prefix:" { t.Fatal("unknown field") }
}
func TestErrorEncodingFailsClosed(t *testing.T) {
 data := errorFixture()
 data.field1 = math.NaN()
 if raw,err := data.AppendJSON([]byte("prefix:")); err == nil || string(raw) != "prefix:" { t.Fatal("non-finite output") }
 if raw,ok := data.AppendText([]byte("prefix:"),"valueFloat"); ok || string(raw) != "prefix:" { t.Fatal("non-finite text") }
 data = errorFixture()
 data.field10.Next = &data.field10
 if _,err := data.AppendJSON(nil); err == nil { t.Fatal("cyclic data accepted") }
}
func TestGenericJSONIsNotAnEnum(t *testing.T) {
 data := errorFixture()
 data.field9 = json.RawMessage("{\"state\":\"FUTURE\"}")
 raw, err := data.AppendJSON(nil)
 if err != nil { t.Fatal(err) }
 var decoded struct { ValueJSON json.RawMessage }
 if err := json.Unmarshal(raw,&decoded); err != nil || string(decoded.ValueJSON) != string(data.field9) { t.Fatalf("generic JSON changed: %s %v",raw,err) }
}
func TestNilErrorData(t *testing.T) {
 var data *RejectedErrorData
 raw, err := data.AppendJSON([]byte("prefix:"))
 if err != nil || string(raw) != "prefix:null" { t.Fatalf("%s: %v",raw,err) }
 if raw, found := data.AppendText([]byte("prefix:"),"valueInt"); found || string(raw) != "prefix:" { t.Fatal("nil data text") }
}
`

func TestGeneratedBuiltinErrors(t *testing.T) {
	var source strings.Builder
	for _, name := range []string{"NotFound", "Unauthorized", "Forbidden", "BadRequest", "Conflict", "InternalError", "ValidationFailed", "RateLimited", "ServiceTimeout", "notFound", "not_found"} {
		fmt.Fprintf(&source, "api fail%s(): Int { throw error.%s }\n", name, name)
	}
	runGeneratedFixture(t, source.String(), `package generated
import("bytes";"encoding/json";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api"
"github.com/light-speak/luxo/pkg/lux/errors")

func TestBuiltinErrors(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 for name,want:=range map[string]*errors.AppError{
 "NotFound":errors.NotFound,"Unauthorized":errors.Unauthorized,"Forbidden":errors.Forbidden,
 "BadRequest":errors.BadRequest,"Conflict":errors.Conflict,"InternalError":errors.InternalError,
 "ValidationFailed":errors.ValidationFailed,"RateLimited":errors.RateLimited,"ServiceTimeout":errors.ServiceTimeout,
 "notFound":errors.NotFound,"not_found":errors.NotFound,
 } {
  body,err:=json.Marshal(map[string]string{"$api":"fail"+name});if err!=nil{t.Fatal(err)}
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=want.Code{t.Fatal(name,response.Code,response.Body.String())}
 }
}
`, StableIDs{})
}

func TestCompileBuiltinErrorRejectsUnvalidatedName(t *testing.T) {
	defer func() {
		failure, ok := recover().(*InternalCompilerError)
		if !ok || !strings.Contains(failure.Detail, "Missing") {
			t.Fatal("missing internal generation diagnostic", failure)
		}
	}()
	compileBuiltinError("Missing")
}
