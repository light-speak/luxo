package semantic

import (
	"fmt"
	"hash/maphash"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
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

func TestSaveRequiresModelInstanceReceiver(t *testing.T) {
	for name, expression := range map[string]string{
		"model":       "Entry.save()",
		"query":       "Entry.where(id > 0).save()",
		"alias":       "val query = Entry.where(id > 0) query.save()",
		"alias-chain": "val query = Entry.where(id > 0) val next = query.limit(1) next.orderBy(id.desc).save()",
		"shadow":      "val Entry = Entry.where(id > 0) Entry.save()",
		"safe-query":  "Entry.where(id > 0)?.save()",
		"bare-model":  "val value = Entry.save",
		"bare-query":  "val value = Entry.where(id > 0).save",
	} {
		t.Run(name, func(t *testing.T) {
			result := analyze(t, "model Entry @noTime { id: Int @id name: String } fn change() { "+expression+" }")
			for _, diagnostic := range result.Errors {
				if strings.Contains(diagnostic.Message, "save requires a model instance receiver") && diagnostic.Pos.Line > 0 && diagnostic.Pos.Col > 0 {
					return
				}
			}
			t.Fatal("save accepted a receiver without model field values", result.Errors)
		})
	}
}

func TestInstanceSaveRequiresInvocation(t *testing.T) {
	result := analyze(t, `model Entry @noTime { id: Int @id name: String }
fn change(row: Entry) { val value = row.save }`)
	expectError(t, result, "save requires an invocation")
}

func TestSaveNamedDataField(t *testing.T) {
	result := analyze(t, `model Entry @noTime { id: Int @id save: String }
fn read(row: Entry): String { return row.save }`)
	expectNoErrors(t, result)
}

func TestSavePreservesModelInstanceReceivers(t *testing.T) {
	for _, source := range []string{
		`fn change(value: Entry): Entry { return value.save() }`,
		`fn change(value: Entry?): Entry? { return value?.save() }`,
		`fn change(Entry: Entry): Entry { return Entry.save() }`,
		`fn change(): Entry { return Entry.find(id: 1).save() }`,
		`fn change(): Entry { return Entry.create(id: 1, name: "new").save() }`,
		`fn change(): Entry { return (Entry {id: 1 name: "new"}).save() }`,
		`fn change(): Int { return Entry.where(id > 0).count() }`,
	} {
		t.Run(source, func(t *testing.T) {
			result := analyze(t, "model Entry @noTime { id: Int @id name: String } "+source)
			expectNoErrors(t, result)
		})
	}
}

const orderQueryModels = `
type OrderBase { inherited: String }
enum OrderState { OPEN CLOSED }
model Ordered : OrderBase @noTime {
 id: Int @id
 name: String
 score: Int?
 state: OrderState
 tags: [String]
 raw: JSON
 bytes: Bytes
 asc: Int
 desc: Int
 childId: Int
 child: OrderChild @by(id, childId)
 children: [OrderChild] @by(orderedId, id)
 val childCount: Int get @count(children)
}
model OrderChild @noTime { id: Int @id orderedId: Int }
type OrderedGroup { id: Int }
`

func orderQueryForms(expression string) map[string]string {
	return map[string]string{
		"direct":        `Ordered.orderBy(` + expression + `).all()`,
		"selected":      `Ordered.select().orderBy(` + expression + `).all()`,
		"local":         `val query = Ordered.where(id > 0) query.orderBy(` + expression + `).all()`,
		"method":        `Ordered.findMany(order: ` + expression + `)`,
		"function":      `findMany(Ordered, order: ` + expression + `)`,
		"find":          `Ordered.find(order: ` + expression + `)`,
		"first":         `findFirst(Ordered, order: ` + expression + `)`,
		"grouped-input": `Ordered.orderBy(` + expression + `).groupBy { it.id }.select { OrderedGroup { id: it.key } }`,
	}
}

func TestQueryOrderRejectsInvalidSelectors(t *testing.T) {
	for _, expression := range []string{
		`id.asc()`, `id.desc()`, `id.desc(42)`, `id.ASC`, `id.DESC`,
		`missing`, `missing.desc`, `outside`, `foreign.id`, `Ordered.id`,
		`id + 1`, `"id"`, `1`, `name.id.desc`, `id.sideways`,
		`[id.asc, name.desc]`, `[]`, `null`, `id?.asc`, `it?.id`, `it.id?.desc`,
		`child`, `child.desc`, `children`, `childCount`, `childCount.desc`,
	} {
		for form, body := range orderQueryForms(expression) {
			t.Run(form+"/"+expression, func(t *testing.T) {
				result := analyze(t, orderQueryModels+`api invalid(outside: Int, foreign: OrderChild): [Ordered] { `+body+` return Ordered.all() }`)
				expectError(t, result, "order requires a stored field")
			})
		}
	}
}

func TestQueryOrderPreservesStoredFieldSelectors(t *testing.T) {
	for _, expression := range []string{
		`id`, `id.asc`, `id.desc`, `it.id`, `it.id.asc`, `it.id.desc`,
		`(id.asc)`, `(it.id).desc`, `inherited`, `it.inherited.desc`,
		`score.desc`, `state`, `tags`, `raw`, `bytes`, `childId`,
		`asc`, `desc`, `asc.desc`, `desc.asc`, `it.asc`, `it.desc`, `it.asc.desc`, `it.desc.asc`,
	} {
		for form, body := range orderQueryForms(expression) {
			t.Run(form+"/"+expression, func(t *testing.T) {
				expectNoErrors(t, analyze(t, orderQueryModels+`api valid(id: String): [Ordered] { `+body+` return Ordered.all() }`))
			})
		}
	}
	for _, body := range []string{
		`Ordered.orderBy(id.asc, name.desc, it.inherited).all()`,
		`Ordered.select().orderBy((id.asc), it.name.desc).all()`,
		`val query = Ordered.select() query.orderBy(id, name).all()`,
		`Ordered.orderBy().all()`,
	} {
		expectNoErrors(t, analyze(t, orderQueryModels+`api valid(): [Ordered] { `+body+` return Ordered.all() }`))
	}
}

func TestQueryOrderManagedColumns(t *testing.T) {
	for _, sample := range []struct {
		directives, field string
		valid             bool
	}{
		{"", "createdAt", true}, {"", "updatedAt", true}, {"@soft", "deletedAt", true},
		{"@noTime", "createdAt", false}, {"@noTime", "updatedAt", false}, {"", "deletedAt", false},
		{"@noTime @soft", "deletedAt", true},
	} {
		t.Run(sample.directives+"/"+sample.field, func(t *testing.T) {
			result := analyze(t, `model Managed `+sample.directives+` { id: Int @id } api sorted(): [Managed] { return Managed.orderBy(`+sample.field+`.desc).all() }`)
			if sample.valid {
				expectNoErrors(t, result)
			} else {
				expectError(t, result, "order requires a stored field")
			}
		})
	}
}

func TestQueryOrderValidationRecoveryAndFieldShapes(t *testing.T) {
	analyzer := New()
	call := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Missing"}, Field: "orderBy"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}}}
	if analyzer.checkOrderByArguments(&ast.CallExpr{Func: &ast.Ident{Name: "other"}}, nil) {
		t.Fatal("ordinary call was classified as orderBy")
	}
	for _, resolved := range []*ResolvedType{nil, analyzer.types["Int"]} {
		if analyzer.checkOrderByArguments(call, resolved) || len(analyzer.errors) != 0 {
			t.Fatal("an unresolved receiver produced a cascading order diagnostic")
		}
	}
	if !analyzer.checkOrderByArguments(call, &ResolvedType{Kind: TypeQueryBuilder}) || len(analyzer.errors) != 0 {
		t.Fatal("query error recovery produced a cascading diagnostic")
	}
	for _, field := range []*FieldInfo{
		{Computed: true}, {IsMethod: true}, {}, {Type: &ResolvedType{Kind: TypeUnknown}},
	} {
		if storedOrderField(field) {
			t.Fatal("a non-column field was accepted", field)
		}
	}
	analyzer.files = []*ast.File{{Models: []*ast.ModelDecl{{Name: "Other"}}}}
	if analyzer.managedOrderField("Missing", "createdAt") {
		t.Fatal("a missing model acquired a managed column")
	}
	analyzer.checkQueryOrder(&ast.Ident{Name: "id"}, analyzer.types["String"])
	if len(analyzer.errors) != 0 {
		t.Fatal("an invalid receiver caused a cascading diagnostic")
	}
}

func TestQueryOrderPreservesArgumentBoundaries(t *testing.T) {
	for _, prefix := range []string{`Ordered.findMany(`, `findMany(Ordered, `} {
		for _, arguments := range []string{`order: id.asc, order: name.desc`, `order: id.asc, id: 1`} {
			expectNoErrors(t, analyze(t, orderQueryModels+`api valid(): [Ordered] { return `+prefix+arguments+`) }`))
		}
		expectError(t, analyze(t, orderQueryModels+`api invalid(): [Ordered] { return `+prefix+`order: id.asc, name.desc) }`), "desc")
		source := orderQueryModels + `api invalid(): [Ordered] { return ` + prefix + `order: ) }`
		tokens, errors := lexer.New(source, "order.luxo").Tokenize()
		if len(errors) != 0 {
			t.Fatal(errors)
		}
		if _, errors := parser.New(tokens).Parse("order.luxo"); len(errors) == 0 {
			t.Fatal("a missing named argument value passed the parser")
		}
	}
}

func TestQueryOrderFieldNamedItPreservesDirection(t *testing.T) {
	for _, fields := range []string{`it: Int`, `it: Int asc: Int desc: Int`} {
		for _, expression := range []string{`it`, `it.asc`, `it.desc`, `it.it`, `it.it.desc`} {
			for form, body := range orderQueryForms(expression) {
				if form == "grouped-input" {
					continue
				}
				body = strings.ReplaceAll(body, "Ordered", "Item")
				expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id `+fields+` } api sorted(): [Item] { `+body+` return Item.all() }`))
			}
		}
	}
}

func TestNonQueryOrderByChecksOrdinaryArguments(t *testing.T) {
	expectError(t, analyze(t, `type Box { orderBy: Int } fn inspect(box: Box): Int { return box.orderBy(missing) }`), "undefined: 'missing'")
	expectError(t, analyze(t, `fn orderBy(value: Int): Int { return value } fn inspect(): Int { return orderBy("bad") }`), "expects 'Int'")
}

func TestQueryOrderScopeSelectors(t *testing.T) {
	for _, prefix := range []string{"", "where(id > 0)."} {
		for _, expression := range []string{`id.desc()`, `missing.asc`, `id + 1`, `[id.asc]`} {
			expectError(t, analyze(t, `model Item @noTime { id: Int @id scope sorted = `+prefix+`orderBy(`+expression+`) } api sorted(): [Item] @scope(sorted) { return Item.all() }`), "order requires a stored field")
		}
		for _, expression := range []string{`id`, `it.id.desc`, `id.asc, it.id.desc`, ""} {
			expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id scope sorted = `+prefix+`orderBy(`+expression+`).limit(2) } api sorted(): [Item] @scope(sorted) { return Item.all() }`))
		}
	}
	// A scope without order selectors must retain its existing field/value rules.
	expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id scope only = where(id > 0) } api sorted(): [Item] @scope(only) { return Item.all() }`))
	expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id scope only = where } api sorted(): [Item] @scope(only) { return Item.all() }`))
}

