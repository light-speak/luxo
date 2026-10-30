package codegen

import (
	goast "go/ast"
	"go/parser"
	gotoken "go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedSignaturesImportScalarPackages(t *testing.T) {
	for _, kind := range []string{"error", "stream", "native"} {
		for _, scalar := range []struct{ name, path string }{
			{"JSON", "encoding/json"}, {"DateTime", "time"}, {"Duration", "time"},
			{"UUID", "github.com/google/uuid"}, {"Decimal", "github.com/shopspring/decimal"},
			{"Int", ""}, {"String", ""}, {"Bytes", ""},
		} {
			for _, wrapper := range []string{"scalar", "list", "nullable"} {
				t.Run(kind+"/"+scalar.name+"/"+wrapper, func(t *testing.T) {
					ref := &ast.TypeRef{Name: scalar.name, IsList: wrapper == "list", Nullable: wrapper == "nullable"}
					code := signatureImportFixture(kind, ref)
					file, err := parser.ParseFile(gotoken.NewFileSet(), "generated.go", code, parser.ImportsOnly)
					if err != nil {
						t.Fatal(err)
					}
					assertScalarImports(t, file, scalar.path)
				})
			}
		}
	}
}

func signatureImportFixture(kind string, ref *ast.TypeRef) []byte {
	param := &ast.ParamDecl{Name: "value", Type: ref}
	api := &ast.ApiDecl{Name: "watchValue", Params: []*ast.ParamDecl{param}, ReturnType: ref,
		Directives: []*ast.Directive{{Name: "native"}}}
	file := &ast.File{Name: "test.luxo", APIs: []*ast.ApiDecl{api}, Errors: []*ast.ErrorDecl{{
		Name: "Rejected", Code: 422, Fields: []*ast.ParamDecl{param},
		Message: "json.RawMessage time.Time uuid.UUID decimal.Decimal", // Literal package names must not add imports.
	}}}
	switch kind {
	case "error":
		return generateErrorFile(result(file), "luxo")
	case "stream":
		api.Directives = append(api.Directives, &ast.Directive{Name: "stream"})
		return generateStreamFile(result(file), "luxo")
	default:
		return GenerateNativeFile(result(file), "luxo")
	}
}

func assertScalarImports(t *testing.T, file *goast.File, expected string) {
	t.Helper()
	counts := make(map[string]int)
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		counts[path]++
	}
	for _, path := range []string{"encoding/json", "time", "github.com/google/uuid", "github.com/shopspring/decimal"} {
		want := 0
		if path == expected {
			want = 1
		}
		if counts[path] != want {
			t.Errorf("import %q count = %d, want %d", path, counts[path], want)
		}
	}
}

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

func TestHandlerStringUtilityImports(t *testing.T) {
	for _, sample := range []struct{ source, path string }{
		{`str.Matches("b", value)`, "github.com/light-speak/luxo/pkg/lux/str"},
		{`convert.StringToInt(value)`, "github.com/light-speak/luxo/pkg/lux/convert"},
	} {
		var out strings.Builder
		writeHandlerImports(&out, &semantic.Result{}, nil, handlerFeatures{}, false, sample.source)
		if !strings.Contains(out.String(), strconv.Quote(sample.path)) {
			t.Fatalf("missing import %s: %s", sample.path, out.String())
		}
		out.Reset()
		writeHandlerImports(&out, &semantic.Result{}, nil, handlerFeatures{}, false, strconv.Quote(sample.source))
		if strings.Contains(out.String(), strconv.Quote(sample.path)) {
			t.Fatalf("literal added unused import: %s", out.String())
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

func TestGeneratedContextImport(t *testing.T) {
	for source, want := range map[string]bool{
		`context.Context`:                       true,
		`context /* boundary */ . Background()`: true,
		`"context.Context"`:                     false,
		`object.context.Context`:                false,
	} {
		if got := generatedPackages(source).has("context"); got != want {
			t.Fatalf("context import for %q = %v, want %v", source, got, want)
		}
	}
}

func TestGeneratedPackagesStopsAfterRequiredSet(t *testing.T) {
	wanted := generatedPackageBit("json") | generatedPackageBit("time")
	before := scanGeneratedPackages(`json.RawMessage; "time.Time"; codec.Encoder`, wanted)
	if !before.has("json") || before.has("time") || !before.has("codec") {
		t.Fatalf("incomplete set stopped early: %b", before)
	}
	after := scanGeneratedPackages(`json.RawMessage; time.Time; codec.Encoder`, wanted)
	if !after.has("json") || !after.has("time") || after.has("codec") {
		t.Fatalf("complete set scanned past its boundary: %b", after)
	}
	full := generatedPackages(`json.RawMessage; time.Time; codec.Encoder`)
	if !full.has("codec") {
		t.Fatal("unrestricted scan must retain later packages")
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

func TestGeneratedImportsRespectLocalBindings(t *testing.T) {
	for _, sample := range []struct {
		body string
		want bool
	}{
		{`func f(selection Input) { _ = selection.Value }`, false},
		{`func f() { selection := Input{}; _ = selection.Value }`, false},
		{`func f() { var selection *Input; _ = selection.Value }`, false},
		{`func f(selection Input) { _ = selection.Value }; func g() { _ = selection.Field }`, true},
		{`func f() { _ = selection.Field; { selection := Input{}; _ = selection.Value } }`, true},
		{`selection := Input{}; _ = selection.Value`, false},
		{`func f(selection Input) { _ = selection.Value`, true},
	} {
		if got := generatedPackages(sample.body).has("selection"); got != sample.want {
			t.Errorf("import=%v want=%v: %s", got, sample.want, sample.body)
		}
	}
}

func TestFormalLocalPackageName(t *testing.T) {
	const source = `type Input { value: Int }
fn readInput(selection: Input): Int { return selection.value }`
	runGeneratedFixture(t, source, `package generated
import "testing"
type App struct{}
func TestReadInput(t *testing.T) {
 value,err:=(&App{}).readInput(t.Context(),Input{Value:42})
 if err!=nil || value!=42 {t.Fatal(value,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Input": {"value": 1}}})
}
