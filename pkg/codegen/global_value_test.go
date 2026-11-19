package codegen

import (
	"bytes"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedGlobalScalarConstants(t *testing.T) {
	runGeneratedFixture(t, `
enum Role { USER ADMIN }
type _luxoGlobal0 { value: Int token: UUID? roles: [Role] }
val APP_NAME = "luxo"
val RETRIES = 3
val WAIT = 10ms
val DAY = 1d
val FRACTION = 0.1 + 0.2
val TENTH = 0.1
val FIFTH = 0.2
val ONE: Float = 1.0
val NEGATIVE_ZERO = -0.0
val LOWEST = -9223372036854775808
val ALLOWED = true && !false
val SELECTED_ROLE = Role.ADMIN
val ctx = RETRIES + 2
val LABEL = APP_NAME + "-beta"
fn name(): String { return LABEL }
fn retries(): Int { return RETRIES }
fn wait(): Duration { return WAIT }
fn day(): Duration { return DAY }
fn fraction(): Float { return FRACTION }
fn runtimeSum(): Float { return TENTH + FIFTH }
fn typedFloat(): Float { return ONE + 0.2 }
fn literalRetries(): Int { return 3 }
fn literalFraction(): Float { return 0.30000000000000004 }
fn literalName(): String { return "luxo-beta" }
fn discardedArithmetic(value: Int): Int { value + 1 return 7 }
fn explicitDiscard(value: Int): Int { val _ = value + 1 return 7 }
fn negativeZero(): Float { return NEGATIVE_ZERO }
fn lowest(): Int { return LOWEST }
fn allowed(): Boolean { return ALLOWED }
fn role(): Role { return SELECTED_ROLE }
fn reserved(): Int { return ctx }
fn shadow(APP_NAME: String): String { return APP_NAME }
fn local(): Int { val RETRIES = 7 return RETRIES }
fn localInitial(): Int { val RETRIES = RETRIES + 1 return RETRIES }
fn typedInitial(): Int { val RETRIES: Int = RETRIES + 1 return RETRIES }
fn nested(): Int { for item in [1, 2] { val RETRIES = item RETRIES + 1 } return RETRIES }
fn mapped(): [Int] { return [1, 2].map { RETRIES + it } }
api applicationName(): String { return APP_NAME }
api applicationFraction(): Float { return FRACTION }
`, globalConstantAssertions, StableIDs{ModelFields: map[string]map[string]int{"_luxoGlobal0": {"value": 1, "token": 2, "roles": 3}}})
}

const globalConstantAssertions = `package generated
import("bytes";"math";"net";"net/http/httptest";"testing";"time";
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/google/uuid")
func TestGlobalConstants(t *testing.T) {
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 if v,e:=app.name(t.Context());e!=nil||v!="luxo-beta"{t.Fatal(v,e)}
 if v,e:=app.retries(t.Context());e!=nil||v!=3{t.Fatal(v,e)}
 if v,e:=app.wait(t.Context());e!=nil||v!=10*time.Millisecond{t.Fatal(v,e)}
 if v,e:=app.day(t.Context());e!=nil||v!=24*time.Hour{t.Fatal(v,e)}
 left,right:=0.1,0.2
 if v,e:=app.fraction(t.Context());e!=nil||math.Float64bits(v)!=math.Float64bits(left+right){t.Fatal(v,e)}
 if v,e:=app.runtimeSum(t.Context());e!=nil||math.Float64bits(v)!=math.Float64bits(left+right){t.Fatal(v,e)}
 if v,e:=app.typedFloat(t.Context());e!=nil||v!=1.2{t.Fatal(v,e)}
 if v,e:=app.discardedArithmetic(t.Context(),3);e!=nil||v!=7{t.Fatal(v,e)}
 if v,e:=app.explicitDiscard(t.Context(),3);e!=nil||v!=7{t.Fatal(v,e)}
 if v,e:=app.negativeZero(t.Context());e!=nil||math.Float64bits(v)!=1<<63{t.Fatal(v,e)}
 if v,e:=app.lowest(t.Context());e!=nil||v!=math.MinInt64{t.Fatal(v,e)}
 if v,e:=app.allowed(t.Context());e!=nil||!v{t.Fatal(v,e)}
 if v,e:=app.role(t.Context());e!=nil||v!=RoleADMIN{t.Fatal(v,e)}
 if v,e:=app.reserved(t.Context());e!=nil||v!=5{t.Fatal(v,e)}
 if v,e:=app.shadow(t.Context(),"local");e!=nil||v!="local"{t.Fatal(v,e)}
 if v,e:=app.local(t.Context());e!=nil||v!=7{t.Fatal(v,e)}
 if v,e:=app.localInitial(t.Context());e!=nil||v!=4{t.Fatal(v,e)}
 if v,e:=app.typedInitial(t.Context());e!=nil||v!=4{t.Fatal(v,e)}
 if v,e:=app.nested(t.Context());e!=nil||v!=3{t.Fatal(v,e)}
 if v,e:=app.mapped(t.Context());e!=nil||len(v)!=2||v[0]!=4||v[1]!=5{t.Fatal(v,e)}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{7},nil);if err!=nil{t.Fatal(err)}
 token:=uuid.UUID{1,2,3};value:=_luxoGlobal0{Value:7,Token:&token,Roles:[]Role{RoleADMIN}};buf:=&api.ResponseBuf{}
 if err:=value.WriteLuxo(buf,mask);err!=nil{t.Fatal(err)}
 var decoded _luxoGlobal0;decoder:=codec.NewDecoder(buf.B);decoded.ReadLuxo(decoder)
 if err:=decoder.Finish();err!=nil||decoded.Value!=7||decoded.Token==nil||*decoded.Token!=token||len(decoded.Roles)!=1||decoded.Roles[0]!=RoleADMIN{t.Fatal(decoded,err)}
 var readError error
 if count:=testing.AllocsPerRun(100,func(){_,readError=app.fraction(t.Context())});count!=0||readError!=nil{t.Fatal("Float constant read",count,readError)}
}
func TestGlobalConstantsWire(t *testing.T) {
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,name:=range []string{"applicationName","applicationFraction"} {
  for _,binary:=range []bool{false,true} {
   body:=[]byte("{\"$api\":\""+name+"\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
   if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
   if binary{verifyGlobalBinary(t,name,response.Body.Bytes())}else{
    want:="{\"data\":\"luxo\"}";if name=="applicationFraction"{want="{\"data\":0.30000000000000004}"}
    if response.Body.String()!=want{t.Fatal(name,response.Body.String())}
   }
  }
  data,err:=client.CallWithMaskContext(t.Context(),"",router.Schema.APIs[name].ID,nil,[]byte{0});if err!=nil{t.Fatal(err)}
  verifyGlobalBinary(t,name,data)
 }
}
func verifyGlobalBinary(t *testing.T,name string,data []byte) {
 t.Helper();decoder:=codec.NewDecoder(data)
 if name=="applicationName"{if v:=decoder.ReadString();v!="luxo"{t.Fatal(v)}}else{
  left,right:=0.1,0.2;if v:=decoder.ReadFloat();math.Float64bits(v)!=math.Float64bits(left+right){t.Fatal(v)}
 }
 if err:=decoder.Finish();err!=nil{t.Fatal(err)}
}

var benchmarkGlobalInt int64
var benchmarkGlobalFloat float64
var benchmarkGlobalString string
func BenchmarkGlobalReads(b *testing.B) {
 app,err:=New(b.Context());if err!=nil{b.Fatal(err)};defer app.Close();ctx:=b.Context()
 b.Run("int/global",func(b *testing.B){b.ReportAllocs();for b.Loop(){benchmarkGlobalInt,_=app.retries(ctx)}})
 b.Run("int/literal",func(b *testing.B){b.ReportAllocs();for b.Loop(){benchmarkGlobalInt,_=app.literalRetries(ctx)}})
 b.Run("float/global",func(b *testing.B){b.ReportAllocs();for b.Loop(){benchmarkGlobalFloat,_=app.fraction(ctx)}})
 b.Run("float/literal",func(b *testing.B){b.ReportAllocs();for b.Loop(){benchmarkGlobalFloat,_=app.literalFraction(ctx)}})
 b.Run("string/global",func(b *testing.B){b.ReportAllocs();for b.Loop(){benchmarkGlobalString,_=app.name(ctx)}})
 b.Run("string/literal",func(b *testing.B){b.ReportAllocs();for b.Loop(){benchmarkGlobalString,_=app.literalName(ctx)}})
 b.Run("statement/implicit",func(b *testing.B){b.ReportAllocs();for b.Loop(){benchmarkGlobalInt,_=app.discardedArithmetic(ctx,3)}})
 b.Run("statement/explicit",func(b *testing.B){b.ReportAllocs();for b.Loop(){benchmarkGlobalInt,_=app.explicitDiscard(ctx,3)}})
}
`

func TestGenerateReportsUnresolvedGlobalInitializer(t *testing.T) {
	for _, source := range []string{
		`val INITIAL = now() fn read(): DateTime { return INITIAL }`,
		`val INITIAL = [1, 2] fn read(): [Int] { return INITIAL }`,
		`val BASE = 9223372036854775807 val INITIAL = BASE + 1 fn read(): Int { return INITIAL }`,
		`val INITIAL = 1.0 / 0.0 fn read(): Float { return INITIAL }`,
	} {
		result := analyzePermissionFixture(t, source)
		output, err := Generate(result, "generated", GeneratorConfig{Driver: DriverPG})
		if output != nil || err == nil || !strings.Contains(err.Error(), "INITIAL") || !strings.Contains(err.Error(), "initializer") {
			t.Fatalf("unresolved initializer emitted incomplete source: output=%t error=%v", output != nil, err)
		}
	}
}

func TestGlobalValueCompilationIsolation(t *testing.T) {
	inputs := []*semantic.Result{
		analyzePermissionFixture(t, `val ITEM = 3 fn read(): Int { return ITEM }`),
		analyzePermissionFixture(t, `val ITEM = 9 fn read(): Int { return ITEM }`),
	}
	g := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG})
	var expected [2][]byte
	for index, input := range inputs {
		generated, err := g.Generate(input, "generated", nil)
		if err != nil {
			t.Fatal(err)
		}
		expected[index] = generated.Files["global.gen.go"]
	}
	var group sync.WaitGroup
	for index := range 16 {
		group.Go(func() {
			which := index % len(inputs)
			generated, err := g.Generate(inputs[which], "generated", nil)
			if err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(generated.Files["global.gen.go"], expected[which]) {
				t.Error("concurrent compilation reused another input's global constants")
			}
		})
	}
	group.Wait()
	if g.globalValues != nil {
		t.Fatal("compilation mutated reusable generator configuration")
	}
}

