package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenerateRejectsSemanticErrors(t *testing.T) {
	input := &semantic.Result{Errors: []semantic.Error{{Message: "invalid where predicate"}}}
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
	if err == nil || generated != nil || !strings.Contains(err.Error(), "invalid where predicate") {
		t.Fatalf("semantic errors were ignored by formal generation: %v", err)
	}
}

func TestGenerateRejectsAbstractSealedPayloads(t *testing.T) {
	for _, reference := range []string{"Label", "Label?", "[Label]", "[Label]?", "(Int, String)"} {
		t.Run(reference, func(t *testing.T) {
			input := analyzePermissionFixture(t, "interface Label { value: String }\nsealed Outcome {\n Value(value: "+reference+")\n Empty\n}")
			generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
				"Outcome": {"Value": 1, "Empty": 2}, "Outcome.Value": {"value": 1},
			}}})
			if generated != nil || err == nil || !strings.Contains(err.Error(), "sealed payload field 'value' requires a concrete data representation") {
				t.Fatalf("formal generation must reject abstract payloads without partial output: %v", err)
			}
			if len(input.Errors) != 1 || input.Errors[0].Pos.Line != 3 {
				t.Fatalf("formal generation lost the source diagnostic: %v", input.Errors)
			}
		})
	}
}

func TestGenerateRejectsDuplicateUpdateManySetters(t *testing.T) {
	for name, call := range map[string]string{
		"model":    `Item.updateMany(name: "first", name: "second")`,
		"chain":    `Item.where(id > 0).updateMany(name: "first", name: "second")`,
		"alias":    `query.updateMany(name: "first", name: "second")`,
		"function": `updateMany(Item, name: "first", name: "second")`,
	} {
		t.Run(name, func(t *testing.T) {
			input := analyzePermissionFixture(t, `model Item @noTime { id: Int @id name: String }
fn change(): Int { val query = Item.where(id > 0) return `+call+` }`)
			generated, err := Generate(input, "generated", GeneratorConfig{})
			if generated != nil || err == nil || !strings.Contains(err.Error(), "duplicate argument 'name'") {
				t.Fatalf("formal generation must reject duplicate setters without partial output: %v", err)
			}
		})
	}
}

func TestFormalGeneratorRejectsSameNameCallErrors(t *testing.T) {
	for _, sample := range []struct{ call, message string }{
		{"shared(wrong: 41)?", "unknown argument 'wrong'"},
		{"shared(value: 41)?", "unknown argument 'value'"},
		{"shared()?", "missing required argument 'input'"},
		{`shared("wrong")?`, "argument 'input' expects 'Int'"},
	} {
		input := analyzePermissionFixture(t, `fn shared(input: Int): Result<Int> @native
api shared(value: Int): Int @native api invoke(): Int { return `+sample.call+` }`)
		config := GeneratorConfig{IDs: completeFixtureAPIIDs(input, StableIDs{})}
		generated, err := Generate(input, "generated", config)
		if generated != nil || err == nil || !strings.Contains(err.Error(), sample.message) {
			t.Fatalf("formal generation did not preserve the call diagnostic: %v", err)
		}
	}
}

func TestGeneratedSameNamedCallableContracts(t *testing.T) {
	for _, sample := range []struct{ declaration, unwrap, app, public, shared string }{
		{`fn shared(input: Int = 7): Result<String> @native`, "?", "&App{Resolver:callableResolver{}}", `api shared(value: Int = 99): String @native`, "99"},
		{`fn shared(input: Int = 7): String { return "${input}" }`, "", "&App{Resolver:callableResolver{}}", `api shared(value: Int = 99): String @native`, "99"},
		{`fn shared(input: Int = 7): String { return "${input}" }`, "", "&App{}", `api shared(): Int { return 42 }`, "42"},
		{`fn shared(input: Int = 7): Result<String> @native`, "?", "&App{Resolver:callableResolver{}}", `api shared(): Int { return 42 }`, "42"},
	} {
		t.Run(sample.declaration+sample.public, func(t *testing.T) {
			source := sample.declaration + " " + sample.public + " api invoke(): String { return shared()" + sample.unwrap + " } api named(): String { return shared(input: 41)" + sample.unwrap + " }"
			assertions := strings.NewReplacer("APP_SETUP", sample.app, "SHARED_RESULT", sample.shared).Replace(sameNameCallableAssertions)
			runGeneratedFixture(t, source, assertions, StableIDs{APIs: map[string]int{"shared": 1, "invoke": 2, "named": 3}, APIParams: map[string]map[string]int{"shared": {"value": 1}}})
		})
	}
}

