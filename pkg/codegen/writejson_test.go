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

func TestWriteVisibleDirective_EmptyBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "salary",
		Type: &ast.TypeRef{Name: "Float"},
		Directives: []*ast.Directive{
			{Name: "visible", Body: &ast.Block{}},
		},
	}
	got := writeVisibleDirective(&b, f)
	if got {
		t.Error("empty body should return false")
	}
}

func TestWriteVisibleDirective_NonExprStmt(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "salary",
		Type: &ast.TypeRef{Name: "Float"},
		Directives: []*ast.Directive{
			{Name: "visible", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ReturnStmt{},
			}}},
		},
	}
	got := writeVisibleDirective(&b, f)
	if got {
		t.Error("non-ExprStmt should return false")
	}
}

func TestWriteTransformDirective_EmptyBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "transform", Body: &ast.Block{}},
		},
	}
	got := writeTransformDirective(&b, f, "u.Name")
	if got != "u.Name" {
		t.Errorf("empty body should return original, got %q", got)
	}
}

func TestWriteMaskDirective_Nullable(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name:       "phone",
		Type:       &ast.TypeRef{Name: "String", Nullable: true},
		Directives: []*ast.Directive{{Name: "mask"}},
	}
	got := writeMaskDirective(&b, f, "u.Phone", "String")
	if got != "u.Phone" {
		t.Errorf("nullable @mask should be skipped, got %q", got)
	}
}

func TestCompileVisibleExpr_AndOr(t *testing.T) {
	// my.role == "admin" && my.level > 5
	expr := &ast.BinaryExpr{
		Left: &ast.BinaryExpr{
			Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
			Op:    "==",
			Right: &ast.Literal{Kind: token.String, Value: "admin"},
		},
		Op: "&&",
		Right: &ast.BinaryExpr{
			Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "level"},
			Op:    ">",
			Right: &ast.Literal{Kind: token.Int, Value: "5"},
		},
	}
	got := compileVisibleExpr(expr)
	if !strings.Contains(got, "&&") {
		t.Errorf("should support &&: %q", got)
	}
	if !strings.Contains(got, "IdentityString") && !strings.Contains(got, "IdentityInt") {
		t.Errorf("should use Identity helpers: %q", got)
	}
}

func TestWriteTransformDirective_NonExprStmt(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}}},
		},
	}
	got := writeTransformDirective(&b, f, "u.Name")
	if got != "u.Name" {
		t.Errorf("non-ExprStmt body should return original, got %q", got)
	}
}

func TestWriteTransformDirective_EmptyCompile(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.BinaryExpr{Left: &ast.Ident{Name: "a"}, Op: "+", Right: &ast.Ident{Name: "b"}}},
			}}},
		},
	}
	got := writeTransformDirective(&b, f, "u.Name")
	if got != "u.Name" {
		t.Errorf("unsupported expr should return original, got %q", got)
	}
}

func TestWriteMaskDirective_BadArgs(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "phone",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "mask", Args: []*ast.NamedArg{
				{Value: &ast.Ident{Name: "x"}},
				{Value: &ast.Ident{Name: "y"}},
			}},
		},
	}
	got := writeMaskDirective(&b, f, "u.Phone", "String")
	if got != "u.Phone" {
		t.Errorf("non-literal args should return original, got %q", got)
	}
}

func TestCompileVisibleExpr_EmptyBinary(t *testing.T) {
	// Binary with unsupported left → empty
	expr := &ast.BinaryExpr{
		Left:  &ast.CallExpr{},
		Op:    "==",
		Right: &ast.Literal{Kind: token.Int, Value: "1"},
	}
	got := compileVisibleExpr(expr)
	if got != "" {
		t.Errorf("unsupported left should return empty, got %q", got)
	}
}

func TestWriteVisibleDirective_UnsupportedExpr(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "salary",
		Type: &ast.TypeRef{Name: "Float"},
		Directives: []*ast.Directive{
			{Name: "visible", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.CallExpr{}},
			}}},
		},
	}
	got := writeVisibleDirective(&b, f)
	if got {
		t.Error("unsupported expr should return false")
	}
}

