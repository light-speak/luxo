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

	ops := Diff(current, desired, nil)
	if len(ops) != 1 || ops[0].Kind != CreateTable {
		t.Errorf("expected CreateTable, got %v", ops)
	}
}

func TestDiffDropTable(t *testing.T) {
	current := &SchemaState{Models: map[string]*ModelState{
		"Old": {Table: "olds", Columns: map[string]*ColumnState{}},
	}}
	desired := &SchemaState{Models: map[string]*ModelState{}}

	ops := Diff(current, desired, nil)
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

	ops := Diff(current, desired, nil)
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

	ops := Diff(current, desired, nil)
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

	ops := Diff(current, desired, nil)
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
	ops := Diff(s, s, nil)
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

	ops := Diff(current, desired, nil)
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

// ===== buildColumnState =====

func TestBuildColumnStateWithDefault(t *testing.T) {
	d := pgDialect()

	// String default
	f := &ast.FieldDecl{
		Name:    "status",
		Type:    &ast.TypeRef{Name: "String"},
		Default: &ast.Literal{Value: "active"},
	}
	cs := buildColumnState(f, d)
	if cs == nil {
		t.Fatal("should not be nil")
	}
	if cs.Default != "'active'" {
		t.Errorf("String default = %q, want 'active'", cs.Default)
	}

	// Boolean default true
	f = &ast.FieldDecl{
		Name:    "active",
		Type:    &ast.TypeRef{Name: "Boolean"},
		Default: &ast.Literal{Value: "true"},
	}
	cs = buildColumnState(f, d)
	if cs.Default != "TRUE" {
		t.Errorf("Boolean true default = %q, want TRUE", cs.Default)
	}

	// Boolean default false
	f = &ast.FieldDecl{
		Name:    "visible",
		Type:    &ast.TypeRef{Name: "Boolean"},
		Default: &ast.Literal{Value: "false"},
	}
	cs = buildColumnState(f, d)
	if cs.Default != "FALSE" {
		t.Errorf("Boolean false default = %q, want FALSE", cs.Default)
	}

	// Int default (numeric, falls to default branch)
	f = &ast.FieldDecl{
		Name:    "count",
		Type:    &ast.TypeRef{Name: "Int"},
		Default: &ast.Literal{Value: "0"},
	}
	cs = buildColumnState(f, d)
	if cs.Default != "0" {
		t.Errorf("Int default = %q, want 0", cs.Default)
	}

	// Float default
	f = &ast.FieldDecl{
		Name:    "score",
		Type:    &ast.TypeRef{Name: "Float"},
		Default: &ast.Literal{Value: "3.14"},
	}
	cs = buildColumnState(f, d)
	if cs.Default != "3.14" {
		t.Errorf("Float default = %q, want 3.14", cs.Default)
	}
}

func TestBuildColumnStateEnumDefault(t *testing.T) {
	d := pgDialect()
	f := &ast.FieldDecl{
		Name:    "role",
		Type:    &ast.TypeRef{Name: "Role"},
		Default: &ast.Ident{Name: "Role.USER"},
	}
	cs := buildColumnState(f, d)
	if cs == nil {
		t.Fatal("should not be nil")
	}
	if cs.Default != "'USER'" {
		t.Errorf("Enum default = %q, want 'USER'", cs.Default)
	}
}

func TestBuildColumnStateNilType(t *testing.T) {
	d := pgDialect()
	f := &ast.FieldDecl{Name: "x", Type: nil}
	cs := buildColumnState(f, d)
	if cs != nil {
		t.Error("nil type should return nil")
	}
}

func TestBuildColumnStateListType(t *testing.T) {
	d := pgDialect()
	f := &ast.FieldDecl{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}}
	cs := buildColumnState(f, d)
	if cs != nil {
		t.Error("list type should return nil")
	}
}

func TestBuildColumnStateNullable(t *testing.T) {
	d := pgDialect()
	f := &ast.FieldDecl{Name: "bio", Type: &ast.TypeRef{Name: "String", Nullable: true}}
	cs := buildColumnState(f, d)
	if cs == nil {
		t.Fatal("should not be nil")
	}
	if !cs.Nullable {
		t.Error("should be nullable")
	}
}

// ===== defaultSQL =====

