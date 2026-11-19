package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestNativeEventHandlersCrossModule(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		EventModule: map[string]string{"Changed": "producer"}, ModulePath: "example.com/service",
	}})
	result := &semantic.Result{Files: []*ast.File{{Name: "origin/consumer/events.luxo", Listeners: []*ast.OnDecl{
		{EventName: "Changed", Native: true}, {EventName: "Changed", Native: true, Broadcast: true},
	}}}}
	code := string(g.generateEventFile(result, "luxo"))
	for _, expected := range []string{
		`"fmt"`, `producer_luxo "example.com/service/producer/luxo"`,
		`OnChanged func(context.Context, producer_luxo.ChangedEvent) error`,
		`nativeOnChanged := app.EventHandlers.OnChanged`,
		`func(data []byte) (producer_luxo.ChangedEvent, error)`,
	} {
		if !strings.Contains(code, expected) {
			t.Fatalf("missing %q in generated listener:\n%s", expected, code)
		}
	}
	if strings.Count(code, "OnChanged func(") != 1 || strings.Count(code, "nativeOnChanged :=") != 1 {
		t.Fatal("duplicate native handler declaration")
	}
	app := string(generateAppFile(result, "luxo", nil, DriverPG))
	if !strings.Contains(app, "EventHandlers NativeEventHandlers") || !strings.Contains(app, "EventBus event.Bus") {
		t.Fatalf("listener-only module has no event dependencies:\n%s", app)
	}
}

