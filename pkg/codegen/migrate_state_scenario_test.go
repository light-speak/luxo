package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// ===== Scenario: First migration — empty DB, create all tables =====

func TestScenarioFirstMigration(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "user.luxo", Line: 1, Col: 1},
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "serial"}}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "varchar", Args: []*ast.NamedArg{{Value: &ast.Literal{Value: "100"}}}}}},
					{Name: "email", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "unique"}}},
				},
			}},
		}},
	}

	d := pgDialect()
	desired := ComputeState(result, nil, d)
	current := &SchemaState{Models: map[string]*ModelState{}}

	ops := Diff(current, desired)
	if len(ops) != 1 || ops[0].Kind != CreateTable {
		t.Fatalf("expected 1 CreateTable, got %d ops", len(ops))
	}

	up, down := GenerateMigrationSQL(ops, desired, d)

	// UP checks
	checks := []string{"CREATE TABLE users", "SERIAL", "PRIMARY KEY", "VARCHAR(100)", "UNIQUE", "created_at", "updated_at"}
	for _, c := range checks {
		if !strings.Contains(up, c) {
			t.Errorf("UP missing %q:\n%s", c, up)
		}
	}

	// DOWN checks
	if !strings.Contains(down, "DROP TABLE IF EXISTS users") {
		t.Errorf("DOWN missing DROP TABLE:\n%s", down)
	}

	// Name
	name := AutoMigrationName(ops)
	if name != "create_users" {
		t.Errorf("name = %q, want create_users", name)
	}
}

// ===== Scenario: Add column to existing table =====

func TestScenarioAddColumn(t *testing.T) {
	d := pgDialect()
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id":   {Type: "SERIAL", PK: true, Serial: true},
			"name": {Type: "TEXT"},
		}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id":     {Type: "SERIAL", PK: true, Serial: true},
			"name":   {Type: "TEXT"},
			"avatar": {Type: "TEXT", Nullable: true},
		}},
	}}

	ops := Diff(current, desired)
	up, down := GenerateMigrationSQL(ops, desired, d)

	if !strings.Contains(up, "ADD COLUMN avatar TEXT") {
		t.Errorf("UP missing ADD COLUMN:\n%s", up)
	}
	// Nullable → no NOT NULL
	if strings.Contains(up, "avatar TEXT NOT NULL") {
		t.Error("nullable column should not have NOT NULL")
	}
	if !strings.Contains(down, "DROP COLUMN avatar") {
		t.Errorf("DOWN missing DROP COLUMN:\n%s", down)
	}

	name := AutoMigrationName(ops)
	if name != "add_avatar_to_users" {
		t.Errorf("name = %q", name)
	}
}

// ===== Scenario: Drop column (dangerous, commented out) =====

func TestScenarioDropColumn(t *testing.T) {
	d := pgDialect()
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id":        {Type: "SERIAL", PK: true},
			"old_field": {Type: "TEXT"},
		}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id": {Type: "SERIAL", PK: true},
		}},
	}}

	ops := Diff(current, desired)
	up, _ := GenerateMigrationSQL(ops, desired, d)

	// Should be commented out
	if !strings.Contains(up, "-- ALTER TABLE users DROP COLUMN old_field") {
		t.Errorf("DROP COLUMN should be commented out:\n%s", up)
	}
}

// ===== Scenario: Change column type =====

func TestScenarioAlterColumnType(t *testing.T) {
	d := pgDialect()
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"name": {Type: "VARCHAR(50)"},
		}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"name": {Type: "TEXT"},
		}},
	}}

	ops := Diff(current, desired)
	up, down := GenerateMigrationSQL(ops, desired, d)

	if !strings.Contains(up, "ALTER COLUMN name TYPE TEXT") {
		t.Errorf("UP missing type change:\n%s", up)
	}
	if !strings.Contains(down, "ALTER COLUMN name TYPE VARCHAR(50)") {
		t.Errorf("DOWN missing reverse type change:\n%s", down)
	}
}

// ===== Scenario: Change nullable =====

func TestScenarioChangeNullable(t *testing.T) {
	d := pgDialect()
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"bio": {Type: "TEXT", Nullable: false},
		}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"bio": {Type: "TEXT", Nullable: true},
		}},
	}}

	ops := Diff(current, desired)
	up, down := GenerateMigrationSQL(ops, desired, d)

	if !strings.Contains(up, "DROP NOT NULL") {
		t.Errorf("UP should drop NOT NULL:\n%s", up)
	}
	if !strings.Contains(down, "SET NOT NULL") {
		t.Errorf("DOWN should set NOT NULL:\n%s", down)
	}
}

// ===== Scenario: Add unique constraint =====

func TestScenarioAddUnique(t *testing.T) {
	d := pgDialect()
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"email": {Type: "TEXT"},
		}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"email": {Type: "TEXT", Unique: true},
		}},
	}}

	ops := Diff(current, desired)
	up, down := GenerateMigrationSQL(ops, desired, d)

	if !strings.Contains(up, "ADD CONSTRAINT users_email_key UNIQUE") {
		t.Errorf("UP should add unique:\n%s", up)
	}
	if !strings.Contains(down, "DROP CONSTRAINT users_email_key") {
		t.Errorf("DOWN should drop unique:\n%s", down)
	}
}

// ===== Scenario: Remove unique constraint =====

