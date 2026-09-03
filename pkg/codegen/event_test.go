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

func TestEventJSONFallbackTypes(t *testing.T) {
	events := []*ast.EventDecl{{Params: []*ast.ParamDecl{
		{Name: "unknown", Type: &ast.TypeRef{Name: "LegacyPayload"}},
		{Name: "items", Type: &ast.TypeRef{Name: "String", IsList: true}},
	}}}
	if !eventsNeedJSON(events, nil, nil) {
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
	if !eventsNeedJSON([]*ast.EventDecl{{Params: []*ast.ParamDecl{{Name: "unknown"}}}}, nil, nil) {
		t.Fatal("an unresolved event parameter must retain the JSON fallback")
	}
}

func TestEventScalarWriteStatements(t *testing.T) {
	tests := []struct {
		name, typeName, want string
		isEnum               bool
	}{
		{name: "enum", typeName: "Role", isEnum: true, want: "enc.WriteString(string(value))"},
		{name: "int", typeName: "Int", want: "enc.WriteInt(value)"},
		{name: "float", typeName: "Float", want: "enc.WriteFloat(value)"},
		{name: "string", typeName: "String", want: "enc.WriteString(value)"},
		{name: "boolean", typeName: "Boolean", want: "enc.WriteBool(value)"},
		{name: "datetime", typeName: "DateTime", want: "enc.WriteInt(value.Unix())"},
		{name: "duration", typeName: "Duration", want: "enc.WriteInt(int64(value))"},
		{name: "uuid", typeName: "UUID", want: "enc.WriteUUID([16]byte(value))"},
		{name: "decimal", typeName: "Decimal", want: "enc.WriteString(value.String())"},
		{name: "bytes", typeName: "Bytes", want: "enc.WriteBytes(value)"},
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
		{typeName: "DateTime", want: "time.Unix(*raw, 0).UTC()"},
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

func TestEventCodecFallbackWriters(t *testing.T) {
	old := eventFieldIDs
	defer func() { eventFieldIDs = old }()
	SetEventFieldIDs(map[string]map[string]int{"Changed": {"payload": 1}})
	param := &ast.ParamDecl{Name: "payload", Type: &ast.TypeRef{Name: "External"}}
	var b strings.Builder
	writeEventMarshalField(&b, "Changed", param, false, false)
	writeEventUnmarshalField(&b, "Changed", param, false, false)
	writeEventListMarshal(&b, 2, "e.Payloads", "External", false, false)
	writeEventListUnmarshal(&b, 2, "e.Payloads", "External", false, false)
	for _, want := range []string{"json.Marshal(e.Payload)", "json.Unmarshal(dec.ReadBytes(), &e.Payload)", "json.Marshal(e.Payloads[i])", "json.Unmarshal(dec.ReadBytes(), &e.Payloads[i])"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("fallback codec missing %q:\n%s", want, b.String())
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
		"func (e OrderCreatedEvent) MarshalLuxo() []byte",
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
	old := eventFieldIDs
	defer func() { eventFieldIDs = old }()

	SetEventFieldIDs(map[string]map[string]int{
		"OrderCreated": {"orderId": 1, "amount": 2, "note": 3, "paid": 4},
	})

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

	src := generateEventFile(result, "luxo")
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
	old := eventFieldIDs
	defer func() { eventFieldIDs = old }()

	SetEventFieldIDs(map[string]map[string]int{
		"DataEvent": {"amount": 1, "note": 2, "active": 3},
	})

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

	src := generateEventFile(result, "luxo")
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
	old := eventFieldIDs
	defer func() { eventFieldIDs = old }()

	SetEventFieldIDs(map[string]map[string]int{
		"ComplexEvent": {"data": 1},
	})

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

	src := generateEventFile(result, "luxo")
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
	old := globalEventCtx
	defer func() { globalEventCtx = old }()

	globalEventCtx = &EventContext{
		EventModule: map[string]string{"ProjectDeleted": "common"},
		ModulePath:  "github.com/test/service",
	}

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

	imports := collectCrossModuleEventImports(result, listeners, "monitoring")
	if imports["common"] != "common_luxo" {
		t.Errorf("expected common_luxo import, got %v", imports)
	}
}

func TestCollectCrossModuleEventImports_NoContext(t *testing.T) {
	old := globalEventCtx
	globalEventCtx = nil
	defer func() { globalEventCtx = old }()

	imports := collectCrossModuleEventImports(&semantic.Result{}, nil, "test")
	if len(imports) != 0 {
		t.Error("should return empty without context")
	}
}

func TestGenerateEventFileCrossModule(t *testing.T) {
	old := globalEventCtx
	defer func() { globalEventCtx = old }()

	globalEventCtx = &EventContext{
		EventModule: map[string]string{"ProjectDeleted": "common"},
		ModulePath:  "github.com/test/service",
	}

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

	src := generateEventFile(result, "luxo")
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