func TestQueryOrderQualifierIgnoresNonColumnIt(t *testing.T) {
	for _, fields := range []string{
		`otherId: Int it: Other @by(id, otherId)`,
		`children: [Other] @by(itemId, id) val it: Int get @count(children)`,
	} {
		expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id asc: Int `+fields+` } model Other @noTime { id: Int @id itemId: Int } api sorted(): [Item] { return Item.orderBy(it.asc).all() }`))
	}
}

func TestQueryOrderRetainsExactCacheRead(t *testing.T) {
	analyzer := New()
	model := &ResolvedType{Kind: TypeModel, Name: "Item", Fields: map[string]*FieldInfo{"id": {Name: "id", Type: analyzer.types["Int"]}}}
	analyzer.scope.Define(&Symbol{Name: "Item", Kind: SymModel, Type: model})
	analyzer.cacheModelReads = make(map[ast.Expr]CacheDependency)
	call := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Item"}, Field: "orderBy"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}}}
	if !analyzer.checkOrderByArguments(call, &ResolvedType{Kind: TypeQueryBuilder, ModelType: model}) {
		t.Fatal("orderBy was not handled")
	}
	if dependency, ok := analyzer.cacheModelReads[call]; !ok || dependency.Kind != CacheModelDependency || dependency.Name != "Item" {
		t.Fatal("order validation lost its exact model-read dependency", analyzer.cacheModelReads)
	}
}

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

// Build a void function body to isolate query expression checking from API output rules.
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
		Functions: []*ast.FnDecl{
			{
				Name: "testQuery",
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
		{"select", TypeQueryBuilder, false, false},
		{"count", TypeInt, false, false},
		{"first", TypeModel, false, true},
		{"all", TypeModel, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			got := a.resolveQueryMethod(tt.method, orderType, nil, nil)
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

	// unknown method returns nil
	if got := a.resolveQueryMethod("unknown", orderType, nil, nil); got != nil {
		t.Errorf("resolveQueryMethod(unknown) = %v, want nil", got)
	}
}

func TestSelectedQueryPreservesResultType(t *testing.T) {
	for _, body := range []string{
		`return Item.select().all()`,
		`return Item.where(id > 0).select().orderBy(id.asc).all()`,
		`val query = Item.select() return query.all()`,
	} {
		result := analyze(t, `model Item @noTime { id: Int @id } api items(): [Item] @paginate { `+body+` }`)
		expectNoErrors(t, result)
		ret := result.Files[0].APIs[0].Body.Stmts[len(result.Files[0].APIs[0].Body.Stmts)-1].(*ast.ReturnStmt)
		if ret.Value.GetTypeTag() != "Item" || !ret.Value.IsListType() || ret.Value.IsNullable() {
			t.Fatalf("selection lost list metadata: %s", body)
		}
	}
	for _, body := range []string{
		`return Item.select().all()`,
		`return Item.select().unknown()`,
	} {
		result := analyze(t, `model Item @noTime { id: Int @id } api invalid(): Int { `+body+` }`)
		if len(result.Errors) == 0 {
			t.Fatalf("unknown selection type bypassed validation: %s", body)
		}
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

func TestDeleteManyRejectsDiscardedArguments(t *testing.T) {
	for name, expression := range map[string]string{
		"model":    "Item.deleteMany(ARGS)",
		"alias":    "query.deleteMany(ARGS)",
		"chain":    "Item.where(id > 0).deleteMany(ARGS)",
		"function": "deleteMany(Item, ARGS)",
	} {
		for _, argument := range []string{"id: 1", "unknown: true", "1", "where: id > 0, where: id < 5", "where: 1", "where: [true]", "where: optional"} {
			t.Run(name+"/"+argument, func(t *testing.T) {
				prefix := ""
				if name == "alias" {
					prefix = "val query = Item.where(id > 0) "
				}
				source := "model Item @noTime { id: Int @id } api probe(optional: Boolean?): Int { " + prefix + "return " + strings.ReplaceAll(expression, "ARGS", argument) + " }"
				input := analyze(t, source)
				found := false
				for _, diagnostic := range input.Errors {
					if strings.Contains(diagnostic.Message, "deleteMany") && diagnostic.Pos.Line > 0 {
						found = true
					}
				}
				if !found {
					t.Fatalf("deleteMany silently accepted a discarded or invalid argument: %s; errors=%v", source, input.Errors)
				}
			})
		}
	}
}

func TestDeleteManyExistingCallContracts(t *testing.T) {
	for _, expression := range []string{
		"Item.deleteMany()", "Item.deleteMany(where: id == 1)",
		"Item.where(id > 0).deleteMany()", "Item.where(id > 0).deleteMany(where: id < 5)",
		"deleteMany(Item)", "deleteMany(Item, where: id == 1)",
	} {
		t.Run(expression, func(t *testing.T) {
			input := analyze(t, "model Item @noTime { id: Int @id } api probe(): Int { return "+expression+" }")
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
		})
	}
	input := analyze(t, "fn deleteMany(value: Int): Int { return value } api probe(): Int { return deleteMany(1) }")
	if len(input.Errors) != 0 {
		t.Fatal("ordinary function acquired CRUD argument rules", input.Errors)
	}
}

func TestDeleteManyRequiresDeclaredModelArgument(t *testing.T) {
	for _, expression := range []string{"deleteMany()", "deleteMany(1)", "deleteMany(target: Item)", "deleteMany(candidate)"} {
		t.Run(expression, func(t *testing.T) {
			input := analyze(t, "model Item @noTime { id: Int @id } api probe(candidate: Item): Int { return "+expression+" }")
			found := false
			for _, diagnostic := range input.Errors {
				if strings.Contains(diagnostic.Message, "deleteMany requires a positional model") {
					found = true
				}
			}
			if !found {
				t.Fatal("builtin deleteMany accepted a value in place of its declared model", input.Errors)
			}
		})
	}
}

func TestDeleteManyPredicateForms(t *testing.T) {
	for _, predicate := range []string{"false", "allowed"} {
		for _, query := range []string{"Item.deleteMany(where: PREDICATE)", "Item.where(id < 5).deleteMany(where: PREDICATE)", "deleteMany(Item, where: PREDICATE)"} {
			source := "model Item @noTime { id: Int @id } api probe(allowed: Boolean): Int { return " + strings.ReplaceAll(query, "PREDICATE", predicate) + " }"
			input := analyze(t, source)
			if len(input.Errors) != 0 {
				t.Fatal(source, input.Errors)
			}
		}
	}
	for _, predicate := range []string{"it.id > 0", "row -> row.id > 0"} {
		input := analyze(t, "model Item @noTime { id: Int @id } api probe(): Int { return Item.where { "+predicate+" }.deleteMany(where: false) }")
		if len(input.Errors) != 0 {
			t.Fatal(input.Errors)
		}
	}

}

func TestDeleteManyInvalidInternalArgumentLocation(t *testing.T) {
	model := &ResolvedType{Kind: TypeModel, Name: "Item"}
	for _, arguments := range [][]*ast.NamedArg{{nil}, {{Name: "where"}}} {
		analyzer := &Analyzer{types: map[string]*ResolvedType{"Item": model}}
		scope := NewScope()
		scope.Define(&Symbol{Name: "Item", Kind: SymModel, Type: model})
		position := token.Position{File: "invalid.luxo", Line: 7, Col: 9}
		call := &ast.CallExpr{Pos: position, Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Item"}, Field: "deleteMany"}, Args: arguments}
		analyzer.checkQueryWhereArguments(call, scope)
		if len(analyzer.errors) != 1 || analyzer.errors[0].Pos != position || !strings.Contains(analyzer.errors[0].Message, "deleteMany") {
			t.Fatal("invalid internal argument lost the call-site diagnostic", analyzer.errors)
		}
	}
}

func TestModelQueryReceiverRejectsInstances(t *testing.T) {
	for _, sample := range []struct{ name, params, body string }{
		{"row", "row: Item", "return row.deleteMany()"},
		{"parameter-shadow", "Item: Item", "return Item.deleteMany()"},
		{"local-shadow", "row: Item", "val Item = row return Item.deleteMany()"},
		{"instance-query-chain", "Item: Item", "return Item.where(id > 0).deleteMany()"},
		{"instance-query-alias", "Item: Item", "val query = Item.where(id > 0) return query.deleteMany()"},
		{"row-query-alias", "row: Item", "val query = row.where(id > 0) return query.deleteMany()"},
		{"find-result", "", "return Item.find(1).deleteMany()"},
		{"first-result", "", "return Item.where(id > 0).first().deleteMany()"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyze(t, "model Item @noTime { id: Int @id name: String count: Int } api probe("+sample.params+"): Int { "+sample.body+" }")
			found := false
			for _, diagnostic := range input.Errors {
				if strings.Contains(diagnostic.Message, "requires a model type or query builder") && diagnostic.Pos.Line > 0 {
					found = true
				}
			}
			if !found {
				t.Fatal("model instance became a class query or escaped through a builder", input.Errors)
			}
		})
	}
}

func TestModelQueryReceiverPreservesBindings(t *testing.T) {
	for _, sample := range []struct{ params, body string }{
		{"row: Item", `return row.update(name: "kept")`},
		{"row: Item", "return row.delete()"},
		{"", "return Item.where(id > 0).count()"},
		{"", "val query = Item.where(id > 0).orderBy(id.desc) return query.count()"},
		{"", "val Item = Item.where(id > 0).orderBy(id.desc) return Item.count()"},
		{"row: Item", "val Item = row return Item.count"},
		{"row: Item", "return row.name.trim().length"},
	} {
		t.Run(sample.body, func(t *testing.T) {
			input := analyze(t, "model Item @noTime { id: Int @id name: String count: Int } api probe("+sample.params+"): Int { "+sample.body+" }")
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
		})
	}
}

func TestModelQueryReceiverResolvedFieldMetadata(t *testing.T) {
	model := &ResolvedType{Kind: TypeModel, Name: "Item", Fields: map[string]*FieldInfo{"where": {IsMethod: true, Type: &ResolvedType{Kind: TypeString, Name: "String"}}}}
	member := &ast.MemberExpr{Object: &ast.Ident{Name: "row"}, Field: "where"}
	analyzer := &Analyzer{callTarget: member}
	if analyzer.rejectInstanceQueryReceiver(member, model, NewScope()) || len(analyzer.errors) != 0 {
		t.Fatal("resolved user field method was treated as a query receiver", analyzer.errors)
	}
}

func TestQueryTerminalRejectsDiscardedArguments(t *testing.T) {
	for _, method := range []string{"count", "exists"} {
		resultType := "Int"
		if method == "exists" {
			resultType = "Boolean"
		}
		for name, expression := range map[string]string{
			"model": "Item.METHOD(ARGS)", "alias": "query.METHOD(ARGS)",
			"chain": "Item.where(id > 0).METHOD(ARGS)", "function": "METHOD(Item, ARGS)",
		} {
			for _, argument := range []string{"id: 1", "unknown: true", "1", "where: id > 0, where: id < 5", "where: 1", "where: [true]", "where: optional"} {
				t.Run(method+"/"+name+"/"+argument, func(t *testing.T) {
					prefix := ""
					if name == "alias" {
						prefix = "val query = Item.where(id > 0) "
					}
					body := strings.ReplaceAll(strings.ReplaceAll(expression, "METHOD", method), "ARGS", argument)
					input := analyze(t, "model Item @noTime { id: Int @id } api probe(optional: Boolean?): "+resultType+" { "+prefix+"return "+body+" }")
					found := false
					for _, diagnostic := range input.Errors {
						if strings.Contains(diagnostic.Message, method+" ") && diagnostic.Pos.Line > 0 && diagnostic.Pos.Col > 0 {
							found = true
						}
					}
					if !found {
						t.Fatal("query terminal discarded invalid arguments", body, input.Errors)
					}
				})
			}
		}
	}
}

func TestQueryTerminalPreservesCallContracts(t *testing.T) {
	for _, method := range []string{"count", "exists"} {
		resultType := "Int"
		if method == "exists" {
			resultType = "Boolean"
		}
		for _, expression := range []string{
			"Item.METHOD()", "Item.METHOD(where: false)", "Item.METHOD(where: it.id == requested)",
			"Item.where(id > 0).METHOD(where: id < requested)", "METHOD(Item, where: id == requested)",
			"query.METHOD(where: allowed)",
		} {
			t.Run(method+"/"+expression, func(t *testing.T) {
				body := strings.ReplaceAll(expression, "METHOD", method)
				expectNoErrors(t, analyze(t, "model Item @noTime { id: Int @id } api probe(requested: Int, allowed: Boolean): "+resultType+" { val query = Item.where(id > 0) return "+body+" }"))
			})
		}
		ordinary := "fn " + method + "(value: " + resultType + "): " + resultType + " { return value } api probe(value: " + resultType + "): " + resultType + " { return " + method + "(value) }"
		expectNoErrors(t, analyze(t, ordinary))
	}
}

func TestQueryTerminalRejectsModelValues(t *testing.T) {
	for _, method := range []string{"count", "exists"} {
		resultType := "Int"
		if method == "exists" {
			resultType = "Boolean"
		}
		for _, body := range []string{"return row.METHOD()", "val Item = row return Item.METHOD()", "return Item.find(1).METHOD()", "val query = row.where(id > 0) return query.METHOD()", "return METHOD(row)", "val Item = row return METHOD(Item)"} {
			t.Run(method+"/"+body, func(t *testing.T) {
				input := analyze(t, "model Item @noTime { id: Int @id } api probe(row: Item): "+resultType+" { "+strings.ReplaceAll(body, "METHOD", method)+" }")
				if len(input.Errors) == 0 {
					t.Fatal("model value became a whole-model database query", body)
				}
			})
		}
	}
}

func TestQueryTerminalPreservesNonDatabaseCalls(t *testing.T) {
	input := analyze(t, `model Item @noTime { id: Int @id }
api rejected(items: [Item]): Int { return items.count() }`)
	expectError(t, input, "list has no field 'count'")
	for _, diagnostic := range input.Errors {
		if strings.Contains(diagnostic.Message, "count accepts") || strings.Contains(diagnostic.Message, "requires a model type or query") {
			t.Fatal("unsupported collection call was classified as a database terminal", diagnostic)
		}
	}
	expectNoErrors(t, analyze(t, groupedQueryModel+`type Summary { count: Int }
api summarize(): [Summary] { return Order.groupBy { it.status }.select { Summary { count: it.count() } } }`))
	for _, method := range []string{"count", "exists"} {
		model := &ResolvedType{Kind: TypeModel, Name: "Item", Fields: map[string]*FieldInfo{method: {IsMethod: true, Type: &ResolvedType{Kind: TypeString, Name: "String"}}}}
		member := &ast.MemberExpr{Object: &ast.Ident{Name: "row"}, Field: method, DeclaredField: true}
		analyzer := &Analyzer{callTarget: member}
		if analyzer.rejectInstanceQueryReceiver(member, model, NewScope()) || len(analyzer.errors) != 0 {
			t.Fatal("resolved field method was treated as a database query", method, analyzer.errors)
		}
		if _, _, model := analyzer.queryWhereArguments(&ast.CallExpr{Func: member}, NewScope()); model != nil {
			t.Fatal("resolved field method acquired database where arguments", method)
		}
	}
}

func TestUpdateManyRejectsInvalidSetters(t *testing.T) {
	for name, expression := range map[string]string{
		"model": "Item.updateMany(ARGS)", "chain": "Item.where(id > 0).updateMany(ARGS)",
		"alias": "query.updateMany(ARGS)", "function": "updateMany(Item, ARGS)",
	} {
		for _, arguments := range []string{`1`, `missing: 1`, `name: 1`, `name: maybe`, `locked: "changed"`, `calc: 1`, `where: 1`} {
			t.Run(name+"/"+arguments, func(t *testing.T) {
				input := analyze(t, `model Item @noTime { id: Int @id name: String optional: String? locked: String @immutable val calc: Int get { 1 } }
api probe(maybe: String?): Int { val query = Item.where(id > 0) return `+strings.ReplaceAll(expression, "ARGS", arguments)+` }`)
				found := false
				for _, diagnostic := range input.Errors {
					if strings.Contains(diagnostic.Message, "updateMany") && diagnostic.Pos.Line > 0 && diagnostic.Pos.Col > 0 {
						found = true
					}
				}
				if !found {
					t.Fatal("invalid updateMany setter lost its located diagnostic", arguments, input.Errors)
				}
			})
		}
	}
}

func TestUpdateManyPreservesExistingWrites(t *testing.T) {
	for _, expression := range []string{
		`Item.updateMany()`, `Item.updateMany(where: false)`, `Item.updateMany(name: name)`,
		`Item.updateMany(id: 2)`, `Item.updateMany(secret: name)`, `Item.updateMany(optional: maybe)`,
		`Item.updateMany(createdAt: stamp)`, `Item.where(id > 0).updateMany(name: name)`,
		`query.updateMany(where: id < 5, name: name)`, `updateMany(Item, name: name)`,
	} {
		t.Run(expression, func(t *testing.T) {
			expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id name: String optional: String? secret: String @internal createdAt: DateTime }
api probe(name: String, maybe: String?, stamp: DateTime): Int { val query = Item.where(id > 0) return `+expression+` }`))
		})
	}
	expectNoErrors(t, analyze(t, `fn updateMany(value: String): String { return value } api probe(): String { return updateMany("kept") }`))
}

