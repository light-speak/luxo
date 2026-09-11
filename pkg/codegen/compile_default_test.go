package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestWriteErrorReturnAvoidsIntermediateAllocations(t *testing.T) {
	for _, test := range []struct {
		name       string
		inFunction bool
		result     *ast.TypeRef
		indent     string
		want       string
	}{
		{name: "handler", want: "\treturn failure\n"},
		{name: "void function", inFunction: true, indent: "\t", want: "\t\treturn failure\n"},
		{name: "value function", inFunction: true, result: &ast.TypeRef{Name: "Int"}, want: "\treturn _value, failure\n"},
		{name: "handler with result", result: &ast.TypeRef{Name: "Int"}, want: "\treturn failure\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output strings.Builder
			output.Grow(1001 * len(test.want))
			c := compiler{b: &output, indent: "\t", inFunction: test.inFunction, functionResult: test.result}
			allocations := testing.AllocsPerRun(1000, func() {
				c.writeErrorReturn(test.indent, "failure")
			})
			if allocations != 0 {
				t.Fatalf("error return allocated %v times, want 0", allocations)
			}
			if output.String() != strings.Repeat(test.want, 1001) {
				t.Fatal("error return changed generated code")
			}
		})
	}
}

func TestCompileAPIBody_ParamWithDefaultInt(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "listUsers",
		Params: []*ast.ParamDecl{
			{
				Name:    "page",
				Type:    &ast.TypeRef{Name: "Int"},
				Default: &ast.Literal{Kind: token.Int, Value: "1"},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	// Should use Param with error-based fallback
	if !strings.Contains(out, `req.ParamInt("page")`) {
		t.Fatalf("expected ParamInt call, got:\n%s", out)
	}
	// Should have default value fallback
	if !strings.Contains(out, "= 1") {
		t.Fatalf("expected default value 1, got:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithDefaultString(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "search",
		Params: []*ast.ParamDecl{
			{
				Name:    "sort",
				Type:    &ast.TypeRef{Name: "String"},
				Default: &ast.Literal{Kind: token.String, Value: "createdAt"},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamString("sort")`) {
		t.Fatalf("expected ParamString call, got:\n%s", out)
	}
	if !strings.Contains(out, `"createdAt"`) {
		t.Fatalf("expected default string value, got:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithDefaultBool(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "list",
		Params: []*ast.ParamDecl{
			{
				Name:    "active",
				Type:    &ast.TypeRef{Name: "Boolean"},
				Default: &ast.Literal{Kind: token.True, Value: "true"},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamBool("active")`) {
		t.Fatalf("expected ParamBool call, got:\n%s", out)
	}
	if !strings.Contains(out, "= true") {
		t.Fatalf("expected default true, got:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithDefaultFloat(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "calc",
		Params: []*ast.ParamDecl{
			{
				Name:    "rate",
				Type:    &ast.TypeRef{Name: "Float"},
				Default: &ast.Literal{Kind: token.Float, Value: "0.05"},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamFloat("rate")`) {
		t.Fatalf("expected ParamFloat call, got:\n%s", out)
	}
	if !strings.Contains(out, "0.05") {
		t.Fatalf("expected default 0.05, got:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithDefaultCustomType(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "create",
		Params: []*ast.ParamDecl{
			{
				Name:    "input",
				Type:    &ast.TypeRef{Name: "CreateInput"},
				Default: &ast.Ident{Name: "false"}, // Fallback zero value
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	// Custom types use native messages in binary mode and JSON only as fallback.
	if !strings.Contains(out, "var input CreateInput") {
		t.Fatalf("expected var declaration, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamJSONOptional("input", &input)`) {
		t.Fatalf("expected ParamJSONOptional, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamMessage("input", true, false)`) {
		t.Fatalf("expected optional native message extraction, got:\n%s", out)
	}
}

func TestCompileDefaultValueNullUsesNil(t *testing.T) {
	if got := compileDefaultValue(&ast.Literal{Kind: token.Null}, "*Payload", nil); got != "nil" {
		t.Fatalf("null default = %q, want nil", got)
	}
}

func TestCompileDefaultValueDuration(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{value: "5ms", want: "5 * time.Millisecond"},
		{value: "1h", want: "time.Hour"},
		{value: "2d", want: "2 * 24 * time.Hour"},
	}
	for _, tt := range tests {
		if got := compileDefaultValue(&ast.Literal{Kind: token.Duration, Value: tt.value}, "time.Duration", nil); got != tt.want {
			t.Errorf("duration default %s = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestCompileDefaultValueSignedNumber(t *testing.T) {
	value := &ast.UnaryExpr{Op: "-", Value: &ast.Literal{Kind: token.Int, Value: "1"}}
	if got := compileDefaultValue(value, "int64", nil); got != "-1" {
		t.Fatalf("signed default = %q, want -1", got)
	}
	duration := &ast.UnaryExpr{Op: "-", Value: &ast.Literal{Kind: token.Duration, Value: "5s"}}
	if got := compileDefaultValue(duration, "time.Duration", nil); got != "-(5 * time.Second)" {
		t.Fatalf("signed duration default = %q, want %q", got, "-(5 * time.Second)")
	}
}

func TestCompileDefaultValueFalseLiteral(t *testing.T) {
	if got := compileDefaultValue(&ast.Literal{Kind: token.False}, "bool", nil); got != "false" {
		t.Fatalf("false default = %q, want false", got)
	}
}

func TestCompileDurationLiteralExpression(t *testing.T) {
	c := newCompiler(nil)
	value := &ast.Literal{Kind: token.Duration, Value: "5s"}
	if got := c.compileExpr(value); got != "5 * time.Second" {
		t.Fatalf("duration expression = %q, want %q", got, "5 * time.Second")
	}
}

func TestCompileAPIBody_NullableParamIsRequired(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "update",
		Params: []*ast.ParamDecl{{
			Name: "note",
			Type: &ast.TypeRef{Name: "String", Nullable: true},
		}},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	if out := b.String(); !strings.Contains(out, `req.ParamJSONNullable("note", &note)`) {
		t.Fatalf("nullable parameter must remain required:\n%s", out)
	}
}

func TestCompileAPIBodyNullableParamWithNonNullDefault(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "lookup",
		Params: []*ast.ParamDecl{{
			Name:    "label",
			Type:    &ast.TypeRef{Name: "String", Nullable: true},
			Default: &ast.Literal{Kind: token.String, Value: "ready"},
		}},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()
	if !strings.Contains(out, `_defaultLabel := "ready"`) ||
		!strings.Contains(out, `var label *string = &_defaultLabel`) ||
		!strings.Contains(out, `if err := req.ParamJSONOptionalNullable("label", &label); err != nil`) {
		t.Fatalf("nullable non-null default must use a stable pointer:\n%s", out)
	}
}

func TestCompileAPIBody_ParamWithEnumDefault(t *testing.T) {
	enums := map[string]bool{"Role": true}
	api := &ast.ApiDecl{
		Name: "list",
		Params: []*ast.ParamDecl{
			{
				Name: "role",
				Type: &ast.TypeRef{Name: "String"},
				Default: &ast.MemberExpr{
					Object: &ast.Ident{Name: "Role"},
					Field:  "admin",
				},
			},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, enums)
	out := b.String()

	// Enum default: RoleADMIN
	if !strings.Contains(out, "RoleADMIN") {
		t.Fatalf("expected enum default RoleADMIN, got:\n%s", out)
	}
}

func TestCompileDefaultValue_Fallbacks(t *testing.T) {
	// Unsupported expr type — should use zero value based on goType
	got := compileDefaultValue(&ast.CallExpr{}, "int64", nil)
	if got != "0" {
		t.Errorf("int64 fallback = %q, want 0", got)
	}
	got = compileDefaultValue(&ast.CallExpr{}, "float64", nil)
	if got != "0" {
		t.Errorf("float64 fallback = %q, want 0", got)
	}
	got = compileDefaultValue(&ast.CallExpr{}, "string", nil)
	if got != `""` {
		t.Errorf("string fallback = %q, want \"\"", got)
	}
	got = compileDefaultValue(&ast.CallExpr{}, "bool", nil)
	if got != "false" {
		t.Errorf("bool fallback = %q, want false", got)
	}
	got = compileDefaultValue(&ast.CallExpr{}, "MyType", nil)
	if got != "MyType{}" {
		t.Errorf("custom type fallback = %q, want MyType{}", got)
	}
}

func TestCompileDefaultValue_IdentTrue(t *testing.T) {
	got := compileDefaultValue(&ast.Ident{Name: "true"}, "bool", nil)
	if got != "true" {
		t.Errorf("got %q, want true", got)
	}
}

func TestCompileDefaultValue_IdentFalse(t *testing.T) {
	got := compileDefaultValue(&ast.Ident{Name: "false"}, "bool", nil)
	if got != "false" {
		t.Errorf("got %q, want false", got)
	}
}
