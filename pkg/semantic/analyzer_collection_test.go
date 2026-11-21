package semantic

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestCollectionLengthCallsRejectArguments(t *testing.T) {
	for _, sample := range []struct{ receiver, member, result string }{
		{"[Int]", "size", "Int"}, {"[Int]", "isEmpty", "Boolean"},
		{"String", "size", "Int"}, {"String", "length", "Int"}, {"String", "isEmpty", "Boolean"},
	} {
		for _, argument := range []string{"(1)", "(tick())", "(ignored: 1)", "(1, 2)", " { 1 }"} {
			t.Run(sample.receiver+"/"+sample.member+"/"+argument, func(t *testing.T) {
				source := "fn tick(): Int { return 1 } fn run(value: " + sample.receiver + "): " + sample.result + " { return value." + sample.member + argument + " }"
				expectError(t, analyze(t, source), "accepts no arguments")
			})
		}
	}
}

func TestCollectionLengthCallsRemainValid(t *testing.T) {
	for _, sample := range []struct{ receiver, member, result string }{
		{"[Int]", "size", "Int"}, {"[Int]", "isEmpty", "Boolean"},
		{"String", "size", "Int"}, {"String", "length", "Int"}, {"String", "isEmpty", "Boolean"},
	} {
		for _, nullable := range []bool{false, true} {
			for _, suffix := range []string{"", "()"} {
				receiver, result, access := sample.receiver, sample.result, "."
				if nullable {
					receiver, result, access = receiver+"?", result+"?", "?."
				}
				expectNoErrors(t, analyze(t, "fn run(value: "+receiver+"): "+result+" { return value"+access+sample.member+suffix+" }"))
			}
		}
	}
	result := analyze(t, "fn run(value: String): Int { return value.size(missing) }")
	expectError(t, result, "accepts no arguments")
	expectError(t, result, "undefined")
	for _, source := range []string{
		"fn run(value: Int): Int { return value.size(1) }",
		"fn run(value: [Int]): Int { return value.length(1) }",
	} {
		expectError(t, analyze(t, source), "has no field")
	}
}

func TestListsDoNotInheritElementFields(t *testing.T) {
	for _, source := range []string{
		`fn run(values: [String]): Int { return values.length }`,
		`fn run(values: [String]): Int { return values.length() }`,
		`fn run(values: [String]): String { return values.lowercase() }`,
		`fn run(values: [String]): String { return values.trim() }`,
		`model Node @noTime { id: Int @id } fn run(values: [Node]): Int { return values.id }`,
		`type Box { length: Int } fn run(values: [Box]): Int { return values.length }`,
	} {
		expectError(t, analyze(t, source), "has no field")
	}
}

func TestListsRejectUnimplementedAndQueryMembers(t *testing.T) {
	methods := []string{
		"first", "last", "firstOrNull", "lastOrNull", "count", "contains", "take", "takeLast",
		"drop", "dropLast", "reversed", "shuffled", "distinct", "distinctBy", "flatMap", "groupBy",
		"indexOf", "zip", "chunked", "windowed", "all", "none", "minOf", "maxOf", "sortByDesc", "isNotEmpty", "let",
		"where", "orderBy", "select", "limit", "offset", "load", "find", "findFirst", "findMany", "create", "update", "delete", "save", "exists", "sum", "avg", "min", "max",
		"days", "hours", "minutes", "seconds", "milliseconds", "unknown",
	}
	for _, receiver := range []string{"[Int]", "[String]", "[Node]", "[Value]", "[Node]?"} {
		for _, method := range methods {
			for _, suffix := range []string{"", "()"} {
				t.Run(receiver+"/"+method+suffix, func(t *testing.T) {
					access := "."
					if receiver == "[Node]?" {
						access = "?."
					}
					source := `model Node @noTime { id: Int @id } type Value { first: Int count: Int } fn probe(values: ` + receiver + `): Int { values` + access + method + suffix + ` return 1 }`
					expectError(t, analyze(t, source), "has no field")
				})
			}
		}
	}
}