func TestDefaultSQL(t *testing.T) {
	tests := []struct {
		luxoType string
		value    string
		want     string
	}{
		{"String", "hello", "'hello'"},
		{"Boolean", "true", "TRUE"},
		{"Boolean", "false", "FALSE"},
		{"Int", "42", "42"},
		{"Float", "3.14", "3.14"},
		{"DateTime", "NOW()", "NOW()"},
	}
	for _, tt := range tests {
		t.Run(tt.luxoType+"_"+tt.value, func(t *testing.T) {
			got := defaultSQL(tt.luxoType, tt.value)
			if got != tt.want {
				t.Errorf("defaultSQL(%q, %q) = %q, want %q", tt.luxoType, tt.value, got, tt.want)
			}
		})
	}
}

// ===== extractDirectiveInfos =====

func TestExtractDirectiveInfos(t *testing.T) {
	dirs := []*ast.Directive{
		{Name: "unique"},
		{Name: "varchar", Args: []*ast.NamedArg{{Value: &ast.Literal{Value: "100"}}}},
		{Name: "index"},
	}
	infos := extractDirectiveInfos(dirs)
	if len(infos) != 3 {
		t.Fatalf("got %d infos, want 3", len(infos))
	}
	if infos[0].Name != "unique" || infos[0].Arg != "" {
		t.Errorf("first = %+v", infos[0])
	}
	if infos[1].Name != "varchar" || infos[1].Arg != "100" {
		t.Errorf("second = %+v", infos[1])
	}
}

func TestExtractDirectiveInfosWithNonLiteralArg(t *testing.T) {
	// Arg that is not a Literal (e.g., Ident) — Arg should remain empty
	dirs := []*ast.Directive{
		{Name: "ref", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "User"}}}},
	}
	infos := extractDirectiveInfos(dirs)
	if len(infos) != 1 {
		t.Fatalf("got %d infos, want 1", len(infos))
	}
	if infos[0].Arg != "" {
		t.Errorf("non-literal arg should be empty, got %q", infos[0].Arg)
	}
}

func TestExtractDirectiveInfosEmpty(t *testing.T) {
	infos := extractDirectiveInfos(nil)
	if len(infos) != 0 {
		t.Errorf("nil directives should return empty slice")
	}
}

// ===== collectIndexes =====

func TestCollectIndexesFieldLevel(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "serial"}}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "index"}}},
			{Name: "bio", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "search"}}},
			{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}, Directives: []*ast.Directive{{Name: "brin"}}},
		},
	}
	indexes := collectIndexes(m, "users")
	if len(indexes) != 3 {
		t.Fatalf("got %d indexes, want 3", len(indexes))
	}

	found := map[lux.IndexKind]bool{}
	for _, idx := range indexes {
		found[idx.Kind] = true
	}
	if !found[lux.BTreeIndex] {
		t.Error("missing BTree index for @index")
	}
	if !found[lux.SearchIndex] {
		t.Error("missing Search index for @search")
	}
	if !found[lux.BrinIndex] {
		t.Error("missing BRIN index for @brin")
	}
}

func TestCollectIndexesComposite(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Directives: []*ast.Directive{
			{Name: "index", Args: []*ast.NamedArg{
				{Name: "fields", Value: &ast.ListExpr{Items: []ast.Expr{
					&ast.Ident{Name: "firstName"},
					&ast.Ident{Name: "lastName"},
				}}},
			}},
		},
		Fields: []*ast.FieldDecl{
			{Name: "firstName", Type: &ast.TypeRef{Name: "String"}},
			{Name: "lastName", Type: &ast.TypeRef{Name: "String"}},
		},
	}
	indexes := collectIndexes(m, "users")
	if len(indexes) != 1 {
		t.Fatalf("got %d indexes, want 1", len(indexes))
	}
	if indexes[0].Kind != lux.CompositeIndex {
		t.Errorf("kind = %v, want CompositeIndex", indexes[0].Kind)
	}
	if len(indexes[0].Columns) != 2 {
		t.Errorf("columns = %v, want 2 columns", indexes[0].Columns)
	}
	if indexes[0].Columns[0] != "first_name" || indexes[0].Columns[1] != "last_name" {
		t.Errorf("columns = %v", indexes[0].Columns)
	}
}

