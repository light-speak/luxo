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

var apiIdentityFixtures = []string{
	"identity_reader_bench_test.go",
	"identity_reader_fuzz_test.go",
	"identity_reader_test.go",
	"identity_value_test.go",
}

const luviaObservationFixture = "metrics_retention_test.go"

var luviaPrimaryTraceFixtures = map[string]bool{
	"metrics_json_reference_test.go": true,
	"metrics_json_test.go":           true,
	luviaObservationFixture:          true,
	"primary_keys_baseline_test.go":  true,
	"primary_keys_bench_test.go":     true,
}

func writeAPIBenchmarkOverlay(pkg, workspace string) (string, error) {
	replacements := make(map[string]string, len(apiIdentityFixtures))
	for _, name := range apiIdentityFixtures {
		path := filepath.Join(pkg, name)
		if err := validateAPIFixture(path); err != nil {
			return "", fmt.Errorf("API identity fixture %s: %w", name, err)
		}
		replacements[path] = ""
	}
	return writeTestOverlay(workspace, replacements)
}

func validateAPIFixture(path string) error {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return err
	}
	file, err := parseBenchmarkFixture(token.NewFileSet(), path, data)
	if err != nil {
		return err
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Benchmark") &&
			benchlayout.ProfileForBenchmark(benchlayout.APIPackage, fn.Name.Name) != benchlayout.DefaultProfile {
			return fmt.Errorf("non-identity benchmark %s uses the ordinary identity fixture", fn.Name.Name)
		}
	}
	return nil
}

func writeLuviaBenchmarkOverlay(pkg, workspace string) (string, error) {
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
		replacement, err := projectLuviaFixture(path, workspace)
		if err != nil {
			return "", fmt.Errorf("Luvia primary and trace fixture %s: %w", name, err)
		}
		if luviaPrimaryTraceFixtures[name] {
			retained++
			if replacement == "" {
				continue
			}
		}
		replacements[path] = replacement
	}
	if retained != len(luviaPrimaryTraceFixtures) {
		return "", errors.New("missing Luvia primary and trace test fixture")
	}
	return writeTestOverlay(workspace, replacements)
}

func writeAPILocalRouteExportOverlay(pkg, workspace string) (string, error) {
	return retainBenchmarkTestFiles(pkg, workspace, "API local route export", "local_input_bench_test.go")
}

func writeLuviaMergeDestinationOverlay(pkg, workspace string) (string, error) {
	// The merge benchmark shares the primary-key input builder and its baseline fixture.
	return retainBenchmarkTestFiles(pkg, workspace, "Luvia merge destination",
		"merger_bench_test.go", "primary_keys_bench_test.go", "primary_keys_baseline_test.go")
}

func retainBenchmarkTestFiles(pkg, workspace, description string, fixtures ...string) (string, error) {
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
		data, err := readRegularTestFixture(path)
		if err != nil {
			return "", fmt.Errorf("%s fixture %s: %w", description, name, err)
		}
		if _, err := parseBenchmarkFixture(token.NewFileSet(), path, data); err != nil {
			return "", err
		}
		if slices.Contains(fixtures, name) {
			retained++
			continue
		}
		replacements[path] = ""
	}
	if retained != len(fixtures) {
		return "", fmt.Errorf("missing %s test fixture", description)
	}
	return writeTestOverlay(workspace, replacements)
}

func projectLuviaFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	if err != nil || filepath.Base(path) != luviaObservationFixture {
		return "", err
	}
	for _, declaration := range file.Decls {
		if isFunction(declaration, "observationTestTrace") {
			return writeLuviaObservation(workspace, positions, declaration, data)
		}
	}
	return "", errors.New("missing observationTestTrace declaration")
}

func writeLuviaObservation(workspace string, positions *token.FileSet, declaration ast.Decl, data []byte) (string, error) {
	const header = "package luvia\n\nimport (\n\t\"net/http\"\n\t\"time\"\n\n\t\"github.com/light-speak/luxo/pkg/lux/api\"\n)\n\n"
	file := positions.File(declaration.Pos())
	projected := append([]byte(header), data[file.Offset(declaration.Pos()):file.Offset(declaration.End())]...)
	path := filepath.Join(workspace, luviaObservationFixture)
	return path, os.WriteFile(path, append(projected, '\n'), 0o600)
}

func readRegularTestFixture(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("fixture must be a regular file")
	}
	return os.ReadFile(path)
}
