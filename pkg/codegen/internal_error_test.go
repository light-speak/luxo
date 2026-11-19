package codegen

import (
	"errors"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenerateRejectsStaleStructuredCallMetadata(t *testing.T) {
	const sealedSource = `sealed Choice { Value(value: Int) Empty }
fn create(): Choice { return Choice.Value(value: 7) }`
	for _, sample := range []struct{ name, source, want string }{
		{"sealed owner", sealedSource, "sealed constructor owner metadata is missing"},
		{"sealed variant", sealedSource, "sealed constructor variant metadata is missing"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			analyzed := analyzePermissionFixture(t, sample.source)
			if len(analyzed.Errors) != 0 {
				t.Fatal(analyzed.Errors)
			}
			input := &semantic.Result{Files: analyzed.Files, Types: analyzed.Types, Scope: analyzed.Scope}
			config := GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
				"Choice": {"Value": 1, "Empty": 2}, "Choice.Value": {"value": 1},
			}}}
			if generated, err := Generate(input, "generated", config); err != nil || generated == nil {
				t.Fatalf("valid structured result was rejected: %v", err)
			}
			call := input.Files[0].Functions[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
			member := call.Func.(*ast.MemberExpr)
			switch sample.name {
			case "sealed owner":
				call.ConstructorType.Name = "MissingOwner"
			case "sealed variant":
				member.Field = "MissingVariant"
			}
			generated, err := Generate(input, "generated", config)
			requireInternalGenerationFailure(t, generated, err, sample.want)
		})
	}
}

func TestGenerateRejectsNonScalarJoinMetadataWithoutOutput(t *testing.T) {
	input := analyzePermissionFixture(t, `type Box { value: Int }
fn render(values: [Int]): String { return values.joinToString(",") }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	function := input.Files[0].Functions[0]
	function.Params[0].Type.Name = "Box"
	call := function.Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	call.Func.(*ast.MemberExpr).Object.SetTypeTag("Box")
	generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Box": {"value": 1}}}})
	requireInternalGenerationFailure(t, generated, err, "joinToString requires scalar or enum elements")
}

func TestGenerateRejectsNonNumericSumMetadataWithoutOutput(t *testing.T) {
	input := analyzePermissionFixture(t, `fn total(values: [Int]): Int { return values.sumOf { it } }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	function := input.Files[0].Functions[0]
	call := function.Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	lambda := call.Args[0].Value.(*ast.LambdaExpr)
	lambda.SetTypeTag("String")
	lambda.Body.Stmts[0].(*ast.ExprStmt).Expr = &ast.Literal{Kind: token.String, Value: "invalid"}
	call.SetTypeTag("String")
	function.ReturnType.Name = "String"
	generated, err := Generate(input, "generated", GeneratorConfig{})
	requireInternalGenerationFailure(t, generated, err, "sumOf requires a numeric selector")
}

func TestGenerateRejectsUnavailableTransformDependencyWithoutOutput(t *testing.T) {
	for _, kind := range []string{"missing-body", "native", "service"} {
		t.Run(kind, func(t *testing.T) {
			input, config := transformDependencyGenerationInput(t)
			generator := mustNewGenerator(t, config)
			before, err := generator.Generate(input, "generated", nil)
			if err != nil {
				t.Fatal(err)
			}
			function := input.Files[0].Functions[0]
			if kind == "missing-body" {
				function.Body = nil
			} else {
				function.Directives = append(function.Directives, &ast.Directive{Name: kind})
			}
			generated, err := generator.Generate(input, "generated", nil)
			if kind == "service" {
				if generated != nil || err == nil || !strings.Contains(err.Error(), "API svc:normalize has no stable ID") {
					t.Fatalf("service metadata escaped earlier catalog validation: %v", err)
				}
			} else {
				requireInternalGenerationFailure(t, generated, err, "field function requires unavailable runtime dependency normalize")
			}
			restored, _ := transformDependencyGenerationInput(t)
			after, err := generator.Generate(restored, "generated", nil)
			if err != nil || after == nil || len(after.Files) != len(before.Files) {
				t.Fatalf("generation after rejection failed: %v", err)
			}
			for name, content := range before.Files {
				if string(content) != string(after.Files[name]) {
					t.Errorf("rejected dependency contaminated %s", name)
				}
			}
		})
	}
}

func TestGenerateRejectsInvalidTransformOperandsWithoutOutput(t *testing.T) {
	unknown := &ast.LambdaExpr{}
	literal := &ast.Literal{Kind: token.Int, Value: "1"}
	for _, sample := range []struct {
		name       string
		expression ast.Expr
	}{
		{"unary-operator", &ast.UnaryExpr{Op: "~", Value: literal}},
		{"unary-child", &ast.UnaryExpr{Op: "!", Value: unknown}},
		{"binary-left", &ast.BinaryExpr{Op: "+", Left: unknown, Right: literal}},
		{"binary-right", &ast.BinaryExpr{Op: "+", Left: literal, Right: unknown}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			field := &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: sample.expression}}}}}}
			input := &semantic.Result{Files: []*ast.File{{Types: []*ast.TypeDecl{{Name: "Payload", Fields: []*ast.FieldDecl{field}}}}}}
			generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Payload": {"value": 1}}}})
			requireInternalGenerationFailure(t, generated, err, "unsupported @transform expression for field value")
		})
	}
}