func TestUpdateManyRequiresModelQueryReceiver(t *testing.T) {
	for _, expression := range []string{`row.updateMany(name: "kept")`, `updateMany(row, name: "kept")`, `Item.updateMany(name: "kept")`} {
		t.Run(expression, func(t *testing.T) {
			input := analyze(t, `model Item @noTime { id: Int @id name: String } api probe(row: Item): Int { val Item = row return `+expression+` }`)
			if len(input.Errors) == 0 {
				t.Fatal("model value became a bulk model query", expression)
			}
		})
	}
}

func TestOrdinaryUpdateManyUsesCallerScope(t *testing.T) {
	expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id value: String }
fn updateMany(row: Item, value: Int): Int { return value }
api probe(Item: Item, value: Int): Int { return updateMany(Item, value) }`))
}

func TestOrdinaryUpdateManyKeepsArgumentAndLambdaRules(t *testing.T) {
	expectError(t, analyze(t, `fn updateMany(order: Int): Int { return order } api probe(): Int { return updateMany(order: "bad") }`), "argument 'order' expects 'Int'")
	expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id value: String }
fn updateMany(row: Item, value: Int): Int { return value }
api probe(rows: [Item]): [Int] { return rows.map { updateMany(it, 1) } }`))
}

func TestDeclaredCRUDNamesUseCallerScope(t *testing.T) {
	for _, name := range []string{"count", "create"} {
		t.Run(name, func(t *testing.T) {
			expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id value: String }
fn `+name+`(row: Item, value: Int): Int { return value }
api probe(Item: Item, value: Int): Int { return `+name+`(Item, value) }`))
		})
	}
}

func TestUpdateManyUsesActualQueryBuilderFields(t *testing.T) {
	expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id value: String }
model Other @noTime { id: Int @id value: Int }
api probe(): Int { val Item = Other.where(id > 0) return Item.updateMany(where: value > 0, value: 1) }`))
}

