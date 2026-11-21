package testdeps

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fatalf must stop execution exactly as testing.T does, while letting the outer
// test verify diagnostics without failing the whole test process.
type dependencyReporter struct {
	testing.TB
	message string
}
type dependencyFailure struct{}

func (*dependencyReporter) Helper() {}
func (r *dependencyReporter) Fatalf(format string, args ...any) {
	r.message = fmt.Sprintf(format, args...)
	panic(dependencyFailure{})
}
func dependencyDiagnostic(r *dependencyReporter, prefixes ...string) (message string) {
	defer func() {
		if failure := recover(); failure != nil {
			if _, ok := failure.(dependencyFailure); !ok {
				panic(failure)
			}
			message = r.message
		}
	}()
	Excludes(r, prefixes...)
	return ""
}

func TestExcludesDependencyGraph(t *testing.T) {
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "dependency-boundary-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, sample := range []struct {
		name, script string
		prefixes     []string
		want         string
	}{
		{"portable", "printf 'fmt\\nexample.com/portable\\n'", []string{"example.com/database", "example.com/queue"}, ""},
		{"forbidden", "printf 'fmt\\nexample.com/database/driver\\n'", []string{"example.com/database"}, "portable package imports example.com/database"},
		{"command error", "printf 'broken module graph\\n'; exit 42", []string{"example.com/database"}, "dependency graph: exit status 42"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			script := "#!/bin/sh\n[ \"$*\" = 'list -deps .' ] || exit 99\n" + sample.script + "\n"
			if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			message := dependencyDiagnostic(&dependencyReporter{}, sample.prefixes...)
			if sample.want == "" && message != "" || sample.want != "" && !strings.Contains(message, sample.want) {
				t.Fatalf("diagnostic %q, want %q", message, sample.want)
			}
			if sample.name == "command error" && !strings.Contains(message, "broken module graph") {
				t.Fatal("lost command output")
			}
		})
	}
}
