package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenerateModel(t *testing.T) {
	tests := []struct {
		name   string
		model  *ast.ModelDecl
		checks []string
		absent []string
	}{
		{
			name: "basic model",
			model: &ast.ModelDecl{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "email", Type: &ast.TypeRef{Name: "String"}},
				},
			},
			checks: []string{"type User struct", `db:"id"`, `db:"name"`, `db:"email"`, `json:"id"`, `json:"name"`},
		},
		{
			name: "nullable and list fields",
			model: &ast.ModelDecl{
				Name: "Post",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "title", Type: &ast.TypeRef{Name: "String"}},
					{Name: "content", Type: &ast.TypeRef{Name: "String", Nullable: true}},
					{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}},
				},
			},
			checks: []string{"type Post struct", "*string", "[]string", `db:"content"`, `db:"tags"`},
		},
		{
			name: "hidden and internal fields",
			model: &ast.ModelDecl{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
					{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
				},
			},
			checks: []string{`json:"-"`},
		},
		{
			name: "camelCase to snake_case",
			model: &ast.ModelDecl{
				Name: "Order",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "orderNo", Type: &ast.TypeRef{Name: "String"}},
					{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
				},
			},
			checks: []string{`db:"order_no"`, `db:"user_id"`, `db:"created_at"`, `json:"orderNo"`, "time.Time"},
		},
		{
			name: "enum field type",
			model: &ast.ModelDecl{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
					{Name: "status", Type: &ast.TypeRef{Name: "Status", Nullable: true}},
				},
			},
			checks: []string{"Role ", "*Status ", `db:"role"`, `db:"status"`},
		},
		{
			name: "computed fields are response-only",
			model: &ast.ModelDecl{
				Name: "Post",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "title", Type: &ast.TypeRef{Name: "String"}},
					{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{
						Directives: []*ast.Directive{{Name: "count"}},
					}},
				},
			},
			checks: []string{"type Post struct", "Title", "Id", "CommentCount", `json:"commentCount"`},
			absent: []string{`db:"comment_count"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			generateModel(&b, tt.model, map[string]bool{"Status": true, "Role": true})
			got := b.String()
			for _, check := range tt.checks {
				if !strings.Contains(got, check) {
					t.Errorf("missing %q in:\n%s", check, got)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(got, a) {
					t.Errorf("should not contain %q in:\n%s", a, got)
				}
			}
		})
	}
}

func TestPrimaryKeyFieldNilModel(t *testing.T) {
	if primaryKeyField(nil) != nil {
		t.Fatal("nil model must not have a primary key")
	}
}

func TestResolveGoType(t *testing.T) {
	tests := []struct {
		typeRef *ast.TypeRef
		expect  string
	}{
		{&ast.TypeRef{Name: "Int"}, "int64"},
		{&ast.TypeRef{Name: "Float"}, "float64"},
		{&ast.TypeRef{Name: "String"}, "string"},
		{&ast.TypeRef{Name: "Boolean"}, "bool"},
		{&ast.TypeRef{Name: "DateTime"}, "time.Time"},
		{&ast.TypeRef{Name: "Duration"}, "time.Duration"},
		{&ast.TypeRef{Name: "UUID"}, "uuid.UUID"},
		{&ast.TypeRef{Name: "Decimal"}, "decimal.Decimal"},
		{&ast.TypeRef{Name: "Bytes"}, "[]byte"},
		{&ast.TypeRef{Name: "JSON"}, "json.RawMessage"},
		{&ast.TypeRef{Name: "String", Nullable: true}, "*string"},
		{&ast.TypeRef{Name: "Int", Nullable: true}, "*int64"},
		{&ast.TypeRef{Name: "UUID", Nullable: true}, "*uuid.UUID"},
		{&ast.TypeRef{Name: "String", IsList: true}, "[]string"},
		{&ast.TypeRef{Name: "Int", IsList: true}, "[]int64"},
		{&ast.TypeRef{Name: "Role"}, "Role"},
		{&ast.TypeRef{Name: "Role", Nullable: true}, "*Role"},
		{nil, "any"},
	}

	for _, tt := range tests {
		name := "nil"
		if tt.typeRef != nil {
			name = tt.typeRef.Name
			if tt.typeRef.Nullable {
				name += "?"
			}
			if tt.typeRef.IsList {
				name = "[" + name + "]"
			}
		}
		t.Run(name, func(t *testing.T) {
			got := resolveGoType(tt.typeRef)
			if got != tt.expect {
				t.Errorf("resolveGoType = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"id", "id"},
		{"name", "name"},
		{"userId", "user_id"},
		{"orderNo", "order_no"},
		{"createdAt", "created_at"},
		{"deletedAt", "deleted_at"},
		{"firstName", "first_name"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := str.ToSnakeCase(tt.input)
			if got != tt.expect {
				t.Errorf("str.ToSnakeCase(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"id", "Id"},
		{"name", "Name"},
		{"userId", "UserId"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := str.Capitalize(tt.input)
			if got != tt.expect {
				t.Errorf("str.Capitalize(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestIsSoftDelete(t *testing.T) {
	soft := &ast.ModelDecl{Directives: []*ast.Directive{{Name: "soft"}}}
	if !isSoftDelete(soft) {
		t.Error("should be soft delete")
	}

	notSoft := &ast.ModelDecl{Directives: []*ast.Directive{{Name: "unique"}}}
	if isSoftDelete(notSoft) {
		t.Error("should not be soft delete")
	}

	none := &ast.ModelDecl{}
	if isSoftDelete(none) {
		t.Error("should not be soft delete with no directives")
	}
}

func TestHasDeletedAtField(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id"},
		{Name: "deletedAt"},
	}
	if !hasDeletedAtField(fields) {
		t.Error("should find deletedAt")
	}
	if hasDeletedAtField(fields[:1]) {
		t.Error("should not find deletedAt")
	}
}

func TestSoftDeleteField(t *testing.T) {
	f := softDeleteField()
	if f.Name != "deletedAt" {
		t.Errorf("name = %q", f.Name)
	}
	if f.Type.Name != "DateTime" {
		t.Errorf("type = %q", f.Type.Name)
	}
	if !f.Type.Nullable {
		t.Error("should be nullable")
	}
}

func TestGenerateModelWithSoftDelete(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
		},
		Directives: []*ast.Directive{{Name: "soft"}},
	}

	var b strings.Builder
	generateModel(&b, m, nil)
	got := b.String()

	if !strings.Contains(got, "DeletedAt") {
		t.Errorf("missing DeletedAt:\n%s", got)
	}
	if !strings.Contains(got, "*time.Time") {
		t.Errorf("missing *time.Time:\n%s", got)
	}
}

func TestGenerateModelSoftDeleteExistingField(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}},
		},
		Directives: []*ast.Directive{{Name: "soft"}},
	}

	var b strings.Builder
	generateModel(&b, m, nil)
	got := b.String()

	// Should NOT duplicate DeletedAt
	count := strings.Count(got, "DeletedAt")
	if count != 1 {
		t.Errorf("DeletedAt should appear once, got %d:\n%s", count, got)
	}
}

func TestGenerateModelRelationPointer(t *testing.T) {
	// Single model reference should use pointer type
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "user", Type: &ast.TypeRef{Name: "User"}},
		},
	}

	var b strings.Builder
	generateModel(&b, m, nil) // no enums, so User is a relation
	got := b.String()

	// Single relation should use *User pointer
	if !strings.Contains(got, "*User") {
		t.Errorf("single relation should use pointer type:\n%s", got)
	}
	// Relation field should have json tag only, no db tag
	if !strings.Contains(got, "`json:\"user\"`") {
		t.Errorf("relation field should have json-only tag:\n%s", got)
	}
}

