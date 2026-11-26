package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCodecReaderProjectionPreservesRetainedDeclarations(t *testing.T) {
	for _, name := range []string{"payload_bench_test.go", "payload_reference_test.go", "payload_test.go"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../..", "pkg/lux/codec", name))
			if err != nil {
				t.Fatal(err)
			}
			projected, err := projectCodecReader(name, bytes.Clone(data))
			if err != nil {
				t.Fatal(err)
			}
			if len(projected) != len(data) || bytes.Count(projected, []byte("\n")) != bytes.Count(data, []byte("\n")) {
				t.Fatalf("projected fixture changed source positions: %s", name)
			}
			verifyRetainedFixtureDeclarations(t, data, projected)
		})
	}
}

func verifyRetainedFixtureDeclarations(t *testing.T, original, projected []byte) {
	t.Helper()
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, "fixture.go", projected, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Decls) == 0 {
		t.Fatal("codec reader fixture has no declarations")
	}
	source := positions.File(file.Pos())
	for _, decl := range file.Decls {
		begin, end := source.Offset(decl.Pos()), source.Offset(decl.End())
		if !bytes.Equal(projected[begin:end], original[begin:end]) {
			t.Fatalf("codec reader fixture changed retained declaration at %s", source.Position(decl.Pos()))
		}
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			if fn.Name.Name == "BenchmarkMissingElementPayload" || fn.Name.Name == "TestElementPayloadPreflight" {
				t.Fatalf("codec reader also registers %s", fn.Name.Name)
			}
		}
	}
}

func TestCodecReaderRejectsTestPackageSetup(t *testing.T) {
	for _, name := range []string{"init", "TestMain"} {
		data := []byte("package codec\nfunc " + name + "() {}\n")
		if _, err := projectCodecReader("other_test.go", data); err == nil {
			t.Fatalf("codec performance overlay silently dropped %s", name)
		}
	}
}

func TestCodecReaderRejectsChangedFixtureBoundaries(t *testing.T) {
	for _, name := range []string{"payload_bench_test.go", "payload_test.go"} {
		if _, err := projectCodecReader(name, []byte("package codec\nfunc BenchmarkNewTestRoot() {}\n")); err == nil {
			t.Fatalf("codec readers accepted a changed %s structure", name)
		}
	}
}

func TestPerformanceCommandsRejectUnscopedCodecReader(t *testing.T) {
	for _, command := range []string{"compile-performance", "compile-performance-baseline", "run-performance"} {
		if _, _, err := performanceArguments(command, "pkg/lux/rpc", []string{"codec-reader"}); err == nil {
			t.Fatalf("%s accepted the codec reader profile for RPC", command)
		}
	}
}

