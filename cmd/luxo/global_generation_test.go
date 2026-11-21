package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestCLIGenericSchemaAndLockUseTheSameConcreteTypes(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"alpha/main": `type Box<T> { value: T }
fn makeValue(): Box<Int> { return Box<Int>(value: 42) }
api alphaValue(): Int { return makeValue().value }
api echo(value: Box<Int>): Box<Int> { return value }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("luxo.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Types map[string]json.RawMessage `json:"types"`
	}
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	lf, err := lockfile.Load("luxo.lock")
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Types) != 1 {
		t.Fatalf("expected one concrete Box<Int>, got %d", len(exported.Types))
	}
	for name := range exported.Types {
		if _, exists := lf.Types[name]; !exists {
			t.Fatalf("exported concrete type %s is absent from luxo.lock", name)
		}
	}
	if _, exists := lf.Types["Box"]; exists {
		t.Fatal("generic template was registered as a concrete wire type")
	}
	assertGlobalGenerationCalls(t, "alpha", "alphaValue", "", "", "42")
}

func TestCLICommonGenericInstantiationsCompileInEachModule(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/box": `type Box<T> { value: T }`,
		"alpha/main": `fn makeAlpha(): Box<Int> { return Box<Int>(value: 42) } api alphaValue(): Int { return makeAlpha().value }`,
		"beta/main":  `fn makeBeta(): Box<Int> { return Box<Int>(value: 42) } api betaValue(): Int { return makeBeta().value }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "alpha", "alphaValue", "", "", "42")
	assertGlobalGenerationCalls(t, "beta", "betaValue", "", "", "42")
}

func TestCLIGenericDependencyKeepsLowercaseSourceType(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/box": `type Box<T> { value: T } type counter { value: Int }`,
		"alpha/main": `api echo(value: Box<counter>): Box<counter> { return value } api alphaValue(): Int { return 42 }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "alpha", "alphaValue", "", "", "42")
}

func TestCLICommonGenericPayloadDependenciesCompileInEachModule(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/box": `type Box<T> { value: T } type Counter { value: Int }`,
		"alpha/main": `fn makeAlpha(): Box<Counter> { return Box<Counter>(value: Counter { value: 42 }) } api alphaValue(): Int { return makeAlpha().value.value }`,
		"beta/main":  `fn makeBeta(): Page<Box<Counter>> { return Page<Box<Counter>>(items: [Box<Counter>(value: Counter { value: 42 })], total: 42, page: 1, pageSize: 10) } api betaValue(): Int { return makeBeta().total }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "alpha", "alphaValue", "", "", "42")
	assertGlobalGenerationCalls(t, "beta", "betaValue", "", "", "42")
}

func TestCLIGenericUnicodePayloadUsesExportedCommonFields(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/box": `type 盒<T> { 值: T } type 计数 { 数值: Int } enum 状态 { 就绪 }`,
		"alpha/main": `fn makeAlpha(): 盒<Int> { return 盒<Int>(值: 42) } api alphaValue(): Int { return makeAlpha().值 } api echo(value: 盒<计数>): 盒<计数> { return value } api current(): 状态 { return 状态.就绪 }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "alpha", "alphaValue", "", "", "42")
}

func TestCLICanonicalGenericDepthBudgetSurvivesAliases(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/node": `type Node<T> { value: T next: Node<T>? }`,
		"alpha/main":  `type Envelope<T> { child: T } fn makeEnvelope(): Envelope<Node<Int>> { return Envelope<Node<Int>>(child: Node<Int>(value: 1, next: null)) } api echo(value: Envelope<Node<Int>>): Envelope<Node<Int>> { return value }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("luxo.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		APIs map[string]struct {
			ReturnType string `json:"returnType"`
		} `json:"apis"`
	}
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	source := strings.ReplaceAll(cliGenericDepthAssertions, "ROOT_TYPE", exported.APIs["echo"].ReturnType)
	if err := os.WriteFile("service/alpha/luxo/depth_test.go", []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-race", "./service/alpha/luxo")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("canonical alias depth budget: %v\n%s", err, output)
	}
}

