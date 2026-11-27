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
)

type original struct {
	Role         string
	SourceSHA256 string
	Spans        [][2]int
}

type fixture struct {
	OriginalInput    []original
	ProjectionSHA256 string
	Functions        []string
	Imports          []string
}

func must(err error) {
	if err != nil {
		panic(err.Error())
	}
}

func fileSHA(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// Parse package-level declarations so fixture source strings cannot masquerade as TestMain.
func readTest(path string) ([]byte, *token.FileSet, *ast.File) {
	info, err := os.Lstat(path)
	must(err)
	if !info.Mode().IsRegular() {
		panic(fmt.Sprintf("unexpected test file: %s", path))
	}
	input, err := os.ReadFile(path)
	must(err)
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, input, parser.SkipObjectResolution)
	must(err)
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "TestMain" {
			panic(fmt.Sprintf("package TestMain affects the stock benchmark: %s", path))
		}
	}
	return input, positions, file
}

func project(input []byte, positions *token.FileSet, file *ast.File, spec fixture, role string) []byte {
	index := slices.IndexFunc(spec.OriginalInput, func(arm original) bool { return arm.Role == role })
	if index < 0 || spec.OriginalInput[index].SourceSHA256 != fileSHA(input) {
		panic("fixture input does not match the signed source")
	}
	source := spec.OriginalInput[index]
	var out bytes.Buffer
	fmt.Fprintf(&out, "package %s\n\nimport (\n", file.Name.Name)
	for _, imported := range spec.Imports {
		fmt.Fprintf(&out, "\t%q\n", imported)
	}
	out.WriteString(")\n\n")
	appendFunctions(&out, input, positions, file, spec.Functions, source.Spans)
	if fileSHA(out.Bytes()) != spec.ProjectionSHA256 {
		panic("fixture output does not match the signed projection")
	}
	return out.Bytes()
}

func appendFunctions(out *bytes.Buffer, input []byte, positions *token.FileSet, file *ast.File, names []string, spans [][2]int) {
	offsets := positions.File(file.Package)
	index := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !slices.Contains(names, fn.Name.Name) {
			continue
		}
		lines := [2]int{positions.Position(fn.Pos()).Line, positions.Position(fn.End()).Line}
		if index >= len(names) || index >= len(spans) || fn.Name.Name != names[index] || lines != spans[index] {
			panic("signed declaration order or span changed")
		}
		if index > 0 {
			out.WriteByte('\n')
		}
		out.Write(input[offsets.Offset(fn.Pos()):offsets.Offset(fn.End())])
		out.WriteByte('\n')
		index++
	}
	if index == 0 || index != len(names) || index != len(spans) {
		panic("missing signed fixture declarations")
	}
}

func overlay(source, target, role string, files map[string]fixture) map[string]string {
	paths, err := filepath.Glob(filepath.Join(source, "*_test.go"))
	must(err)
	must(os.Mkdir(target, 0700))
	replacements := make(map[string]string, len(paths))
	matched := 0
	for _, path := range paths {
		input, positions, file := readTest(path)
		name := filepath.Base(path)
		spec, present := files[name]
		replacements[path] = ""
		if present {
			output := filepath.Join(target, name)
			must(os.WriteFile(output, project(input, positions, file, spec, role), 0600))
			replacements[path] = output
			matched++
		}
	}
	if matched != len(files) {
		panic("incomplete signed fixture inventory")
	}
	return replacements
}

func main() {
	if len(os.Args) != 6 {
		panic("usage: project manifest source package arm output")
	}
	var config struct {
		Packages map[string]struct {
			Files map[string]fixture
		}
	}
	data, err := os.ReadFile(os.Args[1])
	must(err)
	must(json.Unmarshal(data, &config))
	pkg, present := config.Packages[os.Args[3]]
	if !present {
		panic("unexpected projection package")
	}
	source, err := filepath.Abs(filepath.Join(os.Args[2], os.Args[3]))
	must(err)
	target, err := filepath.Abs(os.Args[5])
	must(err)
	replacements := overlay(source, target, os.Args[4], pkg.Files)
	data, err = json.MarshalIndent(struct{ Replace map[string]string }{replacements}, "", "  ")
	must(err)
	must(os.WriteFile(filepath.Join(target, "overlay.json"), data, 0600))
}
