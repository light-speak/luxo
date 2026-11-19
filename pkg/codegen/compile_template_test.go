package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestCompileTemplateStaticKinds(t *testing.T) {
	for _, name := range []string{"Int", "Float", "Boolean", "String", "DateTime", "Duration", "UUID", "Decimal", "Status"} {
		t.Run(name, func(t *testing.T) {
			c := newCompiler(nil)
			c.enums = map[string]bool{"Status": true}
			value := &ast.Ident{Name: "value"}
			value.SetTypeTag(name)
			got := c.compileTemplate(&ast.TemplateString{Parts: []ast.Expr{value}})
			if strings.Contains(got, "fmt.") || strings.Contains(got, "json.") {
				t.Fatalf("template used dynamic formatting: %s", got)
			}
			if !strings.Contains(got, "value") {
				t.Fatalf("template lost its input: %s", got)
			}
		})
	}
}

func TestGeneratedStaticTemplateInterpolation(t *testing.T) {
	runGeneratedFixture(t, `
enum Status { READY }
type Snapshot { text: String count: Int }
fn renderScalars(i: Int, f: Float, b: Boolean, s: String, state: Status): String {
 return "${i}|${f}|${b}|${s}|${state}"
}

fn renderSpecial(d: DateTime, t: Duration, id: UUID, amount: Decimal): String {
 return "${d}|${t}|${id}|${amount}"
}
fn renderNullable(value: String?): String { return "value=${value ?: "missing"}" }
fn renderNarrowed(value: Int?): String {
 if value != null { return "value=${value}" }
 return "missing"
}
fn renderMembers(value: Snapshot): String { return "${value.text}|${value.count}" }
fn renderCalls(value: String): String { return "${value.contains("yes")}|${value.length}|${value.trim()}" }
fn failTemplate(): Int { throw error.NotFound }
fn renderFailure(): String { return "value=${failTemplate()}" }
fn fallbackTemplate(): String { throw error.NotFound }
fn renderLazy(value: String?): String { return "${value ?: fallbackTemplate()}" }
fn defaultNullable(value: String?, fallback: String?): String? { return value ?: fallback }
fn defaultList(value: [Int]?): [Int] { return value ?: [42] }
fn defaultFloat(value: Float?, fallback: Float): Float { return value ?: fallback }
fn literalDefault(): String { return null ?: "literal" }
fn unnecessaryDefault(value: String): String { return value ?: fallbackTemplate() }
fn renderInt(value: Int): String { return "value=${value}" }
fn renderBool(value: Boolean): String { return "value=${value}" }
fn renderText(value: String): String { return "value=${value}" }
api render(value: String): String { return renderCalls(value) }
`, staticTemplateAssertions, StableIDs{ModelFields: map[string]map[string]int{"Snapshot": {"text": 1, "count": 2}}})
}

func TestGeneratedTemplateCaptureIsolation(t *testing.T) {
	runGeneratedFixture(t, `
fn renderCaptured(_templatePart0: Int, _templateNumber0: Int, _sb: String): String {
 return "${_templatePart0}|${_templateNumber0}|${_sb}"
}
fn renderNestedCollection(values: [Int]): String {
 return "items=${values.map { _templatePart0 -> "v=${_templatePart0}" }.joinToString("|")}"
}
fn renderLocalCapture(value: Int): String {
 val _templateNumber1 = value
 val _templatePart1 = "kept"
 return "${_templateNumber1}:${_templatePart1}"
}
`, `package generated
import "testing"

func TestCaptureIsolation(t *testing.T) {
 app:=&App{}; ctx:=t.Context()
 if value,err:=app.renderCaptured(ctx,3,4,"text");err!=nil||value!="3|4|text"{t.Fatal(value,err)}
 if value,err:=app.renderNestedCollection(ctx,[]int64{3,-4});err!=nil||value!="items=v=3|v=-4"{t.Fatal(value,err)}
 if value,err:=app.renderNestedCollection(ctx,nil);err!=nil||value!="items="{t.Fatal(value,err)}
 if value,err:=app.renderLocalCapture(ctx,42);err!=nil||value!="42:kept"{t.Fatal(value,err)}
}
`, StableIDs{})
}

