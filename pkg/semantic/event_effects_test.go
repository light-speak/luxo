package semantic

import (
	"slices"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestEventCallableIndexIsBuiltOnlyForFunctionCalls(t *testing.T) {
	file := parseFileWithName(t, `model User { id: Int @id }
event First(id: Int)
event Second(id: Int)
fn relay() { emit Second(id: 1) }
on First { "observed".i
User.count()
emit Second(id: 1) }
on Second { relay() }`, "events.luxo")
	analyzer := New()
	analyzer.files = []*ast.File{file}
	want := []string{"Second"}
	got, bodies := collectReachableEmits(file.Listeners[0].Body, "", nil, analyzer)
	if !slices.Equal(got, want) {
		t.Fatalf("direct event lost: %v", got)
	}
	if bodies != nil {
		t.Fatal("direct listener built an unused callable index")
	}
	got, bodies = collectReachableEmits(file.Listeners[1].Body, "", bodies, analyzer)
	if !slices.Equal(got, want) {
		t.Fatalf("helper event lost: %v", got)
	}
	if bodies[callableKey{name: "relay"}] != file.Functions[0].Body {
		t.Fatal("helper did not initialize the shared index")
	}
	// No analyzer is needed once the previous listener populated the index.
	if got, _ := collectReachableEmits(file.Listeners[1].Body, "", bodies, nil); !slices.Equal(got, want) {
		t.Fatalf("shared index lost: %v", got)
	}
}

func TestEmptyEventBodiesPreserveCallableIndex(t *testing.T) {
	key := callableKey{name: "relay"}
	block := &ast.Block{}
	for _, body := range []*ast.Block{nil, {}} {
		for _, bodies := range []map[callableKey]*ast.Block{nil, {key: block}} {
			events, next := collectReachableEmits(body, "", bodies, New())
			if len(events) != 0 || len(next) != len(bodies) || next[key] != bodies[key] {
				t.Fatal("empty listener changed the shared index or emitted an event")
			}
		}
	}
}