const sameNameCallableAssertions = `package generated
import("bytes";"context";"encoding/json";"net/http/httptest";"strconv";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type callableResolver struct{}
func(callableResolver)Shared(_ context.Context,input int64)(string,error){return strconv.FormatInt(input,10),nil}
func TestSeparateCallableContracts(t *testing.T){
 app:=APP_SETUP;router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{name,want string}{{"shared","SHARED_RESULT"},{"invoke","7"},{"named","41"}}{ for _,binary:=range []bool{false,true}{
  definition:=router.Schema.APIs[sample.name]
  body,err:=json.Marshal(map[string]string{"$api":sample.name});if err!=nil{t.Fatal(err)}
  if binary{body,err=api.EncodeBinaryRequest(definition.ID,nil,nil,router.Registry.ParamOrder(sample.name));if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200{t.Fatal(sample.name,binary,response.Code,response.Body.String())}
  want:=[]byte(strconv.Quote(sample.want));var wire []byte
  if definition.ReturnType=="Int"{want=[]byte(sample.want);value,err:=strconv.ParseInt(sample.want,10,64);if err!=nil{t.Fatal(err)};wire=codec.AppendSvarint(nil,value)}else{wire,err=codec.AppendString(nil,sample.want);if err!=nil{t.Fatal(err)}}
  if binary{if !bytes.Equal(response.Body.Bytes(),wire){t.Fatal(sample.name,response.Body.Bytes(),wire)}}else{
   var result struct{Data json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&result);err!=nil||!bytes.Equal(result.Data,want){t.Fatal(sample.name,string(result.Data),string(want),err)}
  }
 }}
}
`

func TestGeneratedFunctionAppMemberIsolation(t *testing.T) {
	for _, source := range []string{`
fn Close(value: Int): Int { return value + 1 }
api read(value: Int): Int { return Close(value) }
`, `
model Entry @noTime { id: Int @id }
fn Close(value: Int): Int { return value + 1 }
fn DB(value: Int): Int { return value }
fn Queue(value: Int): Int { return value }
fn Tx(value: Int): Int { return value }
fn range(value: Int): Int { return value }
fn SetLoaders(value: Int): Int { return value }
fn loaders(value: Int): Int { return value }
fn Resolver(value: Int): Int { return value }
fn _luxoFn0(value: Int): Int { return value }
fn EventBus(value: Int): Int { return value }
fn nativeFn(value: Int): Result<Int> @native
fn unusedLoad(id: Int): Entry? { return Entry.load(id) }
api read(value: Int): Int {
 val initial = Close(value)
 val database = DB(initial)
 val queued = Queue(database)
 return EventBus(_luxoFn0(Resolver(range(SetLoaders(loaders(Tx(queued)))))))
}
`} {
		runGeneratedFixture(t, source, functionAppMemberAssertions, StableIDs{
			APIs: map[string]int{"read": 1}, APIParams: map[string]map[string]int{"read": {"value": 1}},
			ModelFields: map[string]map[string]int{"Entry": {"id": 1}},
		})
	}
}

func TestFunctionBindingsPreserveSourceAndRepeatedGeneration(t *testing.T) {
	var first []byte
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"read": 1}}})
	for _, name := range []string{"Close", "answer", "Close"} {
		input := analyzePermissionFixture(t, "fn "+name+"(): Int { return 42 } api read(): Int { return "+name+"() }")
		generated, err := g.Generate(input, "generated", nil)
		if err != nil {
			t.Fatal(err)
		}
		if input.Files[0].Functions[0].Name != name {
			t.Fatal("generated binding mutated the source function")
		}
		body := generated.Files["handler.gen.go"]
		if name == "answer" {
			if !strings.Contains(string(body), "func (app *App) answer(") || strings.Contains(string(body), "_luxoFn") {
				t.Fatal("ordinary function acquired a stale binding")
			}
		} else if first == nil {
			first = body
		} else if string(body) != string(first) {
			t.Fatal("repeated generation changed identical function bindings")
		}
	}
}

