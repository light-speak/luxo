package codegen

import (
	"strings"
	"testing"

	luxoast "github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func mkModel(name string, directives []*luxoast.Directive, fields []*luxoast.FieldDecl) *luxoast.ModelDecl {
	return &luxoast.ModelDecl{
		Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name:       name,
		Directives: directives,
		Fields:     fields,
	}
}

func mkField(name, typeName string, directives ...*luxoast.Directive) *luxoast.FieldDecl {
	return &luxoast.FieldDecl{
		Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name:       name,
		Type:       &luxoast.TypeRef{Name: typeName},
		Directives: directives,
	}
}

func TestGenerateCreateTable(t *testing.T) {
	m := mkModel("User", nil, []*luxoast.FieldDecl{
		mkField("id", "Int", &luxoast.Directive{Name: "id"}, &luxoast.Directive{Name: "serial"}),
		mkField("name", "String"),
		mkField("email", "String", &luxoast.Directive{Name: "unique"}),
	})

	up, down := generateCreateTable(m, nil)

	checks := []string{
		"CREATE TABLE users",
		"SERIAL",
		"PRIMARY KEY",
		"name TEXT NOT NULL",
		"email TEXT NOT NULL UNIQUE",
		"created_at TIMESTAMPTZ",
		"updated_at TIMESTAMPTZ",
	}
	for _, check := range checks {
		if !strings.Contains(up, check) {
			t.Errorf("missing %q in UP:\n%s", check, up)
		}
	}

	if !strings.Contains(down, "DROP TABLE IF EXISTS users") {
		t.Errorf("bad DOWN:\n%s", down)
	}
}

func TestGenerateCreateTableSoftDelete(t *testing.T) {
	m := mkModel("Post", []*luxoast.Directive{{Name: "soft"}}, []*luxoast.FieldDecl{
		mkField("id", "Int", &luxoast.Directive{Name: "id"}),
		mkField("title", "String"),
	})

	up, _ := generateCreateTable(m, nil)

	if !strings.Contains(up, "deleted_at TIMESTAMPTZ") {
		t.Errorf("missing deleted_at for @soft:\n%s", up)
	}
}

func TestGenerateCreateTableNoTime(t *testing.T) {
	m := mkModel("Config", []*luxoast.Directive{{Name: "noTime"}}, []*luxoast.FieldDecl{
		mkField("key", "String"),
		mkField("value", "String"),
	})

	up, _ := generateCreateTable(m, nil)

	if strings.Contains(up, "created_at") {
		t.Error("@noTime should skip created_at")
	}
	if strings.Contains(up, "updated_at") {
		t.Error("@noTime should skip updated_at")
	}
}

func TestGenerateCreateTableIndex(t *testing.T) {
	m := mkModel("User", nil, []*luxoast.FieldDecl{
		mkField("id", "Int"),
		mkField("name", "String", &luxoast.Directive{Name: "index"}),
	})

	up, _ := generateCreateTable(m, nil)

	if !strings.Contains(up, "CREATE INDEX idx_users_name ON users (name)") {
		t.Errorf("missing index:\n%s", up)
	}
}

func TestGenerateCreateTableSkipsRelation(t *testing.T) {
	m := mkModel("Post", nil, []*luxoast.FieldDecl{
		mkField("id", "Int"),
		mkField("title", "String"),
		{Name: "comments", Type: &luxoast.TypeRef{Name: "Comment", IsList: true}},
	})

	up, _ := generateCreateTable(m, nil)

	if strings.Contains(up, "comments") {
		t.Error("relation field should be skipped")
	}
}

func TestGenerateCreateTableNullable(t *testing.T) {
	m := mkModel("User", nil, []*luxoast.FieldDecl{
		mkField("id", "Int"),
		{Name: "avatar", Type: &luxoast.TypeRef{Name: "String", Nullable: true}},
	})

	up, _ := generateCreateTable(m, nil)

	// Nullable field should not have NOT NULL
	if strings.Contains(up, "avatar TEXT NOT NULL") {
		t.Errorf("nullable field should not be NOT NULL:\n%s", up)
	}
}

