package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenerateWriteJSONFileNoModels(t *testing.T) {
	generator := defaultGenerator()
	result := &semantic.Result{Files: []*ast.File{{Name: "test.luxo"}}}
	src := generator.generateWriteJSONFile(result, "app", nil)
	if src != nil {
		t.Error("should return nil when no models")
	}
}

// --- WriteLuxo generation tests ---

func TestGenerateWriteLuxoWithFieldIDs(t *testing.T) {

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
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

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, ".Unix()") {
		t.Errorf("DateTime should use .Unix():\n%s", code)
	}
	if !strings.Contains(code, "int64(") {
		t.Errorf("Duration should cast to int64:\n%s", code)
	}
}

func TestGenerateWriteLuxoNullableFields(t *testing.T) {

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
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

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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
	src := generator.generateWriteJSONFile(result, "app", enums)
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

func TestGenerateWriteLuxoHiddenSkippedAndComputedEncoded(t *testing.T) {

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
	code := string(src)

	if strings.Contains(code, "u.Password") {
		t.Error("hidden field should be skipped in WriteLuxo")
	}
	if !strings.Contains(code, "u.FullName") {
		t.Error("computed field should be encoded by WriteLuxo")
	}
	if strings.Contains(code, "u.Internal") {
		t.Error("internal field should be skipped in WriteLuxo")
	}
}

func TestGenerateWriteLuxoNoFieldIDs(t *testing.T) {
	generator := defaultGenerator()

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

	src := generator.generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// WriteLuxo should still be generated but with no field writes
	if !strings.Contains(code, "WriteLuxo") {
		t.Errorf("WriteLuxo should be generated even without field IDs:\n%s", code)
	}
}

func TestGenerateWriteLuxoRelationEncoded(t *testing.T) {

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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
	generator.generateWriteLuxo(&b, m, enums)
	code := b.String()

	if !strings.Contains(code, "p.User.WriteLuxo(buf, nil)") {
		t.Errorf("relation field should use nested row encoding:\n%s", code)
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

func TestWriteMaskDirective_WithPattern(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "phone",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "mask", Args: []*ast.NamedArg{
				{Value: &ast.Literal{Kind: token.String, Value: "###****####"}},
			}},
		},
	}
	result := writeMaskDirective(&b, f, "u.Phone", "String")
	if result != "phoneMasked" {
		t.Errorf("should return masked var, got %q", result)
	}
	if !strings.Contains(b.String(), `str.MaskPattern(u.Phone, "###****####")`) {
		t.Errorf("should use str.MaskPattern: %s", b.String())
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

func TestWriteColumnarMaskedStringField(t *testing.T) {
	var b strings.Builder
	field := &ast.FieldDecl{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "mask"}}}
	writeColumnarMaskedStringField(&b, field, "Secret", 3)
	code := b.String()
	if !strings.Contains(code, "str.Mask(item.Secret, 3, 4)") || !strings.Contains(code, "WriteColumnString(3, vals)") {
		t.Fatalf("masked columnar field is incomplete:\n%s", code)
	}
}

func TestWriteColumnarMaskedStringFieldWithPattern(t *testing.T) {
	var b strings.Builder
	field := &ast.FieldDecl{
		Name: "phone",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "mask", Args: []*ast.NamedArg{
			{Value: &ast.Literal{Kind: token.String, Value: "###****####"}},
		}}},
	}
	writeColumnarMaskedStringField(&b, field, "Phone", 3)
	code := b.String()
	if !strings.Contains(code, `str.MaskPattern(item.Phone, "###****####")`) || !strings.Contains(code, "WriteColumnString(3, vals)") {
		t.Fatalf("pattern-masked columnar field is incomplete:\n%s", code)
	}
}

