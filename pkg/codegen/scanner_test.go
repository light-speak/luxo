package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGenerateScanner(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "email", Type: &ast.TypeRef{Name: "String"}},
			{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
		},
	}

	var b strings.Builder
	generateScanner(&b, m)
	got := b.String()

	checks := []string{
		"func scanUser(rows pg.Rows) (*User, error)",
		"var user User",
		"fds := rows.FieldDescriptions()",
		"dests := make([]any, len(fds))",
		"switch string(fd.Name)",
		`case "id":`,
		"dests[i] = &user.Id",
		`case "name":`,
		"dests[i] = &user.Name",
		`case "email":`,
		"dests[i] = &user.Email",
		`case "created_at":`,
		"dests[i] = &user.CreatedAt",
		"default:",
		"dests[i] = new(any)",
		"return &user, rows.Scan(dests...)",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGenerateScannerSkipsComputed(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
			{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
		},
	}

	var b strings.Builder
	generateScanner(&b, m)
	got := b.String()

	if strings.Contains(got, "comment_count") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
	if !strings.Contains(got, `case "id":`) {
		t.Errorf("non-computed field missing:\n%s", got)
	}
}

func TestGenerateScannerFunctionName(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "OrderItem",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		},
	}

	var b strings.Builder
	generateScanner(&b, m)
	got := b.String()

	if !strings.Contains(got, "func scanOrderItem(rows pg.Rows)") {
		t.Errorf("wrong function name:\n%s", got)
	}
	if !strings.Contains(got, "var orderItem OrderItem") {
		t.Errorf("wrong variable name:\n%s", got)
	}
}