func TestGeneratedValueDecoderRegistrations(t *testing.T) {
	tokens, lexErrors := lexer.New(eventValueDecoderSource, "events.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("events.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	ids := StableIDs{ModelFields: map[string]map[string]int{"DecodePlain": {"value": 1}}, EventFields: map[string]map[string]int{
		"Plain": {"value": 1}, "Named": {"value": 1}, "Native": {"value": 1}, "Captured": {"value": 1},
	}}
	g := mustNewGenerator(t, GeneratorConfig{IDs: completeFixtureAPIIDs(result, ids)})
	generated, err := g.Generate(result, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, streams := string(generated.Files["event.gen.go"]), string(generated.Files["stream.gen.go"])
	for _, want := range []string{"event.OnQueueValueDecode(", "event.OnValueDecode(", "func(data []byte) (PlainEvent, error)", "func UnmarshalPlain(data []byte, v any) error"} {
		if !strings.Contains(events, want) {
			t.Errorf("missing %q in event source", want)
		}
	}
	if !strings.Contains(streams, "event.OnValueDecode[PlainEvent](") || !strings.Contains(streams, "func(data []byte) (PlainEvent, error)") {
		t.Error("event-backed stream retained pointer-target decoding")
	}
	if strings.Contains(events, "func DecodePlain(") || strings.Contains(events, "func DecodeNamed(") {
		t.Error("value decoder introduced a global name that collides with business declarations")
	}
	if t.Failed() {
		return
	}
	runGeneratedCompilation(t, generated, eventValueDecoderAssertions)
}

const eventValueDecoderSource = `
type DecodePlain { value: Int }
fn DecodeNamed(value: Int): Int { return value }
api decoderNames(DecodePlain: Int, DecodeNamed: Int): Int { return DecodePlain + DecodeNamed }
event Plain(value: Int)
event Named(value: Int)
event Native(value: Int)
event Captured(value: Int)
on Plain { emit Captured(value: it.value) }
on Named @broadcast { emit Captured(value: it.value) }
on Native @native
api watch(): Int @stream(Plain) { true }
`

const eventValueDecoderAssertions = `package generated
import("bytes";"context";"errors";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/event")
type valueDecoderBus struct{event.Bus;handlers map[string][]event.Handler;captured int64;queues []string}
func(b *valueDecoderBus)On(name string,h event.Handler)error{b.handlers[name]=append(b.handlers[name],h);return nil}
func(b *valueDecoderBus)OnQueue(name,group string,h event.Handler)error{b.queues=append(b.queues,group);return b.On(name,h)}
func(b *valueDecoderBus)Emit(_ context.Context,name string,p any)error{if name!="Captured"{return errors.New("unexpected emission")};b.captured=p.(*CapturedEvent).Value;return nil}
func(b *valueDecoderBus)deliver(ctx context.Context,name string,p any)error{for _,h:=range b.handlers[name]{if err:=h(ctx,p);err!=nil{return err}};return nil}
func TestGeneratedValueEventListeners(t *testing.T){
 bus:=&valueDecoderBus{handlers:map[string][]event.Handler{}}
 var native int64;failure:=errors.New("native listener failed")
 app:=&App{EventBus:bus,EventHandlers:NativeEventHandlers{OnNative:func(_ context.Context,p NativeEvent)error{native=p.Value;return failure}}}
 if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{name string;payload any;want int64}{{"Plain",PlainEvent{Value:7},7},{"Named",NamedEvent{Value:9},9}}{
  if err:=bus.deliver(t.Context(),sample.name,sample.payload);err!=nil||bus.captured!=sample.want{t.Fatal(err,bus.captured)}
 }
 plain,err:=(&PlainEvent{Value:13}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 named,err:=(&NamedEvent{Value:17}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{name string;raw []byte;want int64}{{"Plain",plain,13},{"Named",named,17}}{
  if err:=bus.deliver(t.Context(),sample.name,sample.raw);err!=nil||bus.captured!=sample.want{t.Fatal(err,bus.captured)}
 }
 if err:=bus.deliver(t.Context(),"Native",NativeEvent{Value:19});!errors.Is(err,failure)||native!=19{t.Fatal(err,native)}
 raw,err:=(&NativeEvent{Value:23}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 if err:=bus.deliver(t.Context(),"Native",raw);!errors.Is(err,failure)||native!=23{t.Fatal(err,native)}
 for _,name:=range []string{"Plain","Named","Native"}{
  if err:=bus.deliver(t.Context(),name,[]byte{255});err==nil{t.Fatal("malformed wire reached listener",name)}
  if err:=bus.deliver(t.Context(),name,42);!errors.Is(err,event.ErrUnexpectedPayload){t.Fatal(name,err)}
 }
 if bus.captured!=17||native!=23{t.Fatal("rejected payload changed business result")}
 if got,err:=app.DecodeNamed(t.Context(),41);err!=nil||got!=41{t.Fatal(got,err)}
 _=DecodePlain{Value:1}
}
func TestLegacyGeneratedEventDecoderCompatibility(t *testing.T){
 var broadcast func(event.Bus,string,func([]byte,any)error,func(context.Context,PlainEvent)error)error=event.OnDecode[PlainEvent]
 var queue func(event.Bus,string,string,func([]byte,any)error,func(context.Context,PlainEvent)error)error=event.OnQueueDecode[PlainEvent]
 bus:=&valueDecoderBus{handlers:map[string][]event.Handler{}};var got int64
 handler:=func(_ context.Context,p PlainEvent)error{got=p.Value;return nil}
 if err:=broadcast(bus,"old",UnmarshalPlain,handler);err!=nil{t.Fatal(err)}
 if err:=queue(bus,"queue","",UnmarshalPlain,handler);err!=nil{t.Fatal(err)}
 raw,err:=(&PlainEvent{Value:29}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 for _,name:=range []string{"old","queue"}{if err:=bus.deliver(t.Context(),name,raw);err!=nil||got!=29{t.Fatal(err,got)}}
 var decoded PlainEvent;if err:=UnmarshalPlain(raw,&decoded);err!=nil||decoded.Value!=29{t.Fatal(err,decoded)}
 if err:=UnmarshalPlain(raw,&NamedEvent{});err==nil{t.Fatal("legacy target validation removed")}
 if len(bus.queues)!=1||bus.queues[0]!=""{t.Fatal("empty queue group changed route")}
}
func TestGeneratedValueEventStream(t *testing.T){
 bus:=&valueDecoderBus{handlers:map[string][]event.Handler{}};router:=api.NewRouter()
 if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
 sub,err:=router.OpenInternalStream(t.Context(),&api.Request{API:"watch",BinaryMode:true});if err!=nil{t.Fatal(err)};defer sub.Close()
 raw,err:=(&PlainEvent{Value:9}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{payload any;wire []byte}{{PlainEvent{Value:8},[]byte{16}},{raw,[]byte{18}}}{
  if err:=bus.deliver(t.Context(),"Plain",sample.payload);err!=nil{t.Fatal(err)}
  select{case body:=<-sub.Messages():if !bytes.Equal(body,sample.wire){t.Fatalf("stream wire %x, want %x",body,sample.wire)};case <-time.After(time.Second):t.Fatal("stream event missing")}
 }
 if err:=bus.deliver(t.Context(),"Plain",[]byte{255});err==nil{t.Fatal("invalid stream wire accepted")}
 if err:=bus.deliver(t.Context(),"Plain",42);!errors.Is(err,event.ErrUnexpectedPayload){t.Fatal(err)}
}
`

func TestGeneratedValueDecoderOwnership(t *testing.T) {
	files := make([]*ast.File, 0, 2)
	for index, source := range []string{
		"type DecodeRemote { value: Int }\nfn DecodeNativeRemote(value: Int): Int { return value }\nevent Remote(value: Int)\nevent NativeRemote(value: Int)",
		"on Remote {}\non NativeRemote @native @broadcast\napi remoteWatch(): Int @stream(Remote) { true }",
	} {
		name := []string{"origin/producer/events.luxo", "origin/consumer/events.luxo"}[index]
		tokens, lexErrors := lexer.New(source, name).Tokenize()
		file, parseErrors := parser.New(tokens).Parse(name)
		if len(lexErrors)+len(parseErrors) != 0 {
			t.Fatal(lexErrors, parseErrors)
		}
		files = append(files, file)
	}
	result := semantic.AnalyzeModules(files)
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	dir := writeProtocolFixture(t, "package generated\n", "package generated\n")
	module := "github.com/light-speak/luxo/.tmp/" + filepath.Base(dir)
	ids := StableIDs{ModelFields: map[string]map[string]int{"DecodeRemote": {"value": 1}}, EventFields: map[string]map[string]int{
		"Remote": {"value": 1}, "NativeRemote": {"value": 1},
	}}
	g := mustNewGenerator(t, GeneratorConfig{IDs: completeFixtureAPIIDs(result, ids), Events: BuildEventContext(files, module)})
	for index, file := range files {
		generated, err := g.Generate(&semantic.Result{Files: []*ast.File{file}}, "generated", nil)
		if err != nil {
			t.Fatal(err)
		}
		target := dir
		if index == 0 {
			target = filepath.Join(dir, "producer", "luxo")
		} else {
			for _, source := range []string{string(generated.Files["event.gen.go"]), string(generated.Files["stream.gen.go"])} {
				if !strings.Contains(source, "func(data []byte) (producer_luxo.RemoteEvent, error)") {
					t.Fatal("decoder lost its event owner prefix")
				}
			}
		}
		if err := os.MkdirAll(target, 0700); err != nil {
			t.Fatal(err)
		}
		for name, data := range generated.Files {
			if err := os.WriteFile(filepath.Join(target, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertions := strings.ReplaceAll(eventValueOwnerAssertions, "MODULE", module)
	if err := os.WriteFile(filepath.Join(dir, "wire_test.go"), []byte(assertions), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-race", "-count=1", "-timeout=90s", ".")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-module value decoder: %v\n%s", err, output)
	}
}

const eventValueOwnerAssertions = `package generated
import("bytes";"context";"errors";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/event";producer "MODULE/producer/luxo")
type ownerDecoderBus struct{event.Bus;handlers map[string][]event.Handler;group string}
func(b *ownerDecoderBus)On(name string,h event.Handler)error{b.handlers[name]=append(b.handlers[name],h);return nil}
func(b *ownerDecoderBus)OnQueue(name,group string,h event.Handler)error{b.group=group;return b.On(name,h)}
func(b *ownerDecoderBus)deliver(ctx context.Context,name string,p any)error{for _,h:=range b.handlers[name]{if err:=h(ctx,p);err!=nil{return err}};return nil}
func TestOwnedValueDecoders(t *testing.T){
 bus:=&ownerDecoderBus{handlers:map[string][]event.Handler{}};var got int64
 app:=&App{EventBus:bus,EventHandlers:NativeEventHandlers{OnNativeRemote:func(_ context.Context,p producer.NativeRemoteEvent)error{got=p.Value;return nil}}}
 if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 if bus.group!="consumer"{t.Fatal("queue module changed",bus.group)}
 if err:=bus.deliver(t.Context(),"NativeRemote",producer.NativeRemoteEvent{Value:7});err!=nil||got!=7{t.Fatal(err,got)}
 raw,err:=(&producer.NativeRemoteEvent{Value:9}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 if err:=bus.deliver(t.Context(),"NativeRemote",raw);err!=nil||got!=9{t.Fatal(err,got)}
 router:=api.NewRouter();if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
 sub,err:=router.OpenInternalStream(t.Context(),&api.Request{API:"remoteWatch",BinaryMode:true});if err!=nil{t.Fatal(err)};defer sub.Close()
 raw,err=(&producer.RemoteEvent{Value:11}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 if err:=bus.deliver(t.Context(),"Remote",raw);err!=nil{t.Fatal(err)}
 select{case body:=<-sub.Messages():if !bytes.Equal(body,[]byte{22}){t.Fatalf("wire %x",body)};case <-time.After(time.Second):t.Fatal("owned stream event missing")}
 if err:=bus.deliver(t.Context(),"Remote",[]byte{255});err==nil{t.Fatal("bad remote wire accepted")}
 if err:=bus.deliver(t.Context(),"Remote",42);!errors.Is(err,event.ErrUnexpectedPayload){t.Fatal(err)}
 var legacy producer.RemoteEvent;if err:=producer.UnmarshalRemote(raw,&legacy);err!=nil||legacy.Value!=11{t.Fatal(err,legacy)}
 _=producer.DecodeRemote{Value:1};if got,err:=(&producer.App{}).DecodeNativeRemote(t.Context(),13);err!=nil||got!=13{t.Fatal(got,err)}
}
`

func TestEventFieldWritersLeaveIncompleteFieldsUnchanged(t *testing.T) {
	for _, field := range []struct {
		id    int
		param *ast.ParamDecl
	}{
		{0, &ast.ParamDecl{Name: "value", Type: &ast.TypeRef{Name: "Int"}}},
		{1, &ast.ParamDecl{Name: "value"}},
	} {
		var output strings.Builder
		output.WriteString("prefix")
		writeEventMarshalField(&output, field.id, field.param, false, false)
		writeEventUnmarshalField(&output, field.id, field.param, false, false, false)
		if output.String() != "prefix" {
			t.Fatalf("incomplete field emitted partial source: %q", output.String())
		}
	}
}

func TestCrossModuleEventImportsIgnoreBodylessAPI(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		EventModule: map[string]string{"Created": "owner"},
	}})
	result := &semantic.Result{Files: []*ast.File{{APIs: []*ast.ApiDecl{{Name: "listUsers"}}}}}
	if source := g.generateEventFile(result, "consumer"); source != nil {
		t.Fatalf("bodyless API generated event file: %s", source)
	}
}

func TestGenerateEventFileNoEvents(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Name: "test.luxo"}}}
	src := generateEventFile(result, "luxo")
	if src != nil {
		t.Error("should return nil when no events")
	}
}

