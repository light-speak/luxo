package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenerateWriteJSONFileNoModels(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Name: "test.luxo"}}}
	src := generateWriteJSONFile(result, "app", nil)
	if src != nil {
		t.Error("should return nil when no models")
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

// ─── @mask/@visible/@transform directive tests ───────────────────────────────

func TestWriteMaskDirective_Email(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "email",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "mask"},
			{Name: "email"},
		},
	}
	result := writeMaskDirective(&b, f, "u.Email", "String")
	if result != "emailMasked" {
		t.Errorf("email @mask should return masked var, got %q", result)
	}
	if !strings.Contains(b.String(), "MaskEmail") {
		t.Errorf("email @mask should use MaskEmail: %s", b.String())
	}
}

func TestWriteMaskDirective_WithArgs(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "phone",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "mask", Args: []*ast.NamedArg{
				{Value: &ast.Literal{Kind: token.Int, Value: "3"}},
				{Value: &ast.Literal{Kind: token.Int, Value: "4"}},
			}},
		},
	}
	result := writeMaskDirective(&b, f, "u.Phone", "String")
	if result != "phoneMasked" {
		t.Errorf("should return masked var, got %q", result)
	}
	if !strings.Contains(b.String(), "str.Mask") {
		t.Errorf("should use str.Mask: %s", b.String())
	}
}

func TestWriteMaskDirective_Default(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name:       "ssn",
		Type:       &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "mask"}},
	}
	result := writeMaskDirective(&b, f, "u.Ssn", "String")
	if result != "ssnMasked" {
		t.Errorf("default @mask should return masked var, got %q", result)
	}
	if !strings.Contains(b.String(), "3, 4") {
		t.Errorf("default should mask with 3,4: %s", b.String())
	}
}

func TestWriteMaskDirective_NoMask(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
	}
	result := writeMaskDirective(&b, f, "u.Name", "String")
	if result != "u.Name" {
		t.Errorf("no @mask should return original, got %q", result)
	}
}

func TestWriteMaskDirective_NonString(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name:       "age",
		Type:       &ast.TypeRef{Name: "Int"},
		Directives: []*ast.Directive{{Name: "mask"}},
	}
	result := writeMaskDirective(&b, f, "u.Age", "Int")
	if result != "u.Age" {
		t.Errorf("@mask on non-string should be ignored, got %q", result)
	}
}

func TestWriteVisibleDirective_NoDirective(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{Name: "name", Type: &ast.TypeRef{Name: "String"}}
	got := writeVisibleDirective(&b, f)
	if got {
		t.Error("no @visible should return false")
	}
}

func TestWriteVisibleDirective_WithBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "salary",
		Type: &ast.TypeRef{Name: "Float"},
		Directives: []*ast.Directive{
			{Name: "visible", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.BinaryExpr{
					Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
					Op:    "==",
					Right: &ast.Literal{Kind: token.String, Value: "admin"},
				}},
			}}},
		},
	}
	got := writeVisibleDirective(&b, f)
	if !got {
		t.Error("@visible with body should return true")
	}
	if !strings.Contains(b.String(), "IdentityString") {
		t.Errorf("should compile my.role: %s", b.String())
	}
}

func TestCompileVisibleExpr(t *testing.T) {
	// my.role == "admin"
	expr := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
		Op:    "==",
		Right: &ast.Literal{Kind: token.String, Value: "admin"},
	}
	got := compileVisibleExpr(expr)
	if !strings.Contains(got, "IdentityString") || !strings.Contains(got, `"admin"`) {
		t.Errorf("got %q", got)
	}

	// my.level > 5 → should use IdentityInt
	expr2 := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "level"},
		Op:    ">",
		Right: &ast.Literal{Kind: token.Int, Value: "5"},
	}
	got2 := compileVisibleExpr(expr2)
	if !strings.Contains(got2, "IdentityInt") {
		t.Errorf("numeric comparison should use IdentityInt: %q", got2)
	}

	// my.id
	expr3 := &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "id"}
	got3 := compileVisibleExpr(expr3)
	if !strings.Contains(got3, "IdentityID") {
		t.Errorf("my.id should use IdentityID: %q", got3)
	}

	// literal
	got4 := compileVisibleExpr(&ast.Literal{Kind: token.Int, Value: "42"})
	if got4 != "42" {
		t.Errorf("literal = %q", got4)
	}

	// ident
	got5 := compileVisibleExpr(&ast.Ident{Name: "x"})
	if got5 != "x" {
		t.Errorf("ident = %q", got5)
	}

	// unsupported → empty
	got6 := compileVisibleExpr(&ast.CallExpr{})
	if got6 != "" {
		t.Errorf("unsupported should be empty, got %q", got6)
	}
}

func TestWriteTransformDirective_NoDirective(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{Name: "name", Type: &ast.TypeRef{Name: "String"}}
	got := writeTransformDirective(&b, f, "u.Name")
	if got != "u.Name" {
		t.Errorf("no @transform should return original, got %q", got)
	}
}

func TestWriteTransformDirective_WithBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "uppercase"},
				}},
			}}},
		},
	}
	got := writeTransformDirective(&b, f, "u.Name")
	if got != "nameTransformed" {
		t.Errorf("should return transformed var, got %q", got)
	}
	if !strings.Contains(b.String(), "strings.ToUpper") {
		t.Errorf("should compile it.uppercase(): %s", b.String())
	}
}
