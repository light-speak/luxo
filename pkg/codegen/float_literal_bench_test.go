package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func BenchmarkCompileFloatEmission(b *testing.B) {
	literal := &ast.Literal{Kind: token.Float, Value: "0.1"}
	zero := &ast.Literal{Kind: token.Float, Value: "0.0"}
	parameter := &ast.Ident{Name: "value"}
	parameter.SetTypeTag("Float")
	for _, sample := range []struct {
		name string
		expr ast.Expr
	}{
		{"literal", literal},
		{"negated_literal", &ast.UnaryExpr{Op: "-", Value: zero}},
		{"negated_parameter", &ast.UnaryExpr{Op: "-", Value: parameter}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			compiler := newCompiler(nil)
			b.ReportAllocs()
			for b.Loop() {
				numericLiteralSourceSink = compiler.compileExpr(sample.expr)
			}
		})
	}
}

func BenchmarkGenerateFloatLiteralProgram(b *testing.B) {
	const source = `fn literalZero(): Float { return -0.0 }
fn variableZero(value: Float): Float { return -value }
fn literalSum(): Float { return 0.1 + 0.2 }`
	tokens, lexErrors := lexer.New(source, "float.luxo").Tokenize()
	file, parseErrors := parser.New(tokens).Parse("float.luxo")
	if len(lexErrors) != 0 || len(parseErrors) != 0 {
		b.Fatal(lexErrors, parseErrors)
	}
	input := semantic.Analyze([]*ast.File{file})
	if len(input.Errors) != 0 {
		b.Fatal(input.Errors)
	}
	for b.Loop() {
		if _, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG}); err != nil {
			b.Fatal(err)
		}
	}
}
