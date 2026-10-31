package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

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
type App struct{Resolver NativeResolver}
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
 container,err:=postgres.Run(t.Context(),"postgres:17-alpine",postgres.WithDatabase("luxo_default"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
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
	enums := map[string]bool{"Role": true}
	api := &ast.ApiDecl{
		Name: "list",
		Params: []*ast.ParamDecl{
			{
				Name: "role",
				Type: &ast.TypeRef{Name: "String"},
				Default: &ast.MemberExpr{
					Object: &ast.Ident{Name: "Role"},
					Field:  "admin",
				},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, enums)
	out := b.String()

	// Enum default: RoleADMIN
	if !strings.Contains(out, "RoleADMIN") {
		t.Fatalf("expected enum default RoleADMIN, got:\n%s", out)
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
