package semantic

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestFloatLiteralOverflowRejected(t *testing.T) {
	value := strings.Repeat("9", 309) + ".0"
	for _, sample := range []struct{ name, source string }{
		{"function", "fn read(): Float { return " + value + " }"},
		{"negative", "fn read(): Float { return -" + value + " }"},
		{"api", "api read(): Float { return " + value + " }"},
		{"function_default", "fn read(input: Float = " + value + "): Float { return input }"},
		{"stream_default", "api read(input: Float = " + value + "): Float @stream @native"},
		{"error_default", "error Rejected(value: Float = " + value + ") { code: 400 message: \"rejected\" }"},
		{"transform", "type Snapshot { value: Float @transform { " + value + " } }"},
		{"visible", "type Snapshot { value: Float? @visible(" + value + " == 0.0) }"},
		{"model_default", "model Snapshot @noTime { id: Int @id value: Float = " + value + " }"},
		{"type_default", "type Snapshot { value: Float = -" + value + " }"},
		{"range", "model Snapshot @noTime { id: Int @id value: Float @range(0.0, " + value + ") }"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := analyze(t, sample.source)
			for _, failure := range result.Errors {
				if strings.Contains(failure.Message, "finite Float64") {
					if failure.Pos.Line == 0 || failure.Pos.Col == 0 {
						t.Fatal("literal error has no source position", failure)
					}
					return
				}
			}
			t.Fatal("overflowing Float literal passed analysis", result.Errors)
		})
	}
}

func TestFloatLiteralFiniteBoundaries(t *testing.T) {
	maximum := strconv.FormatFloat(math.MaxFloat64, 'f', -1, 64) + ".0"
	for _, value := range []string{"0.0", "-0.0", "00.125", "0.1", maximum, "-" + maximum, "0." + strings.Repeat("0", 323) + "5", "0." + strings.Repeat("0", 324) + "1"} {
		result := analyze(t, "fn read(): Float { return "+value+" }")
		if len(result.Errors) != 0 {
			t.Errorf("finite Float rejected: %s: %v", value, result.Errors)
		}
	}
}

func TestFloatLiteralMalformedAST(t *testing.T) {
	for _, text := range []string{"NaN", "Inf", "-Inf", "invalid"} {
		analyzer := New()
		analyzer.literalType(&ast.Literal{Kind: token.Float, Value: text})
		if len(analyzer.errors) != 1 || !strings.Contains(analyzer.errors[0].Message, "finite Float64") {
			t.Errorf("invalid Float AST accepted: %q: %v", text, analyzer.errors)
		}
	}
}
