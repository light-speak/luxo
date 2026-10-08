package codegen

import (
	"errors"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// Embedding preserves valid metadata while simulating an unknown AST variant.
type unsupportedExpression struct{ *ast.Literal }

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