func TestCollectIndexesSkipsComputed(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "index"}}},
			{Name: "computed", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}, Directives: []*ast.Directive{{Name: "index"}}},
		},
	}
	indexes := collectIndexes(m, "users")
	if len(indexes) != 1 {
		t.Fatalf("got %d indexes, want 1 (should skip computed)", len(indexes))
	}
}

// ===== extractIndexFields =====

func TestExtractIndexFieldsValid(t *testing.T) {
	d := &ast.Directive{
		Name: "index",
		Args: []*ast.NamedArg{
			{Name: "fields", Value: &ast.ListExpr{Items: []ast.Expr{
				&ast.Ident{Name: "name"},
				&ast.Ident{Name: "email"},
			}}},
		},
	}
	cols := extractIndexFields(d)
	if len(cols) != 2 || cols[0] != "name" || cols[1] != "email" {
		t.Errorf("got %v", cols)
	}
}

func TestExtractIndexFieldsNoFieldsArg(t *testing.T) {
	d := &ast.Directive{
		Name: "index",
		Args: []*ast.NamedArg{
			{Name: "other", Value: &ast.Literal{Value: "x"}},
		},
	}
	cols := extractIndexFields(d)
	if len(cols) != 0 {
		t.Errorf("should return nil, got %v", cols)
	}
}

func TestExtractIndexFieldsNonListValue(t *testing.T) {
	d := &ast.Directive{
		Name: "index",
		Args: []*ast.NamedArg{
			{Name: "fields", Value: &ast.Literal{Value: "name"}},
		},
	}
	cols := extractIndexFields(d)
	if len(cols) != 0 {
		t.Errorf("non-list value should return nil, got %v", cols)
	}
}

func TestExtractIndexFieldsNoArgs(t *testing.T) {
	d := &ast.Directive{Name: "index"}
	cols := extractIndexFields(d)
	if len(cols) != 0 {
		t.Errorf("no args should return nil, got %v", cols)
	}
}

// ===== BuildFieldIDMaps =====

func TestBuildFieldIDMaps(t *testing.T) {
	oldLock := map[string]map[string]int{
		"User": {"name": 1, "email": 2},
		"Post": {"title": 1},
	}
	newLock := map[string]map[string]int{
		"User":    {"displayName": 1, "email": 2},
		"Comment": {"body": 1},
	}

	result := BuildFieldIDMaps(oldLock, newLock)

	// User should be present (in both)
	userFIM := result["User"]
	if userFIM == nil {
		t.Fatal("User FIM missing")
	}
	if userFIM.OldByID[1] != "name" {
		t.Errorf("old ID 1 = %q, want name", userFIM.OldByID[1])
	}
	if userFIM.NewByID[1] != "display_name" {
		t.Errorf("new ID 1 = %q, want display_name", userFIM.NewByID[1])
	}
	if userFIM.OldByID[2] != "email" {
		t.Errorf("old ID 2 = %q, want email", userFIM.OldByID[2])
	}
	if userFIM.NewByID[2] != "email" {
		t.Errorf("new ID 2 = %q, want email", userFIM.NewByID[2])
	}

	// Post should be present (only in old)
	postFIM := result["Post"]
	if postFIM == nil {
		t.Fatal("Post FIM missing")
	}
	if postFIM.OldByID[1] != "title" {
		t.Errorf("Post old ID 1 = %q, want title", postFIM.OldByID[1])
	}
	if len(postFIM.NewByID) != 0 {
		t.Errorf("Post new should be empty, got %v", postFIM.NewByID)
	}

	// Comment should be present (only in new)
	commentFIM := result["Comment"]
	if commentFIM == nil {
		t.Fatal("Comment FIM missing")
	}
	if len(commentFIM.OldByID) != 0 {
		t.Errorf("Comment old should be empty, got %v", commentFIM.OldByID)
	}
	if commentFIM.NewByID[1] != "body" {
		t.Errorf("Comment new ID 1 = %q, want body", commentFIM.NewByID[1])
	}
}

func TestBuildFieldIDMapsEmpty(t *testing.T) {
	result := BuildFieldIDMaps(nil, nil)
	if len(result) != 0 {
		t.Errorf("empty locks should give empty result, got %d", len(result))
	}
}