func TestGeneratedEventDateTimeNanoseconds(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{
		"Scheduled": {"at": 1, "optional": 2, "dates": 3},
	}}})
	event := &ast.EventDecl{Name: "Scheduled", Params: []*ast.ParamDecl{
		{Name: "at", Type: &ast.TypeRef{Name: "DateTime"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}},
		{Name: "dates", Type: &ast.TypeRef{Name: "DateTime", IsList: true}},
	}}
	var source strings.Builder
	source.WriteString("package generated\nimport (\"fmt\"; \"time\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateEventStruct(&source, event)
	generator.generateEventCodec(&source, event, nil, nil)
	runGeneratedProtocolPackage(t, source.String(), eventDateTimeAssertions)
}

const eventDateTimeAssertions = `package generated
import (
    "testing"
    "time"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
func TestEventDateTimeNanoseconds(t *testing.T) {
    values := []time.Time{time.Unix(-1,999999999).UTC(), time.Unix(1789646400,123456789).UTC(), time.Unix(253402300799,1).UTC()}
    for _, value := range values {
        for _, optional := range []*time.Time{nil, &value} {
            original := ScheduledEvent{At:value, Optional:optional, Dates:values}
            data, err := original.MarshalLuxo()
            if err != nil { t.Fatal(err) }
            var got ScheduledEvent
            if err := got.UnmarshalLuxo(data); err != nil { t.Fatal(err) }
            if !got.At.Equal(value) || (got.Optional == nil) != (optional == nil) { t.Fatalf("changed scalar: %+v", got) }
            if optional != nil && !got.Optional.Equal(value) { t.Fatal("changed optional") }
            if len(got.Dates) != len(values) { t.Fatal("changed length") }
            for i, date := range got.Dates { if !date.Equal(values[i]) { t.Fatalf("changed date %d: %v", i, date) } }
        }
    }
    for _, data := range [][]byte{{1,0}, codec.AppendVarint([]byte{1,0},1_000_000_000)} {
        var event ScheduledEvent
        if err := event.UnmarshalLuxo(data); err == nil { t.Fatalf("accepted invalid date: %x",data) }
    }
    for _, seconds := range []int64{-62167219201,253402300800} {
        invalid := time.Unix(seconds,0)
        for _, event := range []ScheduledEvent{{At:invalid},{Optional:&invalid},{Dates:[]time.Time{{},invalid}}} {
            data, err := event.MarshalLuxo()
            if err == nil || data != nil { t.Fatalf("invalid date was encoded: %x, %v",data,err) }
        }
    }
}
`

func TestGeneratedEventFloatErrors(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{
		"Measured": {"value": 1, "optional": 2, "values": 3, "child": 4, "optionalChild": 5, "children": 6},
	}, ModelFields: map[string]map[string]int{"FloatChild": {"value": 1}}}})
	child := &ast.ModelDecl{Name: "FloatChild", Fields: []*ast.FieldDecl{{Name: "value", Type: &ast.TypeRef{Name: "Float"}}}}
	event := &ast.EventDecl{Name: "Measured", Params: []*ast.ParamDecl{
		{Name: "value", Type: &ast.TypeRef{Name: "Float"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "Float", Nullable: true}},
		{Name: "values", Type: &ast.TypeRef{Name: "Float", IsList: true}},
		{Name: "child", Type: &ast.TypeRef{Name: "FloatChild"}},
		{Name: "optionalChild", Type: &ast.TypeRef{Name: "FloatChild", Nullable: true}},
		{Name: "children", Type: &ast.TypeRef{Name: "FloatChild", IsList: true}},
	}}
	var source strings.Builder
	source.WriteString("package generated\nimport (\"fmt\"; \"github.com/light-speak/luxo/pkg/lux/codec\"; \"github.com/light-speak/luxo/pkg/lux/api\"; \"github.com/light-speak/luxo/pkg/lux/event\")\n")
	generateModel(&source, child, nil)
	generator.generateTypeWriteLuxo(&source, child, nil)
	generator.generateReadLuxo(&source, child, nil)
	generator.generateEventObjectCodec(&source, child.Name, child.Fields, nil, map[string]bool{"FloatChild": true})
	generateEventStruct(&source, event)
	generator.generateEventCodec(&source, event, nil, map[string]bool{"FloatChild": true})
	runGeneratedProtocolPackage(t, source.String(), eventFloatAssertions)
}

func TestGeneratedEventNullableReplacement(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		EventFields: map[string]map[string]int{"OptionalPayload": {"amount": 1, "child": 2}},
		ModelFields: map[string]map[string]int{"EventChild": {"value": 1}},
	}})
	child := &ast.ModelDecl{Name: "EventChild", Fields: []*ast.FieldDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}}}
	event := &ast.EventDecl{Name: "OptionalPayload", Params: []*ast.ParamDecl{
		{Name: "amount", Type: &ast.TypeRef{Name: "Decimal", Nullable: true}},
		{Name: "child", Type: &ast.TypeRef{Name: "EventChild", Nullable: true}},
	}}
	var source strings.Builder
	source.WriteString("package generated\nimport (\"github.com/light-speak/luxo/pkg/lux\";\"fmt\";\"github.com/shopspring/decimal\";\"github.com/light-speak/luxo/pkg/lux/codec\";\"github.com/light-speak/luxo/pkg/lux/api\";\"github.com/light-speak/luxo/pkg/lux/event\")\n")
	generateModel(&source, child, nil)
	g.generateTypeWriteLuxo(&source, child, nil)
	g.generateReadLuxo(&source, child, nil)
	g.generateEventObjectCodec(&source, child.Name, child.Fields, nil, map[string]bool{"EventChild": true})
	generateEventStruct(&source, event)
	g.generateEventCodec(&source, event, nil, map[string]bool{"EventChild": true})
	// Retain the exact pre-fix reader only inside the generated benchmark fixture.
	var previous strings.Builder
	generateEventStruct(&previous, event)
	g.generateEventCodec(&previous, event, nil, map[string]bool{"EventChild": true})
	legacy := strings.ReplaceAll(previous.String(), "OptionalPayloadEvent", "LegacyOptionalPayloadEvent")
	legacy = strings.ReplaceAll(legacy, " else { e.Amount = nil }", "")
	legacy = strings.ReplaceAll(legacy, " else { e.Child = nil }", "")
	source.WriteString(legacy)
	runGeneratedProtocolPackage(t, source.String(), eventNullableReplacementAssertions)
}