func TestCollectionMembersDoNotRestrictScalarFieldsOrQueries(t *testing.T) {
	for _, source := range []string{
		`fn probe(value: Int): Duration { return value.days }`,
		`type Value { first: Int count: Int } fn probe(value: Value): Int { return value.first + value.count }`,
		`model Node @noTime { id: Int @id } api probe(): Node? { return Node.where(id > 0).first() }`,
		`model Node @noTime { id: Int @id } api probe(): Int { return Node.count() }`,
		`model Node @noTime { id: Int @id } api probe(): [Node] { return Node.select().orderBy(id.asc).limit(1).all() }`,
	} {
		expectNoErrors(t, analyze(t, source))
	}
}

func TestTypedCollectionLambdas(t *testing.T) {
	for _, body := range []string{
		`return values.map { it + 1 }`,
		`return values.map { value -> value + 1 }.filter { it > 2 }`,
		`return values.map { value ->
          val doubled = value * 2
          if doubled > 10 { return 10 }
          doubled
        }`,
		`return values.filter { value ->
          if value < 0 { return false }
          value > 2
        }`,
		`return values.map { it + values.filter { it > 0 }.size() }`,
		`return values.map { return it + 1 }`,
		`val empty: [Int] = []
         return empty.filter { it > 0 }`,
		`return values.map { for value in values {
          if value < 0 { continue }
          if value == 0 { break }
          if value > 10 { return value }
        }
        it }`,
	} {
		t.Run(body, func(t *testing.T) {
			expectNoErrors(t, analyze(t, `api selected(values: [Int]): [Int] { `+body+` }`))
		})
	}
}

func TestTypedCollectionLambdaRejectsInvalidValues(t *testing.T) {
	for _, expression := range []string{
		`values.filter { 1 }`, `values.filter { null }`,
		`values.filter { it.missing }`, `values.map { it.missing }`,
		`values.map { a, b -> a }`, `values.map(1)`, `values.filter()`,
		`values.map {}`, `values.filter {}`,
		`values.map(callback: 1)`, `values.map { [it] }`,
		`values.map { null }`,
		`values.map { break }`, `values.map { continue }`,
		`values.map { if it > 0 { break }
          it
        }`,
		`values.map { for value in values { return "wrong" }
          it
        }`,
		`values.map { if it > 0 { return "wrong" }
          it
        }`,
		`values.filter { if it > 0 { return "wrong" }
          true
        }`,
	} {
		t.Run(expression, func(t *testing.T) {
			result := analyze(t, `api selected(values: [Int]): [Int] { return `+expression+` }`)
			if len(result.Errors) == 0 {
				t.Fatal("invalid collection callback was accepted")
			}
		})
	}
}

func TestCollectionReturnsRejectVoidAST(t *testing.T) {
	analyzer := New()
	analyzer.checkCollectionReturns(&ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}}, analyzer.types["Int"])
	if len(analyzer.errors) != 1 {
		t.Fatalf("missing return value was accepted: %v", analyzer.errors)
	}
}

func TestCollectionReceiverRequiresKnownNonNullElements(t *testing.T) {
	for _, source := range []string{
		`api selected(): [Int] { return [].map { 1 } }`,
		`api selected(values: [Int]?): [Int] { return values.filter { it > 0 } }`,
	} {
		t.Run(source, func(t *testing.T) {
			if result := analyze(t, source); len(result.Errors) == 0 {
				t.Fatal("invalid collection receiver was accepted")
			}
		})
	}
}

func TestTypedForEachRejectsInvalidCallbacks(t *testing.T) {
	for _, body := range []string{
		`values.forEach { a, b -> a }`,
		`values.forEach(1)`,
		`values.forEach { it.missing }`,
		`values.forEach { return 1 }`,
		`values.forEach { break }`,
		`values.forEach { continue }`,
		`return values.forEach { it }`,
		`val result = values.forEach { it }`,
	} {
		t.Run(body, func(t *testing.T) {
			result := analyze(t, `fn visit(values: [Int]): Int { `+body+` return 1 }`)
			if len(result.Errors) == 0 {
				t.Fatal("invalid forEach accepted")
			}
		})
	}
	expectNoErrors(t, analyze(t, `fn visit(values: [Int]): Int { values.forEach { ctx -> if ctx < 0 { ctx } ctx } return 1 }`))
}

