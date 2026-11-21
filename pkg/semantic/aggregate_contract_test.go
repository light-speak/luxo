package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"testing"
)

func TestAggregateIgnoresNonModelReceivers(t *testing.T) {
	for _, typ := range []*ResolvedType{{Kind: TypeInt}, {Kind: TypeQueryBuilder}, {Kind: TypeModel, IsList: true}} {
		a := &Analyzer{}
		scope := NewScope()
		scope.Define(&Symbol{Name: "value", Kind: SymVariable, Type: typ})
		call := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "value"}, Field: "sum"}}
		if result, handled := a.checkScalarAggregate(call, scope); result != nil || handled {
			t.Fatal("non-model receiver treated as a scalar aggregate")
		}
	}
}

func TestAggregatePreservesNumericType(t *testing.T) {
	for _, scalar := range []string{"Int", "Float", "Decimal"} {
		for _, operation := range []string{"sum", "min", "max", "avg"} {
			resultType := scalar
			if operation == "avg" && scalar == "Int" {
				resultType = "Float"
			}
			source := `model Metric { id: Int @id value: ` + scalar + ` }
api probe(): ` + resultType + ` { return Metric.where(id: 1).` + operation + ` { it.value } }`
			t.Run(scalar+operation, func(t *testing.T) {
				if result := analyze(t, source); len(result.Errors) != 0 {
					t.Fatal(result.Errors)
				}
			})
		}
	}
}

func TestAggregateRejectsInvalidFieldContracts(t *testing.T) {
	for _, expression := range []string{
		`1.sum(1)`, `Metric.sum()`, `Metric.sum(1)`, `Metric.sum(name)`,
		`Metric.sum { other.value }`, `Metric.sum { it.missing }`,
	} {
		t.Run(expression, func(t *testing.T) {
			result := analyze(t, `model Metric { id: Int @id value: Int name: String }
api probe(): Int { return `+expression+` }`)
			if len(result.Errors) == 0 {
				t.Fatal("invalid aggregate accepted")
			}
		})
	}
}
