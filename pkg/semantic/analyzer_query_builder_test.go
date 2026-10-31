package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

const groupedQueryModel = `
enum State { OPEN CLOSED }
model Order {
 id: Int @id
 status: State
 ownerId: Int
 amount: Float
 points: Int
 optional: Int?
 detail: JSON
 labels: [String]
}
`

func TestNegatedQueryPreservesValueValidation(t *testing.T) {
	for _, condition := range []string{`!(it.id in ["bad"])`, `!(it.id in [it.id])`} {
		t.Run(condition, func(t *testing.T) {
			result := analyze(t, groupedQueryModel+`api invalid(): [Order] { return Order.where { `+condition+` }.all() }`)
			if len(result.Errors) == 0 {
				t.Fatal("negation bypassed query validation")
			}
		})
	}
}

func TestBooleanPredicateRejectsComputedColumns(t *testing.T) {
	for _, expression := range []string{"it.active", "!it.active"} {
		expectError(t, analyze(t, `model Flag {
 id: Int @id
 val active: Boolean get { true }
}
api invalid(): [Flag] { return Flag.where { `+expression+` }.all() }`), "predicate requires a stored scalar field")
	}
}

func TestQueryLogicalOperandsAreBoolean(t *testing.T) {
	for _, condition := range []string{`it.points && it.id > 0`, `it.id > 0 || "yes"`, `[true] && it.id > 0`} {
		for _, query := range []string{
			`Order.where { ` + condition + ` }.all()`,
			`Order.find(where: ` + condition + `)`,
		} {
			t.Run(query, func(t *testing.T) {
				result := analyze(t, groupedQueryModel+`api selected(): [Order] { return `+query+` }`)
				expectError(t, result, "requires non-null Boolean")
			})
		}
	}
}

func TestQueryValueScopeLazilyReusesGuard(t *testing.T) {
	outer := NewScope()
	model := &ResolvedType{Kind: TypeModel, Name: "Probe"}
	values := queryValueScope{parent: outer, parameter: "row", model: model}
	for _, expression := range []ast.Expr{
		&ast.Literal{Kind: token.Int, Value: "1"}, &ast.Ident{Name: "argument"},
		&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "argument"}, Field: "trim"}},
	} {
		if values.forExpr(expression) != outer || values.guarded != nil {
			t.Fatal("row-independent value created a guard")
		}
	}
	guard := values.forExpr(&ast.Ident{Name: "row"})
	if guard == outer || guard.LookupLocal("row").Kind != SymQueryRow {
		t.Fatal("query row is not guarded")
	}
	complex := &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "argument"}}}
	if values.forExpr(complex) != guard || outer.LookupLocal("row") != nil {
		t.Fatal("guard was recreated or caller scope was modified")
	}
	if values.forExpr(&ast.Ident{Name: "argument"}) != outer {
		t.Fatal("independent scalar stopped using lexical scope")
	}
}

func TestWhereCheckingPreservesCallerBindings(t *testing.T) {
	for _, parameter := range []string{"it", "row"} {
		for _, invalid := range []bool{false, true} {
			t.Run(parameter+map[bool]string{false: "/valid", true: "/invalid"}[invalid], func(t *testing.T) {
				analyzer := New()
				model := &ResolvedType{Kind: TypeModel, Name: "Probe", Fields: map[string]*FieldInfo{"id": {Type: analyzer.types["Int"]}}}
				for _, existing := range []bool{false, true} {
					outer := NewScope()
					original := &Symbol{Name: parameter, Kind: SymVariable, Type: analyzer.types["String"], Mutable: true}
					if existing {
						outer.Define(original)
						outer.Narrow(parameter, analyzer.types["String"])
					}
					fields := outer.Child()
					fields.Define(&Symbol{Name: "it", Kind: SymVariable, Type: model})
					var value ast.Expr = &ast.Literal{Kind: token.Int, Value: "1"}
					if invalid {
						value = &ast.Ident{Name: parameter}
					}
					condition := &ast.BinaryExpr{Left: &ast.MemberExpr{Object: &ast.Ident{Name: parameter}, Field: "id"}, Op: "==", Right: value}
					lambda := &ast.LambdaExpr{Params: []string{parameter}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: condition}}}}
					before := len(analyzer.errors)
					analyzer.checkWhereArgument(lambda, fields, outer, model)
					if (len(analyzer.errors) > before) != invalid {
						t.Fatalf("invalid=%v errors=%v", invalid, analyzer.errors[before:])
					}
					if symbol, found := outer.symbols[parameter]; found != existing || existing && symbol != original {
						t.Fatal("caller binding changed")
					}
					if original.Used || original.Kind != SymVariable || original.Type != analyzer.types["String"] {
						t.Fatal("caller symbol was mutated")
					}
				}
			})
		}
	}
}

