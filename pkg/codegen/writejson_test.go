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
		"if fields == nil",
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

// --- WriteLuxo generation tests ---

func TestGenerateWriteLuxoWithFieldIDs(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "name": 2, "active": 3, "score": 4},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
					{Name: "score", Type: &ast.TypeRef{Name: "Float"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	if src == nil {
		t.Fatal("should generate")
	}
	code := string(src)

	// WriteLuxo method should be generated
	if !strings.Contains(code, "func (u *User) WriteLuxo(buf *api.ResponseBuf, mask []byte)") {
		t.Errorf("WriteLuxo method missing:\n%s", code)
	}
	// All-fields fast path
	if !strings.Contains(code, "if len(mask) == 0") {
		t.Errorf("fast path missing:\n%s", code)
	}
	// Masked slow path
	if !strings.Contains(code, "FieldMaskHas(mask,") {
		t.Errorf("FieldMaskHas missing:\n%s", code)
	}
	// Terminator
	if !strings.Contains(code, "buf.B = append(buf.B, 0x00)") {
		t.Errorf("terminator missing:\n%s", code)
	}
}

func TestGenerateWriteLuxoDateTimeField(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Event": {"id": 1, "createdAt": 2, "duration": 3},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Event",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
					{Name: "duration", Type: &ast.TypeRef{Name: "Duration"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, ".Unix()") {
		t.Errorf("DateTime should use .Unix():\n%s", code)
	}
	if !strings.Contains(code, "int64(") {
		t.Errorf("Duration should cast to int64:\n%s", code)
	}
}

func TestGenerateWriteLuxoNullableFields(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Post": {"id": 1, "title": 2, "subtitle": 3, "views": 4, "rating": 5, "published": 6, "startAt": 7, "length": 8},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Post",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "title", Type: &ast.TypeRef{Name: "String"}},
					{Name: "subtitle", Type: &ast.TypeRef{Name: "String", Nullable: true}},
					{Name: "views", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
					{Name: "rating", Type: &ast.TypeRef{Name: "Float", Nullable: true}},
					{Name: "published", Type: &ast.TypeRef{Name: "Boolean", Nullable: true}},
					{Name: "startAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}},
					{Name: "length", Type: &ast.TypeRef{Name: "Duration", Nullable: true}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// Nullable should have AppendPresent/AppendNull pattern
	if !strings.Contains(code, "AppendPresent") {
		t.Errorf("nullable field should have AppendPresent:\n%s", code)
	}
	if !strings.Contains(code, "AppendNull") {
		t.Errorf("nullable field should have AppendNull:\n%s", code)
	}
}

func TestGenerateWriteLuxoEnumFields(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "role": 2, "status": 3},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name:  "test.luxo",
			Enums: []*ast.EnumDecl{{Name: "Role"}, {Name: "Status"}},
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
					{Name: "status", Type: &ast.TypeRef{Name: "Status", Nullable: true}},
				},
			}},
		}},
	}

	enums := collectEnums(result)
	src := generateWriteJSONFile(result, "app", enums)
	code := string(src)

	// Non-nullable enum: string(u.Role)
	if !strings.Contains(code, "string(u.Role)") {
		t.Errorf("enum should convert to string:\n%s", code)
	}
	// Nullable enum: string(*u.Status)
	if !strings.Contains(code, "string(*u.Status)") {
		t.Errorf("nullable enum should deref and convert:\n%s", code)
	}
}

func TestGenerateWriteLuxoHiddenAndComputedSkipped(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "password": 2, "fullName": 3, "internal": 4},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
					{Name: "fullName", Type: &ast.TypeRef{Name: "String"}, Computed: &ast.ComputedField{}},
					{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	if strings.Contains(code, "u.Password") {
		t.Error("hidden field should be skipped in WriteLuxo")
	}
	if strings.Contains(code, "u.FullName") {
		t.Error("computed field should be skipped in WriteLuxo")
	}
	if strings.Contains(code, "u.Internal") {
		t.Error("internal field should be skipped in WriteLuxo")
	}
}

func TestGenerateWriteLuxoNoFieldIDs(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	modelFieldIDs = nil

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// WriteLuxo should still be generated but with no field writes
	if !strings.Contains(code, "WriteLuxo") {
		t.Errorf("WriteLuxo should be generated even without field IDs:\n%s", code)
	}
}

func TestGenerateWriteLuxoRelationSkipped(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Post": {"id": 1, "userId": 2, "user": 3},
	})

	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "user", Type: &ast.TypeRef{Name: "User"}}, // relation
		},
	}
	enums := map[string]bool{}
	generateWriteLuxo(&b, m, enums)
	code := b.String()

	// Relation field "user" should NOT appear in WriteLuxo (but UserId should)
	// Check for "p.User " or "p.User)" - the relation accessor, not "p.UserId"
	codeWithoutUserId := strings.ReplaceAll(code, "p.UserId", "")
	if strings.Contains(codeWithoutUserId, "p.User") {
		t.Errorf("relation field should be skipped in WriteLuxo:\n%s", code)
	}
	// Non-relation fields should appear
	if !strings.Contains(code, "p.Id") {
		t.Errorf("non-relation field should appear:\n%s", code)
	}
}