func TestLocalFunctionFragmentWithoutGenerator(t *testing.T) {
	var body strings.Builder
	c := &compiler{b: &body, api: &ast.ApiDecl{}, inFunction: true}
	if value := c.compileLocalFunctionCall(&ast.CallExpr{}, &ast.FnDecl{Name: "answer"}); value != "" || !strings.Contains(body.String(), "app.answer(ctx)") {
		t.Fatal("source fragment lost its unplanned direct function call", body.String())
	}
}

const functionAppMemberAssertions = `package generated
import("testing";"bytes";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestFunctionMember(t *testing.T){
 app:=&App{};router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"read\",\"value\":41}")
  if binary {var enc codec.Encoder;enc.WriteVarint(1);enc.WriteVarint(0);enc.WriteFieldInt(1,41);enc.WriteEnd();body=enc.Bytes()}
  req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
  if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,req)
  if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  if binary{if !bytes.Equal(response.Body.Bytes(),codec.AppendSvarint(nil,42)){t.Fatal(response.Body.Bytes())}}else if response.Body.String()!="{\"data\":42}"{t.Fatal(response.Body.String())}
 }
 var closeApp func()=app.Close;_=closeApp
}
`

func TestParameterBindingPreservesWireKeyAndDefault(t *testing.T) {
	for _, typ := range []string{"Int", "String", "State", "Snapshot"} {
		for _, list := range []bool{false, true} {
			for _, nullable := range []bool{false, true} {
				param := &ast.ParamDecl{Name: "req", Type: &ast.TypeRef{Name: typ, IsList: list, Nullable: nullable}}
				var output strings.Builder
				writeCallableParamBinding(&output, param, "localValue", map[string]bool{"State": true}, "")
				code := output.String()
				if !strings.Contains(code, `"req"`) || strings.Contains(code, `"localValue"`) || !strings.Contains(code, "localValue") {
					t.Fatalf("binding changed wire key: %s list=%t nullable=%t\n%s", typ, list, nullable, code)
				}
				if param.Name != "req" {
					t.Fatal("binding mutated the semantic AST")
				}
			}
		}
	}
	param := &ast.ParamDecl{Name: "err", Type: &ast.TypeRef{Name: "String"}, Default: &ast.Literal{Kind: token.String, Value: "localValue"}}
	var output strings.Builder
	writeCallableParamBinding(&output, param, "localValue", nil, "")
	code := output.String()
	if !strings.Contains(code, `= "localValue"`) || !strings.Contains(code, `HasParam("err")`) || !strings.Contains(code, `ParamString("err")`) {
		t.Fatalf("binding altered a default literal or parameter key: %s", code)
	}
}

func TestParameterBindingsAvoidExistingIdentifiers(t *testing.T) {
	params := []*ast.ParamDecl{{Name: "ctx"}, {Name: "_luxoParam0"}}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "_luxoParam1", Names: []string{"req", "_luxoParam2"}},
		&ast.ForStmt{VarName: "_luxoParam3"},
		&ast.ExprStmt{Expr: &ast.LambdaExpr{Params: []string{"err", "_luxoParam4"}}},
		&ast.ExprStmt{Expr: &ast.Ident{Name: "_luxoParam5"}},
	}}
	bindings := parameterBindings(params, body)
	used := map[string]bool{}
	for _, source := range []string{"ctx", "req", "err", "_luxoParam0", "_luxoParam1", "_luxoParam2", "_luxoParam3", "_luxoParam4"} {
		name := bindings.name(source)
		if name == source || used[name] {
			t.Fatalf("binding %q reused %q", source, name)
		}
		for index := 0; index <= 5; index++ {
			if name == fmt.Sprintf("_luxoParam%d", index) {
				t.Fatalf("binding %q shadows source identifier %q", source, name)
			}
		}
		used[name] = true
	}
	if bindings.name("unchanged") != "unchanged" || parameterBindings([]*ast.ParamDecl{{Name: "value"}}, nil) != nil {
		t.Fatal("ordinary names acquired unnecessary bindings")
	}
	if params[0].Name != "ctx" || body.Stmts[0].(*ast.ValStmt).Name != "_luxoParam1" {
		t.Fatal("name planning mutated the source AST")
	}
}