func TestGenerateWriteLuxoTransformImportsAndSelectAll(t *testing.T) {
	generator := generatorWithModelFieldIDs(map[string]map[string]int{"User": {"name": 1}})

	transform := &ast.Directive{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "it"}, Field: "uppercase",
		}}},
	}}}
	result := &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{{
		Name: "User",
		Fields: []*ast.FieldDecl{{
			Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{transform, {Name: "mask"}},
		}},
	}}}}}
	code := string(generator.generateWriteJSONFile(result, "app", nil))

	if !strings.Contains(code, `"strings"`) {
		t.Fatalf("@transform string methods must import strings:\n%s", code)
	}
	if !strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/str"`) {
		t.Fatalf("@mask must import the string runtime:\n%s", code)
	}
	if !strings.Contains(code, "nameTransformed := strings.ToUpper(u.Name)") {
		t.Fatalf("single-object writer must transform the field:\n%s", code)
	}
	if strings.Contains(code, "if len(mask) == 0 {") {
		t.Fatalf("SELECT * must not bypass output directives:\n%s", code)
	}
}

func TestGenerateWriteColumnarAppliesTransformBeforeMask(t *testing.T) {
	generator := generatorWithModelFieldIDs(map[string]map[string]int{"User": {"name": 1}})

	transform := &ast.Directive{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "it"}, Field: "uppercase",
		}}},
	}}}
	model := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{{
		Name: "name", Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{transform, {Name: "mask"}},
	}}}
	var b strings.Builder
	generator.generateWriteColumnar(&b, model, nil)
	code := b.String()

	if !strings.Contains(code, "str.Mask(strings.ToUpper(item.Name), 3, 4)") {
		t.Fatalf("columnar writer must transform before masking:\n%s", code)
	}
	if !strings.Contains(code, "w.WriteColumnString(1, vals)") {
		t.Fatalf("transformed values must stay columnar:\n%s", code)
	}
}

func TestGenerateWriteColumnarNullableTransform(t *testing.T) {
	generator := generatorWithModelFieldIDs(map[string]map[string]int{"User": {"nickname": 1}})

	transform := &ast.Directive{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "it"}, Field: "trim",
		}}},
	}}}
	model := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{{
		Name: "nickname", Type: &ast.TypeRef{Name: "String", Nullable: true},
		Directives: []*ast.Directive{transform},
	}}}
	result := &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{model}}}}
	code := string(generator.generateWriteJSONFile(result, "app", nil))

	if !strings.Contains(code, "if item.Nickname != nil { v := strings.TrimSpace(*item.Nickname); vals[i] = &v }") {
		t.Fatalf("nullable transform must preserve nulls:\n%s", code)
	}
	if !strings.Contains(code, "w.WriteColumnStringPtr(1, vals)") {
		t.Fatalf("nullable transform must use the nullable column encoding:\n%s", code)
	}
	if !strings.Contains(code, "if u.Nickname != nil { nicknameTransformedValue := strings.TrimSpace(*u.Nickname)") {
		t.Fatalf("single-object nullable transform must preserve nulls:\n%s", code)
	}
}

func TestGenerateWriteColumnarHonorsVisible(t *testing.T) {
	generator := generatorWithModelFieldIDs(map[string]map[string]int{"User": {"salary": 1}})

	visible := &ast.Directive{Name: "visible", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
			Op:    "==",
			Right: &ast.Literal{Kind: token.String, Value: "admin"},
		}},
	}}}
	model := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{{
		Name: "salary", Type: &ast.TypeRef{Name: "Float", Nullable: true},
		Directives: []*ast.Directive{visible},
	}}}
	var b strings.Builder
	generator.generateWriteColumnar(&b, model, nil)
	code := b.String()

	condition := `if api.IdentityString(buf.Identity, "role") == "admin" {`
	if !strings.Contains(code, condition) {
		t.Fatalf("columnar writer must omit invisible columns:\n%s", code)
	}
	if !strings.Contains(code, "w.WriteColumnFloatPtr(1, vals)") {
		t.Fatalf("visible nullable field must preserve nullable column encoding:\n%s", code)
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
	if got != "phoneMasked" {
		t.Errorf("nullable @mask should return a masked pointer, got %q", got)
	}
	if !strings.Contains(b.String(), "if u.Phone != nil { phoneMaskedValue := str.Mask(*u.Phone, 3, 4)") {
		t.Errorf("nullable @mask must preserve null and mask present values: %s", b.String())
	}
}

