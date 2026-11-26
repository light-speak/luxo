package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type family struct {
	packagePath, packageName, fixture, originalSHA string
	imports, declarations                          []string
}

var families = map[string]family{
	"codegen": {
		packagePath: "pkg/codegen", packageName: "codegen", fixture: "dataloader_nullable_bench_test.go",
		originalSHA: "4f58c514dc0f2ac9c310a3142e961c15cbaffa47b2dc745192ea3b30be95729e",
		imports:     []string{`"strconv"`, `"testing"`, `"github.com/light-speak/luxo/pkg/ast"`, `"github.com/light-speak/luxo/pkg/semantic"`}, declarations: []string{"relationBenchmarkInput", "BenchmarkRelationLoaderGeneration"},
	},
	"semantic": {
		packagePath: "pkg/semantic", packageName: "semantic", fixture: "analyzer_bench_test.go",
		originalSHA: "ee8320cdca18a8700a37b504b8ee2f3ff05624c8e25a6ef35fe9ea8d4bad2a51",
		imports:     []string{`"fmt"`, `"testing"`, `"github.com/light-speak/luxo/pkg/ast"`}, declarations: []string{"BenchmarkCollectEventEmits"},
	},
}

type overlay struct {
	Replace map[string]string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: project <source> <output> <codegen|semantic>")
	}
	selected, ok := families[args[2]]
	if !ok {
		return fmt.Errorf("unknown benchmark family %q", args[2])
	}
	root, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	output, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	if output == root || strings.HasPrefix(output, root+string(filepath.Separator)) {
		return fmt.Errorf("projection output must be outside the source snapshot")
	}
	pkg := filepath.Join(root, filepath.FromSlash(selected.packagePath))
	fixture := filepath.Join(pkg, selected.fixture)
	files, err := testFiles(pkg)
	if err != nil {
		return err
	}
	if _, ok := files[fixture]; !ok {
		return fmt.Errorf("missing test fixture %q", fixture)
	}
	source, err := os.ReadFile(fixture)
	if err != nil {
		return err
	}
	projected, err := projectFixture(fixture, source, selected)
	if err != nil {
		return err
	}
	return writeProjection(output, fixture, files, selected, projected)
}

func testFiles(pkg string) (map[string]string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return nil, err
	}
	files := make(map[string]string)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("test source must be a regular file: %s", entry.Name())
		}
		files[filepath.Join(pkg, entry.Name())] = ""
	}
	return files, nil
}

func projectFixture(path string, source []byte, selected family) ([]byte, error) {
	if fmt.Sprintf("%x", sha256.Sum256(source)) != selected.originalSHA {
		return nil, fmt.Errorf("benchmark source differs from its verified input: %s", path)
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil || file.Name.Name != selected.packageName {
		return nil, fmt.Errorf("could not read pinned benchmark package %s: %v", path, err)
	}
	projected := bytes.Clone(source)
	for i, ch := range projected {
		if ch != '\n' && ch != '\r' {
			projected[i] = ' '
		}
	}
	position := positions.File(file.Package)
	keep := func(first, last token.Pos) {
		start, end := position.Offset(first), position.Offset(last)
		copy(projected[start:end], source[start:end])
	}
	keep(file.Package, file.Name.End())
	if err := retainDeclarations(file, selected, keep); err != nil {
		return nil, err
	}
	return projected, nil
}

func retainDeclarations(file *ast.File, selected family, keep func(token.Pos, token.Pos)) error {
	var imports, functions []string
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.GenDecl:
			if node.Tok == token.IMPORT {
				imports = append(imports, retainImports(node, selected.imports, keep)...)
			}
		case *ast.FuncDecl:
			if node.Recv == nil && slices.Contains(selected.declarations, node.Name.Name) {
				first := node.Pos()
				if node.Doc != nil {
					first = node.Doc.Pos()
				}
				keep(first, node.End())
				functions = append(functions, node.Name.Name)
			}
		}
	}
	if !slices.Equal(imports, selected.imports) || !slices.Equal(functions, selected.declarations) {
		return fmt.Errorf("benchmark declarations or imports differ from the verified fixture")
	}
	return nil
}

func retainImports(block *ast.GenDecl, wanted []string, keep func(token.Pos, token.Pos)) []string {
	keep(block.Pos(), block.Lparen+1)
	keep(block.Rparen, block.End())
	var retained []string
	for _, spec := range block.Specs {
		item := spec.(*ast.ImportSpec)
		if item.Name == nil && slices.Contains(wanted, item.Path.Value) {
			keep(item.Pos(), item.End())
			retained = append(retained, item.Path.Value)
		}
	}
	return retained
}

func writeProjection(output, fixture string, files map[string]string, selected family, projected []byte) error {
	if err := os.Mkdir(output, 0700); err != nil {
		return err
	}
	files[fixture] = filepath.Join(output, selected.fixture)
	if err := os.WriteFile(files[fixture], projected, 0600); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(overlay{Replace: files}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "overlay.json"), append(encoded, '\n'), 0600); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"fixture": selected.fixture, "package": selected.packagePath, "originalSHA256": selected.originalSHA,
		"projectionSHA256": fmt.Sprintf("%x", sha256.Sum256(projected)), "declarations": selected.declarations,
		"omittedTestFiles": len(files) - 1,
	})
}