const staticTemplateAssertions = `package generated
import("math";"testing";"time";"bytes";"net/http/httptest";"context";"fmt";"strconv";"strings";"os";"slices"
"github.com/google/uuid";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux/api")

func TestTemplateValues(t *testing.T){
 app:=&App{};ctx:=t.Context()
 got,err:=app.renderScalars(ctx,math.MinInt64,math.Copysign(0,-1),true,"中文",StatusREADY)
 if err!=nil||got!="-9223372036854775808|-0|true|中文|READY"{t.Fatal(got,err)}
 date:=time.Date(2026,9,25,12,34,56,123456789,time.FixedZone("test",9*3600))
 duration:=time.Duration(-1234567890);id:=uuid.MustParse("531f2d00-20e5-47f2-b874-22b6fc76328b");amount:=decimal.RequireFromString("12345678901234567890.123456789")
 got,err=app.renderSpecial(ctx,date,duration,id,amount)
 want:=date.String()+"|"+duration.String()+"|"+id.String()+"|"+amount.String()
 if err!=nil||got!=want{t.Fatal(got,want,err)}
 for _,value:=range []*string{nil,new("present")}{
  got,err=app.renderNullable(ctx,value);want="value=missing";if value!=nil{want="value=present"}
  if err!=nil||got!=want{t.Fatal(got,want,err)}
 }
 for _,value:=range []*int64{nil,new(int64(42))}{
  got,err=app.renderNarrowed(ctx,value);want="missing";if value!=nil{want="value=42"}
  if err!=nil||got!=want{t.Fatal(got,want,err)}
 }
 got,err=app.renderMembers(ctx,Snapshot{Text:"ok",Count:42});if err!=nil||got!="ok|42"{t.Fatal(got,err)}
 got,err=app.renderCalls(ctx," yes ");if err!=nil||got!="true|5|yes"{t.Fatal(got,err)}
 if _,err=app.renderFailure(ctx);err==nil{t.Fatal("template swallowed function failure")}
 got,err=app.renderLazy(ctx,new("present"));if err!=nil||got!="present"{t.Fatal("eager fallback",got,err)}
 if _,err=app.renderLazy(ctx,nil);err==nil{t.Fatal("fallback swallowed failure")}
 got,err=app.unnecessaryDefault(ctx,"present");if err!=nil||got!="present"{t.Fatal(got,err)}
 got,err=app.literalDefault(ctx);if err!=nil||got!="literal"{t.Fatal(got,err)}
 for _,present:=range []bool{false,true}{
  var value *string;var list []int64;var number *float64
  if present{value=new("present");list=[]int64{7};number=new(3.5)}
  actual,err:=app.defaultNullable(ctx,value,nil);if err!=nil||actual!=value{t.Fatal(actual,err)}
  items,err:=app.defaultList(ctx,list);want:=int64(42);if present{want=7};if err!=nil||len(items)!=1||items[0]!=want{t.Fatal(items,err)}
  result,err:=app.defaultFloat(ctx,number,42);expected:=42.0;if present{expected=3.5};if err!=nil||result!=expected{t.Fatal(result,err)}
 }
}
func TestTemplateHTTP(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 request:=httptest.NewRequest("POST","/",bytes.NewBufferString("{\"$api\":\"render\",\"value\":\" yes \"}"));request.Header.Set("Content-Type","application/json")
 response:=httptest.NewRecorder();router.ServeHTTP(response,request)
 if response.Code!=200||response.Body.String()!="{\"data\":\"true|5|yes\"}"{t.Fatal(response.Code,response.Body.String())}
}

// These controls reproduce the old generated bodies for valid scalar templates.
// They are not controls for the previously broken nullable/method cases.
func(app *App)oldTemplateInt(_ context.Context,value int64)(_value string,err error){
 return func()string{var s strings.Builder;s.WriteString("value=");s.WriteString(strconv.FormatInt(int64(value),10));return s.String()}(),nil
}
func(app *App)oldTemplateBool(_ context.Context,value bool)(_value string,err error){
 return func()string{var s strings.Builder;s.WriteString("value=");fmt.Fprintf(&s,"%v",value);return s.String()}(),nil
}
func(app *App)oldTemplateText(_ context.Context,value string)(_value string,err error){
 return func()string{var s strings.Builder;s.WriteString("value=");s.WriteString(value);return s.String()}(),nil
}
func(app *App)oldTemplateMixed(_ context.Context,i int64,f float64,b bool,text string,state Status)(_value string,err error){
 return func()string{var s strings.Builder;s.WriteString(strconv.FormatInt(int64(i),10));s.WriteString("|");fmt.Fprintf(&s,"%v",f);s.WriteString("|");fmt.Fprintf(&s,"%v",b);s.WriteString("|");s.WriteString(text);s.WriteString("|");s.WriteString(string(state));return s.String()}(),nil
}
func BenchmarkStaticTemplate(b *testing.B){
 app:=&App{};ctx:=context.Background()
 cases:=[]struct{name string;before,after func()(string,error)}{
  {"smallInt",func()(string,error){return app.oldTemplateInt(ctx,42)},func()(string,error){return app.renderInt(ctx,42)}},
  {"minInt",func()(string,error){return app.oldTemplateInt(ctx,math.MinInt64)},func()(string,error){return app.renderInt(ctx,math.MinInt64)}},
  {"bool",func()(string,error){return app.oldTemplateBool(ctx,true)},func()(string,error){return app.renderBool(ctx,true)}},
  {"text",func()(string,error){return app.oldTemplateText(ctx,"中文")},func()(string,error){return app.renderText(ctx,"中文")}},
  {"mixed",func()(string,error){return app.oldTemplateMixed(ctx,math.MinInt64,1.25,true,"中文",StatusREADY)},func()(string,error){return app.renderScalars(ctx,math.MinInt64,1.25,true,"中文",StatusREADY)}},
 }
 for _,tc:=range cases{b.Run(tc.name,func(b *testing.B){
  want,err:=tc.before();got,actualErr:=tc.after();if err!=nil||actualErr!=nil||got!=want{b.Fatal(got,want,err,actualErr)}
  variants:=[]struct{name string;run func()(string,error)}{{"before",tc.before},{"after",tc.after}}
  if os.Getenv("LUXO_TEMPLATE_AFTER_FIRST")=="1"{slices.Reverse(variants)}
  for _,variant:=range variants{b.Run(variant.name,func(b *testing.B){
   b.ReportAllocs();for b.Loop(){value,err:=variant.run();if err!=nil||value!=want{b.Fatal(value,err)}}
  })}
 })}
}
`