func TestGeneratedParametersDoNotCollideWithRuntimeLocals(t *testing.T) {
	runGeneratedFixture(t, `
api echoError(err: Int): Int { return err }
api echoContext(ctx: Int): Int { return ctx }
api echoRequest(req: Int): Int { return req }
api echoCodec(codec: Int): Int { return codec }
`, `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api")

func TestParameterNames(t *testing.T) {
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 for _,body:=range []string{
  "{\"$api\":\"echoError\",\"err\":42}",
  "{\"$api\":\"echoContext\",\"ctx\":42}",
  "{\"$api\":\"echoRequest\",\"req\":42}",
  "{\"$api\":\"echoCodec\",\"codec\":42}",
 } {
  request:=httptest.NewRequest("POST","/",bytes.NewBufferString(body))
  request.Header.Set("Content-Type","application/json")
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200 || !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":42")){t.Fatal(response.Code,response.Body.String())}
 }
}
`, StableIDs{})
}

func TestGeneratedFunctionContextParameterDoesNotCollide(t *testing.T) {
	runGeneratedFixture(t, `
fn echoContext(ctx: Int): Int { val _luxoParam0 = 1 return ctx + _luxoParam0 - 1 }
fn nullableContext(ctx: Int?): Int { return ctx ?: 42 }
`, `package generated
import "testing"

func TestContextParameter(t *testing.T) {
 value,err:=(&App{}).echoContext(t.Context(),42)
 if err!=nil || value!=42 {t.Fatal(value,err)}
 for _,input:=range []*int64{nil,new(int64(7))} {
  value,err:=(&App{}).nullableContext(t.Context(),input)
  want:=int64(42);if input!=nil{want=*input}
  if err!=nil || value!=want {t.Fatal(value,err)}
 }
}
`, StableIDs{})
}

func TestGeneratedBindingScopesPreserveValues(t *testing.T) {
	runGeneratedFixture(t, `
fn loopShadow(ctx: Int): Int {
 var total = ctx
 for ctx in [1, 2] { total += ctx }
 return total + ctx
}
fn localShadow(req: Int): Int {
 var total = req
 if true { val req = 7 total += req }
 return total + req
}
fn lambdaShadow(err: Int): Int {
 val values = [1, 2].map { err -> err + 1 }
 var total = err
 for value in values { total += value }
 return total
}
fn forValueShadow(ctx: Int): Int {
 val values = for ctx in [1, 2] { ctx + 1 }
 var total = ctx
 for value in values { total += value }
 return total
}
`, `package generated
import "testing"

func TestScopes(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{name string;call func()(int64,error);want int64}{
  {"loop",func()(int64,error){return app.loopShadow(t.Context(),10)},23},
  {"local",func()(int64,error){return app.localShadow(t.Context(),10)},27},
  {"lambda",func()(int64,error){return app.lambdaShadow(t.Context(),10)},15},
  {"forValue",func()(int64,error){return app.forValueShadow(t.Context(),10)},15},
 } {t.Run(sample.name,func(t *testing.T){value,err:=sample.call();if err!=nil||value!=sample.want{t.Fatal(value,err)}})}
}
`, StableIDs{})
}

func TestGeneratedLocalRuntimeNames(t *testing.T) {
	runGeneratedFixture(t, `
fn localNames(): Int { val ctx = 10 val err = 7 return ctx + err }
fn loopNames(): Int { var total = 0 for ctx in [1, 2] { total += ctx } return total }
fn lambdaNames(): Int { val values = [1, 2].map { err -> err + 1 } var total = 0 for req in values { total += req } return total }
`, `package generated
import "testing"

func TestLocalNames(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{call func()(int64,error);want int64}{
  {func()(int64,error){return app.localNames(t.Context())},17},
  {func()(int64,error){return app.loopNames(t.Context())},3},
  {func()(int64,error){return app.lambdaNames(t.Context())},5},
 }{value,err:=sample.call();if err!=nil||value!=sample.want{t.Fatal(value,err)}}
}
`, StableIDs{})
}

func TestGeneratedChannelAndAwaitBindingNames(t *testing.T) {
	runGeneratedFixture(t, `
fn channelNames(): Int {
 val original: Channel<Int> = Channel(1)
 val ctx: Channel<Int> = original
 ctx <- 42
 val err = <-ctx
 ctx.close()
 return err
}
fn selectNames(): Int {
 val ch: Channel<Int> = Channel(1)
 ch <- 41
 return when { <-ch -> { ctx -> ctx + 1 } else -> 0 }
}
fn awaitNames(): Int {
 val (ctx, err) = await { 40 2 }
 return ctx + err
}
fn awaitCapturedNames(g: Int, gctx: Int): Int {
 val (first, second) = await { g + 1 gctx + 1 }
 return first + second
}
`, `package generated
import "testing"

func TestBindingNames(t *testing.T){
 app:=&App{}
 for _,call:=range []func()(int64,error){
  func()(int64,error){return app.channelNames(t.Context())},
  func()(int64,error){return app.awaitNames(t.Context())},
  func()(int64,error){return app.selectNames(t.Context())},
  func()(int64,error){return app.awaitCapturedNames(t.Context(),20,20)},
 }{value,err:=call();if err!=nil||value!=42{t.Fatal(value,err)}}
}
`, StableIDs{})
}

