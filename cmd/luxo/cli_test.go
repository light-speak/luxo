package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestCallFieldMaskUsesStableSchemaIDs(t *testing.T) {
	runtimeSchema := schema.New()
	runtimeSchema.RegisterModel(&schema.Model{Name: "Payload", Fields: []schema.Field{
		{ID: 1, Name: "id", Type: schema.FieldInt},
		{ID: 12, Name: "metadata", Type: schema.FieldJSON},
	}})
	apiSchema := &schema.API{Name: "getPayload", ReturnType: "Payload"}

	mask, err := callFieldMask("metadata,id", apiSchema, runtimeSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(codec.SelectionMaskFields(mask), []byte{0x01, 0x08}) {
		t.Fatalf("mask = %x", mask)
	}
	if _, err := callFieldMask("missing", apiSchema, runtimeSchema); err == nil {
		t.Fatal("unknown field should fail")
	}
}

func TestParseBinaryCLIValueUsesSchemaType(t *testing.T) {
	value, err := parseBinaryCLIValue(`["AQI=",""]`, schema.Param{Type: schema.FieldBytes, IsList: true})
	if err != nil {
		t.Fatal(err)
	}
	items := value.([]any)
	if !bytes.Equal(items[0].([]byte), []byte{1, 2}) || len(items[1].([]byte)) != 0 {
		t.Fatalf("bytes = %#v", items)
	}
	jsonValue, err := parseBinaryCLIValue(`{"ok":true}`, schema.Param{Type: schema.FieldJSON})
	if err != nil {
		t.Fatal(err)
	}
	if jsonValue.(map[string]any)["ok"] != true {
		t.Fatalf("JSON = %#v", jsonValue)
	}
}

func TestBuildParamTypesFromASTIncludesServiceFunctions(t *testing.T) {
	files := []*ast.File{{
		Functions: []*ast.FnDecl{{
			Name: "heartbeat",
			Params: []*ast.ParamDecl{
				{Name: "cpuPercent", Type: &ast.TypeRef{Name: "Float"}},
				{Name: "uptime", Type: &ast.TypeRef{Name: "Duration"}},
			},
			Directives: []*ast.Directive{{Name: "service"}},
		}},
	}}

	types := buildParamTypesFromAST(files)
	if got := types["heartbeat"]["cpuPercent"]; got != "Float" {
		t.Fatalf("cpuPercent type = %q, want Float", got)
	}
	if got := types["heartbeat"]["uptime"]; got != "Duration" {
		t.Fatalf("uptime type = %q, want Duration", got)
	}
}

func TestBuildParamTypesFromASTPreservesLists(t *testing.T) {
	files := []*ast.File{{
		APIs: []*ast.ApiDecl{{
			Name: "findMany",
			Params: []*ast.ParamDecl{
				{Name: "ids", Type: &ast.TypeRef{Name: "UUID", IsList: true}},
				{Name: "names", Type: &ast.TypeRef{Name: "String", IsList: true, Nullable: true}},
			},
		}},
	}}

	types := buildParamTypesFromAST(files)
	if got := types["findMany"]["ids"]; got != "[UUID]" {
		t.Fatalf("ids type = %q, want [UUID]", got)
	}
	if got := types["findMany"]["names"]; got != "[String]?" {
		t.Fatalf("names type = %q, want [String]?", got)
	}
}

func TestBuildParamTypesFromASTNormalizesWireTypes(t *testing.T) {
	files := []*ast.File{{
		Enums: []*ast.EnumDecl{{Name: "Role"}},
		Types: []*ast.TypeDecl{{Name: "CreateInput"}},
		APIs: []*ast.ApiDecl{{
			Name: "create",
			Params: []*ast.ParamDecl{
				{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
				{Name: "input", Type: &ast.TypeRef{Name: "CreateInput"}},
				{Name: "inputs", Type: &ast.TypeRef{Name: "CreateInput", IsList: true}},
			},
		}},
	}}

	types := buildParamTypesFromAST(files)
	if got := types["create"]["role"]; got != "Enum" {
		t.Fatalf("role wire type = %q, want Enum", got)
	}
	if got := types["create"]["input"]; got != "JSON" {
		t.Fatalf("input wire type = %q, want JSON", got)
	}
	if got := types["create"]["inputs"]; got != "[JSON]" {
		t.Fatalf("inputs wire type = %q, want [JSON]", got)
	}
}

func TestBuildParamTypesFromASTUsesOnlyGeneratedCRUDParams(t *testing.T) {
	autoID := &ast.FieldDecl{Name: "id", Type: &ast.TypeRef{Name: "UUID"}, Directives: []*ast.Directive{{Name: "auto"}}}
	internal := &ast.FieldDecl{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}}
	immutable := &ast.FieldDecl{Name: "slug", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "immutable"}}}
	files := []*ast.File{{
		Models: []*ast.ModelDecl{
			{Name: "Post", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
				autoID,
				{Name: "title", Type: &ast.TypeRef{Name: "String"}},
				immutable,
				internal,
				{Name: "author", Type: &ast.TypeRef{Name: "User"}},
			}},
			{Name: "User", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "UUID"}}}},
		},
	}}

	types := buildParamTypesFromAST(files)
	create := types["createPost"]
	update := types["updatePost"]
	if len(create) != 2 || create["title"] != "String" || create["slug"] != "String" {
		t.Fatalf("create types = %v", create)
	}
	if len(update) != 2 || update["id"] != "UUID" || update["title"] != "String" {
		t.Fatalf("update types = %v", update)
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"a\nb\nc", 3},
		{"a\nb\n", 2},
		{"single", 1},
		{"", 0},
		{"\n\n", 2},
	}
	for _, tt := range tests {
		lines := splitLines([]byte(tt.input))
		if len(lines) != tt.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.input, len(lines), tt.want)
		}
	}
}

