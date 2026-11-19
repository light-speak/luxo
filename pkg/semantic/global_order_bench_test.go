package semantic

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

func BenchmarkCommonGlobalAnalysis(b *testing.B) {
	for _, count := range []int{0, 1, 128} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			var source strings.Builder
			for index := range count {
				source.WriteString("val VALUE_" + strconv.Itoa(index) + " = " + strconv.Itoa(index) + "\n")
			}
			body := "api read(): Int { return 42 }"
			if count != 0 {
				body = "api read(): Int { return VALUE_" + strconv.Itoa(count-1) + " }"
			}
			var files []*ast.File
			for _, item := range []struct{ path, source string }{{"origin/common/config.luxo", source.String()}, {"origin/alpha/main.luxo", body}} {
				tokens, le := lexer.New(item.source, item.path).Tokenize()
				file, pe := parser.New(tokens).Parse(item.path)
				if len(le)+len(pe) != 0 {
					b.Fatal(le, pe)
				}
				files = append(files, file)
			}
			b.ReportAllocs()
			for b.Loop() {
				if result := AnalyzeModules(files); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}