// ===== classifyTypeChange =====

func TestClassifyTypeChange(t *testing.T) {
	tests := []struct {
		old, new string
		safe     bool // true = no warning (empty string)
	}{
		// Safe widening
		{"SMALLINT", "BIGINT", true},
		{"SMALLINT", "DOUBLE PRECISION", true},
		{"SMALLINT", "DECIMAL", true},
		{"SMALLINT", "TEXT", true},
		{"BIGINT", "DOUBLE PRECISION", true},
		{"BIGINT", "DECIMAL", true},
		{"BIGINT", "TEXT", true},
		{"SERIAL", "BIGINT", true},
		{"SERIAL", "TEXT", true},

		// Dangerous: numeric → non-numeric
		{"BIGINT", "BOOLEAN", false},
		{"BIGINT", "UUID", false},

		// Dangerous: non-numeric → numeric
		{"TEXT", "BIGINT", false},
		{"BOOLEAN", "BIGINT", false},

		// TEXT → VARCHAR: warning about truncation
		{"TEXT", "VARCHAR(255)", false},

		// Generic type change warning
		{"UUID", "TEXT", false},
		{"TIMESTAMPTZ", "TEXT", false},
	}
	for _, tt := range tests {
		t.Run(tt.old+"_to_"+tt.new, func(t *testing.T) {
			got := classifyTypeChange(tt.old, tt.new)
			if tt.safe && got != "" {
				t.Errorf("expected safe (empty), got %q", got)
			}
			if !tt.safe && got == "" {
				t.Errorf("expected warning, got empty")
			}
		})
	}
}

// ===== classifyWarning =====

func TestClassifyWarning(t *testing.T) {
	// DropTable
	op := &DiffOp{Kind: DropTable}
	if w := classifyWarning(op); !strings.Contains(w, "DANGEROUS") {
		t.Errorf("DropTable warning = %q", w)
	}

	// DropColumn
	op = &DiffOp{Kind: DropColumn}
	if w := classifyWarning(op); !strings.Contains(w, "DANGEROUS") {
		t.Errorf("DropColumn warning = %q", w)
	}

	// CreateTable — no warning
	op = &DiffOp{Kind: CreateTable}
	if w := classifyWarning(op); w != "" {
		t.Errorf("CreateTable should have no warning, got %q", w)
	}

	// AddColumn — no warning
	op = &DiffOp{Kind: AddColumn}
	if w := classifyWarning(op); w != "" {
		t.Errorf("AddColumn should have no warning, got %q", w)
	}
}

func TestClassifyAlterWarningTypeChange(t *testing.T) {
	op := &DiffOp{
		Kind:      AlterColumn,
		OldColumn: &ColumnState{Type: "TEXT"},
		NewColumn: &ColumnState{Type: "BIGINT"},
	}
	w := classifyWarning(op)
	if !strings.Contains(w, "DANGEROUS") {
		t.Errorf("TEXT→BIGINT should be DANGEROUS, got %q", w)
	}
}

func TestClassifyAlterWarningNotNullNoDefault(t *testing.T) {
	op := &DiffOp{
		Kind:      AlterColumn,
		OldColumn: &ColumnState{Type: "TEXT", Nullable: true},
		NewColumn: &ColumnState{Type: "TEXT", Nullable: false},
	}
	w := classifyWarning(op)
	if !strings.Contains(w, "WARNING") {
		t.Errorf("nullable→NOT NULL without default should warn, got %q", w)
	}
}

func TestClassifyAlterWarningNotNullWithDefault(t *testing.T) {
	op := &DiffOp{
		Kind:      AlterColumn,
		OldColumn: &ColumnState{Type: "TEXT", Nullable: true},
		NewColumn: &ColumnState{Type: "TEXT", Nullable: false, Default: "'unknown'"},
	}
	w := classifyWarning(op)
	// With default, no special NOT NULL warning — same type, so no warning
	if w != "" {
		t.Errorf("nullable→NOT NULL with default should have no warning, got %q", w)
	}
}

func TestClassifyAlterWarningNilColumns(t *testing.T) {
	op := &DiffOp{Kind: AlterColumn, OldColumn: nil, NewColumn: nil}
	w := classifyWarning(op)
	if w != "" {
		t.Errorf("nil columns should have no warning, got %q", w)
	}
}