const eventNullableReplacementAssertions = `package generated
import ("os";"slices";"testing";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux/codec")
func TestNullableReplacement(t *testing.T) {
    amount:=decimal.NewFromInt(42)
    original:=OptionalPayloadEvent{Amount:&amount,Child:&EventChild{Value:42}}
    present,err:=original.MarshalLuxo();if err!=nil {t.Fatal(err)}
    empty,err:=(&OptionalPayloadEvent{}).MarshalLuxo();if err!=nil {t.Fatal(err)}
    var reused OptionalPayloadEvent
    for round:=0;round<3;round++ {
        if err:=reused.UnmarshalLuxo(present);err!=nil {t.Fatal(err)}
        if reused.Amount==nil || !reused.Amount.Equal(amount) || reused.Child==nil || reused.Child.Value!=42 {t.Fatalf("lost present values: %+v",reused)}
        if err:=reused.UnmarshalLuxo(empty);err!=nil {t.Fatal(err)}
        if reused.Amount!=nil || reused.Child!=nil {t.Fatalf("explicit null retained previous values: %+v",reused)}
    }
    combined:=append(append([]byte{},present[:len(present)-1]...),empty...)
    if err:=reused.UnmarshalLuxo(combined);err!=nil {t.Fatal(err)}
    if reused.Amount!=nil || reused.Child!=nil {t.Fatalf("last explicit null was ignored: %+v",reused)}
    legacy:=LegacyOptionalPayloadEvent{Amount:&amount,Child:&EventChild{Value:42}}
    if err:=legacy.UnmarshalLuxo(empty);err!=nil || legacy.Amount==nil || legacy.Child==nil {t.Fatalf("legacy reference no longer reproduces original bug: %+v %v",legacy,err)}
}
func TestMalformedNullableEvent(t *testing.T) {
    var enc codec.Encoder
    enc.WriteFieldHeader(1);enc.WritePresent()
    if err:=enc.WriteString("not-decimal");err!=nil {t.Fatal(err)}
    enc.WriteEnd()
    for _,raw:=range [][]byte{enc.Bytes(),{1,2,0},{2,2,0},{2,1,127,0}} {
        var got OptionalPayloadEvent
        if err:=got.UnmarshalLuxo(raw);err==nil {t.Fatalf("accepted malformed nullable event: %x",raw)}
    }
}
func BenchmarkEventNullable(b *testing.B) {
    amount:=decimal.NewFromInt(42)
    original:=OptionalPayloadEvent{Amount:&amount,Child:&EventChild{Value:42}}
    present,err:=original.MarshalLuxo();if err!=nil {b.Fatal(err)}
    empty,err:=(&OptionalPayloadEvent{}).MarshalLuxo();if err!=nil {b.Fatal(err)}
    mixed:=append(append([]byte{},present[:len(present)-1]...),empty...)
    orders:=[]string{"previous","current"}
    if os.Getenv("LUXO_EVENT_BENCHMARK_REVERSE")=="1" {slices.Reverse(orders)}
    for _,sample:=range []struct{name string;raw []byte;null bool}{{"present",present,false},{"null",empty,true},{"duplicate-null",mixed,true}} {
        for _,version:=range orders {
            b.Run(sample.name+"/"+version,func(b *testing.B) {
                current:=original
                previous:=LegacyOptionalPayloadEvent{Amount:&amount,Child:&EventChild{Value:42}}
                b.ReportAllocs()
                if version=="previous" {
                    for b.Loop() {if err:=previous.UnmarshalLuxo(sample.raw);err!=nil {b.Fatal(err)}}
                    if previous.Amount==nil || previous.Child==nil {b.Fatal("legacy sample changed")}
                } else {
                    for b.Loop() {if err:=current.UnmarshalLuxo(sample.raw);err!=nil {b.Fatal(err)}}
                    if (current.Amount==nil)!=sample.null || (current.Child==nil)!=sample.null {b.Fatal("incorrect nullable result")}
                }
            })
        }
    }
}
`

const eventFloatAssertions = `package generated
import (
    "errors"
    "math"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
var _ codec.LuxoMarshaler = (*MeasuredEvent)(nil)
func TestEventFloatErrors(t *testing.T) {
    for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
        for _, event := range []MeasuredEvent{
            {Value:value}, {Optional:&value}, {Values:[]float64{1,value,2}},
            {Child:FloatChild{Value:value}}, {OptionalChild:&FloatChild{Value:value}},
            {Children:[]FloatChild{{Value:1},{Value:value}}},
        } {
            data, err := event.MarshalLuxo()
            if !errors.Is(err, codec.ErrNonFiniteFloat) || data != nil { t.Fatalf("invalid event: %x, %v", data, err) }
        }
    }
}
func TestEventFiniteBits(t *testing.T) {
    negativeZero := math.Copysign(0,-1)
    event := MeasuredEvent{Value:math.MaxFloat64, Optional:&negativeZero, Values:[]float64{math.SmallestNonzeroFloat64,negativeZero}}
    data, err := event.MarshalLuxo()
    if err != nil { t.Fatal(err) }
    var decoded MeasuredEvent
    if err := decoded.UnmarshalLuxo(data); err != nil { t.Fatal(err) }
    if decoded.Value != math.MaxFloat64 || decoded.Optional == nil || math.Float64bits(*decoded.Optional) != 1<<63 { t.Fatalf("changed scalar: %+v", decoded) }
    if len(decoded.Values) != 2 || math.Float64bits(decoded.Values[0]) != 1 || math.Float64bits(decoded.Values[1]) != 1<<63 { t.Fatalf("changed array: %+v", decoded.Values) }
}
`

func TestEventRegistrationPropagatesSubscriptionErrors(t *testing.T) {
	var b strings.Builder
	g := &GeneratorContext{}
	g.generateRegisterEvents(&b, []*ast.OnDecl{{EventName: "Completed"}}, "activity", "activity", nil, nil, &semantic.Result{})
	for _, expected := range []string{"func RegisterEvents(bus event.Bus, app *App) error", "if err := event.OnQueueValueDecode", "}); err != nil {", "return err"} {
		if !strings.Contains(b.String(), expected) {
			t.Errorf("missing %q in generated registration", expected)
		}
	}
}

