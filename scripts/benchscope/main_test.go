package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSelectAffectedPackages(t *testing.T) {
	tests := []struct {
		name       string
		changed    []string
		benchmarks []string
		base       map[string][]string
		head       map[string][]string
		want       []string
	}{
		{
			name:       "direct production change",
			changed:    []string{"pkg/lux/codec/codec.go"},
			benchmarks: []string{"pkg/lux/codec", "pkg/parser"},
			base: map[string][]string{
				"pkg/lux/codec": {"pkg/lux/codec/codec.go"},
				"pkg/parser":    {"pkg/parser/parser.go"},
			},
			want: []string{"pkg/lux/codec"},
		},
		{
			name:       "transitive dependency change",
			changed:    []string{"pkg/token/token.go"},
			benchmarks: []string{"pkg/lexer", "pkg/lux/codec"},
			base: map[string][]string{
				"pkg/lexer":     {"pkg/lexer/lexer.go", "pkg/token/token.go"},
				"pkg/lux/codec": {"pkg/lux/codec/codec.go"},
			},
			want: []string{"pkg/lexer"},
		},
		{
			name:       "added and removed production inputs",
			changed:    []string{"pkg/codegen/removed.go", "pkg/semantic/added.go"},
			benchmarks: []string{"pkg/codegen", "pkg/semantic"},
			base: map[string][]string{
				"pkg/codegen":  {"pkg/codegen/removed.go"},
				"pkg/semantic": {"pkg/semantic/analyzer.go"},
			},
			head: map[string][]string{
				"pkg/codegen":  {"pkg/codegen/generator.go"},
				"pkg/semantic": {"pkg/semantic/analyzer.go", "pkg/semantic/added.go"},
			},
			want: []string{"pkg/codegen", "pkg/semantic"},
		},
		{
			name:       "test-only files do not affect production scope",
			changed:    []string{"pkg/lux/codec/protocol_conformance_test.go", "pkg/lux/codec/testdata/protocol-v1.json"},
			benchmarks: []string{"pkg/lux/codec"},
			base:       map[string][]string{"pkg/lux/codec": {"pkg/lux/codec/codec.go"}},
			want:       nil,
		},
		{
			name:       "module dependency change runs every benchmark",
			changed:    []string{"go.mod"},
			benchmarks: []string{"pkg/parser", "pkg/lux/codec"},
			want:       []string{"pkg/lux/codec", "pkg/parser"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lister := fakeDependencyLister(test.base, test.head, nil)
			got, err := selectAffectedPackages("base", "head", test.changed, test.benchmarks, lister)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("affected packages = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSelectAffectedPackagesRejectsDependencyListingFailure(t *testing.T) {
	tests := []struct {
		name     string
		failRoot string
		wantRoot string
	}{
		{name: "base", failRoot: "base", wantRoot: "base revision"},
		{name: "head", failRoot: "head", wantRoot: "head revision"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lister := func(root, _ string) ([]string, error) {
				if root == test.failRoot {
					return nil, errors.New("list failure")
				}
				return nil, nil
			}
			_, err := selectAffectedPackages("base", "head", []string{"pkg/parser/parser.go"}, []string{"pkg/parser"}, lister)
			if err == nil || !strings.Contains(err.Error(), "pkg/parser") || !strings.Contains(err.Error(), test.wantRoot) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParseProductionInputs(t *testing.T) {
	root := filepath.Clean("/workspace/revision")
	input := strings.Join([]string{
		`{"ImportPath":"example/pkg","Dir":"/workspace/revision/pkg","GoFiles":["main.go","ignored_test.go"],"CFiles":["fast.c"],"EmbedFiles":["assets/schema.bin"]}`,
		`{"ImportPath":"example/pkg [example/pkg.test]","ForTest":"example/pkg","Dir":"/workspace/revision/pkg","GoFiles":["main.go","main_test.go"]}`,
		`{"ImportPath":"example/pkg.test","Dir":"/workspace/revision/pkg","GoFiles":["_testmain.go"]}`,
		`{"ImportPath":"external/pkg","Dir":"/go/pkg/mod/external/pkg","GoFiles":["external.go"]}`,
	}, "\n")

	got, err := parseProductionInputs(root, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pkg/assets/schema.bin", "pkg/fast.c", "pkg/main.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("production inputs = %v, want %v", got, want)
	}
}

func TestParseProductionInputsRejectsInvalidJSON(t *testing.T) {
	if _, err := parseProductionInputs("root", strings.NewReader("{")); err == nil {
		t.Fatal("invalid JSON error = nil")
	}
}

func TestListProductionInputs(t *testing.T) {
	inputs, err := listProductionInputs(filepath.Join("..", ".."), "scripts/benchscope")
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(inputs, "scripts/benchscope/main.go") {
		t.Fatalf("production inputs = %v", inputs)
	}
	if containsString(inputs, "scripts/benchscope/main_test.go") {
		t.Fatalf("test input leaked into production inputs: %v", inputs)
	}
}

func TestListProductionInputsRejectsInvalidRoot(t *testing.T) {
	if _, err := listProductionInputs("missing-root", "pkg/parser"); err == nil {
		t.Fatal("invalid root error = nil")
	}
}

func TestRunScopeWritesSortedAffectedPackages(t *testing.T) {
	directory := scopeTestDirectory(t)
	changedPath := filepath.Join(directory, "changed.txt")
	benchmarksPath := filepath.Join(directory, "benchmarks.txt")
	writeScopeFile(t, changedPath, "pkg/token/token.go\n")
	writeScopeFile(t, benchmarksPath, "pkg/parser\npkg/lexer\n")
	lister := fakeDependencyLister(
		map[string][]string{
			"pkg/parser": {"pkg/parser/parser.go", "pkg/token/token.go"},
			"pkg/lexer":  {"pkg/lexer/lexer.go", "pkg/token/token.go"},
		},
		nil,
		nil,
	)
	var output strings.Builder

	if err := runScope([]string{"base", "head", changedPath, benchmarksPath}, &output, lister); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "pkg/lexer\npkg/parser\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRunScopeRejectsInvalidInputs(t *testing.T) {
	directory := scopeTestDirectory(t)
	emptyPath := filepath.Join(directory, "empty.txt")
	writeScopeFile(t, emptyPath, "")

	if err := runScope(nil, io.Discard, fakeDependencyLister(nil, nil, nil)); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("usage error = %v", err)
	}
	if err := runScope([]string{"base", "head", "missing", emptyPath}, io.Discard, fakeDependencyLister(nil, nil, nil)); err == nil {
		t.Fatal("missing changed-files error = nil")
	}
	if err := runScope([]string{"base", "head", emptyPath, "missing"}, io.Discard, fakeDependencyLister(nil, nil, nil)); err == nil {
		t.Fatal("missing benchmark-packages error = nil")
	}
}

func TestRunScopeReportsOutputFailure(t *testing.T) {
	directory := scopeTestDirectory(t)
	changedPath := filepath.Join(directory, "changed.txt")
	benchmarksPath := filepath.Join(directory, "benchmarks.txt")
	writeScopeFile(t, changedPath, "go.sum\n")
	writeScopeFile(t, benchmarksPath, "pkg/parser\n")

	err := runScope([]string{"base", "head", changedPath, benchmarksPath}, failingWriter{}, fakeDependencyLister(nil, nil, nil))
	if err == nil || !strings.Contains(err.Error(), "write affected package") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunScopeReportsDependencyListingFailure(t *testing.T) {
	directory := scopeTestDirectory(t)
	changedPath := filepath.Join(directory, "changed.txt")
	benchmarksPath := filepath.Join(directory, "benchmarks.txt")
	writeScopeFile(t, changedPath, "pkg/parser/parser.go\n")
	writeScopeFile(t, benchmarksPath, "pkg/parser\n")

	err := runScope([]string{"base", "head", changedPath, benchmarksPath}, io.Discard, fakeDependencyLister(nil, nil, errors.New("list failure")))
	if err == nil || !strings.Contains(err.Error(), "list failure") {
		t.Fatalf("error = %v", err)
	}
}

func TestScopeCommandReturnsProcessStatus(t *testing.T) {
	directory := scopeTestDirectory(t)
	changedPath := filepath.Join(directory, "changed.txt")
	benchmarksPath := filepath.Join(directory, "benchmarks.txt")
	writeScopeFile(t, changedPath, "go.mod\n")
	writeScopeFile(t, benchmarksPath, "pkg/parser\n")
	lister := fakeDependencyLister(nil, nil, nil)

	if code := scopeCommand([]string{"base", "head", changedPath, benchmarksPath}, io.Discard, io.Discard, lister); code != 0 {
		t.Fatalf("success status = %d", code)
	}
	var errorOutput strings.Builder
	if code := scopeCommand(nil, io.Discard, &errorOutput, lister); code != 1 {
		t.Fatalf("error status = %d", code)
	}
	if !strings.Contains(errorOutput.String(), "usage") {
		t.Fatalf("error output = %q", errorOutput.String())
	}
}

func TestReadLinesReportsScannerFailure(t *testing.T) {
	directory := scopeTestDirectory(t)
	path := filepath.Join(directory, "large.txt")
	writeScopeFile(t, path, strings.Repeat("x", 70*1024))
	if _, err := readLines(path); err == nil {
		t.Fatal("scanner error = nil")
	}
}

func fakeDependencyLister(base, head map[string][]string, listError error) dependencyLister {
	return func(root, packagePath string) ([]string, error) {
		if listError != nil {
			return nil, listError
		}
		if root == "head" && head != nil {
			return head[packagePath], nil
		}
		return base[packagePath], nil
	}
}

func scopeTestDirectory(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", ".tmp")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(root, "benchscope-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanRoot := filepath.Clean(root) + string(filepath.Separator)
		if !strings.HasPrefix(filepath.Clean(directory), cleanRoot) {
			t.Fatalf("refusing to clean unexpected directory %s", directory)
		}
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("clean test directory: %v", err)
		}
	})
	return directory
}

func writeScopeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failure")
}