func TestWhereNestedCollectionShadowsRowOnlyLocally(t *testing.T) {
	result := analyze(t, `model Probe { id: Int @id }
api selected(values: [Int], it: Int): Int {
 val rows = Probe.where { it.id in values.map { it + 1 } }.all()
 return it
}`)
	expectNoErrors(t, result)
}

func TestWhereMembershipTypes(t *testing.T) {
	for _, predicate := range []string{
		`{ it.points in [1, 2] }`, `{ row -> row.status in [State.OPEN] }`,
		`{ it.points in [] }`, `{ it.points in 1..10 }`,
		`(it.points in 1..10)`, `{ it.optional in [1] }`,
		`(points in 1..10)`, `{ it.points in [1] && it.ownerId > 0 }`,
		`(points: 1)`,
	} {
		expectNoErrors(t, analyze(t, groupedQueryModel+`api selected(): [Order] { return Order.where `+predicate+`.all() }`))
	}
	for _, predicate := range []string{
		`{ it.points in 1 }`, `{ it.points in null }`, `{ it.points in ["x"] }`,
		`{ it.amount in 1..10 }`, `{ it.status in ["OPEN"] }`,
		`{ it.points in it.labels }`, `{ it.points in nullable }`,
		`{ 1 in [1] }`, `{ it.labels in ["x"] }`,
		`(it.points in ["x"])`,
		`{ it.points > 0 || it.points in ["x"] }`,
		`{ it.detail in [] }`,
	} {
		t.Run(predicate, func(t *testing.T) {
			expectError(t, analyze(t, groupedQueryModel+`api selected(nullable: [Int]?): [Order] { return Order.where `+predicate+`.all() }`), "membership")
		})
	}
}

func TestMembershipFieldWithoutModel(t *testing.T) {
	if membershipField(&ast.Ident{Name: "points"}, "it", nil) != nil {
		t.Fatal("missing model produced a membership field")
	}
}

func TestWhereValuesUseLexicalScope(t *testing.T) {
	for _, body := range []string{
		`return Order.where { it.points in points }.all()`,
		`return Order.where { row -> row.points in points }.all()`,
		`return Order.where(points in points).all()`,
		`return Order.where { it.points in points && it.optional == optional }.all()`,
		`val query = Order.where { it.points in points }
         return query.where { it.points in points }.all()`,
		`return Order.where { it.points in points.filter { it > 0 } }.all()`,
	} {
		t.Run(body, func(t *testing.T) {
			expectNoErrors(t, analyze(t, groupedQueryModel+`api selected(points: [Int], optional: Int): [Order] { `+body+` }`))
		})
	}
}

func TestWhereValuesRejectInjectedFields(t *testing.T) {
	for _, predicate := range []string{
		`{ it.points in [points] }`, `{ it.points == ownerId }`,
		`{ row -> row.points == row.ownerId }`,
	} {
		t.Run(predicate, func(t *testing.T) {
			result := analyze(t, groupedQueryModel+`api selected(): [Order] { return Order.where `+predicate+`.all() }`)
			if len(result.Errors) == 0 {
				t.Fatal("row field escaped into a bound query value")
			}
		})
	}
}

