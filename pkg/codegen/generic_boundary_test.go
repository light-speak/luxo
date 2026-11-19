package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestCompiledEventValuesFollowSealedPayloads(t *testing.T) {
	input := analyzePermissionFixture(t, `type Box<T> { value: T }
sealed Outcome { Done(value: Box<Int>) Next(value: Outcome?) }
event Changed(value: Outcome)`)
	view, err := semantic.CompilationView(input)
	if err != nil {
		t.Fatal(err)
	}
	catalog := view.CompiledValueDeclarations()
	needed := compiledEventValueTypes(view, nil, catalog)
	for name := range view.ConcreteTypeOrigins() {
		if !needed[name] {
			t.Fatalf("sealed event payload lost canonical snapshot dependency %s", name)
		}
	}
}

func TestGeneratorBindsRawEventCatalogToConcreteValueTypes(t *testing.T) {
	var files []*ast.File
	for index, source := range []string{`type Box<T> { value: T }`, `event Ready(value: Box<Int>)`} {
		name := []string{"origin/common/types.luxo", "origin/producer/events.luxo"}[index]
		tokens, lexical := lexer.New(source, name).Tokenize()
		file, syntax := parser.New(tokens).Parse(name)
		if len(lexical)+len(syntax) != 0 {
			t.Fatal(lexical, syntax)
		}
		files = append(files, file)
	}
	analysis := semantic.AnalyzeModules(files)
	if len(analysis.Errors) != 0 {
		t.Fatal(analysis.Errors)
	}
	full, err := semantic.CompilationView(analysis)
	if err != nil {
		t.Fatal(err)
	}
	selected := *full
	selected.Files = full.Files[:1]
	context := BuildEventContext(files, "example.com/project/service")
	g := mustNewGenerator(t, GeneratorConfig{Events: context, IDs: StableIDs{ModelFields: map[string]map[string]int{"Box": {"value": 1}}}})
	generated, err := g.Generate(&selected, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Files["value_event.gen.go"]) == 0 {
		t.Fatal("raw event metadata lost its common concrete snapshot codec")
	}
	if context.Events["Ready"].Params[0].Type.Name != "Box" || len(context.Events["Ready"].Params[0].Type.TypeArgs) != 1 {
		t.Fatal("generator mutated the caller's raw event catalog")
	}
}

func TestCheckedEntriesRejectUnmaterializableGenericTypes(t *testing.T) {
	const basic = `type Box<T> { value: T } api echo(value: Box<Int>): Box<Int> { return value }`
	view, err := semantic.CompilationView(analyzePermissionFixture(t, basic))
	if err != nil {
		t.Fatal(err)
	}
	name := view.Files[0].APIs[0].Params[0].Type.Name
	source := analyzePermissionFixture(t, basic+" type "+name+" { value: String }")
	if len(source.Errors) != 0 {
		t.Fatal(source.Errors)
	}
	for _, generate := range []func() error{
		func() error { _, err := GenerateEntryFileChecked(source, "example.com/project"); return err },
		func() error { _, err := GenerateModuleEntryFilesChecked(source, "example.com/project"); return err },
		func() error { _, err := GenerateGatewayEntryChecked(source, "example.com/project"); return err },
	} {
		if err := generate(); err == nil || !strings.Contains(err.Error(), "conflicts with a declaration") {
			t.Fatal("entry point bypassed concrete type validation", err)
		}
	}
}

func TestGenerateRejectsChannelWireAndIgnoredTypeArguments(t *testing.T) {
	for _, source := range []string{
		`api readValue(value: Channel<Int>): Int { return 1 }`,
		`api readValue(): Channel<Int> @native`,
		`fn readValue(): Result<Channel<Int>> @service @native`,
		`type Packet { value: Channel<Int> }`,
		`model Item @noTime { id: Int @id value: Channel<Int> }`,
		`type Packet { value: Int<String> }`,
		`sealed Outcome { Value(value: Channel<Int>) }`,
		`sealed Outcome { Value(value: [Channel<Int>]) }`,
		`sealed Outcome { Value(value: Result<Int>) }`,
		`sealed Outcome { Value(value: Int<String>) }`,
		`sealed Outcome { Value(value: MissingPayload) }`,
		`sealed Outcome { Value(value: Int = "bad") }`,
		`sealed Outcome { Value(value: Int, value: String) }`,
		`sealed Outcome { Value(value: Int) Value(other: String) }`,
		`type Box<T> { value: T } api stray(value: T): Int { return 1 }`,
		`type Box<T> { value: T } type Stray { value: T }`,
	} {
		input := analyzePermissionFixture(t, source)
		if len(input.Errors) == 0 {
			t.Fatal("unsupported type passed semantic validation", source)
		}
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || output != nil || !strings.Contains(err.Error(), "semantic") {
			t.Fatalf("unsupported type emitted source: %v", err)
		}
	}
}

