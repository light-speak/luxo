package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenerateErrorFileNoErrors(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Name: "test.luxo"}}}
	src := generateErrorFile(result, "luxo")
	if src != nil {
		t.Error("should return nil when no errors")
	}
}

func TestGenerateErrorFileWithParams(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Errors: []*ast.ErrorDecl{
				{
					Pos:     token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:    "OutOfStock",
					Code:    409,
					Message: "error.out_of_stock",
					Fields: []*ast.ParamDecl{
						{Name: "productId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "available", Type: &ast.TypeRef{Name: "Int"}},
					},
				},
			},
		}},
	}

	src := generateErrorFile(result, "shop")
	if src == nil {
		t.Fatal("should generate error file")
	}
	code := string(src)

	checks := []string{
		"package shop",
		`"github.com/light-speak/luxo/pkg/lux/errors"`,
		"func NewOutOfStock(productId int64, available int64) *errors.AppError",
		`errors.New("OutOfStock", 409, "error.out_of_stock")`,
		`.WithData(map[string]any{`,
		`"productId": productId,`,
		`"available": available,`,
	}
	for _, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing %q in:\n%s", check, code)
		}
	}
}

func TestGenerateErrorFileNoParams(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Errors: []*ast.ErrorDecl{
				{
					Name:    "Unauthorized",
					Code:    401,
					Message: "error.unauthorized",
				},
			},
		}},
	}

	src := generateErrorFile(result, "auth")
	code := string(src)

	if !strings.Contains(code, "func NewUnauthorized() *errors.AppError") {
		t.Errorf("no-param error should have empty param list:\n%s", code)
	}
	// Should NOT have WithData
	if strings.Contains(code, "WithData") {
		t.Errorf("no-param error should not call WithData:\n%s", code)
	}
}

func TestGenerateErrorFileMultiple(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Errors: []*ast.ErrorDecl{
				{Name: "NotFound", Code: 404, Message: "error.not_found",
					Fields: []*ast.ParamDecl{{Name: "resource", Type: &ast.TypeRef{Name: "String"}}}},
				{Name: "Conflict", Code: 409, Message: "error.conflict"},
			},
		}},
	}

	src := generateErrorFile(result, "app")
	code := string(src)

	if !strings.Contains(code, "NewNotFound") {
		t.Error("missing NewNotFound")
	}
	if !strings.Contains(code, "NewConflict") {
		t.Error("missing NewConflict")
	}
}

func TestGenerateErrorFileMultiFiles(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{Name: "a.luxo", Errors: []*ast.ErrorDecl{
				{Name: "ErrA", Code: 400, Message: "error.a"},
			}},
			{Name: "b.luxo", Errors: []*ast.ErrorDecl{
				{Name: "ErrB", Code: 500, Message: "error.b"},
			}},
		},
	}

	src := generateErrorFile(result, "multi")
	code := string(src)

	if !strings.Contains(code, "NewErrA") || !strings.Contains(code, "NewErrB") {
		t.Errorf("should generate constructors from all files:\n%s", code)
	}
}
