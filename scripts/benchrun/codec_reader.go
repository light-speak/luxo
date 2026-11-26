package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func codecReaderOverlay(root string) (string, func() error, error) {
	return packageTestOverlay(root, benchlayout.CodecPackage, "bench-codec-reader-*", writeCodecReaderOverlay)
}

func packageTestOverlay(root, packagePath, pattern string, write func(string, string) (string, error)) (string, func() error, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, err
	}
	pkg := filepath.Join(realRoot, packagePath)
	resolved, err := filepath.EvalSymlinks(pkg)
	if err != nil {
		return "", nil, err
	}
	if resolved != pkg {
		return "", nil, fmt.Errorf("benchmark package must stay at %s", pkg)
	}
	workspace, err := os.MkdirTemp(filepath.Join(realRoot, ".tmp"), pattern)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error { return os.RemoveAll(workspace) }
	path, err := write(pkg, workspace)
	if err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return path, cleanup, nil
}

func writeCodecReaderOverlay(pkg, workspace string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	replacements := make(map[string]string)
	selected := 0
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, "_test.go") {
			source := filepath.Join(pkg, name)
			projected, err := projectCodecReaderFile(source, workspace, entry.Type())
			if err != nil {
				return "", fmt.Errorf("codec reader fixture %s: %w", name, err)
			}
			replacements[source] = projected
			if projected != "" {
				selected++
			}
		}
	}
	if selected != 3 {
		return "", errors.New("codec reader profile requires its three payload fixtures")
	}
	return writeTestOverlay(workspace, replacements)
}

func writeTestOverlay(workspace string, replacements map[string]string) (string, error) {
	overlay := struct{ Replace map[string]string }{replacements}
	path := filepath.Join(workspace, "overlay.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	return path, errors.Join(json.NewEncoder(file).Encode(overlay), file.Close())
}

func projectCodecReaderFile(source, workspace string, mode os.FileMode) (string, error) {
	if !mode.IsRegular() {
		return "", errors.New("fixture must be a regular file")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	name := filepath.Base(source)
	projected, err := projectCodecReader(name, data)
	if err != nil || projected == nil {
		return "", err
	}
	path := filepath.Join(workspace, name)
	return path, os.WriteFile(path, projected, 0o600)
}

func parseBenchmarkFixture(fset *token.FileSet, name string, data []byte) (*ast.File, error) {
	file, err := parser.ParseFile(fset, name, data, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	for _, declaration := range file.Decls {
		if isFunction(declaration, "init") || isFunction(declaration, "TestMain") {
			return nil, fmt.Errorf("cannot isolate test package setup in %s", name)
		}
	}
	return file, nil
}

func projectCodecReader(name string, data []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parseBenchmarkFixture(fset, name, data)
	if err != nil {
		return nil, err
	}
	positions := fset.File(file.Pos())
	switch name {
	case "payload_bench_test.go":
		if err := stripMissingPayload(data, positions, file.Decls); err != nil {
			return nil, err
		}
	case "payload_reference_test.go":
		// The historical V0 reader is part of the same workload.
	case "payload_test.go":
		if err := stripPayloadCorrectness(data, positions, file.Decls); err != nil {
			return nil, err
		}
	default:
		return nil, nil
	}
	return data, nil
}

func stripMissingPayload(data []byte, positions *token.File, declarations []ast.Decl) error {
	for i, decl := range declarations {
		if !isFunction(decl, "BenchmarkMissingElementPayload") {
			continue
		}
		if i == 0 || !isFunction(declarations[i-1], benchlayout.CodecReaderBenchmark) ||
			i+1 >= len(declarations) || !isFunction(declarations[i+1], "benchmarkPayloadRead") {
			break
		}
		clearGoSpan(data[positions.Offset(decl.Pos()):positions.Offset(declarations[i+1].Pos())])
		return nil
	}
	return errors.New("expected payload reader and missing-element benchmark declarations")
}

func stripPayloadCorrectness(data []byte, positions *token.File, declarations []ast.Decl) error {
	if len(declarations) < 4 || !isImport(declarations[0]) || !isPayloadReaderType(declarations[1]) ||
		!isFunction(declarations[2], "payloadReaderCases") || !isFunction(declarations[3], "TestElementPayloadPreflight") {
		return errors.New("expected payload reader fixture declarations before correctness tests")
	}
	clearGoSpan(data[positions.Offset(declarations[0].Pos()):positions.Offset(declarations[1].Pos())])
	clearGoSpan(data[positions.Offset(declarations[3].Pos()):])
	return nil
}

func isFunction(decl ast.Decl, name string) bool {
	fn, ok := decl.(*ast.FuncDecl)
	return ok && fn.Recv == nil && fn.Name.Name == name
}

func isImport(decl ast.Decl) bool {
	gen, ok := decl.(*ast.GenDecl)
	return ok && gen.Tok == token.IMPORT
}

func isPayloadReaderType(decl ast.Decl) bool {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.TYPE || len(gen.Specs) != 1 {
		return false
	}
	return gen.Specs[0].(*ast.TypeSpec).Name.Name == "payloadReaderCase"
}

// Preserve line and byte offsets of every retained declaration for the compiler.
func clearGoSpan(data []byte) {
	for i, b := range data {
		if b != '\n' {
			data[i] = ' '
		}
	}
}
