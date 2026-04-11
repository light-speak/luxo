package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGenerateWriteJSONFileNoModels(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Name: "test.luxo"}}}
	src := generateWriteJSONFile(result, "app", nil)
	if src != nil {
		t.Error("should return nil when no models")
	}
}

func TestGenerateWriteJSONBasic(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	if src == nil {
		t.Fatal("should generate writejson file")
	}
	code := string(src)

	checks := []string{
		"func (u *User) WriteJSON(buf *api.ResponseBuf, fields []*selection.Field)",
		`case "id":`,
		"buf.AppendInt(u.Id)",
		`case "name":`,
		"buf.AppendJSONString(u.Name)",
		`case "active":`,
		"buf.AppendBool(u.Active)",
		"sonic.Marshal(u)",
		// List wrapper
		"type userListJSON []*User",
		"func (l userListJSON) WriteJSON",
	}
	for _, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing %q in:\n%s", check, code)
		}
	}
}

func TestGenerateWriteJSONNullable(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Post",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "subtitle", Type: &ast.TypeRef{Name: "String", Nullable: true}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, "if p.Subtitle == nil") {
		t.Errorf("nullable field should have nil check:\n%s", code)
	}
	if !strings.Contains(code, `buf.AppendString("null")`) {
		t.Errorf("nullable field should write null:\n%s", code)
	}
}

func TestGenerateWriteJSONRelation(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Post",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					{Name: "tags", Type: &ast.TypeRef{Name: "Tag", IsList: true}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// Single relation — call WriteJSON recursively
	if !strings.Contains(code, "p.User.WriteJSON(buf, f.Children)") {
		t.Errorf("single relation should call WriteJSON:\n%s", code)
	}
	// List relation — iterate and call WriteJSON
	if !strings.Contains(code, "item.WriteJSON(buf, f.Children)") {
		t.Errorf("list relation should iterate and call WriteJSON:\n%s", code)
	}
}

func TestGenerateWriteJSONHiddenField(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "password", Type: &ast.TypeRef{Name: "String"},
						Directives: []*ast.Directive{{Name: "hidden"}}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	if strings.Contains(code, `"password"`) {
		t.Errorf("@hidden field should not appear in WriteJSON:\n%s", code)
	}
}

func TestGenerateWriteJSONEnum(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name:  "test.luxo",
			Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"ADMIN", "USER"}}},
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
				},
			}},
		}},
	}

	enums := collectEnums(result)
	src := generateWriteJSONFile(result, "app", enums)
	code := string(src)

	// Enum should be written as string, not as a relation
	if !strings.Contains(code, "buf.AppendJSONString(string(u.Role))") {
		t.Errorf("enum field should use AppendJSONString:\n%s", code)
	}
}

func TestGenerateWriteJSONDateTime(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Event",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, "time.RFC3339Nano") {
		t.Errorf("DateTime should use RFC3339Nano:\n%s", code)
	}
}

func TestGenerateWriteJSONComputed(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "fullName", Type: &ast.TypeRef{Name: "String"},
						Computed: &ast.ComputedField{}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	if strings.Contains(code, `"fullName"`) {
		t.Errorf("computed field should be skipped:\n%s", code)
	}
}

func TestGenerateWriteJSONListField(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, "for i, v := range u.Tags") {
		t.Errorf("list scalar field should iterate:\n%s", code)
	}
}
