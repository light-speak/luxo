package codegen

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// This cold control includes a shared type catalog but no log expressions.
func BenchmarkDebugTypeCatalog(b *testing.B) {
	var source strings.Builder
	fields := make(map[string]map[string]int)
	for index := range 16 {
		name := "DebugInput" + strconv.Itoa(index)
		source.WriteString("type " + name + " { id: Int text: String enabled: Boolean value: Float }\n")
		fields[name] = map[string]int{"id": 1, "text": 2, "enabled": 3, "value": 4}
	}
	source.WriteString("fn identity(value: DebugInput0): DebugInput0 { return value }")
	input := analyzePermissionFixture(b, source.String())
	config := GeneratorConfig{IDs: StableIDs{ModelFields: fields}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Generate(input, "generated", config); err != nil {
			b.Fatal(err)
		}
	}
}

func TestGeneratedDebugChains(t *testing.T) {
	runGeneratedFixture(t, debugChainSource, debugChainAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"LogNode":  {"id": 1, "name": 2, "password": 3, "next": 4, "logNodeId": 5},
		"LogBox":   {"name": 1, "nodes": 2, "amount": 3},
		"LogCycle": {"next": 1},
	}})
}

func TestDebugGeneratedFormattingIsStatic(t *testing.T) {
	input := analyzePermissionFixture(t, debugChainSource)
	ids := StableIDs{ModelFields: map[string]map[string]int{
		"LogNode": {"id": 1, "name": 2, "password": 3, "next": 4, "logNodeId": 5},
		"LogBox":  {"name": 1, "nodes": 2, "amount": 3}, "LogCycle": {"next": 1},
	}}
	generated, err := Generate(input, "generated", GeneratorConfig{IDs: completeFixtureAPIIDs(input, ids)})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	helpers := 0
	for _, name := range []string{"handler.gen.go", "debug.gen.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), name, generated.Files[name], 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*goast.FuncDecl)
			if !ok || !(strings.HasPrefix(fn.Name.Name, "log") || fn.Name.Name == "luxoAppendDebug") {
				continue
			}
			count++
			if fn.Name.Name == "luxoAppendDebug" {
				helpers++
			}
			goast.Inspect(fn.Body, func(node goast.Node) bool {
				if name, ok := node.(*goast.Ident); ok && name.Name == "any" {
					t.Error("dynamic value in generated log", fn.Name.Name)
				}
				if call, ok := node.(*goast.CallExpr); ok {
					if selector, ok := call.Fun.(*goast.SelectorExpr); ok {
						if name, ok := selector.X.(*goast.Ident); ok && (name.Name == "fmt" || name.Name == "reflect" || name.Name == "json") {
							t.Error("dynamic formatting in generated log", fn.Name.Name)
						}
					}
				}
				return true
			})
		}
	}
	if count < 20 || helpers != 3 {
		t.Fatal("missing actual generated log bodies", count, helpers)
	}
}