func TestQueryStringValuesUseCallerScope(t *testing.T) {
	for _, predicate := range []string{
		`{ it.title.contains(title) }`, `{ row -> row.title.startsWith(title) }`,
		`(it.title.endsWith(title))`, `{ it.title.contains(title.trim()) && it.id > 0 }`,
	} {
		expectNoErrors(t, analyze(t, `model Probe { id: Int @id title: String? }
api selected(title: String): [Probe] { return Probe.where `+predicate+`.all() }`))
	}
}

func TestQueryStringValuesRejectRowReferences(t *testing.T) {
	for _, predicate := range []string{
		`{ it.title.contains(it.title) }`, `{ row -> row.title.startsWith(row.title) }`,
		`(it.title.endsWith(it.title))`, `{ it.title.contains(title) }`,
		`{ it.title.contains(1) }`, `{ it.title.contains(null) }`,
		`{ it.title.contains() }`, `{ it.title.contains("a", "b") }`,
		`{ it.title.contains(value: "a") }`, `{ "literal".startsWith("a") }`,
		`{ it.id.contains("a") }`, `{ missing() }`, `{ missing.method() }`,
		`{ it.title.contains {} }`, `{ it.title.contains { "value" } }`,
	} {
		t.Run(predicate, func(t *testing.T) {
			result := analyze(t, `model Probe { id: Int @id title: String }
api selected(): [Probe] { return Probe.where `+predicate+`.all() }`)
			if len(result.Errors) == 0 {
				t.Fatal("invalid SQL string argument was accepted")
			}
		})
	}
}

func TestCRUDWhereValuesUseCallerScope(t *testing.T) {
	for _, expression := range []string{
		`Probe.find(where: it.points in points)`, `find(Probe, where: it.points in points)`,
		`Probe.findMany(where: it.points in points)`,
		`Probe.updateMany(where: it.points in points, title: "changed")`,
		`Probe.deleteMany(where: it.points in points)`,
	} {
		t.Run(expression, func(t *testing.T) {
			expectNoErrors(t, analyze(t, `model Probe { id: Int @id points: Int title: String }
api selected(points: [Int]): Int { val rows = `+expression+`
return 1 }`))
		})
	}
}

func TestCRUDWhereRejectsRowValues(t *testing.T) {
	for _, expression := range []string{
		`Probe.find(where: it.points == it.id)`, `find(Probe, where: it.points == id)`,
		`Probe.findMany(where: it.points == it.id)`,
		`Probe.updateMany(where: it.points == it.id, title: "changed")`,
		`Probe.deleteMany(where: it.points == it.id)`,
	} {
		t.Run(expression, func(t *testing.T) {
			result := analyze(t, `model Probe { id: Int @id points: Int title: String }
api selected(): Int { val rows = `+expression+`
return 1 }`)
			if len(result.Errors) == 0 {
				t.Fatal("query row was accepted as a bound CRUD parameter")
			}
		})
	}
}

func TestTypedGroupHaving(t *testing.T) {
	source := groupedQueryModel + `
type Summary { count: Int }
api summary(minimum: Int): [Summary] {
 return Order.where(it.points > 0).groupBy { it.status }.select { Summary { count: it.count() } }
  .having { group -> group.count() >= minimum && group.key != State.CLOSED }
  .having { it.min { it.optional } != null }
}`
	expectNoErrors(t, analyze(t, source))
}

