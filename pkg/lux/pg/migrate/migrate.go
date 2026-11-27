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
	db DB
	migrationOperation
}

type migrationOperation struct {
	db                        migrationQueries
	dir                       string
	metadata                  migrationMetadata
	rollbackReceipt           string
	sessionBound              bool
	uncachedRetryQueries      bool
	uncachedStandaloneJournal bool
}

// New creates a Runner from DATABASE_* env vars.
func New(ctx context.Context, dir string) (*Runner, error) {
	url := BuildDatabaseURL()
	db, err := NewPGDB(ctx, url)
	if err != nil {
		return nil, err
	}
	return NewFromDB(db, dir), nil
}

// NewFromDB creates a Runner from an existing DB implementation.
func NewFromDB(db DB, dir string) *Runner {
	return &Runner{db: db, migrationOperation: migrationOperation{db: db, dir: dir}}
}

// Close closes the database connection.
func (r *Runner) Close() {
	r.db.Close()
}

// Init creates the _migrations table if it doesn't exist.
func (r *Runner) Init(ctx context.Context) error {
	operation, err := r.bindMetadata(ctx, false)
	if err != nil {
		return err
	}
	return operation.init(ctx)
}

func (r *migrationOperation) init(ctx context.Context) error {
	if r.metadata.schema == "" {
		return errors.New("PostgreSQL search_path has no schema for migration history")
	}
	return r.db.Exec(ctx, createHistory.sql(r.metadata.relation(migrationHistoryTable)))
}

// Up executes all pending migrations with advisory lock and checksum verification.
func (r *Runner) Up(ctx context.Context) ([]string, error) {
	return r.withMigrationLock(ctx, true, (*migrationOperation).up)
}

func (r *Runner) withMigrationLock(ctx context.Context, checkStringMode bool, run func(*migrationOperation, context.Context) ([]string, error)) ([]string, error) {
	if db, ok := r.db.(*PGDB); ok {
		operationCtx, stop := db.lockContext(ctx)
		defer stop()
		session, err := db.acquireLockSession(operationCtx, true)
		if err != nil {
			return nil, fmt.Errorf("acquire lock: %w", err)
		}
		holder := pgMigrationHolder{
			pgMigrationQueries: pgMigrationQueries[*pgx.Conn]{conn: session.conn},
			cachedStatements:   db.lockConfig.DefaultQueryExecMode == pgx.QueryExecModeCacheStatement && db.lockConfig.StatementCacheCapacity > 0,
		}
		defer func() { db.releaseLockSession(ctx, session, !holder.wrote) }()
		if err := db.lockLifetime.Err(); err != nil {
			return nil, err
		}
		operation := migrationOperation{db: &holder, dir: r.dir, sessionBound: true}
		return operation.runWithMetadata(operationCtx, checkStringMode, run)
	}

	if err := r.db.AcquireLock(ctx); err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer r.db.ReleaseLock(ctx)
	operation := r.migrationOperation
	operation.sessionBound = migrationSessionBound(r.db)
	return operation.runWithMetadata(ctx, checkStringMode, run)
}

func (r migrationOperation) runWithMetadata(ctx context.Context, checkStringMode bool, run func(*migrationOperation, context.Context) ([]string, error)) ([]string, error) {
	operation, err := r.bindMetadata(ctx, checkStringMode)
	if err != nil {
		return nil, err
	}
	return run(&operation, ctx)
}

