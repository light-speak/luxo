package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

type canonicalFixture struct {
	declarations []string
	imports      []string
}

var apiCanonicalFixtures = map[string]canonicalFixture{
	"binary_bench_test.go": {declarations: []string{
		"BenchmarkCanonicalJSONStructured", "BenchmarkCanonicalJSONTyped", "BenchmarkCanonicalJSONBytes",
		"BenchmarkCanonicalJSONFloat", "BenchmarkCanonicalJSONValue", "BenchmarkCanonicalJSONInteger",
		"canonicalIntegerSink", "opaqueJSONBenchmarkCase", "benchmarkCanonicalJSONValue",
		"canonicalJSONBenchmarkGolden", "encodeOpaqueJSONReference", "beforeOpaqueJSONValue", "beforeJSONInteger",
	}},
	"request_scalar_bench_test.go": {declarations: []string{"benchmarkStringArray"}, imports: []string{`"strconv"`, `"strings"`}},
}

func writeAPICanonicalJSONOverlay(pkg, workspace string) (string, error) {
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
		replacement, err := projectAPICanonicalFixture(path, workspace)
		if err != nil {
			return "", fmt.Errorf("API CanonicalJSON fixture %s: %w", name, err)
		}
		if replacement != "" {
			retained++
		}
		replacements[path] = replacement
	}
	if retained != len(apiCanonicalFixtures) {
		return "", errors.New("missing API CanonicalJSON fixture")
	}
	return writeTestOverlay(workspace, replacements)
}

func projectAPICanonicalFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	if err != nil {
		return "", err
	}
	name := filepath.Base(path)
	fixture, keep := apiCanonicalFixtures[name]
	if !keep {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil &&
				benchlayout.ProfileForBenchmark(benchlayout.APIPackage, fn.Name.Name) == benchlayout.APICanonicalJSONProfile {
				return "", fmt.Errorf("unexpected CanonicalJSON benchmark %s", fn.Name.Name)
			}
		}
		return "", nil
	}
	if err := retainAPICanonicalDeclarations(data, positions.File(file.Pos()), file.Decls, fixture); err != nil {
		return "", err
	}
	output := filepath.Join(workspace, name)
	return output, os.WriteFile(output, data, 0o600)
}

func retainAPICanonicalDeclarations(data []byte, pos *token.File, declarations []ast.Decl, fixture canonicalFixture) error {
	found := make(map[string]bool, len(fixture.declarations))
	for _, decl := range declarations {
		if isImport(decl) {
			retainAPICanonicalImports(data, pos, decl.(*ast.GenDecl), fixture.imports)
			continue
		}
		if name := apiCanonicalDeclarationName(decl); slices.Contains(fixture.declarations, name) {
			if found[name] {
				return fmt.Errorf("duplicate CanonicalJSON declaration %s", name)
			}
			found[name] = true
			continue
		}
		clearGoSpan(data[pos.Offset(decl.Pos()):pos.Offset(decl.End())])
	}
	if len(found) != len(fixture.declarations) {
		return errors.New("CanonicalJSON fixture declarations changed")
	}
	return nil
}

func retainAPICanonicalImports(data []byte, pos *token.File, group *ast.GenDecl, imports []string) {
	if imports == nil {
		return
	}
	for _, item := range group.Specs {
		spec := item.(*ast.ImportSpec)
		if slices.Contains(imports, spec.Path.Value) {
			continue
		}
		start := spec.Pos()
		if !group.Lparen.IsValid() {
			start = group.Pos()
		}
		clearGoSpan(data[pos.Offset(start):pos.Offset(spec.End())])
	}
}

func apiCanonicalDeclarationName(decl ast.Decl) string {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		if decl.Recv == nil {
			return decl.Name.Name
		}
	case *ast.GenDecl:
		if len(decl.Specs) == 1 {
			switch spec := decl.Specs[0].(type) {
			case *ast.TypeSpec:
				return spec.Name.Name
			case *ast.ValueSpec:
				if len(spec.Names) == 1 {
					return spec.Names[0].Name
				}
			}
		}
	}
	return ""
}
