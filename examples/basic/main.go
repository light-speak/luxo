// Command basic generates Go code from schema.luxo to demonstrate the Luxo codegen pipeline.
//
// Usage:
//
//	go run examples/basic/main.go
//
// Output is written to examples/basic/gen/.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func main() {
	schemaPath := filepath.Join("examples", "basic", "schema.luxo")
	outDir := filepath.Join("examples", "basic", "gen")

	// Read schema file
	content, err := os.ReadFile(schemaPath)
	if err != nil {
		fatal("read schema: %v", err)
	}

	// Phase 1: Lex
	l := lexer.New(string(content), schemaPath)
	tokens, lexErrs := l.Tokenize()
	if len(lexErrs) > 0 {
		for _, e := range lexErrs {
			fmt.Fprintf(os.Stderr, "lex error: %v\n", e)
		}
		os.Exit(1)
	}

	// Phase 2: Parse
	p := parser.New(tokens)
	file, parseErrs := p.Parse(schemaPath)
	if len(parseErrs) > 0 {
		for _, e := range parseErrs {
			fmt.Fprintf(os.Stderr, "parse error: %v\n", e)
		}
		os.Exit(1)
	}

	// Phase 3: Semantic analysis
	a := semantic.New()
	result := a.Analyze([]*ast.File{file})
	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintf(os.Stderr, "semantic error: %v\n", e)
		}
		os.Exit(1)
	}
	if len(result.Warnings) > 0 {
		for _, w := range result.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %v\n", w)
		}
	}

	// Phase 4: Code generation
	gr := codegen.Generate(result, "gen")

	// Write output files
	for name, src := range gr.Files {
		outPath := filepath.Join(outDir, name)
		if err := os.WriteFile(outPath, src, 0644); err != nil {
			fatal("write %s: %v", outPath, err)
		}
		fmt.Printf("  generated: %s (%d bytes)\n", outPath, len(src))
	}

	fmt.Println("done.")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
