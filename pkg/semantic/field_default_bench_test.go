package semantic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkFieldDefaultValidation(b *testing.B) {
	for _, sample := range []struct {
		name, kind, value string
		count             int
	}{
		{"model_no_default_control", "model", "Int", 128},
		{"model_int_single", "model", "Int = 42", 1},
		{"model_int", "model", "Int = 42", 128},
		{"model_float", "model", "Float = -0.1", 128},
		{"type_nullable", "type", "String? = null", 128},
		{"type_enum", "type", "State = State.OPEN", 128},
		{"type_enum_bare", "type", "State = OPEN", 128},
		{"json_null_control", "type", "JSON = null", 128},
		{"fn_parameter_control", "fn", "Int = 42", 128},
		{"api_parameter_control", "api", "Int = 42", 128},
	} {
		b.Run(sample.name, func(b *testing.B) {
			var source strings.Builder
			source.WriteString("enum State { OPEN CLOSED }\n")
			for index := range sample.count {
				switch sample.kind {
				case "fn", "api":
					fmt.Fprintf(&source, "%s read%d(value: %s): Int { return value }\n", sample.kind, index, sample.value)
				default:
					fmt.Fprintf(&source, "%s Probe%d { value: %s }\n", sample.kind, index, sample.value)
				}
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

func BenchmarkDeclaredFieldUniqueness(b *testing.B) {
	for _, kind := range []string{"model", "type", "interface"} {
		for _, count := range []int{1, 128} {
			var source strings.Builder
			for index := range count {
				fmt.Fprintf(&source, "%s Record%d ", kind, index)
				if kind == "model" {
					source.WriteString("@noTime ")
				}
				source.WriteString("{\n")
				if kind == "model" {
					source.WriteString("id: Int @id\n")
				}
				for field := range 16 {
					fmt.Fprintf(&source, "field%d: Int\n", field)
				}
				source.WriteString("}\n")
			}
			file := parseFile(b, source.String())
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
						b.Fatal(result.Errors)
					}
				}
			})
		}
	}
}

func BenchmarkRejectedTypeDeclarations(b *testing.B) {
	for _, kind := range []string{"model", "interface", "type"} {
		for _, count := range []int{1, 128} {
			var source strings.Builder
			for index := range count {
				fmt.Fprintf(&source, "%s Entry%d { keep: Int }\n%s Entry%d { poisoned: String }\n", kind, index, kind, index)
			}
			file := parseFile(b, source.String())
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != count {
						b.Fatalf("expected %d duplicate declaration errors, got %v", count, result.Errors)
					}
				}
			})
		}
	}
}

func BenchmarkEnumSealedDeclarationControl(b *testing.B) {
	for _, count := range []int{1, 128} {
		var source strings.Builder
		for index := range count {
			fmt.Fprintf(&source, "enum State%d { READY } sealed Outcome%d { Value(value: State%d) }\n", index, index, index)
		}
		file := parseFile(b, source.String())
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}
