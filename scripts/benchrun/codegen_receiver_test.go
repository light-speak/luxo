package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func TestCodegenReceiverOverlayMatchesNativeProjection(t *testing.T) {
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
	pkg := filepath.Join(root, benchlayout.CodegenPackage)
	actualFiles, err := filepath.Glob(filepath.Join(pkg, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, baseline := range []bool{true, false} {
		file, cleanup, err := compileOverlay(root, benchlayout.CodegenPackage, baseline, benchlayout.CodegenReceiverProfile)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := cleanup(); err != nil {
				t.Error(err)
			}
		})
		verifyReceiverOverlay(t, file, actualFiles)
	}
}

func verifyReceiverOverlay(t *testing.T, file string, actualFiles []string) {
	t.Helper()
	var overlay struct{ Replace map[string]string }
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatal(err)
	}
	if len(overlay.Replace) != len(actualFiles) {
		t.Fatalf("got %d replacements for %d files", len(overlay.Replace), len(actualFiles))
	}
	for _, path := range actualFiles {
		replacement, okay := overlay.Replace[path]
		if !okay || (replacement != "") != (filepath.Base(path) == codegenReceiverFixture) {
			t.Fatalf("unexpected receiver overlay for %q: %q", path, replacement)
		}
		if replacement != "" {
			checkReceiverProjection(t, replacement)
		}
	}
}

func checkReceiverProjection(t *testing.T, file string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	const verified = "330fc5c6785cae1fd2e89810a1ec945e9384669a9efaa3af47cc5e3a2bf74da3"
	if actual := fmt.Sprintf("%x", sha256.Sum256(data)); actual != verified {
		t.Fatalf("native receiver fixture digest = %s, want %s", actual, verified)
	}
	positions := token.NewFileSet()
	source, err := parser.ParseFile(positions, file, data, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Decls) != 2 || !isImport(source.Decls[0]) || !isFunction(source.Decls[1], benchlayout.CodegenReceiverBenchmark) ||
		positions.Position(source.Decls[1].Pos()).Line != 46 {
		t.Fatal("projection must retain exactly the receiver root at its original line")
	}
}

func TestCodegenReceiverOverlayRejectsChangedFixture(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.CodegenPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeCodegenReceiverOverlay(pkg, workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory returned %v", err)
	}
	name := filepath.Join(benchlayout.CodegenPackage, codegenReceiverFixture)
	if _, err := projectCodegenReceiverFixture(filepath.Join(root, name), workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing fixture returned %v", err)
	}
	original, err := os.ReadFile(filepath.Join("../..", name))
	if err != nil {
		t.Fatal(err)
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.CodegenPackage, "irrelevant_test.go"), "package codegen\n")
	for _, tc := range []struct{ label, data, want string }{
		{"missing root file", "", "missing codegen receiver"},
		{"parse failure", "package codegen\nfunc (\n", "expected"},
		{"wrong package", "package other\n", "unexpected package"},
		{"missing declaration", "package codegen\n", "missing BenchmarkGlobalReceiverName"},
		{"moved declaration", "\n" + string(original), "line 46"},
		{"different body", strings.Replace(string(original), "empty receiver", "unexpected receiver", 1), "does not match"},
		{"trailing declaration", string(original) + "\nfunc TestMore() {}\n", "does not match"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if tc.data != "" {
				writeBenchmarkRunFixture(t, root, name, tc.data)
			}
			if _, err := writeCodegenReceiverOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("receiver overlay error = %v, want %q", err, tc.want)
			}
		})
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.CodegenPackage, "irrelevant_test.go"), "package codegen\nfunc init() {}\n")
	writeBenchmarkRunFixture(t, root, name, string(original))
	if _, err := writeCodegenReceiverOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "cannot isolate test package setup") {
		t.Fatalf("init in omitted fixture returned %v", err)
	}
}