func TestWriteMaskDirective_NullablePattern(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "phone",
		Type: &ast.TypeRef{Name: "String", Nullable: true},
		Directives: []*ast.Directive{{Name: "mask", Args: []*ast.NamedArg{
			{Value: &ast.Literal{Kind: token.String, Value: "###****####"}},
		}}},
	}
	got := writeMaskDirective(&b, f, "u.Phone", "String")
	if got != "phoneMasked" {
		t.Errorf("nullable pattern @mask should return a masked pointer, got %q", got)
	}
	if code := b.String(); !strings.Contains(code, `str.MaskPattern(*u.Phone, "###****####")`) || !strings.Contains(code, "if u.Phone != nil") {
		t.Errorf("nullable pattern @mask must preserve null and mask present values: %s", code)
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
			}},
		},
	}
	got := writeMaskDirective(&b, f, "u.Phone", "String")
	if got != "phoneMasked" || !strings.Contains(b.String(), `str.MaskPattern(u.Phone, "")`) {
		t.Errorf("invalid args should fail closed, got %q: %s", got, b.String())
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
	generator := generatorWithModelFieldIDs(map[string]map[string]int{
		"AuthPayload": {"token": 1, "userId": 2},
	})

	var b strings.Builder
	generator.generateTypeWriteLuxo(&b, m, nil)
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

func TestGenerateTypeWriteColumnar(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "ServiceSummary",
		Fields: []*ast.FieldDecl{
			{Name: "serviceName", Type: &ast.TypeRef{Name: "String"}},
			{Name: "apiCount", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "version", Type: &ast.TypeRef{Name: "String", Nullable: true}},
			{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}},
		},
	}
	generator := generatorWithModelFieldIDs(map[string]map[string]int{
		"ServiceSummary": {"serviceName": 1, "apiCount": 2, "version": 3, "tags": 4},
	})

	var b strings.Builder
	generator.generateTypeWriteColumnar(&b, m, nil)
	out := b.String()

	// Value slice — native resolvers return []Type, not []*Type
	if !strings.Contains(out, "func WriteColumnarServiceSummary(buf *api.ResponseBuf, items []ServiceSummary, mask []byte)") {
		t.Errorf("should take value slice: %s", out)
	}
	if !strings.Contains(out, "w.WriteColumnString(1, vals)") {
		t.Errorf("should write serviceName column: %s", out)
	}
	if !strings.Contains(out, "w.WriteColumnInt(2, vals)") {
		t.Errorf("should write apiCount column: %s", out)
	}
	if !strings.Contains(out, "WriteColumnStringPtr(3, vals)") {
		t.Errorf("should write nullable version column: %s", out)
	}
	if !strings.Contains(out, "w.WriteColumnBytes(4, cells)") {
		t.Errorf("should write list tags column: %s", out)
	}
}

func TestCompileMaskDirectiveMultipleArgs(t *testing.T) {
	field := &ast.FieldDecl{
		Name: "phone",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "mask", Args: []*ast.NamedArg{
			{Value: &ast.Literal{Kind: token.String, Value: "first"}},
			{Value: &ast.Literal{Kind: token.String, Value: "second"}},
		}}},
	}
	if got := compileMaskDirectiveExpr(field, "value", "String"); got != `str.MaskPattern(value, "")` {
		t.Fatalf("compileMaskDirectiveExpr() = %q", got)
	}
}