func TestDeclaredQueryNamesAvoidDatabaseWarnings(t *testing.T) {
	result := analyze(t, `model Item @noTime { id: Int @id value: String }
fn count(row: Item, where: Boolean): Int { return 1 }
api probe(Item: Item, values: [Int]): Int { for value in values { count(Item, where: value > 0) } return 0 }`)
	expectNoErrors(t, result)
	for _, warning := range result.Warnings {
		if strings.Contains(warning.Message, "N+1 query") || strings.Contains(warning.Message, "full table scan") {
			t.Fatal("declared function was classified as a database query", warning)
		}
	}
}

func TestUpdateManyRejectsDuplicateSetters(t *testing.T) {
	for name, expression := range map[string]string{
		"model":    `Item.updateMany(name: "first", name: "second")`,
		"chain":    `Item.where(id > 0).updateMany(name: "first", name: "second")`,
		"alias":    `query.updateMany(name: "first", name: "second")`,
		"function": `updateMany(Item, name: "first", name: "second")`,
	} {
		t.Run(name, func(t *testing.T) {
			result := analyze(t, `model Item @noTime { id: Int @id name: String }
api probe(): Int {
 val query = Item.where(id > 0)
 return `+expression+`
}`)
			expectError(t, result, "duplicate argument 'name'")
			for _, diagnostic := range result.Errors {
				if strings.Contains(diagnostic.Message, "duplicate argument 'name'") && (diagnostic.Pos.Line != 4 || diagnostic.Pos.Col == 0) {
					t.Fatal("duplicate setter diagnostic lost its source location", diagnostic)
				}
			}
		})
	}
}

