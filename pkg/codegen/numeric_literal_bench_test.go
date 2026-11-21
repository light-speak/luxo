package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

var numericLiteralSourceSink string

func BenchmarkCompileDecimalIntegerLiterals(b *testing.B) {
	for _, sample := range []struct {
		name, value string
		kind        token.Type
	}{
		{"int_plain", "10", token.Int},
		{"int_leading_zero", "010", token.Int},
		{"duration_plain", "10ms", token.Duration},
		{"duration_leading_zero", "010ms", token.Duration},
	} {
		b.Run(sample.name, func(b *testing.B) {
			compiler := newCompiler(nil)
			literal := &ast.Literal{Kind: sample.kind, Value: sample.value}
			b.ReportAllocs()
			for b.Loop() {
				numericLiteralSourceSink = compiler.compileLiteral(literal)
			}
		})
	}
}

func BenchmarkGenerateNumericLiteralBindings(b *testing.B) {
	for _, leading := range []bool{false, true} {
		source, name := decimalLeadingZeroSource, "leading_zero"
		if !leading {
			name = "plain"
			source = strings.ReplaceAll(strings.ReplaceAll(source, "010", "10"), "000", "0")
		}
		tokens, lexErrors := lexer.New(source, "numeric.luxo").Tokenize()
		file, parseErrors := parser.New(tokens).Parse("numeric.luxo")
		if len(lexErrors) != 0 || len(parseErrors) != 0 {
			b.Fatal(lexErrors, parseErrors)
		}
		input := semantic.Analyze([]*ast.File{file})
		if len(input.Errors) != 0 {
			b.Fatal(input.Errors)
		}
		config := GeneratorConfig{Driver: DriverPG, IDs: numericLiteralIDs()}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