func TestEventListenerIncludesExtendedModelContext(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name:    "origin/post/event.luxo",
		Extends: []*ast.ExtendDecl{{Name: "User", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}},
		Events:  []*ast.EventDecl{{Name: "Published", Params: []*ast.ParamDecl{{Name: "authorId", Type: &ast.TypeRef{Name: "Int"}}}}},
		Listeners: []*ast.OnDecl{{
			Pos: token.Position{File: "origin/post/event.luxo"}, EventName: "Published", Params: []string{"e"},
			Body: &ast.Block{Stmts: []ast.Stmt{&ast.ValStmt{Name: "_", Value: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "load"},
				Args: []*ast.NamedArg{{Value: &ast.MemberExpr{Object: &ast.Ident{Name: "e"}, Field: "authorId"}}},
			}}}},
		}},
	}}}
	g := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{ModelModule: map[string]string{"User": "user"}}})
	code := string(g.generateEventFile(result, "luxo"))
	if !strings.Contains(code, "app.loaders.ExtendUser.Load(ctx, e.AuthorId,") || strings.Contains(code, " := User.Load(") {
		t.Fatalf("listeners must compile extend loads through typed loaders:\n%s", code)
	}
	if !strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/selection"`) || strings.Contains(code, "e.AuthorId, nil)") {
		t.Fatalf("discarded remote loads must use an explicit key-only selection:\n%s", code)
	}
}

func TestEventJSONFieldRequirement(t *testing.T) {
	events := []*ast.EventDecl{{Name: "Changed", Params: []*ast.ParamDecl{
		{Name: "unknown", Type: &ast.TypeRef{Name: "LegacyPayload"}},
		{Name: "metadata", Type: &ast.TypeRef{Name: "JSON"}},
		{Name: "items", Type: &ast.TypeRef{Name: "String", IsList: true}},
	}}}
	code := string(generateEventFile(result(&ast.File{Events: events}), "generated"))
	if !strings.Contains(code, `"encoding/json"`) {
		t.Fatal("JSON event fields require their concrete type import")
	}
	if !isEventBuiltin("JSON") || isEventBuiltin("LegacyPayload") {
		t.Fatal("event builtin classification is incorrect")
	}
}

func TestEventObjectCollectionAndJSONRequirements(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Models: []*ast.ModelDecl{{Name: "User"}},
		Types:  []*ast.TypeDecl{{Name: "Payload"}},
	}}}
	objects := collectEventObjectTypes(result)
	if !objects["User"] || !objects["Payload"] {
		t.Fatalf("event object types = %v", objects)
	}
	events := []*ast.EventDecl{{Params: []*ast.ParamDecl{{Name: "payload", Type: &ast.TypeRef{Name: "Payload"}}}}}
	if !eventsUseObjects(events, objects) {
		t.Fatal("type declarations must be treated as event objects")
	}
	if eventsUseObjects([]*ast.EventDecl{{Params: []*ast.ParamDecl{{Name: "unknown"}}}}, objects) {
		t.Fatal("an unresolved event parameter must not enable an object fallback")
	}
}

func TestEventScalarWriteStatements(t *testing.T) {
	tests := []struct {
		name, typeName, want string
		isEnum               bool
	}{
		{name: "enum", typeName: "Role", isEnum: true, want: "if !value.IsValid() { return nil, codec.ErrInvalidEnum }; if err := enc.WriteString(string(value)); err != nil { return nil, err }"},
		{name: "int", typeName: "Int", want: "enc.WriteInt(value)"},
		{name: "float", typeName: "Float", want: "if err := enc.WriteFloat(value); err != nil { return nil, err }"},
		{name: "string", typeName: "String", want: "if err := enc.WriteString(value); err != nil { return nil, err }"},
		{name: "boolean", typeName: "Boolean", want: "enc.WriteBool(value)"},
		{name: "datetime", typeName: "DateTime", want: "if err := enc.WriteDateTime(value); err != nil { return nil, err }"},
		{name: "duration", typeName: "Duration", want: "enc.WriteInt(int64(value))"},
		{name: "uuid", typeName: "UUID", want: "enc.WriteUUID([16]byte(value))"},
		{name: "decimal", typeName: "Decimal", want: "if err := enc.WriteString(lux.FormatDecimal(value)); err != nil { return nil, err }"},
		{name: "bytes", typeName: "Bytes", want: "enc.WriteBytes(value)"},
		{name: "json", typeName: "JSON", want: "if err := enc.WriteJSON(value); err != nil { return nil, err }"},
		{name: "unknown", typeName: "Unknown", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eventScalarWriteStatement(tt.typeName, "value", tt.isEnum); got != tt.want {
				t.Fatalf("write statement = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEventScalarReadExpressions(t *testing.T) {
	tests := []struct {
		typeName string
		isEnum   bool
		want     string
	}{
		{typeName: "Int", want: "dec.ReadIntPtr()"},
		{typeName: "Float", want: "dec.ReadFloatPtr()"},
		{typeName: "String", want: "dec.ReadStringPtr()"},
		{typeName: "Boolean", want: "dec.ReadBoolPtr()"},
		{typeName: "Bytes", want: "dec.ReadBytesValuePtr()"},
		{typeName: "JSON", want: "json.RawMessage"},
		{typeName: "Duration", want: "time.Duration(*raw)"},
		{typeName: "DateTime", want: "dec.ReadDateTimePtr()"},
		{typeName: "UUID", want: "uuid.UUID(*raw)"},
		{typeName: "Role", isEnum: true, want: "Role(*raw)"},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			got := eventScalarReadExpression(tt.typeName, tt.isEnum, true)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("nullable read expression = %q, want %q", got, tt.want)
			}
		})
	}
	if got := eventScalarReadExpression("Role", true, false); got != "Role(dec.ReadString())" {
		t.Fatalf("enum read expression = %q", got)
	}
	if got := eventScalarReadExpression("Bytes", false, false); got != "dec.ReadBytes()" {
		t.Fatalf("bytes read expression = %q", got)
	}
	if got := eventScalarReadExpression("Unknown", false, false); got != "" {
		t.Fatalf("unknown read expression = %q", got)
	}
}

func TestEventCodecDoesNotGenerateJSONFallbacks(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{"Changed": {"payload": 1}}}})
	param := &ast.ParamDecl{Name: "payload", Type: &ast.TypeRef{Name: "External"}}
	var b strings.Builder
	generator.writeEventMarshalField(&b, "Changed", param, false, false)
	generator.writeEventUnmarshalField(&b, "Changed", param, false, false)
	writeEventListMarshal(&b, 2, "e.Payloads", &ast.TypeRef{Name: "External", IsList: true}, false, false)
	writeEventListUnmarshal(&b, 2, "e.Payloads", &ast.TypeRef{Name: "External", IsList: true}, false, false)
	for _, forbidden := range []string{"json.Marshal", "json.Unmarshal"} {
		if strings.Contains(b.String(), forbidden) {
			t.Fatalf("invalid event types must not generate reflection fallback %q:\n%s", forbidden, b.String())
		}
	}
}

