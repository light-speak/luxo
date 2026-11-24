package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

type fixtures struct {
	decls   map[string]bool
	imports map[string]bool
}

var decimalAlignment = fixtures{
	map[string]bool{"BenchmarkDecimalAlignment": true},
	map[string]bool{"testing": true, "github.com/shopspring/decimal": true},
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: lux-root-overlay <source package> <output directory>")
		os.Exit(2)
	}
	source, output := os.Args[1], os.Args[2]
	kept := map[string]fixtures{"condition_filter_bench_test.go": {}, "decimal_bench_test.go": decimalAlignment}
	entries, err := os.ReadDir(source)
	must(err)
	must(os.MkdirAll(output, 0o700))
	mapping := make(map[string]string)
	total := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		if !entry.Type().IsRegular() {
			panic(name)
		}
		file := filepath.Join(source, name)
		data, err := os.ReadFile(file)
		must(err)
		positions := token.NewFileSet()
		parsed, err := parser.ParseFile(positions, file, data, parser.SkipObjectResolution)
		must(err)
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && (fn.Name.Name == "init" || fn.Name.Name == "TestMain") {
				panic(fmt.Sprintf("unexpected top-level fixture initialization: %s %s", file, fn.Name.Name))
			}
		}
		mapping[file] = ""
		selected, ok := kept[name]
		if !ok {
			continue
		}
		total++
		projected := data
		if selected.decls != nil {
			projected = strip(data, positions.File(parsed.Pos()), parsed.Decls, selected)
		}
		out := filepath.Join(output, name)
		must(os.WriteFile(out, projected, 0o600))
		mapping[file] = out
	}
	if total != len(kept) {
		panic("missing retained fixture")
	}
	file, err := os.Create(filepath.Join(output, "overlay.json"))
	must(err)
	must(json.NewEncoder(file).Encode(struct{ Replace map[string]string }{mapping}))
	must(file.Close())
}

func strip(data []byte, file *token.File, decls []ast.Decl, selected fixtures) []byte {
	keepDecls, keepImports := 0, 0
	for _, decl := range decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && selected.decls[d.Name.Name] {
				keepDecls++
				continue
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				for _, spec := range d.Specs {
					spec := spec.(*ast.ImportSpec)
					name, err := strconv.Unquote(spec.Path.Value)
					must(err)
					if selected.imports[name] && (spec.Name == nil || spec.Name.Name == path.Base(name)) {
						keepImports++
						continue
					}
					mask(data, file, spec)
				}
				continue
			}
			if d.Tok == token.VAR && len(d.Specs) == 1 {
				v := d.Specs[0].(*ast.ValueSpec)
				if len(v.Names) == 1 && selected.decls[v.Names[0].Name] {
					keepDecls++
					continue
				}
			}
		}
		mask(data, file, decl)
	}
	if keepDecls != len(selected.decls) || keepImports != len(selected.imports) {
		panic("fixture declaration inventory changed")
	}
	return data
}

func mask(data []byte, file *token.File, node ast.Node) {
	for i := file.Offset(node.Pos()); i < file.Offset(node.End()); i++ {
		if data[i] != '\n' && data[i] != '\r' {
			data[i] = ' '
		}
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