func TestCodecReaderCompileProfileUsesGoTestRegistry(t *testing.T) {
	root := newBenchmarkRunTree(t)
	bin := filepath.Join(root, "bin")
	writeReaderProfileFixture(t, root)
	var out bytes.Buffer
	for _, profile := range []string{"default", "codec-reader"} {
		if err := run([]string{"compile-performance-baseline", root, bin, "pkg/lux/codec", profile}, nil, &out, &out); err != nil {
			t.Fatalf("compile %s: %v\n%s", profile, err, &out)
		}
		out.Reset()
		if err := run([]string{"run-performance", root, bin, "pkg/lux/codec", profile, "-test.list=.*"}, nil, &out, &out); err != nil {
			t.Fatalf("run %s: %v\n%s", profile, err, &out)
		}
		if profile == "codec-reader" && out.String() != "BenchmarkElementPayload\n" {
			t.Fatalf("reader registry = %s", &out)
		}
		if profile == "default" && (!strings.Contains(out.String(), "TestElementPayloadPreflight\n") || !strings.Contains(out.String(), "BenchmarkMissingElementPayload\n")) {
			t.Fatalf("ordinary registry = %s", &out)
		}
		out.Reset()
	}
	leftovers, err := filepath.Glob(filepath.Join(root, ".tmp", "bench-codec-reader-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("reader overlays remain: %v, %v", leftovers, err)
	}
}

func writeReaderProfileFixture(t *testing.T, root string) {
	t.Helper()
	writeBenchmarkRunFixture(t, root, "go.mod", "module github.com/light-speak/luxo\ngo 1.27.4\n")
	writeBenchmarkRunFixture(t, root, "go.work", "go 1.27.4\nuse .\n")
	for name, data := range map[string]string{
		"payload_bench_test.go": `package codec
import "testing"
func BenchmarkElementPayload(b *testing.B) { for b.Loop() { benchmarkPayloadRead() } }
func BenchmarkMissingElementPayload(b *testing.B) { for b.Loop() {} }
func benchmarkPayloadRead() { _ = payloadReaderCases() }
`,
		"payload_test.go": `package codec
import "testing"
type payloadReaderCase struct{}
func payloadReaderCases() payloadReaderCase { return payloadReaderCase{} }
func TestElementPayloadPreflight(t *testing.T) {}
`,
		"payload_reference_test.go": "package codec\nfunc referenceReader() {}\n",
	} {
		writeBenchmarkRunFixture(t, root, filepath.Join("pkg/lux/codec", name), data)
	}
}

func TestCodecReaderOverlayRejectsFilesystemMistakes(t *testing.T) {
	for _, stage := range []string{"root", "package", "symlink", "workspace", "missing fixture", "invalid fixture", "symlink file"} {
		t.Run(stage, func(t *testing.T) {
			root := newBenchmarkRunTree(t)
			root = breakReaderProfileLayout(t, root, stage)
			_, cleanup, err := codecReaderOverlay(root)
			if err == nil {
				_ = cleanup()
				t.Fatalf("codec accepted invalid %s", stage)
			}
			leftovers, err := filepath.Glob(filepath.Join(root, ".tmp", "bench-codec-reader-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("failed codec overlay left %v: %v", leftovers, err)
			}
		})
	}
}

func breakReaderProfileLayout(t *testing.T, root, stage string) string {
	t.Helper()
	pkgDir := filepath.Join(root, "pkg/lux/codec")
	switch stage {
	case "root":
		return filepath.Join(root, "missing")
	case "package":
		return root
	case "symlink":
		if err := os.MkdirAll(filepath.Dir(pkgDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(root, pkgDir); err != nil {
			t.Fatal(err)
		}
	case "workspace":
		writeReaderProfileFixture(t, root)
		if err := os.Remove(filepath.Join(root, ".tmp")); err != nil {
			t.Fatal(err)
		}
	case "missing fixture":
		writeBenchmarkRunFixture(t, root, "pkg/lux/codec/unrelated_test.go", "package codec\n")
	case "invalid fixture":
		writeBenchmarkRunFixture(t, root, "pkg/lux/codec/payload_bench_test.go", "package codec\nfunc {\n")
	case "symlink file":
		writeBenchmarkRunFixture(t, root, "pkg/lux/codec/test.go", "package codec\n")
		if err := os.Symlink(filepath.Join(pkgDir, "test.go"), filepath.Join(pkgDir, "unrelated_test.go")); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCodecReaderProjectionReportsFileErrors(t *testing.T) {
	root := newBenchmarkRunTree(t)
	workspace := filepath.Join(root, ".tmp")
	writeReaderProfileFixture(t, root)
	pkgDir := filepath.Join(root, "pkg/lux/codec")
	if _, err := writeCodecReaderOverlay(filepath.Join(root, "not-a-package"), workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing codec directory error = %v", err)
	}
	if _, err := projectCodecReaderFile(filepath.Join(pkgDir, "missing_test.go"), workspace, 0); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing fixture read error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "overlay.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeCodecReaderOverlay(pkgDir, workspace); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing overlay error = %v", err)
	}
}

func TestPerformanceCommandsRejectInvalidProfileArguments(t *testing.T) {
	root := newBenchmarkRunTree(t)
	bin := filepath.Join(root, "bin")
	for _, args := range [][]string{
		{"run-performance", root, bin, "pkg/lux/codec"},
		{"compile-performance", root, bin, "pkg/lux/codec", "codec-reader", "extra"},
	} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted invalid profile arguments %v", args)
		}
	}
	if err := os.WriteFile(bin, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeReaderProfileFixture(t, root)
	if err := run([]string{"compile-performance", root, bin, "pkg/lux/codec", "codec-reader"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted a regular file as the binary directory")
	}
}

func TestCodecReaderRejectsMovedFixtureDeclarations(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"payload_bench_test.go", "package codec\nfunc BenchmarkMissingElementPayload() {}\nfunc benchmarkPayloadRead() {}\n"},
		{"payload_test.go", "package codec\nimport \"testing\"\nvar payloadReaderCase int\nfunc payloadReaderCases() {}\nfunc TestElementPayloadPreflight(_ *testing.T) {}"},
	} {
		if _, err := projectCodecReader(tc.name, []byte(tc.data)); err == nil {
			t.Fatalf("accepted misplaced declaration in %s", tc.name)
		}
	}
}

func TestCompileIsolatedBenchmarkReportsCleanupFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("needs ordinary directory permissions")
	}
	for _, test := range []struct {
		name     string
		exitCode int
	}{{"compiler success", 0}, {"compiler failure", 7}} {
		t.Run(test.name, func(t *testing.T) {
			root := newBenchmarkRunTree(t)
			writeReaderProfileFixture(t, root)
			setupCodecReaderCleanupFailure(t, root, test.exitCode)
			args := []string{"compile-performance", root, filepath.Join(root, "bin"), "pkg/lux/codec", "codec-reader"}
			err := run(args, nil, io.Discard, io.Discard)
			if !errors.Is(err, os.ErrPermission) {
				t.Fatalf("expected isolated overlay cleanup permission error, got %v", err)
			}
			var compileErr *exec.ExitError
			if found := errors.As(err, &compileErr); found != (test.exitCode != 0) || found && compileErr.ExitCode() != test.exitCode {
				t.Fatalf("unexpected compiler status, got %v", err)
			}
		})
	}
}

func setupCodecReaderCleanupFailure(t *testing.T, root string, exitCode int) {
	t.Helper()
	compiler := filepath.Join(root, "tool", "go")
	writeBenchmarkRunFixture(t, root, "tool/go", `#!/bin/sh
set -eu
for argument do
    case "$argument" in
        -overlay=*) overlay=${argument#-overlay=} ;;
    esac
done
case "$overlay" in
    "$PWD"/.tmp/bench-codec-reader-*/overlay.json) ;;
    *) exit 5 ;;
esac
chmod 0500 "${overlay%/*}"
exit "$BENCHRUN_TEST_COMPILER_EXIT"
`)
	if err := os.Chmod(compiler, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(compiler)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BENCHRUN_TEST_COMPILER_EXIT", strconv.Itoa(exitCode))
	t.Cleanup(func() {
		workspaces, err := filepath.Glob(filepath.Join(root, ".tmp", "bench-codec-reader-*"))
		if err != nil {
			t.Error(err)
		}
		for _, workspace := range workspaces {
			if err := os.Chmod(workspace, 0o700); err != nil {
				t.Error(err)
			}
		}
	})
}
