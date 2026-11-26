package migrate

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPlanReindexTransactionMode(t *testing.T) {
	// PostgreSQL 16 utility options accept boolean words and integer 0/1.
	cases := []struct {
		name, statement string
		autocommit      bool
	}{
		{"quoted option", `REINDEX ("concurrently" TRUE) INDEX notes_idx`, true},
		{"uppercase quoted option stays distinct", `REINDEX ("CONCURRENTLY" TRUE) INDEX notes_idx`, false},
		{"unicode option", `REINDEX (U&"concurr\0065ntly" TRUE) INDEX notes_idx`, true},
		{"custom unicode option", `REINDEX (U&"concurr@0065ntly" UESCAPE '@') INDEX notes_idx`, true},
		{"explicit option without value", `REINDEX (CONCURRENTLY, VERBOSE) INDEX notes_idx`, true},
		{"dollar true", `REINDEX (CONCURRENTLY $switch$ON$switch$) INDEX notes_idx`, true},
		{"dollar false", `REINDEX (CONCURRENTLY $$false$$) INDEX notes_idx`, false},
		{"escape true", `REINDEX (CONCURRENTLY E't\162ue') INDEX notes_idx`, true},
		{"escaped uppercase hex", `REINDEX (CONCURRENTLY E'\x6FN') INDEX notes_idx`, true},
		{"escaped plain character", `REINDEX (CONCURRENTLY E'trU\e') INDEX notes_idx`, true},
		{"unicode true", `REINDEX (CONCURRENTLY U&'t@0072ue' UESCAPE '@') INDEX notes_idx`, true},
		{"unicode escape true", `REINDEX (CONCURRENTLY E't\u0072ue') INDEX notes_idx`, true},
		{"long unicode escape true", `REINDEX (CONCURRENTLY E't\U00000072ue') INDEX notes_idx`, true},
		{"hex one", `REINDEX (CONCURRENTLY 0x1) INDEX notes_idx`, true},
		{"adjacent true", "REINDEX (CONCURRENTLY 't'\n'rue') INDEX notes_idx", true},
		{"quoted zero is no boolean option", `REINDEX (CONCURRENTLY '0') INDEX notes_idx`, false},
		{"short boolean is no option", `REINDEX (CONCURRENTLY 't') INDEX notes_idx`, false},
		{"float one is no option", `REINDEX (CONCURRENTLY 1.0) INDEX notes_idx`, false},
		{"tagged dollar off", `REINDEX (CONCURRENTLY $switch$ofF$switch$) TABLE notes`, false},
		{"quoted false", `REINDEX (CONCURRENTLY 'False') INDEX notes_idx`, false},
		{"identifier false", `REINDEX (CONCURRENTLY "False") INDEX notes_idx`, false},
		{"escape false", `REINDEX (CONCURRENTLY E'fal\x73e') INDEX notes_idx`, false},
		{"escape octal", `REINDEX (CONCURRENTLY E'fa\154se') INDEX notes_idx`, false},
		{"escape short hex", `REINDEX (CONCURRENTLY E'false\x0') INDEX notes_idx`, false},
		{"unicode false", `REINDEX (CONCURRENTLY U&'f@0061lse' UESCAPE '@') INDEX notes_idx`, false},
		{"unicode identifier false", `REINDEX (CONCURRENTLY U&"f\+000061lse") INDEX notes_idx`, false},
		{"adjacent lines", "REINDEX (CONCURRENTLY 'fal'\n'se') INDEX notes_idx", false},
		{"adjacent lines with comment", "REINDEX (CONCURRENTLY 'fal' -- between parts\n'se') INDEX notes_idx", false},
		{"adjacent escape lines", "REINDEX (CONCURRENTLY E'fal'\n'\\x73e') INDEX notes_idx", false},
		{"integer zero", `REINDEX (CONCURRENTLY 0) INDEX notes_idx`, false},
		{"leading zero", `REINDEX (CONCURRENTLY 00) INDEX notes_idx`, false},
		{"hex zero", `REINDEX (CONCURRENTLY 0x0) INDEX notes_idx`, false},
		{"octal zero", `REINDEX (CONCURRENTLY 0o0) INDEX notes_idx`, false},
		{"binary zero", `REINDEX (CONCURRENTLY 0b0) INDEX notes_idx`, false},
		{"underscored zero", `REINDEX (CONCURRENTLY 0_0) INDEX notes_idx`, false},
		{"signed zero", `REINDEX (CONCURRENTLY - /* sign */ 00) INDEX notes_idx`, false},
		{"signed one", `REINDEX (CONCURRENTLY + 01) INDEX notes_idx`, true},
		{"last false", `REINDEX (CONCURRENTLY TRUE, "concurrently" OFF) INDEX notes_idx`, false},
		{"last true", `REINDEX (CONCURRENTLY OFF, "concurrently" TRUE) INDEX notes_idx`, true},
		{"keyword overrides option", `REINDEX (CONCURRENTLY FALSE) INDEX CONCURRENTLY notes_idx`, true},
		{"option value is not an option name", `REINDEX (TABLESPACE "concurrently") TABLE notes`, false},
		{"schema false", `REINDEX (CONCURRENTLY FALSE) SCHEMA public`, true},
		{"database", `REINDEX DATABASE`, true},
		{"system", `REINDEX SYSTEM`, true},
		{"qualified keyword relation", `REINDEX INDEX concurrently.notes_idx`, false},
		{"plain table", `REINDEX TABLE public.notes`, false},
		{"unknown target", `REINDEX MATERIALIZED VIEW notes`, false},
		{"dangling sign", `REINDEX (CONCURRENTLY +) INDEX notes_idx`, false},
		{"adjacent numeric separators", `REINDEX (CONCURRENTLY 0__1) INDEX notes_idx`, false},
		{"trailing numeric separator", `REINDEX (CONCURRENTLY 1_) INDEX notes_idx`, false},
		{"dollar parameter", `REINDEX (CONCURRENTLY $1) INDEX notes_idx`, false},
		{"unclosed quoted option", `REINDEX ("concurrently`, false},
		{"unclosed unicode option", `REINDEX (U&"concurrently`, false},
		{"invalid unicode escape token", `REINDEX (CONCURRENTLY U&'true' UESCAPE @) INDEX notes_idx`, false},
		{"invalid unicode escape digit", `REINDEX (CONCURRENTLY U&'true' UESCAPE '1') INDEX notes_idx`, false},
		{"incomplete unicode escape", `REINDEX (CONCURRENTLY U&'true\') INDEX notes_idx`, false},
		{"doubled custom unicode escape", `REINDEX (CONCURRENTLY U&'t@@rue' UESCAPE '@') INDEX notes_idx`, false},
		{"escaped control character", `REINDEX (CONCURRENTLY E'tr\rue') INDEX notes_idx`, false},
		{"escape hex followed by nonhex", `REINDEX (CONCURRENTLY E'tr\x6ue') INDEX notes_idx`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := planMigrationSQL(tc.statement + ";")
			if err != nil || mode.autocommit != tc.autocommit {
				t.Fatalf("plan mode = %+v, %v, want autocommit=%v", mode, err, tc.autocommit)
			}
			_, err = planMigrationSQL(tc.statement + "; SELECT 1;")
			if errors.Is(err, errSplitMigration) != tc.autocommit || (err != nil && !errors.Is(err, errSplitMigration)) {
				t.Fatalf("mixed plan error = %v, want split=%v", err, tc.autocommit)
			}
		})
	}
}