const cliGenericDepthAssertions = `package luxo
import("errors";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestCanonicalAliasDepth(t *testing.T){
 value,err:=(&App{}).makeEnvelope(t.Context());if err!=nil{t.Fatal(err)}
 node:=value.Child;for range 62{next:=*node;node.Next=&next;node=node.Next}
 var rows api.ResponseBuf;if err:=value.WriteLuxo(&rows,nil);err!=nil{t.Fatal("64 response",err)}
 var decoded ROOT_TYPE;dec:=codec.NewDecoder(rows.B);decoded.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal("64 read",err)}
 var columnar api.ResponseBuf;if err:=WriteColumnarROOT_TYPE(&columnar,[]ROOT_TYPE{value},nil);err!=nil{t.Fatal("64 columnar",err)}
 if _,err:=ReadColumnarROOT_TYPE(columnar.B);err!=nil{t.Fatal("64 columnar read",err)}
 json:="{\"child\":"+strings.Repeat("{\"value\":1,\"next\":",63)+"null"+strings.Repeat("}",64)
 if err:=decoded.ReadJSONInput([]byte(json));err!=nil{t.Fatal("64 JSON",err)}
 input:=[]byte{0,1,2,2,0,0};for range 62{input=append(append([]byte{0,1,2,2,1},input...),0)}
 input=append(append([]byte{0,1},input...),0);dec=codec.NewDecoder(input);decoded.ReadLuxoInput(dec);if err:=dec.Finish();err!=nil{t.Fatal("64 input",err)}
 extra:=*node;node.Next=&extra
 if err:=value.WriteLuxo(&api.ResponseBuf{},nil);!errors.Is(err,codec.ErrModelDepth){t.Fatal("65 response",err)}
 if err:=WriteColumnarROOT_TYPE(&api.ResponseBuf{},[]ROOT_TYPE{value},nil);!errors.Is(err,codec.ErrModelDepth){t.Fatal("65 columnar",err)}
 json="{\"child\":"+strings.Repeat("{\"value\":1,\"next\":",64)+"null"+strings.Repeat("}",65)
 if err:=decoded.ReadJSONInput([]byte(json));!errors.Is(err,codec.ErrModelDepth){t.Fatal("65 JSON",err)}
 input=append(append([]byte{0,1,0,1,2,2,1},input[2:len(input)-1]...),0,0);dec=codec.NewDecoder(input);decoded.ReadLuxoInput(dec);if err:=dec.Finish();!errors.Is(err,codec.ErrModelDepth){t.Fatal("65 input",err)}
}
`

func TestCLIGenericPayloadCrossesEventModuleBoundary(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/box": `type Box<T> { value: T }`,
		"alpha/main": `event Ready(value: Box<Int>) api alphaValue(): Int { emit Ready(value: Box<Int>(value: 42)) return 42 }`,
		"beta/main":  `event Seen(value: Int) fn consume(value: Box<Int>): Int { return value.value } on Ready { emit Seen(value: consume(it.value)) } api betaValue(): Int { return 42 }`,
	})
	t.Setenv("LUXO_EVENT_BACKEND", "memory")
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("service/beta/luxo/event_delivery_test.go", []byte(cliGenericEventAssertions), 0600); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "beta", "betaValue", "", "", "42")
}

func TestCLISealedPayloadCrossesEventModuleBoundary(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/box": `type Box<T> { value: T } sealed Outcome { Done(value: Box<Int>) }`,
		"alpha/main": `event Ready(value: Outcome) api alphaValue(): Int { emit Ready(value: Outcome.Done(Box<Int>(value: 42))) return 42 }`,
		"beta/main":  `event Seen(value: Int) fn consume(value: Outcome): Int { return when(value) { is Done -> value.value.value } } on Ready { emit Seen(value: consume(it.value)) } api betaValue(): Int { return 42 }`,
	})
	t.Setenv("LUXO_EVENT_BACKEND", "memory")
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	assertions := strings.ReplaceAll(cliGenericEventAssertions, `"{\"value\":42}"`, `"{\"Done\":{\"value\":{\"value\":42}}}"`)
	if err := os.WriteFile("service/beta/luxo/event_delivery_test.go", []byte(assertions), 0600); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "beta", "betaValue", "", "", "42")
}