func TestEventCodecNullableObjectAndDecimalWriters(t *testing.T) {
	var b strings.Builder
	writeEventObjectMarshal(&b, 1, "e.Payload", true)
	writeEventObjectUnmarshal(&b, 1, "e.Payload", &ast.TypeRef{Name: "Payload", Nullable: true})
	writeEventDecimalUnmarshal(&b, "e.Amount", true)
	writeEventListUnmarshal(&b, 2, "e.Amounts", &ast.TypeRef{Name: "Decimal", IsList: true}, false, false)
	for _, want := range []string{"e.Payload == nil", "enc.WritePresent()", "e.Payload = &Payload{}", "dec.ReadStringPtr()", "decimal.NewFromString(dec.ReadString())"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("nullable/object codec missing %q:\n%s", want, b.String())
		}
	}
}

func TestGenerateEventFile(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{
					Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name: "OrderCreated",
					Params: []*ast.ParamDecl{
						{Name: "order", Type: &ast.TypeRef{Name: "Order"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					},
				},
			},
			Listeners: []*ast.OnDecl{
				{
					Pos:       token.Position{File: "test.luxo", Line: 5, Col: 1},
					EventName: "OrderCreated",
					Params:    []string{"e"},
				},
			},
		}},
	}

	src := generateEventFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate event file")
	}
	code := string(src)

	checks := []string{
		"type OrderCreatedEvent struct",
		`Order Order`,
		`UserId int64`,
		"func EmitOrderCreated(ctx context.Context, bus event.Bus, e OrderCreatedEvent) error",
		`bus.Emit(ctx, "OrderCreated", &e)`,
		"func (e *OrderCreatedEvent) MarshalLuxo() ([]byte, error)",
		"event OrderCreated: expected *OrderCreatedEvent, got %T",
		"func RegisterEvents(bus event.Bus, app *App)",
		`event.OnQueueValueDecode(bus, "OrderCreated", "test", func(data []byte) (OrderCreatedEvent, error)`,
	}
	for _, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing %q in:\n%s", check, code)
		}
	}

	// Should NOT have raw type switch pattern (replaced by generic helpers)
	if strings.Contains(code, "payload.(type)") {
		t.Errorf("should not have raw type switch, use generic helpers:\n%s", code)
	}
}

func TestGenerateEventFileNoListeners(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{
					Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name: "UserDeleted",
					Params: []*ast.ParamDecl{
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					},
				},
			},
		}},
	}

	src := generateEventFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate even without listeners")
	}
	code := string(src)

	if !strings.Contains(code, "EmitUserDeleted") {
		t.Error("should have emit function")
	}
	if !strings.Contains(code, "func RegisterEvents") {
		t.Error("should have RegisterEvents (empty)")
	}
}

func TestGenerateEventListenerBroadcast(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{Name: "ConfigChanged"},
			},
			Listeners: []*ast.OnDecl{
				{EventName: "ConfigChanged", Broadcast: true},
			},
		}},
	}

	src := generateEventFile(result, "mymodule")
	code := string(src)

	// @broadcast should use the value-decoding broadcast entrypoint.
	if !strings.Contains(code, `event.OnValueDecode(bus, "ConfigChanged"`) {
		t.Errorf("@broadcast should use event.OnValueDecode:\n%s", code)
	}
	if strings.Contains(code, `event.OnQueueValueDecode`) {
		t.Errorf("@broadcast should not use event.OnQueueValueDecode:\n%s", code)
	}
}

func TestGenerateEventListenerMixed(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{Name: "OrderCreated"},
				{Name: "CacheInvalidate"},
			},
			Listeners: []*ast.OnDecl{
				{EventName: "OrderCreated", Params: []string{"e"}},                     // default: queue
				{EventName: "CacheInvalidate", Broadcast: true, Params: []string{"e"}}, // broadcast
			},
		}},
	}

	src := generateEventFile(result, "order")
	code := string(src)

	if !strings.Contains(code, `event.OnQueueValueDecode(bus, "OrderCreated", "test"`) {
		t.Errorf("default listener should use OnQueueValueDecode:\n%s", code)
	}
	if !strings.Contains(code, `event.OnValueDecode(bus, "CacheInvalidate"`) {
		t.Errorf("@broadcast listener should use OnValueDecode:\n%s", code)
	}
}

func TestGenerateEventCodecWithFieldIDs(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{
		"OrderCreated": {"orderId": 1, "amount": 2, "note": 3, "paid": 4},
	}}})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{
					Name: "OrderCreated",
					Params: []*ast.ParamDecl{
						{Name: "orderId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "amount", Type: &ast.TypeRef{Name: "Float"}},
						{Name: "note", Type: &ast.TypeRef{Name: "String"}},
						{Name: "paid", Type: &ast.TypeRef{Name: "Boolean"}},
					},
				},
			},
		}},
	}

	src := generator.generateEventFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate event file")
	}
	code := string(src)

	// MarshalLuxo should encode all fields
	if !strings.Contains(code, "enc.WriteFieldHeader(1)") || !strings.Contains(code, "enc.WriteInt(e.OrderId)") {
		t.Errorf("missing canonical Int encoding for orderId:\n%s", code)
	}
	if !strings.Contains(code, "enc.WriteFieldHeader(2)") || !strings.Contains(code, "enc.WriteFloat(e.Amount)") {
		t.Errorf("missing canonical Float encoding for amount:\n%s", code)
	}
	if !strings.Contains(code, "enc.WriteFieldHeader(3)") || !strings.Contains(code, "enc.WriteString(e.Note)") {
		t.Errorf("missing canonical String encoding for note:\n%s", code)
	}
	if !strings.Contains(code, "enc.WriteFieldHeader(4)") || !strings.Contains(code, "enc.WriteBool(e.Paid)") {
		t.Errorf("missing canonical Boolean encoding for paid:\n%s", code)
	}

	// UnmarshalLuxo should decode all fields
	if !strings.Contains(code, "case 1:") || !strings.Contains(code, "e.OrderId = dec.ReadInt()") {
		t.Errorf("missing ReadInt for orderId:\n%s", code)
	}
	if !strings.Contains(code, "case 2:") || !strings.Contains(code, "e.Amount = dec.ReadFloat()") {
		t.Errorf("missing ReadFloat for amount:\n%s", code)
	}
	if !strings.Contains(code, "case 3:") || !strings.Contains(code, "e.Note = dec.ReadString()") {
		t.Errorf("missing ReadString for note:\n%s", code)
	}
	if !strings.Contains(code, "case 4:") || !strings.Contains(code, "e.Paid = dec.ReadBool()") {
		t.Errorf("missing ReadBool for paid:\n%s", code)
	}
	if !strings.Contains(code, "seenOrderId = true") || !strings.Contains(code, "if !seenOrderId") {
		t.Errorf("required event fields must be presence-checked:\n%s", code)
	}
	if !strings.Contains(code, "default:") || !strings.Contains(code, "dec.SkipField()") {
		t.Errorf("unknown event fields must fail instead of corrupting decoder state:\n%s", code)
	}
}

