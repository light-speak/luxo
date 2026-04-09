package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/pg"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func pgDialect() lux.Dialect { return pg.Dialect{} }

func mkPos() token.Position {
	return token.Position{File: "test.luxo", Line: 1, Col: 1}
}

func TestLoadStateNotFound(t *testing.T) {
	s, err := LoadState("/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Models) != 0 {
		t.Error("should be empty")
	}
}

func TestSaveAndLoadState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".state.json")

	s := &SchemaState{Models: map[string]*ModelState{
		"User": {
			Table:   "users",
			Columns: map[string]*ColumnState{"id": {Type: "SERIAL", PK: true}},
		},
	}}

	if err := SaveState(path, s); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Models["User"].Table != "users" {
		t.Error("table name mismatch")
	}
	if loaded.Models["User"].Columns["id"].Type != "SERIAL" {
		t.Error("column type mismatch")
	}
}

func TestLoadStateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".state.json")
	os.WriteFile(path, []byte("bad"), 0644)

	_, err := LoadState(path)
	if err == nil {
		t.Fatal("should error on invalid JSON")
	}
}

func TestComputeState(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  mkPos(),
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "serial"}}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "email", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "unique"}}},
					{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}}, // relation
				},
			}},
		}},
	}

	s := ComputeState(result, nil, pgDialect())
	ms := s.Models["User"]
	if ms == nil {
		t.Fatal("User model missing")
	}
	if ms.Table != "users" {
		t.Errorf("table = %q", ms.Table)
	}
	if _, ok := ms.Columns["posts"]; ok {
		t.Error("relation field should not be a column")
	}
	if ms.Columns["id"].Serial != true {
		t.Error("id should be serial")
	}
	if ms.Columns["email"].Unique != true {
		t.Error("email should be unique")
	}
	if _, ok := ms.Columns["created_at"]; !ok {
		t.Error("should auto-add created_at")
	}
}

func TestDiffCreateTable(t *testing.T) {
	current := &SchemaState{Models: map[string]*ModelState{}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id": {Type: "SERIAL", PK: true},
		}},
	}}

	ops := Diff(current, desired)
	if len(ops) != 1 || ops[0].Kind != CreateTable {
		t.Errorf("expected CreateTable, got %v", ops)
	}
}

func TestDiffDropTable(t *testing.T) {
	current := &SchemaState{Models: map[string]*ModelState{
		"Old": {Table: "olds", Columns: map[string]*ColumnState{}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{}}

	ops := Diff(current, desired)
	if len(ops) != 1 || ops[0].Kind != DropTable {
		t.Errorf("expected DropTable, got %v", ops)
	}
}

func TestDiffAddColumn(t *testing.T) {
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id": {Type: "SERIAL"},
		}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id":   {Type: "SERIAL"},
			"name": {Type: "TEXT"},
		}},
	}}

	ops := Diff(current, desired)
	found := false
	for _, op := range ops {
		if op.Kind == AddColumn && op.Column == "name" {
			found = true
		}
	}
	if !found {
		t.Error("expected AddColumn for name")
	}
}

func TestDiffDropColumn(t *testing.T) {
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id":  {Type: "SERIAL"},
			"old": {Type: "TEXT"},
		}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id": {Type: "SERIAL"},
		}},
	}}

	ops := Diff(current, desired)
	found := false
	for _, op := range ops {
		if op.Kind == DropColumn && op.Column == "old" {
			found = true
		}
	}
	if !found {
		t.Error("expected DropColumn for old")
	}
}

func TestDiffAlterColumn(t *testing.T) {
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
	found := false
	for _, op := range ops {
		if op.Kind == AlterColumn && op.Column == "name" {
			found = true
		}
	}
	if !found {
		t.Error("expected AlterColumn for name type change")
	}
}

func TestDiffNoChanges(t *testing.T) {
	s := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id": {Type: "SERIAL"},
		}},
	}}
	ops := Diff(s, s)
	if len(ops) != 0 {
		t.Errorf("expected no changes, got %d ops", len(ops))
	}
}

