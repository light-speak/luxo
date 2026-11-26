package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/light-speak/luxo/pkg/lux"
)

// Runner executes migrations against a database.
type Runner struct {
	db  DB
	dir string // migrations directory
}

// New creates a Runner from DATABASE_* env vars.
func New(ctx context.Context, dir string) (*Runner, error) {
	url := BuildDatabaseURL()
	db, err := NewPGDB(ctx, url)
	if err != nil {
		return nil, err
	}
	return &Runner{db: db, dir: dir}, nil
}

// NewFromDB creates a Runner from an existing DB implementation.
func NewFromDB(db DB, dir string) *Runner {
	return &Runner{db: db, dir: dir}
}

// Close closes the database connection.
func (r *Runner) Close() {
	r.db.Close()
}

// Init creates the _migrations table if it doesn't exist.
func (r *Runner) Init(ctx context.Context) error {
	return r.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _migrations (
			id         SERIAL PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			checksum   TEXT,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
}

// Up executes all pending migrations with advisory lock and checksum verification.
func (r *Runner) Up(ctx context.Context) ([]string, error) {
	if err := r.db.AcquireLock(ctx); err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer r.db.ReleaseLock(ctx)

	exists, err := r.historyExists(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		if err := r.verifyChecksums(ctx); err != nil {
			return nil, err
		}
	}
	plan, err := r.pendingPlan(ctx, exists, true)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := r.Init(ctx); err != nil {
			return nil, err
		}
	}

	var executed []string
	for _, migration := range plan {
		if err := r.execMigration(ctx, migration); err != nil {
			return executed, fmt.Errorf("%s: UP: %w", migration.name, err)
		}
		executed = append(executed, migration.name)
	}
	return executed, nil
}

// DryRun returns the pending SQL in execution order without changing the database.
func (r *Runner) DryRun(ctx context.Context) ([]string, []string, error) {
	exists, err := r.historyExists(ctx)
	if err != nil {
		return nil, nil, err
	}
	plan, err := r.pendingPlan(ctx, exists, false)
	if err != nil {
		return nil, nil, err
	}

	var names, sqls []string
	for _, migration := range plan {
		names = append(names, migration.name)
		sqls = append(sqls, migration.sql)
	}
	return names, sqls, nil
}

func (r *Runner) historyExists(ctx context.Context) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, "SELECT pg_catalog.to_regclass('_migrations') IS NOT NULL").Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("read migration history: %w", err)
	}
	return exists, nil
}

func (r *Runner) pendingPlan(ctx context.Context, hasHistory, withChecksum bool) ([]plannedMigration, error) {
	var applied map[string]bool
	if hasHistory {
		var err error
		applied, err = r.appliedSet(ctx)
		if err != nil {
			return nil, err
		}
	}
	paths, err := r.pendingFiles(applied)
	if err != nil {
		return nil, err
	}
	return planPendingFiles(paths, withChecksum)
}

// Down rolls back the last N migrations (default 1) with advisory lock.
func (r *Runner) Down(ctx context.Context, n int) ([]string, error) {
	if err := r.db.AcquireLock(ctx); err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer r.db.ReleaseLock(ctx)
	if n <= 0 {
		n = 1
	}

	files, err := r.rollbackFiles(ctx, n)
	if err != nil {
		return nil, err
	}
	plan, err := planRollbackFiles(files)
	if err != nil {
		return nil, err
	}

	var rolledBack []string
	for _, migration := range plan {
		if err := r.execRollback(ctx, migration); err != nil {
			return rolledBack, fmt.Errorf("%s: DOWN: %w", migration.name, err)
		}
		rolledBack = append(rolledBack, migration.name)
	}
	return rolledBack, nil
}

