package migrate

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Frozen D lookup preserves an independent reference for the consumed state.
const originalRetryReadProjectionSQL = `
  SELECT identity.same_role, identity.same_schemas,
     CASE WHEN identity.same_role AND (identity.same_schemas OR $9::boolean)
        THEN (WITH requested(slot, kind, name) AS (VALUES (1, $1::int, $2::text), (2, $3::int, $4::text)),
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
),
ts AS (
  SELECT pg_catalog.current_setting('default_tablespace') AS setting
)
SELECT pg_catalog.jsonb_build_object(
  'version', 1, 'targets', bound.value, 'permanent_schemas', ARRAY(
  SELECT ns.oid
  FROM pg_catalog.unnest(pg_catalog.current_schemas(true)) WITH ORDINALITY AS path(name, pos)
  JOIN pg_catalog.pg_namespace ns ON ns.nspname = path.name
  WHERE ns.oid <> pg_catalog.pg_my_temp_schema()
  ORDER BY path.pos
)::text,
  'tablespace', (CASE $5::int
    WHEN 1 THEN COALESCE(spc.oid, db.dattablespace)
    WHEN 2 THEN COALESCE(spc.oid, 0)
    ELSE 0
  END)::bigint, 'tablespace_value', CASE WHEN $5::int = 1 THEN ts.setting ELSE '' END
)
FROM bound CROSS JOIN ts
JOIN pg_catalog.pg_database db ON db.datname = current_database()
LEFT JOIN pg_catalog.pg_tablespace spc ON spc.spcname = CASE $5::int
  WHEN 1 THEN NULLIF(ts.setting, '')::pg_catalog.name
  WHEN 2 THEN (pg_catalog.parse_ident($6::text))[1]::pg_catalog.name
END)::text ELSE '' END
  FROM (SELECT pg_catalog.to_regrole(pg_catalog.quote_ident(current_user))::oid::text = $7 AS same_role,
        ARRAY(
    SELECT namespace.oid
    FROM pg_catalog.unnest(pg_catalog.current_schemas(true)) WITH ORDINALITY AS resolved(name, position)
    JOIN pg_catalog.pg_namespace namespace ON namespace.nspname = resolved.name
    ORDER BY resolved.position
)::text = $8 AS same_schemas) identity`

func readProjectionArguments(t *testing.T, db DB) []any {
	t.Helper()
	var role, schemas string
	if err := db.QueryRow(t.Context(), "SELECT "+migrationExecutionRole+", "+migrationExecutionSchemas).Scan(&role, &schemas); err != nil {
		t.Fatal(err)
	}
	return []any{migrationRelation, "audit_users", migrationNoObject, "", migrationDefaultTablespace, "", role, schemas, true}
}

