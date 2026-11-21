package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkStringCallGeneration(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"control", `fn test(value: Int): Int { return value + 1 }`},
		{"positional", `fn test(value: String): String { return value.replace("x", "y") }`},
		{"named", `fn test(value: String): String { return value.replace(new: "y", old: "x") }`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			generator, err := NewGenerator(GeneratorConfig{Driver: DriverPG})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := generator.Generate(input, "generated", nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkStringFieldExpression(b *testing.B) {
	for _, sample := range []struct{ name, arguments string }{
		{"positional", `"x", "y"`}, {"named", `new: "y", old: "x"`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, "model TextProbe @noTime { id: Int @id name: String @transform { it.replace("+sample.arguments+") } }")
			expression := input.Files[0].Models[0].Fields[1].Directives[0].Body.Stmts[0].(*ast.ExprStmt).Expr
			b.ReportAllocs()
			for b.Loop() {
				if value := compileFieldExpr(expression, "value"); value == "" {
					b.Fatal("field expression was not compiled")
				}
			}
		})
	}
}
