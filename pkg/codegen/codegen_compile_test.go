package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

// TestGeneratedCodeCompiles generates a model exercising every scalar type,
// scalar arrays ([T]), and UUID (16-byte fixed), then compiles the output to
// verify the generated model/db/writejson/schema code is valid, type-correct Go.
// This is the end-to-end guard for the array + UUID codegen changes.
func TestGeneratedCodeCompiles(t *testing.T) {
	modelIDs := map[string]map[string]int{
		"Doc": {
			"id": 1, "name": 2, "active": 3, "uid": 4, "ouid": 5,
			"created": 6, "dur": 7, "price": 8, "data": 9,
			"tags": 10, "scores": 11, "ids": 12, "ratings": 13, "flags": 14, "times": 15,
			"metadata": 16, "childId": 17, "child": 18, "children": 19,
		},
		"Child": {"id": 1, "docId": 2, "name": 3},
	}
	eventIDs := map[string]map[string]int{
		"DocChanged": {
			"doc": 1, "at": 2, "ttl": 3, "uid": 4, "price": 5,
			"metadata": 6, "docs": 7, "labels": 8,
		},
	}

	file := &ast.File{
		Name: "test.luxo",
		Models: []*ast.ModelDecl{{
			Name: "Doc",
			Fields: []*ast.FieldDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "name", Type: &ast.TypeRef{Name: "String"}},
				{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
				{Name: "uid", Type: &ast.TypeRef{Name: "UUID"}},
				{Name: "ouid", Type: &ast.TypeRef{Name: "UUID", Nullable: true}},
				{Name: "created", Type: &ast.TypeRef{Name: "DateTime"}},
				{Name: "dur", Type: &ast.TypeRef{Name: "Duration"}},
				{Name: "price", Type: &ast.TypeRef{Name: "Decimal"}},
				{Name: "data", Type: &ast.TypeRef{Name: "Bytes"}},
				{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}},
				{Name: "scores", Type: &ast.TypeRef{Name: "Int", IsList: true}},
				{Name: "ids", Type: &ast.TypeRef{Name: "UUID", IsList: true}},
				{Name: "ratings", Type: &ast.TypeRef{Name: "Float", IsList: true}},
				{Name: "flags", Type: &ast.TypeRef{Name: "Boolean", IsList: true}},
				{Name: "times", Type: &ast.TypeRef{Name: "DateTime", IsList: true}},
				{Name: "metadata", Type: &ast.TypeRef{Name: "JSON"}},
				{Name: "childId", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
				{Name: "child", Type: &ast.TypeRef{Name: "Child", Nullable: true}},
				{Name: "children", Type: &ast.TypeRef{Name: "Child", IsList: true}},
			},
		}, {
			Name: "Child",
			Fields: []*ast.FieldDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "docId", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			},
		}},
		Events: []*ast.EventDecl{{
			Name: "DocChanged",
			Params: []*ast.ParamDecl{
				{Name: "doc", Type: &ast.TypeRef{Name: "Doc"}},
				{Name: "at", Type: &ast.TypeRef{Name: "DateTime"}},
				{Name: "ttl", Type: &ast.TypeRef{Name: "Duration"}},
				{Name: "uid", Type: &ast.TypeRef{Name: "UUID"}},
				{Name: "price", Type: &ast.TypeRef{Name: "Decimal"}},
				{Name: "metadata", Type: &ast.TypeRef{Name: "JSON"}},
				{Name: "docs", Type: &ast.TypeRef{Name: "Doc", IsList: true}},
				{Name: "labels", Type: &ast.TypeRef{Name: "String", IsList: true}},
			},
		}},
		APIs: []*ast.ApiDecl{
			{
				Name:       "watchDoc",
				Params:     []*ast.ParamDecl{{Name: "uid", Type: &ast.TypeRef{Name: "UUID"}}},
				ReturnType: &ast.TypeRef{Name: "Doc"},
				Directives: []*ast.Directive{{Name: "stream", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "DocChanged"}}}}},
				Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BinaryExpr{
					Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "uid"},
					Op:    "==",
					Right: &ast.Ident{Name: "uid"},
				}}}},
			},
			{
				Name:       "watchNativeDoc",
				ReturnType: &ast.TypeRef{Name: "Doc"},
				Directives: []*ast.Directive{{Name: "stream"}, {Name: "native"}},
			},
		},
	}

	generator, err := NewGenerator(GeneratorConfig{IDs: StableIDs{ModelFields: modelIDs, EventFields: eventIDs}})
	if err != nil {
		t.Fatal(err)
	}
	gr, err := generator.Generate(result(file), "gentest", nil)
	if err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Temp dir inside the luxo module so generated imports resolve via go.mod.
	tmpRoot := filepath.Clean(filepath.Join(wd, "..", "..", ".tmp"))
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(tmpRoot, "genbuild-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Self-contained files (no user resolver / API dependency).
	selfContained := map[string]bool{
		"model.gen.go":     true,
		"db.gen.go":        true,
		"writejson.gen.go": true,
		"schema.gen.go":    true,
		"event.gen.go":     true,
		"stream.gen.go":    true,
	}
	if err := os.WriteFile(filepath.Join(dir, "app_stub.go"), []byte("package gentest\n\ntype App struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, src := range gr.Files {
		if !selfContained[name] {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("go", "build", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated code failed to compile:\n%s", out)
	}
}
