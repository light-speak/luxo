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

func BenchmarkGlobalConstantGenerate(b *testing.B) {
	for _, count := range []int{0, 1, 128} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			var source strings.Builder
			for index := range count {
				source.WriteString("val item" + strconv.Itoa(index) + " = " + strconv.Itoa(index) + "\n")
			}
			value := "42"
			if count != 0 {
				value = "item" + strconv.Itoa(count-1)
			}
			source.WriteString("fn read(): Int { return " + value + " }")
			tokens, lexErrors := lexer.New(source.String(), "global.luxo").Tokenize()
			file, parseErrors := parser.New(tokens).Parse("global.luxo")
			result := semantic.Analyze([]*ast.File{file})
			if len(lexErrors)+len(parseErrors)+len(result.Errors) != 0 {
				b.Fatal(lexErrors, parseErrors, result.Errors)
			}
			generator, err := NewGenerator(GeneratorConfig{Driver: DriverPG})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := generator.Generate(result, "generated", nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGlobalReceiverName(b *testing.B) {
	for _, name := range []string{"User", "_Record", "用户"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if receiverName(name) == "" {
					b.Fatal("empty receiver")
				}
			}
		})
	}
}