func TestTypedGroupHavingRejectsInvalidConditions(t *testing.T) {
	for _, test := range []struct{ predicate, message string }{
		{`{ "wrong" }`, "having requires"},
		{`{ true && it.count() > 0 }`, "having requires"},
		{`{ it.count() > 0 || false }`, "having requires"},
		{`{ it.count() >= "1" }`, "having value type"},
		{`{ it.key > State.OPEN }`, "having comparison"},
		{`{ it.count() == null }`, "non-null"},
		{`{ it.min { it.optional } > null }`, "null comparison"},
		{`{ it.key.missing == 1 }`, "not a grouping key"},
		{`{ it.count() + 1 }`, "having comparison"},
		{`{ it.count() > it.count() }`, "row-independent"},
		{`{ it.count() > Order.count() }`, "database query"},
		{`{ it.count() > unknown }`, "undefined"},
		{`{ it.count() > [1] }`, "having value type"},
		{`{}`, "single predicate expression"},
		{`{ a, b -> a.count() > 0 }`, "single predicate expression"},
	} {
		t.Run(test.predicate, func(t *testing.T) {
			expectError(t, analyze(t, groupedQueryModel+`type Summary { count: Int }
api summary(): [Summary] {
 return Order.groupBy { it.status }.select { Summary { count: it.count() } }.having `+test.predicate+`
}`), test.message)
		})
	}
}

func TestHavingRejectsNullableBoundValues(t *testing.T) {
	for _, body := range []string{
		`val bound = null`,
		`val bound = value`,
	} {
		expectError(t, analyze(t, groupedQueryModel+`type Summary { count: Int }
api summary(value: Int?): [Summary] {
 `+body+`
 return Order.groupBy { it.status }.select { Summary { count: it.count() } }.having { it.count() == bound }
}`), "having value type")
	}
}

func TestGroupedQueryTailRejectsMalformedAST(t *testing.T) {
	for _, call := range []*ast.CallExpr{
		nil,
		{Func: &ast.Ident{Name: "invalid"}},
		{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "invalid"}, Field: "having"}},
	} {
		if projection, conditions := groupedQueryTail(call, nil); projection != nil || conditions != nil {
			t.Fatal("malformed grouped query tail accepted")
		}
	}
}

func TestQueryPredicateLambdaTypes(t *testing.T) {
	for _, predicate := range []string{`{ it.points >= 6 }`, `{ row -> row.points >= 6 }`} {
		result := analyze(t, groupedQueryModel+`api selected(): [Order] { return Order.where `+predicate+`.all() }`)
		expectNoErrors(t, result)
	}
	for _, test := range []struct{ predicate, message string }{
		{`{ it.missing > 0 }`, "missing"},
		{`{ row -> row.missing > 0 }`, "missing"},
		{`{ "not a condition" }`, "Boolean"},
		{`{}`, "single predicate expression"},
		{`{ a, b -> a.points > 0 }`, "single predicate expression"},
		{`{ val minimum = 1
it.points > minimum }`, "single predicate expression"},
		{`{ return it.points > 0 }`, "single predicate expression"},
		{`{ Order.count() > 0 }`, "database query"},
	} {
		t.Run(test.predicate, func(t *testing.T) {
			result := analyze(t, groupedQueryModel+`api selected(): [Order] { return Order.where `+test.predicate+`.all() }`)
			expectError(t, result, test.message)
		})
	}
}

func TestTypedGroupedQuery(t *testing.T) {
	result := analyze(t, groupedQueryModel+`
type Summary { status: State
 total: Float
 count: Int }
api summarize(): [Summary] {
 return Order.where(points > 0).groupBy { it.status }.select {
  Summary { status: it.key, total: it.sum { it.amount }, count: it.count() }
 }
}`)
	expectNoErrors(t, result)
	call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if call.GetTypeTag() != "Summary" || !call.IsListType() || call.IsNullable() {
		t.Fatalf("grouped output lost its explicit type: %+v", call.TypeTagged)
	}
	if plan := call.Group; plan == nil || plan.Model != "Order" || plan.Result != "Summary" || len(plan.Keys) != 1 || plan.Keys[0] != "status" || len(plan.Columns) != 3 {
		t.Fatalf("missing static grouped projection: %+v", plan)
	} else if plan.Columns[0] != (ast.GroupColumn{Field: "status", Source: "status", Type: "State"}) || plan.Columns[1] != (ast.GroupColumn{Field: "total", Source: "amount", Aggregate: "sum", Type: "Float"}) || plan.Columns[2] != (ast.GroupColumn{Field: "count", Aggregate: "count", Type: "Int"}) {
		t.Fatalf("incorrect static projection: %+v", plan.Columns)
	}
}

