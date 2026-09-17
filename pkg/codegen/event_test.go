package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

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
	source.WriteString("package generated\nimport (\"fmt\"; \"github.com/light-speak/luxo/pkg/lux/codec\"; \"github.com/light-speak/luxo/pkg/lux/api\")\n")
	generateModel(&source, child, nil)
	generator.generateTypeWriteLuxo(&source, child, nil)
	generator.generateReadLuxo(&source, child, nil)
	generateEventStruct(&source, event)
	generator.generateEventCodec(&source, event, nil, map[string]bool{"FloatChild": true})
	runGeneratedProtocolPackage(t, source.String(), eventFloatAssertions)
}

const eventFloatAssertions = `package generated
import (
    "errors"
    "math"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
var _ codec.LuxoMarshaler = MeasuredEvent{}
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
	g.generateRegisterEvents(&b, []*ast.OnDecl{{EventName: "Completed"}}, "activity", "activity", nil, nil)
	for _, expected := range []string{"func RegisterEvents(bus event.Bus, app *App) error", "if err := event.OnQueueDecode", "}); err != nil {", "return err"} {
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
	events := []*ast.EventDecl{{Params: []*ast.ParamDecl{
		{Name: "unknown", Type: &ast.TypeRef{Name: "LegacyPayload"}},
		{Name: "metadata", Type: &ast.TypeRef{Name: "JSON"}},
		{Name: "items", Type: &ast.TypeRef{Name: "String", IsList: true}},
	}}}
	if !eventsNeedJSON(events) {
		t.Fatal("eventsNeedJSON() = false, want true")
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
	if eventsNeedJSON([]*ast.EventDecl{{Params: []*ast.ParamDecl{{Name: "unknown"}}}}) {
		t.Fatal("an unresolved event parameter must not enable a JSON fallback")
	}
}

func TestEventScalarWriteStatements(t *testing.T) {
	tests := []struct {
		name, typeName, want string
		isEnum               bool
	}{
		{name: "enum", typeName: "Role", isEnum: true, want: "if err := enc.WriteString(string(value)); err != nil { return nil, err }"},
		{name: "int", typeName: "Int", want: "enc.WriteInt(value)"},
		{name: "float", typeName: "Float", want: "if err := enc.WriteFloat(value); err != nil { return nil, err }"},
		{name: "string", typeName: "String", want: "if err := enc.WriteString(value); err != nil { return nil, err }"},
		{name: "boolean", typeName: "Boolean", want: "enc.WriteBool(value)"},
		{name: "datetime", typeName: "DateTime", want: "if err := enc.WriteDateTime(value); err != nil { return nil, err }"},
		{name: "duration", typeName: "Duration", want: "enc.WriteInt(int64(value))"},
		{name: "uuid", typeName: "UUID", want: "enc.WriteUUID([16]byte(value))"},
		{name: "decimal", typeName: "Decimal", want: "if err := enc.WriteString(value.String()); err != nil { return nil, err }"},
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
	writeEventListMarshal(&b, 2, "e.Payloads", "External", false, false)
	writeEventListUnmarshal(&b, 2, "e.Payloads", "External", false, false)
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
	writeEventListUnmarshal(&b, 2, "e.Amounts", "Decimal", false, false)
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
		`bus.Emit(ctx, "OrderCreated", e)`,
		"func (e OrderCreatedEvent) MarshalLuxo() ([]byte, error)",
		"event OrderCreated: expected *OrderCreatedEvent, got %T",
		"func RegisterEvents(bus event.Bus, app *App)",
		`event.OnQueueDecode(bus, "OrderCreated", "test", UnmarshalOrderCreated, func(ctx context.Context, e OrderCreatedEvent)`,
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

	// @broadcast should use event.OnDecode (not event.OnQueueDecode)
	if !strings.Contains(code, `event.OnDecode(bus, "ConfigChanged"`) {
		t.Errorf("@broadcast should use event.OnDecode:\n%s", code)
	}
	if strings.Contains(code, `event.OnQueueDecode`) {
		t.Errorf("@broadcast should NOT use event.OnQueueDecode:\n%s", code)
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

	if !strings.Contains(code, `event.OnQueueDecode(bus, "OrderCreated", "test"`) {
		t.Errorf("default listener should use OnQueueDecode:\n%s", code)
	}
	if !strings.Contains(code, `event.OnDecode(bus, "CacheInvalidate"`) {
		t.Errorf("@broadcast listener should use OnDecode:\n%s", code)
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

	if !strings.Contains(code, "e.Data.WriteLuxo(buf, nil)") || !strings.Contains(code, "e.Data.ReadLuxo(nested)") {
		t.Errorf("model event payload should use nested Luxo binary:\n%s", code)
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
	// Default should use OnQueueDecode
	if !strings.Contains(code, `event.OnQueueDecode(bus, "Ping", "test"`) {
		t.Errorf("default should use OnQueueDecode:\n%s", code)
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

	imports := generator.collectCrossModuleEventImports(result, listeners, "monitoring")
	if imports["common"] != "common_luxo" {
		t.Errorf("expected common_luxo import, got %v", imports)
	}
}

func TestCollectCrossModuleEventImports_NoContext(t *testing.T) {
	imports := defaultGenerator().collectCrossModuleEventImports(&semantic.Result{}, nil, "test")
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
	if !strings.Contains(code, "common_luxo.UnmarshalProjectDeleted") {
		t.Errorf("should use cross-module unmarshal:\n%s", code)
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