func TestGenerateTypeWriteLuxo(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "AuthPayload",
		Fields: []*ast.FieldDecl{
			{Name: "token", Type: &ast.TypeRef{Name: "String"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
		},
	}
	// Set field IDs for the test
	SetModelFieldIDs(map[string]map[string]int{
		"AuthPayload": {"token": 1, "userId": 2},
	})
	defer SetModelFieldIDs(nil)

	var b strings.Builder
	generateTypeWriteLuxo(&b, m, nil)
	out := b.String()
	// Value receiver, not pointer
	if !strings.Contains(out, "func (a AuthPayload) WriteLuxo") {
		t.Errorf("should use value receiver: %s", out)
	}
	if strings.Contains(out, "*AuthPayload") {
		t.Errorf("should NOT use pointer receiver: %s", out)
	}
	if !strings.Contains(out, "AppendString") {
		t.Errorf("should write String field: %s", out)
	}
	if !strings.Contains(out, "AppendSvarint") {
		t.Errorf("should write Int field: %s", out)
	}
}

func TestGenerateWriteLuxoUUIDDecimalBytesJSON(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Doc": {"id": 1, "uuid": 2, "price": 3, "data": 4, "meta": 5,
			"optUuid": 6, "optPrice": 7, "optData": 8, "optMeta": 9},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Doc",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "uuid", Type: &ast.TypeRef{Name: "UUID"}},
					{Name: "price", Type: &ast.TypeRef{Name: "Decimal"}},
					{Name: "data", Type: &ast.TypeRef{Name: "Bytes"}},
					{Name: "meta", Type: &ast.TypeRef{Name: "JSON"}},
					{Name: "optUuid", Type: &ast.TypeRef{Name: "UUID", Nullable: true}},
					{Name: "optPrice", Type: &ast.TypeRef{Name: "Decimal", Nullable: true}},
					{Name: "optData", Type: &ast.TypeRef{Name: "Bytes", Nullable: true}},
					{Name: "optMeta", Type: &ast.TypeRef{Name: "JSON", Nullable: true}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// WriteLuxo checks
	if !strings.Contains(code, "AppendUUID") {
		t.Errorf("UUID should use AppendUUID (16-byte fixed, per protocol):\n%s", code)
	}
	if !strings.Contains(code, ".String()") {
		t.Errorf("Decimal should use .String():\n%s", code)
	}
	if !strings.Contains(code, "AppendBytes") {
		t.Errorf("Bytes/JSON should use AppendBytes:\n%s", code)
	}

	// ReadLuxo checks
	if !strings.Contains(code, "uuid.UUID(dec.ReadUUID())") {
		t.Errorf("UUID ReadLuxo should use ReadUUID (16-byte fixed):\n%s", code)
	}
	if !strings.Contains(code, "decimal.RequireFromString") {
		t.Errorf("Decimal ReadLuxo should use decimal.RequireFromString:\n%s", code)
	}
	if !strings.Contains(code, "ReadBytes()") {
		t.Errorf("Bytes ReadLuxo should use ReadBytes:\n%s", code)
	}
	if !strings.Contains(code, "ReadBytesPtr()") {
		t.Errorf("nullable Bytes ReadLuxo should use ReadBytesPtr:\n%s", code)
	}

	// WriteColumnar checks
	if !strings.Contains(code, "WriteColumnUUID") {
		t.Errorf("UUID columnar should use WriteColumnUUID (16-byte fixed):\n%s", code)
	}
	if !strings.Contains(code, "WriteColumnUUIDPtr") {
		t.Errorf("nullable UUID columnar should use WriteColumnUUIDPtr:\n%s", code)
	}
	if !strings.Contains(code, "WriteColumnString") {
		t.Errorf("Decimal columnar should use WriteColumnString:\n%s", code)
	}
	if !strings.Contains(code, "WriteColumnBytes") {
		t.Errorf("Bytes columnar should use WriteColumnBytes:\n%s", code)
	}
}

func TestGenerateScalarArrayFields(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Tagged": {"id": 1, "tags": 2, "scores": 3, "ids": 4, "roles": 5},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Tagged",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}},
					{Name: "scores", Type: &ast.TypeRef{Name: "Int", IsList: true}},
					{Name: "ids", Type: &ast.TypeRef{Name: "UUID", IsList: true}},
					{Name: "roles", Type: &ast.TypeRef{Name: "Role", IsList: true}},
				},
			}},
		}},
	}
	enums := map[string]bool{"Role": true}
	code := string(generateWriteJSONFile(result, "app", enums))

	checks := []string{
		// WriteLuxo: array header + per-type item append
		"codec.AppendArrayHeader(buf.B, len(t.Tags))",
		"codec.AppendUUID(buf.B, [16]byte(v))", // [UUID]
		"codec.AppendString(buf.B, string(v))", // [Role] enum
		// ReadLuxo: array readers + conversions
		"dec.ReadStringArray()",
		"dec.ReadIntArray()",
		"dec.ReadUUIDArray()",
		"uuid.UUID(v)",
		"make([]Role, len(_a))",
		// Columnar: array field encoded as a Bytes column of inline-array cells
		"w.WriteColumnBytes",
		"cells := make([][]byte, len(items))",
		"cb = codec.AppendArrayHeader(cb, len(item.Tags))",
	}
	for _, c := range checks {
		if !strings.Contains(code, c) {
			t.Errorf("generated code missing %q:\n%s", c, code)
		}
	}
}