func TestTypedGroupedQueryRejectsInvalidProjection(t *testing.T) {
	for _, test := range []struct{ name, fields, query, message string }{
		{"missing projection", "status: State", `Order.groupBy { it.status }`, "explicit type projection"},
		{"missing key", "count: Int", `Order.groupBy {}.select { Summary { count: it.count() } }`, "grouping key"},
		{"non-lambda key", "count: Int", `Order.groupBy(1).select { Summary { count: it.count() } }`, "grouping key"},
		{"unknown key", "count: Int", `Order.groupBy { it.missing }.select { Summary { count: it.count() } }`, "unknown grouping field"},
		{"duplicate key", "count: Int", `Order.groupBy { [it.status, it.status] }.select { Summary { count: it.count() } }`, "duplicate grouping field"},
		{"JSON key", "count: Int", `Order.groupBy { it.detail }.select { Summary { count: it.count() } }`, "unsupported grouping field"},
		{"list key", "count: Int", `Order.groupBy { it.labels }.select { Summary { count: it.count() } }`, "unsupported grouping field"},
		{"untyped select", "count: Int", `Order.groupBy { it.status }.select { it.count() }`, "explicit type projection"},
		{"unknown result", "count: Int", `Order.groupBy { it.status }.select { Missing { count: it.count() } }`, "declared type"},
		{"model result", "count: Int", `Order.groupBy { it.status }.select { Order { id: it.count() } }`, "declared type"},
		{"wrong count type", "count: String", `Order.groupBy { it.status }.select { Summary { count: it.count() } }`, "projection type mismatch"},
		{"unknown output", "count: Int", `Order.groupBy { it.status }.select { Summary { wrong: it.count() } }`, "unknown result field"},
		{"duplicate output", "count: Int", `Order.groupBy { it.status }.select { Summary { count: it.count(), count: it.count() } }`, "duplicate result field"},
		{"omitted nullable output", "count: Int, total: Float?", `Order.groupBy { it.status }.select { Summary { count: it.count() } }`, "missing result field"},
		{"ungrouped field", "status: Int", `Order.groupBy { it.status }.select { Summary { status: it.key.ownerId } }`, "not a grouping key"},
		{"ambiguous key", "status: State", `Order.groupBy { [it.status, it.ownerId] }.select { Summary { status: it.key } }`, "name a grouping key"},
		{"wrong aggregate", "count: Int", `Order.groupBy { it.status }.select { Summary { count: it.median { it.points } } }`, "unsupported group aggregate"},
		{"unknown aggregate field", "count: Int", `Order.groupBy { it.status }.select { Summary { count: it.sum { it.missing } } }`, "unknown grouping field"},
		{"sum enum", "count: Int", `Order.groupBy { it.status }.select { Summary { count: it.sum { it.status } } }`, "requires a numeric field"},
		{"count arguments", "count: Int", `Order.groupBy { it.status }.select { Summary { count: it.count(it.points) } }`, "count accepts no arguments"},
		{"missing aggregate argument", "count: Int", `Order.groupBy { it.status }.select { Summary { count: it.sum() } }`, "one field lambda"},
		{"computed aggregate expression", "count: Int", `Order.groupBy { it.status }.select { Summary { count: it.sum { it.points + 1 } } }`, "direct model field"},
		{"nullable min", "count: Int", `Order.groupBy { it.status }.select { Summary { count: it.min { it.optional } } }`, "projection type mismatch"},
		{"literal projection", "count: Int", `Order.groupBy { it.status }.select { Summary { count: 0 } }`, "group key or aggregate"},
		{"empty key list", "count: Int", `Order.groupBy { [] }.select { Summary { count: it.count() } }`, "grouping key"},
		{"return key statement", "count: Int", `Order.groupBy { return it.status }.select { Summary { count: it.count() } }`, "grouping key"},
		{"min enum", "count: State", `Order.groupBy { it.status }.select { Summary { count: it.min { it.status } } }`, "does not support this field type"},
		{"empty result", "", `Order.groupBy { it.status }.select { Summary {} }`, "at least one result field"},
		{"model value", "count: Int", `Order.find(id: 1).groupBy { it.status }.select { Summary { count: it.count() } }`, "requires a model query"},
		{"invalid prefix condition", "count: Int", `Order.where(missing == 1).groupBy { it.status }.select { Summary { count: it.count() } }`, "undefined"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := analyze(t, groupedQueryModel+"type Summary { "+strings.ReplaceAll(test.fields, ",", "\n")+" }\napi summarize(): [Summary] { return "+test.query+" }")
			expectError(t, result, test.message)
			call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
			if call.Group != nil {
				t.Fatal("invalid query published a grouped projection")
			}
		})
	}
}

