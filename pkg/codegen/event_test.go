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
		"func RegisterEvents(bus event.Bus)",
		`bus.OnQueue("OrderCreated", "luxo"`,
		"var e OrderCreatedEvent",
		"case OrderCreatedEvent:",
		"case []byte:",
		"sonic.Unmarshal",
	}
	for _, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing %q in:\n%s", check, code)
		}
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

	// @broadcast should use bus.On (not bus.OnQueue)
	if !strings.Contains(code, `bus.On("ConfigChanged"`) {
		t.Errorf("@broadcast should use bus.On:\n%s", code)
	}
	if strings.Contains(code, `bus.OnQueue`) {
		t.Errorf("@broadcast should NOT use bus.OnQueue:\n%s", code)
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

	if !strings.Contains(code, `bus.OnQueue("OrderCreated", "order"`) {
		t.Errorf("default listener should use OnQueue:\n%s", code)
	}
	if !strings.Contains(code, `bus.On("CacheInvalidate"`) {
		t.Errorf("@broadcast listener should use On:\n%s", code)
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
	if !strings.Contains(code, "var payload PingEvent") {
		t.Errorf("should default to 'payload' param:\n%s", code)
	}
	// Default should use OnQueue
	if !strings.Contains(code, `bus.OnQueue("Ping", "luxo"`) {
		t.Errorf("default should use OnQueue:\n%s", code)
	}
}