func TestScenarioRemoveUnique(t *testing.T) {
	d := pgDialect()
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"email": {Type: "TEXT", Unique: true},
		}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"email": {Type: "TEXT", Unique: false},
		}},
	}}

	ops := Diff(current, desired)
	up, down := GenerateMigrationSQL(ops, desired, d)

	if !strings.Contains(up, "DROP CONSTRAINT users_email_key") {
		t.Errorf("UP should drop unique:\n%s", up)
	}
	if !strings.Contains(down, "ADD CONSTRAINT users_email_key UNIQUE") {
		t.Errorf("DOWN should add unique:\n%s", down)
	}
}

// ===== Scenario: Add and drop index =====

func TestScenarioIndexChanges(t *testing.T) {
	d := pgDialect()
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{}, Indexes: []string{"idx_users_name"}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{}, Indexes: []string{"idx_users_email"}},
	}}

	ops := Diff(current, desired)
	up, down := GenerateMigrationSQL(ops, desired, d)

	if !strings.Contains(up, "CREATE INDEX idx_users_email ON users (email)") {
		t.Errorf("UP should create index:\n%s", up)
	}
	if !strings.Contains(up, "DROP INDEX IF EXISTS idx_users_name") {
		t.Errorf("UP should drop old index:\n%s", up)
	}
	if !strings.Contains(down, "DROP INDEX IF EXISTS idx_users_email") {
		t.Errorf("DOWN should drop new index:\n%s", down)
	}
	if !strings.Contains(down, "CREATE INDEX idx_users_name ON users (name)") {
		t.Errorf("DOWN should recreate old index:\n%s", down)
	}
}

// ===== Scenario: Drop table (dangerous, commented out) =====

func TestScenarioDropTable(t *testing.T) {
	d := pgDialect()
	current := &SchemaState{Models: map[string]*ModelState{
		"OldModel": {Table: "old_models", Columns: map[string]*ColumnState{}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{}}

	ops := Diff(current, desired)
	up, _ := GenerateMigrationSQL(ops, desired, d)

	if !strings.Contains(up, "-- DROP TABLE IF EXISTS old_models") {
		t.Errorf("DROP TABLE should be commented out:\n%s", up)
	}
}

// ===== Scenario: Multiple changes at once =====

func TestScenarioMultipleChanges(t *testing.T) {
	d := pgDialect()
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id":   {Type: "SERIAL", PK: true},
			"name": {Type: "VARCHAR(50)"},
		}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id":     {Type: "SERIAL", PK: true},
			"name":   {Type: "TEXT"},                 // changed type
			"avatar": {Type: "TEXT", Nullable: true}, // new column
		}, Indexes: []string{"idx_users_name"}}, // new index
		"Post": {Table: "posts", Columns: map[string]*ColumnState{ // new table
			"id":    {Type: "SERIAL", PK: true},
			"title": {Type: "TEXT"},
		}},
	}}

	ops := Diff(current, desired)

	// Should have: CreateTable(Post) + AddColumn(avatar) + AlterColumn(name) + AddIndex
	kinds := map[DiffKind]int{}
	for _, op := range ops {
		kinds[op.Kind]++
	}
	if kinds[CreateTable] != 1 {
		t.Errorf("expected 1 CreateTable, got %d", kinds[CreateTable])
	}
	if kinds[AddColumn] != 1 {
		t.Errorf("expected 1 AddColumn, got %d", kinds[AddColumn])
	}
	if kinds[AlterColumn] != 1 {
		t.Errorf("expected 1 AlterColumn, got %d", kinds[AlterColumn])
	}
	if kinds[AddIndex] != 1 {
		t.Errorf("expected 1 AddIndex, got %d", kinds[AddIndex])
	}

	up, _ := GenerateMigrationSQL(ops, desired, d)
	if !strings.Contains(up, "CREATE TABLE posts") {
		t.Error("UP should create posts table")
	}
	if !strings.Contains(up, "ADD COLUMN avatar") {
		t.Error("UP should add avatar column")
	}
	if !strings.Contains(up, "ALTER COLUMN name TYPE TEXT") {
		t.Error("UP should alter name type")
	}
}

// ===== Scenario: Soft delete model =====

func TestScenarioSoftDeleteModel(t *testing.T) {
	d := pgDialect()
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "Post",
				Directives: []*ast.Directive{{Name: "soft"}},
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}},
					{Name: "title", Type: &ast.TypeRef{Name: "String"}},
				},
			}},
		}},
	}

	desired := ComputeState(result, nil, d)
	ms := desired.Models["Post"]

	if _, ok := ms.Columns["deleted_at"]; !ok {
		t.Error("@soft model should have deleted_at column")
	}
	if !ms.Columns["deleted_at"].Nullable {
		t.Error("deleted_at should be nullable")
	}
}

// ===== Scenario: @noTime model =====

func TestScenarioNoTimeModel(t *testing.T) {
	d := pgDialect()
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "Config",
				Directives: []*ast.Directive{{Name: "noTime"}},
				Fields: []*ast.FieldDecl{
					{Name: "key", Type: &ast.TypeRef{Name: "String"}},
					{Name: "value", Type: &ast.TypeRef{Name: "String"}},
				},
			}},
		}},
	}

	desired := ComputeState(result, nil, d)
	ms := desired.Models["Config"]

	if _, ok := ms.Columns["created_at"]; ok {
		t.Error("@noTime should not have created_at")
	}
	if _, ok := ms.Columns["updated_at"]; ok {
		t.Error("@noTime should not have updated_at")
	}
}

// ===== Scenario: Idempotent diff =====

func TestScenarioIdempotent(t *testing.T) {
	d := pgDialect()
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "serial"}}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
				},
			}},
		}},
	}

	state := ComputeState(result, nil, d)
	ops := Diff(state, state)

	if len(ops) != 0 {
		t.Errorf("diff of same state should be empty, got %d ops", len(ops))
	}
}
