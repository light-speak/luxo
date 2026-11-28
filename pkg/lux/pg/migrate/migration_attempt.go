package migrate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jackc/pgx/v5"
)

// MigrationAttempt identifies a transaction-external migration requiring reconciliation.
type MigrationAttempt struct {
	ID               string
	Name             string
	Direction        string
	HistoryCommitted bool
}

// MigrationReconciliationError suspends migration work until a named attempt is reconciled.
type MigrationReconciliationError struct {
	Attempt MigrationAttempt
}

func (e *MigrationReconciliationError) Error() string {
	phase := "history commit unconfirmed"
	if e.Attempt.HistoryCommitted {
		phase = "history committed"
	}
	return fmt.Sprintf("migration reconciliation required for %s %s (attempt %s, %s)", e.Attempt.Direction, e.Attempt.Name, e.Attempt.ID, phase)
}

type storedMigrationAttempt struct {
	MigrationAttempt
	digest           string
	historyID        *int64
	historyChecksum  *string
	searchPath       string
	executionRole    string
	executionSchemas string
	executionBinding string
}

const migrationAttemptTableDefinition = ` (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    id text NOT NULL,
    name text NOT NULL,
    direction text NOT NULL CHECK (direction IN ('UP', 'DOWN')),
    digest text NOT NULL CHECK (length(digest) = 64),
    history_id bigint,
    history_checksum text,
    history_committed boolean NOT NULL DEFAULT false,
    execution_search_path text NOT NULL,
    execution_role text NOT NULL,
    execution_schemas text NOT NULL,
    execution_binding jsonb,
    CHECK ((direction = 'UP' AND history_id IS NULL AND history_checksum IS NULL) OR
           (direction = 'DOWN' AND history_id IS NOT NULL))
)`

const migrationExecutionRole = `pg_catalog.to_regrole(pg_catalog.quote_ident(current_user))::oid::text`

// Use the effective schema OIDs and order, so missing, replaced, or newly
// accessible schemas cannot silently redirect a named retry.
const migrationExecutionSchemas = `ARRAY(
    SELECT namespace.oid
    FROM pg_catalog.unnest(pg_catalog.current_schemas(true)) WITH ORDINALITY AS resolved(name, position)
    JOIN pg_catalog.pg_namespace namespace ON namespace.nspname = resolved.name
    ORDER BY resolved.position
)::text`

func (r *migrationOperation) requireStandaloneSession(plan []plannedMigration, direction string) error {
	if !r.sessionBound {
		for _, migration := range plan {
			if migration.mode.autocommit {
				return fmt.Errorf("%s: %s: standalone migration requires a lock-bound database session", migration.name, direction)
			}
		}
	}
	return nil
}

func (r *migrationOperation) ensureNoMigrationAttempt(ctx context.Context) error {
	attempt, err := r.readMigrationAttempt(ctx, false)
	if err != nil || attempt == nil {
		return err
	}
	return &MigrationReconciliationError{Attempt: attempt.MigrationAttempt}
}

func (r *migrationOperation) readMigrationAttempt(ctx context.Context, includeBinding bool) (*storedMigrationAttempt, error) {
	if !r.metadata.attemptExists {
		return nil, nil
	}
	var attempt storedMigrationAttempt
	err := r.readMigrationAttemptRow(ctx, &attempt, includeBinding)
	if includeBinding && migrationHasSQLState(err, "42703") {
		// Receipts from an earlier journal can still be inspected, recorded,
		// or acknowledged even though they did not bind retry targets.
		err = r.readMigrationAttemptRow(ctx, &attempt, false)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read migration reconciliation state: %w", err)
	}
	return &attempt, nil
}

const readMigrationAttemptProjection = `
        SELECT id, name, direction, digest, history_id, history_checksum, history_committed,
               execution_search_path, execution_role, execution_schemas, `
const readMigrationAttemptFrom = `
        FROM `
const readMigrationAttemptWhere = ` WHERE singleton
    `
const emptyMigrationBinding = `''`
const selectedMigrationBinding = `COALESCE(execution_binding::text, '')`

const readPublicMigrationAttempt = readMigrationAttemptProjection + emptyMigrationBinding + readMigrationAttemptFrom + publicMigrationAttempt + readMigrationAttemptWhere
const readPublicBoundMigrationAttempt = readMigrationAttemptProjection + selectedMigrationBinding + readMigrationAttemptFrom + publicMigrationAttempt + readMigrationAttemptWhere

