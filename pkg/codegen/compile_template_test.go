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

const staticTemplateAssertions = `package generated
import("math";"testing";"time";"bytes";"net/http/httptest";"context";"fmt";"strconv";"strings";"os";"slices"
"github.com/google/uuid";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux/api")
type App struct{}
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
