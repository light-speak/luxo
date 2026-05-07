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
		"func RegisterEvents(bus event.Bus, app *App)",
		`event.OnQueueDecode(bus, "OrderCreated", "luxo", unmarshalOrderCreated, func(ctx context.Context, e OrderCreatedEvent)`,
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

	if !strings.Contains(code, `event.OnQueueDecode(bus, "OrderCreated", "order"`) {
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
	if !strings.Contains(code, "WriteFieldInt(1, e.OrderId)") {
		t.Errorf("missing WriteFieldInt for orderId:\n%s", code)
	}
	if !strings.Contains(code, "WriteFieldFloat(2, e.Amount)") {
		t.Errorf("missing WriteFieldFloat for amount:\n%s", code)
	}
	if !strings.Contains(code, "WriteFieldString(3, e.Note)") {
		t.Errorf("missing WriteFieldString for note:\n%s", code)
	}
	if !strings.Contains(code, "WriteFieldBool(4, e.Paid)") {
		t.Errorf("missing WriteFieldBool for paid:\n%s", code)
	}

	// UnmarshalLuxo should decode all fields
	if !strings.Contains(code, "case 1: e.OrderId = dec.ReadInt()") {
		t.Errorf("missing ReadInt for orderId:\n%s", code)
	}
	if !strings.Contains(code, "case 2: e.Amount = dec.ReadFloat()") {
		t.Errorf("missing ReadFloat for amount:\n%s", code)
	}
	if !strings.Contains(code, "case 3: e.Note = dec.ReadString()") {
		t.Errorf("missing ReadString for note:\n%s", code)
	}
	if !strings.Contains(code, "case 4: e.Paid = dec.ReadBool()") {
		t.Errorf("missing ReadBool for paid:\n%s", code)
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

	// Nullable types resolve to *int64, *float64, etc. via resolveGoType,
	// which doesn't match the switch cases ("int64", "float64", etc.),
	// so they fall through to the default TODO case.
	if !strings.Contains(code, "TODO: complex type") {
		t.Errorf("nullable types should fall to TODO default:\n%s", code)
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

	// Complex type should have TODO comment
	if !strings.Contains(code, "TODO: complex type") {
		t.Errorf("complex type should get TODO comment:\n%s", code)
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
	if !strings.Contains(code, `event.OnQueueDecode(bus, "Ping", "luxo"`) {
		t.Errorf("default should use OnQueueDecode:\n%s", code)
	}
}
