package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux"
)

func TestMigrationSQLRejectsInvalidMetadata(t *testing.T) {
	column := &ColumnState{Type: "BIGINT"}
	for _, sample := range []struct {
		name string
		op   DiffOp
	}{
		{"table", DiffOp{Kind: DropTable, Table: strings.Repeat("a", 64)}},
		{"empty-table", DiffOp{Kind: DropTable}},
		{"column", DiffOp{Kind: AddColumn, Table: "users", Column: strings.Repeat("a", 64), NewColumn: column}},
		{"old-name", DiffOp{Kind: RenameColumn, Table: "users", Column: "name", OldName: strings.Repeat("a", 64)}},
		{"missing-new-column", DiffOp{Kind: AddColumn, Table: "users", Column: "name"}},
		{"missing-old-column", DiffOp{Kind: DropColumn, Table: "users", Column: "name"}},
		{"missing-alter-new", DiffOp{Kind: AlterColumn, Table: "users", Column: "name", OldColumn: column}},
		{"missing-alter-old", DiffOp{Kind: AlterColumn, Table: "users", Column: "name", NewColumn: column}},
		{"alter-primary-key", DiffOp{Kind: AlterColumn, Table: "users", Column: "id", OldColumn: column, NewColumn: &ColumnState{Type: "BIGINT", PK: true}}},
		{"rename-serial", DiffOp{Kind: RenameColumn, Table: "users", OldName: "id", Column: "key", OldColumn: column, NewColumn: &ColumnState{Type: "BIGINT", Serial: true}}},
		{"empty-type", DiffOp{Kind: AddColumn, Table: "users", Column: "name", NewColumn: &ColumnState{}}},
		{"missing-index", DiffOp{Kind: AddIndex, Table: "users"}},
		{"index-name", DiffOp{Kind: AddIndex, Table: "users", Index: &lux.IndexInfo{Name: strings.Repeat("a", 64), Columns: []string{"id"}}}},
		{"empty-index-columns", DiffOp{Kind: AddIndex, Table: "users", Index: &lux.IndexInfo{Name: "idx_users"}}},
		{"index-column", DiffOp{Kind: AddIndex, Table: "users", Index: &lux.IndexInfo{Name: "idx_users", Columns: []string{strings.Repeat("a", 64)}}}},
		{"index-kind", DiffOp{Kind: AddIndex, Table: "users", Index: &lux.IndexInfo{Name: "idx_users", Columns: []string{"id"}, Kind: 99}}},
		{"search-columns", DiffOp{Kind: AddIndex, Table: "users", Index: &lux.IndexInfo{Name: "idx_users", Columns: []string{"id", "name"}, Kind: lux.SearchIndex}}},
		{"unknown-operation", DiffOp{Kind: 99, Table: "users"}},
		{"missing-desired-model", DiffOp{Kind: CreateTable, Table: "users", Model: "User"}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			ops := []DiffOp{{Kind: DropTable, Table: "valid_before_error"}, sample.op}
			up, down, err := GenerateMigrationSQL(ops, nil, pgDialect())
			if err == nil || up != "" || down != "" {
				t.Fatal("invalid migration emitted partial SQL instead of an error", up, down, err)
			}
		})
	}
}

