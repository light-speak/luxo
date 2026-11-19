package inferred

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestPrimaryKeyField(t *testing.T) {
	plain := &ast.FieldDecl{Name: "id", Type: &ast.TypeRef{Name: "Int"}}
	custom := &ast.FieldDecl{Name: "key", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "id"}}}
	for _, sample := range []struct {
		model *ast.ModelDecl
		want  *ast.FieldDecl
	}{
		{nil, nil}, {&ast.ModelDecl{}, nil},
		{&ast.ModelDecl{Fields: []*ast.FieldDecl{{Name: "id"}}}, nil},
		{&ast.ModelDecl{Fields: []*ast.FieldDecl{plain}}, plain},
		{&ast.ModelDecl{Fields: []*ast.FieldDecl{plain, custom}}, custom},
		{&ast.ModelDecl{Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "unique"}}}}}, nil},
	} {
		if got := PrimaryKeyField(sample.model); got != sample.want {
			t.Fatalf("key=%v want=%v", got, sample.want)
		}
		want := "id"
		if sample.want != nil {
			want = sample.want.Name
		}
		if got := PrimaryKeyName(sample.model); got != want {
			t.Fatalf("name=%s want=%s", got, want)
		}
	}
}

func TestImplicitRelationKeys(t *testing.T) {
	owner := &ast.ModelDecl{Name: "Source", Fields: []*ast.FieldDecl{
		{Name: "key", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "id"}}},
		{Name: "targetUuid", Type: &ast.TypeRef{Name: "UUID"}},
	}}
	for _, sample := range []struct {
		list                     bool
		targetKey, local, remote string
		belongs                  bool
	}{
		{false, "uuid", "targetUuid", "uuid", true},
		{true, "uuid", "key", "sourceKey", false},
		{false, "id", "key", "sourceKey", false},
	} {
		local, remote, belongs := ImplicitRelationKeys(owner, &ast.FieldDecl{Type: &ast.TypeRef{Name: "Target", IsList: sample.list}}, sample.targetKey)
		if local != sample.local || remote != sample.remote || belongs != sample.belongs {
			t.Fatalf("keys=%s/%s/%v want=%+v", local, remote, belongs, sample)
		}
	}
	for _, field := range []*ast.FieldDecl{nil, {}} {
		local, remote, belongs := ImplicitRelationKeys(owner, field, "id")
		if local != "" || remote != "" || belongs {
			t.Fatal("incomplete field inferred a relation")
		}
	}
	if local, _, _ := ImplicitRelationKeys(nil, nil, "id"); local != "" {
		t.Fatal("nil owner inferred a key")
	}
	if got := ForeignKeyName("Équipe", "clé"); got != "équipeClé" {
		t.Fatal(got)
	}
}

func TestForeignKeyNameBoundaries(t *testing.T) {
	for _, sample := range []struct{ model, key, want string }{
		{"", "", ""}, {"", "id", "Id"}, {"Source", "", "source"},
		{"Équipe", "clé", "équipeClé"}, {"Source", "ȿ", "sourceⱾ"},
	} {
		if got := ForeignKeyName(sample.model, sample.key); got != sample.want {
			t.Fatalf("%q/%q = %q want %q", sample.model, sample.key, got, sample.want)
		}
	}
}