func TestResolveColumnType(t *testing.T) {
	tests := []struct {
		typeName string
		want     string
	}{
		{"Int", "BIGINT"},
		{"Float", "DOUBLE PRECISION"},
		{"String", "TEXT"},
		{"Boolean", "BOOLEAN"},
		{"DateTime", "TIMESTAMPTZ"},
		{"Duration", "INTERVAL"},
		{"UUID", "UUID"},
		{"Decimal", "DECIMAL"},
		{"Bytes", "BYTEA"},
		{"Role", "TEXT"}, // enum → TEXT
	}
	for _, tt := range tests {
		f := &luxoast.FieldDecl{Type: &luxoast.TypeRef{Name: tt.typeName}}
		got := resolveColumnType(f)
		if got != tt.want {
			t.Errorf("resolveColumnType(%s) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestResolveColumnTypeList(t *testing.T) {
	f := &luxoast.FieldDecl{Type: &luxoast.TypeRef{Name: "Post", IsList: true}}
	if got := resolveColumnType(f); got != "" {
		t.Errorf("list type should return empty, got %q", got)
	}
}

func TestResolveColumnTypeNil(t *testing.T) {
	f := &luxoast.FieldDecl{}
	if got := resolveColumnType(f); got != "" {
		t.Errorf("nil type should return empty, got %q", got)
	}
}

func TestGenerateMigrations(t *testing.T) {
	result := &semantic.Result{
		Files: []*luxoast.File{{
			Models: []*luxoast.ModelDecl{
				mkModel("User", nil, []*luxoast.FieldDecl{
					mkField("id", "Int"),
					mkField("name", "String"),
				}),
				mkModel("Post", nil, []*luxoast.FieldDecl{
					mkField("id", "Int"),
					mkField("title", "String"),
				}),
			},
		}},
	}

	migrations := GenerateMigrations(result)
	if len(migrations) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(migrations))
	}

	if !strings.Contains(migrations[0].Name, "create_users") {
		t.Errorf("first migration name = %q", migrations[0].Name)
	}
	if !strings.Contains(migrations[1].Name, "create_posts") {
		t.Errorf("second migration name = %q", migrations[1].Name)
	}
}

func TestHasFieldNamedFound(t *testing.T) {
	fields := []*luxoast.FieldDecl{
		{Name: "id"},
		{Name: "createdAt"},
		{Name: "updatedAt"},
	}
	if !hasFieldNamed(fields, "createdAt") {
		t.Error("should find createdAt")
	}
	if !hasFieldNamed(fields, "updatedAt") {
		t.Error("should find updatedAt")
	}
}

func TestHasFieldNamedNotFound(t *testing.T) {
	fields := []*luxoast.FieldDecl{
		{Name: "id"},
	}
	if hasFieldNamed(fields, "createdAt") {
		t.Error("should not find createdAt")
	}
}

func TestGenerateColumnSerial(t *testing.T) {
	f := &luxoast.FieldDecl{
		Name:       "id",
		Type:       &luxoast.TypeRef{Name: "Int"},
		Directives: []*luxoast.Directive{{Name: "serial"}, {Name: "id"}},
	}
	col := generateColumn(f)
	if !strings.Contains(col, "SERIAL") {
		t.Errorf("serial field should use SERIAL type, got: %s", col)
	}
	if !strings.Contains(col, "PRIMARY KEY") {
		t.Errorf("@id should add PRIMARY KEY, got: %s", col)
	}
	// Serial should not have NOT NULL
	if strings.Contains(col, "NOT NULL") {
		t.Errorf("serial field should not have NOT NULL, got: %s", col)
	}
}

func TestGenerateColumnWithDefault(t *testing.T) {
	f := &luxoast.FieldDecl{
		Name:    "status",
		Type:    &luxoast.TypeRef{Name: "String"},
		Default: &luxoast.Literal{Value: "active"},
	}
	col := generateColumn(f)
	// Field with default should not have NOT NULL
	if strings.Contains(col, "NOT NULL") {
		t.Errorf("field with default should not have NOT NULL, got: %s", col)
	}
}

func TestGenerateColumnNilType(t *testing.T) {
	f := &luxoast.FieldDecl{
		Name: "unknown",
		Type: nil,
	}
	col := generateColumn(f)
	if col != "" {
		t.Errorf("nil type should return empty, got: %s", col)
	}
}

func TestGenerateColumnListType(t *testing.T) {
	f := &luxoast.FieldDecl{
		Name: "tags",
		Type: &luxoast.TypeRef{Name: "String", IsList: true},
	}
	col := generateColumn(f)
	if col != "" {
		t.Errorf("list type should return empty, got: %s", col)
	}
}

func TestResolveVarcharType(t *testing.T) {
	// @varchar with length arg
	f := &luxoast.FieldDecl{
		Name: "name",
		Type: &luxoast.TypeRef{Name: "String"},
		Directives: []*luxoast.Directive{{
			Name: "varchar",
			Args: []*luxoast.NamedArg{{
				Name:  "length",
				Value: &luxoast.Literal{Value: "100"},
			}},
		}},
	}
	got := resolveVarcharType(f)
	if got != "VARCHAR(100)" {
		t.Errorf("resolveVarcharType = %q, want VARCHAR(100)", got)
	}
}

func TestResolveVarcharTypeDefault(t *testing.T) {
	// @varchar without args
	f := &luxoast.FieldDecl{
		Name:       "name",
		Type:       &luxoast.TypeRef{Name: "String"},
		Directives: []*luxoast.Directive{{Name: "varchar"}},
	}
	got := resolveVarcharType(f)
	if got != "VARCHAR(255)" {
		t.Errorf("resolveVarcharType = %q, want VARCHAR(255)", got)
	}
}

func TestResolveColumnTypeVarchar(t *testing.T) {
	f := &luxoast.FieldDecl{
		Name: "name",
		Type: &luxoast.TypeRef{Name: "String"},
		Directives: []*luxoast.Directive{{
			Name: "varchar",
			Args: []*luxoast.NamedArg{{
				Name:  "length",
				Value: &luxoast.Literal{Value: "50"},
			}},
		}},
	}
	got := resolveColumnType(f)
	if got != "VARCHAR(50)" {
		t.Errorf("resolveColumnType with @varchar = %q, want VARCHAR(50)", got)
	}
}

func TestApplyTypeDirectiveIntBigint(t *testing.T) {
	f := &luxoast.FieldDecl{
		Type:       &luxoast.TypeRef{Name: "Int"},
		Directives: []*luxoast.Directive{{Name: "bigint"}},
	}
	got := applyTypeDirective("BIGINT", f)
	if got != "BIGINT" {
		t.Errorf("got %q, want BIGINT", got)
	}
}

func TestApplyTypeDirectiveIntSmallint(t *testing.T) {
	f := &luxoast.FieldDecl{
		Type:       &luxoast.TypeRef{Name: "Int"},
		Directives: []*luxoast.Directive{{Name: "smallint"}},
	}
	got := applyTypeDirective("BIGINT", f)
	if got != "SMALLINT" {
		t.Errorf("got %q, want SMALLINT", got)
	}
}

func TestApplyTypeDirectiveFloatDecimal(t *testing.T) {
	f := &luxoast.FieldDecl{
		Type:       &luxoast.TypeRef{Name: "Float"},
		Directives: []*luxoast.Directive{{Name: "decimal"}},
	}
	got := applyTypeDirective("DOUBLE PRECISION", f)
	if got != "DECIMAL" {
		t.Errorf("got %q, want DECIMAL", got)
	}
}

func TestApplyTypeDirectiveDateTimeDate(t *testing.T) {
	f := &luxoast.FieldDecl{
		Type:       &luxoast.TypeRef{Name: "DateTime"},
		Directives: []*luxoast.Directive{{Name: "date"}},
	}
	got := applyTypeDirective("TIMESTAMPTZ", f)
	if got != "DATE" {
		t.Errorf("got %q, want DATE", got)
	}
}

func TestApplyTypeDirectiveDateTimeTime(t *testing.T) {
	f := &luxoast.FieldDecl{
		Type:       &luxoast.TypeRef{Name: "DateTime"},
		Directives: []*luxoast.Directive{{Name: "time"}},
	}
	got := applyTypeDirective("TIMESTAMPTZ", f)
	if got != "TIME" {
		t.Errorf("got %q, want TIME", got)
	}
}

func TestApplyTypeDirectiveNoOverride(t *testing.T) {
	f := &luxoast.FieldDecl{
		Type: &luxoast.TypeRef{Name: "String"},
	}
	got := applyTypeDirective("TEXT", f)
	if got != "TEXT" {
		t.Errorf("got %q, want TEXT (no override)", got)
	}
}

func TestSchemaHashSkipsComputed(t *testing.T) {
	// Schema with computed field should skip it in hash
	result := &semantic.Result{
		Files: []*luxoast.File{{
			Models: []*luxoast.ModelDecl{
				mkModel("User", nil, []*luxoast.FieldDecl{
					mkField("id", "Int"),
					{Name: "postCount", Type: &luxoast.TypeRef{Name: "Int"}, Computed: &luxoast.ComputedField{}},
				}),
			},
		}},
	}
	h1 := SchemaHash(result)

	// Same model without computed field should have same hash
	result2 := &semantic.Result{
		Files: []*luxoast.File{{
			Models: []*luxoast.ModelDecl{
				mkModel("User", nil, []*luxoast.FieldDecl{
					mkField("id", "Int"),
				}),
			},
		}},
	}
	h2 := SchemaHash(result2)
	if h1 != h2 {
		t.Error("computed fields should be excluded from hash")
	}
}

func TestGenerateCreateTableExistingTimestamps(t *testing.T) {
	// Model that already has createdAt and updatedAt fields
	m := mkModel("User", nil, []*luxoast.FieldDecl{
		mkField("id", "Int"),
		mkField("createdAt", "DateTime"),
		mkField("updatedAt", "DateTime"),
	})

	up, _ := generateCreateTable(m, nil)

	// Should not duplicate timestamp columns
	if strings.Count(up, "created_at") != 1 {
		t.Errorf("created_at should appear exactly once:\n%s", up)
	}
	if strings.Count(up, "updated_at") != 1 {
		t.Errorf("updated_at should appear exactly once:\n%s", up)
	}
}

func TestSchemaHash(t *testing.T) {
	result := &semantic.Result{
		Files: []*luxoast.File{{
			Models: []*luxoast.ModelDecl{
				mkModel("User", nil, []*luxoast.FieldDecl{
					mkField("id", "Int"),
				}),
			},
		}},
	}
	h1 := SchemaHash(result)
	if len(h1) != 16 {
		t.Errorf("hash length = %d, want 16", len(h1))
	}

	// Different schema → different hash
	result2 := &semantic.Result{
		Files: []*luxoast.File{{
			Models: []*luxoast.ModelDecl{
				mkModel("User", nil, []*luxoast.FieldDecl{
					mkField("id", "Int"),
					mkField("name", "String"),
				}),
			},
		}},
	}
	h2 := SchemaHash(result2)
	if h1 == h2 {
		t.Error("different schemas should have different hashes")
	}
}