func TestTypedGroupedQueryKeysAndAggregates(t *testing.T) {
	for _, test := range []struct{ name, fieldType, expression string }{
		{"count", "Int", "group.count()"},
		{"sum integer", "Int", "group.sum { row -> row.points }"},
		{"sum float", "Float", "group.sum { row -> row.amount }"},
		{"sum nullable", "Int", "group.sum { row -> row.optional }"},
		{"average integer", "Float", "group.avg { row -> row.points }"},
		{"min nullable", "Int?", "group.min { row -> row.optional }"},
		{"max integer", "Int", "group.max { row -> row.points }"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `type Summary { status: State
 ownerId: Int
 value: FIELD }
api summarize(): [Summary] {
 return Order.groupBy { row -> [row.status, row.ownerId] }.select { group ->
  Summary { status: group.key.status, ownerId: group.key.ownerId, value: EXPR }
 }
}`
			source = strings.ReplaceAll(strings.ReplaceAll(source, "FIELD", test.fieldType), "EXPR", test.expression)
			expectNoErrors(t, analyze(t, groupedQueryModel+source))
		})
	}
}

func TestTypedGroupedQueryModuleAndLambdaBoundaries(t *testing.T) {
	source := groupedQueryModel + `
type Summary { count: Int }
api summarize(): [Summary] {
 return Order.groupBy { it.status }.select { Summary { count: it.count() } }
}`
	file := parseFileWithName(t, source, "origin/orders.luxo")
	expectNoErrors(t, analyzeModules(t, []*ast.File{file}))
	result := analyze(t, source)
	call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	analyzer := New()
	analyzer.types = result.Types
	analyzer.inLambda = true
	analyzer.checkExpr(call, analyzer.scope)
	if call.Group != nil || len(analyzer.errors) == 0 || !strings.Contains(analyzer.errors[0].Message, "inside collection lambda") {
		t.Fatalf("collection lambda accepted a DB query: %+v", analyzer.errors)
	}
}

func TestGroupedProjectionRejectsIncompleteAST(t *testing.T) {
	for _, mutation := range []string{"nil field", "nil value", "unresolved type"} {
		t.Run(mutation, func(t *testing.T) {
			result := analyze(t, groupedQueryModel+`type Summary { count: Int }
api summarize(): [Summary] { return Order.groupBy { it.status }.select { Summary { count: it.count() } } }`)
			expectNoErrors(t, result)
			call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
			expression, _ := groupLambda(call)
			object := expression.(*ast.ObjectExpr)
			switch mutation {
			case "nil field":
				object.Fields[0] = nil
			case "nil value":
				object.Fields[0].Value = nil
			case "unresolved type":
				result.Types["Summary"].Fields["count"].Type = nil
			}
			analyzer := New()
			analyzer.types = result.Types
			analyzer.checkExpr(call, analyzer.scope)
			if len(analyzer.errors) == 0 || call.Group != nil {
				t.Fatalf("invalid AST retained a plan: %v", analyzer.errors)
			}
		})
	}
}

