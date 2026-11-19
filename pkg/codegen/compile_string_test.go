package codegen

import (
	"bytes"
	goast "go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestPureNamedStringArgumentsMatchPositionalGoBody(t *testing.T) {
	input := analyzePermissionFixture(t, `
fn positional(value: String): String { return value.replace("x", "y") }
fn named(value: String): String { return value.replace(new: "y", old: "x") }
`)
	output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
	if err != nil {
		t.Fatal(err)
	}
	bodies := make(map[string]string)
	positions := token.NewFileSet()
	for name, source := range output.Files {
		file, err := parser.ParseFile(positions, name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*goast.FuncDecl)
			if !ok || (fn.Name.Name != "named" && fn.Name.Name != "positional") {
				continue
			}
			var body bytes.Buffer
			if err := format.Node(&body, positions, fn.Body); err != nil {
				t.Fatal(err)
			}
			bodies[fn.Name.Name] = body.String()
		}
	}
	if len(bodies) != 2 || bodies["named"] != bodies["positional"] {
		t.Fatalf("pure named call adds or changes runtime work: %v", bodies)
	}
}

func TestFieldStringNamedArgumentsMatchPositionalOutput(t *testing.T) {
	for _, directive := range []string{"transform", "beforeSave"} {
		t.Run(directive, func(t *testing.T) {
			var outputs []*GenerateResult
			for _, arguments := range []string{`"x", "y"`, `new: "y", old: "x"`} {
				expression := "it.replace(" + arguments + ")"
				if directive == "beforeSave" {
					expression = "it = " + expression
				}
				input := analyzePermissionFixture(t, "model TextProbe @noTime { id: Int @id name: String @"+directive+" { "+expression+" } }")
				generator := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"TextProbe": {"id": 1, "name": 2}}}})
				output, err := generator.Generate(input, "generated", nil)
				if err != nil {
					t.Fatal(err)
				}
				outputs = append(outputs, output)
			}
			if len(outputs[0].Files) != len(outputs[1].Files) {
				t.Fatal("named fields changed generated file count")
			}
			for name, source := range outputs[0].Files {
				if !bytes.Equal(source, outputs[1].Files[name]) {
					t.Fatalf("named field changes generated %s", name)
				}
			}
		})
	}
}