func TestNoGlobalValuesAvoidEmissionAllocation(t *testing.T) {
	g := defaultGenerator()
	input := &semantic.Result{Files: []*ast.File{{Name: "ordinary.luxo"}}}
	var output []byte
	var err error
	allocations := testing.AllocsPerRun(100, func() {
		output, err = g.generateGlobalValues(input, "generated", nil)
	})
	if allocations != 0 || output != nil || err != nil {
		t.Fatal("ordinary compilation allocated an unused global source builder", allocations, err)
	}
}

func assertGlobalScalar(t *testing.T, value globalScalar, known bool, want globalScalar, valid bool) {
	t.Helper()
	if known != valid || known && (value.kind != want.kind || value.code != want.code || value.integer != want.integer || math.Float64bits(value.floating) != math.Float64bits(want.floating)) {
		t.Fatalf("constant=%+v known=%t; want=%+v known=%t", value, known, want, valid)
	}
}

func TestGlobalLiteralValues(t *testing.T) {
	for _, sample := range []struct {
		kind      token.Type
		raw, sign string
		want      globalScalar
		valid     bool
	}{
		{token.Int, "09", "", globalScalar{kind: "Int", integer: 9}, true},
		{token.Int, "9223372036854775808", "-", globalScalar{kind: "Int", integer: math.MinInt64}, true},
		{token.Int, "bad", "", globalScalar{}, false},
		{token.Duration, "2ms", "", globalScalar{kind: "Duration", integer: int64(2 * time.Millisecond)}, true},
		{token.Duration, "1d", "-", globalScalar{kind: "Duration", integer: -int64(24 * time.Hour)}, true},
		{token.Duration, "xd", "", globalScalar{}, false},
		{token.Duration, "9223372036854775807d", "", globalScalar{}, false},
		{token.Duration, "bad", "", globalScalar{}, false},
		{token.Float, "0.0", "-", globalScalar{kind: "Float", floating: math.Copysign(0, -1)}, true},
		{token.Float, "bad", "", globalScalar{}, false},
		{token.Float, "NaN", "", globalScalar{}, false},
		{token.String, "中文", "", globalScalar{kind: "String", code: `"中文"`}, true},
		{token.String, "text", "+", globalScalar{}, false},
		{token.True, "", "", globalScalar{kind: "Boolean", code: "true"}, true},
		{token.False, "", "", globalScalar{kind: "Boolean", code: "false"}, true},
		{token.Null, "", "", globalScalar{}, false},
	} {
		value, ok := globalLiteral(&ast.Literal{Kind: sample.kind, Value: sample.raw}, sample.sign)
		assertGlobalScalar(t, value, ok, sample.want, sample.valid)
	}
}

