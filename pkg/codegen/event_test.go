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
		`bus.Emit(ctx, "OrderCreated", data)`,
		"func RegisterEvents(bus event.Bus)",
		`bus.On("OrderCreated"`,
		"var e OrderCreatedEvent",
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
}