func TestGeneratedNativeParameterNamesDoNotCollide(t *testing.T) {
	runGeneratedFixture(t, `api nativeEcho(ctx: Int, err: Int, req: Int, result: Int, app: Int): Int @native`, `package generated
import("bytes";"context";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api")
type nativeEchoResolver struct{}
func(nativeEchoResolver)NativeEcho(_ context.Context,a,b,c,d,e int64)(int64,error){return a+b+c+d+e,nil}
func TestNativeNames(t *testing.T){
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:nativeEchoResolver{}});err!=nil{t.Fatal(err)}
 request:=httptest.NewRequest("POST","/",bytes.NewBufferString("{\"$api\":\"nativeEcho\",\"ctx\":1,\"err\":2,\"req\":3,\"result\":4,\"app\":5}"))
 request.Header.Set("Content-Type","application/json")
 response:=httptest.NewRecorder();router.ServeHTTP(response,request)
 if response.Code!=200 || !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":15")){t.Fatal(response.Code,response.Body.String())}
}
`, StableIDs{})
}

func TestGeneratedNativeServiceBindingNamesAcrossRPC(t *testing.T) {
	runGeneratedFixture(t, `fn nativeSum(ctx: Int, req: Int, err: Int): Int @service @native`, `package generated
import("bytes";"context";"net";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc")
type serviceResolver struct{}
func(serviceResolver)NativeSum(_ context.Context,a,b,c int64)(int64,error){return a+b+c,nil}
func TestNativeServiceNames(t *testing.T){
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterServiceFns(router,&App{Resolver:serviceResolver{}});err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 var params codec.Encoder
 params.WriteFieldInt(1,10);params.WriteFieldInt(2,12);params.WriteFieldInt(3,20);params.WriteEnd()
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
 data,err:=client.CallWithMaskContext(ctx,"",1,nil,params.Bytes())
 if err!=nil||!bytes.Equal(data,codec.AppendSvarint(nil,42)){t.Fatal(data,err)}
}
`, StableIDs{APIs: map[string]int{"svc:nativeSum": 1}, APIParams: map[string]map[string]int{"svc:nativeSum": {"ctx": 1, "req": 2, "err": 3}}})
}

func TestGeneratedServiceVoidAndZeroResponses(t *testing.T) {
	runGeneratedFixture(t, `
fn noValue() @service {}
fn zero(): Int @service { return 0 }
fn missing(): Int? @service { return null }
`, `package generated
import("bytes";"net";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/rpc")

func TestServiceValuePresence(t *testing.T){
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterServiceFns(router,&App{});err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{name string;want []byte}{
  {"svc:noValue",nil},{"svc:zero",[]byte{0}},{"svc:missing",[]byte{0}},
 }{
  definition:=router.Schema.APIs[sample.name]
  if !definition.Internal{t.Fatal("service function became public")}
  if (definition.ReturnType=="")!=(sample.name=="svc:noValue"){t.Fatal("return metadata lost",sample.name)}
  data,err:=client.CallWithMaskContext(t.Context(),"",definition.ID,nil,nil)
  if err!=nil||!bytes.Equal(data,sample.want){t.Fatalf("%s: got %x/%v, want %x",sample.name,data,err,sample.want)}
 }
}
`, StableIDs{APIs: map[string]int{"svc:noValue": 1, "svc:zero": 2, "svc:missing": 3}})
}

func TestGenerateRejectsUntypedRequestParameters(t *testing.T) {
	for _, optional := range []bool{false, true} {
		param := &ast.ParamDecl{Name: "value"}
		if optional {
			param.Default = &ast.Ident{Name: "fallback"}
		}
		file := &ast.File{APIs: []*ast.ApiDecl{{Name: "read", Params: []*ast.ParamDecl{param}, ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}}}}}
		generated, err := Generate(result(file), "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "value") {
			t.Fatalf("untyped parameter produced source: optional=%v, err=%v", optional, err)
		}
	}
}