// ===== addSafetyWarnings =====

func TestAddSafetyWarnings(t *testing.T) {
	ops := []DiffOp{
		{Kind: CreateTable, Table: "users"},
		{Kind: DropTable, Table: "olds"},
		{Kind: DropColumn, Table: "users", Column: "old"},
		{Kind: AddColumn, Table: "users", Column: "new", NewColumn: &ColumnState{Type: "TEXT"}},
	}
	result := addSafetyWarnings(ops)
	if len(result) != 4 {
		t.Fatalf("len = %d, want 4", len(result))
	}
	if result[0].Warning != "" {
		t.Error("CreateTable should have no warning")
	}
	if !strings.Contains(result[1].Warning, "DANGEROUS") {
		t.Errorf("DropTable warning = %q", result[1].Warning)
	}
	if !strings.Contains(result[2].Warning, "DANGEROUS") {
		t.Errorf("DropColumn warning = %q", result[2].Warning)
	}
	if result[3].Warning != "" {
		t.Error("AddColumn should have no warning")
	}
}

// ===== isNumeric =====

func TestIsNumeric(t *testing.T) {
	numerics := []string{"BIGINT", "SMALLINT", "SERIAL", "DOUBLE PRECISION", "DECIMAL", "DECIMAL(10,2)"}
	for _, n := range numerics {
		if !isNumeric(n) {
			t.Errorf("isNumeric(%q) = false, want true", n)
		}
	}
	nonNumerics := []string{"TEXT", "BOOLEAN", "UUID", "TIMESTAMPTZ", "VARCHAR(255)", "BYTEA"}
	for _, n := range nonNumerics {
		if isNumeric(n) {
			t.Errorf("isNumeric(%q) = true, want false", n)
		}
	}
}

// ===== GenerateMigrationSQL — AlterColumn default changes =====

func TestGenerateMigrationSQLDefaultChange(t *testing.T) {
	ops := []DiffOp{
		{Kind: AlterColumn, Model: "User", Table: "users", Column: "score",
			OldColumn: &ColumnState{Type: "BIGINT"},
			NewColumn: &ColumnState{Type: "BIGINT", Default: "0"}},
	}
	up, down := GenerateMigrationSQL(ops, &SchemaState{Models: map[string]*ModelState{}}, pgDialect())

	if !strings.Contains(up, "SET DEFAULT 0") {
		t.Errorf("UP should SET DEFAULT:\n%s", up)
	}
	if !strings.Contains(down, "DROP DEFAULT") {
		t.Errorf("DOWN should DROP DEFAULT:\n%s", down)
	}
}

func TestGenerateMigrationSQLDropDefault(t *testing.T) {
	ops := []DiffOp{
		{Kind: AlterColumn, Model: "User", Table: "users", Column: "score",
			OldColumn: &ColumnState{Type: "BIGINT", Default: "0"},
			NewColumn: &ColumnState{Type: "BIGINT"}},
	}
	up, down := GenerateMigrationSQL(ops, &SchemaState{Models: map[string]*ModelState{}}, pgDialect())

	if !strings.Contains(up, "DROP DEFAULT") {
		t.Errorf("UP should DROP DEFAULT:\n%s", up)
	}
	if !strings.Contains(down, "SET DEFAULT 0") {
		t.Errorf("DOWN should SET DEFAULT:\n%s", down)
	}
}

// ===== GenerateMigrationSQL — SetNotNull direction =====

func TestGenerateMigrationSQLSetNotNull(t *testing.T) {
	ops := []DiffOp{
		{Kind: AlterColumn, Model: "User", Table: "users", Column: "bio",
			OldColumn: &ColumnState{Type: "TEXT", Nullable: true},
			NewColumn: &ColumnState{Type: "TEXT", Nullable: false}},
	}
	up, down := GenerateMigrationSQL(ops, &SchemaState{Models: map[string]*ModelState{}}, pgDialect())

	if !strings.Contains(up, "SET NOT NULL") {
		t.Errorf("UP should SET NOT NULL:\n%s", up)
	}
	if !strings.Contains(down, "DROP NOT NULL") {
		t.Errorf("DOWN should DROP NOT NULL:\n%s", down)
	}
}

// ===== LoadState — nil models field =====

