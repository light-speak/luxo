package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type migrationBoundObject struct {
	OID  uint32 `json:"oid"`
	Name string `json:"name"`
}

type migrationExecutionBinding struct {
	Version          int                     `json:"version"`
	Targets          [2]migrationBoundObject `json:"targets"`
	PermanentSchemas string                  `json:"permanent_schemas"`
	Tablespace       uint32                  `json:"tablespace"`
	TablespaceValue  string                  `json:"tablespace_value"`
}

const migrationPermanentSchemas = `ARRAY(
  SELECT ns.oid
  FROM pg_catalog.unnest(pg_catalog.current_schemas(true)) WITH ORDINALITY AS path(name, pos)
  JOIN pg_catalog.pg_namespace ns ON ns.nspname = path.name
  WHERE ns.oid <> pg_catalog.pg_my_temp_schema()
  ORDER BY path.pos
)::text`

// Only the executing PostgreSQL session can resolve search_path, quoted names,
// physical object identities, and the effective index tablespace together.
const migrationBindingObjects = `WITH requested(slot, kind, name) AS (VALUES (1, $1::int, $2::text), (2, $3::int, $4::text)),
targets AS (
  SELECT slot, kind, CASE kind
    WHEN 1 THEN pg_catalog.to_regclass(name)::oid
    WHEN 2 THEN pg_catalog.to_regnamespace(name)::oid
    WHEN 3 THEN (SELECT oid FROM pg_catalog.pg_database WHERE datname =
      CASE WHEN name = '' THEN current_database() ELSE (pg_catalog.parse_ident(name))[1]::pg_catalog.name END)
  END AS oid FROM requested
),
bound AS (
  SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid', COALESCE(t.oid, 0)::bigint,
    'name', CASE WHEN t.oid IS NOT NULL THEN CASE t.kind
      WHEN 1 THEN pg_catalog.format('%I.%I', rel_ns.nspname, rel.relname)
      WHEN 2 THEN pg_catalog.quote_ident(ns.nspname)
      WHEN 3 THEN pg_catalog.quote_ident(db.datname)
    END END) ORDER BY t.slot) AS value
  FROM targets t
  LEFT JOIN pg_catalog.pg_class rel ON t.kind = 1 AND rel.oid = t.oid
  LEFT JOIN pg_catalog.pg_namespace rel_ns ON rel_ns.oid = rel.relnamespace
  LEFT JOIN pg_catalog.pg_namespace ns ON t.kind = 2 AND ns.oid = t.oid
  LEFT JOIN pg_catalog.pg_database db ON t.kind = 3 AND db.oid = t.oid
)`

const migrationBindingTablespaceSetting = `,
ts AS (
  SELECT pg_catalog.current_setting('default_tablespace') AS setting
)`

const migrationBindingTablespaceValue = `, 'tablespace_value', CASE WHEN $5::int = 1 THEN ts.setting ELSE '' END`

const migrationBindingFrom = `
)
FROM bound`

const migrationBindingTablespaceJoin = `
JOIN pg_catalog.pg_database db ON db.datname = current_database()
LEFT JOIN pg_catalog.pg_tablespace spc ON spc.spcname = CASE $5::int
  WHEN 1 THEN NULLIF(`

const migrationBindingTablespaceJoinEnd = `, '')::pg_catalog.name
  WHEN 2 THEN (pg_catalog.parse_ident($6::text))[1]::pg_catalog.name
END`

const migrationBindingFields = `
SELECT pg_catalog.jsonb_build_object(
  'version', 1, 'targets', bound.value, 'permanent_schemas', ` + migrationPermanentSchemas + `,
  'tablespace', (CASE $5::int
    WHEN 1 THEN COALESCE(spc.oid, db.dattablespace)
    WHEN 2 THEN COALESCE(spc.oid, 0)
    ELSE 0
  END)::bigint`

const queryExecutionBinding = migrationBindingObjects + migrationBindingTablespaceSetting +
	migrationBindingFields + migrationBindingTablespaceValue + migrationBindingFrom + ` CROSS JOIN ts` +
	migrationBindingTablespaceJoin + `ts.setting` + migrationBindingTablespaceJoinEnd

// Retry consumes the persisted setting before inspecting current identity. Its
// result needs no tablespace_value, leaving only one read of default_tablespace.
const queryRetryExecutionBinding = migrationBindingObjects + migrationBindingFields + migrationBindingFrom +
	migrationBindingTablespaceJoin + `pg_catalog.current_setting('default_tablespace')` + migrationBindingTablespaceJoinEnd