func TestGeneratedEventDebugChains(t *testing.T) {
	runGeneratedFixture(t, `
type LogSnapshot { value: Int text: String }
event DebugRequested(data: LogSnapshot)
event DebugDone(value: Int)
fn logSnapshot(data: LogSnapshot): Int { data.d return data.value }
on DebugRequested {
 it.d
 it.data.d
 val value = logSnapshot(it.data)
 emit DebugDone(value: value)
}
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/luxolog")
type debugBus struct {event.Bus;handler event.Handler;value int64;calls int}
func(b *debugBus)OnQueue(_ string,_ string,h event.Handler)error {b.handler=h;return nil}
func(b *debugBus)Emit(_ context.Context,_ string,payload any)error {b.value=payload.(*DebugDoneEvent).Value;b.calls++;return nil}
func TestEventDebug(t *testing.T){
 old:=luxolog.GetLevel();defer luxolog.SetLevel(old);if err:=luxolog.SetLevel(luxolog.LevelDebug);err!=nil{t.Fatal(err)}
 bus:=&debugBus{};app:=&App{EventBus:bus};if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 if err:=bus.handler(t.Context(),DebugRequestedEvent{Data:LogSnapshot{Value:7,Text:"snapshot"}});err!=nil{t.Fatal(err)}
 if bus.calls!=1||bus.value!=7{t.Fatal(bus.calls,bus.value)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"LogSnapshot": {"value": 1, "text": 2}}, EventFields: map[string]map[string]int{"DebugRequested": {"data": 1}, "DebugDone": {"value": 1}}})
}

func TestDebugReceiverBoundaries(t *testing.T) {
	capture := debugCaptureSource()
	for _, sample := range []struct{ name, source, assertion string }{
		{"empty", `fn probe(): [Int] { return [].d }`, `value,err:=app.probe(t.Context());if err!=nil||len(value)!=0{t.Fatal(value,err)}`},
		{"empty_chain", `fn probe(): [String] { return [].d.i }`, `value,err:=app.probe(t.Context());if err!=nil||value==nil||len(value)!=0{t.Fatal(value,err)}`},
		{"empty_local", `fn probe(): [Int] { val value: [Int] = [].d return value }`, `value,err:=app.probe(t.Context());if err!=nil||value==nil||len(value)!=0{t.Fatal(value,err)}`},
		{"empty_argument", `fn consume(value: [Int]): Int { return value.size } fn probe(): Int { return consume([].d) }`, `value,err:=app.probe(t.Context());if err!=nil||value!=0{t.Fatal(value,err)}`},
		{"null", `fn probe(): Int? { return null?.d }`, `value,err:=app.probe(t.Context());if err!=nil||value!=nil{t.Fatal(value,err)}`},
		{"null_chain", `fn probe(): Int? { return null?.d?.i }`, `value,err:=app.probe(t.Context());if err!=nil||value!=nil{t.Fatal(value,err)}`},
		{"declared_fields", `type DebugFields { d: Int i: String w: Boolean e: [Int] }
fn probe(value: DebugFields): Int { val d = value.d val i = value.i val w = value.w val e = value.e if w { return d + i.length + e.size + 1 } return 0 }`,
			`value,err:=app.probe(t.Context(),DebugFields{D:7,I:"ab",W:true,E:[]int64{1}});if err!=nil||value!=11{t.Fatal(value,err)}`},
		{"model_field", `model DebugModel @noTime { id: Int @id d: Int }
fn probe(value: DebugModel): Int { return value.d }`, `value,err:=app.probe(t.Context(),&DebugModel{Id:1,D:7});if err!=nil||value!=7{t.Fatal(value,err)}`},
		{"nullable_field", `type DebugFields { d: Int i: String w: Boolean e: [Int] }
fn probe(value: DebugFields?): Int? { return value?.d }`, `value,err:=app.probe(t.Context(),nil);if err!=nil||value!=nil{t.Fatal(value,err)};value,err=app.probe(t.Context(),&DebugFields{D:7});if err!=nil||value==nil||*value!=7{t.Fatal(value,err)}`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runGeneratedFixture(t, sample.source, `package generated
import("bytes";"os";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/luxolog")
`+capture+`
func TestDebugReceiver(t *testing.T){old:=luxolog.GetLevel();defer luxolog.SetLevel(old);app:=&App{}
 for _,level:=range []int{luxolog.LevelDebug,luxolog.LevelError}{
  if err:=luxolog.SetLevel(level);err!=nil{t.Fatal(err)}
  output:=logCapture(t,func(){`+sample.assertion+`})
  empty:=strings.HasPrefix(`+strconv.Quote(sample.name)+`,"empty")
  if (level==luxolog.LevelError||!empty)&&output!=""{t.Fatal("unexpected diagnostic",output)}
  if level==luxolog.LevelDebug&&empty&&!strings.Contains(output,"[]"){t.Fatal("empty diagnostic missing",output)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"DebugFields": {"d": 1, "i": 2, "w": 3, "e": 4}, "DebugModel": {"id": 1, "d": 2}}})
		})
	}
}

func TestDebugCompilerGuards(t *testing.T) {
	c := newCompiler(nil)
	c.generator = &GeneratorContext{}
	value := &ast.Ident{Name: "value", TypeTagged: ast.TypeTagged{TypeTag: "Int"}}
	c.compileExpr(&ast.MemberExpr{Object: value, Field: "d"})
	if !strings.Contains(c.b.String(), "_debugRecord.Int") || c.generator.generateDebugFile("generated") != nil {
		t.Fatal("scalar fragment lost its static formatter")
	}
	g := &debugLogGenerator{owner: c.generator}
	if _, found := g.findObject("missing"); found {
		t.Fatal("nil source produced an object")
	}
	g.source = &semantic.Result{Files: []*ast.File{{Extends: []*ast.ExtendDecl{{Name: "Remote", Fields: []*ast.FieldDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}}}}}}}
	if fields, found := g.findObject("Remote"); !found || len(fields) != 2 || fields[1].Name != "value" {
		t.Fatal("extend debug layout lost its key or field", fields, found)
	}
	if _, found := g.findObject("missing"); found {
		t.Fatal("unknown declaration acquired a formatter")
	}
	for name, invalid := range map[string]func(){
		"unknown_declaration": func() { g.requireObject("missing") },
		"missing_type":        func() { g.writeValue(c.b, nil, "value", "record", "1", "", false, true) },
		"unknown_receiver": func() {
			c.compileExpr(&ast.MemberExpr{Object: &ast.Ident{Name: "untyped"}, Field: "d"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if _, ok := recover().(*InternalCompilerError); !ok {
					t.Error("invalid compiler metadata did not report an internal error")
				}
			}()
			invalid()
		})
	}
}

