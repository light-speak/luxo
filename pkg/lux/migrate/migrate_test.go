package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSectionUP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "001_create_users.sql")
	content := `-- ====== UP ======
CREATE TABLE users (
  id SERIAL PRIMARY KEY,
  name TEXT NOT NULL
);
-- ====== DOWN ======
DROP TABLE IF EXISTS users;
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	up, err := parseSection(path, "UP")
	if err != nil {
		t.Fatalf("parseSection UP: %v", err)
	}
	if !strings.Contains(up, "CREATE TABLE users") {
		t.Errorf("UP should contain CREATE TABLE, got:\n%s", up)
	}
	// UP section should NOT contain DOWN content
	if strings.Contains(up, "DROP TABLE") {
		t.Error("UP section should not contain DOWN content")
	}
}

func TestParseSectionDOWN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "001_create_users.sql")
	content := `-- ====== UP ======
CREATE TABLE users (id SERIAL PRIMARY KEY);
-- ====== DOWN ======
DROP TABLE IF EXISTS users;
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	down, err := parseSection(path, "DOWN")
	if err != nil {
		t.Fatalf("parseSection DOWN: %v", err)
	}
	if !strings.Contains(down, "DROP TABLE IF EXISTS users") {
		t.Errorf("DOWN should contain DROP TABLE, got:\n%s", down)
	}
	if strings.Contains(down, "CREATE TABLE") {
		t.Error("DOWN section should not contain UP content")
	}
}

func TestParseSectionDOWNAtEndOfFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "002.sql")
	content := `-- ====== UP ======
ALTER TABLE users ADD COLUMN bio TEXT;
-- ====== DOWN ======
ALTER TABLE users DROP COLUMN bio;`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	down, err := parseSection(path, "DOWN")
	if err != nil {
		t.Fatalf("parseSection DOWN at EOF: %v", err)
	}
	if !strings.Contains(down, "ALTER TABLE users DROP COLUMN bio") {
		t.Errorf("DOWN should contain DROP COLUMN, got:\n%s", down)
	}
}

func TestParseSectionMissingSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.sql")
	content := `-- This file has no sections
CREATE TABLE users (id SERIAL);
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := parseSection(path, "UP")
	if err == nil {
		t.Fatal("expected error for missing UP section")
	}
	if !strings.Contains(err.Error(), "section UP not found") {
		t.Errorf("unexpected error: %v", err)
	}

	_, err = parseSection(path, "DOWN")
	if err == nil {
		t.Fatal("expected error for missing DOWN section")
	}
	if !strings.Contains(err.Error(), "section DOWN not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseSectionFileNotFound(t *testing.T) {
	_, err := parseSection("/nonexistent/path.sql", "UP")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestBuildDatabaseURLDefaults(t *testing.T) {
	// Clear all DATABASE_* env vars to get defaults
	for _, key := range []string{
		"DATABASE_HOST", "DATABASE_PORT", "DATABASE_USER",
		"DATABASE_PASSWORD", "DATABASE_SSL", "DATABASE_PREFIX",
	} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}

	url := BuildDatabaseURL()
	// Defaults: localhost, 5432, postgres, empty password, disable ssl, luxo db
	want := "postgres://postgres:@localhost:5432/luxo?sslmode=disable"
	if url != want {
		t.Errorf("BuildDatabaseURL defaults:\ngot  %q\nwant %q", url, want)
	}
}

func TestBuildDatabaseURLCustom(t *testing.T) {
	t.Setenv("DATABASE_HOST", "db.example.com")
	t.Setenv("DATABASE_PORT", "5433")
	t.Setenv("DATABASE_USER", "myuser")
	t.Setenv("DATABASE_PASSWORD", "secret")
	t.Setenv("DATABASE_SSL", "require")
	t.Setenv("DATABASE_PREFIX", "myapp")

	url := BuildDatabaseURL()
	want := "postgres://myuser:secret@db.example.com:5433/myapp?sslmode=require"
	if url != want {
		t.Errorf("BuildDatabaseURL custom:\ngot  %q\nwant %q", url, want)
	}
}

func TestBuildDatabaseURLPartialEnv(t *testing.T) {
	// Set only host and password, rest use defaults
	for _, key := range []string{
		"DATABASE_PORT", "DATABASE_USER",
		"DATABASE_SSL", "DATABASE_PREFIX",
	} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("DATABASE_HOST", "192.168.1.100")
	t.Setenv("DATABASE_PASSWORD", "pass123")

	url := BuildDatabaseURL()
	want := "postgres://postgres:pass123@192.168.1.100:5432/luxo?sslmode=disable"
	if url != want {
		t.Errorf("BuildDatabaseURL partial:\ngot  %q\nwant %q", url, want)
	}
}

func TestSplitConcurrently(t *testing.T) {
	sql := "CREATE TABLE users (id INT);\nCREATE INDEX CONCURRENTLY idx ON users (name);\nALTER TABLE users ADD COLUMN age INT;"
	txSQL, nonTx := splitConcurrently(sql)

	if len(nonTx) != 1 {
		t.Fatalf("expected 1 non-tx, got %d", len(nonTx))
	}
	if !strings.Contains(nonTx[0], "CONCURRENTLY") {
		t.Error("non-tx should contain CONCURRENTLY")
	}
	if strings.Contains(txSQL, "CONCURRENTLY") {
		t.Error("tx SQL should not contain CONCURRENTLY")
	}
}

func TestSplitConcurrentlyNone(t *testing.T) {
	txSQL, nonTx := splitConcurrently("CREATE TABLE users (id INT);")
	if len(nonTx) != 0 {
		t.Errorf("expected 0 non-tx, got %d", len(nonTx))
	}
	if !strings.Contains(txSQL, "CREATE TABLE") {
		t.Error("should contain all")
	}
}

func TestSplitConcurrentlyEmpty(t *testing.T) {
	_, nonTx := splitConcurrently("")
	if len(nonTx) != 0 {
		t.Errorf("expected 0, got %d", len(nonTx))
	}
}

func TestSplitStatements(t *testing.T) {
	stmts := splitStatements("SELECT 1; SELECT 2;  ; SELECT 3")
	if len(stmts) != 3 {
		t.Errorf("expected 3, got %d: %v", len(stmts), stmts)
	}
}

func TestFileChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sql")
	os.WriteFile(path, []byte("CREATE TABLE users (id INT);"), 0644)

	cs1 := fileChecksum(path)
	if cs1 == "" || len(cs1) != 32 {
		t.Errorf("bad checksum: %q", cs1)
	}

	os.WriteFile(path, []byte("DIFFERENT"), 0644)
	cs2 := fileChecksum(path)
	if cs1 == cs2 {
		t.Error("different content should differ")
	}
}

func TestFileChecksumNotFound(t *testing.T) {
	if fileChecksum("/nonexistent") != "" {
		t.Error("should be empty")
	}
}

func TestNewConnectionError(t *testing.T) {
	t.Setenv("DATABASE_HOST", "192.0.2.1") // non-routable
	t.Setenv("DATABASE_PORT", "1")
	t.Setenv("DATABASE_USER", "bad")
	t.Setenv("DATABASE_PASSWORD", "bad")
	t.Setenv("DATABASE_PREFIX", "bad")
	t.Setenv("DATABASE_SSL", "disable")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := New(ctx, t.TempDir())
	if err == nil {
		t.Fatal("should fail")
	}
}