func (r *migrationOperation) readMigrationAttemptRow(ctx context.Context, attempt *storedMigrationAttempt, includeBinding bool) error {
	query, binding := readPublicMigrationAttempt, emptyMigrationBinding
	if includeBinding {
		query, binding = readPublicBoundMigrationAttempt, selectedMigrationBinding
	}
	if r.metadata.schema != "public" {
		query = readMigrationAttemptProjection + binding + readMigrationAttemptFrom + r.metadata.relation(migrationAttemptTable) + readMigrationAttemptWhere
	}
	return r.queryMigrationLookup(ctx, query).Scan(&attempt.ID, &attempt.Name, &attempt.Direction, &attempt.digest, &attempt.historyID,
		&attempt.historyChecksum, &attempt.HistoryCommitted, &attempt.searchPath, &attempt.executionRole,
		&attempt.executionSchemas, &attempt.executionBinding)
}

func (r *migrationOperation) queryMigrationLookup(ctx context.Context, query string, args ...any) Row {
	if r.uncachedRetryQueries {
		// A retry that reaches settings restoration discards its lock holder.
		options := make([]any, 1+len(args))
		options[0] = pgx.QueryExecModeExec
		copy(options[1:], args)
		args = options
	}
	return r.db.QueryRow(ctx, query, args...)
}

func (r *migrationOperation) startMigrationAttempt(ctx context.Context, migration plannedMigration, direction string) (*storedMigrationAttempt, error) {
	attempt := &storedMigrationAttempt{
		MigrationAttempt: MigrationAttempt{ID: rand.Text(), Name: migration.name, Direction: direction},
		digest:           migration.digest,
	}
	if direction == "DOWN" {
		id, checksum, err := r.originalMigrationHistory(ctx, migration.name)
		if err != nil {
			return nil, err
		}
		attempt.historyID, attempt.historyChecksum = &id, checksum
	}
	var plan migrationExecutionPlan
	if r.sessionBound {
		plan = planMigrationExecution(migration.sql)
	}
	if !r.metadata.attemptExists {
		query := "CREATE TABLE IF NOT EXISTS " + r.metadata.relation(migrationAttemptTable) + migrationAttemptTableDefinition
		if err := r.db.Exec(ctx, query); err != nil {
			return nil, fmt.Errorf("create migration reconciliation table: %w", err)
		}
		r.metadata.attemptExists = true
	}
	err := r.storeMigrationAttempt(ctx, attempt, plan)
	if migrationHasSQLState(err, "42703") {
		upgrade := `ALTER TABLE ` + r.metadata.relation(migrationAttemptTable) + ` ADD COLUMN IF NOT EXISTS execution_binding jsonb`
		if err := r.db.Exec(ctx, upgrade); err != nil {
			return nil, fmt.Errorf("upgrade migration reconciliation table: %w", err)
		}
		err = r.storeMigrationAttempt(ctx, attempt, plan)
	}
	if err != nil {
		return nil, fmt.Errorf("migration attempt %s was not confirmed before sending SQL: %w", attempt.ID, err)
	}
	return attempt, nil
}

const publicMigrationAttempt = `"public"."` + migrationAttemptTable + `"`

const insertMigrationAttemptPrefix = `
            INSERT INTO `
const insertMigrationAttemptSuffix = `
                (id, name, direction, digest, history_id, history_checksum,
                 execution_search_path, execution_role, execution_schemas, execution_binding)
            VALUES ($7, $8, $9, $10, $11, $12, pg_catalog.current_setting('search_path'),
                    ` + migrationExecutionRole + `, ` + migrationExecutionSchemas + `, ` + storeExecutionBinding + `)
        `
const insertMigrationAttempt = insertMigrationAttemptPrefix + publicMigrationAttempt + insertMigrationAttemptSuffix

const replaceMigrationAttemptPrefix = `
        WITH next AS (
            UPDATE `
const replaceMigrationAttemptSuffix = `
            SET id = $7, name = $8, direction = 'DOWN', digest = $10,
                history_id = $11, history_checksum = $12, history_committed = false,
                execution_search_path = pg_catalog.current_setting('search_path'),
                execution_role = ` + migrationExecutionRole + `, execution_schemas = ` + migrationExecutionSchemas + `,
                execution_binding = ` + storeExecutionBinding + `
            WHERE singleton AND id = $9 AND direction = 'DOWN' AND history_committed
            RETURNING 1
        ) SELECT 1 / count(*) FROM next
    `
