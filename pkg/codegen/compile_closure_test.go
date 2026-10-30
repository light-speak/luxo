package codegen

import (
	"context"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestFormalControlFlowGeneratesConcreteTypes(t *testing.T) {
	const source = `
fn choose(flag: Boolean): [Int] {
 val values = when {
  flag -> [1, 2]
  else -> [3]
 }
 return values
}

fn mapped(items: [Int]): [Int] {
 return for item in items { item + 1 }
}
fn collected(items: [Int]): [Int] {
 return items.map { value -> value + 1 }.filter { it > 0 }
}
fn parallel(flag: Boolean): [Int] {
 val (left, right) = await { choose(flag) choose(false) }
 return collected(left)
}
api values(flag: Boolean): [Int] { return mapped(choose(flag)) }
`
	tokens, lexErrors := lexer.New(source, "static.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("static.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	ids := completeFixtureAPIIDs(result, StableIDs{APIs: map[string]int{"values": 1}})
	generated, err := Generate(result, "generated", GeneratorConfig{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	assertConcreteControlFlow(t, generated)
	runGeneratedFixture(t, source, `package generated
import("testing";"slices")
type App struct{}
func TestConcreteFunctions(t *testing.T) {
 app := &App{}
 chosen,err:=app.choose(t.Context(),true)
 if err!=nil||!slices.Equal(chosen,[]int64{1,2}) {t.Fatal(chosen,err)}
 mapped,err:=app.mapped(t.Context(),chosen)
 if err!=nil||!slices.Equal(mapped,[]int64{2,3}) {t.Fatal(mapped,err)}
 filtered,err:=app.collected(t.Context(),[]int64{-2,0,1})
 if err!=nil||!slices.Equal(filtered,[]int64{1,2}) {t.Fatal(filtered,err)}
 parallel,err:=app.parallel(t.Context(),true)
 if err!=nil||!slices.Equal(parallel,[]int64{2,3}) {t.Fatal(parallel,err)}
}
`, ids)
}

func TestFormalTypedChannel(t *testing.T) {
	const source = `
model Envelope @noTime { id: Int @id }
fn payload(): Int {
 val ch: Channel<Envelope> = Channel(1)
 ch <- Envelope { id: 42 }
 val value = <-ch
 return value.id
}
fn nullableMessage(): Int {
 val ch: Channel<Int?> = Channel(1)
 ch <- 42
 val value = <-ch
 return value ?: 0
}
fn exchange(capacity: Int): Int {
 val ch: Channel<Int> = Channel(capacity)
 ch <- 42
 val value = <-ch
 ch.close()
 return value
}
fn selected(): Int {
 val ch: Channel<Int> = Channel(1)
 ch <- 41
 return when { <-ch -> { value -> value + 1 } else -> 0 }
}
api probe(): Int { return exchange(1) }
fn transfer(ch: Channel<Int>, value: Int): Int {
 ch <- value
 return <-ch
}
`
	runGeneratedFixture(t, source, `package generated
import "testing"
func TestChannel(t *testing.T) {
 app := &App{}
 value,err:=app.exchange(t.Context(),1)
 if err!=nil||value!=42 {t.Fatal(value,err)}
 if _,err=app.exchange(t.Context(),-1);err==nil {t.Fatal("negative capacity accepted")}
 if value,err=app.selected(t.Context());err!=nil||value!=42 {t.Fatal(value,err)}
 if value,err=app.payload(t.Context());err!=nil||value!=42 {t.Fatal(value,err)}
 if value,err=app.nullableMessage(t.Context());err!=nil||value!=42 {t.Fatal(value,err)}
}
// The legacy control retains the former interface payload; capacity is valid.
func legacyExchange(capacity int64) (int64,error) {
 ch:=make(chan any,capacity)
 ch<-int64(42)
 value:=(<-ch).(int64)
 close(ch)
 return value,nil
}
func uncheckedTypedExchange(capacity int64) (int64,error) {
 ch:=make(chan int64,capacity)
 ch<-42
 value:=<-ch
 close(ch)
 return value,nil
}
func BenchmarkTypedChannel(b *testing.B) {
 app:=&App{}
 ctx:=b.Context()
 b.Run("lifecycle/legacy",func(b *testing.B){
  b.ReportAllocs()
  for b.Loop(){value,err:=legacyExchange(1);if err!=nil||value!=42 {b.Fatal(value,err)}}
 })
 b.Run("lifecycle/generated",func(b *testing.B){
  b.ReportAllocs()
  for b.Loop(){value,err:=app.exchange(ctx,1);if err!=nil||value!=42 {b.Fatal(value,err)}}
 })
 b.Run("lifecycle/typed_unchecked",func(b *testing.B){
  b.ReportAllocs()
  for b.Loop(){value,err:=uncheckedTypedExchange(1);if err!=nil||value!=42 {b.Fatal(value,err)}}
 })
 b.Run("message/legacy",func(b *testing.B){
  ch:=make(chan any,1)
  var input int64=9007199254740993
  b.ReportAllocs()
  for b.Loop(){ch<-input;value:=(<-ch).(int64);if value!=input {b.Fatal(value)};input++}
 })
 b.Run("message/generated",func(b *testing.B){
  ch:=make(chan int64,1)
  var input int64=9007199254740993
  b.ReportAllocs()
  for b.Loop(){value,err:=app.transfer(ctx,ch,input);if err!=nil||value!=input {b.Fatal(value,err)};input++}
 })
}
`, StableIDs{APIs: map[string]int{"probe": 1}, ModelFields: map[string]map[string]int{"Envelope": {"id": 1}}})
}

func assertConcreteControlFlow(t *testing.T, generated *GenerateResult) {
	t.Helper()
	checked := 0
	for name, data := range generated.Files {
		parsed, err := goparser.ParseFile(gotoken.NewFileSet(), name, data, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			fn, ok := declaration.(*goast.FuncDecl)
			if !ok || (fn.Name.Name != "choose" && fn.Name.Name != "mapped" && fn.Name.Name != "collected" && fn.Name.Name != "parallel") {
				continue
			}
			checked++
			goast.Inspect(fn, func(node goast.Node) bool {
				if ident, ok := node.(*goast.Ident); ok && ident.Name == "any" {
					t.Errorf("%s contains dynamic fallback", fn.Name.Name)
				}
				if _, ok := node.(*goast.InterfaceType); ok {
					t.Errorf("%s contains interface fallback", fn.Name.Name)
				}
				return true
			})
		}
	}
	if checked != 4 {
		t.Fatalf("checked %d functions, want 4", checked)
	}
}

func TestGeneratedNestedYieldOwnership(t *testing.T) {
	runGeneratedFixture(t, `
fn mapped(items: [Int]): [Int] {
 val results = for item in items {
  val inner = for candidate in [item] { yield candidate }
  inner ?: 0
 }
 return results
}
api mapItems(items: [Int]): [Int] { return mapped(items) }
`, `package generated
import("testing";"slices")
type App struct{}
func TestMapped(t *testing.T) {
 for _,items:=range [][]int64{nil,{}, {1,2,3}} {
  got,err:=(&App{}).mapped(t.Context(),items)
  if err!=nil {t.Fatal(err)}
  if !slices.Equal(got,items) {t.Fatalf("got %v, want %v",got,items)}
 }
}
`, StableIDs{})
}

func TestGeneratedYieldControlFlow(t *testing.T) {
	runGeneratedFixture(t, `
fn pickDirect(items: [Int]): Int? {
 val found = for item in items {
  if item <= 0 { continue }
  yield item
 }
 return found
}
fn pickNestedIf(items: [Int]): Int? {
 val found = for item in items {
  if item >= 0 { if item > 0 { yield item } }
 }
 return found
}
fn pickNestedLoop(items: [Int]): Int? {
 val found = for item in items {
  for candidate in [item] { if candidate > 0 { yield candidate } }
 }
 return found
}
fn pickWhen(items: [Int]): Int? {
 val found = for item in items {
  when {
   item > 0 -> yield item
   else -> null
  }
 }
 return found
}
fn positive(value: Int): Boolean { return value > 0 }
fn pickWhenDiscardedValue(items: [Int]): Int? {
 val found = for item in items { when { item > 0 -> yield item else -> item } }
 return found
}
fn pickWhenCall(items: [Int]): Int? {
 val found = for item in items { when { positive(item) -> yield item else -> null } }
 return found
}
fn pickWhenSubject(items: [Int]): Int? {
 val found = for item in items { when item { 7 -> yield item else -> null } }
 return found
}
fn pickWhenBlock(items: [Int]): Int? {
 val found = for item in items { when { item > 0 -> { val chosen = item yield chosen } else -> null } }
 return found
}
fn pickWhenNested(items: [Int]): Int? {
 val found = for item in items {
  when { item >= 0 -> when { item > 0 -> yield item else -> null } else -> null }
 }
 return found
}
fn pickWhenValue(items: [Int]): Int? {
 val found = for item in items {
  val fallback = when { item > 0 -> yield item else -> 0 }
  if fallback > 0 { yield fallback }
 }
 return found
}
fn pickWhenValueNullable(items: [Int]): Int? {
 val found = for item in items {
  val fallback = when { item > 0 -> yield item else -> null }
  if fallback != null { yield fallback }
 }
 return found
}
fn pickWhenValueBlock(items: [Int]): Int? {
 val found = for item in items {
  val fallback = when {
   item > 0 -> { val chosen = item yield chosen }
   else -> { val chosen = 0 chosen }
  }
  if fallback > 0 { yield fallback }
 }
 return found
}
api firstPositive(items: [Int]): Int? { return pickDirect(items) }
`, `package generated
import("context";"testing")
type App struct{}
func TestFirstPositive(t *testing.T) {
 app:=&App{}
 for name,call:=range map[string]func(context.Context,[]int64)(*int64,error){
  "direct":app.pickDirect,"nested-if":app.pickNestedIf,"nested-loop":app.pickNestedLoop,"when":app.pickWhen,
  "when-call":app.pickWhenCall,"when-subject":app.pickWhenSubject,"when-block":app.pickWhenBlock,"when-nested":app.pickWhenNested,
  "when-value":app.pickWhenValue,
  "when-discarded-value":app.pickWhenDiscardedValue,
  "when-value-nullable":app.pickWhenValueNullable,"when-value-block":app.pickWhenValueBlock,
 } {t.Run(name,func(t *testing.T){
  for _,items:=range [][]int64{nil,{}, {-2,0}, {-2,0,7,9}} {
   got,err:=call(t.Context(),items);if err!=nil{t.Fatal(err)}
   if len(items)==4 {if got==nil||*got!=7{t.Fatalf("first yield: %v",got)}} else if got!=nil {t.Fatalf("no yield: %d",*got)}
  }
 })}
}
`, StableIDs{})
}

func TestGeneratedLogMethodsPreserveReturnValues(t *testing.T) {
	runGeneratedFixture(t, `
fn logged(): String { "debug".d "info".i "warn".w return "error".e }
api loggedValue(): String { return logged() }
api inlineLog(): String { return "inline".d }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/luxolog")
type App struct{}
func TestGeneratedLogging(t *testing.T) {
 previous:=luxolog.GetLevel();defer func(){if err:=luxolog.SetLevel(previous);err!=nil{t.Error(err)}}()
 if err:=luxolog.SetLevel(luxolog.LevelError);err!=nil{t.Fatal(err)}
 got,err:=(&App{}).logged(t.Context());if err!=nil||got!="error"{t.Fatalf("%q %v",got,err)}
}
`, StableIDs{})
}

func TestWhenBindingPreservesAnalyzedType(t *testing.T) {
	for _, want := range []valType{
		{name: "Int"}, {name: "Int", isList: true},
		{name: "String", nullable: true}, {name: "User", isModel: true, isList: true},
	} {
		t.Run(goTypeForValType(want), func(t *testing.T) {
			c := newCompiler(map[string]*ast.ModelDecl{"User": {Name: "User"}})
			c.api.ReturnType = &ast.TypeRef{Name: "String"}
			expression := &ast.WhenExpr{Else: &ast.Ident{Name: "fallback"}}
			expression.SetTypeTag(want.name)
			expression.SetListType(want.isList)
			expression.SetNullable(want.nullable)
			c.compileVal(&ast.ValStmt{Name: "chosen", Value: expression})
			if got := c.vars["chosen"]; got != want {
				t.Fatalf("analyzed when type changed: got %+v, want %+v", got, want)
			}
		})
	}
}

func TestNarrowedReferencesKeepGoABI(t *testing.T) {
	c := newCompiler(map[string]*ast.ModelDecl{"User": {Name: "User"}})
	for _, name := range []string{"User", "Snapshot", "Bytes", "JSON"} {
		t.Run(name, func(t *testing.T) {
			identifier := &ast.Ident{Name: "value"}
			identifier.SetTypeTag(name)
			identifier.NarrowedTag = true
			c.vars["value"] = valType{name: name, nullable: true}
			want := "(*value)"
			if name == "User" {
				want = "value"
			}
			if got := c.compileExpr(identifier); got != want {
				t.Fatalf("reference ABI mismatch: got %s want %s", got, want)
			}
		})
	}
	list := &ast.Ident{Name: "values"}
	list.SetTypeTag("Boolean")
	list.SetListType(true)
	list.NarrowedTag = true
	c.vars["values"] = valType{name: "Boolean", nullable: true, isList: true}
	if got := c.compileExpr(list); got != "values" {
		t.Fatalf("nullable list changed ABI: %s", got)
	}
}

func TestLexicalBindingsRestorePaginationAndLocals(t *testing.T) {
	c := newCompiler(nil)
	c.vars["value"] = valType{name: "Boolean", nullable: true}
	c.paginationTotals = map[string]string{"value": "originalTotal"}
	original := c.saveBinding("value")
	c.vars["value"] = valType{name: "Int"}
	c.paginationTotals["value"] = "otherTotal"
	c.restoreBinding(original)
	if c.vars["value"] != original.value || c.paginationTotals["value"] != original.total {
		t.Fatal("outer binding was not restored")
	}
	block := &ast.Block{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		block.Stmts = append(block.Stmts, &ast.ValStmt{Name: name, Value: &ast.Literal{Kind: token.Int, Value: "1"}})
	}
	c.compileBlock(block)
	if len(c.vars) != 1 || c.vars["value"] != original.value {
		t.Fatal("block locals escaped their scope", c.vars)
	}
}

func TestGeneratedNullableBooleanGuards(t *testing.T) {
	runGeneratedFixture(t, `
type Snapshot { enabled: Boolean }
fn snapshotFlag(value: Snapshot): Boolean { return value.enabled }
fn bytesPresent(value: Bytes): Boolean { return true }
fn jsonPresent(value: JSON): Boolean { return true }
fn guardedSnapshot(value: Snapshot?): Boolean {
 if value != null { return snapshotFlag(value) }
 return false
}
fn guardedBytes(value: Bytes?): Boolean {
 if value != null { return bytesPresent(value) }
 return false
}
fn guardedJSON(value: JSON?): Boolean {
 if value != null { return jsonPresent(value) }
 return false
}
fn guarded(value: Boolean?): Boolean {
 if value != null { return !value }
 return false
}
api guardedIf(value: Boolean?): Boolean {
 if value != null { return !value }
 return false
}
api guardedFunction(value: Boolean?): Boolean { return guarded(value) }
api guardedAnd(value: Boolean?): Boolean { return value != null && !value }
api guardedOr(value: Boolean?): Boolean { return value == null || !value }
api guardedNot(value: Boolean?): Boolean { return !(value == null) && !value }
api guardedCopy(value: Boolean?): Boolean {
 if value != null { val copy = value return !copy }
 return false
}
api guardedShadow(value: Boolean?): Boolean {
 if value != null { val value = false if value { return true } }
 return value == null || !value
}
api guardedLoop(value: Boolean?): Boolean {
 if value != null { for value in [true, false] { if !value { return false } } }
 return false
}
api guardedMutation(value: Boolean?): Boolean {
 if value != null { value = null }
 return value == null
}
api guardedDestructure(value: Boolean?): Boolean {
 if value != null {
  val (value, other) = await { true false }
  return value && !other
 }
 return false
}
`, nullableBooleanGuardAssertions, StableIDs{ModelFields: map[string]map[string]int{"Snapshot": {"enabled": 1}}})
}

const nullableBooleanGuardAssertions = `package generated
import("bytes";"encoding/json";"fmt";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type App struct{}
func TestNarrowedValueArguments(t *testing.T){
 app:=&App{};snapshot:=Snapshot{Enabled:true};data:=[]byte{1};raw:=json.RawMessage("{}")
 for _,present:=range []bool{false,true}{
  var s *Snapshot;var d *[]byte;var r *json.RawMessage
  if present{s=&snapshot;d=&data;r=&raw}
  got,err:=app.guardedSnapshot(t.Context(),s);if err!=nil||got!=present{t.Fatal("snapshot",got,err)}
  got,err=app.guardedBytes(t.Context(),d);if err!=nil||got!=present{t.Fatal("bytes",got,err)}
  got,err=app.guardedJSON(t.Context(),r);if err!=nil||got!=present{t.Fatal("JSON",got,err)}
 }
}
func TestNullableBooleanGuards(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 for _,name:=range []string{"guardedIf","guardedFunction","guardedAnd","guardedOr","guardedNot","guardedCopy","guardedShadow","guardedLoop","guardedMutation","guardedDestructure"}{
  for _,value:=range []any{nil,false,true}{for _,binary:=range []bool{false,true}{
   t.Run(fmt.Sprintf("%s/%v/%v",name,value,binary),func(t *testing.T){
    text:="null";if value!=nil{text=fmt.Sprint(value)}
    body:=[]byte(fmt.Sprintf("{\"$api\":%q,\"value\":%s}",name,text))
    if binary{var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,map[string]any{"value":value},[]api.ParamMeta{{Name:"value",Type:"Boolean",FieldID:1,Nullable:true}});if err!=nil{t.Fatal(err)}}
    request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
    if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
    response:=httptest.NewRecorder();router.ServeHTTP(response,request)
    if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
    want:=value==false || value==nil&&(name=="guardedOr"||name=="guardedShadow")
    if name=="guardedLoop"{want=false}
    if name=="guardedMutation"{want=true}
    if name=="guardedDestructure"{want=value!=nil}
    if binary{decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadBool();if decoder.Finish()!=nil||got!=want{t.Fatalf("got=%v want=%v",got,want)}}else if !bytes.Contains(response.Body.Bytes(),[]byte(fmt.Sprintf("\"data\":%t",want))){t.Fatal(response.Body.String())}
   })
  }}
 }
}
`

func TestGeneratedLogicalShortCircuit(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn probe(value: Boolean): Result<Boolean> @native
fn checked(value: Boolean): Boolean { return probe(value)? }
api nativeAnd(enabled: Boolean): Boolean { return enabled && probe(true)? }
api nativeOr(enabled: Boolean): Boolean { return enabled || probe(false)? }
api localAnd(enabled: Boolean): Boolean { return enabled && checked(true) }
api localOr(enabled: Boolean): Boolean { return enabled || checked(false) }
api closure(enabled: Boolean): Boolean {
 return when {
  enabled -> false && checked(true)
  else -> true || checked(false)
 }
}
api grouped(enabled: Boolean): Boolean { return (enabled || true) && false }
api comparison(enabled: Boolean): Boolean { return enabled && (true == false) }
api nestedRight(enabled: Boolean): Boolean { return enabled && (checked(false) || checked(true)) }
api selectedClosure(enabled: Boolean): Boolean {
 return when {
  enabled -> enabled && checked(true)
  else -> false
 }
}
api leftFirst(enabled: Boolean): Boolean { return probe(enabled)? && probe(true)? }
`, logicalShortCircuitAssertions+logicalShortCircuitBenchmarks)
}

func TestGeneratedStringReceiverEvaluationOrder(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn text(stage: Int): Result<String> @native
fn checkedText(stage: Int): String { return text(stage)? }
api containsText(): Boolean { return checkedText(1).contains(checkedText(2)) }
api matchesText(): Boolean { return checkedText(1).matches(checkedText(2)) }
api replacesText(): Boolean { return checkedText(1).replace(checkedText(2), checkedText(3)) == "axc" }
api convertsText(): Boolean { return "123".toInt() == 123 && "1.25".toFloat() == 1.25 }
api templateText(): Boolean { return "${checkedText(1)}|${checkedText(2)}|${checkedText(3)}" == "abc|b|x" }
`, stringReceiverOrderAssertions)
}

const stringReceiverOrderAssertions = `package generated
import("bytes";"context";"errors";"fmt";"net/http/httptest";"slices";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type App struct{ Resolver NativeResolver }
type orderResolver struct{ calls []int64; failAt int64 }
func (r *orderResolver) Text(_ context.Context, stage int64)(string,error){
 r.calls=append(r.calls,stage)
 if stage==r.failAt{return "",errors.New("text failed")}
 return []string{"","abc","b","x"}[stage],nil
}
func TestStringReceiverOrder(t *testing.T){
 for _,sample:=range []struct{name string;count int64}{
  {"containsText",2},{"matchesText",2},{"replacesText",3},{"convertsText",0},{"templateText",3},
 }{for failAt:=int64(0);failAt<=sample.count;failAt++{for _,binary:=range []bool{false,true}{
  t.Run(fmt.Sprintf("%s/fail=%d/binary=%v",sample.name,failAt,binary),func(t *testing.T){
   router:=api.NewRouter();resolver:=&orderResolver{failAt:failAt}
   if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
   if err:=RegisterHandlers(router,&App{Resolver:resolver});err!=nil{t.Fatal(err)}
   body:=[]byte(fmt.Sprintf("{\"$api\":%q}",sample.name))
   if binary{var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[sample.name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
   if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request)
   count,status:=sample.count,200;if failAt>0{count,status=failAt,500}
   want:=[]int64{1,2,3}[:count]
   if response.Code!=status||!slices.Equal(resolver.calls,want){t.Fatalf("status=%d calls=%v want status=%d calls=%v body=%s",response.Code,resolver.calls,status,want,response.Body.String())}
   if status!=200{return}
   if binary{
    decoder:=codec.NewDecoder(response.Body.Bytes());value:=decoder.ReadBool()
    if err:=decoder.Finish();err!=nil||!value{t.Fatalf("value=%v err=%v",value,err)}
   }else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":true")){t.Fatalf("JSON: %s",response.Body.String())}
  })
 }}}
}
`

const logicalShortCircuitBenchmarks = `
func BenchmarkLogicalHandler(b *testing.B){
 for _,sample:=range []struct{name string;handler func(*App)api.HandlerFunc}{
  {"nativeAnd",handleNativeAnd},{"nativeOr",handleNativeOr},
  {"localAnd",handleLocalAnd},{"localOr",handleLocalOr},
 }{for _,enabled:=range []bool{false,true}{
  b.Run(fmt.Sprintf("%s/%v",sample.name,enabled),func(b *testing.B){
   resolver:=&logicalResolver{};handler:=sample.handler(&App{Resolver:resolver})
   request:=httptest.NewRequest("POST","/",bytes.NewBufferString(fmt.Sprintf("{\"$api\":%q,\"enabled\":%t}",sample.name,enabled)))
   req,err:=api.ParseRequest(request);if err!=nil{b.Fatal(err)};req.Buf=&api.ResponseBuf{B:make([]byte,0,16)}
   ctx:=context.Background();b.ReportAllocs();b.ResetTimer()
   for b.Loop(){req.Buf.B=req.Buf.B[:0];if err:=handler(ctx,req);err!=nil{b.Fatal(err)}}
   if len(req.Buf.B)!=1{b.Fatal("missing Boolean response")}
  })
 }}
}
`

const logicalShortCircuitAssertions = `package generated
import("bytes";"context";"errors";"fmt";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type App struct{ Resolver NativeResolver }
type logicalResolver struct{ calls int; fail bool }
func (r *logicalResolver) Probe(_ context.Context,value bool)(bool,error){
 r.calls++;if r.fail{return false,errors.New("probe failure")};return value,nil
}
func TestLogicalShortCircuitHTTP(t *testing.T){
 for _,name:=range []string{"nativeAnd","nativeOr","localAnd","localOr","closure","grouped","comparison","nestedRight","selectedClosure","leftFirst"}{
  for _,enabled:=range []bool{false,true}{for _,fail:=range []bool{false,true}{for _,binary:=range []bool{false,true}{
   t.Run(fmt.Sprintf("%s/enabled=%v/fail=%v/binary=%v",name,enabled,fail,binary),func(t *testing.T){
    testLogicalRequest(t,name,enabled,fail,binary)
   })
  }}}
 }
}
func testLogicalRequest(t *testing.T,name string,enabled,fail,binary bool){
 t.Helper();router:=api.NewRouter();resolver:=&logicalResolver{fail:fail}
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:resolver});err!=nil{t.Fatal(err)}
 want,calls:=enabled,0
 switch name {
 case "nativeAnd","localAnd","selectedClosure":if enabled{calls=1}
 case "nativeOr","localOr":if !enabled{calls=1}
 case "closure":want=!enabled
 case "grouped","comparison":want=false
 case "nestedRight":if enabled{calls=2;if fail{calls=1}}
 case "leftFirst":calls=1;if enabled&&!fail{calls=2}
 }
 body:=[]byte(fmt.Sprintf("{\"$api\":%q,\"enabled\":%t}",name,enabled))
 if binary{var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,map[string]any{"enabled":enabled},[]api.ParamMeta{{Name:"enabled",Type:"Boolean",FieldID:1}});if err!=nil{t.Fatal(err)}}
 request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
 if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request)
 status:=200;if fail&&calls>0{status=500}
 if response.Code!=status||resolver.calls!=calls{t.Fatalf("status=%d calls=%d want status=%d calls=%d body=%s",response.Code,resolver.calls,status,calls,response.Body.String())}
 if status!=200{return}
 if binary{
  decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadBool()
  if err:=decoder.Finish();err!=nil||got!=want{t.Fatalf("got=%v want=%v err=%v",got,want,err)}
 }else if !bytes.Contains(response.Body.Bytes(),[]byte(fmt.Sprintf("\"data\":%t",want))){t.Fatalf("JSON: %s",response.Body.String())}
}
`

func TestGeneratedWhenListReturn(t *testing.T) {
	runGeneratedTransactionFixture(t, `
api chooseNumbers(enabled: Boolean): [Int] {
 val chosen = when {
  enabled -> [1, 9007199254740993]
  else -> [-7]
 }
 return chosen
}
`, whenListReturnAssertions)
}

const whenListReturnAssertions = `package generated
import("bytes";"fmt";"net/http/httptest";"slices";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type App struct{}
func TestWhenListHTTP(t *testing.T) {
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 for _,enabled:=range []bool{false,true} {for _,binary:=range []bool{false,true} {
  t.Run(fmt.Sprintf("enabled=%v/binary=%v",enabled,binary),func(t *testing.T){
   want:=[]int64{-7};wantJSON:="[-7]"
   if enabled {want=[]int64{1,9007199254740993};wantJSON="[1,9007199254740993]"}
   body:=[]byte(fmt.Sprintf("{\"$api\":\"chooseNumbers\",\"enabled\":%t}",enabled))
   if binary {
    var err error
    body,err=api.EncodeBinaryRequest(router.Schema.APIs["chooseNumbers"].ID,nil,map[string]any{"enabled":enabled},[]api.ParamMeta{{Name:"enabled",Type:"Boolean",FieldID:1}})
    if err!=nil{t.Fatal(err)}
   }
   request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
   if binary {request.Header.Set("X-Luxo-Mode","binary")} else {request.Header.Set("Content-Type","application/json")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request)
   if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
   if binary {
    decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadIntArray()
    if err:=decoder.Finish();err!=nil||!slices.Equal(got,want){t.Fatalf("got=%v want=%v err=%v",got,want,err)}
   } else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":"+wantJSON)) {t.Fatalf("JSON result: %s",response.Body.String())}
  })
 }}
}
`

func TestCompiledExpressionClosuresExecute(t *testing.T) {
	call := func() ast.Expr { return &ast.CallExpr{Func: &ast.Ident{Name: "check"}} }
	check := &ast.FnDecl{Name: "check", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{}}
	loop := &ast.ForStmt{VarName: "i", Collection: &ast.RangeExpr{Start: &ast.Literal{Kind: token.Int, Value: "1"}, End: &ast.Literal{Kind: token.Int, Value: "2"}},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: call()}}}}
	loop.SetTypeTag("Int")
	loop.SetListType(true)
	branch := &ast.WhenExpr{Branches: []*ast.WhenBranch{{Condition: &ast.Ident{Name: "enabled"}, Body: call()}}, Else: &ast.Literal{Kind: token.Int, Value: "7"}}
	branch.SetTypeTag("Int")
	predicate := &ast.WhenExpr{Branches: []*ast.WhenBranch{
		{Condition: &ast.Ident{Name: "enabled"}, Body: &ast.Literal{Kind: token.Int, Value: "7"}},
		{Condition: &ast.CallExpr{Func: &ast.Ident{Name: "eligible"}}, Body: &ast.Literal{Kind: token.Int, Value: "9"}},
	}, Else: &ast.Literal{Kind: token.Int, Value: "11"}}
	predicate.SetTypeTag("Int")
	var generated strings.Builder
	for _, fn := range []*ast.FnDecl{
		{Name: "collect", ReturnType: &ast.TypeRef{Name: "Int", IsList: true}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: loop}}}},
		{Name: "choose", Params: []*ast.ParamDecl{{Name: "enabled", Type: &ast.TypeRef{Name: "Boolean"}}}, ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: branch}}}},
		{Name: "choosePredicate", Params: []*ast.ParamDecl{{Name: "enabled", Type: &ast.TypeRef{Name: "Boolean"}}}, ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: predicate}}}},
	} {
		defaultGenerator().compileLocalFunction(&generated, fn, nil, nil, nil, map[string]*ast.FnDecl{"check": check,
			"eligible": {Name: "eligible", ReturnType: &ast.TypeRef{Name: "Boolean"}, Body: &ast.Block{}},
		}, nil)
	}
	generated.WriteString(compiledNativeSelect())
	runGeneratedClosureTest(t, generated.String(), `
func TestRun(t *testing.T) {
 app := &App{}
 app.Resolver = &resolver{app: app}
 ch := make(chan int64, 1)
 ch <- 5
 if value, err := app.pick(context.Background(), ch); err != nil || value != 3 { t.Fatalf("native select: %v %v", value, err) }
 if value, err := app.pick(context.Background(), ch); err != nil || value != 0 { t.Fatalf("select default: %v %v", value, err) }
 app.calls = 0
 values, err := app.collect(context.Background())
 if err != nil || len(values) != 2 || app.calls != 2 { t.Fatalf("collect: %v %v calls=%d", values, err, app.calls) }
 app.calls = 0
 value, err := app.choose(context.Background(), false)
 if err != nil || value != 7 || app.calls != 0 { t.Fatalf("inactive branch ran: %v %v calls=%d", value, err, app.calls) }
 if allocations := testing.AllocsPerRun(100, func() { _, _ = app.choose(context.Background(), true) }); allocations != 0 { t.Fatalf("value closure allocated: %v", allocations) }
 app.fail = true
 app.calls = 0
 if value, err := app.choosePredicate(context.Background(), true); value != 7 || err != nil || app.calls != 0 { t.Fatalf("inactive predicate ran: %v %v", value, err) }
 if _, err := app.choosePredicate(context.Background(), false); err != sentinel { t.Fatalf("predicate error: %v", err) }
 if _, err := app.collect(context.Background()); err != sentinel { t.Fatalf("collect error: %v", err) }
 if _, err := app.choose(context.Background(), true); err != sentinel { t.Fatalf("branch error: %v", err) }
 ch <- 5
 if _, err := app.pick(context.Background(), ch); err != sentinel { t.Fatalf("native select error: %v", err) }
}
`)
}

