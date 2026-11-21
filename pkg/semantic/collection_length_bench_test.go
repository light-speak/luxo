package semantic

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

// Parse once; these inputs were valid before the member checks were added.
func BenchmarkAnalyzeLengthMembers(b *testing.B) {
	for _, sample := range []struct {
		name, declarations, signature, body string
		count                               int
	}{
		{"scalar", "", "(value: Int): Int", "return value", 1},
		{"field16", "type Box { text: String }", "(value: Box): String", "return value.text", 16},
		{"list1", "", "(value: [Int]): Int", "return value.size()", 1},
		{"list32", "", "(value: [Int]): Int", "return value.size()", 32},
		{"string32", "", "(value: String): Boolean", "return value.contains(\"x\")", 32},
		{"map16", "", "(value: [Int]): [Int]", "return value.map { it + 1 }", 16},
	} {
		b.Run(sample.name, func(b *testing.B) {
			var source strings.Builder
			source.WriteString(sample.declarations)
			for index := range sample.count {
				source.WriteString("\nfn read" + strconv.Itoa(index) + sample.signature + " { " + sample.body + " }")
			}
			file := parseFile(b, source.String())
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}