const replaceMigrationAttempt = replaceMigrationAttemptPrefix + publicMigrationAttempt + replaceMigrationAttemptSuffix

func (r *migrationOperation) storeMigrationAttempt(ctx context.Context, attempt *storedMigrationAttempt, plan migrationExecutionPlan) error {
	query, prefix, suffix := insertMigrationAttempt, insertMigrationAttemptPrefix, insertMigrationAttemptSuffix
	directionOrReceipt := attempt.Direction
	if r.rollbackReceipt != "" {
		query, prefix, suffix = replaceMigrationAttempt, replaceMigrationAttemptPrefix, replaceMigrationAttemptSuffix
		directionOrReceipt = r.rollbackReceipt
	}
	if r.metadata.schema != "public" {
		query = prefix + r.metadata.relation(migrationAttemptTable) + suffix
	}
	var options []any
	if r.journalWritesUncached() {
		options = make([]any, 1, 13)
		options[0] = pgx.QueryExecModeExec
	}
	args := append(options, plan.targets[0].kind, plan.targets[0].name, plan.targets[1].kind,
		plan.targets[1].name, plan.tablespace, plan.tablespaceName, attempt.ID, attempt.Name,
		directionOrReceipt, attempt.digest, attempt.historyID, attempt.historyChecksum)
	return r.db.Exec(ctx, query, args...)
}

func (r *migrationOperation) originalMigrationHistory(ctx context.Context, name string) (int64, *string, error) {
	var id int64
	var checksum *string
	err := r.queryMigrationLookup(ctx, `SELECT id, checksum FROM `+r.metadata.relation(migrationHistoryTable)+` WHERE name = $1`, name).Scan(&id, &checksum)
	if migrationHasSQLState(err, "42703") {
		err = r.queryMigrationLookup(ctx, `SELECT id FROM `+r.metadata.relation(migrationHistoryTable)+` WHERE name = $1`, name).Scan(&id)
	}
	if err != nil {
		return 0, nil, fmt.Errorf("read rollback history identity: %w", err)
	}
	return id, checksum, nil
}

func (r *migrationOperation) runMigrationAttemptSQL(ctx context.Context, migration plannedMigration, attempt *storedMigrationAttempt) error {
	if err := execMigrationStatement(ctx, r.db, migration.sql); err != nil {
		if errors.Is(err, errMigrationStringMode) {
			return err
		}
		return fmt.Errorf("migration attempt %s: autocommit statement success is unconfirmed: %w", attempt.ID, err)
	}
	if migration.mode.index != "" {
		if err := r.verifyConcurrentIndex(ctx, migration.mode, 0); err != nil {
			return migrationHistoryUncommitted(true, err)
		}
	}
	return nil
}

func (r *migrationOperation) setMigrationAttemptCommitted(ctx context.Context, tx Tx, id string) error {
	statement := `
        WITH completed AS (
            UPDATE ` + r.metadata.relation(migrationAttemptTable) + ` SET history_committed = true
            WHERE singleton AND id = $1 AND NOT history_committed
            RETURNING 1
        ) SELECT 1 / count(*) FROM completed
    `
	if r.journalWritesUncached() {
		// This holder retires after its single journal write sequence.
		return tx.Exec(ctx, statement, pgx.QueryExecModeExec, id)
	}
	return tx.Exec(ctx, statement, id)
}

func (r *migrationOperation) acknowledgeMigrationAttempt(ctx context.Context, id string) error {
	statement := `
        DELETE FROM ` + r.metadata.relation(migrationAttemptTable) + ` WHERE singleton AND id = $1 AND history_committed
    `
	if r.journalWritesUncached() {
		return r.db.Exec(ctx, statement, pgx.QueryExecModeExec, id)
	}
	return r.db.Exec(ctx, statement, id)
}

// ReconciliationAction controls the resolution of a named migration attempt.
type ReconciliationAction uint8

const (
	// RecordApplied records the outcome after the operator verifies its effect, without reexecuting the migration SQL.
	RecordApplied ReconciliationAction = iota + 1
	// RetryMigration requests another execution of the exact persisted migration file.
	RetryMigration
	// AcknowledgeMigration clears the persisted receipt for an already committed attempt.
	AcknowledgeMigration
)