func TestMigrationSQLRejectsInvalidCreateState(t *testing.T) {
	for _, sample := range []struct {
		name   string
		mutate func(*ModelState)
	}{
		{"table-mismatch", func(m *ModelState) { m.Table = "different" }},
		{"nil-column", func(m *ModelState) { m.Columns["name"] = nil }},
		{"empty-type", func(m *ModelState) { m.Columns["name"] = &ColumnState{} }},
		{"invalid-column", func(m *ModelState) { m.Columns[strings.Repeat("a", 64)] = &ColumnState{Type: "TEXT"} }},
		{"invalid-index", func(m *ModelState) {
			m.Indexes = []lux.IndexInfo{{Name: "idx_users", Columns: []string{"name"}, Kind: 99}}
		}},
		{"missing-index-column", func(m *ModelState) { m.Indexes = []lux.IndexInfo{{Name: "idx_users", Columns: []string{"missing"}}} }},
		{"table-index-collision", func(m *ModelState) { m.Indexes = []lux.IndexInfo{{Name: "users", Columns: []string{"name"}}} }},
		{"unique-index-collision", func(m *ModelState) {
			m.Columns["name"].Unique = true
			m.Indexes = []lux.IndexInfo{{Name: "users_name_key", Columns: []string{"name"}}}
		}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			model := &ModelState{Table: "users", Columns: map[string]*ColumnState{"name": {Type: "TEXT"}}}
			sample.mutate(model)
			state := &SchemaState{Models: map[string]*ModelState{"User": model}}
			up, down, err := GenerateMigrationSQL([]DiffOp{{Kind: CreateTable, Model: "User", Table: "users"}}, state, pgDialect())
			if err == nil || up != "" || down != "" {
				t.Fatal("invalid desired state emitted SQL", up, down, err)
			}
		})
	}
}

func TestMigrationSQLEmptyAndMissingDialect(t *testing.T) {
	if up, down, err := GenerateMigrationSQL(nil, nil, nil); err != nil || up != "" || down != "" {
		t.Fatal("empty migration failed", up, down, err)
	}
	if up, down, err := GenerateMigrationSQL([]DiffOp{{Kind: DropTable, Table: "users"}}, nil, nil); err == nil || up != "" || down != "" {
		t.Fatal("missing dialect emitted SQL", up, down, err)
	}
}

func TestMigrationSQLCreateCatalogConflicts(t *testing.T) {
	user := &ModelState{Table: "users", Columns: map[string]*ColumnState{"name": {Type: "TEXT", Unique: true}}}
	desired := &SchemaState{Models: map[string]*ModelState{"User": user,
		"Other": {Table: "users_name_key", Columns: map[string]*ColumnState{"id": {Type: "BIGINT"}}}}}
	for _, ops := range [][]DiffOp{
		{{Kind: CreateTable, Model: "User", Table: "users"}, {Kind: CreateTable, Model: "User", Table: "users"}},
		{{Kind: CreateTable, Model: "Other", Table: "users_name_key"}, {Kind: CreateTable, Model: "User", Table: "users"}},
	} {
		if up, down, err := GenerateMigrationSQL(ops, desired, pgDialect()); err == nil || up != "" || down != "" {
			t.Fatal("conflicting catalog emitted SQL", up, down, err)
		}
	}
}

func TestMigrationSQLAddedColumnNamesUniqueConstraint(t *testing.T) {
	column := &ColumnState{Type: "TEXT", Unique: true}
	up, _, err := GenerateMigrationSQL([]DiffOp{{Kind: AddColumn, Table: "users", Column: "name", NewColumn: column}}, nil, pgDialect())
	if err != nil || !strings.Contains(up, `ADD CONSTRAINT "users_name_key" UNIQUE ("name")`) || !column.Unique {
		t.Fatal("new column lost its explicit constraint or changed metadata", up, err, column)
	}
}