func TestCLISealedLowercaseCommonOwner(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/result": `sealed outcome { Done(value: Int) }`,
		"alpha/main":    `fn make(): outcome { return outcome.Done(42) } api alphaValue(): Int { val value = make() return when(value) { is Done -> value.value } }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "alpha", "alphaValue", "", "", "42")
}

func TestCLISealedEventStreamUsesCanonicalPayload(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/result": `type Profile { secret: String? @visible(my.id == 42) } sealed Outcome { Value(profile: Profile) }`,
		"alpha/main":    `event Ready(payload: Outcome) api alphaValue(): Int { return 42 }`,
		"beta/main":     `api watch(): Outcome @stream(Ready) api betaValue(): Int { return 42 }`,
	})
	t.Setenv("LUXO_EVENT_BACKEND", "memory")
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("service/beta/luxo/sealed_stream_test.go", []byte(cliSealedStreamAssertions), 0600); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "beta", "betaValue", "", "", "42")
}

func TestCLISealedEqualityUsesCanonicalOwner(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/result": `sealed Outcome { Value(value: Int) Next(child: Outcome?) }`,
		"beta/main":     `fn same(left: Outcome,right: Outcome): Boolean { return left == right } api betaValue(): Int { return 42 }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("service/beta/luxo/equality_test.go", []byte(`package luxo
import "testing"
func TestCanonicalSealedEquality(t *testing.T){
 app:=&App{};left:=(Outcome{}).NewVariant1(7);right:=(Outcome{}).NewVariant1(7)
 if equal,err:=app.same(t.Context(),left,right);err!=nil||!equal{t.Fatal(equal,err)}
 left=(Outcome{}).NewVariant2(nil);left.AsVariant2().F1=&left
 right=(Outcome{}).NewVariant2(nil);right.AsVariant2().F1=&right
 if equal,err:=app.same(t.Context(),left,right);err!=nil||!equal{t.Fatal(equal,err)}
}

`), 0600); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "beta", "betaValue", "", "", "42")
}

func TestCLISealedEnumPayloadDependencies(t *testing.T) {
	for _, name := range []string{"Role", "role"} {
		t.Run(name, func(t *testing.T) {
			writeGlobalGenerationProject(t, map[string]string{
				"common/result": fmt.Sprintf(`enum %s { READY } enum LuxoPrivateEnum_726f6c65 { OTHER } sealed Outcome { Value(role: %s) }`, name, name),
				"beta/main":     `api echo(value: Outcome): Outcome { return value } api betaValue(): Int { return 42 }`,
			})
			if err := runGen(nil, nil); err != nil {
				t.Fatal(err)
			}
			assertions := strings.ReplaceAll(`package luxo
import("testing";"github.com/light-speak/luxo/pkg/lux/schema")
func TestSealedEnumPayload(t *testing.T){
 s:=schema.New();if err:=RegisterSchema(s);err!=nil{t.Fatal(err)}
 if s.Enums["ENUM_NAME"]==nil{t.Fatal("sealed payload lost canonical enum metadata")}
 var value Outcome;if err:=value.ReadJSONInput([]byte("{\"Value\":{\"role\":\"READY\"}}"));err!=nil{t.Fatal(err)}
}
`, "ENUM_NAME", name)
			if err := os.WriteFile("service/beta/luxo/sealed_enum_test.go", []byte(assertions), 0600); err != nil {
				t.Fatal(err)
			}
			assertGlobalGenerationCalls(t, "beta", "betaValue", "", "", "42")
		})
	}
}

