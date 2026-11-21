package semantic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/token"
)

func BenchmarkIntegralLiteralType(b *testing.B) {
	for _, sample := range []struct {
		name, value string
		kind        token.Type
	}{
		{"int_small", "42", token.Int},
		{"int_maximum", "9223372036854775807", token.Int},
		{"duration_ms", "9223372036854ms", token.Duration},
		{"duration_days", "106751d", token.Duration},
		{"float_control", "0.1", token.Float},
	} {
		b.Run(sample.name, func(b *testing.B) {
			analyzer := New()
			literal := &ast.Literal{Kind: sample.kind, Value: sample.value}
			b.ReportAllocs()
			for b.Loop() {
				floatLiteralTypeSink = analyzer.literalType(literal)
			}
		})
	}
}

func BenchmarkAnalyzeIntegralLiterals(b *testing.B) {
	for _, sample := range []struct {
		name, kind, expression string
		field                  bool
	}{
		{"int", "Int", "-9223372036854775808", false},
		{"duration", "Duration", "9223372036854ms", false},
		{"float_control", "Float", "0.1", false},
		{"default", "Int", "= -9223372036854775808", true},
		{"range", "Int", "@range(-9223372036854775808, 9223372036854775807)", true},
		{"float_default_control", "Float", "= -0.1", true},
		{"float_range_control", "Float", "@range(0.0, 1.0)", true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			var source strings.Builder
			if sample.field {
				source.WriteString("model Snapshot @noTime { id: Int @id ")
			}
			for index := range 128 {
				if sample.field {
					fmt.Fprintf(&source, "value%d: %s %s ", index, sample.kind, sample.expression)
				} else {
					fmt.Fprintf(&source, "fn read%d(): %s { return %s }\n", index, sample.kind, sample.expression)
				}
			}
			if sample.field {
				source.WriteByte('}')
			}
			files := numericLiteralBenchmarkInput(b, source.String())
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze(files); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

func numericLiteralBenchmarkInput(b testing.TB, source string) []*ast.File {
	b.Helper()
	tokens, lexErrors := lexer.New(source, "numeric.luxo").Tokenize()
	file, parseErrors := parser.New(tokens).Parse("numeric.luxo")
	if len(lexErrors) != 0 || len(parseErrors) != 0 {
		b.Fatal(lexErrors, parseErrors)
	}
	return []*ast.File{file}
}