func TestGlobalUnaryValues(t *testing.T) {
	g := defaultGenerator()
	g.globalValues = map[string]generatedGlobalValue{
		"integer":  {value: globalScalar{kind: "Int", integer: 3}},
		"minimum":  {value: globalScalar{kind: "Int", integer: math.MinInt64}},
		"float":    {value: globalScalar{kind: "Float"}},
		"flag":     {value: globalScalar{kind: "Boolean"}, reference: "flag"},
		"text":     {value: globalScalar{kind: "String"}, reference: "text"},
		"duration": {value: globalScalar{kind: "Duration", integer: 1}},
	}
	for _, sample := range []struct {
		operator, name string
		want           globalScalar
		valid          bool
	}{
		{"+", "integer", globalScalar{kind: "Int", integer: 3}, true},
		{"-", "integer", globalScalar{kind: "Int", integer: -3}, true},
		{"-", "minimum", globalScalar{}, false},
		{"+", "float", globalScalar{kind: "Float"}, true},
		{"-", "float", globalScalar{kind: "Float", floating: math.Copysign(0, -1)}, true},
		{"!", "flag", globalScalar{kind: "Boolean", code: "!(flag)"}, true},
		{"-", "text", globalScalar{}, false},
		{"-", "duration", globalScalar{kind: "Duration", integer: -1}, true},
		{"~", "integer", globalScalar{}, false},
		{"-", "missing", globalScalar{}, false},
	} {
		value, ok := g.evaluateGlobalUnary(&ast.UnaryExpr{Op: sample.operator, Value: &ast.Ident{Name: sample.name}}, nil)
		assertGlobalScalar(t, value, ok, sample.want, sample.valid)
	}
}

