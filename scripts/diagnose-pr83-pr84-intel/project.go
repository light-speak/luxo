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

type fixtureSpec struct {
	Name      string
	SHA256    string
	Imports   []string
	Functions []string
}

type manifest struct {
	Projections map[string]struct{ Fixtures []fixtureSpec }
}

type fixtureHash struct {
	Source, Compiled string
}

type projection struct {
	Profile  string
	Fixtures map[string]fixtureHash
	Removed  int
	Overlay  string
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: project SOURCE MANIFEST PROFILE OUTPUT")
		os.Exit(1)
	}
	if err := project(os.Args[1], os.Args[2], os.Args[3], os.Args[4]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func project(source, manifestPath, profile, output string) error {
	if !filepath.IsAbs(source) || !filepath.IsAbs(output) || output == source || strings.HasPrefix(output, source+string(filepath.Separator)) {
		return fmt.Errorf("projection output must be outside the source checkout")
	}
	cfg, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	group := cfg.Projections[profile]
	if len(group.Fixtures) == 0 {
		return fmt.Errorf("unverified fixture projection %q", profile)
	}
	dir := filepath.Join(source, "pkg", "lux", "api")
	removed, err := testFiles(dir)
	if err != nil {
		return err
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		return err
	}
	hashes := make(map[string]fixtureHash, len(group.Fixtures))
	for _, fixture := range group.Fixtures {
		name := filepath.Join(dir, fixture.Name)
		if _, present := removed[name]; !present {
			return fmt.Errorf("missing or duplicated retained fixture: %s", fixture.Name)
		}
		if err := writeFixture(name, output, fixture, removed, hashes); err != nil {
			return err
		}
	}
	overlay := filepath.Join(output, "overlay.json")
	body, err := json.MarshalIndent(struct{ Replace map[string]string }{removed}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(overlay, body, 0o600); err != nil {
		return err
	}
	excluded := 0
	for _, replacement := range removed {
		if replacement == "" {
			excluded++
		}
	}
	return json.NewEncoder(os.Stdout).Encode(projection{profile, hashes, excluded, overlay})
}

func readManifest(path string) (manifest, error) {
	var cfg manifest
	body, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func testFiles(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("API test fixture is not regular: %s", entry.Name())
		}
		result[filepath.Join(dir, entry.Name())] = ""
	}
	return result, nil
}

func writeFixture(path, output string, fixture fixtureSpec, replacements map[string]string, hashes map[string]fixtureHash) error {
	input, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	original := fmt.Sprintf("%x", sha256.Sum256(input))
	if original != fixture.SHA256 {
		return fmt.Errorf("verified fixture source changed: %s", fixture.Name)
	}
	delete(replacements, path)
	compiled := original
	if len(fixture.Functions) != 0 {
		retained, err := keepFunctions(path, input, fixture)
		if err != nil {
			return err
		}
		destination := filepath.Join(output, fixture.Name)
		if err := os.WriteFile(destination, retained, 0o600); err != nil {
			return err
		}
		compiled = fmt.Sprintf("%x", sha256.Sum256(retained))
		replacements[path] = destination
	}
	hashes[fixture.Name] = fixtureHash{original, compiled}
	return nil
}

func keepFunctions(path string, input []byte, fixture fixtureSpec) ([]byte, error) {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, input, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil || file.Name.Name != "api" {
		return nil, fmt.Errorf("invalid API fixture %s: %v", fixture.Name, err)
	}
	out := bytes.Clone(input)
	for i, value := range out {
		if value != '\n' && value != '\r' {
			out[i] = ' '
		}
	}
	offsets := positions.File(file.Package)
	keep := func(first, last token.Pos) {
		start, end := offsets.Offset(first), offsets.Offset(last)
		copy(out[start:end], input[start:end])
	}
	keep(file.Package, file.Name.End())
	if !retainImports(file, fixture.Imports, keep) || !retainFunctions(file, fixture.Functions, keep) {
		return nil, fmt.Errorf("fixture imports or helper declarations changed: %s", fixture.Name)
	}
	return out, nil
}

func retainImports(file *ast.File, wanted []string, keep func(token.Pos, token.Pos)) bool {
	var found []string
	for _, decl := range file.Decls {
		block, ok := decl.(*ast.GenDecl)
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
			name, err := strconv.Unquote(item.Path.Value)
			if err == nil && item.Name == nil && slices.Contains(wanted, name) {
				keep(item.Pos(), item.End())
				found = append(found, name)
			}
		}
	}
	return slices.Equal(found, wanted)
}

func retainFunctions(file *ast.File, wanted []string, keep func(token.Pos, token.Pos)) bool {
	var found []string
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !slices.Contains(wanted, fn.Name.Name) {
			continue
		}
		first := fn.Pos()
		if fn.Doc != nil {
			first = fn.Doc.Pos()
		}
		keep(first, fn.End())
		found = append(found, fn.Name.Name)
	}
	return slices.Equal(found, wanted)
}
