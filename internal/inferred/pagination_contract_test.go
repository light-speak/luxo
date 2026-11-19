package inferred_test

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestPaginationPlanFinalListLineage(t *testing.T) {
	for _, sample := range []struct {
		name, declarations, body string
		queries                  int
		rejected                 bool
	}{
		{"query", "", `return Node.orderBy(id.asc).all()`, 1, false},
		{"selected_query", "", `return Node.select().all()`, 1, false},
		{"selected_ordered_query", "", `return Node.where(id > 0).select().orderBy(id.asc).all()`, 1, false},
		{"selected_builder_alias", "", `val query = Node.select() return query.all()`, 1, false},
		{"query_alias", "", `val rows = Node.all() val alias = rows return alias`, 1, false},
		{"builder_alias", "", `val query = Node.where(id > 1) return query.all()`, 1, false},
		{"find", "", `return Node.findMany(id: 1)`, 1, false},
		{"identity_loop", "", `val rows = Node.all() return for row in rows { row }`, 1, false},
		{"identity_map", "", `return Node.all().map { row -> row }`, 1, false},
		{"when_queries", "", `return when { choose -> Node.where(id == 1).all() else -> Node.all() }`, 2, false},
		{"discarded_query", "", `Node.where(id == 2).all() return Node.all()`, 1, false},
		{"unused_query", "", `val unused = Node.all() return Node.where(id > 1).all()`, 1, false},
		{"overwrite", "", `var rows = Node.all() rows = [Node { id: 1 }] return rows`, 0, false},
		{"manual_filter", "", `return [Node { id: 1 }].filter { it.id > 0 }`, 0, false},
		{"manual_sort", "", `return [Node { id: 2 }, Node { id: 1 }].sortBy { it.id }`, 0, false},
		{"manual_helper", `fn rows(): [Node] { return [Node { id: 1 }] }`, `return rows()`, 0, false},
		{"point_list", "", `return [Node.findFirst(id: 1) ?: throw error.NotFound]`, 0, false},
		{"implicit_return", "", `Node.all()`, 1, false},
		{"debug_result", "", `return Node.all().d`, 1, false},
		{"debug_alias", "", `val rows = Node.all() return rows.i`, 1, false},
		{"debug_statement", "", `val rows = Node.all() rows.w return rows`, 1, false},
		{"debug_then_size", "", `val rows = Node.all().d val count = rows.size return rows`, 0, true},
		{"debug_then_filter", "", `return Node.all().d.filter { it.id > 0 }`, 0, true},
		{"nullable_fallback", "", `val rows: [Node]? = Node.all() return rows ?: [Node { id: 1 }]`, 1, false},
		{"nullable_throw", "", `val rows: [Node]? = Node.all() return rows ?: throw error.NotFound`, 1, false},
		{"block_branches", "", `return when { choose -> { val rows = Node.all() rows } else -> [Node { id: 1 }] }`, 1, false},
		{"mapped_projection", "", `return Node.all().map { Node { id: -it.id + 1 } }`, 1, false},
		{"throwing_branch", "", `if choose { throw error.NotFound } return Node.all()`, 1, false},
		{"manual_ordinary_loop", "", `for row in [Node { id: 1 }] { val id = row.id } return [Node { id: 1 }]`, 0, false},
		{"nonnullable_default", "", `val rows = Node.all() return rows ?: [Node { id: 1 }]`, 1, false},
		{"opaque_map_result", `fn id(): Int { return 1 }`, `return Node.all().map { Node { id: id() } }`, 0, true},
		{"manual_mapping", "", `return [Node { id: 1 }].map { Node { id: -it.id } }`, 0, false},
		{"native_point_list", `fn row(): Result<Node> @native`, `return [row()?]`, 0, false},
		{"native_opaque", `fn rows(): Result<[Node]> @native`, `return rows()?`, 0, true},
		{"native_source_argument", `fn rows(items: [Node]): Result<[Node]> @native`, `return rows(Node.all())?`, 0, true},
		{"manual_recursive", `fn rows(stop: Boolean): [Node] { return when { stop -> [Node { id: 1 }] else -> rows(true) } }`, `return rows(false)`, 0, false},
		{"query_in_ordinary_loop", "", `val rows = Node.all() for row in rows { val id = row.id } return rows`, 0, true},
		{"database_filter", "", `return Node.all().filter { it.id > 1 }`, 0, true},
		{"database_sort", "", `return Node.all().sortBy { it.id }`, 0, true},
		{"break", "", `return for row in Node.all() { if row.id > 1 { break } row }`, 0, true},
		{"capture", "", `val rows = Node.all() return rows.map { Node { id: rows.size } }`, 0, true},
		{"query_limit", "", `return Node.limit(1).all()`, 0, true},
		{"query_offset", "", `val query = Node.offset(1) return query.all()`, 0, true},
		{"transaction_result", "", `return transaction { Node.all() }`, 0, true},
		{"await_source", "", `val (rows, other) = await { Node.all() Node.all() } return rows`, 0, true},
		{"load_list", "", `return Node.load(id: 1)`, 0, true},
		{"opaque_helper", `fn rows(): [Node] { return Node.all() }`, `return rows()`, 0, true},
		{"helper_argument", `fn rows(items: [Node]): [Node] { return items }`, `return rows(items: Node.all())`, 0, true},
		{"consumed_then_returned", "", `val rows = Node.all() val count = rows.size return rows`, 0, true},
		{"conditionally_mutated", "", `var rows = Node.all() if choose { rows = [Node { id: 1 }] } return rows`, 0, true},
		{"loop_query_assignment", "", `var rows = [Node { id: 1 }] for id in [1] { rows = Node.all() } return rows`, 0, true},
		{"empty_loop_query_assignment", "", `var rows = [Node { id: 1 }] val ids: [Int] = [] for id in ids { rows = Node.all() } return rows`, 0, true},
		{"loop_manual_assignment", "", `var rows = Node.all() for id in [1] { rows = [Node { id: id }] } return rows`, 0, true},
		{"loop_replaces_query", "", `var rows = Node.all() for id in [1] { rows = Node.where(id: id).all() } return rows`, 0, true},
		{"mapping_query_assignment", "", `var rows = [Node { id: 1 }] val ignored = for id in [1] { rows = Node.all() id } return rows`, 0, true},
		{"method_mapping_query_assignment", "", `var rows = [Node { id: 1 }] val ignored = [1].map { rows = Node.all() it } return rows`, 0, true},
		{"manual_loop_assignment", "", `var rows = [Node { id: 1 }] for id in [2] { rows = [Node { id: id }] } return rows`, 0, false},
		{"unused_loop_assignment", "", `var rows = [Node { id: 1 }] for id in [1] { rows = Node.all() } return [Node { id: 2 }]`, 0, false},
		{"loop_scope_restored", "", `var rows = [Node { id: 1 }] for id in [1] { val local = id } rows = Node.all() return rows`, 1, false},
		{"guard_consumes_rows", "", `val rows = Node.all() rows.size() > 1 !: throw error.Conflict return rows`, 0, true},
		{"template_consumes_rows", "", `val rows = Node.all() val label = "rows=${rows.size()}" return rows`, 0, true},
		{"range_end_consumes_rows", "", `val rows = Node.all() for index in 1..rows.size() { val value = index } return rows`, 0, true},
		{"range_start_consumes_rows", "", `val rows = Node.all() for index in rows.size()..3 { val value = index } return rows`, 0, true},
		{"async_consumes_rows", "", `val rows = Node.all() async { val count = rows.size() } return rows`, 0, true},
		{"async_assigns_rows", "", `var rows = [Node { id: 1 }] async { rows = Node.all() } return rows`, 0, true},
		{"event_consumes_rows", `event Counted(count: Int)`, `val rows = Node.all() emit Counted(count: rows.size()) return rows`, 0, true},
		{"event_snapshot_consumes_rows", `type Snapshot { ids: [Int] } event Captured(snapshot: Snapshot)`, `val rows = Node.all() emit Captured(snapshot: Snapshot { ids: rows.map { it.id } }) return rows`, 0, true},
		{"native_snapshot_consumes_rows", `type Snapshot { ids: [Int] } fn inspect(snapshot: Snapshot): Result<Int> @native`, `val rows = Node.all() val snapshot = Snapshot { ids: rows.map { it.id } } inspect(snapshot)? return rows`, 0, true},
		{"manual_guard", "", `val rows = [Node { id: 1 }] rows.size() > 2 !: throw error.Conflict return rows`, 0, false},
		{"manual_template", "", `val rows = [Node { id: 1 }] val label = "rows=${rows.size()}" return rows`, 0, false},
		{"manual_range", "", `val rows = [Node { id: 1 }] for index in 1..rows.size() { val value = index } return rows`, 0, false},
		{"unrelated_event", `event Counted(count: Int)`, `val rows = Node.all() emit Counted(count: rows.size()) return [Node { id: 1 }]`, 0, false},
		{"unrelated_async", "", `val rows = Node.all() async { val count = rows.size() } return [Node { id: 1 }]`, 0, false},
		{"query_filter_consumes_rows", "", `val rows = Node.all() val ignored = Node.where(id > rows.size()).all() return rows`, 0, true},
		{"chained_query_filter_consumes_rows", "", `val rows = Node.all() val ignored = Node.where(id > rows.size()).select().orderBy(id.asc).all() return rows`, 0, true},
		{"terminal_argument_consumes_rows", "", `val rows = Node.all() val ignored = Node.findMany(id: rows.size()) return rows`, 0, true},
		{"query_limit_consumes_rows", "", `val rows = Node.all() val ignored = Node.limit(rows.size()).all() return rows`, 0, true},
		{"query_helper_argument_consumes_rows", `fn count(items: [Node]): Int { return items.size() }`, `val rows = Node.all() val ignored = Node.where(id > count(rows)).all() return rows`, 0, true},
		{"query_builder_argument_consumes_rows", "", `val rows = Node.all() val query = Node.where(id > rows.size()) val ignored = query.all() return rows`, 0, true},
		{"independent_query_argument", "", `val rows = Node.all() val ignored = Node.where(id > rows.size()).all() return Node.where(id > 0).all()`, 1, false},
		{"terminal_independent_argument", "", `val rows = Node.all() return Node.findMany(id: rows.size())`, 1, false},
		{"manual_query_argument", "", `val rows = [Node { id: 1 }] val ignored = Node.where(id > rows.size()).all() return rows`, 0, false},
		{"query_reassignment_argument", "", `var rows = Node.all() rows = Node.findMany(id: rows.size()) return rows`, 1, false},
		{"field_copy_preserves_rows", `type Box { rows: [Node] }`, `val rows = Node.all() val box = Box { rows: [] } box.rows = rows return rows`, 1, false},
		{"field_mapping_preserves_rows", `type Snapshot { ids: [Int] }`, `val rows = Node.all() val snapshot = Snapshot { ids: [] } snapshot.ids = rows.map { it.id } return rows`, 1, false},
		{"nested_field_preserves_rows", `type Snapshot { ids: [Int] } type Outer { snapshot: Snapshot }`, `val rows = Node.all() val outer = Outer { snapshot: Snapshot { ids: [] } } outer.snapshot.ids = rows.map { it.id } return rows`, 1, false},
		{"conditional_field_consumes_rows", `type Box { rows: [Node] }`, `val rows = Node.all() val box = Box { rows: [] } if choose { box.rows = rows } return rows`, 0, true},
		{"field_guard_consumes_rows", `type Snapshot { ids: [Int] }`, `val rows = Node.all() val snapshot = Snapshot { ids: [] } snapshot.ids = rows.map { it.id } snapshot.ids.size() > 1 !: throw error.Conflict return rows`, 0, true},
		{"independent_field_assignment", `type Snapshot { ids: [Int] }`, `val rows = Node.all() val snapshot = Snapshot { ids: [] } snapshot.ids = rows.map { it.id } return Node.where(id > 0).all()`, 1, false},
		{"manual_field_assignment", `type Snapshot { ids: [Int] }`, `val rows = [Node { id: 1 }] val snapshot = Snapshot { ids: [] } snapshot.ids = rows.map { it.id } return rows`, 0, false},
		{"scalar_field_assignment", `type Counter { count: Int }`, `val rows = Node.all() val counter = Counter { count: 0 } counter.count = 1 return rows`, 1, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			file := parsePagination(t, `model Node @noTime { id: Int @id } `+sample.declarations+` api page(choose: Boolean): [Node] @paginate { `+sample.body+` }`)
			result := semantic.Analyze([]*ast.File{file})
			functions := make(map[string]*ast.FnDecl)
			for _, function := range file.Functions {
				functions[function.Name] = function
			}
			plan, err := inferred.PlanPagination(file.APIs[0], map[string]*ast.ModelDecl{"Node": file.Models[0]}, functions)
			if sample.rejected {
				if err == nil || !strings.Contains(err.Error(), "cannot safely push down pagination") || len(result.Errors) == 0 {
					t.Fatalf("unsafe plan escaped: %v / %v", err, result.Errors)
				}
				return
			}
			if err != nil || len(result.Errors) != 0 {
				t.Fatalf("valid plan rejected: %v / %v", err, result.Errors)
			}
			if len(plan.Queries) != sample.queries {
				t.Fatalf("paged queries=%d want=%d", len(plan.Queries), sample.queries)
			}
			for _, query := range plan.Queries {
				if plan.Owners[query] != query {
					t.Fatal("query total has no stable owner")
				}
			}
		})
	}
}

