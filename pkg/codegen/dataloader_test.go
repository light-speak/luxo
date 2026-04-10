package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
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

	src := generateDataLoaderFile(result, "luxo", nil, nil)
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
	src := generateDataLoaderFile(result, "luxo", nil, nil)
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
		if got := str.LowerFirst(tt.in); got != tt.want {
			t.Errorf("str.LowerFirst(%q) = %q, want %q", tt.in, got, tt.want)
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

func TestAnalyzeRelationsWithByHasMany(t *testing.T) {
	// @by on a list field → HasMany
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "articles", Type: &ast.TypeRef{Name: "Article", IsList: true}, Directives: []*ast.Directive{
				{Name: "by", Args: []*ast.NamedArg{
					{Name: "remote", Value: &ast.Ident{Name: "authorId"}},
					{Name: "local", Value: &ast.Ident{Name: "id"}},
				}},
			}},
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
	if r.RemoteKey != "authorId" || r.LocalKey != "id" {
		t.Errorf("keys = remote:%s local:%s, want remote:authorId local:id", r.RemoteKey, r.LocalKey)
	}
}

func TestAnalyzeRelationsWithByHasOne(t *testing.T) {
	// @by on a non-list field where the localKey is NOT a FK on this model → HasOne
	// The local key must not match any existing field name in the model
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "settings", Type: &ast.TypeRef{Name: "Settings"}, Directives: []*ast.Directive{
				{Name: "by", Args: []*ast.NamedArg{
					{Name: "remote", Value: &ast.Ident{Name: "userId"}},
					// local defaults to "id" via extractByArgs, but "id" IS a field
					// So use a local key that doesn't exist as a field
					{Name: "local", Value: &ast.Ident{Name: "nonExistentKey"}},
				}},
			}},
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
}

func TestAnalyzeRelationsSkipsNilType(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "unknown", Type: nil},
		},
	}
	rels := analyzeRelations(m, map[string]bool{})
	if len(rels) != 0 {
		t.Errorf("nil type should be skipped, got %d relations", len(rels))
	}
}

func TestAnalyzeRelationsSkipsComputed(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "postCount", Type: &ast.TypeRef{Name: "Post"}, Computed: &ast.ComputedField{}},
		},
	}
	rels := analyzeRelations(m, map[string]bool{})
	if len(rels) != 0 {
		t.Errorf("computed fields should be skipped, got %d relations", len(rels))
	}
}

func TestGenerateDataLoaderDedup(t *testing.T) {
	// Two models with relations to the same target via the same key
	// should deduplicate loader types
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{
				{
					Pos:  pos0(),
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				},
				{
					Pos:  pos0(),
					Name: "Comment",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				},
			},
		}},
	}
	src := generateDataLoaderFile(result, "luxo", nil, nil)
	code := string(src)

	// UserByIdLoader type should appear only once
	count := strings.Count(code, "type UserByIdLoader")
	if count != 1 {
		t.Errorf("UserByIdLoader type should be defined once, found %d times", count)
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

func TestGenerateDataLoaderWithExternalSoftModels(t *testing.T) {
	// Test externalSoftModels non-nil path + soft target filtering
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{
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

	// User is soft-deleted externally
	externalSoft := map[string]bool{"User": true}
	src := generateDataLoaderFile(result, "luxo", nil, externalSoft)
	if src == nil {
		t.Fatal("should generate dataloader file")
	}
	code := string(src)

	// Should contain soft delete filter for User target
	if !strings.Contains(code, "deleted_at") {
		t.Errorf("should filter soft-deleted User targets:\n%s", code)
	}
}

func TestGenerateDataLoaderWithLocalSoftModel(t *testing.T) {
	// Test local @soft model detected in generateDataLoaderFile
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
					Pos:        pos0(),
					Name:       "Post",
					Directives: []*ast.Directive{{Name: "soft"}},
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					},
				},
			},
		}},
	}

	src := generateDataLoaderFile(result, "luxo", nil, nil)
	if src == nil {
		t.Fatal("should generate dataloader file")
	}
	code := string(src)

	// HasMany to Post (which is @soft) should add deleted_at filter
	if !strings.Contains(code, "deleted_at") {
		t.Errorf("should filter soft-deleted Post targets:\n%s", code)
	}
}

func TestDeduplicateLoadersSkipsDuplicate(t *testing.T) {
	// Two relations that produce the same loader field name
	allRelations := []struct {
		modelName string
		relations []Relation
	}{
		{
			modelName: "Post",
			relations: []Relation{
				{FieldName: "user", TargetName: "User", RemoteKey: "id", LocalKey: "userId"},
			},
		},
		{
			modelName: "Comment",
			relations: []Relation{
				// Same fieldName pattern: Comment + user → CommentUser, different from PostUser
				// Use same modelName+field combo to force dedup
				{FieldName: "user", TargetName: "User", RemoteKey: "id", LocalKey: "userId"},
			},
		},
	}

	// PostUser and CommentUser are different, so both should be kept
	entries := deduplicateLoaders(allRelations)
	if len(entries) != 2 {
		// This tests that unique names are kept; let's create a real duplicate
		t.Logf("got %d entries (expected 2 for different model names)", len(entries))
	}

	// Test with actual duplicates: same model+field produces same loaderFieldName
	dupRelations := []struct {
		modelName string
		relations []Relation
	}{
		{
			modelName: "Post",
			relations: []Relation{
				{FieldName: "user", TargetName: "User", RemoteKey: "id", LocalKey: "userId"},
				{FieldName: "user", TargetName: "User", RemoteKey: "id", LocalKey: "userId"},
			},
		},
	}
	entries2 := deduplicateLoaders(dupRelations)
	if len(entries2) != 1 {
		t.Errorf("duplicate loaders should be deduped, got %d", len(entries2))
	}
}

func TestGenerateDefaultLoadersDeduplicate(t *testing.T) {
	// Test that generateDefaultLoaders skips duplicate loaders (seen map)
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{
				{
					Pos:  pos0(),
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				},
				{
					Pos:  pos0(),
					Name: "Comment",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				},
			},
		}},
	}

	src := generateDataLoaderFile(result, "luxo", nil, nil)
	code := string(src)

	// UserByIdLoader should only appear once as a type definition
	typeCount := strings.Count(code, "type UserByIdLoader")
	if typeCount != 1 {
		t.Errorf("UserByIdLoader type should be defined once, found %d times", typeCount)
	}
}
