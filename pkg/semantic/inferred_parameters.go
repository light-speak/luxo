package semantic

import (
	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
)

// Explicit parameters bind positionally to name-derived clauses. Native and
// body APIs own their arguments; omitted parameters retain automatic inference.
func (a *Analyzer) validateInferredParameters(files []*ast.File) {
	var models map[string]*ast.ModelDecl
	for _, file := range files {
		for _, api := range file.APIs {
			if api.Body != nil || len(api.Params) == 0 || hasNamedDirective(api.Directives, "native") {
				continue
			}
			if models == nil {
				models = inferred.Models(files)
			}
			query := inferred.Parse(api.Name, models)
			if query == nil {
				continue
			}
			expected := inferred.Parameters(query, models[query.ModelName])
			a.validateInferredParameterTypes(api, expected)
		}
	}
}

func (a *Analyzer) validateInferredParameterTypes(api *ast.ApiDecl, expected []*ast.ParamDecl) {
	if len(api.Params) != len(expected) {
		a.addError(api.Pos, "inferred API '%s' declares %d parameters but its name consumes %d; use matching By clauses, an explicit body, or @native", api.Name, len(api.Params), len(expected))
		return
	}
	for index, param := range api.Params {
		if !sameTypeRefExact(param.Type, expected[index].Type) {
			a.addError(param.Pos, "inferred parameter '%s' in API '%s' has type %s, expected %s for its positional query clause", param.Name, api.Name, formatTypeRef(param.Type), formatTypeRef(expected[index].Type))
		}
	}
}
