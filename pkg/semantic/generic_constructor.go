package semantic

import "github.com/light-speak/luxo/pkg/ast"

func (a *Analyzer) genericConstructorType(typ *ResolvedType) bool {
	return typ != nil && (a.genericTemplate(typ.Name) != nil ||
		typ.Kind == TypeGeneric && (typ.Name == "Page" || typ.Name == "Cursor"))
}

func (a *Analyzer) requireConstructorTypeArguments(name string, expression ast.Expr, fields []*ast.NamedArg, scope *Scope) {
	a.addError(expression.GetPos(), "generic constructor '%s' requires explicit type arguments / 泛型构造 '%s' 需要显式类型实参", name, name)
	a.checkObjectExprValues(fields, scope)
}

func (a *Analyzer) checkGenericConstructor(call *ast.CallExpr, scope *Scope) (*ResolvedType, bool) {
	ident, named := call.Func.(*ast.Ident)
	if !named {
		if call.TypeArgs != nil {
			a.rejectGenericConstructor(call, scope)
			return nil, true
		}
		return nil, false
	}
	if call.TypeArgs == nil && a.genericTemplate(ident.Name) == nil && ident.Name != "Page" && ident.Name != "Cursor" {
		return nil, false
	}
	symbol := scope.Lookup(ident.Name)
	generic := symbol != nil && symbol.Kind == SymType && symbol.Type == a.types[ident.Name] && a.genericConstructorType(symbol.Type)
	if call.TypeArgs == nil && !generic {
		return nil, false
	}
	if !generic {
		a.rejectGenericConstructor(call, scope)
		return nil, true
	}
	if call.TypeArgs == nil {
		a.requireConstructorTypeArguments(ident.Name, call, call.Args, scope)
		return nil, true
	}
	return a.resolveGenericConstructor(call, ident, scope), true
}

func (a *Analyzer) rejectGenericConstructor(call *ast.CallExpr, scope *Scope) {
	a.addError(call.Pos, "type arguments require a generic type constructor / 类型实参只能用于泛型类型构造")
	a.checkObjectExprValues(call.Args, scope)
}

func (a *Analyzer) resolveGenericConstructor(call *ast.CallExpr, ident *ast.Ident, scope *Scope) *ResolvedType {
	for _, argument := range call.TypeArgs {
		if argument == nil {
			a.addError(call.Pos, "constructor type argument requires a type reference / 构造类型实参需要类型引用")
			a.checkObjectExprValues(call.Args, scope)
			return nil
		}
	}
	before := len(a.errors)
	ref := &ast.TypeRef{Pos: ident.Pos, Name: ident.Name, TypeArgs: call.TypeArgs}
	typ := a.resolveTypeRefInContext(ref, call.Pos, 0)
	if len(a.errors) != before {
		a.checkObjectExprValues(call.Args, scope)
		return nil
	}
	if a.moduleMap != nil && a.currentFile != nil {
		if module := a.moduleMap[a.currentFile.Name]; module != nil {
			a.checkTypeRefVisibilityInScope(ref, module, call.Pos, a.typeParameters)
		}
	}
	a.setExprResolvedType(ident, typ)
	object := &ast.ObjectExpr{Pos: call.Pos, TypeName: ident.Name, Fields: call.Args}
	provided := a.validateObjectExprFields(object, typ, scope)
	if typ.Kind != TypeCustom {
		a.checkRequiredObjectFields(object, typ, provided)
	}
	if len(a.errors) == before {
		call.ConstructorType = typ.TypeRef()
		call.ConstructorType.Pos = call.Pos
	}
	return typ.AsNonNull()
}