func (r *migrationOperation) up(ctx context.Context) ([]string, error) {
	if err := r.ensureNoMigrationAttempt(ctx); err != nil {
		return nil, err
	}
	if r.metadata.historyExists {
		if err := r.verifyChecksums(ctx); err != nil {
			return nil, err
		}
	}
	plan, err := r.pendingPlan(ctx, true)
	if err != nil {
		return nil, err
	}
	if err := r.requireStandaloneSession(plan, "UP"); err != nil {
		return nil, err
	}
	r.configureStandaloneJournal(plan)
	if !r.metadata.historyExists {
		if err := r.init(ctx); err != nil {
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

// Only a native single-statement journal has no later write to reuse its
// cached statements. All multi-migration plans keep their existing cache policy.
func (r *migrationOperation) configureStandaloneJournal(plan []plannedMigration) {
	holder, native := r.db.(*pgMigrationHolder)
	r.uncachedStandaloneJournal = native && holder.cachedStatements && len(plan) == 1 && plan[0].mode.autocommit
}

func (r *migrationOperation) journalWritesUncached() bool {
	return r.uncachedRetryQueries || r.uncachedStandaloneJournal
}

// DryRun returns the pending SQL in execution order without changing the database.
func (r *Runner) DryRun(ctx context.Context) ([]string, []string, error) {
	operation, err := r.bindMetadata(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	if err := operation.ensureNoMigrationAttempt(ctx); err != nil {
		return nil, nil, err
	}
	plan, err := operation.pendingPlan(ctx, false)
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

func (r *migrationOperation) pendingPlan(ctx context.Context, withChecksum bool) ([]plannedMigration, error) {
	var applied map[string]bool
	if r.metadata.historyExists {
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
	rolledBack, _, err := r.DownWithReceipt(ctx, n)
	return rolledBack, err
}

// DownWithReceipt rolls back migrations and returns the last standalone rollback
// receipt ID on success. Use Reconcile with AcknowledgeMigration to clear it.
func (r *Runner) DownWithReceipt(ctx context.Context, n int) ([]string, string, error) {
	var receipt string
	rolledBack, err := r.withMigrationLock(ctx, true, func(operation *migrationOperation, ctx context.Context) ([]string, error) {
		names, err := operation.down(ctx, n)
		if err == nil {
			receipt = operation.rollbackReceipt
		}
		return names, err
	})
	return rolledBack, receipt, err
}

func (r *migrationOperation) down(ctx context.Context, n int) ([]string, error) {
	if err := r.ensureNoMigrationAttempt(ctx); err != nil {
		return nil, err
	}
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
	if err := r.requireStandaloneSession(plan, "DOWN"); err != nil {
		return nil, err
	}
	r.configureStandaloneJournal(plan)

	var rolledBack []string
	for _, migration := range plan {
		if err := r.execRollback(ctx, migration); err != nil {
			return rolledBack, fmt.Errorf("%s: DOWN: %w", migration.name, err)
		}
		rolledBack = append(rolledBack, migration.name)
	}
	return rolledBack, nil
}

func (r *migrationOperation) rollbackFiles(ctx context.Context, n int) ([]rollbackFile, error) {
	if !r.metadata.historyExists {
		return nil, nil
	}
	files, missingChecksum, err := r.queryRollbackFiles(ctx, n, true)
	if missingChecksum {
		files, _, err = r.queryRollbackFiles(ctx, n, false)
	}
	return files, err
}

func (r *migrationOperation) queryRollbackFiles(ctx context.Context, n int, withChecksum bool) ([]rollbackFile, bool, error) {
	statement := rollbackHistoryLegacy
	if withChecksum {
		statement = rollbackHistoryWithChecksum
	}
	rows, err := r.db.Query(ctx, statement.sql(r.metadata.relation(migrationHistoryTable)), n)
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
	operation, err := r.bindMetadata(ctx, false)
	if err != nil {
		return nil, err
	}
	return operation.status(ctx)
}

func (r *migrationOperation) status(ctx context.Context) ([]MigrationStatus, error) {
	if err := r.init(ctx); err != nil {
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
func (r *migrationOperation) execMigration(ctx context.Context, migration plannedMigration) error {
	return r.execSection(ctx, migration, "UP", insertHistory.sql(r.metadata.relation(migrationHistoryTable)), migration.name, migration.checksum)
}

// execRollback runs the DOWN section and removes it from _migrations.
func (r *migrationOperation) execRollback(ctx context.Context, migration plannedMigration) error {
	return r.execSection(ctx, migration, "DOWN", deleteHistory.sql(r.metadata.relation(migrationHistoryTable)), migration.name)
}

const migrationRetryAdvice = "Check PostgreSQL state and migration history before retrying"

func (r *migrationOperation) execSection(ctx context.Context, migration plannedMigration, direction, record string, args ...any) error {
	if !migration.mode.autocommit {
		return r.commitMigrationSection(ctx, migration, nil, record, args...)
	}
	// A prior migration can change the holder's string mode. Do not persist
	// an attempt for a statement that is rejected before it is sent.
	if holder, ok := r.db.(*pgMigrationHolder); ok {
		if err := checkMigrationStringMode(holder.conn); err != nil {
			return err
		}
	}
	attempt, err := r.startMigrationAttempt(ctx, migration, direction)
	if err != nil {
		return err
	}
	if err := r.runMigrationAttemptSQL(ctx, migration, attempt); err != nil {
		return err
	}
	if err := r.finishMigrationAttempt(ctx, migration, attempt); err != nil {
		return err
	}
	if direction == "DOWN" {
		r.rollbackReceipt = attempt.ID
	}
	return nil
}

func (r *migrationOperation) finishMigrationAttempt(ctx context.Context, migration plannedMigration, attempt *storedMigrationAttempt) error {
	record := insertHistory.sql(r.metadata.relation(migrationHistoryTable))
	value := any(migration.checksum)
	if attempt.Direction == "DOWN" {
		record = `WITH removed AS (
            DELETE FROM ` + r.metadata.relation(migrationHistoryTable) + ` WHERE name = $1 AND id = $2 RETURNING 1
        ) SELECT 1 / count(*) FROM removed`
		value = *attempt.historyID
	}
	var args []any
	if r.journalWritesUncached() {
		args = []any{pgx.QueryExecModeExec, migration.name, value}
	} else {
		args = []any{migration.name, value}
	}
	if err := r.commitMigrationSection(ctx, migration, attempt, record, args...); err != nil {
		return fmt.Errorf("migration attempt %s: %w", attempt.ID, err)
	}
	// A repeat of Down selects older rows once this one is deleted. The
	// committed receipt stays until the caller acknowledges this exact ID, or
	// until this same batch atomically replaces it with its next attempt.
	if attempt.Direction == "DOWN" {
		return nil
	}
	if err := r.acknowledgeMigrationAttempt(ctx, attempt.ID); err != nil {
		return fmt.Errorf("migration attempt %s committed, reconciliation acknowledgement unconfirmed: %w", attempt.ID, err)
	}
	return nil
}

func (r *migrationOperation) commitMigrationSection(ctx context.Context, migration plannedMigration, attempt *storedMigrationAttempt, record string, args ...any) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return migrationHistoryUncommitted(attempt != nil, err)
	}
	defer tx.Rollback(ctx)

	if attempt == nil && migration.sql != "" {
		if err := execMigrationStatement(ctx, tx, migration.sql); err != nil {
			return err
		}
	}
	if err := tx.Exec(ctx, record, args...); err != nil {
		return migrationHistoryUncommitted(attempt != nil, err)
	}
	if attempt != nil {
		if err := r.setMigrationAttemptCommitted(ctx, tx, attempt.ID); err != nil {
			return migrationHistoryUncommitted(true, err)
		}
	}
	err = tx.Commit(ctx)
	if err != nil && attempt != nil {
		return fmt.Errorf("autocommit statement returned success. The migration history commit is unconfirmed. %s: %w", migrationRetryAdvice, err)
	}
	return err
}

type migrationExec interface {
	Exec(context.Context, string, ...any) error
}

func execMigrationStatement(ctx context.Context, db migrationExec, sql string) error {
	if executor, ok := db.(interface {
		execMigrationStatement(context.Context, string) error
	}); ok {
		return executor.execMigrationStatement(ctx, sql)
	}
	return db.Exec(ctx, sql)
}

func migrationHistoryUncommitted(autocommit bool, err error) error {
	if !autocommit {
		return err
	}
	return fmt.Errorf("autocommit statement returned success. The change to migration history remains uncommitted. %s: %w", migrationRetryAdvice, err)
}

const concurrentIndexExists = `EXISTS (
		SELECT 1
		FROM pg_catalog.pg_class t
		JOIN pg_catalog.pg_index i ON i.indrelid = t.oid
		JOIN pg_catalog.pg_class x ON x.oid = i.indexrelid AND x.relnamespace = t.relnamespace
		WHERE t.oid = CASE WHEN $3::pg_catalog.oid = 0 THEN pg_catalog.to_regclass($2)::pg_catalog.oid ELSE $3 END
		  AND x.relname = (pg_catalog.parse_ident($1))[1]::pg_catalog.name
		  AND i.indisvalid AND i.indisready AND i.indislive
	)`

// PostgreSQL may report success when IF NOT EXISTS skips an invalid index left
// by an earlier failed concurrent build. Verify the index before writing history.
func (r *migrationOperation) verifyConcurrentIndex(ctx context.Context, mode migrationMode, tableOID uint32) error {
	var valid bool
	err := r.db.QueryRow(ctx, "SELECT "+concurrentIndexExists, mode.index, mode.table, tableOID).Scan(&valid)
	return concurrentIndexResult(mode, valid, err)
}

func concurrentIndexResult(mode migrationMode, valid bool, err error) error {
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
func (r *migrationOperation) verifyChecksums(ctx context.Context) error {
	rows, err := r.db.Query(ctx, checksumHistory.sql(r.metadata.relation(migrationHistoryTable)))
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

func (r *migrationOperation) appliedHistoryQuery() string {
	return appliedHistory.sql(r.metadata.relation(migrationHistoryTable))
}

// appliedSet validates the history rows while retaining only the applied names.
func (r *migrationOperation) appliedSet(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.Query(ctx, r.appliedHistoryQuery())
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
func (r *migrationOperation) appliedMap(ctx context.Context) (map[string]time.Time, error) {
	rows, err := r.db.Query(ctx, r.appliedHistoryQuery())
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
func (r *migrationOperation) pendingFiles(applied map[string]bool) ([]string, error) {
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
	operation, err := r.bindMetadata(ctx, false)
	if err != nil {
		return nil, err
	}
	return operation.verify(ctx)
}

func (r *migrationOperation) verify(ctx context.Context) ([]string, error) {
	if err := r.init(ctx); err != nil {
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

func (r *migrationOperation) listTables(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = $1 AND table_type = 'BASE TABLE'
		AND table_name NOT IN ($2, $3)
	`, r.metadata.schema, migrationHistoryTable, migrationAttemptTable)
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

func (r *migrationOperation) checkPending(ctx context.Context) ([]string, error) {
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

func (r *migrationOperation) loadExpectedTables() []string {
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
