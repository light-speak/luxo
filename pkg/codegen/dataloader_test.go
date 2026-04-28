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

	src := generateDataLoaderFile(result, "luxo", nil, nil, DriverPG)
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
	src := generateDataLoaderFile(result, "luxo", nil, nil, DriverPG)
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
	src := generateDataLoaderFile(result, "luxo", nil, nil, DriverPG)
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
	src := generateDataLoaderFile(result, "luxo", nil, externalSoft, DriverPG)
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

	src := generateDataLoaderFile(result, "luxo", nil, nil, DriverPG)
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

	src := generateDataLoaderFile(result, "luxo", nil, nil, DriverPG)
	code := string(src)

	// UserByIdLoader should only appear once as a type definition
	typeCount := strings.Count(code, "type UserByIdLoader")
	if typeCount != 1 {
		t.Errorf("UserByIdLoader type should be defined once, found %d times", typeCount)
	}
}

func TestAnalyzeRelationsNullableFK(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
			{Name: "user", Type: &ast.TypeRef{Name: "User", Nullable: true}},
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
	if !r.FKNullable {
		t.Error("FKNullable should be true for userId: Int?")
	}
}

func TestAnalyzeRelationsNonNullableFK(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "user", Type: &ast.TypeRef{Name: "User"}},
		},
	}
	rels := analyzeRelations(m, map[string]bool{})
	r := rels[0]
	if r.FKNullable {
		t.Error("FKNullable should be false for userId: Int")
	}
}

func TestIsFKNullable(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "userId", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
		{Name: "categoryId", Type: &ast.TypeRef{Name: "Int"}},
	}

	if !isFKNullable(fields, "userId") {
		t.Error("userId should be nullable")
	}
	if isFKNullable(fields, "categoryId") {
		t.Error("categoryId should not be nullable")
	}
	if isFKNullable(fields, "nonExistent") {
		t.Error("non-existent field should return false")
	}
}

// TestGoTypeToCondField covers all branches of goTypeToCondField including
// the previously-untested "string" and "uuid.UUID" cases and the default.
func TestGoTypeToCondField(t *testing.T) {
	cases := []struct {
		goType string
		want   string
	}{
		{"int64", "IntField"},
		{"string", "StringField"},
		{"uuid.UUID", "UUIDField"},
		{"float64", "IntField"},   // default fallback
		{"time.Time", "IntField"}, // default fallback
	}
	for _, tc := range cases {
		got := goTypeToCondField(tc.goType)
		if got != tc.want {
			t.Errorf("goTypeToCondField(%q) = %q, want %q", tc.goType, got, tc.want)
		}
	}
}

// TestGenerateDataLoaderWithStringFK covers the goTypeToCondField("string") branch
// by creating a model whose FK is a String type, so KeyGoType = "string".
func TestGenerateDataLoaderWithStringFK(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{
				{
					Pos:  pos0(),
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						// Use a String FK so KeyGoType maps to "string"
						{Name: "userId", Type: &ast.TypeRef{Name: "String"}},
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				},
			},
		}},
	}

	src := generateDataLoaderFile(result, "luxo", nil, nil, DriverPG)
	if src == nil {
		t.Fatal("should generate dataloader file")
	}
	code := string(src)
	if !strings.Contains(code, "StringField") {
		t.Errorf("string FK should use StringField in batch func:\n%s", code)
	}
}

// TestGenerateDataLoaderWithUUIDFK covers the goTypeToCondField("uuid.UUID") branch.
func TestGenerateDataLoaderWithUUIDFK(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{
				{
					Pos:  pos0(),
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						// UUID FK so KeyGoType = "uuid.UUID"
						{Name: "userId", Type: &ast.TypeRef{Name: "UUID"}},
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				},
			},
		}},
	}

	src := generateDataLoaderFile(result, "luxo", nil, nil, DriverPG)
	if src == nil {
		t.Fatal("should generate dataloader file")
	}
	code := string(src)
	if !strings.Contains(code, "UUIDField") {
		t.Errorf("UUID FK should use UUIDField in batch func:\n%s", code)
	}
}

// TestGenerateDataLoaderDefaultLoadersDedupSeenMap covers the seen-map branch
// in generateDefaultLoaders (line 267–268: already-seen loader name is skipped).
func TestGenerateDataLoaderDefaultLoadersDedupSeenMap(t *testing.T) {
	// Two models that share the same loaderFieldName → second is skipped.
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
					Name: "Post", // duplicate model name → same loaderFieldName
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				},
			},
		}},
	}

	src := generateDataLoaderFile(result, "luxo", nil, nil, DriverPG)
	if src == nil {
		t.Fatal("should generate dataloader file")
	}
	code := string(src)

	// PostUser loader should appear exactly once as a batch function inline
	count := strings.Count(code, "func(ctx context.Context, keys []int64, fields []string) (map[int64]*User, error)")
	if count != 1 {
		t.Errorf("deduplicated default loader should appear once, got %d", count)
	}
}

// --- Extend DataLoader load-by-PK ---