func TestGenerateModelNullableRelationPointer(t *testing.T) {
	// Nullable single relation: user: User? → should be *User, NOT **User
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
			{Name: "user", Type: &ast.TypeRef{Name: "User", Nullable: true}},
		},
	}

	var b strings.Builder
	generateModel(&b, m, nil)
	got := b.String()

	// Should be *User, not **User
	if strings.Contains(got, "**User") {
		t.Errorf("nullable relation should be *User, not **User:\n%s", got)
	}
	if !strings.Contains(got, "*User") {
		t.Errorf("nullable relation should use *User pointer:\n%s", got)
	}
}

func TestGenerateModelRelationList(t *testing.T) {
	// List relation field
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
		},
	}

	var b strings.Builder
	generateModel(&b, m, nil)
	got := b.String()

	// List relation should use []Post (not pointer)
	if !strings.Contains(got, "[]Post") {
		t.Errorf("list relation should use slice type:\n%s", got)
	}
	// Should have json-only tag (db:"-" → json only)
	if !strings.Contains(got, "`json:\"posts\"`") {
		t.Errorf("list relation should have json-only tag:\n%s", got)
	}
}

func TestGenerateExtendStub(t *testing.T) {
	ext := &ast.ExtendDecl{
		Name: "ExternalUser",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "email", Type: &ast.TypeRef{Name: "String"}},
		},
	}

	var b strings.Builder
	generateExtendStub(&b, ext)
	got := b.String()

	if !strings.Contains(got, "type ExternalUser struct") {
		t.Errorf("missing struct declaration:\n%s", got)
	}
	if !strings.Contains(got, `db:"id"`) {
		t.Errorf("missing db tag:\n%s", got)
	}
	if !strings.Contains(got, `json:"email"`) {
		t.Errorf("missing json tag:\n%s", got)
	}
	if !strings.Contains(got, "stub for the external ExternalUser model") {
		t.Errorf("missing doc comment:\n%s", got)
	}
}

func TestGenerateExtendStubWithComputed(t *testing.T) {
	ext := &ast.ExtendDecl{
		Name: "ExternalUser",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "computed", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
		},
	}

	var b strings.Builder
	generateExtendStub(&b, ext)
	got := b.String()

	if !strings.Contains(got, "Computed") || strings.Contains(got, `db:"computed"`) {
		t.Errorf("computed extend field should be response-only:\n%s", got)
	}
}