func TestGenerateRejectsUntypedFunctionParameters(t *testing.T) {
	for _, directives := range [][]*ast.Directive{
		{{Name: "native"}}, {{Name: "native"}, {Name: "service"}}, {{Name: "service"}},
	} {
		file := &ast.File{Functions: []*ast.FnDecl{{Name: "read", Directives: directives,
			Params: []*ast.ParamDecl{{Name: "value"}}, ReturnType: &ast.TypeRef{Name: "Int"},
		}}}
		generated, err := Generate(result(file), "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "value") {
			t.Fatalf("untyped function parameter produced source: %v", err)
		}
	}
}

func TestGenerateRejectsNonStaticCRUDIdentifiers(t *testing.T) {
	for _, name := range []string{"Custom", "Float", "Boolean"} {
		model := &ast.ModelDecl{Name: "Record", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: name}, Directives: []*ast.Directive{{Name: "id"}}},
		}}
		generated, err := Generate(result(&ast.File{Models: []*ast.ModelDecl{model}}), "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "Record") {
			t.Fatalf("unsupported CRUD identifier produced source: %s, err=%v", name, err)
		}
	}
}

func TestGenerateRejectsIncompleteCRUDTypes(t *testing.T) {
	for _, typ := range []*ast.TypeRef{nil, {Name: "Int", Nullable: true}, {Name: "Int", IsList: true}} {
		model := &ast.ModelDecl{Name: "Record", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
			{Name: "id", Type: typ, Directives: []*ast.Directive{{Name: "id"}}},
		}}
		generated, err := Generate(result(&ast.File{Models: []*ast.ModelDecl{model}}), "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "Record") {
			t.Fatalf("incomplete CRUD type produced source: %v, %v", typ, err)
		}
	}
	if err := validateCRUDParameterTypes(&ast.ModelDecl{Name: "Empty"}); err != nil {
		t.Fatalf("no primary key is synthesized by parameter validation: %v", err)
	}
}

func TestGeneratedStaticCRUDIdentifiers(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import (
    "github.com/google/uuid"
    "github.com/light-speak/luxo/pkg/lux/api"
)
`)
	for _, item := range []struct{ name, goType string }{{"Int", "int64"}, {"String", "string"}, {"UUID", "uuid.UUID"}} {
		fmt.Fprintf(&source, "func read%s(req *api.Request, out *%s) error {\n", item.name, item.goType)
		writeParamID(&source, item.goType, "\t")
		source.WriteString("*out=id; return nil\n}\n")
		fmt.Fprintf(&source, "func read%sList(req *api.Request, out *[]%s) error {\n", item.name, item.goType)
		writeParamIDs(&source, item.goType, "\t")
		source.WriteString("*out=ids; return nil\n}\n")
	}
	if strings.Contains(source.String(), "ParamJSON") || strings.Contains(source.String(), "json.Unmarshal") {
		t.Fatal("CRUD identifiers use an arbitrary target decoder")
	}
	runGeneratedProtocolPackage(t, source.String(), staticCRUDIdentifierAssertions)
}

const staticCRUDIdentifierAssertions = `package generated
import (
    "encoding/json"
    "reflect"
    "testing"
    "github.com/google/uuid"
    "github.com/light-speak/luxo/pkg/lux/api"
)

func checkID[T any](t *testing.T, value T, scalar func(*api.Request,*T)error, list func(*api.Request,*[]T)error) {
    t.Helper()
    for _, binary := range []bool{false,true} {
        for _, many := range []bool{false,true} {
            name:="id"; var input any=value
            if many {name="ids";input=[]T{value,value}}
            req:=&api.Request{}
            if binary {req.SetBinaryParams([]string{name},1);req.SetParamSlot(0,input)} else {
                raw,err:=json.Marshal(input);if err!=nil{t.Fatal(err)}
                req.Params=map[string]json.RawMessage{name:raw}
            }
            if many {var out []T; if err:=list(req,&out);err!=nil||!reflect.DeepEqual(out,[]T{value,value}){t.Fatalf("list %v: %v %v",binary,out,err)}} else {
                var out T;if err:=scalar(req,&out);err!=nil||!reflect.DeepEqual(out,value){t.Fatalf("scalar %v: %v %v",binary,out,err)}
            }
        }
        missing:=&api.Request{Params:map[string]json.RawMessage{}}
        if binary {missing.Params=nil;missing.SetBinaryParams([]string{"id","ids"},2)}
        var one T;var many []T
        if scalar(missing,&one)==nil||list(missing,&many)==nil{t.Fatal("missing ID accepted")}
    }
}

func TestCRUDIdentifierReaders(t *testing.T) {
    checkID(t,int64(9223372036854775807),readInt,readIntList)
    checkID(t,"user-東京",readString,readStringList)
    checkID(t,uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),readUUID,readUUIDList)
    for _,raw:=range []string{"null","1.5","9223372036854775808","true"} {
        var id int64;req:=&api.Request{Params:map[string]json.RawMessage{"id":[]byte(raw)}}
        if readInt(req,&id)==nil{t.Fatalf("bad ID %s accepted",raw)}
    }
}
`

func TestGeneratedErrorNamespaceShadowing(t *testing.T) {
	runGeneratedFixture(t, `