func TestMigrationSQLRollbackReversesOperations(t *testing.T) {
	old := &ColumnState{Type: "TEXT"}
	newColumn := &ColumnState{Type: "VARCHAR(100)"}
	ops := []DiffOp{
		{Kind: RenameColumn, Table: "users", OldName: "name", Column: "display_name", OldColumn: old, NewColumn: newColumn},
		{Kind: AlterColumn, Table: "users", Column: "display_name", OldColumn: old, NewColumn: newColumn},
	}
	up, down, err := GenerateMigrationSQL(ops, nil, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(up, "RENAME COLUMN") > strings.Index(up, " TYPE ") ||
		strings.Index(down, " TYPE ") > strings.Index(down, "RENAME COLUMN") {
		t.Fatal("migration rollback did not reverse dependent operations", up, down)
	}
}

func TestMigrationSQLRenameKeepsUniqueConstraintIdentity(t *testing.T) {
	column := &ColumnState{Type: "TEXT", Unique: true}
	ops := []DiffOp{{Kind: RenameColumn, Table: "users", OldName: "name", Column: "display_name", OldColumn: column, NewColumn: column}}
	up, down, err := GenerateMigrationSQL(ops, nil, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(up, `RENAME CONSTRAINT "users_name_key" TO "users_display_name_key"`) ||
		!strings.Contains(down, `RENAME CONSTRAINT "users_display_name_key" TO "users_name_key"`) {
		t.Fatal("renamed unique column lost its migration constraint identity", up, down)
	}
}

func TestMigrationSQLTypeChangeRemovesObsoleteConstraintsFirst(t *testing.T) {
	ops := []DiffOp{{Kind: AlterColumn, Table: "users", Column: "value",
		OldColumn: &ColumnState{Type: "TEXT", Unique: true, Default: "'17'::text"},
		NewColumn: &ColumnState{Type: "BIGINT", Default: "42"}}}
	up, down, err := GenerateMigrationSQL(ops, nil, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	for _, preceding := range []string{"DROP CONSTRAINT", "DROP DEFAULT"} {
		if at := strings.Index(up, preceding); at < 0 || at > strings.Index(up, " TYPE ") {
			t.Fatal("obsolete definition prevents type conversion", up)
		}
	}
	if strings.Index(down, "DROP DEFAULT") > strings.Index(down, " TYPE ") ||
		strings.Index(down, " TYPE ") > strings.Index(down, "ADD CONSTRAINT") {
		t.Fatal("rollback restores a constraint before restoring the type", down)
	}
}

func TestMigrationSQLIndexRollbackUsesLiveColumnNames(t *testing.T) {
	old := &SchemaState{Models: map[string]*ModelState{"User": {Table: "users", Columns: map[string]*ColumnState{"name": {Type: "TEXT"}},
		Indexes: []lux.IndexInfo{{Name: "idx_users_name", Columns: []string{"name"}}}}}}
	next := &SchemaState{Models: map[string]*ModelState{"User": {Table: "users", Columns: map[string]*ColumnState{"display_name": {Type: "TEXT"}},
		Indexes: []lux.IndexInfo{{Name: "idx_users_display_name", Columns: []string{"display_name"}}}}}}
	ops := mustMigrationDiff(t, old, next, map[string]*FieldIDMap{"User": {OldByID: map[int]string{1: "name"}, NewByID: map[int]string{1: "display_name"}}})
	up, down, err := GenerateMigrationSQL(ops, next, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(up, "DROP INDEX") > strings.Index(up, "RENAME COLUMN") ||
		strings.Index(down, "RENAME COLUMN") > strings.Index(down, "CREATE INDEX") {
		t.Fatal("index recreation refers to a column before it is restored", up, down)
	}
}

func TestMigrationSQLRebuildsChangedIndexDefinition(t *testing.T) {
	old := &SchemaState{Models: map[string]*ModelState{"User": {Table: "users", Columns: map[string]*ColumnState{"name": {Type: "TEXT"}, "role": {Type: "TEXT"}},
		Indexes: []lux.IndexInfo{{Name: "idx_users_lookup", Columns: []string{"name"}}}}}}
	next := &SchemaState{Models: map[string]*ModelState{"User": {Table: "users", Columns: old.Models["User"].Columns,
		Indexes: []lux.IndexInfo{{Name: "idx_users_lookup", Kind: lux.UniqueIndex, Columns: []string{"role"}}}}}}
	up, down, err := GenerateMigrationSQL(mustMigrationDiff(t, old, next, nil), next, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(up, `CREATE UNIQUE INDEX "idx_users_lookup" ON "users" ("role")`) ||
		!strings.Contains(down, `CREATE INDEX "idx_users_lookup" ON "users" ("name")`) {
		t.Fatal("same index name hid a changed definition", up, down)
	}
}
