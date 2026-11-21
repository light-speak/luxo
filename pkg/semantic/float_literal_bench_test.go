package semantic

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

var floatLiteralTypeSink *ResolvedType

func BenchmarkFloatLiteralRange(b *testing.B) {
	for _, sample := range []struct{ name, value string }{
		{"short", "0.1"},
		{"maximum", strconv.FormatFloat(math.MaxFloat64, 'f', -1, 64) + ".0"},
		{"subnormal", "0." + strings.Repeat("0", 323) + "5"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			analyzer := New()
			literal := &ast.Literal{Kind: token.Float, Value: sample.value}
			b.ReportAllocs()
			for b.Loop() {
				floatLiteralTypeSink = analyzer.literalType(literal)
			}
		})
	}
}

func BenchmarkAnalyzeFloatLiteralRange(b *testing.B) {
	for _, sample := range []struct{ name, typ, value string }{{"float", "Float", "0.1"}, {"int_control", "Int", "1"}} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				for index := range count {
					fmt.Fprintf(&source, "fn read%d(): %s { return %s }\n", index, sample.typ, sample.value)
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
}

func BenchmarkAnalyzeFloatDeclarationRange(b *testing.B) {
	for _, sample := range []struct{ name, suffix string }{{"default", " = -0.1"}, {"range", " @range(0.0, 1.0)"}} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				source.WriteString("model Snapshot @noTime { id: Int @id ")
				for index := range count {
					fmt.Fprintf(&source, "value%c%c: Float%s ", 'a'+index/26, 'a'+index%26, sample.suffix)
				}
				source.WriteString("}")
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
}
