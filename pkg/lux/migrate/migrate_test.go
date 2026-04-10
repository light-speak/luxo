package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestEnvOr(t *testing.T) {
	t.Setenv("TEST_ENVVAR_EXISTS", "value")
	os.Unsetenv("TEST_ENVVAR_MISSING")

	if got := envOr("TEST_ENVVAR_EXISTS", "fallback"); got != "value" {
		t.Errorf("envOr with existing: got %q, want %q", got, "value")
	}
	if got := envOr("TEST_ENVVAR_MISSING", "fallback"); got != "fallback" {
		t.Errorf("envOr with missing: got %q, want %q", got, "fallback")
	}
}
