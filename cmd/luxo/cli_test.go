package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lux"
)

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"a\nb\nc", 3},
		{"a\nb\n", 2},
		{"single", 1},
		{"", 0},
		{"\n\n", 2},
	}
	for _, tt := range tests {
		lines := splitLines([]byte(tt.input))
		if len(lines) != tt.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.input, len(lines), tt.want)
		}
	}
}

func TestSplitLinesContent(t *testing.T) {
	lines := splitLines([]byte("hello\nworld"))
	if string(lines[0]) != "hello" || string(lines[1]) != "world" {
		t.Errorf("got %q, %q", string(lines[0]), string(lines[1]))
	}
}

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	os.WriteFile("go.mod", []byte("module github.com/test/myapp\n\ngo 1.26\n"), 0644)

	path, err := readModulePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "github.com/test/myapp" {
		t.Errorf("got %q", path)
	}
}

func TestReadModulePathNotFound(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// No go.mod
	_, err := readModulePath()
	if err == nil {
		t.Error("expected error when go.mod missing")
	}
}

func TestReadModulePathNoDirective(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	os.WriteFile("go.mod", []byte("go 1.26\n"), 0644)

	_, err := readModulePath()
	if err == nil {
		t.Error("expected error when module directive missing")
	}
}

func TestLoadDialect(t *testing.T) {
	d := loadDialect()
	if d == nil {
		t.Error("default dialect should not be nil")
	}
}

func TestLoadDialectMySQL(t *testing.T) {
	t.Setenv("DATABASE_DRIVER", "mysql")
	d := loadDialect()
	if d == nil {
		t.Error("mysql dialect should fallback to pg")
	}
}

func TestNextMigrationSeq(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// No migrations dir
	seq := nextMigrationSeq()
	if seq != 1 {
		t.Errorf("got %d, want 1", seq)
	}

	// Create some migration files
	os.Mkdir("migrations", 0755)
	os.WriteFile(filepath.Join("migrations", "001_init.sql"), []byte("--"), 0644)
	os.WriteFile(filepath.Join("migrations", "002_add_user.sql"), []byte("--"), 0644)

	seq = nextMigrationSeq()
	if seq != 3 {
		t.Errorf("got %d, want 3", seq)
	}
}

func TestPrintDiffOps(t *testing.T) {
	// Just ensure it doesn't panic for all op types
	ops := []codegen.DiffOp{
		{Kind: codegen.CreateTable, Table: "users"},
		{Kind: codegen.DropTable, Table: "old_table"},
		{Kind: codegen.AddColumn, Table: "users", Column: "email"},
		{Kind: codegen.DropColumn, Table: "users", Column: "old_col"},
		{Kind: codegen.RenameColumn, Table: "users", Column: "new_name", OldName: "old_name"},
		{Kind: codegen.AlterColumn, Table: "users", Column: "age"},
		{Kind: codegen.AddIndex, Index: &lux.IndexInfo{Name: "idx_email"}},
		{Kind: codegen.DropIndex, Index: &lux.IndexInfo{Name: "idx_old"}},
		{Kind: codegen.AddColumn, Table: "users", Column: "x", Warning: "breaking change"},
	}
	// Redirect stdout to discard
	old := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = old }()

	printDiffOps(ops)
}
