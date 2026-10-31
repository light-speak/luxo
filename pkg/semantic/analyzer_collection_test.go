package semantic

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

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
	analyzer.checkCollectionReturns(&ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}}, analyzer.types["Int"], false)
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
