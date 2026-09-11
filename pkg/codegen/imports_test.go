package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedImportsIgnoreLiteralPackageNames(t *testing.T) {
	for _, body := range []string{
		`func f() { _ = "selection.Field codec.NewEncoder errgroup.WithContext time.Now" }`,
		"// selection.Field\n/* errgroup.WithContext */\nfunc f() { _ = `codec.NewEncoder` }",
	} {
		var out strings.Builder
		writeHandlerImports(&out, &semantic.Result{}, nil, handlerFeatures{}, false, body)
		for _, name := range []string{"selection", "codec", "errgroup", "time"} {
			if strings.Contains(out.String(), name+"\"") {
				t.Fatalf("spurious %s import:\n%s", name, out.String())
			}
		}
	}
}

func TestBodyContainsAwaitRecurses(t *testing.T) {
	await := &ast.AwaitExpr{Body: &ast.Block{}}
	for _, statement := range []ast.Stmt{
		&ast.ValStmt{Name: "value", Value: await},
		&ast.IfStmt{Then: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: await}}}},
		&ast.ForStmt{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: await}}}},
		&ast.ExprStmt{Expr: &ast.AsyncExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: await}}}}},
	} {
		if !bodyContainsAwait(&ast.Block{Stmts: []ast.Stmt{statement}}) {
			t.Fatalf("missed await in %T", statement)
		}
	}
}

func TestEventImportsIgnoreLiteralSelection(t *testing.T) {
	file := &ast.File{Events: []*ast.EventDecl{{Name: "Changed"}}, Listeners: []*ast.OnDecl{{
		EventName: "Changed", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ValStmt{Name: "message", Value: &ast.Literal{Kind: token.String, Value: "selection.Field"}}}},
	}}}
	code := string(generateEventFile(result(file), "luxo"))
	if strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/selection"`) {
		t.Fatalf("literal caused unused import:\n%s", code)
	}
}

func TestGeneratedPackagesLexicalBoundaries(t *testing.T) {
	for _, test := range []struct {
		source           string
		selection, codec bool
	}{
		{`selection.Field; codec.NewEncoder()`, true, true},
		{`object.selection.Field; object.codec.NewEncoder()`, false, false},
		{"selection /* comment */ . Field; codec\t. NewEncoder()", true, true},
		{`"escaped\" selection.Field"; codec.NewEncoder()`, false, true},
		{"`selection.Field`; '\\''; codec.NewEncoder()", false, true},
		{"// selection.Field", false, false},
		{"/* selection.Field", false, false},
		{"\"unterminated selection.Field", false, false},
		{"变量selection.Field; codec.NewEncoder()", false, true},
	} {
		got := generatedPackages(test.source)
		if got.has("selection") != test.selection || got.has("codec") != test.codec {
			t.Fatalf("imports for %q = %b", test.source, got)
		}
	}
	if allocs := testing.AllocsPerRun(100, func() { generatedPackages(`selection.Field; "codec.NewEncoder"`) }); allocs != 0 {
		t.Fatalf("scan allocated %v times", allocs)
	}
}

func TestHandlerFeaturesIncludeFunctionsAndInferredOr(t *testing.T) {
	model := testModel("User", nil, []*ast.FieldDecl{testField("name", "String"), testField("email", "String")})
	if !inferredAPIsHaveOrGroups([]*ast.ApiDecl{{Name: "listUsersByNameOrEmail"}}, map[string]*ast.ModelDecl{"User": model}) {
		t.Fatal("inferred OR conditions were not detected")
	}
	if !detectAuthNeeded(result(&ast.File{Functions: []*ast.FnDecl{{Name: "secured", Directives: []*ast.Directive{{Name: "auth"}}}}}), nil) {
		t.Fatal("function authentication dependency was missed")
	}
}