// PendingAttempt reads the unresolved migration attempt without changing the database.
func (r *Runner) PendingAttempt(ctx context.Context) (*MigrationAttempt, error) {
	operation, err := r.bindMetadata(ctx, false)
	if err != nil {
		return nil, err
	}
	attempt, err := operation.readMigrationAttempt(ctx, false)
	if err != nil || attempt == nil {
		return nil, err
	}
	return &attempt.MigrationAttempt, nil
}

// Reconcile uses a persisted attempt ID to resolve an interrupted standalone
// migration. The caller must check the affected objects before selecting an action.
// Custom database adapters must implement MigrationSessionDB.
func (r *Runner) Reconcile(ctx context.Context, id string, action ReconciliationAction) error {
	if id == "" {
		return errors.New("migration attempt ID is required")
	}
	if action != RecordApplied && action != RetryMigration && action != AcknowledgeMigration {
		return errors.New("invalid migration reconciliation action")
	}
	if !migrationSessionBound(r.db) {
		return errors.New("named migration reconciliation requires a lock-bound database session")
	}
	_, err := r.withMigrationLock(ctx, action != AcknowledgeMigration, func(operation *migrationOperation, ctx context.Context) ([]string, error) {
		if db, ok := r.db.(*PGDB); ok && action == RetryMigration {
			cfg := db.lockConfig
			operation.uncachedRetryQueries = cfg.DefaultQueryExecMode == pgx.QueryExecModeCacheStatement &&
				cfg.StatementCacheCapacity > 0
		}
		return nil, operation.reconcile(ctx, id, action)
	})
	return err
}

func (r *migrationOperation) reconcile(ctx context.Context, id string, action ReconciliationAction) error {
	attempt, err := r.readMigrationAttempt(ctx, action != AcknowledgeMigration)
	if err != nil {
		return err
	}
	if attempt == nil || attempt.ID != id {
		return fmt.Errorf("no suspended migration matches attempt %q", id)
	}
	if action == AcknowledgeMigration {
		if !attempt.HistoryCommitted {
			return errors.New("migration history is not yet committed")
		}
		return r.acknowledgeMigrationAttempt(ctx, id)
	}
	if attempt.HistoryCommitted {
		return errors.New("migration history already committed; acknowledge the recorded result")
	}
	migration, err := r.validateReconciliation(ctx, attempt)
	if err != nil {
		return err
	}
	binding, err := readMigrationBinding(attempt.executionBinding)
	if err != nil {
		return err
	}
	if action == RetryMigration {
		migration, err = r.prepareMigrationRetry(ctx, migration, attempt, binding)
		if err != nil {
			return err
		}
		if err := r.runMigrationAttemptSQL(ctx, migration, attempt); err != nil {
			return err
		}
	} else if err := r.verifyRecordedMigration(ctx, migration, attempt, binding); err != nil {
		return err
	}
	return r.finishMigrationAttempt(ctx, migration, attempt)
}

func (r *migrationOperation) applyMigrationExecutionSettings(ctx context.Context, attempt *storedMigrationAttempt, plan migrationExecutionPlan, binding migrationExecutionBinding) error {
	// A session change must go through Exec so the holder cannot be reused,
	// including when a safety check rejects the retry after restoration.
	var args []any
	if r.uncachedRetryQueries {
		args = make([]any, 1, 5)
		args[0] = pgx.QueryExecModeExec
	}
	args = append(args, attempt.searchPath, attempt.executionRole, binding.TablespaceValue, plan.tablespace == migrationDefaultTablespace)
	if err := r.db.Exec(ctx, `
        SELECT CASE WHEN `+migrationExecutionRole+` = $2
            THEN pg_catalog.set_config('search_path', $1, false)
        END, CASE WHEN $4::boolean AND `+migrationExecutionRole+` = $2
            THEN pg_catalog.set_config('default_tablespace', $3, false)
        END
    `, args...); err != nil {
		return fmt.Errorf("restore migration execution settings: %w", err)
	}
	return nil
}

func (r *migrationOperation) restoreMigrationExecution(ctx context.Context, attempt *storedMigrationAttempt, plan migrationExecutionPlan, binding migrationExecutionBinding) error {
	if err := r.applyMigrationExecutionSettings(ctx, attempt, plan, binding); err != nil {
		return err
	}
	var sameRole, sameSchemas bool
	err := r.db.QueryRow(ctx, `SELECT `+migrationExecutionRole+` = $1, `+migrationExecutionSchemas+` = $2`,
		attempt.executionRole, attempt.executionSchemas).Scan(&sameRole, &sameSchemas)
	if err != nil {
		return fmt.Errorf("verify migration execution namespace: %w", err)
	}
	return validateRestoredMigrationNamespace(sameRole, sameSchemas, plan.tempIndependent)
}