func TestLoadStateReadError(t *testing.T) {
	// Create a directory instead of a file — ReadFile on a directory returns an error
	// that is NOT os.IsNotExist, exercising the return nil, err path.
	dir := t.TempDir()
	dirPath := dir + "/.state.json"
	os.Mkdir(dirPath, 0755) // create a directory with the file's name

	_, err := LoadState(dirPath)
	if err == nil {
		t.Fatal("expected error reading a directory as file")
	}
}

func TestLoadStateNullModels(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.state.json"
	os.WriteFile(path, []byte(`{}`), 0644)

	s, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Models == nil {
		t.Error("Models should be initialized even when null in JSON")
	}
}

// ===== CollectEnumsFromResult =====

func TestCollectEnumsFromResult(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "a.luxo",
				Enums: []*ast.EnumDecl{
					{Name: "Role", Values: []string{"USER", "ADMIN"}},
				},
			},
			{
				Name: "b.luxo",
				Enums: []*ast.EnumDecl{
					{Name: "Status", Values: []string{"ACTIVE", "INACTIVE"}},
				},
			},
		},
	}
	enums := CollectEnumsFromResult(result)
	if len(enums) != 2 {
		t.Fatalf("got %d enums, want 2", len(enums))
	}
	if !enums["Role"] {
		t.Error("missing Role")
	}
	if !enums["Status"] {
		t.Error("missing Status")
	}
}

// ===== detectRenames edge cases =====

func TestDetectRenamesNilFIM(t *testing.T) {
	current := &ModelState{Columns: map[string]*ColumnState{"a": {Type: "TEXT"}}}
	desired := &ModelState{Columns: map[string]*ColumnState{"b": {Type: "TEXT"}}}
	renames := detectRenames(current, desired, nil)
	if renames != nil {
		t.Errorf("nil FIM should return nil renames, got %v", renames)
	}
}

func TestDetectRenamesNoMatch(t *testing.T) {
	current := &ModelState{Columns: map[string]*ColumnState{"a": {Type: "TEXT"}}}
	desired := &ModelState{Columns: map[string]*ColumnState{"b": {Type: "TEXT"}}}
	fim := &FieldIDMap{
		OldByID: map[int]string{1: "a"},
		NewByID: map[int]string{2: "b"}, // different ID → no rename
	}
	renames := detectRenames(current, desired, fim)
	if len(renames) != 0 {
		t.Errorf("different IDs should not be renames, got %v", renames)
	}
}

func TestDetectRenamesSameName(t *testing.T) {
	current := &ModelState{Columns: map[string]*ColumnState{"a": {Type: "TEXT"}}}
	desired := &ModelState{Columns: map[string]*ColumnState{"a": {Type: "TEXT"}}}
	fim := &FieldIDMap{
		OldByID: map[int]string{1: "a"},
		NewByID: map[int]string{1: "a"}, // same name → no rename
	}
	renames := detectRenames(current, desired, fim)
	if len(renames) != 0 {
		t.Errorf("same name should not be a rename, got %v", renames)
	}
}

func TestDetectRenamesNotInCurrent(t *testing.T) {
	current := &ModelState{Columns: map[string]*ColumnState{"x": {Type: "TEXT"}}}
	desired := &ModelState{Columns: map[string]*ColumnState{"b": {Type: "TEXT"}}}
	fim := &FieldIDMap{
		OldByID: map[int]string{1: "a"}, // "a" not in current
		NewByID: map[int]string{1: "b"},
	}
	renames := detectRenames(current, desired, fim)
	if len(renames) != 0 {
		t.Errorf("old name not in current should not be rename, got %v", renames)
	}
}

func TestDetectRenamesNotInDesired(t *testing.T) {
	current := &ModelState{Columns: map[string]*ColumnState{"a": {Type: "TEXT"}}}
	desired := &ModelState{Columns: map[string]*ColumnState{"x": {Type: "TEXT"}}}
	fim := &FieldIDMap{
		OldByID: map[int]string{1: "a"},
		NewByID: map[int]string{1: "b"}, // "b" not in desired
	}
	renames := detectRenames(current, desired, fim)
	if len(renames) != 0 {
		t.Errorf("new name not in desired should not be rename, got %v", renames)
	}
}