func TestGeneratedRemoteDebugValue(t *testing.T) {
	generated := loadDemandModuleFixture(t, `fn debugRemote(value: Product): Product { return value.d }`)
	capture := debugCaptureSource()
	runGeneratedCompilation(t, generated, `package generated
import("bytes";"os";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/luxolog")
`+capture+`
func TestRemoteDebugValue(t *testing.T){
 old:=luxolog.GetLevel();defer luxolog.SetLevel(old);if err:=luxolog.SetLevel(luxolog.LevelDebug);err!=nil{t.Fatal(err)}
 app:=&App{};original:=&Product{Id:7,Name:"remote",Price:42}
 output:=logCapture(t,func(){value,err:=app.debugRemote(t.Context(),original);if err!=nil||value!=original{t.Fatal(value,err)}})
 for _,text:=range []string{"Product{","id: 7","name: ","remote","price: 42"}{if !strings.Contains(output,text){t.Fatal("missing stub field",text,output)}}
}
`)
}

func debugCaptureSource() string {
	return debugChainAssertions[strings.Index(debugChainAssertions, "func logCapture"):strings.Index(debugChainAssertions, "func TestDebugValues")]
}

func TestDebugEmptyAndNullAPIs(t *testing.T) {
	runGeneratedFixture(t, `api emptyNumbers(): [Int] { return [].d }
api emptyStrings(): [String] { return [].d.i }
api nullValue(): Int? { return null?.d?.i }`, `package generated
import("bytes";"os";"strings";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luxolog")
`+debugCaptureSource()+`
func TestDebugEmptyAndNullWire(t *testing.T){
 old:=luxolog.GetLevel();defer luxolog.SetLevel(old);router:=api.NewRouter();app:=&App{}
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,level:=range []int{luxolog.LevelDebug,luxolog.LevelError}{if err:=luxolog.SetLevel(level);err!=nil{t.Fatal(err)}
  for _,name:=range []string{"emptyNumbers","emptyStrings","nullValue"}{for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\""+name+"\"}")
   if binary{body=codec.AppendVarint(nil,uint64(router.Schema.APIs[name].ID));body=append(body,0,0)}
   req:=httptest.NewRequest("POST","/",bytes.NewReader(body));req.Header.Set("Content-Type","application/json");if binary{req.Header.Set("X-Luxo-Mode","binary")}
   result:=httptest.NewRecorder();output:=logCapture(t,func(){router.ServeHTTP(result,req)})
   want:=[]byte("{\"data\":[]}");if name=="nullValue"{want=[]byte("{\"data\":null}")};if binary{want=[]byte{0}}
   if result.Code!=200||!bytes.Equal(result.Body.Bytes(),want){t.Fatal(name,binary,result.Code,result.Body.String())}
   if (name=="nullValue"||level==luxolog.LevelError)&&output!=""{t.Fatal("unexpected diagnostic",name,output)}
   if name!="nullValue"&&level==luxolog.LevelDebug&&!strings.Contains(output,"[]"){t.Fatal("missing diagnostic",name,output)}
  }}
 }
}
`, StableIDs{})
}

