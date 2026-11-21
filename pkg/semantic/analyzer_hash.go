package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

func hashVerificationParameters(name string) *ast.FnDecl {
	return &ast.FnDecl{Name: name, Params: []*ast.ParamDecl{{Name: "plain", Type: &ast.TypeRef{Name: "String"}}}}
}

// Resolve effective stored fields before injecting methods, including inherited fields.
func effectiveHashFields(model *ResolvedType) []*FieldInfo {
	var fields []*FieldInfo
	seenNames := make(map[string]bool)
	seenTypes := make(map[*ResolvedType]bool)
	var visit func(*ResolvedType)
	visit = func(typ *ResolvedType) {
		if typ == nil || seenTypes[typ] {
			return
		}
		seenTypes[typ] = true
		for name, field := range typ.Fields {
			if field.IsMethod || seenNames[name] {
				continue
			}
			seenNames[name] = true
			if hasDirective(field.Directives, "hash") {
				fields = append(fields, field)
			}
		}
		for _, parent := range typ.Parents {
			visit(parent)
		}
	}
	visit(model)
	return fields
}

func (a *Analyzer) injectHashMethods(model *ResolvedType) {
	for _, field := range effectiveHashFields(model) {
		name := "verify" + str.Capitalize(field.Name)
		if existing := model.LookupField(name); existing != nil && !existing.IsMethod {
			a.addError(existing.Pos, "field '%s' conflicts with @hash injected method", name)
			continue
		}
		model.Fields[name] = &FieldInfo{
			Name: name, Type: a.types["Boolean"], IsMethod: true, Directives: []string{"hash"}, Pos: field.Pos,
			Doc: "." + name + "(plain: String): Boolean — Verify plaintext against @hash field",
		}
	}
}
