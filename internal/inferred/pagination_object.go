package inferred

import (
	"maps"

	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/pkg/ast"
)

// Go values own top-level slots. Pointer fields share children; initial slots
// are shared separately so lazy reads preserve aliasing across value copies.
type pageObject struct {
	fields  map[string]pageValue
	initial *pageObject
	opaque  ast.Expr
}

func (p *paginationAnalysis) parameters(params []*ast.ParamDecl) map[string]pageValue {
	var bindings map[string]pageValue
	for _, param := range params {
		if param.Type == nil || param.Type.IsList || !StructuredType(param.Type.Name) {
			continue
		}
		if bindings == nil {
			bindings = make(map[string]pageValue)
		}
		bindings[param.Name] = pageValue{object: newPageObject(nil), ref: param.Type.Nullable || p.models[param.Type.Name] != nil}
	}
	return bindings
}

func newPageObject(origin ast.Expr) *pageObject {
	initial := &pageObject{opaque: origin}
	return &pageObject{initial: initial, opaque: origin}
}

// StructuredType identifies object-like declarations independently of spelling.
func StructuredType(name string) bool {
	switch name {
	case "", "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "JSON", "Decimal", "Bytes", "Void":
		return false
	}
	return true
}

func (p *paginationAnalysis) unknownObject(expression ast.Expr) pageValue {
	if expression.IsListType() || !StructuredType(expression.GetTypeTag()) {
		return pageValue{}
	}
	return pageValue{object: newPageObject(expression), ref: expression.IsNullable() || p.models[expression.GetTypeTag()] != nil}
}

func (p *paginationAnalysis) bindObject(value pageValue, expected *ast.TypeRef) pageValue {
	if value.object == nil {
		return value
	}
	// Taking the address of an API model value preserves its existing storage.
	modelAddress := expected != nil && p.models[expected.Name] != nil
	if !value.ref && !modelAddress {
		copy := *value.object
		copy.fields = maps.Clone(copy.fields)
		value.object = &copy
	}
	if expected != nil {
		value.ref = expected.Nullable || modelAddress
	}
	return value
}

func (p *paginationAnalysis) object(expression *ast.ObjectExpr) pageValue {
	object := newPageObject(nil)
	for _, field := range expression.Fields {
		value := p.bindObject(p.expression(field.Value), field.ExpectedType)
		if value.object != nil {
			value.ref = true // Structured fields use pointer storage.
		}
		object.set(field.Name, value)
	}
	return pageValue{object: object, ref: p.models[expression.TypeName] != nil}
}

func (object *pageObject) set(name string, value pageValue) {
	if object.fields == nil {
		object.fields = make(map[string]pageValue)
	}
	object.fields[name] = value
}

func (p *paginationAnalysis) member(expression *ast.MemberExpr) pageValue {
	base := p.expression(expression.Object)
	if builtin.IsDebugProperty(expression) {
		return base
	}
	if base.object == nil {
		if base.opaque != nil {
			return p.objectField(newPageObject(base.opaque), expression)
		}
		p.consume(base, expression)
		base.bad = len(base.queries) != 0
		return base
	}
	value := p.objectField(base.object, expression)
	if value.object != nil {
		value.ref = expression.IsNullable() || p.models[expression.GetTypeTag()] != nil
	}
	return value
}

func (p *paginationAnalysis) objectField(object *pageObject, member *ast.MemberExpr) pageValue {
	if value, found := object.fields[member.Field]; found {
		return value
	}
	initial := object.initial
	if value, found := initial.fields[member.Field]; found {
		return value
	}
	var value pageValue
	if member.IsListType() && object.opaque != nil {
		value = pageValue{owner: object.opaque, queries: []ast.Expr{object.opaque}, bad: true}
	} else if !member.IsListType() && StructuredType(member.GetTypeTag()) {
		value = pageValue{object: newPageObject(object.opaque), ref: true}
	}
	if value.object != nil || len(value.queries) != 0 {
		initial.set(member.Field, value)
	}
	return value
}

func (p *paginationAnalysis) iterationItem(collection ast.Expr) pageValue {
	if collection == nil || !StructuredType(collection.GetTypeTag()) {
		return pageValue{}
	}
	return pageValue{object: newPageObject(collection), ref: p.models[collection.GetTypeTag()] != nil}
}

func (p *paginationAnalysis) assignField(base pageValue, member *ast.MemberExpr, value pageValue, statement *ast.AssignStmt) bool {
	if base.object == nil {
		return false
	}
	if p.depth != 0 || statement.Op != "=" {
		previous := p.objectField(base.object, member)
		p.consume(previous, member)
		p.consume(value, statement.Value)
		value = mergePageValues(previous, value)
		value.bad = len(value.queries) != 0
	}
	expected := &ast.TypeRef{Name: member.GetTypeTag(), Nullable: member.IsNullable(), IsList: member.IsListType()}
	value = p.bindObject(value, expected)
	if value.object != nil {
		value.ref = true
	}
	base.object.set(member.Field, value)
	return true
}

func pageQueries(value pageValue) []ast.Expr {
	if value.object == nil {
		return value.queries
	}
	var queries []ast.Expr
	seen := make(map[*pageObject]bool)
	var visit func(pageValue)
	visit = func(value pageValue) {
		if value.object == nil {
			queries = mergePageValues(pageValue{queries: queries}, value).queries
			return
		}
		object := value.object
		if seen[object] {
			return
		}
		seen[object] = true
		object.eachField(func(field pageValue) bool { visit(field); return true })
	}
	visit(value)
	return queries
}

// Losing field shape must not disguise a database list as a manual list.
func flattenPageObject(value pageValue) pageValue {
	if value.object == nil {
		return value
	}
	value.queries = pageQueries(value)
	value.opaque = pageObjectOrigin(value.object, make(map[*pageObject]bool))
	if len(value.queries) != 0 {
		value.owner = value.queries[0]
		value.bad = true
	}
	value.object = nil
	return value
}

func pageObjectOrigin(object *pageObject, seen map[*pageObject]bool) ast.Expr {
	if object == nil || seen[object] {
		return nil
	}
	if object.opaque != nil {
		return object.opaque
	}
	seen[object] = true
	var origin ast.Expr
	object.eachField(func(field pageValue) bool {
		origin = field.opaque
		if origin == nil {
			origin = pageObjectOrigin(field.object, seen)
		}
		return origin == nil
	})
	return origin
}

func (object *pageObject) eachField(visit func(pageValue) bool) {
	for _, field := range object.fields {
		if !visit(field) {
			return
		}
	}
	for name, field := range object.initial.fields {
		if _, overridden := object.fields[name]; !overridden && !visit(field) {
			return
		}
	}
}