func TestIntegrationReindexMigrationTransactionMode(t *testing.T) {
	ctx, admin := databaseTestAdmin(t)
	schema := "luxo_reindex_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	scope := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+scope); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(dropCtx, "DROP SCHEMA "+scope+" CASCADE"); err != nil {
			t.Error("drop test schema", err)
			return
		}
		var found bool
		if err := admin.QueryRow(dropCtx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", schema).Scan(&found); err != nil || found {
			t.Errorf("test schema cleanup: found=%v err=%v", found, err)
		}
	})
	connURL, err := url.Parse(BuildDatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	connURL.Path = "/postgres"
	params := connURL.Query()
	params.Set("search_path", schema)
	connURL.RawQuery = params.Encode()
	db, err := NewPGDB(ctx, connURL.String())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	runner := NewFromDB(db, dir)
	t.Cleanup(runner.Close)
	if err := db.Exec(ctx, "CREATE TABLE items(id int); CREATE INDEX items_idx ON items(id);"); err != nil {
		t.Fatal(err)
	}
	const quoted = `REINDEX ("concurrently" TRUE) INDEX items_idx;`
	const transactional = "REINDEX (CONCURRENTLY $$false$$) INDEX items_idx;"
	schemaReindex := "REINDEX (CONCURRENTLY FALSE) SCHEMA " + scope + ";"
	writeMigration(t, dir, "001_index.sql", quoted, quoted)
	writeMigration(t, dir, "002_false.sql", transactional+" INSERT INTO items VALUES (1);", "DELETE FROM items; "+transactional)
	writeMigration(t, dir, "003_schema.sql", schemaReindex, schemaReindex)
	const mixed = "004_mixed.sql"
	writeMigration(t, dir, mixed, schemaReindex+" SELECT 1;", "SELECT 1;")
	if _, _, err := runner.DryRun(ctx); !errors.Is(err, errSplitMigration) || !strings.Contains(err.Error(), mixed) {
		t.Fatalf("dry run should reject mixed schema reindex: %v", err)
	}
	if applied, err := runner.Up(ctx); len(applied) != 0 || !errors.Is(err, errSplitMigration) {
		t.Fatalf("mixed plan applied migrations: %v %v", applied, err)
	}
	var history bool
	if err := db.QueryRow(ctx, "SELECT to_regclass('_migrations') IS NOT NULL").Scan(&history); err != nil || history {
		t.Fatalf("rejected plan changed history: %v %v", history, err)
	}
	if err := os.Remove(filepath.Join(dir, mixed)); err != nil {
		t.Fatal(err)
	}
	if names, _, err := runner.DryRun(ctx); err != nil || len(names) != 3 {
		t.Fatalf("unexpected dry run: %v %v", names, err)
	}
	if applied, err := runner.Up(ctx); err != nil || len(applied) != 3 {
		t.Fatalf("apply: %v %v", applied, err)
	}
	var written int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM items WHERE id = 1").Scan(&written); err != nil || written != 1 {
		t.Fatalf("transactional file did not apply: %v %v", written, err)
	}
	if rolled, err := runner.Down(ctx, 3); err != nil || len(rolled) != 3 {
		t.Fatalf("rollback: %v %v", rolled, err)
	}
}
