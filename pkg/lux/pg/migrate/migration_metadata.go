package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const migrationHistoryTable = "_migrations"
const migrationAttemptTable = "_luxo_migration_attempt"
const publicMigrationHistory = "public._migrations"

var errMigrationStringMode = errors.New("PostgreSQL migrations require standard_conforming_strings=on")

type migrationHistoryStatement string

func (s migrationHistoryStatement) sql(history string) string {
	if history == publicMigrationHistory {
		return string(s)
	}
	return strings.Replace(string(s), publicMigrationHistory, history, 1)
}

const (
	createHistory migrationHistoryStatement = `
		CREATE TABLE IF NOT EXISTS ` + publicMigrationHistory + ` (
			id         SERIAL PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			checksum   TEXT,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`
	appliedHistory              migrationHistoryStatement = `SELECT name, applied_at FROM ` + publicMigrationHistory + ` ORDER BY id`
	checksumHistory             migrationHistoryStatement = `SELECT name, checksum FROM ` + publicMigrationHistory + ` WHERE checksum IS NOT NULL ORDER BY id`
	insertHistory               migrationHistoryStatement = `INSERT INTO ` + publicMigrationHistory + ` (name, checksum) VALUES ($1, $2)`
	deleteHistory               migrationHistoryStatement = `DELETE FROM ` + publicMigrationHistory + ` WHERE name = $1`
	rollbackHistoryWithChecksum migrationHistoryStatement = `SELECT name, checksum FROM ` + publicMigrationHistory + ` ORDER BY id DESC LIMIT $1`
	rollbackHistoryLegacy       migrationHistoryStatement = `SELECT name FROM ` + publicMigrationHistory + ` ORDER BY id DESC LIMIT $1`
)

// Bind both tables to the initial creation schema, before any user SQL can
// change the holder's search path. Other schemas cannot shadow either table.
const migrationMetadataQuery = `
    SELECT COALESCE(namespace.nspname::text, ''), history.oid IS NOT NULL, attempt.oid IS NOT NULL,
           (history.oid IS NULL OR (history.relkind IN ('r', 'p') AND history.relpersistence = 'p'))
           AND (attempt.oid IS NULL OR (attempt.relkind = 'r' AND attempt.relpersistence = 'p'))
           AND (namespace.oid IS NULL OR (namespace.oid <> pg_catalog.pg_my_temp_schema()
               AND NOT pg_catalog.pg_is_other_temp_schema(namespace.oid))),
           pg_catalog.current_setting('standard_conforming_strings') = 'on'
    FROM (SELECT pg_catalog.current_schema() AS schema) AS selected
    LEFT JOIN pg_catalog.pg_namespace namespace ON namespace.nspname = selected.schema
    LEFT JOIN pg_catalog.pg_class history ON history.relnamespace = namespace.oid
        AND history.relname = '_migrations'
    LEFT JOIN pg_catalog.pg_class attempt ON attempt.relnamespace = namespace.oid
        AND attempt.relname = '_luxo_migration_attempt'`

type migrationMetadata struct {
	schema        string
	historyExists bool
	attemptExists bool
}

func (r migrationOperation) bindMetadata(ctx context.Context, checkStringMode bool) (migrationOperation, error) {
	var persistent, standardStrings bool
	err := r.db.QueryRow(ctx, migrationMetadataQuery).Scan(
		&r.metadata.schema, &r.metadata.historyExists, &r.metadata.attemptExists, &persistent, &standardStrings)
	if err != nil {
		return r, fmt.Errorf("resolve PostgreSQL migration metadata: %w", err)
	}
	if checkStringMode && !standardStrings {
		return r, errMigrationStringMode
	}
	if !persistent {
		return r, errors.New("PostgreSQL migration metadata requires persistent tables")
	}
	return r, nil
}

func (m migrationMetadata) relation(table string) string {
	if m.schema == "public" && table == migrationHistoryTable {
		return publicMigrationHistory
	}
	if m.schema == "" {
		return table
	}
	return pgx.Identifier{m.schema, table}.Sanitize()
}
