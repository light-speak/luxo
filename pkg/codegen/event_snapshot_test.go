package codegen

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestGeneratedEventSnapshotMatrix(t *testing.T) {
	object := &ast.TypeDecl{Name: "MatrixSnapshot"}
	var values strings.Builder
	values.WriteString("func matrixFixture() MatrixSnapshot { return MatrixSnapshot{\n")
	for _, sample := range []struct{ kind, value string }{
		{"Int", "int64(42)"}, {"Float", "1.5"}, {"String", `"hello"`}, {"Boolean", "true"},
		{"DateTime", "time.Unix(-1,123456789).UTC()"}, {"Duration", "time.Second"},
		{"UUID", `uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")`},
		{"Decimal", `decimal.RequireFromString("12345678901234567890.12345")`},
		{"JSON", `json.RawMessage("null")`}, {"Bytes", "[]byte{1,2}"}, {"State", "StateOPEN"},
	} {
		for _, shape := range []string{"value", "list", "optional", "optionalList"} {
			name := shape + sample.kind
			ref := &ast.TypeRef{Name: sample.kind, IsList: shape == "list" || shape == "optionalList", Nullable: strings.HasPrefix(shape, "optional")}
			object.Fields = append(object.Fields, &ast.FieldDecl{Name: name, Type: ref})
			value := sample.value
			if ref.IsList {
				value = resolveGoType(ref) + "{" + value + "}"
			} else if ref.Nullable {
				value = "pointer(" + value + ")"
			}
			fmt.Fprintf(&values, "%s: %s,\n", strings.ToUpper(name[:1])+name[1:], value)
		}
	}
	values.WriteString("} }\n")
	file := &ast.File{Types: []*ast.TypeDecl{object}, Enums: []*ast.EnumDecl{{Name: "State", Values: []string{"OPEN", "CLOSED"}}},
		Events: []*ast.EventDecl{{Name: "Matrix", Params: []*ast.ParamDecl{{Name: "payload", Type: &ast.TypeRef{Name: "MatrixSnapshot"}}}}}}
	runGeneratedProtocolPackage(t, generatedSnapshotSource(t, file), eventSnapshotMatrixAssertions+values.String())
}