func TestGenerateEventCodecNullableTypes(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{
		"DataEvent": {"amount": 1, "note": 2, "active": 3},
	}}})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{
					Name: "DataEvent",
					Params: []*ast.ParamDecl{
						{Name: "amount", Type: &ast.TypeRef{Name: "Float", Nullable: true}},
						{Name: "note", Type: &ast.TypeRef{Name: "String", Nullable: true}},
						{Name: "active", Type: &ast.TypeRef{Name: "Boolean", Nullable: true}},
					},
				},
			},
		}},
	}

	src := generator.generateEventFile(result, "luxo")
	code := string(src)

	for _, want := range []string{
		"if e.Amount == nil { enc.WriteNull() } else {",
		"enc.WriteFloat((*e.Amount))",
		"enc.WriteString((*e.Note))",
		"enc.WriteBool((*e.Active))",
		"e.Amount = dec.ReadFloatPtr()",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("nullable codec missing %q:\n%s", want, code)
		}
	}
}

func TestGenerateEventCodecComplexType(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{
		"ComplexEvent": {"data": 1},
	}}})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name:   "Order",
				Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
			}},
			Events: []*ast.EventDecl{
				{
					Name: "ComplexEvent",
					Params: []*ast.ParamDecl{
						{Name: "data", Type: &ast.TypeRef{Name: "Order"}},
					},
				},
			},
		}},
	}

	src := generator.generateEventFile(result, "luxo")
	code := string(src)

	if !strings.Contains(code, "e.Data.marshalEventSnapshot(buf.B, depth + 1)") || !strings.Contains(code, "e.Data.unmarshalEventSnapshot(raw, depth + 1)") {
		t.Errorf("snapshot event payload should use independent Luxo binary:\n%s", code)
	}
	if strings.Contains(code, "json.Marshal(e.Data)") {
		t.Errorf("model event payload must not embed JSON:\n%s", code)
	}
}

func TestGenerateEventListenerNoParams(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{Name: "Ping"},
			},
			Listeners: []*ast.OnDecl{
				{EventName: "Ping"}, // no params
			},
		}},
	}

	src := generateEventFile(result, "luxo")
	code := string(src)

	// Should default to "payload" as param name
	if !strings.Contains(code, "payload PingEvent") {
		t.Errorf("should default to 'payload' param:\n%s", code)
	}
	// Default listeners retain queue delivery with a concrete decoder.
	if !strings.Contains(code, `event.OnQueueValueDecode(bus, "Ping", "test"`) {
		t.Errorf("default should use OnQueueValueDecode:\n%s", code)
	}
}

func TestGenerateEventFileOnBody(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{
					Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name: "ProjectDeleted",
					Params: []*ast.ParamDecl{
						{Name: "projectId", Type: &ast.TypeRef{Name: "Int"}},
					},
				},
			},
			Models: []*ast.ModelDecl{
				{Name: "Trace", Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "projectId", Type: &ast.TypeRef{Name: "Int"}},
				}},
			},
			Listeners: []*ast.OnDecl{
				{
					Pos:       token.Position{File: "test.luxo", Line: 5, Col: 1},
					EventName: "ProjectDeleted",
					Params:    []string{"ev"},
					Body: &ast.Block{
						Stmts: []ast.Stmt{
							&ast.ExprStmt{Expr: &ast.CallExpr{
								Func: &ast.MemberExpr{
									Object: &ast.CallExpr{
										Func: &ast.MemberExpr{
											Object: &ast.MemberExpr{
												Object: &ast.Ident{Name: "Trace"},
												Field:  "where",
											},
											Field: "deleteMany",
										},
									},
								},
							}},
						},
					},
				},
			},
		}},
	}

	src := generateEventFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate event file")
	}
	code := string(src)

	if !strings.Contains(code, "func RegisterEvents(bus event.Bus, app *App)") {
		t.Errorf("missing RegisterEvents with app: %s", code)
	}
	if strings.Contains(code, "_ = ev") {
		t.Errorf("on body should be compiled, not empty: %s", code)
	}
}

func TestCollectCrossModuleEventImports(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		EventModule: map[string]string{"ProjectDeleted": "common"},
		ModulePath:  "github.com/test/service",
	}})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name:      "origin/monitoring/trace.luxo",
			Listeners: []*ast.OnDecl{{EventName: "ProjectDeleted"}},
			APIs: []*ast.ApiDecl{{
				Name: "deleteProject",
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.EmitStmt{EventName: "ProjectDeleted"},
				}},
			}},
		}},
	}
	listeners := result.Files[0].Listeners

	imports := generator.collectCrossModuleEventImports(listeners, "monitoring")
	if imports["common"] != "common_luxo" {
		t.Errorf("expected common_luxo import, got %v", imports)
	}
}

func TestCollectCrossModuleEventImports_NoContext(t *testing.T) {
	imports := defaultGenerator().collectCrossModuleEventImports(nil, "test")
	if len(imports) != 0 {
		t.Error("should return empty without context")
	}
}

func TestGenerateEventFileCrossModule(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		EventModule: map[string]string{"ProjectDeleted": "common"},
		ModulePath:  "github.com/test/service",
	}})

	// Module with listener for cross-module event (no local events)
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/monitoring/trace.luxo",
			Listeners: []*ast.OnDecl{{
				EventName: "ProjectDeleted",
				Params:    []string{"ev"},
				Body:      &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Literal{Kind: token.Int, Value: "1"}}}},
			}},
			Models: []*ast.ModelDecl{{Name: "Trace", Fields: []*ast.FieldDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			}}},
		}},
	}

	src := generator.generateEventFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate event file for cross-module listener")
	}
	code := string(src)

	if !strings.Contains(code, `common_luxo "github.com/test/service/common/luxo"`) {
		t.Errorf("should import common_luxo:\n%s", code)
	}
	if !strings.Contains(code, "common_luxo.ProjectDeletedEvent") {
		t.Errorf("should use cross-module event type:\n%s", code)
	}
	if !strings.Contains(code, "func(data []byte) (common_luxo.ProjectDeletedEvent, error)") {
		t.Errorf("should use the event owner's concrete decoder:\n%s", code)
	}
}

func TestGenerateEventFileNeedsTime(t *testing.T) {
	// Listener body uses now() → should import "time"
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{Name: "SessionExpired"},
			},
			Listeners: []*ast.OnDecl{
				{
					EventName: "SessionExpired",
					Params:    []string{"ev"},
					Body: &ast.Block{Stmts: []ast.Stmt{
						&ast.ValStmt{
							Name:  "t",
							Value: &ast.CallExpr{Func: &ast.Ident{Name: "now"}},
						},
					}},
				},
			},
		}},
	}

	src := generateEventFile(result, "auth")
	if src == nil {
		t.Fatal("should generate event file")
	}
	code := string(src)

	if !strings.Contains(code, `"time"`) {
		t.Errorf("listener with now() should import time:\n%s", code)
	}
}

