package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

const (
	codegenReceiverFixture       = "global_value_bench_test.go"
	codegenReceiverLine          = 46
	codegenReceiverProjectionSHA = "330fc5c6785cae1fd2e89810a1ec945e9384669a9efaa3af47cc5e3a2bf74da3"

	codegenMigrationStateFixture    = "migrate_state_bench_test.go"
	codegenMigrationStateFixtureSHA = "08bf4e4e370167fd7f5d91cc0e420ecb09302576a76dbb9e767202316cf8ce50"
)

func writeCodegenReceiverOverlay(pkg, workspace string) (string, error) {
	return writeCodegenReceiverFixtures(pkg, workspace, "")
}

func writeCodegenMigrationStateOverlay(pkg, workspace string) (string, error) {
	data, err := readRegularTestFixture(filepath.Join(pkg, codegenMigrationStateFixture))
	if err != nil {
		return "", fmt.Errorf("codegen migration state fixture: %w", err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != codegenMigrationStateFixtureSHA {
		return "", errors.New("source does not match the verified migration fixture")
	}
	// The verified binary compiled the original migration file alongside the receiver projection.
	return writeCodegenReceiverFixtures(pkg, workspace, codegenMigrationStateFixture)
}

func writeCodegenReceiverFixtures(pkg, workspace, keptFixture string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	replacements := make(map[string]string)
	for _, entry := range entries {
		name := entry.Name()
		if name == keptFixture || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkg, name)
		replacement, err := projectCodegenReceiverFixture(path, workspace)
		if err != nil {
			return "", fmt.Errorf("codegen receiver fixture %s: %w", name, err)
		}
		replacements[path] = replacement
	}
	if replacements[filepath.Join(pkg, codegenReceiverFixture)] == "" {
		return "", errors.New("missing codegen receiver benchmark fixture")
	}
	return writeTestOverlay(workspace, replacements)
}

func projectCodegenReceiverFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	if err != nil || filepath.Base(path) != codegenReceiverFixture {
		return "", err
	}
	if file.Name.Name != "codegen" {
		return "", fmt.Errorf("unexpected package %q", file.Name.Name)
	}
	for _, declaration := range file.Decls {
		if isFunction(declaration, benchlayout.CodegenReceiverBenchmark) {
			return writeCodegenReceiverProjection(workspace, positions.PositionFor(declaration.Pos(), false), data)
		}
	}
	return "", fmt.Errorf("missing %s declaration", benchlayout.CodegenReceiverBenchmark)
}

func writeCodegenReceiverProjection(workspace string, start token.Position, data []byte) (string, error) {
	if start.Line != codegenReceiverLine {
		return "", fmt.Errorf("receiver benchmark must start on line %d", codegenReceiverLine)
	}
	// Keep the tested registration and line numbers with the same minimal import.
	const header = "package codegen\n\nimport \"testing\"\n"
	projected := append([]byte(header+strings.Repeat("\n", start.Line-1-strings.Count(header, "\n"))), data[start.Offset:]...)
	if fmt.Sprintf("%x", sha256.Sum256(projected)) != codegenReceiverProjectionSHA {
		return "", errors.New("receiver benchmark declaration does not match the verified projection")
	}
	path := filepath.Join(workspace, codegenReceiverFixture)
	return path, os.WriteFile(path, projected, 0o600)
}