const debugChainSource = `
enum LogState { ready }
enum LogNames { d i w e }
model LogNode @noTime { id: Int @id name: String password: String @hidden next: LogNode? logNodeId: Int? }
type LogBox { name: String nodes: [LogNode] amount: Decimal }
type LogCycle { next: LogCycle? }
fn logText(value: String): String { return value.d.i.w.e }
fn logInt(value: Int): Int { return value.d }
fn logFloat(value: Float): Float { return value.d }
fn logBool(value: Boolean): Boolean { return value.d }
fn logState(value: LogState): LogState { return value.d }
fn logList(values: [Int]): [Int] { return values.d }
fn logNodes(values: [LogNode]): [LogNode] { return values.d }
fn logNode(value: LogNode): LogNode { return value.d }
fn logBox(value: LogBox): LogBox { return value.d }
fn logCycle(value: LogCycle): LogCycle { return value.d }
fn logChannel(value: Channel<Int>): Channel<Int> { return value.d }
fn logNames(): LogNames { return LogNames.d }
fn logShadow(LogNames: Int, _debugValue1: Int, _debugRecord: String): Int { return LogNames.d + _debugValue1 }
fn logExtra(date: DateTime, duration: Duration, id: UUID, amount: Decimal, raw: Bytes, json: JSON): Int {
 date.d
 duration.i
 id.w
 amount.e
 raw.d
 json.d
 return 1
}
fn logNullable(value: Int?): Int? { return value?.d }
fn logNullableList(values: [Int]?): [Int]? { return values?.d }
fn logStatement(value: Int): Int { value.i return value }
fn logMapped(values: [Int]): [Int] { return values.map { it.d } }
fn logDiscarded(values: [Int]): Int { values.forEach { it.d } return values.size }
fn logFailure(): Int { throw error.NotFound }
fn logFailedReceiver(): Int { return logFailure().d }
fn logSideEffect(): Result<Int> @native
fn logOnce(): Int { return (logSideEffect()?).d }
fn logShort(value: Boolean): Boolean { return value && ((logSideEffect()?).d > 0) }
api logOutput(value: Int): Int { return value.d }
`