func TestGlobalIntegralOperations(t *testing.T) {
	for _, sample := range []struct {
		operator            string
		left, right, result int64
		valid               bool
	}{
		{"+", 2, 3, 5, true}, {"-", 3, 2, 1, true}, {"*", 2, 3, 6, true}, {"/", 7, 2, 3, true}, {"%", 7, 2, 1, true},
		{"+", math.MaxInt64, 1, 0, false}, {"/", math.MinInt64, -1, 0, false}, {"/", 1, 0, 0, false}, {"&", 1, 2, 0, false},
	} {
		value, ok := globalBinary(sample.operator, "Int", globalScalar{kind: "Int", integer: sample.left}, globalScalar{kind: "Int", integer: sample.right})
		assertGlobalScalar(t, value, ok, globalScalar{kind: "Int", integer: sample.result}, sample.valid)
	}
	value, ok := globalBinary("+", "Duration", globalScalar{kind: "Duration", integer: 2}, globalScalar{kind: "Duration", integer: 3})
	assertGlobalScalar(t, value, ok, globalScalar{kind: "Duration", integer: 5}, true)
}

func TestGlobalFloatOperations(t *testing.T) {
	for _, sample := range []struct {
		operator            string
		left, right, result float64
		valid               bool
	}{
		{"+", 0.1, 0.2, 0.1 + float64(0.2), true}, {"-", 3.5, 2, 1.5, true}, {"*", 2.5, 3, 7.5, true},
		{"/", 7, 2, 3.5, true}, {"%", 5.5, 2, 1.5, true}, {"/", 1, 0, 0, false}, {"/", 0, 0, 0, false}, {"&", 1, 2, 0, false},
	} {
		value, ok := globalBinary(sample.operator, "Float", globalScalar{kind: "Float", floating: sample.left}, globalScalar{kind: "Float", floating: sample.right})
		want := sample.result
		if sample.operator == "+" {
			want = sample.left + sample.right
		}
		assertGlobalScalar(t, value, ok, globalScalar{kind: "Float", floating: want}, sample.valid)
	}
	for _, operands := range [][2]globalScalar{
		{{kind: "Int", integer: 3}, {kind: "Float", floating: 0.5}},
		{{kind: "Float", floating: 0.5}, {kind: "Int", integer: 3}},
		{{kind: "Int", integer: 3}, {kind: "Int", integer: 0}},
	} {
		value, ok := globalBinary("+", "Float", operands[0], operands[1])
		want := 3.5
		if operands[1].kind == "Int" && operands[0].kind == "Int" {
			want = 3
		}
		assertGlobalScalar(t, value, ok, globalScalar{kind: "Float", floating: want}, true)
	}
}