var errMigrationExecutionRoleChanged = errors.New("migration execution role changed; use the recorded effective database role")

func validateRestoredMigrationNamespace(sameRole, sameSchemas, tempIndependent bool) error {
	if !sameRole {
		return errMigrationExecutionRoleChanged
	}
	if !sameSchemas && !tempIndependent {
		return errors.New("migration execution schemas changed; inspect the recorded search path before retrying")
	}
	return nil
}

func (r *migrationOperation) verifyRecordedMigration(ctx context.Context, migration plannedMigration, attempt *storedMigrationAttempt, binding migrationExecutionBinding) error {
	target := binding.Targets[0].OID
	if binding.Version != 0 && migration.mode.index != "" && target != 0 {
		return r.verifyRecordedConcurrentIndex(ctx, migration.mode, target, attempt.executionRole)
	}
	if binding.Version == 0 {
		// Legacy guarded receipts lack a relation OID. Keep the original
		// namespace check before inspecting an index by its recorded name.
		if err := r.restoreMigrationExecution(ctx, attempt, migrationExecutionPlan{}, binding); err != nil {
			return err
		}
	} else {
		var sameRole bool
		if err := r.db.QueryRow(ctx, `SELECT `+migrationExecutionRole+` = $1`, attempt.executionRole).Scan(&sameRole); err != nil {
			return fmt.Errorf("verify migration execution role: %w", err)
		}
		if !sameRole {
			return errMigrationExecutionRoleChanged
		}
	}
	if migration.mode.index == "" {
		return nil
	}
	if binding.Version != 0 && target == 0 {
		return errors.New("no recorded relation for the index; inspect the migration result before recording it")
	}
	return r.verifyConcurrentIndex(ctx, migration.mode, target)
}

func (r *migrationOperation) verifyRecordedConcurrentIndex(ctx context.Context, mode migrationMode, target uint32, role string) error {
	var sameRole, valid bool
	err := r.db.QueryRow(ctx, `
		SELECT identity.same_role, CASE WHEN identity.same_role THEN `+concurrentIndexExists+` ELSE false END
		FROM (SELECT `+migrationExecutionRole+` = $4 AS same_role) identity
	`, mode.index, mode.table, target, role).Scan(&sameRole, &valid)
	if err == nil && !sameRole {
		return errMigrationExecutionRoleChanged
	}
	return concurrentIndexResult(mode, valid, err)
}

func (r *migrationOperation) validateReconciliation(ctx context.Context, attempt *storedMigrationAttempt) (plannedMigration, error) {
	if attempt.Name != filepath.Base(attempt.Name) {
		return plannedMigration{}, errors.New("invalid recorded migration filename")
	}
	migration, err := planMigration(filepath.Join(r.dir, attempt.Name), attempt.Direction, true, nil)
	if err != nil {
		return plannedMigration{}, fmt.Errorf("read recorded migration file: %w", err)
	}
	if !migration.mode.autocommit || migration.digest != attempt.digest {
		return plannedMigration{}, errors.New("migration file changed since the recorded attempt")
	}
	return migration, r.checkReconciliationHistory(ctx, attempt)
}

func (r *migrationOperation) checkReconciliationHistory(ctx context.Context, attempt *storedMigrationAttempt) error {
	if attempt.Direction == "DOWN" {
		id, checksum, err := r.originalMigrationHistory(ctx, attempt.Name)
		if err != nil {
			return err
		}
		if attempt.historyID == nil || id != *attempt.historyID || !sameMigrationChecksum(checksum, attempt.historyChecksum) {
			return errors.New("the history row changed since the recorded rollback attempt")
		}
		return nil
	}
	var exists bool
	err := r.queryMigrationLookup(ctx, `SELECT EXISTS (SELECT 1 FROM `+r.metadata.relation(migrationHistoryTable)+` WHERE name = $1)`, attempt.Name).Scan(&exists)
	if err != nil {
		return fmt.Errorf("read migration history before reconciliation: %w", err)
	}
	if exists {
		return errors.New("the migration history already contains this file")
	}
	return nil
}

func sameMigrationChecksum(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