func TestGeneratedStringNamedFieldTransform(t *testing.T) {
	runGeneratedFixture(t, `
model TextProbe @noTime { id: Int @id name: String @transform { it.replace(new: "y", old: "x") } }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestNamedFieldOutput(t *testing.T){
 value:=TextProbe{Id:42,Name:"xax"};buffer:=&api.ResponseBuf{}
 if err:=value.WriteLuxo(buffer,[]byte{3});err!=nil{t.Fatal(err)}
 var decoded TextProbe
 decoder:=codec.NewDecoder(buffer.B);decoded.ReadLuxo(decoder)
 if err:=decoder.Finish();err!=nil||decoded.Name!="yay"||value.Name!="xax"{t.Fatal(decoded,value,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"TextProbe": {"id": 1, "name": 2}}})
}

func TestFieldStringNamedArgumentsRejectInvalidInternalBindings(t *testing.T) {
	for _, arguments := range [][]*ast.NamedArg{
		{nil, {Name: "new", Value: &ast.Ident{Name: "it"}}},
		{{Name: "wrong", Value: &ast.Ident{Name: "it"}}, {Name: "new", Value: &ast.Ident{Name: "it"}}},
	} {
		call := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "replace"}, Args: arguments}
		if got := compileFieldExpr(call, "value"); got != "" {
			t.Fatalf("invalid field binding returned Go code: %s", got)
		}
	}
}

func TestFormalGenerateRejectsStringSignatures(t *testing.T) {
	for _, sample := range []struct{ method, arguments, result, unwrap string }{
		{"lowercase", "1", "String", ""}, {"uppercase", "1", "String", ""},
		{"trim", "1", "String", ""}, {"reversed", "1", "String", ""},
		{"trimStart", "1", "String", ""}, {"trimEnd", "1", "String", ""},
		{"contains", "1", "Boolean", ""}, {"startsWith", "1", "Boolean", ""},
		{"endsWith", "1", "Boolean", ""}, {"matches", "1", "Boolean", ""},
		{"replace", `"x"`, "String", ""}, {"replaceAll", `"x"`, "String", ""},
		{"split", "1", "[String]", ""}, {"substring", `"x"`, "String", ""},
		{"repeat", `"x"`, "String", ""}, {"padStart", `1, 2`, "String", ""},
		{"padEnd", `1, 2`, "String", ""},
		{"toInt", "1", "Int", "?"}, {"toFloat", "1", "Float", "?"},
	} {
		t.Run(sample.method, func(t *testing.T) {
			input := analyzePermissionFixture(t, "fn run(value: String): "+sample.result+" { return value."+sample.method+"("+sample.arguments+")"+sample.unwrap+" }")
			output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
			if err == nil || output != nil || !strings.Contains(err.Error(), "String."+sample.method) {
				t.Fatalf("invalid String call generated output: %v", err)
			}
		})
	}
}

func TestGeneratedStringNamedArgumentOrder(t *testing.T) {
	runGeneratedFixture(t, `
model StringState @noTime { id: Int @id text: String pattern: String calls: Int }
fn replacement(state: StringState): String { state.text = "changed" state.pattern = "c" state.calls = state.calls * 10 + 1 return "z" }
fn pattern(state: StringState): String { state.calls = state.calls * 10 + 2 return "a" }
fn named(state: StringState): String { return state.text.replace(new: replacement(state), old: pattern(state)) }
fn namedAll(state: StringState): String { return state.text.replaceAll(new: replacement(state), old: pattern(state)) }
fn mutatedPattern(state: StringState): String { state.pattern = "c" return "a" }
fn captured(state: StringState): String { return state.text.replace(new: state.pattern, old: mutatedPattern(state)) }
fn fail(state: StringState): String { state.calls = state.calls * 10 + 2 throw error.NotFound }
fn failFirst(state: StringState): String { return state.text.replace(new: fail(state), old: pattern(state)) }
fn failLast(state: StringState): String { return state.text.replace(new: replacement(state), old: fail(state)) }
`, `package generated
import "testing"
func TestNamedStringOrder(t *testing.T) {
 for _,call:=range []func(*App,*StringState)(string,error){
  func(app *App,state *StringState)(string,error){return app.named(t.Context(),state)},
  func(app *App,state *StringState)(string,error){return app.namedAll(t.Context(),state)},
 }{
  state:=&StringState{Text:"abc",Pattern:"a"}
  value,err:=call(&App{},state)
  if err!=nil||value!="zbc"||state.Calls!=12||state.Text!="changed" { t.Fatal(value,err,state) }
 }
 state:=&StringState{Text:"abc",Pattern:"z"}
 if value,err:=(&App{}).captured(t.Context(),state);err!=nil||value!="zbc"||state.Pattern!="c"{t.Fatal(value,err,state)}
 state=&StringState{Text:"abc"}
 if _,err:=(&App{}).failFirst(t.Context(),state);err==nil||state.Calls!=2{t.Fatal(err,state)}
 state=&StringState{Text:"abc"}
 if _,err:=(&App{}).failLast(t.Context(),state);err==nil||state.Calls!=12{t.Fatal(err,state)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"StringState": {"id": 1, "text": 2, "pattern": 3, "calls": 4}}})
}

func TestCompileNamedStringRejectsInvalidInternalBindings(t *testing.T) {
	defer func() {
		failure, ok := recover().(*InternalCompilerError)
		if !ok || !strings.Contains(failure.Detail, "invalid String.replace argument binding") {
			t.Fatalf("invalid binding did not report an internal compiler error: %v", failure)
		}
	}()
	c := newCompiler(nil)
	c.compileStringMethod(&ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Literal{Value: "x"}, Field: "replace"},
		Args: []*ast.NamedArg{{Name: "wrong"}, {Name: "new"}},
	})
}

