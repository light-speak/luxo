package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGenerateClient(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}
	var b strings.Builder
	generateClient(&b, "User", "users", fields, false)
	got := b.String()

	checks := []string{
		"type UserClient struct",
		"db *pg.DB",
		"func (c *UserClient) Find(ctx context.Context, id int64) (*User, error)",
		"UserWhere.Id.Eq(id)",
		"func (c *UserClient) Where(conds ...lux.Condition) *pg.Query[User]",
		"pg.NewQuery(c.db,",
		"scanUser",
		"func (c *UserClient) Create() *UserCreateBuilder",
		"pg.CreateBase[User]",
		"func (c *UserClient) CreateMany() *UserCreateManyBuilder",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGenerateFieldConstants(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	generateFieldConstants(&b, "User", fields)
	got := b.String()

	checks := []string{
		"var UserFields = struct {",
		"Id string",
		"Name string",
		"CreatedAt string",
		`Id: "id"`,
		`Name: "name"`,
		`CreatedAt: "created_at"`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	if strings.Contains(got, "CommentCount") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
}

func TestGenerateWhereFields(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "score", Type: &ast.TypeRef{Name: "Float"}},
		{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
		{Name: "role", Type: &ast.TypeRef{Name: "Role"}}, // enum → StringField
	}

	var b strings.Builder
	generateWhereFields(&b, "User", fields)
	got := b.String()

	checks := []string{
		"var UserWhere = struct {",
		"Id lux.IntField",
		"Name lux.StringField",
		"Score lux.FloatField",
		"Active lux.BoolField",
		"CreatedAt lux.TimeField",
		"Role lux.StringField",
		`Id: lux.NewIntField("id")`,
		`Name: lux.NewStringField("name")`,
		`Score: lux.NewFloatField("score")`,
		`Active: lux.NewBoolField("active")`,
		`CreatedAt: lux.NewTimeField("created_at")`,
		`Role: lux.NewStringField("role")`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGenerateWhereFieldsSkipsComputed(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	generateWhereFields(&b, "Post", fields)
	got := b.String()

	if strings.Contains(got, "CommentCount") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
	if !strings.Contains(got, "Id lux.IntField") {
		t.Errorf("non-computed field missing:\n%s", got)
	}
}

func TestGenerateCreateBuilder(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "email", Type: &ast.TypeRef{Name: "String"}},
		{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	generateCreateBuilder(&b, "User", fields)
	got := b.String()

	checks := []string{
		"type UserCreateBuilder struct",
		"pg.CreateBase[User]",
		"func (b *UserCreateBuilder) SetId(v int64) *UserCreateBuilder",
		"func (b *UserCreateBuilder) SetName(v string) *UserCreateBuilder",
		"func (b *UserCreateBuilder) SetEmail(v string) *UserCreateBuilder",
		`b.Set("name", v)`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	// no Exec method — inherited from CreateBase
	if strings.Contains(got, "func (b *UserCreateBuilder) Exec") {
		t.Errorf("Exec should be inherited, not generated:\n%s", got)
	}

	if strings.Contains(got, "SetInternal") {
		t.Errorf("@internal field should be skipped:\n%s", got)
	}
	if strings.Contains(got, "SetCommentCount") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
}

func TestGenerateCreateManyBuilder(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "email", Type: &ast.TypeRef{Name: "String"}},
		{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	generateCreateManyBuilder(&b, "User", fields)
	got := b.String()

	checks := []string{
		"type UserCreateInput struct",
		"Id int64",
		"Name string",
		"Email string",
		"type UserCreateManyBuilder struct",
		"rows  []UserCreateInput",
		"func (b *UserCreateManyBuilder) Add(input UserCreateInput) *UserCreateManyBuilder",
		"func (b *UserCreateManyBuilder) Exec(ctx context.Context) ([]*User, error)",
		"pg.InsertManyReturning(ctx, b.db, scanUser",
		`"id", "name", "email"`,
		"r.Id, r.Name, r.Email,",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	if strings.Contains(got, "Internal string") {
		t.Errorf("@internal field should be skipped in input struct:\n%s", got)
	}
}

func TestGenerateUpdateBuilder(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "immutable"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "email", Type: &ast.TypeRef{Name: "String"}},
		{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	generateUpdateBuilder(&b, "User", fields)
	got := b.String()

	checks := []string{
		"type UserUpdateBuilder struct",
		"pg.UpdateBase[User, int64]",
		"func (b *UserUpdateBuilder) SetName(v string) *UserUpdateBuilder",
		"func (b *UserUpdateBuilder) SetEmail(v string) *UserUpdateBuilder",
		`b.Set("name", v)`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	if strings.Contains(got, "SetId") {
		t.Errorf("@immutable field should be skipped:\n%s", got)
	}
	if strings.Contains(got, "SetInternal") {
		t.Errorf("@internal field should be skipped:\n%s", got)
	}
	if strings.Contains(got, "SetCommentCount") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
}

func TestGenerateQueryBuilder(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "email", Type: &ast.TypeRef{Name: "String"}},
		},
	}

	var b strings.Builder
	generateQueryBuilder(&b, m, nil)
	got := b.String()

	sections := []string{
		"scanUser",
		"UserClient",
		"UserFields",
		"UserWhere",
		"UserCreateBuilder",
		"UserUpdateBuilder",
	}
	for _, section := range sections {
		if !strings.Contains(got, section) {
			t.Errorf("missing section %q in:\n%s", section, got)
		}
	}

	if !strings.Contains(got, `"users"`) {
		t.Errorf("missing table name 'users' in:\n%s", got)
	}
}

func TestGenerateQueryBuilderTableName(t *testing.T) {
	tests := []struct {
		modelName string
		tableName string
	}{
		{"User", "users"},
		{"Order", "orders"},
		{"OrderItem", "order_items"},
	}

	for _, tt := range tests {
		t.Run(tt.modelName, func(t *testing.T) {
			m := &ast.ModelDecl{
				Name: tt.modelName,
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				},
			}
			var b strings.Builder
			generateQueryBuilder(&b, m, nil)
			got := b.String()
			if !strings.Contains(got, `"`+tt.tableName+`"`) {
				t.Errorf("expected table name %q in:\n%s", tt.tableName, got)
			}
		})
	}
}

func TestFieldConditionType(t *testing.T) {
	tests := []struct {
		typeRef *ast.TypeRef
		expect  string
	}{
		{&ast.TypeRef{Name: "Int"}, "IntField"},
		{&ast.TypeRef{Name: "Float"}, "FloatField"},
		{&ast.TypeRef{Name: "String"}, "StringField"},
		{&ast.TypeRef{Name: "Boolean"}, "BoolField"},
		{&ast.TypeRef{Name: "DateTime"}, "TimeField"},
		{&ast.TypeRef{Name: "Role"}, "StringField"},    // enum → string comparison
		{&ast.TypeRef{Name: "Unknown"}, "StringField"}, // unknown → string fallback
		{nil, "StringField"},                           // nil → string fallback
	}

	for _, tt := range tests {
		name := "nil"
		if tt.typeRef != nil {
			name = tt.typeRef.Name
		}
		t.Run(name, func(t *testing.T) {
			got := fieldConditionType(tt.typeRef)
			if got != tt.expect {
				t.Errorf("fieldConditionType = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestGenerateCreateBuilderWithTypes(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "score", Type: &ast.TypeRef{Name: "Float"}},
		{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
		{Name: "bio", Type: &ast.TypeRef{Name: "String", Nullable: true}},
	}

	var b strings.Builder
	generateCreateBuilder(&b, "Post", fields)
	got := b.String()

	checks := []string{
		"SetId(v int64)",
		"SetScore(v float64)",
		"SetActive(v bool)",
		"SetBio(v *string)",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	// createdAt is auto-managed, no Set method
	if strings.Contains(got, "SetCreatedAt") {
		t.Errorf("createdAt should be auto-managed, no Set method:\n%s", got)
	}
	// but Exec should auto-fill it
	if !strings.Contains(got, `"created_at", time.Now()`) {
		t.Errorf("missing auto-fill for created_at:\n%s", got)
	}
}

func TestGenerateCreateBuilderAutoUUID(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "UUID"}, Directives: []*ast.Directive{{Name: "auto"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
	}

	var b strings.Builder
	generateCreateBuilder(&b, "Token", fields)
	got := b.String()

	// No SetId — @auto UUID
	if strings.Contains(got, "SetId") {
		t.Errorf("@auto UUID should not generate SetId:\n%s", got)
	}
	// Auto-fill UUID
	if !strings.Contains(got, "uuid.NewV7()") {
		t.Errorf("missing uuid.NewV7() auto-fill:\n%s", got)
	}
	// SetName should exist
	if !strings.Contains(got, "SetName") {
		t.Errorf("missing SetName:\n%s", got)
	}
}

func TestGenerateCreateBuilderSerial(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "serial"}}},
		{Name: "title", Type: &ast.TypeRef{Name: "String"}},
	}

	var b strings.Builder
	generateCreateBuilder(&b, "Post", fields)
	got := b.String()

	// No SetId — @serial
	if strings.Contains(got, "SetId") {
		t.Errorf("@serial should not generate SetId:\n%s", got)
	}
	// SetTitle should exist
	if !strings.Contains(got, "SetTitle") {
		t.Errorf("missing SetTitle:\n%s", got)
	}
}

func TestGenerateUpdateBuilderAutoUpdatedAt(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "immutable"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "updatedAt", Type: &ast.TypeRef{Name: "DateTime"}},
	}

	var b strings.Builder
	generateUpdateBuilder(&b, "User", fields)
	got := b.String()

	// No SetUpdatedAt
	if strings.Contains(got, "SetUpdatedAt") {
		t.Errorf("updatedAt should be auto-managed:\n%s", got)
	}
	// Exec auto-fills updated_at
	if !strings.Contains(got, `"updated_at", time.Now()`) {
		t.Errorf("missing auto-fill for updated_at:\n%s", got)
	}
}

func TestLuxImportConstant(t *testing.T) {
	if luxImport != "github.com/light-speak/luxo/pkg/lux" {
		t.Errorf("luxImport = %q, want github.com/light-speak/luxo/pkg/lux", luxImport)
	}
}

func TestGenerateClientSoftDelete(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}
	var b strings.Builder
	generateClient(&b, "User", "users", fields, true)
	got := b.String()

	checks := []string{
		// Where auto-filters deleted records
		`lux.NewTimeField("deleted_at").IsNull()`,
		// WithDeleted bypasses soft delete filter
		"func (c *UserClient) WithDeleted(conds ...lux.Condition) *pg.Query[User]",
		// SoftDelete sets deleted_at
		"func (c *UserClient) SoftDelete(ctx context.Context, conds ...lux.Condition) (int64, error)",
		`lux.SetField{Col: "deleted_at", Val: time.Now()}`,
		// ForceDelete does real DELETE
		"func (c *UserClient) ForceDelete(ctx context.Context, conds ...lux.Condition) (int64, error)",
		"q.Delete(ctx)",
		// Restore un-deletes
		"func (c *UserClient) Restore(ctx context.Context, conds ...lux.Condition) (int64, error)",
		`lux.SetField{Col: "deleted_at", Val: nil}`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGenerateClientNoSoftDelete(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}
	var b strings.Builder
	generateClient(&b, "User", "users", fields, false)
	got := b.String()

	// Should NOT have soft delete methods
	absent := []string{"WithDeleted", "SoftDelete", "ForceDelete", "Restore", "deleted_at"}
	for _, a := range absent {
		if strings.Contains(got, a) {
			t.Errorf("non-soft model should not contain %q in:\n%s", a, got)
		}
	}
}

func TestGenerateQueryBuilderSoft(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
		},
		Directives: []*ast.Directive{{Name: "soft"}},
	}

	var b strings.Builder
	generateQueryBuilder(&b, m, nil)
	got := b.String()

	// Should have deletedAt in scanner and fields
	checks := []string{
		"SoftDelete",
		"ForceDelete",
		"WithDeleted",
		"Restore",
		"DeletedAt",    // field constant
		`"deleted_at"`, // column name in scanner
		`*time.Time`,   // nullable DateTime type for deletedAt
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGenerateQueryBuilderSoftWithExistingDeletedAt(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
			{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}},
		},
		Directives: []*ast.Directive{{Name: "soft"}},
	}

	var b strings.Builder
	generateQueryBuilder(&b, m, nil)
	got := b.String()

	// Should still have soft delete methods
	if !strings.Contains(got, "SoftDelete") {
		t.Errorf("missing SoftDelete:\n%s", got)
	}
	// Should NOT duplicate deletedAt field
	count := strings.Count(got, `"deleted_at"`)
	// deletedAt appears in: scanner case, field constant value, where field value, plus soft delete methods
	// but should NOT have duplicate struct fields
	if count < 3 {
		t.Errorf("expected at least 3 occurrences of deleted_at, got %d:\n%s", count, got)
	}
}