func TestGenerateTypeOutputDirectives(t *testing.T) {
	generator := generatorWithModelFieldIDs(map[string]map[string]int{"Profile": {"displayName": 1, "secret": 2, "hidden": 3}})

	transform := &ast.Directive{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "it"}, Field: "trim",
		}}},
	}}}
	visible := &ast.Directive{Name: "visible", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
			Op:    "==",
			Right: &ast.Literal{Kind: token.String, Value: "admin"},
		}},
	}}}
	result := &semantic.Result{Files: []*ast.File{{Types: []*ast.TypeDecl{{
		Name: "Profile",
		Fields: []*ast.FieldDecl{
			{Name: "displayName", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{transform}},
			{Name: "secret", Type: &ast.TypeRef{Name: "String", Nullable: true}, Directives: []*ast.Directive{{Name: "mask"}, visible}},
			{Name: "hidden", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
			{Name: "noFieldID", Type: &ast.TypeRef{Name: "String"}},
		},
	}}}}}
	code := string(generator.generateWriteJSONFile(result, "app", nil))

	if !strings.Contains(code, "displayNameTransformed := strings.TrimSpace(p.DisplayName)") {
		t.Fatalf("type row writer must apply @transform:\n%s", code)
	}
	if !strings.Contains(code, "vals[i] = strings.TrimSpace(item.DisplayName)") {
		t.Fatalf("type columnar writer must apply @transform:\n%s", code)
	}
	if !strings.Contains(code, `if api.IdentityString(buf.Identity, "role") == "admin" {`) {
		t.Fatalf("type writers must honor @visible:\n%s", code)
	}
	if !strings.Contains(code, "secretMaskedValue := str.Mask(*p.Secret, 3, 4)") {
		t.Fatalf("type row writer must mask nullable strings:\n%s", code)
	}
	if strings.Contains(code, "p.Hidden") || strings.Contains(code, "item.Hidden") {
		t.Fatalf("type writers must omit hidden fields:\n%s", code)
	}
}

func TestGenerateTypeWriteColumnarNestedList(t *testing.T) {
	// MetricTimeSeries { apiName: String, points: [MetricPoint] } —
	// nested type list becomes a blob column where each cell is the
	// columnar encoding of that record's nested items.
	m := &ast.ModelDecl{
		Name: "MetricTimeSeries",
		Fields: []*ast.FieldDecl{
			{Name: "apiName", Type: &ast.TypeRef{Name: "String"}},
			{Name: "points", Type: &ast.TypeRef{Name: "MetricPoint", IsList: true}},
		},
	}
	generator := generatorWithModelFieldIDs(map[string]map[string]int{
		"MetricTimeSeries": {"apiName": 1, "points": 2},
	})

	var b strings.Builder
	generator.generateTypeWriteColumnar(&b, m, nil)
	out := b.String()

	if !strings.Contains(out, "WriteColumnarMetricPoint(") {
		t.Errorf("nested list cell should encode via nested columnar writer: %s", out)
	}
	if !strings.Contains(out, "w.WriteColumnBytes(2, vals)") {
		t.Errorf("nested list should be a blob column: %s", out)
	}
}

func TestGenerateTypeWriteColumnarNestedSingle(t *testing.T) {
	// Single nested type — blob cell holds the nested WriteLuxo bytes.
	m := &ast.ModelDecl{
		Name: "Outer",
		Fields: []*ast.FieldDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "inner", Type: &ast.TypeRef{Name: "Inner"}},
		},
	}
	generator := generatorWithModelFieldIDs(map[string]map[string]int{
		"Outer": {"name": 1, "inner": 2},
	})

	var b strings.Builder
	generator.generateTypeWriteColumnar(&b, m, nil)
	out := b.String()

	if !strings.Contains(out, ".WriteLuxo(") {
		t.Errorf("nested single cell should encode via WriteLuxo: %s", out)
	}
	if !strings.Contains(out, "w.WriteColumnBytes(2, vals)") {
		t.Errorf("nested single should be a blob column: %s", out)
	}
}

func TestGenerateTypeWriteColumnarNestedNullable(t *testing.T) {
	var b strings.Builder
	writeColumnarNestedBlobField(&b, "Child", 4, &ast.TypeRef{Name: "Child", Nullable: true}, "selectionMask")
	code := b.String()
	if !strings.Contains(code, "if items[i].Child != nil") || !strings.Contains(code, "items[i].Child.WriteLuxo") {
		t.Fatalf("nullable nested type must preserve null cells:\n%s", code)
	}
	if !strings.Contains(code, "w.WriteColumnBytesPtr(4, vals)") {
		t.Fatalf("nullable nested type must use canonical nullable column encoding:\n%s", code)
	}
}