const debugChainAssertions = `package generated
import (
 "bytes"
 "math"
 "net/http/httptest"
 "os"
 "strings"
 "testing"
 "context"
 "time"
 "github.com/google/uuid"
 "github.com/shopspring/decimal"
 "github.com/light-speak/luxo/pkg/lux/api"
 luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
 "github.com/light-speak/luxo/pkg/lux/luxolog"
 "github.com/light-speak/luxo/pkg/lux/codec"
)
type logResolver struct { calls int }
func (r *logResolver) LogSideEffect(context.Context)(int64,error) {r.calls++;return 7,nil}
func logCapture(t *testing.T,fn func())string {
 t.Helper();file,err:=os.CreateTemp(".","debug-log-");if err!=nil{t.Fatal(err)};defer file.Close();defer os.Remove(file.Name())
 old:=os.Stderr;os.Stderr=file;defer func(){os.Stderr=old}();fn()
 if _,err:=file.Seek(0,0);err!=nil{t.Fatal(err)};var out bytes.Buffer;if _,err:=out.ReadFrom(file);err!=nil{t.Fatal(err)};return out.String()
}
func TestDebugValues(t *testing.T) {
 old:=luxolog.GetLevel();defer luxolog.SetLevel(old);if err:=luxolog.SetLevel(luxolog.LevelDebug);err!=nil{t.Fatal(err)}
 resolver:=&logResolver{};app:=&App{Resolver:resolver};ctx:=t.Context()
 output:=logCapture(t,func(){
  if v,e:=app.logText(ctx,"hello");e!=nil||v!="hello"{t.Fatal(v,e)}
  if v,e:=app.logInt(ctx,math.MinInt64);e!=nil||v!=math.MinInt64{t.Fatal(v,e)}
  if v,e:=app.logFloat(ctx,math.Copysign(0,-1));e!=nil||!math.Signbit(v){t.Fatal(v,e)}
  if v,e:=app.logBool(ctx,true);e!=nil||!v{t.Fatal(v,e)}
  if v,e:=app.logState(ctx,LogStateready);e!=nil||v!=LogStateready{t.Fatal(v,e)}
  values:=[]int64{3,1};if v,e:=app.logList(ctx,values);e!=nil||&v[0]!=&values[0]{t.Fatal(v,e)}
  n:=&LogNode{Id:7,Name:"node",Password:"must-not-print"};n.Next=n
  if v,e:=app.logNode(ctx,n);e!=nil||v!=n{t.Fatal(v,e)}
  nodes:=[]*LogNode{nil,n};if v,e:=app.logNodes(ctx,nodes);e!=nil||&v[0]!=&nodes[0]{t.Fatal(v,e)}
  box:=LogBox{Name:"box",Nodes:[]*LogNode{n}};if v,e:=app.logBox(ctx,box);e!=nil||v.Name!="box"||v.Nodes[0]!=n{t.Fatal(v,e)}
  cycle:=LogCycle{};cycle.Next=&cycle;if v,e:=app.logCycle(ctx,cycle);e!=nil||v.Next!=&cycle{t.Fatal(v,e)}
  ch:=make(chan int64,2);ch<-9;if v,e:=app.logChannel(ctx,ch);e!=nil||v!=ch||len(ch)!=1{t.Fatal(v,e)}
  if v,e:=app.logNames(ctx);e!=nil||v!=LogNamesd{t.Fatal(v,e)}
  if v,e:=app.logShadow(ctx,3,4,"kept");e!=nil||v!=7{t.Fatal(v,e)}
  if v,e:=app.logExtra(ctx,time.Unix(1,123),time.Second,uuid.Nil,decimal.RequireFromString("12.34"),[]byte{0,255},[]byte("{\"n\":1}"));e!=nil||v!=1{t.Fatal(v,e)}
  if v,e:=app.logStatement(ctx,9);e!=nil||v!=9{t.Fatal(v,e)}
  if v,e:=app.logMapped(ctx,[]int64{3,4});e!=nil||len(v)!=2||v[1]!=4{t.Fatal(v,e)}
  if v,e:=app.logDiscarded(ctx,[]int64{3,4});e!=nil||v!=2{t.Fatal(v,e)}
  if v,e:=app.logOnce(ctx);e!=nil||v!=7||resolver.calls!=1{t.Fatal(v,e,resolver.calls)}
 })
 for _,text:=range []string{"DEBUG","INFO","WARN","ERROR","hello","-9223372036854775808","-0","true","ready","[3, 1]","LogNode","node","<redacted>","<depth-limit>","Channel(len=1, cap=2)","12.34","0x00ff","{\"n\":1}"} {if !strings.Contains(output,text){t.Errorf("missing %q",text)}}
 if strings.Contains(output,"must-not-print"){t.Fatal("hidden value leaked")}
 if len(output)>100000{t.Fatal("cyclic object log unbounded",len(output))}
 if resolver.calls!=1{t.Fatal("receiver repeated",resolver.calls)}
 silent:=logCapture(t,func(){
  if _,e:=app.logFailedReceiver(ctx);e!=luxerrors.NotFound{t.Fatal("receiver error",e)}
  if v,e:=app.logShort(ctx,false);e!=nil||v{t.Fatal(v,e)}
  if v,e:=app.logNullable(ctx,nil);e!=nil||v!=nil{t.Fatal(v,e)}
  if v,e:=app.logNullableList(ctx,nil);e!=nil||v!=nil{t.Fatal(v,e)}
 })
 if silent!=""||resolver.calls!=1{t.Fatal("lazy receiver executed",silent,resolver.calls)}
}
func TestDebugFilteredAndWire(t *testing.T) {
 old:=luxolog.GetLevel();defer luxolog.SetLevel(old);if err:=luxolog.SetLevel(luxolog.LevelError);err!=nil{t.Fatal(err)}
 app:=&App{Resolver:&logResolver{}}
 output:=logCapture(t,func(){if v,e:=app.logInt(t.Context(),7);e!=nil||v!=7{t.Fatal(v,e)}})
 if output!=""{t.Fatal("filtered log emitted",output)}
 router:=api.NewRouter();if e:=RegisterSchema(router.Schema);e!=nil{t.Fatal(e)};if e:=RegisterHandlers(router,app);e!=nil{t.Fatal(e)}
 if e:=router.Registry.RegisterSchemaAPIs(router.Schema);e!=nil{t.Fatal(e)};if e:=router.Prepare();e!=nil{t.Fatal(e)}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"logOutput\",\"value\":7}")
  if binary{body=codec.AppendVarint(nil,uint64(router.Schema.APIs["logOutput"].ID));body=codec.AppendVarint(body,0);enc:=codec.NewEncoder(body);enc.WriteFieldInt(1,7);enc.WriteEnd();body=enc.Bytes()}
  req:=httptest.NewRequest("POST","/",bytes.NewReader(body));req.Header.Set("Content-Type","application/json");if binary{req.Header.Set("X-Luxo-Mode","binary")}
  res:=httptest.NewRecorder();router.ServeHTTP(res,req);want:=[]byte("{\"data\":7}");if binary{want=codec.AppendSvarint(nil,7)}
  if res.Code!=200||!bytes.Equal(res.Body.Bytes(),want){t.Fatal(binary,res.Code,res.Body.String())}
 }
}
`