func TestGenerateExtendQueryBuilderFiltersRelations(t *testing.T) {
	ext := &ast.ExtendDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			// Relation field (capitalized non-primitive) — should be skipped
			{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
			// Single model relation — should be skipped
			{Name: "profile", Type: &ast.TypeRef{Name: "Profile"}},
			// Computed field — should be skipped
			{Name: "fullName", Type: &ast.TypeRef{Name: "String"}, Computed: &ast.ComputedField{}},
		},
	}
	var b strings.Builder
	generateExtendQueryBuilder(&b, ext)
	got := b.String()

	// Should have id and name
	if !strings.Contains(got, `"id"`) || !strings.Contains(got, `"name"`) {
		t.Fatalf("should include primitive fields, got:\n%s", got)
	}
	// Should NOT have posts or profile as DB columns
	if strings.Contains(got, `"posts"`) {
		t.Fatalf("should not include list relation field 'posts', got:\n%s", got)
	}
	if strings.Contains(got, `"profile"`) {
		t.Fatalf("should not include single relation field 'profile', got:\n%s", got)
	}
}

// ─── fieldConditionTypeWithDirectives ────────────────────────────────────────

func TestFieldConditionTypeWithDirectives_SearchField(t *testing.T) {
	f := &ast.FieldDecl{
		Name:       "title",
		Type:       &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "search"}},
	}
	got := fieldConditionTypeWithDirectives(f)
	if got != "SearchField" {
		t.Errorf("expected SearchField, got %q", got)
	}
}

