package migrate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
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
	attempt, err := r.readMigrationAttempt(ctx)
	if err != nil || attempt == nil {
		return err
	}
	return &MigrationReconciliationError{Attempt: attempt.MigrationAttempt}
}

func (r *migrationOperation) readMigrationAttempt(ctx context.Context) (*storedMigrationAttempt, error) {
	if !r.metadata.attemptExists {
		return nil, nil
	}
	var attempt storedMigrationAttempt
	err := r.db.QueryRow(ctx, `
        SELECT id, name, direction, digest, history_id, history_checksum, history_committed,
               execution_search_path, execution_role, execution_schemas
        FROM `+r.metadata.relation(migrationAttemptTable)+` WHERE singleton
    `).Scan(&attempt.ID, &attempt.Name, &attempt.Direction, &attempt.digest, &attempt.historyID,
		&attempt.historyChecksum, &attempt.HistoryCommitted, &attempt.searchPath, &attempt.executionRole,
		&attempt.executionSchemas)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read migration reconciliation state: %w", err)
	}
	return &attempt, nil
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
	if !r.metadata.attemptExists {
		query := "CREATE TABLE IF NOT EXISTS " + r.metadata.relation(migrationAttemptTable) + migrationAttemptTableDefinition
		if err := r.db.Exec(ctx, query); err != nil {
			return nil, fmt.Errorf("create migration reconciliation table: %w", err)
		}
		r.metadata.attemptExists = true
	}
	err := r.storeMigrationAttempt(ctx, attempt)
	if err != nil {
		return nil, fmt.Errorf("migration attempt %s was not confirmed before sending SQL: %w", attempt.ID, err)
	}
	return attempt, nil
}

func (r *migrationOperation) storeMigrationAttempt(ctx context.Context, attempt *storedMigrationAttempt) error {
	if r.rollbackReceipt == "" {
		return r.db.Exec(ctx, `
            INSERT INTO `+r.metadata.relation(migrationAttemptTable)+`
                (id, name, direction, digest, history_id, history_checksum,
                 execution_search_path, execution_role, execution_schemas)
            VALUES ($1, $2, $3, $4, $5, $6, pg_catalog.current_setting('search_path'),
                    `+migrationExecutionRole+`, `+migrationExecutionSchemas+`)
        `, attempt.ID, attempt.Name, attempt.Direction, attempt.digest, attempt.historyID, attempt.historyChecksum)
	}
	return r.db.Exec(ctx, `
        WITH next AS (
            UPDATE `+r.metadata.relation(migrationAttemptTable)+`
            SET id = $1, name = $2, direction = 'DOWN', digest = $3,
                history_id = $4, history_checksum = $5, history_committed = false,
                execution_search_path = pg_catalog.current_setting('search_path'),
                execution_role = `+migrationExecutionRole+`, execution_schemas = `+migrationExecutionSchemas+`
            WHERE singleton AND id = $6 AND direction = 'DOWN' AND history_committed
            RETURNING 1
        ) SELECT 1 / count(*) FROM next
    `, attempt.ID, attempt.Name, attempt.digest, attempt.historyID, attempt.historyChecksum, r.rollbackReceipt)
}

func (r *migrationOperation) originalMigrationHistory(ctx context.Context, name string) (int64, *string, error) {
	var id int64
	var checksum *string
	err := r.db.QueryRow(ctx, `SELECT id, checksum FROM `+r.metadata.relation(migrationHistoryTable)+` WHERE name = $1`, name).Scan(&id, &checksum)
	if migrationHasSQLState(err, "42703") {
		err = r.db.QueryRow(ctx, `SELECT id FROM `+r.metadata.relation(migrationHistoryTable)+` WHERE name = $1`, name).Scan(&id)
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
		if err := r.verifyConcurrentIndex(ctx, migration.mode); err != nil {
			return migrationHistoryUncommitted(true, err)
		}
	}
	return nil
}

func (r *migrationOperation) setMigrationAttemptCommitted(ctx context.Context, tx Tx, id string) error {
	return tx.Exec(ctx, `
        WITH completed AS (
            UPDATE `+r.metadata.relation(migrationAttemptTable)+` SET history_committed = true
            WHERE singleton AND id = $1 AND NOT history_committed
            RETURNING 1
        ) SELECT 1 / count(*) FROM completed
    `, id)
}

func (r *migrationOperation) acknowledgeMigrationAttempt(ctx context.Context, id string) error {
	return r.db.Exec(ctx, `
        DELETE FROM `+r.metadata.relation(migrationAttemptTable)+` WHERE singleton AND id = $1 AND history_committed
    `, id)
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
	attempt, err := operation.readMigrationAttempt(ctx)
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
		return nil, operation.reconcile(ctx, id, action)
	})
	return err
}

func (r *migrationOperation) reconcile(ctx context.Context, id string, action ReconciliationAction) error {
	attempt, err := r.readMigrationAttempt(ctx)
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
	if err := r.restoreMigrationExecution(ctx, attempt); err != nil {
		return err
	}
	if action == RetryMigration {
		if err := r.runMigrationAttemptSQL(ctx, migration, attempt); err != nil {
			return err
		}
	} else if migration.mode.index != "" {
		if err := r.verifyConcurrentIndex(ctx, migration.mode); err != nil {
			return err
		}
	}
	return r.finishMigrationAttempt(ctx, migration, attempt)
}

func (r *migrationOperation) restoreMigrationExecution(ctx context.Context, attempt *storedMigrationAttempt) error {
	// A session change must go through Exec so the holder cannot be reused,
	// including when a safety check rejects the retry after restoration.
	if err := r.db.Exec(ctx, `
        SELECT CASE WHEN `+migrationExecutionRole+` = $2
            THEN pg_catalog.set_config('search_path', $1, false)
        END
    `, attempt.searchPath, attempt.executionRole); err != nil {
		return fmt.Errorf("restore migration execution search path: %w", err)
	}
	var sameRole, sameSchemas bool
	err := r.db.QueryRow(ctx, `SELECT `+migrationExecutionRole+` = $1, `+migrationExecutionSchemas+` = $2`,
		attempt.executionRole, attempt.executionSchemas).Scan(&sameRole, &sameSchemas)
	if err != nil {
		return fmt.Errorf("verify migration execution namespace: %w", err)
	}
	if !sameRole {
		return errors.New("migration execution role changed; use the recorded effective database role")
	}
	if !sameSchemas {
		return errors.New("migration execution schemas changed; inspect the recorded search path before retrying")
	}
	return nil
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
	err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+r.metadata.relation(migrationHistoryTable)+` WHERE name = $1)`, attempt.Name).Scan(&exists)
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