func TestGenerateModelBinaryWritersIncludeRelations(t *testing.T) {
	model := &ast.ModelDecl{
		Name: "Parent",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "child", Type: &ast.TypeRef{Name: "Child", Nullable: true}},
			{Name: "children", Type: &ast.TypeRef{Name: "Child", IsList: true}},
		},
	}
	generator := generatorWithModelFieldIDs(map[string]map[string]int{
		"Parent": {"id": 1, "child": 2, "children": 3},
	})

	var row strings.Builder
	generator.generateWriteLuxo(&row, model, nil)
	rowCode := row.String()
	if !strings.Contains(rowCode, "_childMask2, _ := codec.SelectionMaskNested(selectionMask, 2)") ||
		!strings.Contains(rowCode, "p.Child.WriteLuxo(buf, _childMask2)") ||
		!strings.Contains(rowCode, "p.Children[i].WriteLuxo(buf, _childMask3)") {
		t.Fatalf("model row writer must encode selected relations:\n%s", rowCode)
	}

	var columnar strings.Builder
	generator.generateWriteColumnar(&columnar, model, nil)
	columnarCode := columnar.String()
	if !strings.Contains(columnarCode, "WriteColumnarChildValues(&nb, items[i].Children, _childMask3)") {
		t.Fatalf("model list relation must use canonical nested columnar encoding:\n%s", columnarCode)
	}
	if !strings.Contains(columnarCode, "mask = codec.SelectionMaskFields(mask)") {
		t.Fatalf("columnar writer must decode the recursive selection node:\n%s", columnarCode)
	}
	if !strings.Contains(columnarCode, "w.WriteColumnBytesPtr(2, vals)") {
		t.Fatalf("nullable model relation must use nullable byte cells:\n%s", columnarCode)
	}
	if !strings.Contains(columnarCode, "func WriteColumnarParentValues(") {
		t.Fatalf("model writer must expose a zero-conversion value-slice variant:\n%s", columnarCode)
	}
}

func TestGenerateTypeWriteColumnarNoFields(t *testing.T) {
	generator := generatorWithModelFieldIDs(nil)
	var b strings.Builder
	generator.generateTypeWriteColumnar(&b, &ast.ModelDecl{
		Name:   "Empty",
		Fields: []*ast.FieldDecl{{Name: "value", Type: &ast.TypeRef{Name: "String"}}},
	}, nil)

	if b.Len() != 0 {
		t.Fatalf("type without protocol field IDs should not generate a writer:\n%s", b.String())
	}
}

func TestColumnarWritersIgnoreUnsupportedTypes(t *testing.T) {
	var valueBuilder strings.Builder
	writeColumnarValueField(&valueBuilder, "item.Value", 1, "Unsupported", false)
	if valueBuilder.Len() != 0 {
		t.Fatalf("unsupported scalar should not generate invalid code: %s", valueBuilder.String())
	}

	var nullableBuilder strings.Builder
	writeColumnarNullableValueField(&nullableBuilder, "item.Value", "*item.Value", 1, "Unsupported", false)
	if nullableBuilder.Len() != 0 {
		t.Fatalf("unsupported nullable scalar should not generate invalid code: %s", nullableBuilder.String())
	}
}

func TestWriteColumnarArrayFieldAllScalarTypes(t *testing.T) {
	types := []string{"Float", "Boolean", "DateTime", "Duration", "Decimal", "Bytes", "JSON", "Unsupported"}
	var b strings.Builder
	for id, typeName := range types {
		writeColumnarArrayField(&b, "item.Values", id+1, typeName, false)
	}
	code := b.String()
	checks := []string{
		"codec.AppendFixed64(cb, v)",
		"codec.AppendBool(cb, v)",
		"codec.AppendSvarint(cb, v.Unix())",
		"codec.AppendSvarint(cb, int64(v))",
		"codec.AppendString(cb, v.String())",
		"codec.AppendBytes(cb, v)",
	}
	for _, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing scalar array encoding %q:\n%s", check, code)
		}
	}
}