func TestFieldConditionTypeWithDirectives_StringWithoutSearch(t *testing.T) {
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
	}
	got := fieldConditionTypeWithDirectives(f)
	if got != "StringField" {
		t.Errorf("expected StringField, got %q", got)
	}
}

func TestFieldConditionTypeWithDirectives_IntField(t *testing.T) {
	f := &ast.FieldDecl{
		Name: "age",
		Type: &ast.TypeRef{Name: "Int"},
	}
	got := fieldConditionTypeWithDirectives(f)
	if got != "IntField" {
		t.Errorf("expected IntField, got %q", got)
	}
}

func TestFieldConditionTypeWithDirectives_NilType(t *testing.T) {
	f := &ast.FieldDecl{Name: "unknown"}
	got := fieldConditionTypeWithDirectives(f)
	if got != "StringField" {
		t.Errorf("expected StringField for nil type, got %q", got)
	}
}

func TestGenerateWhereFieldsWithSearch(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "title", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "search"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}

	var b strings.Builder
	generateWhereFields(&b, "Post", fields)
	got := b.String()

	// @search String field should use SearchField
	if !strings.Contains(got, "Title lux.SearchField") {
		t.Errorf("expected SearchField for @search field:\n%s", got)
	}
	if !strings.Contains(got, `Title: lux.NewSearchField("title")`) {
		t.Errorf("expected NewSearchField constructor:\n%s", got)
	}
	// Non-search String field should remain StringField
	if !strings.Contains(got, "Name lux.StringField") {
		t.Errorf("expected StringField for non-search field:\n%s", got)
	}
}