func TestGroupedInputClassification(t *testing.T) {
	analyzer := New()
	model := &ResolvedType{Kind: TypeModel, Name: "Order"}
	query := &ResolvedType{Kind: TypeQueryBuilder, Name: "OrderQueryBuilder", ModelType: model}
	analyzer.scope.Define(&Symbol{Name: "orders", Kind: SymVariable, Type: query})
	analyzer.types["Order"] = model
	for _, expression := range []ast.Expr{&ast.Literal{Kind: token.Int, Value: "1"}, &ast.Ident{Name: "orders"}, &ast.Ident{Name: "unknown"}} {
		call := &ast.CallExpr{Func: &ast.MemberExpr{Object: expression, Field: "groupBy"}}
		_, handled := analyzer.checkGroupedQuery(call, analyzer.scope)
		ident, isIdent := expression.(*ast.Ident)
		if want := isIdent && ident.Name == "orders"; handled != want {
			t.Fatalf("handled=%v want=%v", handled, want)
		}
	}
	if !groupQueryInput(&ast.Ident{Name: "orders"}, "Order", analyzer.scope) {
		t.Fatal("typed query variable rejected")
	}
	for _, expression := range []ast.Expr{&ast.Ident{Name: "missing"}, &ast.Literal{Kind: token.Int, Value: "1"}, &ast.CallExpr{Func: &ast.Ident{Name: "getOrder"}}} {
		if groupQueryInput(expression, "Order", analyzer.scope) {
			t.Fatalf("non-query accepted: %T", expression)
		}
	}
}

// ========== Query Builder — Model chain query syntax ==========

// helper: build a File with an api body containing a single expression statement.
func queryBuilderFile(expr ast.Expr) *ast.File {
	return &ast.File{
		Models: []*ast.ModelDecl{
			{
				Name: "Order",
				Pos:  token.Position{File: "test.luxo", Line: 1},
				Fields: []*ast.FieldDecl{
					{Name: "total", Type: &ast.TypeRef{Name: "Float"}},
					{Name: "status", Type: &ast.TypeRef{Name: "String"}},
				},
			},
		},
		APIs: []*ast.ApiDecl{
			{
				Name: "testApi",
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{Expr: expr},
					},
				},
			},
		},
	}
}

