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
