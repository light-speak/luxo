package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCanonicalAndInvalidCommand(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"canonical"}, strings.NewReader("pkg/lux/cache/redis\npkg/lux/cache\npkg/parser\n"), &out, io.Discard); err != nil || out.String() != "pkg/lux/cache\npkg/parser\n" {
		t.Fatalf("%q: %v", out.String(), err)
	}
	out.Reset()
	if err := run([]string{"performance-profile"}, nil, &out, io.Discard); err != nil || out.String() != performanceProfile+"\n" {
		t.Fatalf("performance profile = %q, %v", out.String(), err)
	}
	if err := run(nil, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("missing command accepted")
	}
}

func TestCompileAndRunBothPartsOfSplit(t *testing.T) {
	root := newBenchmarkRunTree(t)
	files := map[string]string{
		"go.mod":                            "module github.com/light-speak/luxo\ngo 1.27.1\n",
		"go.work":                           "go 1.27.1\nuse (\n.\n./pkg/lux/cache/redis\n)\n",
		"pkg/lux/cache/cache_test.go":       "package cache\nimport \"testing\"\nfunc BenchmarkMemory(b *testing.B) { for b.Loop() {} }\n",
		"pkg/lux/cache/redis/go.mod":        "module github.com/light-speak/luxo/pkg/lux/cache/redis\ngo 1.27.1\n",
		"pkg/lux/cache/redis/redis_test.go": "package redis\nimport \"testing\"\nfunc BenchmarkRedis(b *testing.B) { for b.Loop() {} }\n",
	}
	for name, content := range files {
		writeBenchmarkRunFixture(t, root, name, content)
	}
	var out bytes.Buffer
	args := []string{"compile", root, filepath.Join(root, "bin"), "pkg/lux/cache"}
	for _, command := range []string{"compile", "compile-performance"} {
		args[0] = command
		if err := run(args, nil, &out, &out); err != nil {
			t.Fatalf("%s: %v: %s", command, err, out.String())
		}
	}
	args[0] = "run"
	args = append(args, "-test.run=^$", "-test.bench=.", "-test.benchtime=1x")
	if err := run(args, nil, &out, &out); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	for _, want := range []string{"BenchmarkMemory", "BenchmarkRedis", "pkg: github.com/light-speak/luxo/pkg/lux/cache/redis"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
}

func TestPerformanceOverlayBindsOnlyReviewedRPCFile(t *testing.T) {
	root := newBenchmarkRunTree(t)
	name := rpcBoundaryPath
	data, err := os.ReadFile(filepath.Join("../..", name))
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(original), 0o755); err != nil {
		t.Fatal(err)
	}
	if path, cleanup, err := performanceOverlay(root, "pkg/lux/luvia", false); err != nil || path != "" {
		t.Fatalf("unrelated package got an overlay: %q, %v", path, err)
	} else if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := performanceOverlay(root, "pkg/lux/rpc", false); err == nil {
		t.Fatal("missing reviewed fixture was accepted")
	}
	if err := os.WriteFile(original, append([]byte("// unexpected header\n"), data...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := performanceOverlay(root, "pkg/lux/rpc", false); err == nil {
		t.Fatal("changed fixture was accepted")
	}
	if err := os.WriteFile(original, data, 0o600); err != nil {
		t.Fatal(err)
	}
	overlay, cleanup, err := performanceOverlay(root, "pkg/lux/rpc", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Error(err)
		}
	})
	raw, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("{\"Replace\":{%q:\"\"}}\n", original)
	if string(raw) != want || filepath.Dir(overlay) != filepath.Join(root, ".tmp") {
		t.Fatalf("unexpected overlay %s: %s", overlay, raw)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(overlay); !os.IsNotExist(err) {
		t.Fatalf("overlay remained after compilation: %v", err)
	}
	if after, err := os.ReadFile(original); err != nil || !bytes.Equal(after, data) {
		t.Fatalf("ordinary fixture changed: %v", err)
	}
}

func TestCompilePerformanceKeepsBenchmarksAndSeparatesCorrectness(t *testing.T) {
	root := newBenchmarkRunTree(t)
	files := map[string]string{
		"go.mod":                        "module example.com/rpc-fixture\ngo 1.27.1\n",
		"go.work":                       "go 1.27.1\nuse .\n",
		rpcBoundaryPath:                 isolatedRPCFixture,
		"pkg/lux/rpc/benchmark_test.go": "package rpc\nimport \"testing\"\nfunc BenchmarkKept(b *testing.B) { for b.Loop() {} }\n",
	}
	for path, content := range files {
		writeBenchmarkRunFixture(t, root, path, content)
	}
	var out bytes.Buffer
	for _, command := range []string{"compile", "compile-performance", "compile-performance-baseline"} {
		out.Reset()
		bin := filepath.Join(root, "bin", command)
		if err := run([]string{command, root, bin, "pkg/lux/rpc"}, nil, &out, &out); err != nil {
			t.Fatalf("%s: %v\n%s", command, err, out.String())
		}
		if err := run([]string{"run", root, bin, "pkg/lux/rpc", "-test.list=."}, nil, &out, &out); err != nil {
			t.Fatalf("list %s: %v\n%s", command, err, out.String())
		}
		for _, name := range []string{"TestSchemaFreeRouterStreamBoundary", "TestInternalDispatchCannotBeForgedAcrossPublicTransports"} {
			if present := strings.Contains(out.String(), name); present != (command == "compile") {
				t.Fatalf("%s listed %s = %v\n%s", command, name, present, out.String())
			}
		}
		if !strings.Contains(out.String(), "BenchmarkKept\n") {
			t.Fatalf("%s lost its benchmark\n%s", command, out.String())
		}
	}

	writeBenchmarkRunFixture(t, root, "pkg/lux/rpc/broken.go", "package rpc\nfunc broken(\n")
	out.Reset()
	bad := []string{"compile-performance", root, filepath.Join(root, "broken"), "pkg/lux/rpc"}
	if err := run(bad, nil, &out, &out); err == nil || !strings.Contains(out.String(), "broken.go") {
		t.Fatalf("compiler error was lost: %v\n%s", err, out.String())
	}

	invalid := strings.TrimPrefix(isolatedRPCFixture, "// luxo:benchmark-isolated-correctness\n")
	writeBenchmarkRunFixture(t, root, rpcBoundaryPath, invalid)
	for _, command := range []string{"compile-performance", "compile-performance-baseline"} {
		bin := filepath.Join(root, "invalid", command)
		if err := run([]string{command, root, bin, "pkg/lux/rpc"}, nil, io.Discard, io.Discard); err == nil ||
			!strings.Contains(err.Error(), "missing marker or unrecognized historical baseline") {
			t.Fatalf("%s invalid fixture returned %v", command, err)
		}
		if _, err := os.Stat(filepath.Join(bin, "pkg-lux-rpc.test")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s compiled an invalid fixture: %v", command, err)
		}
	}
	overlays, err := filepath.Glob(filepath.Join(root, ".tmp", "bench-rpc-overlay-*.json"))
	if err != nil || len(overlays) != 0 {
		t.Fatalf("compilation left overlays %v, %v", overlays, err)
	}
}

func TestPerformanceOverlayRejectsInvalidPaths(t *testing.T) {
	root := newBenchmarkRunTree(t)
	if _, _, err := performanceOverlay(filepath.Join(root, "missing-root"), "pkg/lux/rpc", false); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing root returned %v", err)
	}
	path := filepath.Join(root, rpcBoundaryPath)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := performanceOverlay(root, "pkg/lux/rpc", false); err == nil ||
		!strings.Contains(err.Error(), "must be regular") {
		t.Fatalf("directory fixture returned %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeBenchmarkRunFixture(t, root, rpcBoundaryPath, isolatedRPCFixture)
	t.Run("symlinked ancestor", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating symlinks requires privileges on Windows")
		}
		alias := filepath.Join(root, "alias")
		if err := os.Mkdir(alias, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "pkg"), filepath.Join(alias, "pkg")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := performanceOverlay(alias, "pkg/lux/rpc", false); err == nil ||
			!strings.Contains(err.Error(), "must stay at") {
			t.Fatalf("symlinked ancestor returned %v", err)
		}
	})
	t.Run("unreadable fixture", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows chmod does not remove read access")
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0600); err != nil {
				t.Error(err)
			}
		})
		if _, err := os.ReadFile(path); err == nil {
			t.Skip("current account can read mode-zero files")
		} else if !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		if _, _, err := performanceOverlay(root, "pkg/lux/rpc", false); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("unreadable fixture returned %v", err)
		}
	})
	if err := os.Remove(filepath.Join(root, ".tmp")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := performanceOverlay(root, "pkg/lux/rpc", false); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing overlay directory returned %v", err)
	}
}

