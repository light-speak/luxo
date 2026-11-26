package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

type codegenLiteralFixture struct {
	function string
	imports  []string
	sink     bool
}

var codegenLiteralFixtures = map[string]codegenLiteralFixture{
	"compile_test.go": {
		function: "newCompiler",
		imports:  []string{"strings", "github.com/light-speak/luxo/pkg/ast"},
	},
	"numeric_literal_bench_test.go": {
		function: benchlayout.CodegenLiteralBenchmark,
		imports:  []string{"testing", "github.com/light-speak/luxo/pkg/ast", "github.com/light-speak/luxo/pkg/token"},
		sink:     true,
	},
}

func writeCodegenLiteralOverlay(pkg, workspace string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	replacements := make(map[string]string)
	retained := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkg, name)
		replacement, err := projectCodegenLiteralFixture(path, workspace)
		if err != nil {
			return "", fmt.Errorf("codegen literal fixture %s: %w", name, err)
		}
		replacements[path] = replacement
		if replacement != "" {
			retained++
		}
	}
	if retained != len(codegenLiteralFixtures) {
		return "", errors.New("missing codegen literal benchmark fixture")
	}
	return writeTestOverlay(workspace, replacements)
}

func projectCodegenLiteralFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	name := filepath.Base(path)
	fixture, retained := codegenLiteralFixtures[name]
	if err != nil || !retained {
		return "", err
	}
	if file.Name.Name != "codegen" {
		return "", fmt.Errorf("unexpected package %q", file.Name.Name)
	}
	if err := retainCodegenLiteralDeclarations(data, positions.File(file.Pos()), file.Decls, fixture); err != nil {
		return "", err
	}
	projected := filepath.Join(workspace, name)
	return projected, os.WriteFile(projected, data, 0o600)
}

func retainCodegenLiteralDeclarations(data []byte, pos *token.File, declarations []ast.Decl, fixture codegenLiteralFixture) error {
	imports := make(map[string]struct{}, len(fixture.imports))
	for _, name := range fixture.imports {
		imports[name] = struct{}{}
	}
	var function, sink bool
	for _, declaration := range declarations {
		switch {
		case isImport(declaration):
			group := declaration.(*ast.GenDecl)
			for _, item := range group.Specs {
				spec := item.(*ast.ImportSpec)
				// Parsing the fixture already validated the Go string literal.
				name, _ := strconv.Unquote(spec.Path.Value)
				if _, keep := imports[name]; keep && spec.Name == nil {
					delete(imports, name)
					continue
				}
				start := spec.Pos()
				if !group.Lparen.IsValid() {
					start = group.Pos()
				}
				clearGoSpan(data[pos.Offset(start):pos.Offset(spec.End())])
			}
		case !function && isFunction(declaration, fixture.function):
			function = true
		case fixture.sink && !sink && isNumericLiteralSink(declaration):
			sink = true
		default:
			clearGoSpan(data[pos.Offset(declaration.Pos()):pos.Offset(declaration.End())])
		}
	}
	if len(imports) != 0 || !function || fixture.sink != sink {
		return errors.New("codegen literal fixture declarations or imports changed")
	}
	return nil
}

func isNumericLiteralSink(decl ast.Decl) bool {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR || len(gen.Specs) != 1 {
		return false
	}
	value := gen.Specs[0].(*ast.ValueSpec)
	typeName, ok := value.Type.(*ast.Ident)
	return ok && typeName.Name == "string" && len(value.Values) == 0 &&
		len(value.Names) == 1 && value.Names[0].Name == "numericLiteralSourceSink"
}
