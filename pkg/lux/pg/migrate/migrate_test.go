package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestNewPGDBRejectsInvalidConfiguration(t *testing.T) {
	if db, err := NewPGDB(t.Context(), "postgres://localhost/db?port=invalid"); err == nil || db != nil {
		t.Fatalf("invalid configuration: db=%v err=%v", db, err)
	}
}

func TestPendingMigrationOrdinalsAfterThreeDigits(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(root, "migration-order-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(directory) != root {
			t.Fatal("refusing cleanup outside owned migration fixture")
		}
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	want := []string{"20261113_998_prior.sql", "20261113_999_prior.sql", "20261113_1000_next.sql", "20261114_1001_next_day.sql"}
	for _, name := range want {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("-- SQL\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &Runner{dir: directory}
	files, err := runner.pendingFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := range files {
		files[index] = filepath.Base(files[index])
	}
	if strings.Join(files, "|") != strings.Join(want, "|") {
		t.Fatalf("migration execution order crossed ordinal999 incorrectly: got=%v want=%v", files, want)
	}
}

func TestMigrationFilenameOrdering(t *testing.T) {
	ordered := []string{
		"", "0.sql", "00.sql", "000.sql", "000_a.sql", "00_a.sql", "0_a.sql",
		"1.sql", "01_a.sql", "1_a.sql", "2_a.sql", "009_a.sql", "10_a.sql",
		"20261113_998_prior.sql", "20261113_999_prior.sql", "20261113_1000_next.sql",
		"20261114_001_next.sql", "99999999999999999999999999999999999999_a.sql",
		"a", "a0", "a00_a", "a0_a", "a1", "a9", "a10", "a10z", "b", "迁移2.sql", "迁移10.sql",
	}
	for i, left := range ordered {
		if compareMigrationFiles(left, left) != 0 {
			t.Fatalf("filename differs from itself: %q", left)
		}
		for j := i + 1; j < len(ordered); j++ {
			right := ordered[j]
			if compareMigrationFiles(left, right) >= 0 || compareMigrationFiles(right, left) <= 0 {
				t.Errorf("inconsistent order: %q before %q", left, right)
			}
		}
	}
	actual := slices.Clone(ordered)
	slices.Reverse(actual)
	slices.SortFunc(actual, compareMigrationFiles)
	if !slices.Equal(actual, ordered) {
		t.Errorf("sort order: got=%q want=%q", actual, ordered)
	}
	if compareMigrationFiles("/left/001_a.sql", "/right/001_a.sql") != 0 {
		t.Error("parent path affected migration filename order")
	}
}

func FuzzMigrationFilenameOrdering(f *testing.F) {
	for _, sample := range [][3]string{
		{"001_a.sql", "01_a.sql", "1_a.sql"},
		{"20261113_999_x.sql", "20261113_1000_x.sql", "20261114_001_x.sql"},
		{"", "0", "a"}, {"a0", "a00x", "a1"},
	} {
		f.Add(sample[0], sample[1], sample[2])
	}
	f.Fuzz(func(t *testing.T, a, b, c string) {
		ab, ba := compareMigrationFiles(a, b), compareMigrationFiles(b, a)
		if (ab < 0) != (ba > 0) || (ab == 0) != (ba == 0) {
			t.Fatalf("asymmetric ordering: %q %q: %d %d", a, b, ab, ba)
		}
		bc, ac := compareMigrationFiles(b, c), compareMigrationFiles(a, c)
		if ab <= 0 && bc <= 0 && ac > 0 {
			t.Fatalf("non-transitive ordering: %q <= %q <= %q", a, b, c)
		}
	})
}

func BenchmarkPendingMigrationFiles(b *testing.B) {
	for _, count := range []int{3, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".tmp"))
			if err != nil {
				b.Fatal(err)
			}
			dir, err := os.MkdirTemp(root, "migration-sort-bench-")
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				if filepath.Dir(dir) != root {
					b.Fatal("unsafe migration benchmark cleanup")
				}
				if err := os.RemoveAll(dir); err != nil {
					b.Error(err)
				}
			})
			for i := 1; i <= count; i++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("20261113_%03d_label.sql", i)), nil, 0600); err != nil {
					b.Fatal(err)
				}
			}
			runner := &Runner{dir: dir}
			b.ReportAllocs()
			for b.Loop() {
				files, err := runner.pendingFiles(nil)
				if err != nil || len(files) != count {
					b.Fatal(len(files), err)
				}
			}
		})
	}
}

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

func TestEnsureDatabaseDefaultAndCanceledConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	t.Setenv("DATABASE_PREFIX", "postgres")
	if err := EnsureDatabase(ctx); err != nil {
		t.Fatal("default database should not connect", err)
	}
	t.Setenv("DATABASE_PREFIX", "luxo_ensure_canceled")
	if err := EnsureDatabase(ctx); err == nil {
		t.Fatal("canceled connection reported success")
	}
}

func TestDatabaseCreationConflictCodes(t *testing.T) {
	for _, sample := range []struct {
		err      error
		conflict bool
	}{
		{nil, false},
		{(*pgconn.PgError)(nil), false},
		{errors.New("already exists"), false},
		{&pgconn.PgError{Code: "42501", Message: "already exists"}, false},
		{&pgconn.PgError{Code: "42P04", Message: "localized diagnostic"}, true},
		{&pgconn.PgError{Code: "23505"}, true},
		{fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "42P04"}), true},
	} {
		if got := databaseCreationConflict(sample.err); got != sample.conflict {
			t.Errorf("conflict(%v)=%v", sample.err, got)
		}
	}
}