func TestDiffIndexes(t *testing.T) {
	current := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{}, Indexes: []lux.IndexInfo{{Name: "idx_users_name", Kind: lux.BTreeIndex, Columns: []string{"name"}}}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{}, Indexes: []lux.IndexInfo{{Name: "idx_users_email", Kind: lux.BTreeIndex, Columns: []string{"email"}}}},
	}}

	ops := Diff(current, desired)
	addIdx, dropIdx := false, false
	for _, op := range ops {
		if op.Kind == AddIndex && op.Index.Name == "idx_users_email" {
			addIdx = true
		}
		if op.Kind == DropIndex && op.Index.Name == "idx_users_name" {
			dropIdx = true
		}
	}
	if !addIdx {
		t.Error("should add idx_users_email")
	}
	if !dropIdx {
		t.Error("should drop idx_users_name")
	}
}

func TestGenerateMigrationSQL(t *testing.T) {
	desired := &SchemaState{Models: map[string]*ModelState{
		"User": {Table: "users", Columns: map[string]*ColumnState{
			"id":   {Type: "SERIAL", PK: true, Serial: true},
			"name": {Type: "TEXT"},
		}},
	}}
	ops := []DiffOp{
		{Kind: CreateTable, Model: "User", Table: "users"},
	}

	up, down := GenerateMigrationSQL(ops, desired, pgDialect())

	if !strings.Contains(up, "CREATE TABLE users") {
		t.Errorf("up missing CREATE TABLE:\n%s", up)
	}
	if !strings.Contains(down, "DROP TABLE IF EXISTS users") {
		t.Errorf("down missing DROP TABLE:\n%s", down)
	}
}

func TestGenerateMigrationSQLDrop(t *testing.T) {
	ops := []DiffOp{
		{Kind: DropTable, Model: "Old", Table: "olds"},
	}
	up, _ := GenerateMigrationSQL(ops, &SchemaState{Models: map[string]*ModelState{}}, pgDialect())

	if !strings.Contains(up, "-- DROP TABLE") {
		t.Error("DROP TABLE should be commented out")
	}
}

func TestGenerateMigrationSQLAlter(t *testing.T) {
	ops := []DiffOp{
		{Kind: AlterColumn, Model: "User", Table: "users", Column: "name",
			OldColumn: &ColumnState{Type: "VARCHAR(50)", Nullable: false, Unique: false},
			NewColumn: &ColumnState{Type: "TEXT", Nullable: true, Unique: true}},
	}
	up, _ := GenerateMigrationSQL(ops, &SchemaState{Models: map[string]*ModelState{}}, pgDialect())

	if !strings.Contains(up, "ALTER COLUMN name TYPE TEXT") {
		t.Errorf("should alter type:\n%s", up)
	}
	if !strings.Contains(up, "DROP NOT NULL") {
		t.Errorf("should drop not null:\n%s", up)
	}
	if !strings.Contains(up, "UNIQUE") {
		t.Errorf("should add unique:\n%s", up)
	}
}

func TestAutoMigrationName(t *testing.T) {
	tests := []struct {
		name string
		ops  []DiffOp
		want string
	}{
		{"empty", nil, "empty"},
		{"create", []DiffOp{{Kind: CreateTable, Table: "users"}}, "create_users"},
		{"create multi", []DiffOp{{Kind: CreateTable, Table: "users"}, {Kind: CreateTable, Table: "posts"}}, "create_users_posts"},
		{"add column", []DiffOp{{Kind: AddColumn, Table: "users", Column: "avatar"}}, "add_avatar_to_users"},
		{"add 4 columns", []DiffOp{
			{Kind: AddColumn, Table: "users", Column: "a"},
			{Kind: AddColumn, Table: "users", Column: "b"},
			{Kind: AddColumn, Table: "users", Column: "c"},
			{Kind: AddColumn, Table: "users", Column: "d"},
		}, "add_a_b_c_and_1_more_to_users"},
		{"mixed", []DiffOp{
			{Kind: CreateTable, Table: "users"},
			{Kind: AddColumn, Table: "posts", Column: "x"},
		}, "update_"}, // prefix check only — map order varies
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AutoMigrationName(tt.ops)
			if !strings.HasPrefix(got, tt.want) {
				t.Errorf("got %q, want prefix %q", got, tt.want)
			}
		})
	}
}

func TestJoinNames(t *testing.T) {
	if got := joinNames([]string{"a", "b"}, 3); got != "a_b" {
		t.Errorf("got %q", got)
	}
	if got := joinNames([]string{"a", "b", "c", "d", "e"}, 3); got != "a_b_c_and_2_more" {
		t.Errorf("got %q", got)
	}
}