func TestTypedAnyContract(t *testing.T) {
	for _, body := range []string{`return values.any { it > 0 }`, `return values.any { ctx -> if ctx < 0 { return false } ctx > 0 }`} {
		expectNoErrors(t, analyze(t, `fn probe(values: [Int]): Boolean { `+body+` }`))
	}
	for _, expr := range []string{`values.any { it }`, `values.any { null }`, `values.any { return 1 }`, `values.any(1)`, `values.any { a, b -> true }`} {
		t.Run(expr, func(t *testing.T) {
			if result := analyze(t, `fn probe(values: [Int]): Boolean { return `+expr+` }`); len(result.Errors) == 0 {
				t.Fatal("non-Boolean predicate accepted")
			}
		})
	}
}

func TestJoinToStringRejectsInvalidArguments(t *testing.T) {
	for _, source := range []string{
		`fn probe(values: [Int]): String { return values.joinToString(42) }`,
		`fn probe(values: [Int]): String { return values.joinToString(null) }`,
		`fn probe(values: [Int], separator: [String]): String { return values.joinToString(separator) }`,
		`fn probe(values: [Int], separator: String?): String { return values.joinToString(separator) }`,
		`fn probe(values: [Int]?): String { return values.joinToString(",") }`,
		`fn probe(values: [Int]): String { return values.joinToString(separator: ",") }`,
		`fn probe(values: [Int]): String { return values.joinToString(",", "extra") }`,
		`fn probe(values: [Int]): String { return values.joinToString() }`,
		`fn probe(values: [JSON]): String { return values.joinToString(",") }`,
		`fn probe(values: [Bytes]): String { return values.joinToString(",") }`,
	} {
		if result := analyze(t, source); len(result.Errors) == 0 {
			t.Fatalf("invalid join accepted: %s", source)
		}
	}
}

func TestSortByKeyContract(t *testing.T) {
	for _, key := range []string{"it", "it + 1", "\"key\""} {
		expectNoErrors(t, analyze(t, `fn ordered(values: [Int]): [Int] { return values.sortBy { `+key+` } }`))
	}
	for _, key := range []string{"true", "null", "[it]"} {
		result := analyze(t, `fn ordered(values: [Int]): [Int] { return values.sortBy { `+key+` } }`)
		if len(result.Errors) == 0 {
			t.Fatalf("invalid key accepted: %s", key)
		}
	}
}

func TestSumOfNumericContract(t *testing.T) {
	for _, typ := range []string{"Int", "Float", "Decimal"} {
		expectNoErrors(t, analyze(t, `fn total(values: [`+typ+`]): `+typ+` { return values.sumOf { it } }`))
	}
	for _, expression := range []string{`values.sumOf { true }`, `values.sumOf { "text" }`, `values.sumOf { null }`, `values.sumOf { [it] }`, `values.sumOf { a,b -> a }`, `values.sumOf(1)`} {
		if result := analyze(t, `fn total(values: [Int]): Float { return `+expression+` }`); len(result.Errors) == 0 {
			t.Fatalf("invalid sum accepted: %s", expression)
		}
	}
}

func TestCollectionMethodsRequireInvocation(t *testing.T) {
	for _, method := range []string{"map", "filter", "forEach", "any", "sortBy", "sumOf", "joinToString"} {
		for _, statement := range []string{"values." + method, "val unused = values." + method} {
			if result := analyze(t, `fn probe(values: [Int]): Int { `+statement+` return 1 }`); len(result.Errors) == 0 {
				t.Fatalf("uncalled method accepted: %s", statement)
			}
		}
	}
}