func (r *Runner) rollbackFiles(ctx context.Context, n int) ([]rollbackFile, error) {
	files, missingChecksum, err := r.queryRollbackFiles(ctx, n, true)
	if missingChecksum {
		files, _, err = r.queryRollbackFiles(ctx, n, false)
	}
	if !migrationHasSQLState(err, "42P01") {
		return files, err
	}
	// An empty database needs no rollback. Confirm the missing relation before
	// treating an undefined-table error as success.
	exists, checkErr := r.historyExists(ctx)
	if checkErr != nil {
		return nil, errors.Join(err, checkErr)
	}
	if exists {
		return nil, err
	}
	return nil, nil
}

func (r *Runner) queryRollbackFiles(ctx context.Context, n int, withChecksum bool) ([]rollbackFile, bool, error) {
	const legacyQuery = `SELECT name FROM _migrations ORDER BY id DESC LIMIT $1`
	const checksumQuery = `SELECT name, checksum FROM _migrations ORDER BY id DESC LIMIT $1`
	query := legacyQuery
	if withChecksum {
		query = checksumQuery
	}
	rows, err := r.db.Query(ctx, query, n)
	if err != nil {
		return nil, withChecksum && migrationHasSQLState(err, "42703"), err
	}
	defer rows.Close()

	var files []rollbackFile
	for rows.Next() {
		var name string
		var checksum *string
		var scanErr error
		if withChecksum {
			scanErr = rows.Scan(&name, &checksum)
		} else {
			scanErr = rows.Scan(&name)
		}
		if scanErr != nil {
			return nil, false, scanErr
		}
		files = append(files, rollbackFile{path: filepath.Join(r.dir, name), checksum: checksum})
	}
	if err := migrationRowsError(rows); err != nil {
		return nil, withChecksum && len(files) == 0 && migrationHasSQLState(err, "42703"), err
	}
	return files, false, nil
}

func migrationHasSQLState(err error, state string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == state
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
	slices.SortFunc(files, compareMigrationFiles)

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

// execMigration runs SQL and records it in _migrations.
func (r *Runner) execMigration(ctx context.Context, migration plannedMigration) error {
	return r.execSection(ctx, migration, `INSERT INTO _migrations (name, checksum) VALUES ($1, $2)`, migration.name, migration.checksum)
}

// execRollback runs the DOWN section and removes it from _migrations.
func (r *Runner) execRollback(ctx context.Context, migration plannedMigration) error {
	return r.execSection(ctx, migration, `DELETE FROM _migrations WHERE name = $1`, migration.name)
}

const migrationRetryAdvice = "Check PostgreSQL state and migration history before retrying"

func (r *Runner) execSection(ctx context.Context, migration plannedMigration, record string, args ...any) error {
	if migration.mode.autocommit {
		if err := r.db.Exec(ctx, migration.sql); err != nil {
			return fmt.Errorf("autocommit statement success is unconfirmed. %s: %w", migrationRetryAdvice, err)
		}
		if migration.mode.index != "" {
			if err := r.verifyConcurrentIndex(ctx, migration.mode); err != nil {
				return migrationHistoryUncommitted(true, err)
			}
		}
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return migrationHistoryUncommitted(migration.mode.autocommit, err)
	}
	defer tx.Rollback(ctx)

	if !migration.mode.autocommit && migration.sql != "" {
		if err := tx.Exec(ctx, migration.sql); err != nil {
			return err
		}
	}
	if err := tx.Exec(ctx, record, args...); err != nil {
		return migrationHistoryUncommitted(migration.mode.autocommit, err)
	}
	err = tx.Commit(ctx)
	if err != nil && migration.mode.autocommit {
		return fmt.Errorf("autocommit statement returned success. The migration history commit is unconfirmed. %s: %w", migrationRetryAdvice, err)
	}
	return err
}

func migrationHistoryUncommitted(autocommit bool, err error) error {
	if !autocommit {
		return err
	}
	return fmt.Errorf("autocommit statement returned success. The change to migration history remains uncommitted. %s: %w", migrationRetryAdvice, err)
}

// PostgreSQL may report success when IF NOT EXISTS skips an invalid index left
// by an earlier failed concurrent build. Verify the index before writing history.
func (r *Runner) verifyConcurrentIndex(ctx context.Context, mode migrationMode) error {
	var valid bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM pg_catalog.pg_class t
		JOIN pg_catalog.pg_index i ON i.indrelid = t.oid
		JOIN pg_catalog.pg_class x ON x.oid = i.indexrelid AND x.relnamespace = t.relnamespace
		WHERE t.oid = pg_catalog.to_regclass($2)
		  AND x.relname = (pg_catalog.parse_ident($1))[1]::pg_catalog.name
		  AND i.indisvalid AND i.indisready AND i.indislive
	)`, mode.index, mode.table).Scan(&valid)
	if err != nil {
		return fmt.Errorf("verify index %s on %s: %w", mode.index, mode.table, err)
	}
	if !valid {
		return fmt.Errorf("index %s on %s is invalid, missing, or belongs to another table; inspect the existing object and resolve the failed build before retrying", mode.index, mode.table)
	}
	return nil
}

// Drivers that expose iteration errors must report them before any migration runs.
func migrationRowsError(rows Rows) error {
	if result, ok := rows.(interface{ Err() error }); ok {
		return result.Err()
	}
	return nil
}

// fileChecksum returns SHA-256 hash of a migration file.
func fileChecksum(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return migrationChecksum(data)
}

func migrationChecksum(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:16])
}

// verifyChecksums checks that applied migrations haven't been modified.
func (r *Runner) verifyChecksums(ctx context.Context) error {
	rows, err := r.db.Query(ctx, `SELECT name, checksum FROM _migrations WHERE checksum IS NOT NULL ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var name, storedChecksum string
		if err := rows.Scan(&name, &storedChecksum); err != nil {
			return err
		}
		filePath := filepath.Join(r.dir, name)
		currentChecksum := fileChecksum(filePath)
		if currentChecksum != "" && currentChecksum != storedChecksum {
			return fmt.Errorf("migration %s has been modified (checksum mismatch: stored=%s, current=%s)", name, storedChecksum, currentChecksum)
		}
	}
	return migrationRowsError(rows)
}

