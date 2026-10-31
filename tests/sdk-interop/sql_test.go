package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSQLFixture(t *testing.T) {
	t.Chdir("../..")
	root, err := filepath.Abs(".tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	out, err := os.MkdirTemp(root, "sdk-sql-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(out) != root {
			t.Fatal("invalid scratch directory")
		}
		if err := os.RemoveAll(out); err != nil {
			t.Error(err)
		}
	})
	for _, invalid := range []string{"", root, filepath.Join(out, "missing")} {
		if err := generateSQLFixture(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	if err := generateSQLFixture(out); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(out, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, fixtureSQLServer) {
		t.Fatal("wrong server template")
	}
	if err := generateSQLFixture(out); err == nil {
		t.Fatal("overwrote existing output")
	}
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(out, "server"), out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	cmd = exec.CommandContext(t.Context(), filepath.Join(out, "server"))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "LUXO_TEST_DATABASE_URL=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("dedicated empty test database")) {
		t.Fatalf("missing database was not rejected: %v\n%s", err, output)
	}
	checkFixtureRegistrationErrors(t, "sql.go", fixtureSQLServer)
}