func TestGeneratedTemplateComposition(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn advance(counter: Counter): Int { counter.value += 1 return counter.value }
fn composed(counter: Counter): String {
 val first = "${counter.value}:${advance(counter)}:${counter.value}"
 val second = "${advance(counter)}:${"nested=${advance(counter)}"}:${counter.value}"
 return "${first}|${second}"
}
`, `package generated
import "testing"
func TestComposition(t *testing.T){
 counter:=&Counter{Value:1};app:=&App{}
 got,err:=app.composed(t.Context(),counter)
 if err!=nil||got!="1:2:2|3:nested=4:4"||counter.Value!=4{t.Fatal(got,err,counter.Value)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedRepeatedRandomHex(t *testing.T) {
	runGeneratedFixture(t, `
fn randomPair(): String {
 val first = crypto.randomHex(4)
 val second = crypto.randomHex(4)
 return first + second
}
fn randomFailure(): String {
 val first = crypto.randomHex(4)
 val invalid = crypto.randomHex(0)
 return first + invalid
}
`, `package generated
import("encoding/hex";"testing")

func TestRandomPair(t *testing.T){
 value,err:=(&App{}).randomPair(t.Context());if err!=nil{t.Fatal(err)}
 if len(value)!=16{t.Fatal(len(value))};if _,err:=hex.DecodeString(value);err!=nil{t.Fatal(err)}
 if _,err:=(&App{}).randomFailure(t.Context());err==nil{t.Fatal("random error swallowed")}
}
`, StableIDs{})
}

func TestGeneratedRandomInputAndErrors(t *testing.T) {
	runGeneratedFixture(t, `
fn sizedHex(size: Int): String { return crypto.randomHex(size) }
fn sizedBytes(size: Int): Bytes { return crypto.randomBytes(size) }
fn literalBytes(): Bytes { val first = crypto.randomBytes(4) return first }
fn randomLength(n: Int): Int { val value = crypto.randomHex(n: n) return value.length }
fn defaultRandom(): String { return crypto.randomHex() }
`, `package generated
import "testing"

func TestRandomInputs(t *testing.T){
 app:=&App{}
 value,err:=app.sizedHex(t.Context(),4);if err!=nil||len(value)!=8{t.Fatal(value,err)}
 bytes,err:=app.sizedBytes(t.Context(),4);if err!=nil||len(bytes)!=4{t.Fatal(bytes,err)}
 bytes,err=app.literalBytes(t.Context());if err!=nil||len(bytes)!=4{t.Fatal(bytes,err)}
 length,err:=app.randomLength(t.Context(),4);if err!=nil||length!=8{t.Fatal(length,err)}
 value,err=app.defaultRandom(t.Context());if err!=nil||len(value)!=64{t.Fatal(value,err)}
 for _,size:=range []int64{0,-1}{
  if _,err:=app.sizedHex(t.Context(),size);err==nil{t.Fatal("invalid hex length accepted",size)}
  if _,err:=app.sizedBytes(t.Context(),size);err==nil{t.Fatal("invalid bytes length accepted",size)}
 }
}
`, StableIDs{})
}

func TestGeneratedUserFunctionNamedNow(t *testing.T) {
	runGeneratedFixture(t, `
fn now(value: Int): Int { return value + 1 }
fn caller(): Int { return now(41) }
`, `package generated
import "testing"

func TestUserNow(t *testing.T){value,err:=(&App{}).caller(t.Context());if err!=nil||value!=42{t.Fatal(value,err)}}
`, StableIDs{})
}

func TestGeneratedNativeFunctionNamedNow(t *testing.T) {
	runGeneratedFixture(t, `
fn now(value: Int): Result<Int> @native
fn caller(): Int { return now(41)? }
`, `package generated
import("context";"errors";"testing")
type namedNowResolver struct{failure error}
func(r *namedNowResolver)Now(_ context.Context,value int64)(int64,error){return value+1,r.failure}
func TestNativeNow(t *testing.T){
 resolver:=&namedNowResolver{};app:=&App{Resolver:resolver}
 value,err:=app.caller(t.Context());if err!=nil||value!=42{t.Fatal(value,err)}
 resolver.failure=errors.New("native now failure")
 if _,err:=app.caller(t.Context());!errors.Is(err,resolver.failure){t.Fatal(err)}
}
`, StableIDs{})
}

func TestGeneratedListenerRandomCalls(t *testing.T) {
	runGeneratedFixture(t, `
event Requested(size: Int)
event Generated(text: String, bytes: Bytes)
on Requested {
 emit Generated(text: crypto.randomHex(it.size), bytes: crypto.randomBytes(it.size))
}
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event")
type randomBus struct{event.Bus;handler event.Handler;got GeneratedEvent;calls int}
func(b *randomBus)OnQueue(_ string,_ string,h event.Handler)error{b.handler=h;return nil}
func(b *randomBus)Emit(_ context.Context,_ string,payload any)error{b.got=*payload.(*GeneratedEvent);b.calls++;return nil}
func TestListenerRandom(t *testing.T){
 bus:=&randomBus{};if err:=RegisterEvents(bus,&App{EventBus:bus});err!=nil{t.Fatal(err)}
 if err:=bus.handler(t.Context(),RequestedEvent{Size:4});err!=nil{t.Fatal(err)}
 if len(bus.got.Text)!=8||len(bus.got.Bytes)!=4||bus.calls!=1{t.Fatal(bus.got,bus.calls)}
 if err:=bus.handler(t.Context(),RequestedEvent{Size:0});err==nil{t.Fatal("invalid length accepted")}
 if bus.calls!=1{t.Fatal("emitted after failure")}
}
`, StableIDs{EventFields: map[string]map[string]int{"Requested": {"size": 1}, "Generated": {"text": 1, "bytes": 2}}})
}

func TestGeneratedListenerDatabaseAndLog(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id name: String }
event Requested(id: Int)
event Counted(value: Int)
on Requested {
 val lower = it.id
 val total = User.where { it.id > lower || it.id < 0 }.count()
 "count=${total}".d
 emit Counted(value: total)
}
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
type queryListenerBus struct{event.Bus;handler event.Handler;count int64;calls int}
func(b *queryListenerBus)OnQueue(_ string,_ string,h event.Handler)error{b.handler=h;return nil}
func(b *queryListenerBus)Emit(_ context.Context,_ string,payload any)error{b.count=payload.(*CountedEvent).Value;b.calls++;return nil}
func TestQueryExecution(t *testing.T){
 ctx:=t.Context()
 container,err:=postgres.Run(ctx,testutil.PostgresImage(),postgres.WithDatabase("listener_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(ctx,"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name text NOT NULL); INSERT INTO users VALUES(-1,'negative'),(1,'equal'),(2,'two'),(3,'three')");err!=nil{t.Fatal(err)}
 bus:=&queryListenerBus{};app:=NewFromDB(db);app.EventBus=bus
 if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 raw,err:=(&RequestedEvent{Id:1}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 for _,payload:=range []any{RequestedEvent{Id:1},raw}{
  if err:=bus.handler(ctx,payload);err!=nil{t.Fatal(err)}
  if bus.count!=3{t.Fatal(bus.count)}
 }
 if bus.calls!=2{t.Fatal(bus.calls)}
 if _,err:=pg.Exec(ctx,db,"DROP TABLE users");err!=nil{t.Fatal(err)}
 if err:=bus.handler(ctx,RequestedEvent{Id:1});err==nil{t.Fatal("database failure swallowed")}
 if bus.calls!=2{t.Fatal("emitted after database failure")}
}

`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}}, EventFields: map[string]map[string]int{"Requested": {"id": 1}, "Counted": {"value": 1}}})
}
