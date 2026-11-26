package codegen

import (
	"strings"
	"testing"
)

func TestGeneratedScalarDestructuringPreservesPagination(t *testing.T) {
	for _, sample := range []struct {
		name     string
		body     string
		required []string
	}{
		{
			name: "query alias",
			body: `val (_, rows) = Node.orderBy(id.asc).all()
return rows`,
			required: []string{".Limit(req.PageSize).Offset((req.Page - 1) * req.PageSize).AllWithCount(ctx)", "codec.AppendSvarint(req.Buf.B, _luxoPageTotal0)"},
		},
		{
			name: "shadow with manual list",
			body: `val rows = Node.orderBy(id.asc).all()
if choose {
 val (_, rows) = [Node { id: 8 }]
 return rows
}
return rows`,
			required: []string{"if choose {", "int64(len(rows))", "codec.AppendSvarint(req.Buf.B, _luxoTotal1)", "codec.AppendSvarint(req.Buf.B, _luxoPageTotal0)"},
		},
	} {
		t.Run(sample.name, func(t *testing.T) {
			source := `model Node @noTime { id: Int @id }
api page(choose: Boolean): [Node] @paginate {
` + sample.body + "\n}"
			input := analyzePermissionFixture(t, source)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1}}})
			generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
			if err != nil {
				t.Fatal(err)
			}
			handler := string(generated.Files["handler.gen.go"])
			for _, text := range sample.required {
				index := strings.Index(handler, text)
				if index == -1 {
					t.Fatalf("generated handler missing %q:\n%s", text, handler)
				}
				handler = handler[index+len(text):]
			}
		})
	}
}

func TestGeneratedAwaitProjectionComposition(t *testing.T) {
	input := analyzePermissionFixture(t, `
model Node @noTime { id: Int @id name: String }
api scoped(): [Node] {
 val rows = Node.select().all()
 val (_, result) = await {
  val rows = Node.select().all()
  rows
 }
 val names = rows.map { it.name }
 return result
}
fn record(value: Int, seen: Channel<Int>): Int {
 seen <- value
 if value < 0 { throw error.NotFound }
 return value
}
fn mixedDependent(value: Int, seen: Channel<Int>): Int {
 val (answer, copy) = await {
  val (left, right) = record(value, seen)
  val next = record(left + right, seen)
  next + 2
 }
 return answer + copy
}
`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1, "name": 2}}})
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	handler := string(generated.Files["handler.gen.go"])
	before, after, found := strings.Cut(handler, `selectNodeSQLColumns([]*selection.Field{}, "name")`)
	if !found || strings.Contains(before, "selectNodeSQLColumns(req.Select)") || !strings.Contains(after, "selectNodeSQLColumns(req.Select)") {
		t.Fatalf("ordinary and scoped query selections are out of order:\n%s", handler)
	}
	runGeneratedCompilation(t, generated, awaitProjectionCompositionAssertions)
}

func TestGeneratedAwaitEarlyReturnPreservesProjection(t *testing.T) {
	const choice = `when {
 early -> {
  val rows = await {
   if early { return Node.select().all() }
   Node.select().all()
  }
  rows
 }
 else -> [Node { id: 1, name: "fallback" }]
}`
	for _, sample := range []struct {
		name, body           string
		projected, discarded int
	}{
		{"scalar", `val rows = await {
 if early { return Node.select().all() }
 Node.select().all()
}
return rows`, 2, 0},
		{"tuple", `val (_, rows) = await {
 if early { if early { return Node.select().all() } }
 42
 Node.select().all()
}
return rows`, 2, 0},
		{"statement", `await {
 if early { return Node.select().all() }
 Node.select().all()
}
return Node.select().all()`, 2, 1},
		{"returned local value", "return " + choice, 2, 0},
		{"discarded local value", "val ignored = " + choice + "\nreturn Node.select().all()", 1, 2},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id name: String }
api page(early: Boolean): [Node] {
`+sample.body+"\n}")
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1, "name": 2}}})
			generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
			if err != nil {
				t.Fatal(err)
			}
			handler := string(generated.Files["handler.gen.go"])
			selected := strings.Count(handler, "selectNodeSQLColumns(req.Select)")
			discarded := strings.Count(handler, "selectNodeSQLColumns([]*selection.Field{})")
			if selected != sample.projected || discarded != sample.discarded {
				t.Fatalf("projected queries=%d, discarded queries=%d; want %d and %d:\n%s", selected, discarded, sample.projected, sample.discarded, handler)
			}
		})
	}
}

const awaitProjectionCompositionAssertions = `package generated
import "testing"

func TestAwaitProjectionComposition(t *testing.T) {
 app := &App{}
 seen := make(chan int64, 3)
 got, err := app.mixedDependent(t.Context(), 20, seen)
 if err != nil || got != 84 || len(seen) != 2 || <-seen != 20 || <-seen != 40 {
  t.Fatalf("dependent aliases: %v %v", got, err)
 }
 got, err = app.mixedDependent(t.Context(), -1, seen)
 if err == nil || got != 0 || len(seen) != 1 || <-seen != -1 {
  t.Fatalf("dependent task continued after failure: %v %v", got, err)
 }
}
`