func TestGeneratedEventSnapshot(t *testing.T) {
	const input = `
enum State { OPEN CLOSED }
type DetailSnapshot { label: String }
type ChildSnapshot { name: String bytes: Bytes metadata: JSON names: [String] optional: String? detail: DetailSnapshot details: [DetailSnapshot] }
event Probe(state: State, score: Float, text: String, at: DateTime, metadata: JSON, names: [String], bytes: Bytes, child: ChildSnapshot?)
event Scalar(id: Int, name: String)
`
	tokens, lexErrors := lexer.New(input, "snapshot.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("snapshot.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	// API presentation directives must not change the explicit event contract.
	file.Types[1].Fields[0].Directives = []*ast.Directive{{Name: "hidden"}}
	file.Types[1].Fields[1].Directives = []*ast.Directive{{Name: "internal"}}
	ensureGeneratedNATS(t)
	runGeneratedProtocolPackage(t, generatedSnapshotSource(t, file), eventSnapshotAssertions+eventSnapshotBenchmarks)
}

// Explicit test endpoints remain authoritative. Without one, own an isolated
// server so generated child packages cannot silently skip protocol assertions.
func ensureGeneratedNATS(t *testing.T) {
	t.Helper()
	if os.Getenv("NATS_URL") != "" {
		return
	}
	container, err := testcontainers.GenericContainer(t.Context(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "nats:2.10-alpine", ExposedPorts: []string{"4222/tcp"},
			Cmd: []string{"-js"}, WaitingFor: wait.ForListeningPort("4222/tcp"),
		}, Started: true,
	})
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	host, err := container.Host(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(t.Context(), "4222/tcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NATS_URL", "nats://"+net.JoinHostPort(host, port.Port()))
}

func TestGeneratedNATSRespectsConfiguredEndpoint(t *testing.T) {
	const endpoint = "nats://127.0.0.1:1"
	t.Setenv("NATS_URL", endpoint)
	ensureGeneratedNATS(t)
	if got := os.Getenv("NATS_URL"); got != endpoint {
		t.Fatalf("explicit test endpoint was replaced: %q", got)
	}
}

func generatedSnapshotSource(t *testing.T, file *ast.File) string {
	ids := StableIDs{EventFields: make(map[string]map[string]int), ModelFields: make(map[string]map[string]int)}
	for _, declaration := range file.Events {
		ids.EventFields[declaration.Name] = make(map[string]int)
		for i, param := range declaration.Params {
			ids.EventFields[declaration.Name][param.Name] = i + 1
		}
	}
	for _, declaration := range file.Types {
		ids.ModelFields[declaration.Name] = make(map[string]int)
		for i, field := range declaration.Fields {
			ids.ModelFields[declaration.Name][field.Name] = i + 1
		}
	}
	g := mustNewGenerator(t, GeneratorConfig{IDs: ids})
	var source strings.Builder
	source.Write(g.generateEventFile(result(file), "generated"))
	source.WriteString("type App struct{}\n")
	enums := CollectEnumsFromResult(result(file))
	for _, declaration := range file.Enums {
		generateEnum(&source, declaration)
	}
	for _, declaration := range file.Types {
		generateTypeStruct(&source, declaration, enums)
	}
	return source.String()
}

func TestEventSnapshotSkipsInvalidFieldMetadata(t *testing.T) {
	g := eventSnapshotGenerator{objects: map[string][]*ast.FieldDecl{"Empty": {{Name: "missing"}}}}
	var code strings.Builder
	g.writeValue(&code, nil, "out", "value", false, true, "nil", "\t")
	if code.Len() != 0 {
		t.Fatal("missing field type generated a dynamic fallback")
	}
	g.writeObject(&code, "Empty")
	if strings.Contains(code.String(), "Missing") {
		t.Fatal("unresolved field was copied")
	}
}

func TestEventSnapshotIgnoresDatabaseModelEvolution(t *testing.T) {
	snapshot := &ast.TypeDecl{Name: "UserSnapshot", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}}
	model := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
	}}
	file := &ast.File{Types: []*ast.TypeDecl{snapshot}, Models: []*ast.ModelDecl{model},
		Events: []*ast.EventDecl{{Name: "Changed", Params: []*ast.ParamDecl{
			{Name: "user", Type: &ast.TypeRef{Name: "UserSnapshot"}},
		}}}}
	before := generatedSnapshotSource(t, file)
	model.Fields = append(model.Fields, &ast.FieldDecl{Name: "secret", Type: &ast.TypeRef{Name: "Bytes"},
		Directives: []*ast.Directive{{Name: "hidden"}}})
	if after := generatedSnapshotSource(t, file); after != before {
		t.Fatal("adding a database field changed the explicit event contract")
	}
	model.Fields[1].Directives = []*ast.Directive{{Name: "internal"}}
	model.Fields = model.Fields[1:]
	if after := generatedSnapshotSource(t, file); after != before {
		t.Fatal("removing a database field or changing API visibility changed the event contract")
	}
	// Positive control: an explicit snapshot change must alter the generated code.
	snapshot.Fields = append(snapshot.Fields, &ast.FieldDecl{Name: "version", Type: &ast.TypeRef{Name: "Int"}})
	if after := generatedSnapshotSource(t, file); after == before {
		t.Fatal("explicit snapshot evolution was ignored")
	}
}

func TestEventSnapshotSelectsImmutableDelivery(t *testing.T) {
	g := eventSnapshotGenerator{enums: map[string]bool{"State": true}}
	for _, ref := range []*ast.TypeRef{nil, {Name: "Int"}, {Name: "String"}, {Name: "DateTime"}, {Name: "State"}} {
		var code strings.Builder
		g.writeEvent(&code, &ast.EventDecl{Name: "Scalar", Params: []*ast.ParamDecl{{Name: "value", Type: ref}}})
		if !strings.Contains(code.String(), "event.NewImmutableSnapshot(out)") || strings.Contains(code.String(), "cloneScalarEvent") {
			t.Fatalf("immutable fields generated per-listener cloning: %s", code.String())
		}
	}
	for _, ref := range []*ast.TypeRef{{Name: "String", Nullable: true}, {Name: "String", IsList: true}, {Name: "Bytes"}, {Name: "JSON"}, {Name: "Child"}} {
		var code strings.Builder
		g.writeEvent(&code, &ast.EventDecl{Name: "Mutable", Params: []*ast.ParamDecl{{Name: "value", Type: ref}}})
		if strings.Contains(code.String(), "event.NewImmutableSnapshot(out)") {
			t.Fatalf("mutable field shared across listeners: %+v", ref)
		}
	}
}