func TestIntegrationRetryReadProjectionOnlyConsumesNeededState(t *testing.T) {
	f := migrationAuditRunner(t)
	db := f.openBoundSession(t)
	space := os.Getenv("LUXO_TEST_MIGRATION_TABLESPACE")
	if space == "" {
		space = "pg_default"
	}
	auditExec(t, db, "SET default_tablespace TO "+pgx.Identifier{space}.Sanitize())
	args := readProjectionArguments(t, db)
	var storedText string
	if err := db.QueryRow(t.Context(), "SELECT ("+storeExecutionBinding+")::text", args[:6]...).Scan(&storedText); err != nil {
		t.Fatal(err)
	}
	stored, err := readMigrationBinding(storedText)
	if err != nil || stored.TablespaceValue != space {
		t.Fatalf("receipt lost restoration setting: %+v %v", stored, err)
	}
	var sameRole, sameSchemas bool
	var currentText string
	if err := db.QueryRow(t.Context(), readRestoredExecutionBinding, args...).Scan(&sameRole, &sameSchemas, &currentText); err != nil {
		t.Fatal(err)
	}
	if !sameRole || !sameSchemas {
		t.Fatal("identity guard changed")
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal([]byte(currentText), &members); err != nil {
		t.Fatal(err)
	}
	if _, present := members["tablespace_value"]; present {
		t.Fatal("live lookup still serializes the setting consumed from the stored receipt")
	}
	current, err := readMigrationBinding(currentText)
	if err != nil {
		t.Fatal(err)
	}
	stored.TablespaceValue = ""
	if current != stored {
		t.Fatalf("consumed identity changed: %+v %+v", stored, current)
	}
	if len(currentText) >= len(storedText) {
		t.Fatal("projection did not remove unused returned bytes")
	}
	if _, fast := readCanonicalMigrationBinding(currentText); !fast {
		t.Fatal("projected ASCII lookup fell back to general JSON decoding")
	}
}

func TestIntegrationRetryReadProjectionMatchesD(t *testing.T) {
	f := migrationAuditRunner(t)
	db := f.openBoundSession(t)
	for _, sample := range []struct {
		name                       string
		role, schemas, independent bool
	}{{"match", true, true, false}, {"temporary-independent", true, false, true}, {"role-rejection", false, true, true}, {"schema-rejection", true, false, false}} {
		t.Run(sample.name, func(t *testing.T) {
			args := readProjectionArguments(t, db)
			args[8] = sample.independent
			if !sample.role {
				args[6] = "0"
			}
			if !sample.schemas {
				args[7] = "{0}"
			}
			var oldRole, oldSchemas, newRole, newSchemas bool
			var oldText, newText string
			query := "SELECT old.role,old.schemas,old.binding,new.role,new.schemas,new.binding FROM (" + originalRetryReadProjectionSQL + ") AS old(role,schemas,binding) CROSS JOIN (" + readRestoredExecutionBinding + ") AS new(role,schemas,binding)"
			if err := db.QueryRow(t.Context(), query, args...).Scan(&oldRole, &oldSchemas, &oldText, &newRole, &newSchemas, &newText); err != nil {
				t.Fatal(err)
			}
			if oldRole != newRole || oldSchemas != newSchemas || (oldText == "") != (newText == "") {
				t.Fatal("guard result changed")
			}
			if oldText == "" {
				return
			}
			old, err := readMigrationBinding(oldText)
			if err != nil {
				t.Fatal(err)
			}
			current, err := readMigrationBinding(newText)
			if err != nil {
				t.Fatal(err)
			}
			old.TablespaceValue = ""
			current.TablespaceValue = ""
			if old != current {
				t.Fatalf("consumed state differs: %+v %+v", old, current)
			}
		})
	}
	// Escaped identifiers retain the existing legacy fallback and exact data.
	quoted := "quoted " + f.schema + "例"
	auditExec(t, db, "CREATE TABLE "+pgx.Identifier{quoted}.Sanitize()+" (id int)")
	args := readProjectionArguments(t, db)
	args[1] = pgx.Identifier{quoted}.Sanitize()
	var role, schemas bool
	var text string
	if err := db.QueryRow(t.Context(), readRestoredExecutionBinding, args...).Scan(&role, &schemas, &text); err != nil {
		t.Fatal(err)
	}
	result, err := readMigrationBinding(text)
	if err != nil || !strings.Contains(result.Targets[0].Name, "例") {
		t.Fatalf("quoted identity changed: %+v %v", result, err)
	}
}

var readProjectionCostSink migrationExecutionBinding

func TestReadProjectionQueryCost(t *testing.T) {
	f := migrationAuditRunner(t)
	db := f.openBoundSession(t)
	auditExec(t, db, "CREATE TABLE public.audit_target (value int)")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.Exec(ctx, "DROP TABLE public.audit_target"); err != nil {
			t.Error(err)
		}
	})
	args := readProjectionArguments(t, db)
	args[1] = "public.audit_target"
	parameters := append([]any{pgx.QueryExecModeExec}, args...)
	type result struct {
		bytes  uint64
		allocs float64
	}
	got := map[string]result{}
	for _, sample := range []struct{ name, sql string }{{"D", originalRetryReadProjectionSQL}, {"scoped", readRestoredExecutionBinding}} {
		call := func() {
			var role, schemas bool
			var encoded string
			if err := db.QueryRow(t.Context(), sample.sql, parameters...).Scan(&role, &schemas, &encoded); err != nil || !role || !schemas {
				t.Fatalf("lookup failed: %v", err)
			}
			value, err := readMigrationBinding(encoded)
			if err != nil {
				t.Fatal(err)
			}
			readProjectionCostSink = value
		}
		allocations := testing.AllocsPerRun(30, call)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range 100 {
			call()
		}
		runtime.ReadMemStats(&after)
		bytes := (after.TotalAlloc - before.TotalAlloc) / 100
		got[sample.name] = result{bytes, allocations}
		t.Logf("READ_PROJECTION_COST arm=%s SQL=%d B/op=%d allocs/op=%.0f", sample.name, len(sample.sql), bytes, allocations)
	}
	if got["scoped"].bytes >= got["D"].bytes || got["scoped"].allocs > got["D"].allocs {
		t.Fatalf("projection did not improve query allocations without a tradeoff: %+v", got)
	}
}
