package semantic

import (
	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
)

func hasPaginationBody(files []*ast.File) bool {
	for _, file := range files {
		for _, api := range file.APIs {
			if api.Body != nil && hasNamedDirective(api.Directives, "paginate") {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) validatePaginationPlans(files []*ast.File) {
	if len(a.errors) != 0 || !hasPaginationBody(files) {
		return
	}
	models := make(map[string]*ast.ModelDecl)
	for _, file := range files {
		for _, model := range file.Models {
			models[model.Name] = model
		}
	}
	for _, file := range files {
		for _, api := range file.APIs {
			if _, err := inferred.PlanPagination(api, models, a.functions); err != nil {
				a.addError(api.Pos, "%s", err)
			}
		}
	}
}