func TestGlobalComparisons(t *testing.T) {
	for _, sample := range []struct {
		operator   string
		comparison int
		want       string
		valid      bool
	}{
		{"==", 0, "true", true}, {"!=", 0, "false", true}, {"<", -1, "true", true},
		{"<=", 0, "true", true}, {">", 1, "true", true}, {">=", 0, "true", true}, {"?", 0, "", false},
	} {
		value, ok := globalComparison(sample.operator, sample.comparison)
		assertGlobalScalar(t, value, ok, globalScalar{kind: "Boolean", code: sample.want}, sample.valid)
	}
	for _, operands := range [][2]globalScalar{
		{{kind: "Int", integer: 1}, {kind: "Int", integer: 2}},
		{{kind: "Float", floating: 1}, {kind: "Float", floating: 2}},
	} {
		value, ok := globalBinary("<", "Boolean", operands[0], operands[1])
		assertGlobalScalar(t, value, ok, globalScalar{kind: "Boolean", code: "true"}, true)
	}
}

func TestGlobalNonNumericOperations(t *testing.T) {
	for _, sample := range []struct {
		operator, result, operand, left, right, want string
		valid                                        bool
	}{
		{"+", "String", "String", `"a"`, `"b"`, `("a" + "b")`, true},
		{"<", "Boolean", "String", `"a"`, `"b"`, `("a" < "b")`, true},
		{"&&", "Boolean", "Boolean", "true", "false", "(true && false)", true},
		{"||", "Boolean", "Boolean", "true", "false", "(true || false)", true},
		{"!=", "Boolean", "Role", "RoleADMIN", "RoleUSER", "(RoleADMIN != RoleUSER)", true},
		{"+", "Boolean", "Boolean", "true", "false", "", false},
		{"*", "String", "String", `"a"`, `"b"`, "", false},
		{"<", "String", "String", `"a"`, `"b"`, "", false},
	} {
		value, ok := globalBinary(sample.operator, sample.result, globalScalar{kind: sample.operand, code: sample.left}, globalScalar{kind: sample.operand, code: sample.right})
		assertGlobalScalar(t, value, ok, globalScalar{kind: sample.result, code: sample.want}, sample.valid)
	}
	if _, ok := globalBinary("+", "String", globalScalar{kind: "String"}, globalScalar{kind: "Boolean"}); ok {
		t.Fatal("mismatched operands accepted")
	}
}

func TestGlobalConstantCoercionAndUnsupportedNodes(t *testing.T) {
	for _, target := range []*ast.TypeRef{nil, {Name: "Int"}, {Name: "Float"}, {Name: "Int", Nullable: true}, {Name: "Int", IsList: true}, {Name: "String"}} {
		value := globalScalar{kind: "Int", integer: 3}
		want := target == nil || target.Name == "Int" && !target.Nullable && !target.IsList
		if ok := coerceGlobalValue(&value, target); ok != want {
			t.Fatal(target, ok)
		}
	}
	g := defaultGenerator()
	for _, expr := range []ast.Expr{nil, &ast.Ident{Name: "missing"}, &ast.ListExpr{}, &ast.MemberExpr{Object: &ast.Literal{}, Field: "field"}, &ast.BinaryExpr{Left: &ast.Ident{Name: "missing"}, Right: &ast.Literal{Kind: token.Int, Value: "1"}}} {
		if _, ok := g.evaluateGlobalValue(expr, nil); ok {
			t.Fatalf("unsupported expression accepted: %T", expr)
		}
	}
	for _, declaration := range []*ast.ValStmt{
		{Name: "x", Mutable: true, Value: &ast.Literal{Kind: token.Int, Value: "1"}},
		{Names: []string{"x", "y"}, Value: &ast.Literal{Kind: token.Int, Value: "1"}},
	} {
		output, err := g.generateGlobalValues(&semantic.Result{Files: []*ast.File{{Globals: []*ast.ValStmt{declaration}}}}, "generated", nil)
		if output != nil || err == nil {
			t.Fatal("unsupported declaration emitted source", err)
		}
	}
	if output, err := g.generateGlobalValues(&semantic.Result{}, "generated", nil); output != nil || err != nil || g.globalValues != nil {
		t.Fatal("empty compilation retained global metadata", err)
	}
}
