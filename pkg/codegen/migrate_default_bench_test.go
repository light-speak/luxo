package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

var migrationDefaultStateSink *SchemaState

func BenchmarkComputeMigrationDefaults(b *testing.B) {
	for _, mode := range []string{"none", "positive", "special"} {
		for _, count := range []int{1, 128} {
			b.Run(mode+"/"+strconv.Itoa(count), func(b *testing.B) {
				input := migrationDefaultBenchmarkInput(b, mode, count)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					state, err := ComputeState(input, nil, pgDialect())
					if err != nil {
						b.Fatal(err)
					}
					migrationDefaultStateSink = state
				}
			})
		}
	}
}

func migrationDefaultBenchmarkInput(b *testing.B, mode string, count int) *semantic.Result {
	b.Helper()
	var source strings.Builder
	for i := range count {
		source.WriteString("model Probe" + strconv.Itoa(i) + " @noTime { id: Int @id\n")
		for j := range 16 {
			declarations := []string{"Int", "Float", "Boolean", "String"}
			if mode == "positive" {
				declarations = []string{"Int = 42", "Float = 0.1", "Boolean = true", "String = \"hello\""}
			} else if mode == "special" {
				declarations = []string{"Int = -42", "Float = -0.0", "Duration = -01d", "JSON = null", "String? = null", "Boolean? = null", "Int = -9223372036854775808", "Float = 1.5"}
			}
			source.WriteString("field" + strconv.Itoa(j) + ": " + declarations[j%len(declarations)] + "\n")
		}
		source.WriteString("}\n")
	}
	tokens, lexErrors := lexer.New(source.String(), "benchmark.luxo").Tokenize()
	file, parseErrors := parser.New(tokens).Parse("benchmark.luxo")
	input := semantic.Analyze([]*ast.File{file})
	if len(lexErrors)+len(parseErrors)+len(input.Errors) != 0 {
		b.Fatal(lexErrors, parseErrors, input.Errors)
	}
	return input
}