// The catalog and the intent share one PostgreSQL statement snapshot. This
// also keeps the binding in PostgreSQL when no reconciliation is needed.
const storeExecutionBinding = `CASE WHEN $1::int <> 0 THEN (` + queryExecutionBinding + `) END`

// Inspect identity only after the settings have been applied on a separate
// statement. The check and object lookup share the same catalog snapshot.
const readRestoredExecutionBinding = `
  SELECT identity.same_role, identity.same_schemas,
     CASE WHEN identity.same_role AND (identity.same_schemas OR $9::boolean)
        THEN (` + queryRetryExecutionBinding + `)::text ELSE '' END
  FROM (SELECT ` + migrationExecutionRole + ` = $7 AS same_role,
        ` + migrationExecutionSchemas + ` = $8 AS same_schemas) identity`

func (r *migrationOperation) inspectRestoredExecution(ctx context.Context, attempt *storedMigrationAttempt, plan migrationExecutionPlan) (string, error) {
	var encoded string
	var sameRole, sameSchemas bool
	var options []any
	if r.uncachedRetryQueries {
		// The retry holder retires after restoring its settings. Its one
		// catalog snapshot can use extended parameters without a cached statement.
		options = make([]any, 1, 10)
		options[0] = pgx.QueryExecModeExec
	}
	args := append(options, plan.targets[0].kind, plan.targets[0].name,
		plan.targets[1].kind, plan.targets[1].name, plan.tablespace, plan.tablespaceName,
		attempt.executionRole, attempt.executionSchemas, plan.tempIndependent)
	if err := r.db.QueryRow(ctx, readRestoredExecutionBinding, args...).Scan(&sameRole, &sameSchemas, &encoded); err != nil {
		return "", fmt.Errorf("bind standalone migration execution objects: %w", err)
	}
	return encoded, validateRestoredMigrationNamespace(sameRole, sameSchemas, plan.tempIndependent)
}

func readMigrationBinding(encoded string) (migrationExecutionBinding, error) {
	if encoded == "" {
		return migrationExecutionBinding{}, nil
	}
	if decoded, ok := readCanonicalMigrationBinding(encoded); ok {
		return decoded, nil
	}
	var binding migrationExecutionBinding
	if err := json.Unmarshal([]byte(encoded), &binding); err != nil {
		return binding, fmt.Errorf("decode recorded migration execution binding: %w", err)
	}
	if binding.Version != 1 {
		return binding, errors.New("unknown version of the migration execution binding")
	}
	return binding, nil
}

func (r *migrationOperation) prepareMigrationRetry(ctx context.Context, migration plannedMigration, attempt *storedMigrationAttempt, binding migrationExecutionBinding) (plannedMigration, error) {
	plan := planMigrationExecution(migration.sql)
	if binding.Version == 0 || plan.targets[0].kind == migrationNoObject {
		return migration, errors.New("no recorded migration object binding; verify the outcome before recording it")
	}
	if err := r.applyMigrationExecutionSettings(ctx, attempt, plan, binding); err != nil {
		return migration, err
	}
	encoded, err := r.inspectRestoredExecution(ctx, attempt, plan)
	if err != nil {
		return migration, err
	}
	current, err := readMigrationBinding(encoded)
	if err != nil {
		return migration, err
	}
	if current.PermanentSchemas != binding.PermanentSchemas {
		return migration, errors.New("migration execution schemas changed; inspect the recorded search path before retrying")
	}
	for i, target := range plan.targets {
		if current.Targets[i].OID != binding.Targets[i].OID {
			return migration, fmt.Errorf("migration execution object %q now resolves to a different identity", target.name)
		}
	}
	if current.Tablespace != binding.Tablespace {
		return migration, errors.New("migration execution tablespace changed; inspect the recorded settings before retrying")
	}
	migration.sql = qualifyMigrationObjects(migration.sql, plan, current)
	return migration, nil
}

func qualifyMigrationObjects(sql string, plan migrationExecutionPlan, binding migrationExecutionBinding) string {
	size := len(sql)
	var names [2]string
	for i, target := range plan.targets {
		name := binding.Targets[i].Name
		if target.end <= target.start || name == "" {
			continue
		}
		names[i] = name
		size += len(name) - (target.end - target.start)
	}
	if names == ([2]string{}) {
		return sql
	}
	var output strings.Builder
	output.Grow(size)
	offset := 0
	for i, name := range names {
		if name == "" {
			continue
		}
		target := plan.targets[i]
		output.WriteString(sql[offset:target.start])
		output.WriteString(name)
		offset = target.end
	}
	output.WriteString(sql[offset:])
	return output.String()
}