// --- Arena header tests ---

func TestWriteLuxoArenaHeader(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "name": 2, "email": 3, "age": 4},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "email", Type: &ast.TypeRef{Name: "String"}},
					{Name: "age", Type: &ast.TypeRef{Name: "Int"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// Arena length calculation should exist
	if !strings.Contains(code, "_arenaLen") {
		t.Errorf("WriteLuxo should calculate _arenaLen:\n%s", code)
	}
	// Should sum string field lengths
	if !strings.Contains(code, "len(u.Name)") {
		t.Errorf("should include len(u.Name):\n%s", code)
	}
	if !strings.Contains(code, "len(u.Email)") {
		t.Errorf("should include len(u.Email):\n%s", code)
	}
	// Should NOT include non-string fields
	if strings.Contains(code, "len(u.Age)") {
		t.Errorf("should not include len for Int field:\n%s", code)
	}
	// Should write arena len to buf
	if !strings.Contains(code, "AppendVarint(buf.B, uint64(_arenaLen))") {
		t.Errorf("should write _arenaLen as varint:\n%s", code)
	}
}

func TestWriteLuxoArenaHeaderNullableString(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Post": {"id": 1, "title": 2, "subtitle": 3},
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
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// Nullable string should check nil before adding to arena len
	if !strings.Contains(code, "p.Subtitle != nil") {
		t.Errorf("nullable string should check nil for arena len:\n%s", code)
	}
	if !strings.Contains(code, "len(*p.Subtitle)") {
		t.Errorf("nullable string should use len(*p.Subtitle):\n%s", code)
	}
}

func TestWriteLuxoArenaHeaderEnumField(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "role": 2},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name:  "test.luxo",
			Enums: []*ast.EnumDecl{{Name: "Role"}},
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

	// Enum should be included in arena calculation
	if !strings.Contains(code, "len(string(u.Role))") {
		t.Errorf("enum should use len(string(u.Role)) for arena:\n%s", code)
	}
}

func TestWriteLuxoArenaHeaderNoStringFields(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Counter": {"id": 1, "count": 2, "active": 3},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Counter",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "count", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// No string fields — should still write arena header (0)
	if !strings.Contains(code, "AppendVarint(buf.B, 0)") {
		t.Errorf("no string fields should write arena len = 0:\n%s", code)
	}
	// Should NOT have _arenaLen variable
	if strings.Contains(code, "_arenaLen") {
		t.Errorf("no string fields should not declare _arenaLen:\n%s", code)
	}
}