func TestCompileNamedStringUnknownMethodDoesNotEvaluateReceiver(t *testing.T) {
	c := newCompiler(nil)
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.CallExpr{Func: &ast.Ident{Name: "effect"}}, Field: "unknown"},
		Args: []*ast.NamedArg{{Name: "value"}},
	}
	if got := c.compileStringMethod(call); got != "" || c.b.Len() != 0 || c.resultTmp != 0 {
		t.Fatalf("unknown method evaluated operands: %q %s", got, c.b)
	}
}

func TestGeneratedStringNamedArgumentValues(t *testing.T) {
	runGeneratedFixture(t, `
fn changed(value: String): String { return value.replace(new: "y", old: "x") }
fn positional(value: String): String { return value.replace("x", "y") }
fn sliced(value: String): String { return value.substring(end: 3, start: 1) }
fn tail(value: String): String { return value.substring(start: 1) }
fn padded(value: String): String { return value.padEnd(pad: "0", length: 4) }
fn optional(value: String?): String? { return value?.replace(new: "y", old: "x") }
`, `package generated
import("context";"testing";"strings";"os")
func TestNamedStringValues(t *testing.T) {
 app:=&App{}
 for _,sample:=range []struct{call func(context.Context,string)(string,error);input,want string}{
  {app.changed,"xax","yay"},{app.sliced,"abcd","bc"},{app.tail,"abcd","bcd"},{app.padded,"x","x000"},
 }{got,err:=sample.call(t.Context(),sample.input);if err!=nil||got!=sample.want{t.Fatal(got,err,sample.want)}}
 if got,err:=app.optional(t.Context(),nil);err!=nil||got!=nil{t.Fatal(got,err)}
 value:="x";got,err:=app.optional(t.Context(),&value);if err!=nil||got==nil||*got!="y"{t.Fatal(got,err)}
}
var namedStringSink string
func BenchmarkStringNamed(b *testing.B) {
 app:=&App{};calls:=[]struct{name string;call func(context.Context,string)(string,error)}{{"positional",app.positional},{"named",app.changed}}
 if os.Getenv("LUXO_STRING_NAMED_REVERSE")=="1" { calls[0],calls[1]=calls[1],calls[0] }
 for _,sample:=range []struct{name,value string}{{"short","x"},{"16B","abcdefghijklmnop"},{"1KiB",strings.Repeat("x",1024)}}{
  for _,candidate:=range calls{b.Run(sample.name+"/"+candidate.name,func(b *testing.B){
   ctx:=b.Context();want:=strings.ReplaceAll(sample.value,"x","y");b.ReportAllocs()
   for b.Loop(){value,err:=candidate.call(ctx,sample.value);if err!=nil||value!=want{b.Fatal(value,err)};namedStringSink=value}
  })}
 }
}
`, StableIDs{})
}

func TestGeneratedStringMethodSignatures(t *testing.T) {
	runGeneratedFixture(t, stringMethodSource, stringMethodAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"StringProbe": {"id": 1}},
		APIs:        map[string]int{"parseNullable": 51},
		APIParams:   map[string]map[string]int{"parseNullable": {"value": 1}},
	})
}