func TestUpdateManyDuplicateChecksPreserveWhereAndSeparateCalls(t *testing.T) {
	expectNoErrors(t, analyze(t, `model Item @noTime { id: Int @id name: String }
api probe(): Int {
 val query = Item.where(id > 0)
 val count = query.updateMany(where: id < 10, where: id > 1, name: "first")
 return count + query.updateMany(name: "second")
}`))
}

func TestBulkSetterDuplicateIndexPreservesSourceOrder(t *testing.T) {
	for _, count := range []int{2, 128, 129} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			arguments := make([]*ast.NamedArg, count)
			for index := range arguments {
				arguments[index] = &ast.NamedArg{Name: fmt.Sprintf("field%d", count-index), Value: &ast.Literal{Kind: token.Int, Value: "1"}}
			}
			original := append([]*ast.NamedArg(nil), arguments...)
			analyzer := New()
			analyzer.checkDuplicateBulkSetters(arguments)
			if len(analyzer.errors) != 0 {
				t.Fatal(analyzer.errors)
			}
			for index, argument := range arguments {
				if argument != original[index] {
					t.Fatal("duplicate validation reordered argument evaluation")
				}
			}
			arguments[count-1] = &ast.NamedArg{Name: arguments[0].Name, Value: &ast.Literal{Pos: token.Position{Line: 12, Col: 9}, Kind: token.Int, Value: "2"}}
			analyzer.checkDuplicateBulkSetters(arguments)
			if len(analyzer.errors) != 1 || analyzer.errors[0].Pos.Line != 12 || analyzer.errors[0].Pos.Col != 9 {
				t.Fatal("duplicate validation failed to locate the later setter", analyzer.errors)
			}
		})
	}
}