func compiledNativeSelect() string {
	c := newCompiler(nil)
	c.inFunction, c.functionResult = true, &ast.TypeRef{Name: "Int"}
	c.nativeFunctions = map[string]bool{"nativeCheck": true}
	branch := &ast.WhenExpr{Branches: []*ast.WhenBranch{{
		Condition: &ast.UnaryExpr{Op: "<-", Value: &ast.Ident{Name: "ch"}},
		Body: &ast.LambdaExpr{Params: []string{"message"}, Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.AssignStmt{Target: &ast.Ident{Name: "_"}, Op: "=", Value: &ast.Ident{Name: "message"}},
			&ast.ReturnStmt{Value: &ast.UnaryExpr{Op: "?", Value: &ast.CallExpr{Func: &ast.Ident{Name: "nativeCheck"}}}},
		}}},
	}}, Else: &ast.Literal{Kind: token.Int, Value: "0"}}
	branch.SetTypeTag("Int")
	c.compileReturn(&ast.ReturnStmt{Value: branch})
	return "func (app *App) pick(ctx context.Context, ch <-chan int64) (_value int64, err error) {\n" + compilerOut(c) + "}\n"
}

func runGeneratedClosureTest(t *testing.T, generated, assertions string) {
	t.Helper()
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "closure-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	source := `package closuretest
import ("context"; "errors"; "testing")
var sentinel = errors.New("expected failure")
type App struct { calls int; fail bool; Resolver *resolver; entered chan struct{}; release chan struct{} }
type resolver struct { app *App }
func (r *resolver) NativeCheck(ctx context.Context) (int64, error) { return r.app.check(ctx) }
func (app *App) check(ctx context.Context) (int64, error) { app.calls++; if app.fail { return 0, sentinel }; return 3, nil }
func (app *App) eligible(ctx context.Context) (bool, error) { app.calls++; if app.fail { return false, sentinel }; return true, nil }
` + generated + assertions
	if strings.Contains(generated, "errgroup.") {
		source = strings.Replace(source, `import (`, `import ("golang.org/x/sync/errgroup";`, 1)
	}
	if strings.Contains(assertions, "time.") {
		source = strings.Replace(source, `import (`, `import ("time";`, 1)
	}
	if err := os.WriteFile(filepath.Join(dir, "closure_test.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-timeout=30s", "-count=1", ".")
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated execution: %v\n%s\n%s", err, output, source)
	}
}

func TestAwaitFunctionCallsRunInsideParallelTasks(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.Ident{Name: "waitCheck"}}
	call.SetTypeTag("Int")
	one, two := &ast.Literal{Kind: token.Int, Value: "1"}, &ast.Literal{Kind: token.Int, Value: "2"}
	one.SetTypeTag("Int")
	two.SetTypeTag("Int")
	fn := &ast.FnDecl{Name: "parallel", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Names: []string{"a", "b"}, Value: &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: call}, &ast.ExprStmt{Expr: call}}}}},
		&ast.ValStmt{Names: []string{"c", "d"}, Value: &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: one}, &ast.ExprStmt{Expr: two}}}}},
		&ast.ReturnStmt{Value: &ast.BinaryExpr{Left: &ast.Ident{Name: "a"}, Op: "+", Right: &ast.Ident{Name: "b"}}},
	}}}
	var body strings.Builder
	functions := map[string]*ast.FnDecl{"waitCheck": {Name: "waitCheck", Body: &ast.Block{}, ReturnType: &ast.TypeRef{Name: "Int"}}}
	defaultGenerator().compileLocalFunction(&body, fn, nil, nil, nil, functions, nil)
	failure := &ast.CallExpr{Func: &ast.Ident{Name: "failCheck"}}
	failure.SetTypeTag("Int")
	functions["failCheck"] = &ast.FnDecl{Name: "failCheck", Body: &ast.Block{}, ReturnType: &ast.TypeRef{Name: "Int"}}
	fn.Name = "parallelFailure"
	fn.Body.Stmts[0].(*ast.ValStmt).Value.(*ast.AwaitExpr).Body.Stmts[1] = &ast.ExprStmt{Expr: failure}
	defaultGenerator().compileLocalFunction(&body, fn, nil, nil, nil, functions, nil)
	if strings.Contains(body.String(), " any") {
		t.Fatalf("typed await lost its value types:\n%s", body.String())
	}
	runGeneratedClosureTest(t, body.String(), `
func (app *App) waitCheck(ctx context.Context) (int64, error) {
 app.entered <- struct{}{}
 select { case <-app.release: return 3, nil; case <-ctx.Done(): return 0, ctx.Err() }
}
func (app *App) failCheck(ctx context.Context) (int64, error) {
 select { case <-app.entered: return 0, sentinel; case <-ctx.Done(): return 0, ctx.Err() }
}
func TestAwait(t *testing.T) {
 ctx, cancel := context.WithCancel(context.Background())
 defer cancel()
 app := &App{entered: make(chan struct{}, 2), release: make(chan struct{})}
 done := make(chan error, 1)
 go func() { value, err := app.parallel(ctx); if err == nil && value != 6 { err = errors.New("incorrect result") }; done <- err }()
 timer := time.NewTimer(time.Second)
 defer timer.Stop()
 for range 2 { select { case <-app.entered: case <-timer.C: t.Fatal("await serialized function calls") } }
 close(app.release)
 if err := <-done; err != nil { t.Fatal(err) }
}
func TestAwaitFailureCancelsSibling(t *testing.T) {
 ctx, cancel := context.WithTimeout(context.Background(), time.Second)
 defer cancel()
 app := &App{entered: make(chan struct{}, 1), release: make(chan struct{})}
 if _, err := app.parallelFailure(ctx); err != sentinel { t.Fatalf("lost task error: %v", err) }
 if ctx.Err() != nil { t.Fatal("sibling waited for the parent deadline instead of group cancellation") }
}
`)
}

func TestCompiledReturnBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		result *ast.TypeRef
		value  ast.Expr
		want   string
	}{
		{"void", nil, nil, "return nil"},
		{"list", &ast.TypeRef{Name: "Int", IsList: true}, &ast.ListExpr{Items: []ast.Expr{&ast.Literal{Kind: token.Int, Value: "1"}}}, "return []int64{1}, nil"},
		{"model", &ast.TypeRef{Name: "User"}, &ast.ObjectExpr{TypeName: "User"}, "return &User{}, nil"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newCompiler(makeModels("User"))
			c.inFunction, c.functionResult = true, test.result
			c.compileReturn(&ast.ReturnStmt{Value: test.value})
			if !strings.Contains(compilerOut(c), test.want) {
				t.Fatalf("return: %s", compilerOut(c))
			}
		})
	}
	c := newCompiler(nil)
	c.inFunction, c.functionResult = true, &ast.TypeRef{Name: "Int"}
	async := c.compileAsync(&ast.AsyncExpr{Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ThrowStmt{Error: &ast.Ident{Name: "sentinel"}}, &ast.ReturnStmt{},
	}}})
	if !strings.Contains(async, "luxolog.Error((sentinel).Error())") || strings.Contains(async, "return _value") || strings.Contains(async, "return nil") {
		t.Fatalf("async ABI: %s", async)
	}
	transaction := c.compileTransaction(&ast.TransactionExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ThrowStmt{Error: &ast.Ident{Name: "sentinel"}}}}})
	if !strings.Contains(transaction, "return sentinel") || strings.Contains(transaction, "return _value") {
		t.Fatalf("transaction ABI: %s", transaction)
	}
	sub := c.valueCompiler()
	sub.compileReturn(&ast.ReturnStmt{})
	if !strings.Contains(sub.b.String(), "return nil") {
		t.Fatalf("nullable closure ABI: %s", sub.b.String())
	}
}

func TestFallibleWhenSubjectWithoutElse(t *testing.T) {
	c := newCompiler(nil)
	c.functions = map[string]*ast.FnDecl{"check": {Name: "check", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{}}}
	expression := &ast.WhenExpr{Subject: &ast.Ident{Name: "value"}, Branches: []*ast.WhenBranch{{
		Condition: &ast.CallExpr{Func: &ast.Ident{Name: "check"}}, Body: &ast.Literal{Kind: token.Int, Value: "1"},
	}}}
	expression.SetTypeTag("Int")
	c.compileWhen(expression)
	code := compilerOut(c)
	for _, want := range []string{"_subject := value", "if _subject == _result", "return 0", "return _closureErr"} {
		if !strings.Contains(code, want) {
			t.Fatalf("missing %q: %s", want, code)
		}
	}
	if got := c.compileLiteral(&ast.Literal{Kind: token.Duration, Value: "broken"}); got != "broken" {
		t.Fatalf("invalid duration recovery = %q", got)
	}
}

func TestFormalValueBlocksExecute(t *testing.T) {
	const source = `
fn chooseBlock(flag: Boolean): Int {
 return when { flag -> { val value = 1 value } else -> { return 2 val unreachable = 3 } }
}
fn mapBlock(items: [Int]): [Int] {
 return for item in items { val value = item value + 1 }
}
fn sumStatements(items: [Int]): Int {
 var total = 0
 for item in items { total += item }
 return total
}
`
	runGeneratedFixture(t, source, `package generated
import("testing";"slices")
type App struct{}
func TestValueBlocks(t *testing.T) {
 app:=&App{}
 for _, flag:= range []bool{false,true} {
  value,err:=app.chooseBlock(t.Context(),flag)
  want:=int64(2);if flag {want=1}
  if err!=nil || value!=want {t.Fatal(value,err)}
 }
 values,err:=app.mapBlock(t.Context(),[]int64{1,2})
 if err!=nil || !slices.Equal(values,[]int64{2,3}) {t.Fatal(values,err)}
 total,err:=app.sumStatements(t.Context(),[]int64{1,2})
 if err!=nil || total!=3 {t.Fatal(total,err)}
}
`, StableIDs{})
}

func TestFormalGenerationRejectsMissingValueResults(t *testing.T) {
	for _, source := range []string{
		`fn broken(flag: Boolean): Int { return when { flag -> 1 else -> { val value = 2 } } }`,
		`fn broken(items: [Int]): [Int] { return for item in items { val value = item } }`,
	} {
		tokens, lexErrors := lexer.New(source, "missing-result.luxo").Tokenize()
		if len(lexErrors) != 0 {
			t.Fatal(lexErrors)
		}
		file, parseErrors := parser.New(tokens).Parse("missing-result.luxo")
		if len(parseErrors) != 0 {
			t.Fatal(parseErrors)
		}
		result := semantic.Analyze([]*ast.File{file})
		generated, err := Generate(result, "generated", GeneratorConfig{IDs: completeFixtureAPIIDs(result, StableIDs{})})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "must produce a value") {
			t.Fatalf("missing value: generated=%v error=%v", generated != nil, err)
		}
	}
}
