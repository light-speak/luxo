package semantic

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestCRUDScopeFieldPrecedenceAndOwnership(t *testing.T) {
	parentFieldType := &ResolvedType{Kind: TypeString, Name: "String"}
	localFieldType := &ResolvedType{Kind: TypeInt, Name: "Int"}
	firstParent := &ResolvedType{Fields: map[string]*FieldInfo{
		"shared": {Type: parentFieldType}, "inherited": {Type: parentFieldType},
	}}
	secondParent := &ResolvedType{Fields: map[string]*FieldInfo{
		"inherited": {Type: localFieldType},
	}}
	model := &ResolvedType{Kind: TypeModel, Name: "Item", Parents: []*ResolvedType{firstParent, secondParent},
		Fields: map[string]*FieldInfo{"shared": {Type: localFieldType}, "it": {Type: localFieldType}}}
	a := &Analyzer{types: map[string]*ResolvedType{"Item": model}}
	scope := NewScope()
	outer := &Symbol{Name: "shared", Kind: SymParam, Type: parentFieldType}
	scope.Define(outer)
	call := &ast.CallExpr{Func: &ast.Ident{Name: "find"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Item"}}}}

	left, right := a.injectCRUDScope(call, scope, true), a.injectCRUDScope(call, scope, true)
	for name, expected := range map[string]*ResolvedType{
		"shared": localFieldType, "inherited": parentFieldType, "it": localFieldType,
	} {
		field := left.Lookup(name)
		other := right.LookupLocal(name)
		if field == nil || field.Kind != SymField || field.Type != expected || !field.Used {
			t.Fatalf("field %q = %+v", name, field)
		}
		if other == nil || other == field || other.Type != expected || other.Used {
			t.Fatalf("field %q in a separate query = %+v", name, other)
		}
	}
	if outer.Used {
		t.Fatal("model field lookup marked the shadowed caller symbol")
	}
}

func TestCRUDNamedValueScopeStillRecordsCacheRead(t *testing.T) {
	model := &ResolvedType{Kind: TypeModel, Name: "Item", Fields: map[string]*FieldInfo{"id": {Type: &ResolvedType{Kind: TypeInt, Name: "Int"}}}}
	a := New()
	a.types["Item"] = model
	a.typeOwners.register("Item", "inventory")
	a.cacheModelReads = make(map[ast.Expr]CacheDependency)
	a.scope.Define(&Symbol{Name: "Item", Kind: SymModel, Type: model})
	scope := a.scope.Child()
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Item"}, Field: "find"},
		Args: []*ast.NamedArg{{Name: "id", Value: &ast.Ident{Name: "id"}}},
	}
	if actual := a.injectCRUDScope(call, scope, callNeedsModelFields(call.Args, true)); actual != scope {
		t.Fatal("named CRUD value allocated a model field scope")
	}
	if got := a.cacheModelReads[call]; got.Name != "Item" || got.Kind != CacheModelDependency || got.Module != "inventory" {
		t.Fatalf("cached dependency = %+v", got)
	}
}