func TestBulkSetterDuplicateHashCollision(t *testing.T) {
	var arguments []*ast.NamedArg
	bucket := uint64(0)
	for candidate := 0; len(arguments) < 3; candidate++ {
		name := fmt.Sprintf("field%d", candidate)
		slot := maphash.String(bulkSetterHashSeed, name) & 255
		if len(arguments) == 0 {
			bucket = slot
		}
		if slot == bucket {
			arguments = append(arguments, &ast.NamedArg{Name: name, Value: &ast.Literal{Kind: token.Int, Value: "1"}})
		}
	}
	analyzer := New()
	analyzer.checkDuplicateBulkSetters(arguments)
	if len(analyzer.errors) != 0 {
		t.Fatal(analyzer.errors)
	}
	arguments = append(arguments, &ast.NamedArg{Name: arguments[1].Name, Value: &ast.Literal{Pos: token.Position{Line: 17, Col: 4}, Kind: token.Int, Value: "2"}})
	analyzer.checkDuplicateBulkSetters(arguments)
	if len(analyzer.errors) != 1 || analyzer.errors[0].Pos.Line != 17 {
		t.Fatal("colliding names lost unique setters or the later duplicate", analyzer.errors)
	}
}

func TestBulkSetterDuplicateScratchAllocations(t *testing.T) {
	for _, count := range []int{1, 8, 16, 128, 129} {
		arguments := make([]*ast.NamedArg, count)
		for index := range arguments {
			arguments[index] = &ast.NamedArg{Name: fmt.Sprintf("field%d", index), Value: &ast.Literal{Kind: token.Int, Value: "1"}}
		}
		analyzer := New()
		allocations := testing.AllocsPerRun(10, func() { analyzer.checkDuplicateBulkSetters(arguments) })
		want := float64(0)
		if count > 128 {
			want = 1
		}
		if allocations != want || len(analyzer.errors) != 0 {
			t.Fatalf("count=%d allocations=%v, want %v, errors=%v", count, allocations, want, analyzer.errors)
		}
	}
}