type FailureDetail { value: Int }
fn readError(error: FailureDetail): Int { return error.value }
fn localError(): Int { val error = FailureDetail { value: 42 } return error.value }
`, `package generated
import "testing"

func TestErrorShadowing(t *testing.T){
 app:=&App{}
 value,err:=app.readError(t.Context(),FailureDetail{Value:42});if err!=nil||value!=42{t.Fatal(value,err)}
 value,err=app.localError(t.Context());if err!=nil||value!=42{t.Fatal(value,err)}
}

`, StableIDs{ModelFields: map[string]map[string]int{"FailureDetail": {"value": 1}}})
}

func TestGeneratedAwaitPackageNameBinding(t *testing.T) {
	runGeneratedFixture(t, `
fn echo(value: Int): Int { return value }
fn parallel(errgroup: Int): Int {
 val (left, right) = await { echo(errgroup) echo(1) }
 return left + right
}
`, `package generated
import "testing"

func TestAwaitBinding(t *testing.T) {
 value,err:=(&App{}).parallel(t.Context(),41)
 if err!=nil||value!=42{t.Fatal(value,err)}
}
`, StableIDs{})
}

func TestGeneratedPaginationNamesRemainBusinessParameters(t *testing.T) {
	runGeneratedFixture(t, `
api echoPage(page: Int): Int { return page }
api echoPageSize(pageSize: Int): Int { return pageSize }
api echoPageText(page: String): String { return page }
api echoPageSizes(pageSize: [Int]): [Int] { return pageSize }
api defaultPage(page: Int = 42): Int { return page }
api nullablePage(page: Int?): Int { return page ?: 42 }
`, paginationNameAssertions, StableIDs{})
}

const paginationNameAssertions = `package generated
import("bytes";"context";"encoding/json";"net";"testing";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/rpc")

func paginationRouter(t *testing.T,forward bool)*api.Router {
 t.Helper();router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 if !forward{return router}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 t.Cleanup(func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}})
 client:=rpc.NewClient(listener.Addr().String());t.Cleanup(client.Close)
 gateway:=api.NewRouter();if err:=RegisterSchema(gateway.Schema);err!=nil{t.Fatal(err)}
 if err:=gateway.Registry.RegisterSchemaAPIs(gateway.Schema);err!=nil{t.Fatal(err)}
 for name,definition:=range gateway.Schema.APIs {
  gateway.Handle(name,func(ctx context.Context,req *api.Request)error{
   data,err:=client.CallWithMaskContext(ctx,"",definition.ID,req.FieldMask,req.BinaryParams());if err!=nil{return err}
   req.Buf.B=append(req.Buf.B,data...);return nil
  })
 }
 return gateway
}
func TestPaginationParameterNames(t *testing.T) {
 for _,forward:=range []bool{false,true}{t.Run(map[bool]string{false:"local",true:"LRPC"}[forward],func(t *testing.T){
 router:=paginationRouter(t,forward)
 for _,sample:=range []struct{name,key,typ,raw,want string;value any;nullable bool}{
  {"echoPage","page","Int","-7","-7",int64(-7),false},
  {"echoPage","page","Int","0","0",int64(0),false},
  {"echoPageSize","pageSize","Int","101","101",int64(101),false},
  {"echoPageSize","pageSize","Int","9223372036854775807","9223372036854775807",int64(9223372036854775807),false},
  {"echoPageText","page","String","\"chapter\"","\"chapter\"","chapter",false},
  {"echoPageSizes","pageSize","Int","[1,200]","[1,200]",[]int64{1,200},false},
  {"defaultPage","page","Int","7","7",int64(7),false},
  {"defaultPage","page","Int","","42",nil,false},
  {"nullablePage","page","Int","null","42",nil,true},
 } { for _,binary:=range []bool{false,true}{ t.Run(sample.name+"/"+sample.raw+"/binary="+map[bool]string{false:"false",true:"true"}[binary],func(t *testing.T){
  body:=[]byte("{\"$api\":\""+sample.name+"\"")
  if sample.raw!=""{body=append(body,[]byte(",\""+sample.key+"\":"+sample.raw)...)}
  body=append(body,'}')
  if binary {
   params:=map[string]any{};if sample.raw!=""{params[sample.key]=sample.value}
   meta:=[]api.ParamMeta{{Name:sample.key,Type:sample.typ,FieldID:1,IsList:sample.name=="echoPageSizes",Nullable:sample.nullable}}
   var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[sample.name].ID,nil,params,meta);if err!=nil{t.Fatal(err)}
  }
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body))
  if binary{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  var data []byte
  if binary {
   var err error;data,err=router.StreamPayloadJSON(sample.name,response.Body.Bytes());if err!=nil{t.Fatal(err)}
  } else {
   var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};data=envelope.Data
  }
  if string(data)!=sample.want{t.Fatalf("got %s, want %s",data,sample.want)}
 })}}
 })}
}
`

func TestGenerateRejectsNonCallableExpressions(t *testing.T) {
	for _, source := range []string{
		`fn probe(): Int { return (1)() }`,
		`fn probe(): [Int] { return [1]() }`,
		`fn probe(): Int { return (1 + 2)() }`,
		`fn scalar(): Int { return 1 } fn probe(): Int { return scalar()() }`,
		`type Box { value: Int } fn probe(box: Box): Int { return box.value() }`,
		`enum Status { ACTIVE } fn probe(): Status { return Status.ACTIVE() }`,
		`fn probe(value: Int): Duration { return value.days() }`,
	} {
		t.Run(source, func(t *testing.T) {
			input := analyzePermissionFixture(t, source)
			generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Box": {"value": 1}}}})
			if generated != nil || err == nil || !strings.Contains(err.Error(), "not callable") {
				t.Fatalf("formal generation accepted a non-callable expression or lost its diagnostic: %v", err)
			}
			found := false
			for _, diagnostic := range input.Errors {
				if strings.Contains(diagnostic.Message, "not callable") {
					found = true
					if diagnostic.Pos.File != "permission.luxo" || diagnostic.Pos.Line < 1 || diagnostic.Pos.Col < 1 {
						t.Fatalf("call diagnostic lost its location: %+v", diagnostic)
					}
				}
			}
			if !found {
				t.Fatal("formal generation did not preserve the semantic diagnostic")
			}
		})
	}
}

func TestGeneratedParenthesizedDirectCalls(t *testing.T) {
	runGeneratedFixture(t, `