func requireInternalGenerationFailure(t *testing.T, generated *GenerateResult, err error, detail string) {
	t.Helper()
	var failure *InternalCompilerError
	if generated != nil || !errors.As(err, &failure) || len(failure.Stack) == 0 || !strings.Contains(failure.Detail, detail) {
		t.Fatalf("generation returned partial output or lost its internal error: %v", err)
	}
}

func transformDependencyGenerationInput(t *testing.T) (*semantic.Result, GeneratorConfig) {
	t.Helper()
	input := analyzePermissionFixture(t, `fn normalize(value: String): String { return value.trim() }
model Row @noTime { id: Int @id name: String @transform { normalize(it) } }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	return input, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Row": {"id": 1, "name": 2}}}}
}

func TestGenerateRejectsMissingGenericExpressionType(t *testing.T) {
	input := analyzePermissionFixture(t, `api probe(flag: Boolean): Int {
 val left: Channel<Int> = Channel(1)
 val channel = when { flag -> left else -> left }
 channel <- 7
 return <-channel
}`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	input.GenericExprTypes = nil
	output, err := Generate(input, "generated", GeneratorConfig{IDs: completeFixtureAPIIDs(input, StableIDs{})})
	var failure *InternalCompilerError
	if output != nil || !errors.As(err, &failure) || !strings.Contains(failure.Detail, "Channel element metadata") {
		t.Fatalf("missing generic type output=%t error=%v", output != nil, err)
	}
}

func TestGenerateRejectsInvalidChannelBindingAST(t *testing.T) {
	for _, condition := range []ast.Expr{
		&ast.Ident{Name: "invalid"},
		&ast.UnaryExpr{Op: "-", Value: &ast.Ident{Name: "channel"}},
	} {
		input := analyzePermissionFixture(t, `api probe(): Int {
 val channel: Channel<Int> = Channel(1)
 return when { <-channel -> { value -> value } <-channel -> { other -> other } }
}`)
		if len(input.Errors) != 0 {
			t.Fatal(input.Errors)
		}
		body := input.Files[0].APIs[0].Body
		expr := body.Stmts[len(body.Stmts)-1].(*ast.ReturnStmt).Value.(*ast.WhenExpr)
		expr.Branches[0].Condition = condition
		output, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"probe": 1}}})
		var failure *InternalCompilerError
		if output != nil || !errors.As(err, &failure) || !strings.Contains(failure.Detail, "channel receive binding") {
			t.Fatalf("invalid receive binding output=%t error=%v", output != nil, err)
		}
	}
}

// Embedding preserves valid metadata while simulating an unknown AST variant.
type unsupportedExpression struct{ *ast.Literal }

func TestGenerateRejectsUntypedLambdaWithoutOutput(t *testing.T) {
	for _, sample := range []struct {
		name   string
		lambda *ast.LambdaExpr
	}{
		{"named", &ast.LambdaExpr{Params: []string{"value"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "value"}}}}}},
		{"implicit", &ast.LambdaExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "it"}}}}}},
		{"empty", &ast.LambdaExpr{Body: &ast.Block{}}},
		{"loop", &ast.LambdaExpr{Params: []string{"items"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ForStmt{VarName: "item", Collection: &ast.Ident{Name: "items"}, Body: &ast.Block{}}}}}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := &semantic.Result{Files: []*ast.File{{APIs: []*ast.ApiDecl{{
				Name: "broken", ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: sample.lambda}}},
			}}}}}
			result, err := Generate(input, "example", GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"broken": 1}}})
			var failure *InternalCompilerError
			if result != nil || !errors.As(err, &failure) || len(failure.Stack) == 0 || !strings.Contains(failure.Detail, "untyped lambda") {
				t.Fatalf("untyped lambda output=%t error=%v", result != nil, err)
			}
		})
	}
}

func TestGenerateReportsInternalExpressionFailure(t *testing.T) {
	value := &unsupportedExpression{&ast.Literal{Kind: token.Int, Value: "1"}}
	input := &semantic.Result{Files: []*ast.File{{APIs: []*ast.ApiDecl{{
		Name: "broken", ReturnType: &ast.TypeRef{Name: "Int"},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: value}}},
	}}}}}
	result, err := Generate(input, "example", GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"broken": 1}}})
	var failure *InternalCompilerError
	if result != nil || !errors.As(err, &failure) || len(failure.Stack) == 0 || !strings.Contains(failure.Detail, "unhandled expression") {
		t.Fatalf("result=%v error=%v", result, err)
	}
}

func TestGenerateReportsInternalASTFailure(t *testing.T) {
	input := &semantic.Result{Files: []*ast.File{{APIs: []*ast.ApiDecl{{
		Name: "broken", ReturnType: &ast.TypeRef{Name: "Int"},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.OnDecl{}}},
	}}}}}
	result, err := Generate(input, "example", GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"broken": 1}}})
	var failure *InternalCompilerError
	if result != nil || !errors.As(err, &failure) {
		t.Fatalf("result=%v error=%v", result, err)
	}
	if !strings.Contains(failure.Error(), "unhandled statement") || len(failure.Stack) == 0 {
		t.Fatalf("missing diagnostic: %+v", failure)
	}
}

func TestCompilerRecoveryDoesNotSwallowOtherPanics(t *testing.T) {
	want := errors.New("unrelated bug")
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic=%v", got)
		}
	}()
	var result *GenerateResult
	var err error
	func() { defer recoverCompilerFailure(&result, &err); panic(want) }()
}

func TestGenerateRejectsMissingAnalysis(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{})
	for name, generate := range map[string]func() (*GenerateResult, error){
		"function": func() (*GenerateResult, error) { return Generate(nil, "example", GeneratorConfig{}) },
		"context":  func() (*GenerateResult, error) { return g.Generate(nil, "example", nil) },
	} {
		t.Run(name, func(t *testing.T) {
			result, err := generate()
			if result != nil || err == nil || !strings.Contains(err.Error(), "semantic result") {
				t.Fatalf("result=%v error=%v", result, err)
			}
		})
	}
}

func TestGenerateRejectsUnsupportedVisibilityWithoutOutput(t *testing.T) {
	field := &ast.FieldDecl{Name: "secret", Type: &ast.TypeRef{Name: "String", Nullable: true}, Directives: []*ast.Directive{{Name: "visible", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "unsupported"}}}}}}}}
	input := &semantic.Result{Files: []*ast.File{{Types: []*ast.TypeDecl{{Name: "Payload", Fields: []*ast.FieldDecl{field}}}}}}
	result, err := Generate(input, "example", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Payload": {"secret": 1}}}})
	var failure *InternalCompilerError
	if result != nil || !errors.As(err, &failure) || !strings.Contains(failure.Detail, "@visible") {
		t.Fatalf("result=%v error=%v", result, err)
	}
}

func TestGenerateRejectsUnsupportedTransformWithoutOutput(t *testing.T) {
	for _, expression := range []ast.Expr{
		&ast.LambdaExpr{},
		&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.LambdaExpr{}, Field: "trim"}},
	} {
		field := &ast.FieldDecl{Name: "secret", Type: &ast.TypeRef{Name: "String", Nullable: true}, Directives: []*ast.Directive{{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: expression}}}}}}
		input := &semantic.Result{Files: []*ast.File{{Types: []*ast.TypeDecl{{Name: "Payload", Fields: []*ast.FieldDecl{field}}}}}}
		result, err := Generate(input, "example", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Payload": {"secret": 1}}}})
		var failure *InternalCompilerError
		if result != nil || !errors.As(err, &failure) || !strings.Contains(failure.Detail, "@transform") {
			t.Fatalf("result=%v error=%v", result, err)
		}
	}
}
