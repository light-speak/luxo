package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func TestCodegenLiteralOverlayPreservesFixtureBodyAndPositions(t *testing.T) {
	root, err := filepath.EvalSymlinks("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, baseline := range []bool{false, true} {
		file, cleanup, err := compileOverlay(root, benchlayout.CodegenPackage, baseline, benchlayout.CodegenLiteralProfile)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := cleanup(); err != nil {
				t.Error(err)
			}
		})
		verifyCodegenOverlay(t, file, filepath.Join(root, benchlayout.CodegenPackage))
	}
}

func verifyCodegenOverlay(t *testing.T, path, dir string) {
	t.Helper()
	var overlay struct{ Replace map[string]string }
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatal(err)
	}
	ordinary, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil || len(ordinary) != len(overlay.Replace) {
		t.Fatalf("expected all %d test files in overlay, got %d: %v", len(ordinary), len(overlay.Replace), err)
	}
	for _, file := range ordinary {
		replacement, exists := overlay.Replace[file]
		name := filepath.Base(file)
		fixture, retained := codegenLiteralFixtures[name]
		if !exists || (replacement != "") != retained {
			t.Fatalf("unexpected projection for %q", name)
		}
		if retained {
			verifyCodegenProjection(t, file, replacement, fixture)
		}
	}
}

func verifyCodegenProjection(t *testing.T, source, replacement string, fixture codegenLiteralFixture) {
	t.Helper()
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(replacement)
	if err != nil {
		t.Fatal(err)
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, replacement, after, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) || len(file.Imports) != len(fixture.imports) {
		t.Fatalf("projected %s changed fixture positions or imports", source)
	}
	kept := 0
	for _, declaration := range file.Decls {
		if isImport(declaration) {
			continue
		}
		if !isFunction(declaration, fixture.function) && !(fixture.sink && isNumericLiteralSink(declaration)) {
			t.Fatalf("unexpected declaration in %s: %T", source, declaration)
		}
		assertCodegenSourceUnchanged(t, positions, declaration, before, after)
		kept++
	}
	if (fixture.sink && kept != 2) || (!fixture.sink && kept != 1) {
		t.Fatalf("unexpected declaration count in %s: %d", source, kept)
	}
	for _, spec := range file.Imports {
		assertCodegenSourceUnchanged(t, positions, spec, before, after)
	}
	t.Logf("%x  %s", sha256.Sum256(after), filepath.Base(source))
}

func assertCodegenSourceUnchanged(t *testing.T, positions *token.FileSet, node ast.Node, before, after []byte) {
	t.Helper()
	file := positions.File(node.Pos())
	first, last := file.Offset(node.Pos()), file.Offset(node.End())
	if !bytes.Equal(before[first:last], after[first:last]) {
		t.Fatalf("changed retained bytes at %d:%d in %s", first, last, file.Name())
	}
}

func TestCodegenLiteralOverlayRejectsChangedFixtureClosure(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.CodegenPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeCodegenLiteralOverlay(pkg, workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing package directory returned %v", err)
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.CodegenPackage, "unrelated_test.go"), "package codegen\n")
	if _, err := writeCodegenLiteralOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "missing codegen literal benchmark fixture") {
		t.Fatalf("missing selected fixture returned %v", err)
	}
	path := filepath.Join(benchlayout.CodegenPackage, "compile_test.go")
	if _, err := projectCodegenLiteralFixture(filepath.Join(root, path), workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing fixture path returned %v", err)
	}
	writeBenchmarkRunFixture(t, root, path, "package another\nfunc newCompiler() {}\n")
	if _, err := writeCodegenLiteralOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "unexpected package") {
		t.Fatalf("unexpected package returned %v", err)
	}
	writeBenchmarkRunFixture(t, root, path, "package codegen\nfunc newCompiler() {}\n")
	if _, err := writeCodegenLiteralOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "fixture declarations or imports changed") {
		t.Fatalf("missing selected imports returned %v", err)
	}
	writeBenchmarkRunFixture(t, root, path, "package codegen\nfunc TestMain() {}\n")
	if _, err := writeCodegenLiteralOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "cannot isolate test package setup") {
		t.Fatalf("unexpected test setup returned %v", err)
	}
}

func TestCodegenLiteralProjectionDropsUnrelatedSingleImportAndType(t *testing.T) {
	root := newBenchmarkRunTree(t)
	workspace := filepath.Join(root, ".tmp")
	path := filepath.Join(benchlayout.CodegenPackage, "numeric_literal_bench_test.go")
	const fixture = `package codegen

import "time"
import (
	"testing"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

type unrelatedTestTimer time.Duration
var numericLiteralSourceSink string

func BenchmarkCompileDecimalIntegerLiterals(b *testing.B) {
	_ = &ast.Literal{Kind: token.Int}
}
`
	writeBenchmarkRunFixture(t, root, path, fixture)
	source := filepath.Join(root, path)
	projected, err := projectCodegenLiteralFixture(source, workspace)
	if err != nil {
		t.Fatal(err)
	}
	verifyCodegenProjection(t, source, projected, codegenLiteralFixtures[filepath.Base(source)])
}