func TestGeneratedPhantomGenericListHasDefinedWireBehavior(t *testing.T) {
	const source = `type Unit<T> {}
fn empty(): [Unit<Int>] { return [] }
fn one(): [Unit<Int>] { return [Unit<Int>()] }
api units(): [Unit<Int>] { return empty() }`
	view, err := semantic.CompilationView(analyzePermissionFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	assertions := strings.ReplaceAll(`package generated
import("bytes";"errors";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestPhantomGenericLists(t *testing.T){
 app:=&App{};empty,err:=app.empty(t.Context());if err!=nil{t.Fatal(err)}
 var output api.ResponseBuf;if err:=WriteColumnarUNIT_TYPE(&output,empty,nil);err!=nil||!bytes.Equal(output.B,[]byte{0,0,0}){t.Fatal(output.B,err)}
 if rows,err:=ReadColumnarUNIT_TYPE(output.B);err!=nil||len(rows)!=0{t.Fatal(rows,err)}
 one,err:=app.one(t.Context());if err!=nil||len(one)!=1{t.Fatal(one,err)}
 output.B=[]byte{7};if err:=WriteColumnarUNIT_TYPE(&output,one,nil);!errors.Is(err,codec.ErrEmptyColumnarProjection)||!bytes.Equal(output.B,[]byte{7}){t.Fatal(output.B,err)}
 request:=&api.Request{Buf:&api.ResponseBuf{}};if err:=handleUnits(app)(t.Context(),request);err!=nil||!bytes.Equal(request.Buf.B,[]byte{0,0,0}){t.Fatal(request.Buf.B,err)}
}
`, "UNIT_TYPE", view.Files[0].Functions[0].ReturnType.Name)
	runGeneratedFixture(t, source, assertions, StableIDs{})
}

func TestGeneratedLocalNativeChannelsKeepTypedABI(t *testing.T) {
	runGeneratedFixture(t, `
fn source(): Result<Channel<Int>> @native
fn relay(ch: Channel<Int>): Result<Int> @native
fn takeValue(ch: Channel<Int>): Int { return <-ch }
fn nativeExchange(): Int { val ch = source()? return relay(ch)? }
api readValue(): Int { val ch: Channel<Int> = Channel(1) ch <- 42 return takeValue(ch) }
fn serviceValue(): Int @service { val ch: Channel<Int> = Channel(1) ch <- 84 return <-ch }
`, `package generated
import("bytes";"context";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type channelResolver struct{}
func(channelResolver)Source(context.Context)(chan int64,error){ch:=make(chan int64,1);ch<-126;return ch,nil}
func(channelResolver)Relay(_ context.Context,ch chan int64)(int64,error){return <-ch,nil}
func TestLocalChannels(t *testing.T){
 app:=&App{Resolver:channelResolver{}}
 if value,err:=app.nativeExchange(t.Context());err!=nil||value!=126{t.Fatal(value,err)}
 for _,sample:=range []struct{handler api.HandlerFunc;value int64}{{handleReadValue(app),42},{_handleService_serviceValue(app),84}}{
  req:=&api.Request{Buf:&api.ResponseBuf{}}
  if err:=sample.handler(t.Context(),req);err!=nil{t.Fatal(err)}
  if !bytes.Equal(req.Buf.B,codec.AppendSvarint(nil,sample.value)){t.Fatalf("local channel changed response: %x",req.Buf.B)}
 }
}
`, StableIDs{})
}

func TestGeneratedGenericValuesCompileAndRoundTrip(t *testing.T) {
	runGeneratedFixture(t, `
type Box<T> { value: T }
type Wrap<T> { box: Box<T> }
type Item { id: Int }
type Never<T> { unused: T }
fn makeNumber(): Box<Int> { return Box<Int>(value: 42) }
fn makeText(): Box<String> { return Box<String>(value: "你好") }
fn makeNullable(): Box<Int?> { return Box<Int?>(value: null) }
fn makeWrap(): Wrap<Int> { return Wrap<Int>(box: makeNumber()) }
fn makePage(): Page<Item> { return Page<Item>(items: [Item { id: 9 }], total: 1, page: 1, pageSize: 20) }
fn makeCursor(): Cursor<Item> { return Cursor<Item>(items: [], nextCursor: null, hasMore: false) }
api echo(value: Box<Int>): Box<Int> { return value }
api probe(): Int { return makeNumber().value }
`, `package generated
import("bytes";"encoding/json";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestGenericValues(t *testing.T){
 app:=&App{}
 number,err:=app.makeNumber(t.Context());if err!=nil||number.Value!=42{t.Fatal(number,err)}
 text,err:=app.makeText(t.Context());if err!=nil||text.Value!="你好"{t.Fatal(text,err)}
 nullable,err:=app.makeNullable(t.Context());if err!=nil||nullable.Value!=nil{t.Fatal(nullable,err)}
 wrap,err:=app.makeWrap(t.Context());if err!=nil||wrap.Box.Value!=42{t.Fatal(wrap,err)}
 page,err:=app.makePage(t.Context());if err!=nil||len(page.Items)!=1||page.Items[0].Id!=9||page.Total!=1||page.Page!=1||page.PageSize!=20{t.Fatal(page,err)}
 cursor,err:=app.makeCursor(t.Context());if err!=nil||len(cursor.Items)!=0||cursor.NextCursor!=nil||cursor.HasMore{t.Fatal(cursor,err)}
 buf:=&api.ResponseBuf{};if err:=number.WriteLuxo(buf,nil);err!=nil{t.Fatal(err)}
 decoded:=number;decoded.Value=0;dec:=codec.NewDecoder(buf.B);decoded.ReadLuxo(dec)
 if err:=dec.Err();err!=nil||decoded.Value!=42{t.Fatal(decoded,err)}
 request:=&api.Request{Params:map[string]json.RawMessage{"value":json.RawMessage(`+"`"+`{"value":42}`+"`"+`)},Buf:&api.ResponseBuf{}}
 if err:=handleEcho(app)(t.Context(),request);err!=nil{t.Fatal(err)}
 if !bytes.Equal(request.Buf.B,buf.B){t.Fatalf("JSON parameter and binary row disagree: %x != %x",request.Buf.B,buf.B)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Box": {"value": 1}, "Wrap": {"box": 1}, "Item": {"id": 1}}})
}
