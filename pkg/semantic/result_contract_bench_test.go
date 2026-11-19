package semantic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkAnalyzeNativeResultShapes(b *testing.B) {
	for _, count := range []int{1, 128} {
		var source strings.Builder
		for index := range count {
			fmt.Fprintf(&source, "fn read%d(value: Int): Result<Int> @native\n", index)
		}
		file := parseFile(b, source.String())
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				result := Analyze([]*ast.File{file})
				if len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}