func TestCrossModuleEventImportsFollowListenerBodies(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{EventModule: map[string]string{"Incoming": "source", "Outgoing": "target", "APIOnly": "other"}}})
	listener := &ast.OnDecl{EventName: "Incoming", Body: &ast.Block{Stmts: []ast.Stmt{&ast.IfStmt{Condition: &ast.Literal{Kind: token.True, Value: "true"}, Then: &ast.Block{Stmts: []ast.Stmt{&ast.EmitStmt{EventName: "Outgoing"}}}}}}}
	result := &semantic.Result{Files: []*ast.File{{APIs: []*ast.ApiDecl{{Body: &ast.Block{Stmts: []ast.Stmt{&ast.EmitStmt{EventName: "APIOnly"}}}}}}}}
	result.Files[0].Name = "origin/consumer/events.luxo"
	result.Files[0].Listeners = []*ast.OnDecl{listener}
	code := string(g.generateEventFile(result, "consumer"))
	if strings.Contains(code, "other_luxo") {
		t.Fatalf("API-only import in event file: %s", code)
	}
	for _, want := range []string{"source_luxo.IncomingEvent", "target_luxo.EmitOutgoing"} {
		if !strings.Contains(code, want) {
			t.Fatalf("missing %s in %s", want, code)
		}
	}
	imports := g.collectCrossModuleEventImports([]*ast.OnDecl{listener}, "consumer")
	if imports["source"] != "source_luxo" || imports["target"] != "target_luxo" {
		t.Fatalf("missing listener imports: %v", imports)
	}
	if _, ok := imports["other"]; ok {
		t.Fatalf("API-only import leaks into event file: %v", imports)
	}
}

func TestGeneratedNativeListenerOnlyModule(t *testing.T) {
	dir := writeProtocolFixture(t, "package generated\n", "package generated\n")
	modulePath := "github.com/light-speak/luxo/.tmp/" + filepath.Base(dir)
	files := []*ast.File{
		{Name: "origin/source/events.luxo", Events: []*ast.EventDecl{{Name: "Incoming"}}},
		{Name: "origin/consumer/events.luxo", Listeners: []*ast.OnDecl{{EventName: "Incoming", Native: true}}},
	}
	analyzed := semantic.AnalyzeModules(files)
	if len(analyzed.Errors) != 0 {
		t.Fatal(analyzed.Errors)
	}
	generator := mustNewGenerator(t, GeneratorConfig{Events: BuildEventContext(files, modulePath)})
	for index, file := range files {
		generated, err := generator.Generate(&semantic.Result{Files: []*ast.File{file}}, "generated", nil)
		if err != nil {
			t.Fatal(err)
		}
		target := dir
		if index == 0 {
			target = filepath.Join(dir, "source", "luxo")
		}
		if err := os.MkdirAll(target, 0700); err != nil {
			t.Fatal(err)
		}
		for name, data := range generated.Files {
			if err := os.WriteFile(filepath.Join(target, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertions := strings.ReplaceAll(`package generated
import("context";"testing";"time";"github.com/light-speak/luxo/pkg/lux/event";source "MODULE/source/luxo")
func TestNativeListenerOnly(t *testing.T){
 bus:=event.NewChanBus(4);defer bus.Close()
 app:=&App{EventBus:bus}
 if err:=RegisterEvents(bus,app);err==nil{t.Fatal("missing callback accepted")}
 received:=make(chan struct{},1)
 app.EventHandlers.OnIncoming=func(context.Context,source.IncomingEvent)error{received<-struct{}{};return nil}
 if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 if err:=source.EmitIncoming(t.Context(),bus,source.IncomingEvent{});err!=nil{t.Fatal(err)}
 select{case <-received:case <-time.After(time.Second):t.Fatal("native listener not invoked")}
}
`, "MODULE", modulePath)
	if err := os.WriteFile(filepath.Join(dir, "wire_test.go"), []byte(assertions), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-race", "-count=1", "-timeout=30s", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native listener-only module: %v\n%s", err, out)
	}
}

func TestGeneratedCrossModuleListenerForwarding(t *testing.T) {
	dir := writeProtocolFixture(t, "package generated\n", "package generated\n")
	modulePath := "github.com/light-speak/luxo/.tmp/" + filepath.Base(dir)
	context := &EventContext{ModulePath: modulePath, EventModule: map[string]string{"Incoming": "source", "Outgoing": "target", "APIOnly": "other"}}
	generator := mustNewGenerator(t, GeneratorConfig{Events: context})
	for _, item := range []struct{ module, event string }{{"source", "Incoming"}, {"target", "Outgoing"}} {
		result := &semantic.Result{Files: []*ast.File{{Name: "origin/" + item.module + "/events.luxo", Events: []*ast.EventDecl{{Name: item.event}}}}}
		path := filepath.Join(dir, item.module, "luxo")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "event.gen.go"), generator.generateEventFile(result, "generated"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "app.go"), []byte("package generated\ntype App struct{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	listener := &ast.OnDecl{EventName: "Incoming", Body: &ast.Block{Stmts: []ast.Stmt{&ast.IfStmt{Condition: &ast.Literal{Kind: token.True, Value: "true"}, Then: &ast.Block{Stmts: []ast.Stmt{&ast.EmitStmt{EventName: "Outgoing"}}}}}}}
	result := &semantic.Result{Files: []*ast.File{{Name: "origin/consumer/events.luxo", Listeners: []*ast.OnDecl{listener}, APIs: []*ast.ApiDecl{{Body: &ast.Block{Stmts: []ast.Stmt{&ast.EmitStmt{EventName: "APIOnly"}}}}}}}}
	if err := os.WriteFile(filepath.Join(dir, "event.gen.go"), generator.generateEventFile(result, "generated"), 0600); err != nil {
		t.Fatal(err)
	}
	assertion := strings.ReplaceAll(crossModuleListenerAssertions, "MODULE", modulePath)
	if err := os.WriteFile(filepath.Join(dir, "wire_test.go"), []byte(assertion), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-race", "-count=1", "-timeout=30s", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-module listener: %v\n%s", err, out)
	}
}

const crossModuleListenerAssertions = `package generated
import("context";"testing";"time";"github.com/light-speak/luxo/pkg/lux/event";source "MODULE/source/luxo";target "MODULE/target/luxo")
type App struct{EventBus event.Bus}
func TestForwarding(t *testing.T){
 bus:=event.NewChanBus(4);defer bus.Close();received:=make(chan struct{},1)
 if err:=event.OnDecode(bus,"Outgoing",target.UnmarshalOutgoing,func(context.Context,target.OutgoingEvent)error{received<-struct{}{};return nil});err!=nil{t.Fatal(err)}
 if err:=RegisterEvents(bus,&App{EventBus:bus});err!=nil{t.Fatal(err)}
 if err:=source.EmitIncoming(t.Context(),bus,source.IncomingEvent{});err!=nil{t.Fatal(err)}
 select{case <-received:case <-time.After(5*time.Second):t.Fatal("missing cross-module event")}
}
`
