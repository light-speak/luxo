package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestIntegralLiteralOverflowRejected(t *testing.T) {
	const tooLarge = "9223372036854775808"
	for _, sample := range []struct{ name, source string }{
		{"int_function", "fn read(): Int { return " + tooLarge + " }"},
		{"int_negative", "fn read(): Int { return -9223372036854775809 }"},
		{"int_leading_zero", "fn read(): Int { return 000" + tooLarge + " }"},
		{"int_api", "api read(): Int { return " + tooLarge + " }"},
		{"int_default", "fn read(value: Int = " + tooLarge + "): Int { return value }"},
		{"int_negative_default", "fn read(value: Int = -9223372036854775809): Int { return value }"},
		{"int_stream", "api read(value: Int = " + tooLarge + "): Int @stream @native"},
		{"int_error", "error Rejected(value: Int = " + tooLarge + ") { code: 400 message: \"rejected\" }"},
		{"int_model", "model Probe @noTime { id: Int @id value: Int = " + tooLarge + " }"},
		{"int_type", "type Probe { value: Int = -9223372036854775809 }"},
		{"int_range", "model Probe @noTime { id: Int @id value: Int @range(0, " + tooLarge + ") }"},
		{"int_rate_limit", "api read(): Int @rateLimit(max: " + tooLarge + ", window: 1s) { return 0 }"},
		{"duration_ms", "fn read(): Duration { return 9223372036855ms }"},
		{"duration_seconds", "fn read(): Duration { return 9223372037s }"},
		{"duration_minutes", "fn read(): Duration { return 153722868m }"},
		{"duration_hours", "fn read(): Duration { return 2562048h }"},
		{"duration_days", "fn read(): Duration { return 106752d }"},
		{"duration_negative", "fn read(): Duration { return -106752d }"},
		{"duration_default", "fn read(value: Duration = 106752d): Duration { return value }"},
		{"duration_field", "type Probe { value: Duration = 106752d }"},
		{"duration_rate_limit", "api read(): Int @rateLimit(max: 1, window: 106752d) { return 0 }"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := analyze(t, sample.source)
			for _, failure := range result.Errors {
				if strings.Contains(failure.Message, "literal") && strings.Contains(failure.Message, "Int64") {
					if failure.Pos.Line == 0 || failure.Pos.Col == 0 {
						t.Fatal("literal error has no source position", failure)
					}
					return
				}
			}
			t.Fatal("out-of-range literal passed analysis", result.Errors)
		})
	}
}

func TestIntegralLiteralMalformedAST(t *testing.T) {
	for _, sample := range []struct {
		kind token.Type
		text string
	}{
		{token.Int, ""}, {token.Int, "+"}, {token.Int, "--1"},
		{token.Int, "1_000"}, {token.Int, "0x10"}, {token.Int, "NaN"},
		{token.Duration, "1"}, {token.Duration, "1ns"}, {token.Duration, "1.1s"},
		{token.Duration, "ms"}, {token.Duration, "2x"}, {token.Duration, "-2x"},
	} {
		analyzer := New()
		analyzer.literalType(&ast.Literal{Kind: sample.kind, Value: sample.text})
		if len(analyzer.errors) != 1 || !strings.Contains(analyzer.errors[0].Message, "Int64") {
			t.Errorf("invalid integral AST accepted: %q: %v", sample.text, analyzer.errors)
		}
	}
	for _, sample := range []struct {
		kind token.Type
		text string
	}{{token.Int, "+42"}, {token.Int, "-9223372036854775808"}, {token.Duration, "-106751d"}} {
		analyzer := New()
		analyzer.literalType(&ast.Literal{Kind: sample.kind, Value: sample.text})
		if len(analyzer.errors) != 0 {
			t.Fatal("valid signed AST rejected", sample.text, analyzer.errors)
		}
	}
}

func TestNumericLiteralTreeKeepsSignLocal(t *testing.T) {
	literal := &ast.Literal{Kind: token.Int, Value: "9223372036854775808"}
	expression := &ast.ListExpr{Items: []ast.Expr{
		&ast.UnaryExpr{Op: "-", Value: literal}, literal,
		&ast.Literal{Kind: token.True, Value: "true"},
	}}
	analyzer := New()
	analyzer.checkNumericLiteralTree(expression)
	if len(analyzer.errors) != 1 || !strings.Contains(analyzer.errors[0].Message, "Int64") {
		t.Fatal("signed minimum exemption escaped its immediate expression", analyzer.errors)
	}
}

func TestIntegralLiteralValidBoundaries(t *testing.T) {
	for _, sample := range []struct{ kind, value string }{
		{"Int", "0"}, {"Int", "00000000000000000000000000000000000001"},
		{"Int", "9223372036854775807"}, {"Int", "-9223372036854775808"},
		{"Int", "-0009223372036854775808"},
		{"Duration", "9223372036854ms"}, {"Duration", "9223372036s"},
		{"Duration", "153722867m"}, {"Duration", "2562047h"}, {"Duration", "106751d"},
		{"Duration", "-9223372036854ms"}, {"Duration", "-106751d"},
	} {
		t.Run(sample.kind+sample.value, func(t *testing.T) {
			result := analyze(t, "fn read(): "+sample.kind+" { return "+sample.value+" }")
			if len(result.Errors) != 0 {
				t.Fatal("legal literal rejected", result.Errors)
			}
		})
	}
	for _, source := range []string{
		"fn read(value: Int = -9223372036854775808): Int { return value }",
		"fn read(value: Float = -0.1): Float { return value }",
		"fn read(value: Duration = -106751d): Duration { return value }",
		"error Rejected(value: Int = -9223372036854775808) { code: 400 message: \"rejected\" }",
		"type Probe { value: Int = -9223372036854775808 }",
		"model Probe @noTime { id: Int @id value: Int @range(-9223372036854775808, 9223372036854775807) }",
		"fn text(): String { return \"92233720368547758081234567890\" }",
		"fn inverse(value: Int): Int { return -value }",
		"fn flag(): Boolean { return !false }",
	} {
		if result := analyze(t, source); len(result.Errors) != 0 {
			t.Fatal("legal default or textual data rejected", source, result.Errors)
		}
	}
}