const stringMethodSource = `
model StringProbe @noTime { id: Int @id }
fn lower(value: String): String { return value.lowercase() }
fn upper(value: String): String { return value.uppercase() }
fn trimmed(value: String): String { return value.trim() }
fn reversed(value: String): String { return value.reversed() }
fn trimStart(value: String): String { return value.trimStart() }
fn trimEnd(value: String): String { return value.trimEnd() }
fn trimStartSet(value: String, cut: String): String { return value.trimStart(cut) }
fn trimEndSet(value: String, cut: String): String { return value.trimEnd(cut) }
fn contains(value: String, part: String): Boolean { return value.contains(part) }
fn starts(value: String, part: String): Boolean { return value.startsWith(part) }
fn ends(value: String, part: String): Boolean { return value.endsWith(part) }
fn matches(value: String, pattern: String): Boolean { return value.matches(pattern) }
fn replaced(value: String, old: String, replacement: String): String { return value.replace(old, replacement) }
fn replacedAll(value: String, old: String, replacement: String): String { return value.replaceAll(old, replacement) }
fn split(value: String, separator: String): [String] { return value.split(separator) }
fn substring(value: String, start: Int): String { return value.substring(start) }
fn substringRange(value: String, start: Int, end: Int): String { return value.substring(start, end) }
fn repeated(value: String, count: Int): String { return value.repeat(count) }
fn paddedStart(value: String, width: Int, pad: String): String { return value.padStart(width, pad) }
fn paddedEnd(value: String, width: Int, pad: String): String { return value.padEnd(width, pad) }
fn integer(value: String): Int { return value.toInt()? }
fn floating(value: String): Float { return value.toFloat()? }
fn safeLower(value: String?): String? { return value?.lowercase() }
fn safeContains(value: String?, part: String): Boolean? { return value?.contains(part) }
fn safeSplit(value: String?, separator: String): [String]? { return value?.split(separator) }
fn safeInteger(value: String?): Int? { return value?.toInt()? }
fn safeFloating(value: String?): Float? { return value?.toFloat()? }
fn integerDefault(value: String?): Int { return value?.toInt()? ?: 7 }
fn integerRequired(value: String?): Int { return value?.toInt()? ?: throw error.NotFound }
fn explicitInteger(value: String?): Int? { if value != null { return value.toInt()? } return null }
api parseNullable(value: String?): Int? { return value?.toInt()? }
`