func TestQueryBuilderWhereOnModel(t *testing.T) {
	// Order.where(...) should not produce errors
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "where",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderChainWhereSum(t *testing.T) {
	// Order.where(...).sum(total) — chain: where returns query builder, sum returns Float
	inner := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "where",
	}
	expr := &ast.MemberExpr{
		Object: inner,
		Field:  "sum",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderGroupBySelect(t *testing.T) {
	// Order.groupBy(status).select { ... }
	inner := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "groupBy",
	}
	expr := &ast.MemberExpr{
		Object: inner,
		Field:  "select",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderFirst(t *testing.T) {
	// Order.where(...).first() returns Order?
	inner := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "where",
	}
	expr := &ast.MemberExpr{
		Object: inner,
		Field:  "first",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderAll(t *testing.T) {
	// Order.all() returns [Order]
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "all",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderCount(t *testing.T) {
	// Order.count() returns Int
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "count",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderAvg(t *testing.T) {
	// Order.avg(total) returns Float
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "avg",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderMinMax(t *testing.T) {
	// Order.min(total) and Order.max(total) return Float
	for _, method := range []string{"min", "max"} {
		t.Run(method, func(t *testing.T) {
			expr := &ast.MemberExpr{
				Object: &ast.Ident{Name: "Order"},
				Field:  method,
			}
			a := New()
			file := queryBuilderFile(expr)
			result := a.Analyze([]*ast.File{file})
			expectNoErrors(t, result)
		})
	}
}

func TestQueryBuilderOrderByLimit(t *testing.T) {
	// Order.orderBy(total).limit(10) — chaining
	inner := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "orderBy",
	}
	expr := &ast.MemberExpr{
		Object: inner,
		Field:  "limit",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderOffset(t *testing.T) {
	// Order.offset(10) — returns query builder
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "offset",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderInvalidMethod(t *testing.T) {
	// Order.foo() — should produce an error (foo is not a query method or field)
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "foo",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectError(t, result, "foo")
}

func TestQueryBuilderTripleChain(t *testing.T) {
	// Order.where(...).orderBy(total).first() — triple chain
	step1 := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "where",
	}
	step2 := &ast.MemberExpr{
		Object: step1,
		Field:  "orderBy",
	}
	expr := &ast.MemberExpr{
		Object: step2,
		Field:  "first",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderReturnTypes(t *testing.T) {
	// Verify return types of query methods
	a := New()
	a.declareType("Order", TypeModel, token.Position{}, "")
	orderType := a.types["Order"]
	orderType.Fields["total"] = &FieldInfo{Name: "total", Type: a.types["Float"]}

	tests := []struct {
		method   string
		wantKind TypeKind
		wantList bool
		wantNull bool
	}{
		{"where", TypeQueryBuilder, false, false},
		{"groupBy", TypeQueryBuilder, false, false},
		{"orderBy", TypeQueryBuilder, false, false},
		{"limit", TypeQueryBuilder, false, false},
		{"offset", TypeQueryBuilder, false, false},
		{"count", TypeInt, false, false},
		{"first", TypeModel, false, true},
		{"all", TypeModel, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			got := a.resolveQueryMethod(tt.method, orderType)
			if got == nil {
				t.Fatalf("resolveQueryMethod(%q) returned nil", tt.method)
			}
			if got.Kind != tt.wantKind {
				t.Errorf("resolveQueryMethod(%q).Kind = %v, want %v", tt.method, got.Kind, tt.wantKind)
			}
			if got.IsList != tt.wantList {
				t.Errorf("resolveQueryMethod(%q).IsList = %v, want %v", tt.method, got.IsList, tt.wantList)
			}
			if got.Nullable != tt.wantNull {
				t.Errorf("resolveQueryMethod(%q).Nullable = %v, want %v", tt.method, got.Nullable, tt.wantNull)
			}
		})
	}

	// select returns nil (depends on lambda)
	if got := a.resolveQueryMethod("select", orderType); got != nil {
		t.Errorf("resolveQueryMethod(select) = %v, want nil", got)
	}

	// unknown method returns nil
	if got := a.resolveQueryMethod("unknown", orderType); got != nil {
		t.Errorf("resolveQueryMethod(unknown) = %v, want nil", got)
	}
}

func TestIsQueryMethod(t *testing.T) {
	valid := []string{"where", "groupBy", "orderBy", "sum", "avg", "count", "min", "max", "select", "first", "all", "limit", "offset", "save"}
	for _, m := range valid {
		if !isQueryMethod(m) {
			t.Errorf("isQueryMethod(%q) = false, want true", m)
		}
	}
	invalid := []string{"foo", "bar", "map", "filter", "toString"}
	for _, m := range invalid {
		if isQueryMethod(m) {
			t.Errorf("isQueryMethod(%q) = true, want false", m)
		}
	}
}

func TestQueryBuilderFieldAccessStillWorks(t *testing.T) {
	// Order.total should still resolve as a field access, not a query method
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "total",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}
