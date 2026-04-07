package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
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
			name: "computed fields skipped",
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
			checks: []string{"type Post struct", "Title", "Id"},
			absent: []string{"CommentCount"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			generateModel(&b, tt.model)
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
			got := toSnakeCase(tt.input)
			if got != tt.expect {
				t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, got, tt.expect)
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
			got := capitalize(tt.input)
			if got != tt.expect {
				t.Errorf("capitalize(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

// suppress unused import warning
var _ = token.Position{}