func TestPerformanceOverlayReportsFixtureResolutionFailure(t *testing.T) {
	root := newBenchmarkRunTree(t)
	writeBenchmarkRunFixture(t, root, rpcBoundaryPath, isolatedRPCFixture)
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(physicalRoot, rpcBoundaryPath)
	denied := errors.New("resolve fixture failed")
	called := false
	resolve := func(path string) (string, error) {
		if path != fixture {
			t.Fatalf("resolved %q, want %q", path, fixture)
		}
		called = true
		return "", denied
	}
	path, cleanup, err := performanceOverlayWithPathResolver(root, "pkg/lux/rpc", false, resolve)
	if path != "" || cleanup != nil || !called || !errors.Is(err, denied) {
		t.Fatalf("failed resolution produced an overlay or lost its error: path=%q called=%v err=%v", path, called, err)
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".tmp")); err != nil || len(entries) != 0 {
		t.Fatalf("failed resolution left overlay entries %v, %v", entries, err)
	}
}

func newBenchmarkRunTree(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("../../.tmp", "bench-rpc-compile-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".tmp"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeBenchmarkRunFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

const isolatedRPCFixture = "// luxo:benchmark-isolated-correctness\n\npackage rpc\n\n" +
	"import \"testing\"\n\n" +
	"func TestInternalDispatchCannotBeForgedAcrossPublicTransports(t *testing.T) {}\n" +
	"func TestSchemaFreeRouterStreamBoundary(t *testing.T) {}\n"

func TestHistoricalRPCFixtureRequiresTheRegisteredDigest(t *testing.T) {
	source := []byte(strings.TrimPrefix(isolatedRPCFixture, rpcBoundaryMarker+"\n\n"))
	digest := fmt.Sprintf("%x", sha256.Sum256(source))

	if err := validateRPCFixture(rpcBoundaryPath, source, digest); err != nil {
		t.Fatalf("historical fixture with the registered digest: %v", err)
	}
	if err := validateRPCFixture(rpcBoundaryPath, source, ""); err == nil {
		t.Fatal("current compilation accepted a fixture without the marker")
	}
	if err := validateRPCFixture(rpcBoundaryPath, append(source, '\n'), digest); err == nil {
		t.Fatal("historical compilation accepted a fixture with a different digest")
	}
}

func TestModernRPCFixturePreservesBenchmarkBoundary(t *testing.T) {
	const source = isolatedRPCFixture
	for _, tc := range []struct {
		name, source string
		accept       bool
	}{
		{"ordinary edits", source + "\nfunc helper() string { return \"//go:build allowed in a string\" }\n", true},
		{"CRLF checkout", strings.ReplaceAll(source, "\n", "\r\n"), true},
		{"missing marker", strings.TrimPrefix(source, "// luxo:benchmark-isolated-correctness\n"), false},
		{"wrong package", strings.Replace(source, "package rpc", "package rpc_test", 1), false},
		{"missing correctness root", strings.Replace(source, "TestSchemaFreeRouterStreamBoundary", "TestOther", 1), false},
		{"initializer", source + "\nfunc init() {}\n", false},
		{"test main", source + "\nfunc TestMain(m *testing.M) {}\n", false},
		{"benchmark", source + "\nfunc BenchmarkNew(b *testing.B) {}\n", false},
		{"method", source + "\nfunc (*Server) OrdinaryHelper() {}\n", false},
		{"global var", source + "\nvar setup = helper()\n", false},
		{"constant", source + "\nconst fixtureSize = 2\n", false},
		{"added import", strings.Replace(source, "\"testing\"", "\"expvar\"", 1), false},
		{"side-effect import", strings.Replace(source, "\"testing\"", "_ \"testing\"", 1), false},
		{"dot import", strings.Replace(source, "\"testing\"", ". \"testing\"", 1), false},
		{"build directive", strings.Replace(source, "\n\npackage", "\n//go:build custom\n\npackage", 1), false},
		{"legacy build directive", strings.Replace(source, "\n\npackage", "\n// +build custom\n\npackage", 1), false},
		{"compiler directive", source + "\n//go:linkname helper hidden\nfunc helper()\n", false},
		{"line directive", source + "\n//line fixture.go:1\nfunc helper() {}\n", false},
		{"bodyless function", source + "\nfunc helper()\n", false},
		{"malformed", source + "\nfunc helper(\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateRPCFixture(rpcBoundaryPath, []byte(tc.source), ""); (err == nil) != tc.accept {
				t.Fatalf("accepted=%v want %v: %v", err == nil, tc.accept, err)
			}
		})
	}
	if err := validateRPCFixtureDeclarations([]ast.Decl{&ast.BadDecl{}}); err == nil {
		t.Fatal("unknown AST declaration was accepted")
	}
}