func TestEventSnapshotListAvoidsRedundantCopy(t *testing.T) {
	g := eventSnapshotGenerator{objects: map[string][]*ast.FieldDecl{"Child": {}}, queued: make(map[string]bool)}
	for _, kind := range []string{"Child", "Bytes", "JSON", "DateTime", "String", "Int"} {
		for _, validate := range []bool{false, true} {
			var code strings.Builder
			g.writeList(&code, &ast.TypeRef{Name: kind, IsList: true}, "out.Values", "value.Values", validate, "nil", "\t")
			wantCopy := kind == "String" || kind == "Int" || (kind == "DateTime" && !validate)
			if got := strings.Contains(code.String(), "copy(out.Values, value.Values)"); got != wantCopy {
				t.Errorf("%s validate=%t: bulk copy=%t, want %t\n%s", kind, validate, got, wantCopy, code.String())
			}
		}
	}
}

const eventSnapshotAssertions = `package generated
import ("bytes";"context";"errors";"fmt";"math";"os";"reflect";"sync";"testing";"time";
"github.com/light-speak/luxo/pkg/lux/codec";natsevent "github.com/light-speak/luxo/pkg/lux/nats/event";"github.com/light-speak/luxo/pkg/lux/event")

func eventFixture() ProbeEvent {
    optional:="optional"
    return ProbeEvent{State:StateOPEN,Score:1.5,Text:"hello",At:time.Unix(-1,123456789).UTC(),
        Metadata:[]byte("{\"n\":123456789012345678901234567890}"),Names:[]string{"one"},Bytes:[]byte{1,2},
        Child:&ChildSnapshot{Name:"child",Bytes:[]byte{3},Metadata:[]byte("null"),Names:[]string{"child"},Optional:&optional,Detail:&DetailSnapshot{Label:"detail"},Details:[]DetailSnapshot{{Label:"list"}}}}
}

func TestGeneratedMemoryChecksWireDomain(t *testing.T) {
    cases:=[]struct{name string; change func(*ProbeEvent); want error}{
        {"enum",func(p *ProbeEvent){p.State=State("FUTURE")},codec.ErrInvalidEnum},
        {"float",func(p *ProbeEvent){p.Score=math.NaN()},codec.ErrNonFiniteFloat},
        {"string",func(p *ProbeEvent){p.Text="\xff"},codec.ErrInvalidUTF8},
        {"date",func(p *ProbeEvent){p.At=time.Date(10000,1,1,0,0,0,0,time.UTC)},codec.ErrInvalidDateTime},
        {"json",func(p *ProbeEvent){p.Metadata=[]byte("{")},codec.ErrInvalidJSON},
        {"unicode",func(p *ProbeEvent){p.Metadata=[]byte("\"\\uD800\"")},codec.ErrInvalidJSON},
        {"list",func(p *ProbeEvent){p.Names=[]string{"valid","\xff"}},codec.ErrInvalidUTF8},
        {"nested",func(p *ProbeEvent){p.Child.Name="\xff"},codec.ErrInvalidUTF8},
    }
    for _,tc:=range cases {t.Run(tc.name,func(t *testing.T){
        p:=eventFixture();tc.change(&p)
        if data,err:=p.MarshalLuxo();!errors.Is(err,tc.want)||data!=nil {t.Fatalf("wire: %x %v",data,err)}
        for _,listen:=range []bool{false,true}{
            bus:=event.NewChanBus(1);called:=false
            if listen {event.OnDecode(bus,"Probe",UnmarshalProbe,func(context.Context,ProbeEvent)error{called=true;return nil})}
            err:=EmitProbe(t.Context(),bus,p);bus.Close()
            if !errors.Is(err,tc.want)||called {t.Fatalf("memory listen=%t err=%v called=%t",listen,err,called)}
        }
    })}
}

func TestGeneratedEventOwnsAllMutableFields(t *testing.T) {
    source:=eventFixture();wire,err:=source.MarshalLuxo();if err!=nil{t.Fatal(err)}
    var expected ProbeEvent;if err:=expected.UnmarshalLuxo(wire);err!=nil{t.Fatal(err)}
    bus:=event.NewChanBus(1);gate:=make(chan struct{});var first,second ProbeEvent
    if err:=event.OnDecode(bus,"Probe",UnmarshalProbe,func(_ context.Context,p ProbeEvent)error{
        <-gate;first=p;p.Bytes[0]=99;p.Metadata[0]=' ';p.Names[0]="changed";p.Child.Bytes[0]=99;p.Child.Metadata[0]=' ';p.Child.Names[0]="changed";*p.Child.Optional="changed";p.Child.Name="changed";p.Child.Detail.Label="changed";p.Child.Details[0].Label="changed";return nil
    });err!=nil{t.Fatal(err)}
    if err:=event.OnDecode(bus,"Probe",UnmarshalProbe,func(_ context.Context,p ProbeEvent)error{second=p;return nil});err!=nil{t.Fatal(err)}
    if err:=EmitProbe(t.Context(),bus,source);err!=nil{t.Fatal(err)}
    source.Bytes[0]=7;source.Metadata[0]=' ';source.Names[0]="sender";source.Child.Bytes[0]=7;source.Child.Metadata[0]=' ';source.Child.Names[0]="sender";*source.Child.Optional="sender";source.Child.Name="sender";source.Child.Detail.Label="sender";source.Child.Details[0].Label="sender"
    close(gate);bus.Close()
    if !reflect.DeepEqual(second,expected){t.Fatalf("memory/wire mismatch: got=%#v want=%#v",second,expected)}
    first.Bytes[0]=8;if bytes.Equal(first.Bytes,second.Bytes)||second.Bytes[0]!=1{t.Fatal("retained listener alias")}
}

type scopedEventBus struct {event.Bus; prefix string}
func (b scopedEventBus) Emit(ctx context.Context,name string,payload any)error{return b.Bus.Emit(ctx,b.prefix+name,payload)}
func (b scopedEventBus) On(name string,handler event.Handler)error{return b.Bus.On(b.prefix+name,handler)}
func (b scopedEventBus) OnQueue(name,group string,handler event.Handler)error{return b.Bus.OnQueue(b.prefix+name,group,handler)}

func TestGeneratedNATSSnapshotParity(t *testing.T) {
    address:=os.Getenv("NATS_URL");if address==""{t.Fatal("generated event parity requires a NATS test server")}
    transport,err:=natsevent.NewNATSBus(address);if err!=nil{t.Fatal(err)};defer transport.Close()
    bus:=scopedEventBus{Bus:transport,prefix:fmt.Sprintf("luxo.tests.snapshot.%d.",time.Now().UnixNano())}
    mutated:=make(chan struct{});received:=make(chan ProbeEvent,1)
    if err:=event.OnDecode(bus,"Probe",UnmarshalProbe,func(_ context.Context,p ProbeEvent)error{
        p.Bytes[0]=99;p.Child.Bytes[0]=99;*p.Child.Optional="changed";close(mutated);return nil
    });err!=nil{t.Fatal(err)}
    if err:=event.OnDecode(bus,"Probe",UnmarshalProbe,func(_ context.Context,p ProbeEvent)error{
        select{case <-mutated: received<-p;return nil;case <-time.After(2*time.Second):return errors.New("first NATS listener did not finish")}
    });err!=nil{t.Fatal(err)}
    want:=eventFixture();if err:=EmitProbe(t.Context(),bus,want);err!=nil{t.Fatal(err)}
    select{case got:=<-received:if !reflect.DeepEqual(got,want){t.Fatalf("NATS value/ownership mismatch: %#v %#v",got,want)}
    case <-time.After(3*time.Second):t.Fatal("NATS event not delivered")}
}

func TestGeneratedNilSnapshotRejected(t *testing.T){
    var missing *ProbeEvent
    if owned,err:=missing.SnapshotEvent();owned!=nil||!errors.Is(err,event.ErrInvalidSnapshot){t.Fatalf("nil snapshot accepted: %v",err)}
    if data,err:=missing.MarshalLuxo();data!=nil||err==nil{t.Fatalf("nil wire snapshot accepted: %x %v",data,err)}
    if err:=missing.UnmarshalLuxo([]byte{0});err==nil{t.Fatal("nil decoder receiver accepted")}
    source:=eventFixture();source.Child.Detail=nil
    if owned,err:=source.SnapshotEvent();owned!=nil||!errors.Is(err,event.ErrInvalidSnapshot){t.Fatalf("missing nested required value accepted: %v",err)}
    if data,err:=source.MarshalLuxo();data!=nil||err==nil{t.Fatalf("wire accepted missing nested required value: %x %v",data,err)}
}

func TestNestedSnapshotRejectsIncompleteBoundary(t *testing.T){
    detail:=DetailSnapshot{Label:"valid"}
    wire,err:=detail.marshalEventSnapshot(nil,1);if err!=nil{t.Fatal(err)}
    for _,bad:=range [][]byte{nil,{0},wire[:len(wire)-1],append(append([]byte{},wire...),0)}{
        var out DetailSnapshot
        if err:=out.unmarshalEventSnapshot(bad,1);err==nil{t.Fatalf("accepted broken nested snapshot: %x",bad)}
    }
}

func TestScalarMemoryEventNeverInvokesWireDecoder(t *testing.T){
    bus:=event.NewChanBus(1);var got ScalarEvent
    decoder:=func([]byte,any)error{t.Error("memory event performed a serialization round trip");return errors.New("wire decoder called")}
    if err:=event.OnDecode(bus,"Scalar",decoder,func(_ context.Context,value ScalarEvent)error{got=value;return nil});err!=nil{t.Fatal(err)}
    if err:=EmitScalar(t.Context(),bus,ScalarEvent{Id:42,Name:"\xff"});!errors.Is(err,codec.ErrInvalidUTF8){t.Fatalf("immutable optimization bypassed validation: %v",err)}
    if err:=EmitScalar(t.Context(),bus,ScalarEvent{Id:42,Name:"value"});err!=nil{t.Fatal(err)};bus.Close()
    if got!=(ScalarEvent{Id:42,Name:"value"}){t.Fatalf("scalar changed: %#v",got)}
}
`

