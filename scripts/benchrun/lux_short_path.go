package main

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

const (
	luxFilterFixture  = "condition_filter_bench_test.go"
	luxDecimalFixture = "decimal_bench_test.go"

	// These are the byte-preserving projections measured by the native A/A and A/H controls.
	luxFilterProjectionSHA  = "03d082b02bcd5afc6fac26ec6bac93e51b3eb36a05b6c79ab12dbe0752249bd6"
	luxDecimalProjectionSHA = "b175de4cf8476cc4eeef7f115b142e542b8b82d25b79341916ae8e207ff55c6f"
)

var luxShortPathProjections = map[string]string{
	luxFilterFixture:  luxFilterProjectionSHA,
	luxDecimalFixture: luxDecimalProjectionSHA,
}

func writeLuxShortPathOverlay(pkg, workspace string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	replacements := make(map[string]string)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(pkg, entry.Name())
		replacement, err := projectLuxShortPathFixture(path, workspace)
		if err != nil {
			return "", fmt.Errorf("Lux short-path fixture %s: %w", entry.Name(), err)
		}
		replacements[path] = replacement
	}
	for _, name := range []string{luxFilterFixture, luxDecimalFixture} {
		if replacements[filepath.Join(pkg, name)] == "" {
			return "", fmt.Errorf("missing Lux short-path fixture %s", name)
		}
	}
	return writeTestOverlay(workspace, replacements)
}

func projectLuxShortPathFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	name := filepath.Base(path)
	digest, retained := luxShortPathProjections[name]
	if err != nil || !retained {
		return "", err
	}
	if file.Name.Name != "lux" {
		return "", fmt.Errorf("unexpected package %q", file.Name.Name)
	}
	if name == luxDecimalFixture {
		retainLuxDecimalAlignment(data, positions.File(file.Pos()), file.Decls)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
		return "", fmt.Errorf("%s does not match the native short-path projection", name)
	}
	projected := filepath.Join(workspace, name)
	return projected, os.WriteFile(projected, data, 0o600)
}

func retainLuxDecimalAlignment(data []byte, positions *token.File, declarations []ast.Decl) {
	for _, declaration := range declarations {
		if isImport(declaration) || isFunction(declaration, benchlayout.LuxDecimalAlignmentBenchmark) {
			continue
		}
		clearGoSpan(data[positions.Offset(declaration.Pos()):positions.Offset(declaration.End())])
	}
}