func TestSplitLinesContent(t *testing.T) {
	lines := splitLines([]byte("hello\nworld"))
	if string(lines[0]) != "hello" || string(lines[1]) != "world" {
		t.Errorf("got %q, %q", string(lines[0]), string(lines[1]))
	}
}

func TestCleanStaleGenFiles(t *testing.T) {
	dir := t.TempDir()
	// Stale generated file — its declaration was removed from the origin,
	// so this run's Files map no longer contains it.
	stale := filepath.Join(dir, "error.gen.go")
	if err := os.WriteFile(stale, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Freshly written generated file — must be kept.
	kept := filepath.Join(dir, "model.gen.go")
	if err := os.WriteFile(kept, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Handwritten file — never touched, even without .gen. suffix match.
	hand := filepath.Join(dir, "helper.go")
	if err := os.WriteFile(hand, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	removed, err := cleanStaleGenFiles(dir, map[string][]byte{"model.gen.go": nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "error.gen.go" {
		t.Errorf("removed = %v, want [error.gen.go]", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale error.gen.go should be deleted")
	}
	if _, err := os.Stat(kept); err != nil {
		t.Error("freshly written model.gen.go must be kept")
	}
	if _, err := os.Stat(hand); err != nil {
		t.Error("handwritten helper.go must never be touched")
	}
}

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	os.WriteFile("go.mod", []byte("module github.com/test/myapp\n\ngo 1.26\n"), 0644)

	path, err := readModulePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "github.com/test/myapp" {
		t.Errorf("got %q", path)
	}
}

func TestReadModulePathNotFound(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// No go.mod
	_, err := readModulePath()
	if err == nil {
		t.Error("expected error when go.mod missing")
	}
}

func TestReadModulePathNoDirective(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	os.WriteFile("go.mod", []byte("go 1.26\n"), 0644)

	_, err := readModulePath()
	if err == nil {
		t.Error("expected error when module directive missing")
	}
}

func TestLoadDialect(t *testing.T) {
	d := loadDialect()
	if d == nil {
		t.Error("default dialect should not be nil")
	}
}

func TestLoadDialectMySQL(t *testing.T) {
	t.Setenv("DATABASE_DRIVER", "mysql")
	d := loadDialect()
	if d == nil {
		t.Error("mysql dialect should fallback to pg")
	}
}

func TestNextMigrationSeq(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// No migrations dir
	seq := nextMigrationSeq()
	if seq != 1 {
		t.Errorf("got %d, want 1", seq)
	}

	// Create some migration files
	os.Mkdir("migrations", 0755)
	os.WriteFile(filepath.Join("migrations", "001_init.sql"), []byte("--"), 0644)
	os.WriteFile(filepath.Join("migrations", "002_add_user.sql"), []byte("--"), 0644)

	seq = nextMigrationSeq()
	if seq != 3 {
		t.Errorf("got %d, want 3", seq)
	}
}

func TestPrintDiffOps(t *testing.T) {
	// Just ensure it doesn't panic for all op types
	ops := []codegen.DiffOp{
		{Kind: codegen.CreateTable, Table: "users"},
		{Kind: codegen.DropTable, Table: "old_table"},
		{Kind: codegen.AddColumn, Table: "users", Column: "email"},
		{Kind: codegen.DropColumn, Table: "users", Column: "old_col"},
		{Kind: codegen.RenameColumn, Table: "users", Column: "new_name", OldName: "old_name"},
		{Kind: codegen.AlterColumn, Table: "users", Column: "age"},
		{Kind: codegen.AddIndex, Index: &lux.IndexInfo{Name: "idx_email"}},
		{Kind: codegen.DropIndex, Index: &lux.IndexInfo{Name: "idx_old"}},
		{Kind: codegen.AddColumn, Table: "users", Column: "x", Warning: "breaking change"},
	}
	// Redirect stdout to discard
	old := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = old }()

	printDiffOps(ops)
}
