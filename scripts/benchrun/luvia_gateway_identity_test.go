package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func verifyLuviaGatewayIdentityOverlay(t *testing.T, replacements map[string]string, pkg string) {
	t.Helper()
	ordinary, err := filepath.Glob(filepath.Join(pkg, "*_test.go"))
	if err != nil || len(ordinary) != len(replacements) {
		t.Fatalf("expected all %d test fixtures in overlay, got %d: %v", len(ordinary), len(replacements), err)
	}
	for _, path := range ordinary {
		replacement, present := replacements[path]
		if !present || (replacement != "") != (filepath.Base(path) == luviaGatewayIdentityFixture) {
			t.Fatalf("unexpected GatewayIdentity projection for %s: %q", path, replacement)
		}
	}
	data, err := os.ReadFile(replacements[filepath.Join(pkg, luviaGatewayIdentityFixture)])
	if err != nil {
		t.Fatal(err)
	}
	const native = "ae790127f4acd6df4fe2388d239b2657c999add2e742584284c411bef514e365"
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != native {
		t.Fatalf("projection SHA %s, want native SHA %s", got, native)
	}
	file, err := parseBenchmarkFixture(token.NewFileSet(), luviaGatewayIdentityFixture, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Decls) != 5 {
		t.Fatalf("isolated GatewayIdentity registrations have %d declarations", len(file.Decls))
	}
	for i, name := range []string{"testCfg", "testClaimsJSON", "testIdentity", benchlayout.LuviaGatewayIdentityBenchmark} {
		fn, okay := file.Decls[i+1].(*ast.FuncDecl)
		if !okay || fn.Name.Name != name {
			t.Fatalf("unexpected GatewayIdentity projection declaration %d", i+1)
		}
	}
}

func TestLuviaGatewayIdentityOverlayRejectsChangedFixture(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.LuviaPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeLuviaGatewayIdentityOverlay(pkg, workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing package returned %v", err)
	}
	name := filepath.Join(benchlayout.LuviaPackage, luviaGatewayIdentityFixture)
	if _, err := projectLuviaGatewayIdentityFixture(filepath.Join(root, name), workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing fixture returned %v", err)
	}
	original, err := os.ReadFile(filepath.Join("../..", name))
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(benchlayout.LuviaPackage, "unrelated_test.go")
	writeBenchmarkRunFixture(t, root, other, "package luvia\n")
	for _, tc := range []struct{ label, data, want string }{
		{"missing file", "", "missing Luvia gateway identity"},
		{"syntax", "package luvia\nfunc (\n", "expected"},
		{"wrong package", "package other\n", "unexpected package"},
		{"missing benchmark", strings.Replace(string(original), "func BenchmarkGatewayIdentity(", "func BenchmarkUnverified(", 1), "missing BenchmarkGatewayIdentity"},
		{"changed helper", strings.Replace(string(original), "test-secret", "new-test-secret", 1), "do not match"},
		{"duplicate helper", string(original) + "\nfunc testCfg() {}\n", "duplicate testCfg"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if tc.data != "" {
				writeBenchmarkRunFixture(t, root, name, tc.data)
			}
			if _, err := writeLuviaGatewayIdentityOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("GatewayIdentity overlay error = %v, want %q", err, tc.want)
			}
		})
	}
	writeBenchmarkRunFixture(t, root, name, string(original))
	writeBenchmarkRunFixture(t, root, other, "package luvia\nfunc TestMain() {}\n")
	if _, err := writeLuviaGatewayIdentityOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "cannot isolate test package setup") {
		t.Fatalf("TestMain in omitted fixture returned %v", err)
	}
}