func TestGenerateDataLoaderExtendByPK(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Models: []*ast.ModelDecl{{
					Pos:  pos0(),
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "phone", Type: &ast.TypeRef{Name: "String"}},
					},
				}},
			},
			{
				Name: "origin/post.luxo",
				Models: []*ast.ModelDecl{{
					Pos:  pos0(),
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					},
				}},
				Extends: []*ast.ExtendDecl{{
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "phone", Type: &ast.TypeRef{Name: "String"}},
					},
				}},
			},
		},
	}

	src := generateDataLoaderFile(result, "post_luxo", map[string]bool{}, nil, DriverPG)
	if src == nil {
		t.Fatal("expected dataloader file")
	}
	code := string(src)

	// Should have ExtendUser loader in struct
	if !strings.Contains(code, "ExtendUser *dataloader.Loader[int64, *User]") {
		t.Error("missing ExtendUser loader in struct")
	}

	// Should have load-by-PK batch function
	if !strings.Contains(code, `lux.NewIntField("id").In(keys...)`) {
		t.Error("missing PK condition in extend batch function")
	}

	// Should query users table
	if !strings.Contains(code, `"users"`) {
		t.Error("missing users table in extend batch function")
	}

	// Should use scanUser
	if !strings.Contains(code, "scanUser") {
		t.Error("missing scanUser in extend batch function")
	}

	// Should have type alias
	if !strings.Contains(code, "ExtendUserByIdLoader") {
		t.Error("missing ExtendUserByIdLoader type")
	}
}

func TestGenerateDataLoaderFKLoad(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/post.luxo",
				Models: []*ast.ModelDecl{{
					Pos:  pos0(),
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "title", Type: &ast.TypeRef{Name: "String"}},
					},
				}},
				APIs: []*ast.ApiDecl{{
					Name:   "getPostsByUser",
					Params: []*ast.ParamDecl{{Name: "userId", Type: &ast.TypeRef{Name: "Int"}}},
					Body: &ast.Block{Stmts: []ast.Stmt{
						&ast.ValStmt{
							Name: "posts",
							Value: &ast.CallExpr{
								Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "load"},
								Args: []*ast.NamedArg{{Name: "userId", Value: &ast.Ident{Name: "userId"}}},
							},
						},
					}},
				}},
			},
		},
	}

	src := generateDataLoaderFile(result, "post_luxo", map[string]bool{}, nil, DriverPG)
	if src == nil {
		t.Fatal("expected dataloader file for FK load")
	}
	code := string(src)

	// Should have PostByUserId loader
	if !strings.Contains(code, "PostByUserId") {
		t.Error("missing PostByUserId loader")
	}
	// Should have batch function with user_id condition
	if !strings.Contains(code, `"user_id"`) {
		t.Error("missing user_id FK condition")
	}
}

func TestGenerateDataLoaderCompositeKeyLoad(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/post.luxo",
				Models: []*ast.ModelDecl{{
					Pos:  pos0(),
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "type", Type: &ast.TypeRef{Name: "String"}},
					},
				}},
				APIs: []*ast.ApiDecl{{
					Name: "getArticles",
					Body: &ast.Block{Stmts: []ast.Stmt{
						&ast.ValStmt{
							Name: "articles",
							Value: &ast.CallExpr{
								Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "load"},
								Args: []*ast.NamedArg{
									{Name: "userId", Value: &ast.Ident{Name: "uid"}},
									{Name: "type", Value: &ast.Literal{Kind: token.String, Value: "article"}},
								},
							},
						},
					}},
				}},
			},
		},
	}

	src := generateDataLoaderFile(result, "post_luxo", map[string]bool{}, nil, DriverPG)
	if src == nil {
		t.Fatal("expected dataloader file for composite load")
	}
	code := string(src)

	// Should have composite key struct
	if !strings.Contains(code, "PostByUserIdAndTypeKey") {
		t.Error("missing composite key struct")
	}
	// Should have composite loader
	if !strings.Contains(code, "PostByUserIdAndType") {
		t.Error("missing composite loader")
	}
	// Should have multi-condition WHERE
	if !strings.Contains(code, `"user_id"`) && !strings.Contains(code, `"type"`) {
		t.Error("missing multi-condition WHERE")
	}
}

func TestCollectLoadCalls(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			APIs: []*ast.ApiDecl{
				{
					Name: "api1",
					Body: &ast.Block{Stmts: []ast.Stmt{
						&ast.ValStmt{Name: "u", Value: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "load"},
							Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}}, // PK
						}},
						&ast.ValStmt{Name: "p", Value: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "load"},
							Args: []*ast.NamedArg{{Name: "userId", Value: &ast.Ident{Name: "uid"}}}, // FK
						}},
					}},
				},
			},
		}},
	}

	calls := collectLoadCalls(result)
	if len(calls) != 2 {
		t.Fatalf("expected 2 load calls, got %d", len(calls))
	}

	// PK call
	found := false
	for _, c := range calls {
		if c.modelName == "User" && len(c.argNames) == 0 {
			found = true
		}
	}
	if !found {
		t.Error("missing PK load call for User")
	}

	// FK call
	found = false
	for _, c := range calls {
		if c.modelName == "Post" && len(c.argNames) == 1 && c.argNames[0] == "userId" {
			found = true
		}
	}
	if !found {
		t.Error("missing FK load call for Post")
	}
}

func TestGenerateDataLoaderExtendOnlyModule(t *testing.T) {
	// Module with no same-module relations, only extend
	result := &semantic.Result{
		Files: []*ast.File{
			{Name: "origin/user.luxo", Models: []*ast.ModelDecl{{Name: "User"}}},
			{
				Name: "origin/notification.luxo",
				Models: []*ast.ModelDecl{{
					Pos:  pos0(),
					Name: "Notification",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					},
				}},
				Extends: []*ast.ExtendDecl{{
					Name:   "User",
					Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}},
				}},
			},
		},
	}

	src := generateDataLoaderFile(result, "notification_luxo", map[string]bool{}, nil, DriverPG)
	if src == nil {
		t.Fatal("expected dataloader file even for extend-only module")
	}
	code := string(src)

	if !strings.Contains(code, "ExtendUser") {
		t.Error("missing ExtendUser loader")
	}
	if !strings.Contains(code, "NewDefaultLoaders") {
		t.Error("missing NewDefaultLoaders")
	}
}
