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
	"strconv"
	"strings"
)

type fixture struct {
	hash, imports, declarations string
}

var projections = map[string]map[string]fixture{
	"FindRead": {
		"codegen_bench_test.go": {"bca2f8c336051736ebb635ab0217f9c865e8551cd2126c1af5e706ffee2243d7",
			"testing github.com/light-speak/luxo/pkg/ast", "BenchmarkFindRead"},
		"compile_test.go": {"4a5fdb53694d84132be177cf1abc478dd572eef99989eef32d52c13c19003584",
			"strings github.com/light-speak/luxo/pkg/ast", "newCompiler"},
		"handler_test.go": {"898db0c8bbe0b59c070468586ac6bd91ec040ed5a7af38b145739ab1cfbe3082",
			"github.com/light-speak/luxo/pkg/ast github.com/light-speak/luxo/pkg/token", "testModel testField directive"},
	},
	"NumericRangeValidation": {
		"numeric_range_bench_test.go": {"e5068c469e3d1a2dd2472dc048ed2a6b19f2cfbecb7a351677e34229a2bda666", "strings testing",
			"BenchmarkNumericRangeValidation"},
		"numeric_literal_bench_test.go": {"8f95bd3b1454ad4f190459b2a3184722ef7eef6717e53723ca8be5935e4ce05a", "",
			"numericLiteralSourceSink"},
		"compile_auth_test.go": {"aabfc4d4e76aa8a7e69721b62ce9c278f52b5f73404b958c77ffa6ba245a5758",
			"testing github.com/light-speak/luxo/pkg/ast github.com/light-speak/luxo/pkg/lexer github.com/light-speak/luxo/pkg/parser github.com/light-speak/luxo/pkg/semantic",
			"analyzePermissionFixture"},
	},
	"EventSnapshotContract": {
		"event_contract_test.go": {"ac3fd6cf95db8f4ac2a51333c78aafeb8d319ab892f0b778b5f1c6d50a066f74",
			"fmt testing github.com/light-speak/luxo/pkg/ast", "BenchmarkEventSnapshotContract"},
	},
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: project <source> <output> <FindRead|NumericRangeValidation|EventSnapshotContract>")
		os.Exit(1)
	}
	if err := run(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root, output, family string) error {
	fixtures, known := projections[family]
	if !known || !filepath.IsAbs(root) || !filepath.IsAbs(output) || output == root || strings.HasPrefix(output, root+string(filepath.Separator)) {
		return fmt.Errorf("invalid projection root, output or family")
	}
	pkg := "codegen"
	if family == "EventSnapshotContract" {
		pkg = "semantic"
	}
	dir := filepath.Join(root, "pkg", pkg)
	paths, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return err
	}
	replacements, hashes := make(map[string]string, len(paths)), make(map[string]string, len(fixtures))
	if err := os.Mkdir(output, 0o700); err != nil {
		return err
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("test fixture is not regular: %s", path)
		}
		name := filepath.Base(path)
		replacements[path] = ""
		selected, keep := fixtures[name]
		if !keep {
			continue
		}
		projected, err := project(path, pkg, selected)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		destination := filepath.Join(output, name)
		if err := os.WriteFile(destination, projected, 0o600); err != nil {
			return err
		}
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(projected))
		replacements[path] = destination
	}
	if len(hashes) != len(fixtures) {
		return fmt.Errorf("missing required benchmark fixture")
	}
	data, err := json.MarshalIndent(struct{ Replace map[string]string }{replacements}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "overlay.json"), data, 0o600); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Family    string
		Projected map[string]string
		Removed   int
	}{family, hashes, len(replacements) - len(hashes)})
}

func project(path, pkg string, selected fixture) ([]byte, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(source)) != selected.hash {
		return nil, fmt.Errorf("fixture differs from its verified source")
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil || file.Name.Name != pkg {
		return nil, fmt.Errorf("fixture does not have the expected package: %v", err)
	}
	out := bytes.Clone(source)
	for i, value := range out {
		if value != '\n' && value != '\r' {
			out[i] = ' '
		}
	}

	// Match the earlier native projector by retaining source offsets.
	offsets := positions.File(file.Package)
	keep := func(start, end token.Pos) {
		first, last := offsets.Offset(start), offsets.Offset(end)
		copy(out[first:last], source[first:last])
	}
	keep(file.Package, file.Name.End())
	if !retainImports(file, strings.Fields(selected.imports), keep) ||
		!retainDeclarations(file, strings.Fields(selected.declarations), keep) {
		return nil, fmt.Errorf("fixture imports or declarations changed")
	}
	return out, nil
}

func retainImports(file *ast.File, wanted []string, keep func(token.Pos, token.Pos)) bool {
	if len(wanted) == 0 {
		return true
	}
	var found []string
	for _, node := range file.Decls {
		block, ok := node.(*ast.GenDecl)
		if !ok || block.Tok != token.IMPORT {
			continue
		}
		if !block.Lparen.IsValid() {
			return false
		}
		keep(block.Pos(), block.Lparen+1)
		keep(block.Rparen, block.End())
		for _, spec := range block.Specs {
			item := spec.(*ast.ImportSpec)
			path, err := strconv.Unquote(item.Path.Value)
			if err == nil && item.Name == nil && slices.Contains(wanted, path) {
				keep(item.Pos(), item.End())
				found = append(found, path)
			}
		}
	}
	return slices.Equal(found, wanted)
}

func retainDeclarations(file *ast.File, wanted []string, keep func(token.Pos, token.Pos)) bool {
	var found []string
	for _, declaration := range file.Decls {
		var name string
		var doc *ast.CommentGroup
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			if node.Recv == nil {
				name, doc = node.Name.Name, node.Doc
			}
		case *ast.GenDecl:
			if node.Tok == token.VAR && len(node.Specs) == 1 {
				value := node.Specs[0].(*ast.ValueSpec)
				if len(value.Names) == 1 && len(value.Values) == 0 {
					name, doc = value.Names[0].Name, node.Doc
				}
			}
		}
		if slices.Contains(wanted, name) {
			first := declaration.Pos()
			if doc != nil {
				first = doc.Pos()
			}
			keep(first, declaration.End())
			found = append(found, name)
		}
	}
	return slices.Equal(found, wanted)
}