func TestGenerateExtendStubAutoId(t *testing.T) {
	// extend without explicit id — should auto-inject Id int64
	// Use short field name "x" with type "bool" (4 chars < "int64" 5 chars)
	// so Id/int64 become the longest columns (covers maxName/maxType branches)
	ext := &ast.ExtendDecl{
		Name: "T",
		Fields: []*ast.FieldDecl{
			{Name: "x", Type: &ast.TypeRef{Name: "Boolean"}},
		},
	}

	var b strings.Builder
	generateExtendStub(&b, ext)
	got := b.String()

	if !strings.Contains(got, "Id") || !strings.Contains(got, "int64") {
		t.Errorf("extend stub without id should auto-inject Id int64:\n%s", got)
	}
	if !strings.Contains(got, `db:"id"`) {
		t.Errorf("auto Id should have db tag:\n%s", got)
	}
}

func TestGenerateExtendStubUsesRemoteUUIDID(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{ModelIDType: map[string]string{"Account": "UUID"}}})
	ext := &ast.ExtendDecl{
		Name: "Account",
		Fields: []*ast.FieldDecl{{
			Name: "name",
			Type: &ast.TypeRef{Name: "String"},
		}},
	}

	var b strings.Builder
	generator.generateExtendStub(&b, ext)
	if got := b.String(); !strings.Contains(got, "uuid.UUID") || !strings.Contains(got, `db:"id"`) {
		t.Errorf("remote UUID ID type was not preserved:\n%s", got)
	}
}

// suppress unused import warning
var _ = token.Position{}

func TestGenerateModelVerifyPassword(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
		},
	}
	var b strings.Builder
	generateModel(&b, m, nil)
	out := b.String()
	if !strings.Contains(out, "VerifyPassword") {
		t.Errorf("@hash should generate VerifyPassword: %s", out)
	}
	if !strings.Contains(out, "luxocrypto.VerifyPassword") {
		t.Errorf("should call luxocrypto.VerifyPassword: %s", out)
	}
}

func TestGenerateModelNoVerifyWithoutHash(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
		},
	}
	var b strings.Builder
	generateModel(&b, m, nil)
	if strings.Contains(b.String(), "Verify") {
		t.Error("no @hash should not generate Verify method")
	}
}

func TestGenerateTypeStruct(t *testing.T) {
	var b strings.Builder
	td := &ast.TypeDecl{
		Name: "AuthPayload",
		Fields: []*ast.FieldDecl{
			{Name: "member", Type: &ast.TypeRef{Name: "Member"}},
			{Name: "token", Type: &ast.TypeRef{Name: "String"}},
		},
	}
	generateTypeStruct(&b, td, nil)
	out := b.String()
	if !strings.Contains(out, "type AuthPayload struct") {
		t.Errorf("missing struct: %s", out)
	}
	if !strings.Contains(out, "*Member") || !strings.Contains(out, "json:\"member\"") {
		t.Errorf("missing Member pointer field: %s", out)
	}
	if !strings.Contains(out, "Token") || !strings.Contains(out, "string") || !strings.Contains(out, "json:\"token\"") {
		t.Errorf("missing Token field: %s", out)
	}
	// Should have json tags but NOT db tags
	if strings.Contains(out, "db:") {
		t.Errorf("type struct should not have db tags: %s", out)
	}
}

func TestGenerateModelWithAuth(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "Member",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "auto"}, {Name: "serial"}}},
			{Name: "role", Type: &ast.TypeRef{Name: "String"}},
		},
		Directives: []*ast.Directive{
			{Name: "withAuth", Args: []*ast.NamedArg{
				{Name: "stores", Value: &ast.ListExpr{Items: []ast.Expr{
					&ast.Ident{Name: "id"},
					&ast.Ident{Name: "role"},
				}}},
			}},
		},
	}
	generateModel(&b, m, nil)
	out := b.String()
	if !strings.Contains(out, "func (m *Member) CreateToken() string") {
		t.Errorf("missing CreateToken: %s", out)
	}
	if !strings.Contains(out, "func (m *Member) RefreshToken(oldToken string) string") {
		t.Errorf("missing RefreshToken: %s", out)
	}
	if !strings.Contains(out, `"id": m.Id`) {
		t.Errorf("missing id in data map: %s", out)
	}
	if !strings.Contains(out, `"role": m.Role`) {
		t.Errorf("missing role in data map: %s", out)
	}
	if !strings.Contains(out, "auth.Sign(cfg, data)") {
		t.Errorf("missing auth.Sign call: %s", out)
	}
	if !strings.Contains(out, "auth.Verify(cfg, oldToken)") {
		t.Errorf("missing auth.Verify call: %s", out)
	}
}

func TestGenerateModelWithAuthNoStores(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}},
		},
		Directives: []*ast.Directive{
			{Name: "withAuth", Args: []*ast.NamedArg{
				{Name: "stores", Value: &ast.Literal{Kind: token.String, Value: "id"}},
			}},
		},
	}
	generateModel(&b, m, nil)
	out := b.String()
	// stores is not a ListExpr, so no fields extracted — should still generate methods
	if !strings.Contains(out, "CreateToken") {
		t.Errorf("should still generate CreateToken: %s", out)
	}
}
