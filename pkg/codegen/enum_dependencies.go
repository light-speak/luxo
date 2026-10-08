package codegen

import (
	"slices"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// withEnumDependencies preserves declaration ownership without importing services.
func (g *GeneratorContext) withEnumDependencies(result *semantic.Result) *semantic.Result {
	if !g.usesSharedEnums() {
		return result
	}
	references := enumReferences{names: make(map[string]bool)}
	local := collectEnums(result)
	for _, file := range result.Files {
		references.file(file)
	}
	var names []string
	for name := range references.names {
		if !local[name] && g.events.Enums[name] != nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return result
	}
	slices.Sort(names)
	copy := *result
	copy.Files = slices.Clone(result.Files)
	for _, name := range names {
		copy.Files = append(copy.Files, &ast.File{
			Name:  "origin/" + g.events.EnumModule[name] + "/enums.luxo",
			Enums: []*ast.EnumDecl{g.events.Enums[name]},
		})
	}
	return &copy
}

type enumReferences struct{ names map[string]bool }

func (r enumReferences) typeRef(ref *ast.TypeRef) {
	if ref == nil {
		return
	}
	r.names[ref.Name] = true
	for _, child := range ref.TypeArgs {
		r.typeRef(child)
	}
	for _, child := range ref.Tuple {
		r.typeRef(child)
	}
}

func (r enumReferences) expr(expr ast.Expr) {
	r.names[expr.GetTypeTag()] = true
	if ident, ok := expr.(*ast.Ident); ok {
		r.names[ident.Name] = true
	}
}

func (r enumReferences) params(params []*ast.ParamDecl) {
	for _, param := range params {
		r.typeRef(param.Type)
		ast.WalkExpr(param.Default, r.expr)
	}
}

func (r enumReferences) fields(fields []*ast.FieldDecl) {
	for _, field := range fields {
		r.typeRef(field.Type)
		ast.WalkExpr(field.Default, r.expr)
		r.directives(field.Directives)
	}
}

func (r enumReferences) directives(directives []*ast.Directive) {
	for _, directive := range directives {
		for _, arg := range directive.Args {
			ast.WalkExpr(arg.Value, r.expr)
		}
		ast.WalkExprs(directive.Body, r.expr)
	}
}

func (r enumReferences) function(fn *ast.FnDecl) {
	r.params(fn.Params)
	r.typeRef(fn.ReturnType)
	ast.WalkExprs(fn.Body, r.expr)
	r.directives(fn.Directives)
}

func (r enumReferences) file(file *ast.File) {
	for _, model := range file.Models {
		r.fields(model.Fields)
		r.directives(model.Directives)
		for _, scope := range model.Scopes {
			r.params(scope.Params)
			ast.WalkExpr(scope.Expr, r.expr)
		}
	}
	for _, ext := range file.Extends {
		r.fields(ext.Fields)
	}
	for _, declaration := range file.Types {
		r.fields(declaration.Fields)
	}
	for _, api := range file.APIs {
		r.params(api.Params)
		r.typeRef(api.ReturnType)
		ast.WalkExprs(api.Body, r.expr)
		r.directives(api.Directives)
	}
	for _, fn := range file.Functions {
		r.function(fn)
	}
	for _, event := range file.Events {
		r.params(event.Params)
	}
	for _, declaration := range file.Errors {
		r.params(declaration.Fields)
	}
	r.otherDeclarations(file)
}

func (r enumReferences) otherDeclarations(file *ast.File) {
	for _, declaration := range file.Interfaces {
		r.fields(declaration.Fields)
		for _, fn := range declaration.Methods {
			r.function(fn)
		}
	}
	for _, declaration := range file.Sealeds {
		for _, variant := range declaration.Variants {
			r.params(variant.Fields)
		}
	}
	for _, listener := range file.Listeners {
		ast.WalkExprs(listener.Body, r.expr)
	}
	for _, middleware := range file.Middlewares {
		ast.WalkExprs(middleware.Body, r.expr)
		r.directives(middleware.Directives)
	}
	for _, global := range file.Globals {
		r.typeRef(global.Type)
		ast.WalkExpr(global.Value, r.expr)
	}
}
