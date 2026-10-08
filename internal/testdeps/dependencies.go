// Package testdeps checks production import boundaries in module tests.
package testdeps

import (
	"os/exec"
	"strings"
	"testing"
)

// Excludes checks the current package's production graph, not its test imports.
func Excludes(t testing.TB, prefixes ...string) {
	t.Helper()
	output, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("dependency graph: %v\n%s", err, output)
	}
	for _, prefix := range prefixes {
		if strings.Contains(string(output), prefix) {
			t.Fatalf("portable package imports %s", prefix)
		}
	}
}