func TestGenerateWriteLuxoUUIDDecimalBytesJSON(t *testing.T) {

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
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
	if !strings.Contains(code, "ReadBytesValuePtr()") {
		t.Errorf("nullable Bytes ReadLuxo should preserve null versus empty bytes:\n%s", code)
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

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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
	code := string(generator.generateWriteJSONFile(result, "app", enums))

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
		"values := item.Tags",
		"cb = codec.AppendArrayHeader(cb, len(values))",
	}
	for _, c := range checks {
		if !strings.Contains(code, c) {
			t.Errorf("generated code missing %q:\n%s", c, code)
		}
	}
}

func TestGenerateNestedModelListUsesCanonicalArrayHeader(t *testing.T) {
	generator := generatorWithModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "posts": 2},
		"Post": {"id": 1},
	})
	result := &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{
		{
			Name: "User",
			Fields: []*ast.FieldDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
			},
		},
		{Name: "Post", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}},
	}}}}

	code := string(generator.generateWriteJSONFile(result, "app", nil))
	if !strings.Contains(code, "codec.AppendArrayHeader(buf.B, len(u.Posts))") {
		t.Fatalf("nested model list must use the canonical unsigned array header:\n%s", code)
	}
	if !strings.Contains(code, "_n := dec.ReadArrayLength()") {
		t.Fatalf("nested model list must read the canonical unsigned array header:\n%s", code)
	}
	if strings.Contains(code, "codec.AppendSvarint(buf.B, int64(len(u.Posts)))") {
		t.Fatalf("nested model list must not zigzag-encode its length:\n%s", code)
	}
}

// --- Arena header tests ---

func TestWriteLuxoArenaHeader(t *testing.T) {

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
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

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
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

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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
	src := generator.generateWriteJSONFile(result, "app", enums)
	code := string(src)

	// Enum should be included in arena calculation
	if !strings.Contains(code, "len(string(u.Role))") {
		t.Errorf("enum should use len(string(u.Role)) for arena:\n%s", code)
	}
}

func TestWriteLuxoArenaHeaderNoStringFields(t *testing.T) {

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
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

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
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

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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
	src := generator.generateWriteJSONFile(result, "app", enums)
	code := string(src)

	// Enum field should use ReadStringArena
	if !strings.Contains(code, "Role(dec.ReadStringArena(_arena, &_arenaOff))") {
		t.Errorf("Enum ReadLuxo should use ReadStringArena:\n%s", code)
	}
}

func TestReadLuxoNoArenaFields(t *testing.T) {

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
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

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
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

	generator := generatorWithModelFieldIDs(map[string]map[string]int{
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

	src := generator.generateWriteJSONFile(result, "app", nil)
	code := string(src)

	// Masked path should check FieldMaskHas for arena len
	if !strings.Contains(code, "FieldMaskHas(mask, 2) { _arenaLen += len(u.Name)") {
		t.Errorf("masked path should check mask for name arena len:\n%s", code)
	}
	if !strings.Contains(code, "FieldMaskHas(mask, 3) && u.Bio != nil") {
		t.Errorf("masked path should check mask + nil for nullable arena len:\n%s", code)
	}
}

func TestGenerateWriteJSONExtendStubIncludesDeclaredPrimaryKey(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		ModelIDField: map[string]string{"Product": "sku"},
		ModelIDType:  map[string]string{"Product": "String"},
	}, IDs: StableIDs{ModelFields: map[string]map[string]int{"Product": {"sku": 7, "name": 8}}}})
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/review.luxo",
		Extends: []*ast.ExtendDecl{{
			Name:   "Product",
			Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}},
		}},
	}}}

	code := string(generator.generateWriteJSONFile(result, "app", nil))
	if !strings.Contains(code, "case 7: p.Sku = dec.ReadStringArena") {
		t.Fatalf("extend codec does not decode the declared primary key:\n%s", code)
	}
}