const cliSealedStreamAssertions = `package luxo
import("bytes";"context";"testing";"time";alpha "example.com/generated/service/alpha/luxo";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/event")
func TestSealedCrossModuleStream(t *testing.T){
 cfg:=&auth.Config{Secret:"cross-module-sealed-stream",Expires:time.Hour};token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,"{\"id\":42}"...),nil});if err!=nil{t.Fatal(err)}
 identity,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();router.IdentityExtractor=func(context.Context)any{return identity};if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 bus:=event.NewChanBus(4);defer bus.Close();if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
 sub,err:=router.OpenInternalStream(t.Context(),&api.Request{API:"watch",BinaryMode:true});if err!=nil{t.Fatal(err)};defer sub.Close()
 var input alpha.ReadyEvent;if err:=input.Payload.ReadJSONInput([]byte("{\"Value\":{\"profile\":{\"secret\":\"private-for-42\"}}}"));err!=nil{t.Fatal(err)}
 if err:=alpha.EmitReady(t.Context(),bus,input);err!=nil{t.Fatal(err)}
 select{case raw,ok:=<-sub.Messages():if !ok||!bytes.Contains(raw,[]byte("private-for-42")){t.Fatal(raw,sub.Err())};case<-time.After(3*time.Second):t.Fatal("missing cross-module stream message")}
}
`