enum Status { ACTIVE }
fn state(): Status { return Status.ACTIVE }
fn duration(value: Int): Duration { return value.days }
fn scalar(): Int { return 7 }
fn direct(): Int { return (scalar)() }
fn member(): String { return (" value ".trim)().uppercase() }
fn properties(value: String): Int {
 if value.isEmpty() { return value.size() }
 return value.length()
}
fn mapped(values: [String]): [String] { return values.map { value -> (value.trim)().uppercase() } }
`, `package generated
import("context";"slices";"testing";"time")
func TestDirectCalls(t *testing.T){
 app:=&App{};ctx:=context.Background()
 if got,err:=app.state(ctx);err!=nil||got!=StatusACTIVE{t.Fatal(got,err)}
 if got,err:=app.duration(ctx,2);err!=nil||got!=48*time.Hour{t.Fatal(got,err)}
 if got,err:=app.direct(ctx);err!=nil||got!=7{t.Fatal(got,err)}
 if got,err:=app.member(ctx);err!=nil||got!="VALUE"{t.Fatal(got,err)}
 for _,value:=range []string{"","text"}{if got,err:=app.properties(ctx,value);err!=nil||got!=int64(len(value)){t.Fatal(got,err)}}
 if got,err:=app.mapped(ctx,[]string{" one ","two "});err!=nil||!slices.Equal(got,[]string{"ONE","TWO"}){t.Fatal(got,err)}
}
`, StableIDs{})
}