const appliedHistoryQuery = `SELECT name, applied_at FROM _migrations ORDER BY id`

// appliedSet validates the history rows while retaining only the applied names.
func (r *Runner) appliedSet(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.Query(ctx, appliedHistoryQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	set := make(map[string]bool)
	for rows.Next() {
		var name string
		var appliedAt time.Time
		if err := rows.Scan(&name, &appliedAt); err != nil {
			return nil, err
		}
		set[name] = true
	}
	if err := migrationRowsError(rows); err != nil {
		return nil, err
	}
	return set, nil
}

// appliedMap returns applied migrations with their timestamps.
func (r *Runner) appliedMap(ctx context.Context) (map[string]time.Time, error) {
	rows, err := r.db.Query(ctx, appliedHistoryQuery)
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
	return result, migrationRowsError(rows)
}

// compareMigrationFiles orders numeric runs by value, then breaks equal-value
// ties by the original name. It never parses integers or allocates sort keys.
func compareMigrationFiles(left, right string) int {
	a, b := filepath.Base(left), filepath.Base(right)
	i, j := 0, 0
	for i < min(len(a), len(b)) && a[i] == b[i] {
		i++
	}
	// Revisit a shared digit prefix so different run lengths still sort numerically.
	for i > 0 && migrationNameDigit(a[i-1]) {
		i--
	}
	j = i
	for i < len(a) && j < len(b) {
		if migrationNameDigit(a[i]) && migrationNameDigit(b[j]) {
			endA, endB := migrationNameDigitEnd(a, i), migrationNameDigitEnd(b, j)
			if order := compareMigrationDigits(a[i:endA], b[j:endB]); order != 0 {
				return order
			}
			i, j = endA, endB
			continue
		}
		if a[i] != b[j] {
			return int(a[i]) - int(b[j])
		}
		i++
		j++
	}
	if remaining := (len(a) - i) - (len(b) - j); remaining != 0 {
		return remaining
	}
	return strings.Compare(a, b)
}

func compareMigrationDigits(left, right string) int {
	left, right = strings.TrimLeft(left, "0"), strings.TrimLeft(right, "0")
	if len(left) != len(right) {
		return len(left) - len(right)
	}
	return strings.Compare(left, right)
}

func migrationNameDigitEnd(name string, start int) int {
	for start < len(name) && migrationNameDigit(name[start]) {
		start++
	}
	return start
}

func migrationNameDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

// pendingFiles returns migration files that haven't been applied yet.
func (r *Runner) pendingFiles(applied map[string]bool) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(r.dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, compareMigrationFiles)

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
	return migrationSection(string(data), section)
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

	drifts, err := r.checkPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("check pending migrations: %w", err)
	}

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
	rows, err := r.db.Query(ctx, `
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
	if err := migrationRowsError(rows); err != nil {
		return nil, err
	}
	return tables, nil
}

func (r *Runner) checkPending(ctx context.Context) ([]string, error) {
	applied, err := r.appliedSet(ctx)
	if err != nil {
		return nil, err
	}
	pending, err := r.pendingFiles(applied)
	if err != nil {
		return nil, err
	}
	var drifts []string
	for _, f := range pending {
		drifts = append(drifts, fmt.Sprintf("pending migration: %s", filepath.Base(f)))
	}
	return drifts, nil
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
	return lux.DBConfigFromEnv().ConnectionString()
}

// EnsureDatabase creates the target database if it doesn't exist.
// Connects to the default "postgres" database to run CREATE DATABASE.
func EnsureDatabase(ctx context.Context) error {
	cfg := lux.DBConfigFromEnv()
	targetDB := cfg.DBName
	if targetDB == "postgres" {
		return nil // already using default
	}

	// Connect to default "postgres" database
	adminCfg := cfg
	adminCfg.DBName = "postgres"
	conn, err := pgx.Connect(ctx, adminCfg.ConnectionString())
	if err != nil {
		return fmt.Errorf("ensure db: connect to postgres: %w", err)
	}
	defer conn.Close(ctx)

	exists, err := databaseExists(ctx, conn, targetDB)
	if err != nil {
		return fmt.Errorf("ensure db: check exists: %w", err)
	}
	if exists {
		return nil
	}

	// SQL identifiers are not Go string literals. Keep the exact configured
	// name, including quotes and backslashes, without executable SQL fragments.
	_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{targetDB}.Sanitize())
	if err == nil {
		return nil
	}
	if databaseCreationConflict(err) {
		// Concurrent creation can report duplicate_database or a catalog unique
		// violation. Neither is success until this exact database exists.
		if exists, checkErr := databaseExists(ctx, conn, targetDB); checkErr == nil && exists {
			return nil
		}
	}
	return fmt.Errorf("ensure db: create %s: %w", targetDB, err)
}

func databaseCreationConflict(err error) bool {
	pgError, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgError != nil && (pgError.Code == "42P04" || pgError.Code == "23505")
}

func databaseExists(ctx context.Context, conn *pgx.Conn, name string) (bool, error) {
	var exists bool
	var bytes, limit int
	// Read the server's actual identifier limit and byte length in the same
	// lookup, avoiding silent PostgreSQL truncation without another round trip.
	err := conn.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM pg_database WHERE datname = $1::text),
		octet_length($1::text), current_setting('max_identifier_length')::int`, name).Scan(&exists, &bytes, &limit)
	if err != nil {
		return false, err
	}
	if bytes == 0 || bytes > limit {
		return false, fmt.Errorf("database name requires 1..%d bytes, got %d", limit, bytes)
	}
	return exists, nil
}