func TestPaginationPlanDisabled(t *testing.T) {
	for _, declaration := range []*ast.ApiDecl{nil, {}, {Body: &ast.Block{}}} {
		plan, err := inferred.PlanPagination(declaration, nil, nil)
		if err != nil || plan != nil {
			t.Fatalf("unpaged API created plan: %v %v", plan, err)
		}
	}
}

func TestPaginationObjectFieldLineage(t *testing.T) {
	for _, sample := range []struct {
		name, extra, body string
		queries           int
		rejected          bool
	}{
		{"direct", "", `return Box { rows: Node.all() }.rows`, 1, false},
		{"alias", "", `val box = Box { rows: Node.all() } return box.rows`, 1, false},
		{"nested", "", `return Outer { box: Box { rows: Node.all() } }.box.rows`, 1, false},
		{"nested_alias", "", `val outer = Outer { box: Box { rows: Node.all() } } return outer.box.rows`, 1, false},
		{"manual", "", `return Box { rows: [Node { id: 1 }] }.rows`, 0, false},
		{"independent_field", "", `val box = Box { rows: Node.all(), count: 5 } val count = box.count return box.rows`, 1, false},
		{"field_query_assignment", "", `val box = Box { rows: [] } box.rows = Node.all() return box.rows`, 1, false},
		{"field_manual_overwrite", "", `val box = Box { rows: Node.all() } box.rows = [] return box.rows`, 0, false},
		{"value_alias_keeps_query", "", `val box = Box { rows: Node.all() } val alias = box box.rows = [] return alias.rows`, 1, false},
		{"value_alias_keeps_manual", "", `val box = Box { rows: [] } val alias = box box.rows = Node.all() return alias.rows`, 0, false},
		{"nested_storage_alias", "", `val outer = Outer { box: Box { rows: Node.all() } } val alias = outer outer.box.rows = [] return alias.box.rows`, 0, false},
		{"nested_value_copy", "", `val outer = Outer { box: Box { rows: Node.all() } } val alias = outer.box outer.box.rows = [] return alias.rows`, 1, false},
		{"input_value_copy", "", `val alias = input input.rows = Node.all() return alias.rows`, 0, false},
		{"input_field_query", "", `input.rows = Node.all() return input.rows`, 1, false},
		{"model_input_reference", "", `val alias = holder holder.rows = Node.all() return alias.rows`, 1, false},
		{"model_implicit_reference", "", `val value = Holder { id: 1, rows: Node.all() } val alias = value value.rows = [] return alias.rows`, 0, false},
		{"model_reference_alias", "", `val value: Holder = Holder { id: 1, rows: Node.all() } val alias = value value.rows = [] return alias.rows`, 0, false},
		{"model_reference_query", "", `val value: Holder = Holder { id: 1, rows: [] } val alias = value value.rows = Node.all() return alias.rows`, 1, false},
		{"model_cycle", "", `val value: Holder = Holder { id: 1, rows: Node.all() } value.parent = value return value.parent?.rows ?: []`, 1, false},
		{"filter", "", `return Box { rows: Node.all() }.rows.filter { it.id > 1 }`, 0, true},
		{"consumed_field", "", `val box = Box { rows: Node.all() } val count = box.rows.size() return box.rows`, 0, true},
		{"conditional_write", "", `val box = Box { rows: Node.all() } if choose { box.rows = [] } return box.rows`, 0, true},
		{"opaque_argument", `fn inspect(value: Box): Result<Int> @native`, `val box = Box { rows: Node.all() } inspect(box)? return box.rows`, 0, true},
		{"native_object", `fn boxed(): Result<Box> @native`, `val box = boxed()? return box.rows`, 0, true},
		{"helper_database_object", `fn boxed(): Box { return Box { rows: Node.all() } }`, `return boxed().rows`, 0, true},
		{"helper_manual_object", `fn boxed(): Box { return Box { rows: [Node { id: 1 }] } }`, `return boxed().rows`, 0, false},
		{"helper_object_argument", `fn boxed(items: [Node]): Box { return Box { rows: items } }`, `return boxed(Node.all()).rows`, 0, true},
		{"helper_reference_write", `fn fill(value: Holder): Int { value.rows = Node.all() return 1 }`, `val value = Holder { id: 1, rows: [] } fill(value) return value.rows`, 0, true},
		{"native_reference_write", `fn fill(value: Holder): Result<Int> @native`, `val value = Holder { id: 1, rows: [] } fill(value)? return value.rows`, 0, true},
		{"native_branch", `fn boxed(): Result<Box> @native`, `val box = when { choose -> boxed()? else -> Box { rows: [] } } return box.rows`, 0, true},
		{"native_nested_branch", `fn boxed(): Result<Box> @native`, `val outer = when { choose -> Outer { box: boxed()? } else -> Outer { box: Box { rows: [] } } } return outer.box.rows`, 0, true},
		{"input_nested_alias", "", `val alias = provided provided.box.rows = Node.all() return alias.box.rows`, 1, false},
		{"input_nested_copy", "", `val alias = provided.box provided.box.rows = Node.all() return alias.rows`, 0, false},
		{"model_address_field", "", `val value = Holder { id: 2, rows: [] } value.parent = holder holder.rows = Node.all() return value.parent?.rows ?: []`, 1, false},
		{"helper_model_input_write", `fn fill(value: Holder): Int { value.rows = Node.all() return 1 }`, `fill(holder) return holder.rows`, 0, true},
		{"helper_model_reference", `fn build(): Holder { return Holder { id: 1, rows: [] } }`, `val value = build() val alias = value value.rows = Node.all() return alias.rows`, 1, false},
		{"nullable_type_copy", "", `val box: Box? = Box { rows: Node.all() } val alias = box ?: throw error.NotFound alias.rows = [] return (box ?: throw error.NotFound).rows`, 1, false},
		{"nullable_type_alias", "", `val box: Box? = Box { rows: Node.all() } val alias = box val present = box ?: throw error.NotFound present.rows = [] return (alias ?: throw error.NotFound).rows`, 1, false},
		{"nullable_variable_assignment", "", `var box: Box? = Box { rows: [] } box = Box { rows: Node.all() } val alias = box val present = box ?: throw error.NotFound present.rows = [] return (alias ?: throw error.NotFound).rows`, 1, false},
		{"helper_manual_returns", `fn boxed(choose: Boolean): Box { if choose { return Box { rows: [Node { id: 1 }] } } return Box { rows: [] } }`, `return boxed(choose).rows`, 0, false},
		{"helper_query_returns", `fn boxed(choose: Boolean): Box { if choose { return Box { rows: Node.all() } } return Box { rows: [] } }`, `return boxed(choose).rows`, 0, true},
		{"consumed_cycle", `fn inspect(value: Holder): Result<Int> @native`, `val value: Holder = Holder { id: 1, rows: Node.all() } value.parent = value inspect(value)? return value.rows`, 0, true},
		{"helper_throw", `fn blocked(): Box { throw error.NotFound }`, `return blocked().rows`, 0, false},
		{"literal_native_consumer", `fn inspect(values: [Box]): Result<Int> @native`, `val rows = Node.all() inspect([Box { rows: rows }])? return rows`, 0, true},
		{"literal_first_source", "", `return [Box { rows: Node.all() }].first()?.rows ?: []`, 0, true},
		{"literal_iteration_source", "", `var rows = [Node { id: 1 }] for box in [Box { rows: Node.all() }] { rows = box.rows } return rows`, 0, true},
		{"native_initial_reference", `fn inspect(value: Outer): Result<Int> @native`, `val alias = provided.box inspect(provided)? val branch = when { choose -> provided else -> provided } return branch.box.rows`, 0, true},
		{"await_object_source", "", `val (box, other) = await { Box { rows: Node.all() } 1 } return box.rows`, 0, true},
		{"transaction_object_source", "", `return (transaction { Box { rows: Node.all() } }).rows`, 0, true},
		{"await_manual_object", "", `val (box, other) = await { Box { rows: [Node { id: 1 }] } 1 } return box.rows`, 0, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			checkPaginationObjectCase(t, sample.extra, sample.body, sample.queries, sample.rejected)
		})
	}
}

