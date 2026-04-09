package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func pos0() token.Position {
	return token.Position{File: "test.luxo", Line: 1, Col: 1}
}

func TestAnalyzeRelationsBelongsTo(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "user", Type: &ast.TypeRef{Name: "User"}},
		},
	}
	rels := analyzeRelations(m, map[string]bool{})
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	r := rels[0]
	if r.Type != BelongsTo {
		t.Errorf("type = %v, want BelongsTo", r.Type)
	}
	if r.LocalKey != "userId" || r.RemoteKey != "id" {
		t.Errorf("keys = (%s, %s), want (userId, id)", r.LocalKey, r.RemoteKey)
	}
}

func TestAnalyzeRelationsHasMany(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
		},
	}
	rels := analyzeRelations(m, map[string]bool{})
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	r := rels[0]
	if r.Type != HasMany {
		t.Errorf("type = %v, want HasMany", r.Type)
	}
	if r.LocalKey != "id" || r.RemoteKey != "userId" {
		t.Errorf("keys = (%s, %s), want (id, userId)", r.LocalKey, r.RemoteKey)
	}
}

func TestAnalyzeRelationsHasOne(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "profile", Type: &ast.TypeRef{Name: "Profile"}},
		},
	}
	rels := analyzeRelations(m, map[string]bool{})
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	r := rels[0]
	if r.Type != HasOne {
		t.Errorf("type = %v, want HasOne", r.Type)
	}
	if r.LocalKey != "id" || r.RemoteKey != "userId" {
		t.Errorf("keys = (%s, %s), want (id, userId)", r.LocalKey, r.RemoteKey)
	}
}

func TestAnalyzeRelationsWithBy(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "authorId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "author", Type: &ast.TypeRef{Name: "User"}, Directives: []*ast.Directive{
				{Name: "by", Args: []*ast.NamedArg{
					{Name: "remote", Value: &ast.Ident{Name: "id"}},
					{Name: "local", Value: &ast.Ident{Name: "authorId"}},
				}},
			}},
		},
	}
	rels := analyzeRelations(m, map[string]bool{})
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	r := rels[0]
	if r.Type != BelongsTo {
		t.Errorf("type = %v, want BelongsTo", r.Type)
	}
	if r.RemoteKey != "id" || r.LocalKey != "authorId" {
		t.Errorf("keys = remote:%s local:%s, want remote:id local:authorId", r.RemoteKey, r.LocalKey)
	}
}

func TestAnalyzeRelationsSkipsEnum(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
		},
	}
	rels := analyzeRelations(m, map[string]bool{"Role": true})
	if len(rels) != 0 {
		t.Errorf("enum field should not be a relation, got %d", len(rels))
	}
}

func TestGenerateDataLoaderFile(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{
				{
					Pos:  pos0(),
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					},
				},
				{
					Pos:  pos0(),
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				},
			},
		}},
	}

	src := generateDataLoaderFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate dataloader file")
	}
	code := string(src)

	checks := []string{
		"PostsByUserIdLoader",
		"UserByIdLoader",
		"type Loaders struct",
		"func (a *App) SetLoaders",
	}
	for _, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing %q in:\n%s", check, code)
		}
	}
}

func TestGenerateDataLoaderFileNoRelations(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  pos0(),
				Name: "Config",
				Fields: []*ast.FieldDecl{
					{Name: "key", Type: &ast.TypeRef{Name: "String"}},
				},
			}},
		}},
	}
	src := generateDataLoaderFile(result, "luxo", nil)
	if src != nil {
		t.Error("should return nil when no relations")
	}
}

func TestLoaderTypeName(t *testing.T) {
	r1 := Relation{TargetName: "Post", RemoteKey: "userId", IsList: true}
	if got := loaderTypeName("User", r1); got != "PostsByUserIdLoader" {
		t.Errorf("got %q", got)
	}

	r2 := Relation{TargetName: "User", RemoteKey: "id", IsList: false}
	if got := loaderTypeName("Post", r2); got != "UserByIdLoader" {
		t.Errorf("got %q", got)
	}
}

func TestLowerFirst(t *testing.T) {
	tests := []struct{ in, want string }{
		{"User", "user"},
		{"Post", "post"},
		{"", ""},
		{"a", "a"},
	}
	for _, tt := range tests {
		if got := lowerFirst(tt.in); got != tt.want {
			t.Errorf("lowerFirst(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractByArgsDefault(t *testing.T) {
	d := &ast.Directive{
		Name: "by",
		Args: []*ast.NamedArg{
			{Name: "remote", Value: &ast.Ident{Name: "uuid"}},
		},
	}
	remote, local := extractByArgs(d)
	if remote != "uuid" || local != "id" {
		t.Errorf("got remote=%s local=%s, want uuid/id", remote, local)
	}
}

func TestExtractByArgsEmpty(t *testing.T) {
	d := &ast.Directive{Name: "by"}
	remote, local := extractByArgs(d)
	if remote != "" || local != "id" {
		t.Errorf("got remote=%s local=%s", remote, local)
	}
}

func TestRelTypeName(t *testing.T) {
	if relTypeName(BelongsTo) != "belongsTo" {
		t.Error("BelongsTo")
	}
	if relTypeName(HasMany) != "hasMany" {
		t.Error("HasMany")
	}
	if relTypeName(HasOne) != "hasOne" {
		t.Error("HasOne")
	}
	if relTypeName(RelationType(99)) != "unknown" {
		t.Error("unknown")
	}
}