func TestCLISealedCanonicalAliasDepthBudget(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/result": `type Box<T> { value: T } type Counter { value: Int } sealed Outcome { Next(child: Outcome?) Done(value: Counter) Pending }`,
		"alpha/main":    `api echo(value: Box<Outcome>): Box<Outcome> { return value }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("luxo.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		APIs map[string]struct{ ReturnType string }
	}
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	assertions := strings.ReplaceAll(cliSealedAliasDepthAssertions, "ROOT_TYPE", exported.APIs["echo"].ReturnType)
	if err := os.WriteFile("service/alpha/luxo/sealed_depth_test.go", []byte(assertions), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-race", "./service/alpha/luxo")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("sealed canonical depth: %v\n%s", err, output)
	}
}

const cliSealedAliasDepthAssertions = `package luxo
import("errors";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestSealedAliasDepth(t *testing.T){
 leaf:=(Outcome{}).NewVariant2(Counter{Value:1})
 for range 30 { child:=leaf;leaf=(Outcome{}).NewVariant1(&child) }
 value:=ROOT_TYPE{Value:&leaf}
 var buf api.ResponseBuf;if err:=value.WriteLuxo(&buf,nil);err!=nil{t.Fatal("64 row",err)}
 var decoded ROOT_TYPE;dec:=codec.NewDecoder(buf.B);decoded.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal("64 read",err)}
 json:="{\"value\":"+strings.Repeat("{\"Next\":{\"child\":",30)+"{\"Done\":{\"value\":{\"value\":1}}}"+strings.Repeat("}}",30)+"}"
 if err:=decoded.ReadJSONInput([]byte(json));err!=nil{t.Fatal("64 JSON",err)}
 var columns api.ResponseBuf;if err:=WriteColumnarROOT_TYPE(&columns,[]ROOT_TYPE{value},nil);err!=nil{t.Fatal("64 columnar",err)}
 if _,err:=ReadColumnarROOT_TYPE(columns.B);err!=nil{t.Fatal("64 columnar read",err)}
 leaf=(Outcome{}).NewVariant3();for range 31{child:=leaf;leaf=(Outcome{}).NewVariant1(&child)};value.Value=&leaf
 if err:=value.WriteLuxo(&api.ResponseBuf{},nil);!errors.Is(err,codec.ErrModelDepth){t.Fatal("65 row",err)}
 if err:=WriteColumnarROOT_TYPE(&api.ResponseBuf{},[]ROOT_TYPE{value},nil);!errors.Is(err,codec.ErrModelDepth){t.Fatal("65 columnar",err)}
 json="{\"value\":"+strings.Repeat("{\"Next\":{\"child\":",31)+"{\"Pending\":{}}"+strings.Repeat("}}",31)+"}"
 if err:=decoded.ReadJSONInput([]byte(json));!errors.Is(err,codec.ErrModelDepth){t.Fatal("65 JSON",err)}
}
`

const cliGenericEventAssertions = `package luxo
import("context";"testing";"time";alpha "example.com/generated/service/alpha/luxo";"github.com/light-speak/luxo/pkg/lux/event")
func TestGeneratedGenericEventDelivery(t *testing.T){
 bus:=event.NewChanBus(16);defer bus.Close()
 app:=&App{EventBus:bus};if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 seen:=make(chan int64,2)
 if err:=event.OnValueDecode(bus,"Seen",func(raw []byte)(SeenEvent,error){var value SeenEvent;err:=value.UnmarshalLuxo(raw);return value,err},func(_ context.Context,value SeenEvent)error{seen<-value.Value;return nil});err!=nil{t.Fatal(err)}
 var input alpha.ReadyEvent;if err:=input.Value.ReadJSONInput([]byte("{\"value\":42}"));err!=nil{t.Fatal(err)}
 raw,err:=input.MarshalLuxo();if err!=nil{t.Fatal(err)}
 if err:=alpha.EmitReady(t.Context(),bus,input);err!=nil{t.Fatal(err)}
 if err:=bus.Emit(t.Context(),"Ready",raw);err!=nil{t.Fatal(err)}
 for range 2{select{case value:=<-seen:if value!=42{t.Fatal(value)};case<-time.After(5*time.Second):t.Fatal("generic event was not delivered")}}
}
`

func TestSchemaDiscoveryRejectsUnreadableModule(t *testing.T) {
	blockGenerationModuleRead(t)
	files, err := findSchemaFiles()
	if files != nil || !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), filepath.Join("origin", "blocked")) {
		t.Fatalf("unreadable module was skipped: files=%v error=%v", files, err)
	}
}

func TestCLIOriginReadFailurePreservesOutputs(t *testing.T) {
	for _, operation := range []string{"gen", "diff", "squash"} {
		t.Run(operation, func(t *testing.T) {
			blockGenerationModuleRead(t)
			files := map[string]string{
				"service/a/luxo/app.gen.go": "// existing generated source\npackage luxo\n",
				"luxo.schema.json":          "{\"existing\":true}\n",
				"migrations/.state.json":    "{\"models\":{}}\n",
			}
			for name, content := range files {
				if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := lockfile.New().Save("luxo.lock"); err != nil {
				t.Fatal(err)
			}
			locked, err := os.ReadFile("luxo.lock")
			if err != nil {
				t.Fatal(err)
			}
			files["luxo.lock"] = string(locked)
			output, err := captureCLIResponse(t, func() error {
				switch operation {
				case "gen":
					return runGen(nil, nil)
				case "diff":
					return runMigrateDiff(nil, nil)
				default:
					return runMigrateSquash(nil, nil)
				}
			})
			if !errors.Is(err, os.ErrPermission) || output != "" {
				t.Errorf("module read failure reported success or lost cause: output=%q error=%v", output, err)
			}
			for name, want := range files {
				if data, err := os.ReadFile(name); err != nil || string(data) != want {
					t.Errorf("unreadable source changed %s: %v", name, err)
				}
			}
			if entries, err := os.ReadDir("migrations"); err != nil || len(entries) != 1 {
				t.Errorf("unreadable source produced migration output: entries=%v error=%v", entries, err)
			}
		})
	}
}

func blockGenerationModuleRead(t *testing.T) {
	t.Helper()
	writeGlobalGenerationProject(t, map[string]string{
		"a":           `api availableValue(): Int { return 7 }`,
		"blocked/api": `api blockedValue(): Int { return 9 }`,
	})
	path := filepath.Join("origin", "blocked")
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0700); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.ReadDir(path); err == nil {
		t.Skip("filesystem credentials permit reading a mode-000 directory")
	} else if !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
}

func writeGlobalGenerationProject(t *testing.T, sources map[string]string) {
	t.Helper()
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	for _, name := range []string{"LUXO_EVENT_BACKEND", "LUXO_QUEUE_BACKEND", "LUXO_CACHE_BACKEND"} {
		t.Setenv(name, "none")
	}
	t.Setenv("DATABASE_DRIVER", "pg")
	for name, source := range sources {
		path := filepath.Join("origin", name+".luxo")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCLIGenerationPreservesGlobalTypeMetadata(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"project/main": `type _luxoGlobal0 { value: Int } val LOCAL_VALUE = 42 api projectValue(): Int { return LOCAL_VALUE }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "project", "projectValue", "", "", "42")
}

