package ast

import (
	"strconv"

	"github.com/light-speak/luxo/pkg/token"
)

// DefaultPaginationPageSize is the protocol default when @paginate omits one.
const DefaultPaginationPageSize = 20

// EffectiveParams returns declared API parameters plus parameters injected by
// language directives. The returned slice never mutates the parsed AST.
func (a *ApiDecl) EffectiveParams() []*ParamDecl {
	if a == nil {
		return nil
	}
	if a.directive("paginate") == nil {
		return a.Params
	}
	params := append([]*ParamDecl(nil), a.Params...)
	if !hasAPIParam(params, "page") {
		params = append(params, paginationParam("page", "1"))
	}
	if !hasAPIParam(params, "pageSize") {
		params = append(params, paginationParam("pageSize", strconv.Itoa(a.DefaultPageSize())))
	}
	return params
}

// DefaultPageSize returns the validated @paginate default, or the protocol
// default when the directive is absent or still contains invalid syntax.
func (a *ApiDecl) DefaultPageSize() int {
	directive := a.directive("paginate")
	if directive == nil || len(directive.Args) == 0 {
		return DefaultPaginationPageSize
	}
	arg := directive.Args[0]
	for _, candidate := range directive.Args {
		if candidate.Name == "defaultPageSize" {
			arg = candidate
			break
		}
	}
	literal, ok := arg.Value.(*Literal)
	if !ok || literal.Kind != token.Int {
		return DefaultPaginationPageSize
	}
	value, err := strconv.Atoi(literal.Value)
	if err != nil || value <= 0 {
		return DefaultPaginationPageSize
	}
	return value
}

func (a *ApiDecl) directive(name string) *Directive {
	if a == nil {
		return nil
	}
	for _, directive := range a.Directives {
		if directive.Name == name {
			return directive
		}
	}
	return nil
}

func hasAPIParam(params []*ParamDecl, name string) bool {
	for _, param := range params {
		if param.Name == name {
			return true
		}
	}
	return false
}

func paginationParam(name, defaultValue string) *ParamDecl {
	return &ParamDecl{
		Name:    name,
		Type:    &TypeRef{Name: "Int"},
		Default: &Literal{Kind: token.Int, Value: defaultValue},
	}
}
