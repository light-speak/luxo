package semantic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

// This compares semantic passes on an already parsed schema. It does not
// measure parsing, code generation, request handling, or the visitor migration.
func BenchmarkCacheEffectAnalysis(b *testing.B) {
	policies := testCachePolicies(b, "1m")
	for _, cached := range []bool{false, true} {
		source := cacheEffectBenchmarkSource(cached)
		tokens, lexErrors := lexer.New(source, "cache.luxo").Tokenize()
		if len(lexErrors) != 0 {
			b.Fatal(lexErrors)
		}
		file, parseErrors := parser.New(tokens).Parse("cache.luxo")
		if len(parseErrors) != 0 {
			b.Fatal(parseErrors)
		}
		files := []*ast.File{file}
		for _, validate := range []bool{false, true} {
			b.Run(fmt.Sprintf("cached=%t/check=%t", cached, validate), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					analyzer := New()
					analyzer.cachePolicies = policies
					analyzer.files = files
					analyzer.runDeclarationPass(files)
					analyzer.runTypePass(files)
					analyzer.runBodyPass(files)
					analyzer.checkEventCycles()
					analyzer.analyzeQueries(file)
					if validate {
						analyzer.validateCacheEffects(files)
					}
					if len(analyzer.errors) != 0 {
						b.Fatal(analyzer.errors)
					}
				}
			})
		}
	}
}

func cacheEffectBenchmarkSource(cached bool) string {
	var source strings.Builder
	source.WriteString("model User { id: Int @id }\nfn helper0(): Int { return User.count() }\n")
	for i := 1; i < 32; i++ {
		fmt.Fprintf(&source, "fn helper%d(): Int { return helper%d() }\n", i, i-1)
	}
	annotation := ""
	if cached {
		annotation = `@cache(policy: "preview")`
	}
	for i := range 8 {
		fmt.Fprintf(&source, "api result%d(): Int %s { return helper31() }\n", i, annotation)
	}
	return source.String()
}