func TestPaginationFiniteObjectListDoesNotPageInnerQueries(t *testing.T) {
	file := parsePagination(t, `model Node @noTime { id: Int @id }
model Holder @noTime { id: Int @id rows: [Node] @by(id, id) }
api page(): [Holder] @paginate { return [Holder { id: 1, rows: Node.all() }] }`)
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	plan, err := inferred.PlanPagination(file.APIs[0], map[string]*ast.ModelDecl{"Node": file.Models[0], "Holder": file.Models[1]}, nil)
	if err != nil || len(plan.Queries) != 0 {
		t.Fatalf("outer literal paged its inner query: %+v %v", plan, err)
	}
}

func checkPaginationObjectCase(t *testing.T, extra, body string, queries int, rejected bool) {
	t.Helper()
	file := parsePagination(t, `model Node @noTime { id: Int @id }
model Holder @noTime { id: Int @id rows: [Node] @by(id, id) parent: Holder? @by(id, id) }
type Box { rows: [Node] count: Int = 0 }
type Outer { box: Box }
`+extra+` api page(choose: Boolean, input: Box, provided: Outer, holder: Holder): [Node] @paginate { `+body+` }`)
	result := semantic.Analyze([]*ast.File{file})
	models := make(map[string]*ast.ModelDecl)
	functions := make(map[string]*ast.FnDecl)
	for _, model := range file.Models {
		models[model.Name] = model
	}
	for _, function := range file.Functions {
		functions[function.Name] = function
	}
	plan, err := inferred.PlanPagination(file.APIs[0], models, functions)
	if rejected {
		if err == nil || !strings.Contains(err.Error(), "cannot safely push down pagination") || len(result.Errors) == 0 {
			t.Fatalf("unsafe object lineage escaped: plan=%+v err=%v semantic=%v", plan, err, result.Errors)
		}
		return
	}
	if err != nil || len(result.Errors) != 0 || plan == nil || len(plan.Queries) != queries {
		t.Fatalf("object lineage: plan=%+v err=%v semantic=%v; want %d queries", plan, err, result.Errors, queries)
	}
	for _, query := range plan.Queries {
		if plan.Owners[query] != query {
			t.Fatal("query lost its total owner")
		}
	}
}

func parsePagination(t *testing.T, source string) *ast.File {
	t.Helper()
	tokens, lexErrors := lexer.New(source, "pagination.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("pagination.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	return file
}
