package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func BenchmarkGenerateAsyncVoidReturn(b *testing.B) {
	for _, statement := range []string{"done.close()", "return done.close()"} {
		name := "statement"
		if strings.HasPrefix(statement, "return ") {
			name = "return"
		}
		b.Run(name, func(b *testing.B) {
			source := "model Counter @noTime { id: Int @id value: Int }\nfn stop(done: Channel<Int>) { async { " + statement + " } }"
			tokens, lexErrors := lexer.New(source, "async-return.luxo").Tokenize()
			file, parseErrors := parser.New(tokens).Parse("async-return.luxo")
			if len(lexErrors) != 0 || len(parseErrors) != 0 {
				b.Fatal(lexErrors, parseErrors)
			}
			input := semantic.Analyze([]*ast.File{file})
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: asyncFailureIDs()}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

var asyncEmissionBenchmarkSink string

func BenchmarkCompileAsyncEmission(b *testing.B) {
	for _, count := range []int{0, 1, 2} {
		name := []string{"control", "single", "double"}[count]
		body := &ast.Block{}
		for range count {
			body.Stmts = append(body.Stmts, &ast.EmitStmt{EventName: "Ping"})
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				c := newCompiler(nil)
				asyncEmissionBenchmarkSink = c.compileAsync(&ast.AsyncExpr{Body: body})
			}
		})
	}
}

func BenchmarkGenerateAsyncProgram(b *testing.B) {
	tokens, lexErrors := lexer.New(asyncFailureSource, "async.luxo").Tokenize()
	file, parseErrors := parser.New(tokens).Parse("async.luxo")
	if len(lexErrors) != 0 || len(parseErrors) != 0 {
		b.Fatal(lexErrors, parseErrors)
	}
	input := semantic.Analyze([]*ast.File{file})
	if len(input.Errors) != 0 {
		b.Fatal(input.Errors)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: asyncFailureIDs()}); err != nil {
			b.Fatal(err)
		}
	}
}
