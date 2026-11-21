package semantic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkAnalyzeSealedPayloads(b *testing.B) {
	for _, sample := range []struct {
		name             string
		variants, fields int
	}{
		{"scalar_control", 0, 0},
		{"one_field", 1, 1},
		{"sixteen_fields", 1, 16},
		{"many_variants", 128, 1},
		{"large_payload_catalog", 128, 16},
	} {
		var source strings.Builder
		if sample.variants == 0 {
			source.WriteString("fn echo(value: Int): Int { return value }")
		} else {
			source.WriteString("sealed Outcome {\n")
			for variant := range sample.variants {
				fmt.Fprintf(&source, "Value%d(", variant)
				for field := range sample.fields {
					if field != 0 {
						source.WriteString(", ")
					}
					fmt.Fprintf(&source, "field%d: Int", field)
				}
				source.WriteString(")\n")
			}
			source.WriteString("}")
		}
		file := parseFile(b, source.String())
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}