const stringMethodAssertions = `package generated
import("bytes";"context";"encoding/json";"math";"net/http/httptest";"slices";"testing";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestStringMethods(t *testing.T) {
 app:=&App{}
 for _,tc:=range []struct{call func(context.Context,string)(string,error);input,want string}{
  {app.lower,"AbC","abc"},{app.upper,"AbC","ABC"},{app.trimmed," a b ","a b"},
  {app.reversed,"ab中","中ba"},{app.trimStart,"  ab ","ab "},{app.trimEnd," ab  "," ab"},
 } {if got,err:=tc.call(t.Context(),tc.input);err!=nil||got!=tc.want{t.Fatal(got,tc.want,err)}}
 if got,err:=app.trimStartSet(t.Context(),"xxax","x");got!="ax"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.trimEndSet(t.Context(),"xaxx","x");got!="xa"||err!=nil{t.Fatal(got,err)}
 for _,tc:=range []struct{call func(context.Context,string,string)(bool,error);value,part string}{
  {app.contains,"abc","b"},{app.starts,"abc","a"},{app.ends,"abc","c"},{app.matches,"aaaa","a+"},
 } {if got,err:=tc.call(t.Context(),tc.value,tc.part);!got||err!=nil{t.Fatal(got,err)}}
 for _,call:=range []func(context.Context,string,string,string)(string,error){app.replaced,app.replacedAll}{
  if got,err:=call(t.Context(),"x-x","x","y");got!="y-y"||err!=nil{t.Fatal(got,err)}
 }
 if got,err:=app.split(t.Context(),"a,b,",",");!slices.Equal(got,[]string{"a","b",""})||err!=nil{t.Fatal(got,err)}
 if got,err:=app.substring(t.Context(),"abcd",1);got!="bcd"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.substringRange(t.Context(),"abcd",1,3);got!="bc"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.repeated(t.Context(),"ab",2);got!="abab"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.paddedStart(t.Context(),"7",3,"0");got!="007"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.paddedEnd(t.Context(),"7",3,"0");got!="700"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.integer(t.Context(),"42");got!=42||err!=nil{t.Fatal(got,err)}
 if _,err:=app.integer(t.Context(),"x");err==nil{t.Fatal("invalid integer accepted")}
 if got,err:=app.floating(t.Context(),"-0");got!=0||!math.Signbit(got)||err!=nil{t.Fatal(got,err)}
}
func TestNullableStringMethods(t *testing.T) {
 app:=&App{}
 if got,err:=app.safeLower(t.Context(),nil);got!=nil||err!=nil{t.Fatal(got,err)}
 value:="AbC";if got,err:=app.safeLower(t.Context(),&value);got==nil||*got!="abc"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.safeContains(t.Context(),nil,"x");got!=nil||err!=nil{t.Fatal(got,err)}
 if got,err:=app.safeContains(t.Context(),&value,"x");got==nil||*got||err!=nil{t.Fatal(got,err)}
 if got,err:=app.safeSplit(t.Context(),nil,",");got!=nil||err!=nil{t.Fatal(got,err)}
 value="a,b";if got,err:=app.safeSplit(t.Context(),&value,",");!slices.Equal(got,[]string{"a","b"})||err!=nil{t.Fatal(got,err)}
 if got,err:=app.safeInteger(t.Context(),nil);got!=nil||err!=nil{t.Fatal(got,err)}
 value="42";if got,err:=app.safeInteger(t.Context(),&value);got==nil||*got!=42||err!=nil{t.Fatal(got,err)}
 value="x";if _,err:=app.safeInteger(t.Context(),&value);err==nil{t.Fatal("invalid safe integer accepted")}
 if got,err:=app.safeFloating(t.Context(),nil);got!=nil||err!=nil{t.Fatal(got,err)}
 value="-0";if got,err:=app.safeFloating(t.Context(),&value);got==nil||!math.Signbit(*got)||err!=nil{t.Fatal(got,err)}
 if got,err:=app.integerDefault(t.Context(),nil);got!=7||err!=nil{t.Fatal(got,err)}
 if _,err:=app.integerRequired(t.Context(),nil);err==nil{t.Fatal("required null accepted")}
 value="x";if _,err:=app.integerDefault(t.Context(),&value);err==nil{t.Fatal("parse error became default")}
 value="42";if got,err:=app.integerRequired(t.Context(),&value);got!=42||err!=nil{t.Fatal(got,err)}
}
func TestNullableStringResultHTTP(t *testing.T) {
 router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{for _,input:=range []string{"null","42","x"}{
  var value *string;if input!="null"{value=&input}
  body:=[]byte("{\"$api\":\"parseNullable\",\"value\":null}")
  if value!=nil{body=[]byte("{\"$api\":\"parseNullable\",\"value\":\""+input+"\"}")}
  if binary{
   encoder:=codec.NewEncoder(codec.AppendVarint(codec.AppendVarint(nil,51),0))
   if err:=encoder.WriteFieldStringPtr(1,value);err!=nil{t.Fatal(err)};encoder.WriteEnd();body=encoder.Bytes()
  }
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if input=="x"{if response.Code<400{t.Fatal("invalid input succeeded",binary,response.Code)};continue}
  if response.Code!=200{t.Fatal(binary,response.Code,response.Body.String())}
  expected:=[]byte("null");if input=="42"{expected=[]byte("42")}
  if binary{
   expected=codec.AppendNull(nil);if input=="42"{expected=codec.AppendSvarint(codec.AppendPresent(nil),42)}
   if !bytes.Equal(response.Body.Bytes(),expected){t.Fatal(binary,input,response.Body.Bytes(),expected)}
  }else{
   var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||!bytes.Equal(envelope.Data,expected){t.Fatal(input,response.Body.String(),err)}
  }
 }}
}
var stringResultSink *int64
var stringErrorSink error
func BenchmarkGeneratedNullableStringResult(b *testing.B) {
 app:=&App{};valid,invalid:="42","x"
 for _,version:=range []struct{name string;call func(context.Context,*string)(*int64,error)}{
  {"explicit",app.explicitInteger},{"safe",app.safeInteger},
 }{b.Run(version.name,func(b *testing.B){
  for _,sample:=range []struct{name string;input *string;fail bool}{{"null",nil,false},{"present",&valid,false},{"error",&invalid,true}}{
   b.Run(sample.name,func(b *testing.B){
    got,err:=version.call(b.Context(),sample.input)
    if (err!=nil)!=sample.fail||(sample.input==nil&&got!=nil)||(sample.name=="present"&&(got==nil||*got!=42)){b.Fatal(got,err)}
    b.ReportAllocs()
    for b.Loop(){stringResultSink,stringErrorSink=version.call(b.Context(),sample.input)}
   })
  }
 })}
}
`