func TestCLIGenerationResolvesCommonGlobals(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/config": `enum Mode { READY DONE } model Shared @noTime { id: Int @id } val COMMON_NAME = "luxo" val BASE_VALUE = 40 val DEFAULT_MODE = Mode.READY`,
		"project/main":  `val PROJECT_VALUE = BASE_VALUE + 2 api projectValue(): Int { return PROJECT_VALUE } api projectLabel(): String { return COMMON_NAME } api projectMode(): Mode { return DEFAULT_MODE }`,
		"task/main":     `val TASK_VALUE = BASE_VALUE + 3 api taskValue(): Int { return TASK_VALUE } api taskLabel(): String { return COMMON_NAME } api taskMode(): Mode { return DEFAULT_MODE }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "project", "projectValue", "projectLabel", "projectMode", "42")
	assertGlobalGenerationCalls(t, "task", "taskValue", "taskLabel", "taskMode", "43")
	for _, module := range []string{"project", "task"} {
		command := exec.Command("go", "list", "-deps", "./service/"+module+"/luxo")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("generated module dependencies: %v\n%s", err, output)
		}
		for _, adapter := range []string{"/pkg/lux/pg", "/pkg/lux/nats", "/pkg/lux/cache/redis", "/pkg/lux/storage/s3"} {
			if strings.Contains(string(output), "github.com/light-speak/luxo"+adapter) {
				t.Fatalf("common constants imported an unused adapter: %s", adapter)
			}
		}
	}
}

func TestCLICommonGlobalsDoNotDependOnModuleName(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"common/config": `enum Mode { READY DONE } val COMMON_NAME = "luxo" val BASE_VALUE = 40 val DEFAULT_MODE = Mode.READY`,
		"alpha/main":    `val ALPHA_VALUE = BASE_VALUE + 2 api alphaValue(): Int { return ALPHA_VALUE } api alphaLabel(): String { return COMMON_NAME } api alphaMode(): Mode { return DEFAULT_MODE }`,
	})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGlobalGenerationCalls(t, "alpha", "alphaValue", "alphaLabel", "alphaMode", "42")
}

func TestCLIGenerationRemovesStaleGlobalFile(t *testing.T) {
	source := `type _luxoGlobal0 { value: Int } val LOCAL_VALUE = 42 api projectValue(): Int { return LOCAL_VALUE }`
	writeGlobalGenerationProject(t, map[string]string{"project/main": source})
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("service", "project", "luxo", "global.gen.go")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	handwritten := filepath.Join("service", "project", "luxo", "custom.go")
	if err := os.WriteFile(handwritten, []byte("package luxo\nconst customValue = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source = `type _luxoGlobal0 { value: Int } api projectValue(): Int { return 42 }`
	if err := os.WriteFile(filepath.Join("origin", "project", "main.luxo"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("removed constant left a stale global source", err)
	}
	if _, err := os.Stat(handwritten); err != nil {
		t.Fatal("regeneration changed handwritten source", err)
	}
	assertGlobalGenerationCalls(t, "project", "projectValue", "", "", "42")
}

func TestModuleCompilationResultKeepsReadOnlyMetadata(t *testing.T) {
	shared, local := &ast.ValStmt{Name: "SHARED"}, &ast.ValStmt{Name: "LOCAL"}
	first := &ast.File{Name: "origin/project/main.luxo", Globals: []*ast.ValStmt{local}}
	second := &ast.File{Name: "origin/project/more.luxo"}
	files := []*ast.File{first, second}
	typ := &semantic.ResolvedType{Name: "Sentinel"}
	expression := &ast.Ident{Name: "input"}
	full := &semantic.Result{
		Files: files, Scope: semantic.NewScope(), Types: map[string]*semantic.ResolvedType{"Sentinel": typ},
		GenericExprTypes: map[ast.Expr]*semantic.ResolvedType{expression: typ},
		Warnings:         []semantic.Warning{{NameLen: 1}},
	}
	result := moduleCompilationResult(full, files, []*ast.ValStmt{shared})
	if result == full || result.Scope != full.Scope || result.Types["Sentinel"] != typ || result.GenericExprTypes[expression] != typ || len(result.Warnings) != 1 {
		t.Fatal("module selection lost semantic metadata")
	}
	if result.Files[0] == first || result.Files[0].Name != first.Name || result.Files[1] != second {
		t.Fatal("module selection changed source ownership")
	}
	if len(result.Files[0].Globals) != 2 || result.Files[0].Globals[0] != shared || result.Files[0].Globals[1] != local {
		t.Fatal("shared constants did not precede local initializers")
	}
	result.Files[0].Globals[0] = nil
	if len(first.Globals) != 1 || first.Globals[0] != local || full.Files[0] != first {
		t.Fatal("module selection mutated original declarations")
	}
	for _, input := range [][]*ast.File{nil, files} {
		result := moduleCompilationResult(full, input, nil)
		if len(result.Files) != len(input) || len(input) != 0 && result.Files[0] != first {
			t.Fatal("ordinary module files were cloned unnecessarily")
		}
	}
	result = moduleCompilationResult(full, nil, []*ast.ValStmt{shared})
	if len(result.Files) != 0 {
		t.Fatal("empty module gained unrelated source")
	}
	globals := commonModuleGlobals([]*ast.File{{Globals: []*ast.ValStmt{shared}}, {Globals: []*ast.ValStmt{local}}})
	if len(globals) != 2 || globals[0] != shared || globals[1] != local || commonModuleGlobals(nil) != nil {
		t.Fatal("common declaration order changed")
	}
}

func assertGlobalGenerationCalls(t *testing.T, module, valueAPI, labelAPI, modeAPI, expected string) {
	t.Helper()
	source := strings.NewReplacer("VALUE_API", valueAPI, "LABEL_API", labelAPI, "MODE_API", modeAPI, "EXPECTED_VALUE", expected).Replace(cliGlobalAssertions)
	path := filepath.Join("service", module, "luxo", "global_test.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-race", "./service/"+module+"/luxo")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("CLI generated globals did not execute: %v\n%s", err, output)
	}
}

const cliGlobalAssertions = `package luxo
import("bytes";"net/http/httptest";"strconv";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestGeneratedGlobalCalls(t *testing.T){
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,name:=range []string{"VALUE_API","LABEL_API","MODE_API"}{if name==""{continue};for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\""+name+"\"}");if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(name,response.Code,response.Body.String())}
  expected:="luxo";if name=="MODE_API"{expected="READY"}
  if binary{decoder:=codec.NewDecoder(response.Body.Bytes());if name=="VALUE_API"{if v:=decoder.ReadInt();v!=EXPECTED_VALUE{t.Fatal(name,v)}}else if v:=decoder.ReadString();v!=expected{t.Fatal(name,v)};if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{
   want:=strconv.Quote(expected);if name=="VALUE_API"{want="EXPECTED_VALUE"};if response.Body.String()!="{\"data\":"+want+"}"{t.Fatal(name,response.Body.String())}
  }
 }}
}
`
