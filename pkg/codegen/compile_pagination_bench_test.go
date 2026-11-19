package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

// The mapped baseline lacks correct final-list pagination; it is not an
// equivalent implementation. Analyze and Generate are compile-time costs only.
func BenchmarkPaginationCandidate(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"scalar", `api count(): Int { return 42 }`},
		{"direct", `model Node @noTime { id: Int @id } api page(): [Node] @paginate { return Node.orderBy(id.asc).all() }`},
		{"selected", `model Node @noTime { id: Int @id } api page(): [Node] @paginate { return Node.select().all() }`},
		{"selected-alias", `model Node @noTime { id: Int @id } api page(): [Node] @paginate { val query = Node.select() return query.all() }`},
		{"selected-filtered", `model Node @noTime { id: Int @id } api page(): [Node] @paginate { return Node.where(id > 1).select().orderBy(id.asc).all() }`},
		{"query-input", `model Node @noTime { id: Int @id } api page(): [Node] @paginate { val rows = Node.all() return Node.where(id > rows.size()).select().orderBy(id.asc).all() }`},
		{"manual-field", `model Node @noTime { id: Int @id } type Snapshot { ids: [Int] } api page(): [Node] @paginate { val rows = [Node { id: 1 }] val snapshot = Snapshot { ids: [] } snapshot.ids = rows.map { it.id } return rows }`},
		{"independent-field", `model Node @noTime { id: Int @id } type Snapshot { ids: [Int] } api page(): [Node] @paginate { val rows = Node.all() val snapshot = Snapshot { ids: [] } snapshot.ids = rows.map { it.id } return Node.orderBy(id.asc).all() }`},
		{"scalar-field", `model Node @noTime { id: Int @id } type Counter { count: Int } api page(): [Node] @paginate { val counter = Counter { count: 0 } counter.count = 1 return Node.orderBy(id.asc).all() }`},
		{"mapped", `model Node @noTime { id: Int @id } api page(): [Node] @paginate { val rows = Node.all() return for row in rows { row } }`},
		{"manual", `model Node @noTime { id: Int @id } api page(): [Node] @paginate { return [Node { id: 1 }, Node { id: 2 }] }`},
		{"manual-loop", `model Node @noTime { id: Int @id } api page(): [Node] @paginate { var rows = [Node { id: 1 }] for id in [2] { rows = [Node { id: id }] } return rows }`},
		{"inputs", `model Node @noTime { id: Int @id } event Counted(count: Int) api page(): [Node] @paginate { val rows = [Node { id: 1 }, Node { id: 2 }] val label = "rows=${rows.size()}" rows.size() > 4 !: throw error.Conflict for index in 1..rows.size() { val value = index } async { val count = rows.size() } emit Counted(count: rows.size()) return rows }`},
		{"object-direct", `model Node @noTime { id: Int @id } type Box { rows: [Node] } api page(): [Node] @paginate { return Box { rows: Node.select().all() }.rows }`},
		{"object-nested", `model Node @noTime { id: Int @id } type Box { rows: [Node] } type Outer { box: Box } api page(): [Node] @paginate { val outer = Outer { box: Box { rows: Node.select().all() } } return outer.box.rows }`},
		{"object-write", `model Node @noTime { id: Int @id } type Box { rows: [Node] } api page(): [Node] @paginate { val box = Box { rows: [] } box.rows = Node.select().all() return box.rows }`},
		{"object-copy", `model Node @noTime { id: Int @id } type Box { rows: [Node] } api page(): [Node] @paginate { val box = Box { rows: Node.select().all() } val alias = box box.rows = [] return alias.rows }`},
		{"object-nullable", `model Node @noTime { id: Int @id } type Box { rows: [Node] } api page(): [Node] @paginate { val box: Box? = Box { rows: Node.select().all() } val alias = box ?: throw error.NotFound alias.rows = [] return (box ?: throw error.NotFound).rows }`},
		{"outer-literal", `model Node @noTime { id: Int @id } model Holder @noTime { id: Int @id rows: [Node] @by(id, id) } api page(): [Holder] @paginate { return [Holder { id: 1, rows: Node.all() }] }`},
	} {
		tokens, lexErrors := lexer.New(sample.source, "page.luxo").Tokenize()
		file, parseErrors := parser.New(tokens).Parse("page.luxo")
		if len(lexErrors) != 0 || len(parseErrors) != 0 {
			b.Fatal(lexErrors, parseErrors)
		}
		input := semantic.Analyze([]*ast.File{file})
		if len(input.Errors) != 0 {
			b.Fatal(input.Errors)
		}
		b.Run(sample.name+"/Analyze", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				result := semantic.Analyze([]*ast.File{file})
				if len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
		ids := StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1}}, EventFields: map[string]map[string]int{"Counted": {"count": 1}}}
		switch sample.name {
		case "manual-field", "independent-field":
			ids.ModelFields["Snapshot"] = map[string]int{"ids": 1}
		case "scalar-field":
			ids.ModelFields["Counter"] = map[string]int{"count": 1}
		case "object-direct", "object-write", "object-copy", "object-nullable":
			ids.ModelFields["Box"] = map[string]int{"rows": 1}
		case "object-nested":
			ids.ModelFields["Box"] = map[string]int{"rows": 1}
			ids.ModelFields["Outer"] = map[string]int{"box": 1}
		case "outer-literal":
			ids.ModelFields["Holder"] = map[string]int{"id": 1, "rows": 2}
		}
		config := GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, ids)}
		b.Run(sample.name+"/Generate", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