const eventSnapshotMatrixAssertions = `package generated
import("bytes";"context";"encoding/json";"reflect";"testing";"time";"github.com/google/uuid";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux/event")
func pointer[T any](value T)*T{return &value}
func TestSnapshotAllScalarShapes(t *testing.T){
    original:=MatrixEvent{Payload:matrixFixture()};wire,err:=original.MarshalLuxo();if err!=nil{t.Fatal(err)}
    var want MatrixEvent;if err:=want.UnmarshalLuxo(wire);err!=nil{t.Fatal(err)}
    bus:=event.NewChanBus(1);var received []MatrixEvent
    for range 2{event.OnDecode(bus,"Matrix",UnmarshalMatrix,func(_ context.Context,p MatrixEvent)error{received=append(received,p);return nil})}
    if err:=EmitMatrix(t.Context(),bus,original);err!=nil{t.Fatal(err)};bus.Close()
    if len(received)!=2{t.Fatal("missing deliveries")}
    for _,got:=range received{if !reflect.DeepEqual(got,want){t.Fatalf("matrix mismatch: %#v %#v",got,want)}}
    first:=&received[0].Payload
    first.ListBytes[0][0]=99;first.OptionalListBytes[0][0]=99;first.ValueBytes[0]=99;(*first.OptionalBytes)[0]=99
    first.ListJSON[0][0]=' ';first.OptionalListJSON[0][0]=' ';first.ValueJSON[0]=' ';(*first.OptionalJSON)[0]=' '
    first.ListInt[0]=99;*first.OptionalInt=99;first.OptionalListInt[0]=99
    for _,got:=range []MatrixEvent{original,received[1]}{if !reflect.DeepEqual(got,want){t.Fatal("mutable scalar/list alias")}}
    rebuilt,err:=received[1].MarshalLuxo();if err!=nil||!bytes.Equal(wire,rebuilt){t.Fatalf("wire changed: %v",err)}
}
`
