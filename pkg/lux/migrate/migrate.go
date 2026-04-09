package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/light-speak/luxo/pkg/lux/env"
)

// Runner executes migrations against a database.
type Runner struct {
	pool *pgxpool.Pool
	dir  string // migrations directory
}

// New creates a Runner from DATABASE_* env vars.
func New(ctx context.Context, dir string) (*Runner, error) {
	url := BuildDatabaseURL()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Runner{pool: pool, dir: dir}, nil
}

// Close closes the database connection.
func (r *Runner) Close() {
	r.pool.Close()
}

// Init creates the _migrations table if it doesn't exist.
func (r *Runner) Init(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _migrations (
			id         SERIAL PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	return err
}

// Up executes all pending migrations.
func (r *Runner) Up(ctx context.Context) ([]string, error) {
	if err := r.Init(ctx); err != nil {
		return nil, err
	}

	applied, err := r.appliedSet(ctx)
	if err != nil {
		return nil, err
	}

	pending, err := r.pendingFiles(applied)
	if err != nil {
		return nil, err
	}

	var executed []string
	for _, f := range pending {
		upSQL, err := parseSection(f, "UP")
		if err != nil {
			return executed, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if err := r.execMigration(ctx, filepath.Base(f), upSQL); err != nil {
			return executed, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		executed = append(executed, filepath.Base(f))
	}

	return executed, nil
}

// Down rolls back the last N migrations (default 1).
func (r *Runner) Down(ctx context.Context, n int) ([]string, error) {
	if err := r.Init(ctx); err != nil {
		return nil, err
	}
	if n <= 0 {
		n = 1
	}

	// Get last N applied migrations in reverse order
	rows, err := r.pool.Query(ctx, `
		SELECT name FROM _migrations ORDER BY id DESC LIMIT $1
	`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var toRollback []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		toRollback = append(toRollback, name)
	}

	var rolledBack []string
	for _, name := range toRollback {
		f := filepath.Join(r.dir, name)
		downSQL, err := parseSection(f, "DOWN")
		if err != nil {
			return rolledBack, fmt.Errorf("%s: %w", name, err)
		}
		if err := r.execRollback(ctx, name, downSQL); err != nil {
			return rolledBack, fmt.Errorf("%s: %w", name, err)
		}
		rolledBack = append(rolledBack, name)
	}

	return rolledBack, nil
}

// Status returns all migrations with their applied status.
func (r *Runner) Status(ctx context.Context) ([]MigrationStatus, error) {
	if err := r.Init(ctx); err != nil {
		return nil, err
	}

	applied, err := r.appliedMap(ctx)
	if err != nil {
		return nil, err
	}

	files, err := filepath.Glob(filepath.Join(r.dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	var result []MigrationStatus
	for _, f := range files {
		name := filepath.Base(f)
		ms := MigrationStatus{Name: name}
		if t, ok := applied[name]; ok {
			ms.Applied = true
			ms.AppliedAt = t
		}
		result = append(result, ms)
	}

	return result, nil
}

// MigrationStatus represents a migration's status.
type MigrationStatus struct {
	Name      string
	Applied   bool
	AppliedAt time.Time
}

// execMigration runs SQL in a transaction and records it in _migrations.
func (r *Runner) execMigration(ctx context.Context, name, sql string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO _migrations (name) VALUES ($1)`, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// execRollback runs down SQL in a transaction and removes from _migrations.
func (r *Runner) execRollback(ctx context.Context, name, sql string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM _migrations WHERE name = $1`, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// appliedSet returns the set of applied migration names.
func (r *Runner) appliedSet(ctx context.Context) (map[string]bool, error) {
	m, err := r.appliedMap(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(m))
	for k := range m {
		set[k] = true
	}
	return set, nil
}

// appliedMap returns applied migrations with their timestamps.
func (r *Runner) appliedMap(ctx context.Context) (map[string]time.Time, error) {
	rows, err := r.pool.Query(ctx, `SELECT name, applied_at FROM _migrations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]time.Time)
	for rows.Next() {
		var name string
		var t time.Time
		if err := rows.Scan(&name, &t); err != nil {
			return nil, err
		}
		result[name] = t
	}
	return result, nil
}

// pendingFiles returns migration files that haven't been applied yet.
func (r *Runner) pendingFiles(applied map[string]bool) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(r.dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	var pending []string
	for _, f := range files {
		if !applied[filepath.Base(f)] {
			pending = append(pending, f)
		}
	}
	return pending, nil
}

// parseSection extracts the UP or DOWN SQL from a migration file.
func parseSection(path, section string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)

	marker := "-- ====== " + section + " ======"
	idx := strings.Index(content, marker)
	if idx < 0 {
		return "", fmt.Errorf("section %s not found", section)
	}
	after := content[idx+len(marker):]

	// Find the next section marker or end of file
	nextMarker := "-- ======"
	end := strings.Index(after, nextMarker)
	if end < 0 {
		return strings.TrimSpace(after), nil
	}
	return strings.TrimSpace(after[:end]), nil
}

// Verify checks that the database matches the expected schema state.
func (r *Runner) Verify(ctx context.Context) ([]string, error) {
	if err := r.Init(ctx); err != nil {
		return nil, err
	}

	dbTables, err := r.listTables(ctx)
	if err != nil {
		return nil, err
	}

	var drifts []string
	drifts = append(drifts, r.checkPending(ctx)...)

	expected := r.loadExpectedTables()
	for _, t := range expected {
		if !dbTables[t] {
			drifts = append(drifts, fmt.Sprintf("missing table: %s (expected by schema)", t))
		}
	}

	expectedSet := make(map[string]bool, len(expected))
	for _, t := range expected {
		expectedSet[t] = true
	}
	for t := range dbTables {
		if !expectedSet[t] {
			drifts = append(drifts, fmt.Sprintf("unexpected table: %s (not in schema)", t))
		}
	}

	return drifts, nil
}

func (r *Runner) listTables(ctx context.Context) (map[string]bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		AND table_name != '_migrations'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables[name] = true
	}
	return tables, nil
}

func (r *Runner) checkPending(ctx context.Context) []string {
	applied, err := r.appliedSet(ctx)
	if err != nil {
		return nil
	}
	pending, err := r.pendingFiles(applied)
	if err != nil {
		return nil
	}
	var drifts []string
	for _, f := range pending {
		drifts = append(drifts, fmt.Sprintf("pending migration: %s", filepath.Base(f)))
	}
	return drifts
}

func (r *Runner) loadExpectedTables() []string {
	data, err := os.ReadFile(filepath.Join(r.dir, ".state.json"))
	if err != nil {
		return nil
	}
	type stateFile struct {
		Models map[string]struct {
			Table string `json:"table"`
		} `json:"models"`
	}
	var state stateFile
	if json.Unmarshal(data, &state) != nil {
		return nil
	}
	var tables []string
	for _, m := range state.Models {
		tables = append(tables, m.Table)
	}
	return tables
}

// BuildDatabaseURL constructs a connection string from DATABASE_* env vars.
func BuildDatabaseURL() string {
	host := envOr("DATABASE_HOST", "localhost")
	port := envOr("DATABASE_PORT", "5432")
	user := envOr("DATABASE_USER", "postgres")
	pass, _ := env.Get("DATABASE_PASSWORD")
	ssl := envOr("DATABASE_SSL", "disable")
	prefix := envOr("DATABASE_PREFIX", "luxo")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, prefix, ssl)
}

func envOr(key, fallback string) string {
	if v, ok := env.Get(key); ok {
		return v
	}
	return fallback
}