func TestReadLuxoArenaDecoding(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "name": 2, "email": 3, "age": 4},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "email", Type: &ast.TypeRef{Name: "String"}},
					{Name: "age", Type: &ast.TypeRef{Name: "Int"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// ReadLuxo should read arena size
	if !strings.Contains(code, "dec.ReadArenaSize()") {
		t.Errorf("ReadLuxo should call ReadArenaSize:\n%s", code)
	}
	// Should allocate arena
	if !strings.Contains(code, "make([]byte, _arenaSize)") {
		t.Errorf("ReadLuxo should allocate arena:\n%s", code)
	}
	// String fields should use ReadStringArena
	if !strings.Contains(code, "ReadStringArena(_arena, &_arenaOff)") {
		t.Errorf("String fields should use ReadStringArena:\n%s", code)
	}
	// Int field should still use ReadInt (not arena)
	if !strings.Contains(code, "dec.ReadInt()") {
		t.Errorf("Int fields should still use ReadInt:\n%s", code)
	}
}

func TestReadLuxoArenaEnumField(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "name": 2, "role": 3},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name:  "test.luxo",
			Enums: []*ast.EnumDecl{{Name: "Role"}},
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
				},
			}},
		}},
	}

	enums := collectEnums(result)
	src := generateWriteJSONFile(result, "app", enums)
	code := string(src)

	// Enum field should use ReadStringArena
	if !strings.Contains(code, "Role(dec.ReadStringArena(_arena, &_arenaOff))") {
		t.Errorf("Enum ReadLuxo should use ReadStringArena:\n%s", code)
	}
}

func TestReadLuxoNoArenaFields(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Counter": {"id": 1, "count": 2},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Counter",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "count", Type: &ast.TypeRef{Name: "Int"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// No arena fields — should skip arena header
	if !strings.Contains(code, "dec.SkipArenaHeader()") {
		t.Errorf("ReadLuxo with no string fields should SkipArenaHeader:\n%s", code)
	}
	if strings.Contains(code, "ReadArenaSize") {
		t.Errorf("ReadLuxo with no string fields should not call ReadArenaSize:\n%s", code)
	}
}

func TestReadLuxoAllFieldTypes(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Full": {"id": 1, "name": 2, "createdAt": 3, "duration": 4,
			"data": 5, "avatar": 6, "score": 7},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "Full",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
					{Name: "duration", Type: &ast.TypeRef{Name: "Duration"}},
					{Name: "data", Type: &ast.TypeRef{Name: "Bytes"}},
					{Name: "avatar", Type: &ast.TypeRef{Name: "String", Nullable: true}},
					{Name: "score", Type: &ast.TypeRef{Name: "Float", Nullable: true}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// ReadLuxo should handle DateTime, Duration, Bytes, nullable String, nullable Float
	if !strings.Contains(code, "time.Unix(dec.ReadInt(), 0)") {
		t.Errorf("DateTime ReadLuxo missing time.Unix:\n%s", code)
	}
	if !strings.Contains(code, "time.Duration(dec.ReadInt())") {
		t.Errorf("Duration ReadLuxo missing:\n%s", code)
	}
	if !strings.Contains(code, "dec.ReadBytes()") {
		t.Errorf("Bytes ReadLuxo missing:\n%s", code)
	}
	if !strings.Contains(code, "ReadStringArenaPtr") {
		t.Errorf("nullable String should use ReadStringArenaPtr:\n%s", code)
	}
	if !strings.Contains(code, "dec.ReadFloatPtr()") {
		t.Errorf("nullable Float ReadLuxo missing:\n%s", code)
	}
}

func TestWriteLuxoArenaHeaderMaskedPath(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "name": 2, "bio": 3},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "bio", Type: &ast.TypeRef{Name: "String", Nullable: true}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// Masked path should check FieldMaskHas for arena len
	if !strings.Contains(code, "FieldMaskHas(mask, 2) { _arenaLen += len(u.Name)") {
		t.Errorf("masked path should check mask for name arena len:\n%s", code)
	}
	if !strings.Contains(code, "FieldMaskHas(mask, 3) && u.Bio != nil") {
		t.Errorf("masked path should check mask + nil for nullable arena len:\n%s", code)
	}
}
