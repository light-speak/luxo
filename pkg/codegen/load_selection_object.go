package codegen

import (
	"maps"
	"slices"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
)

// Object slots follow generated Go storage: values copy top-level slots, model
// and nullable bindings share storage, and structured fields point to children.
// Initial slots are shared so lazy input members preserve nested aliases.
type loadObject struct {
	loadObjectFields
	origin  loadDemand
	initial *loadObjectFields
	base    []loadOrigin
}

type loadObjectFields struct {
	fields map[string][]loadOrigin
	keys   []string
}

func newLoadObject(base []loadOrigin) *loadObject {
	object := &loadObject{base: base}
	object.origin.object = object
	return object
}

func loadObjectOrigin(object *loadObject) loadOrigin {
	return loadOrigin{demand: &object.origin}
}

func (object *loadObject) initialFields() *loadObjectFields {
	if object.initial == nil {
		object.initial = &loadObjectFields{}
	}
	return object.initial
}

func walkLoadInputs(body *ast.Block, visit func(ast.Expr)) bool {
	storage := false
	ast.Walk(body, ast.Visitor{
		Stmt: func(statement ast.Stmt) {
			if assignment, ok := statement.(*ast.AssignStmt); ok {
				_, member := assignment.Target.(*ast.MemberExpr)
				storage = storage || member
			}
		},
		Expr: func(expression ast.Expr) {
			_, object := expression.(*ast.ObjectExpr)
			storage = storage || object
			if call, ok := expression.(*ast.CallExpr); ok && isLoadObjectConstructor(call) {
				storage = true
			}
			visit(expression)
		},
	})
	return storage
}

func (object *loadObjectFields) set(name string, value []loadOrigin) {
	if object.fields == nil {
		object.fields = make(map[string][]loadOrigin)
	}
	if _, exists := object.fields[name]; !exists {
		object.keys = append(object.keys, name)
	}
	object.fields[name] = value
}

func (object *loadObject) eachField(visit func([]loadOrigin)) {
	object.eachNamedField(func(_ string, fields []loadOrigin) { visit(fields) })
}

func (object *loadObject) eachNamedField(visit func(string, []loadOrigin)) {
	for _, name := range object.keys {
		visit(name, object.fields[name])
	}
	if object.initial == nil {
		return
	}
	for _, name := range object.initial.keys {
		if _, overridden := object.fields[name]; !overridden {
			visit(name, object.initial.fields[name])
		}
	}
}

func (a *loadDemandAnalysis) object(expression *ast.ObjectExpr) []loadOrigin {
	return a.objectFields(expression.Fields)
}

func (a *loadDemandAnalysis) objectFields(fields []*ast.NamedArg) []loadOrigin {
	object := newLoadObject(nil)
	for _, field := range fields {
		object.set(field.Name, a.bindObject(a.expression(field.Value), field.Value, field.ExpectedType))
	}
	return []loadOrigin{loadObjectOrigin(object)}
}

func (a *loadDemandAnalysis) bindObject(origins []loadOrigin, expression ast.Expr, expected *ast.TypeRef) []loadOrigin {
	if !a.loadObjectValue(origins, expression, expected) {
		return origins
	}
	// A literal has just created unique slots; only existing values need a copy.
	if _, fresh := expression.(*ast.ObjectExpr); fresh {
		return origins
	}
	if call, ok := expression.(*ast.CallExpr); ok && isLoadObjectConstructor(call) {
		return origins
	}
	return copyLoadObjectSlots(origins, false)
}

func (a *loadDemandAnalysis) loadObjectValue(origins []loadOrigin, expression ast.Expr, expected *ast.TypeRef) bool {
	return len(origins) != 0 && origins[0].demand.object != nil && !expression.IsNullable() &&
		a.models[expression.GetTypeTag()] == nil && (expected == nil || a.models[expected.Name] == nil)
}

func (a *loadDemandAnalysis) bindLoadAliases(scope map[string][]loadOrigin, names []string, origins []loadOrigin, expression ast.Expr, expected *ast.TypeRef) {
	bound := a.bindObject(origins, expression, expected)
	copySlots := len(names) > 1 && !expression.IsListType() && a.loadObjectValue(bound, expression, expected) &&
		(expected == nil || !expected.Nullable && !expected.IsList)
	if copySlots && len(bound) == 1 && bound[0].demand.object.initial == nil {
		bindFreshLoadAliases(scope, names, bound)
		return
	}
	used := false
	for _, name := range names {
		if name == "_" {
			continue
		}
		// Scalar fan-out evaluates the initializer once. Each value alias has
		// its own top-level slots and shares the same reference fields.
		if used && copySlots {
			bound = copyLoadObjectSlots(bound, true)
		}
		scope[name] = bound
		used = true
	}
}

type loadAliasSlot struct {
	object  loadObject
	origins [1]loadOrigin
}

func bindFreshLoadAliases(scope map[string][]loadOrigin, names []string, bound []loadOrigin) {
	count := 0
	for _, name := range names {
		if name != "_" {
			count++
		}
	}
	object := bound[0].demand.object
	var slots []loadAliasSlot
	if count > 1 {
		object.shareInitialSlots()
		slots = make([]loadAliasSlot, count-1)
	}
	first := true
	for _, name := range names {
		if name == "_" {
			continue
		}
		if first {
			scope[name] = bound
			first = false
			continue
		}
		slot := &slots[0]
		slots = slots[1:]
		slot.object = *object
		slot.object.origin.object = &slot.object
		slot.origins[0] = bound[0]
		slot.origins[0].demand = &slot.object.origin
		scope[name] = slot.origins[:]
	}
}

func (object *loadObject) shareInitialSlots() {
	// Each owner can override the shared initialization in its private slots.
	object.initial = &loadObjectFields{fields: object.fields, keys: object.keys}
	object.loadObjectFields = loadObjectFields{}
}

func copyLoadObjectSlots(origins []loadOrigin, shareFresh bool) []loadOrigin {
	bound := slices.Clone(origins)
	for index, origin := range bound {
		object := origin.demand.object
		if object == nil {
			continue
		}
		if shareFresh && object.initial == nil {
			object.shareInitialSlots()
		} else {
			object.initialFields()
		}
		copy := *object
		copy.fields = maps.Clone(copy.fields)
		copy.keys = slices.Clone(copy.keys)
		copy.origin.object = &copy
		bound[index].demand = &copy.origin
	}
	return bound
}

func isLoadObjectConstructor(call *ast.CallExpr) bool {
	if call.ConstructorType == nil {
		return false
	}
	_, named := call.Func.(*ast.Ident)
	if member, ok := call.Func.(*ast.MemberExpr); ok {
		return member.SealedConstructor
	}
	return named
}

func (a *loadDemandAnalysis) constructedObject(call *ast.CallExpr) []loadOrigin {
	payload := a.objectFields(call.Args)
	if member, ok := call.Func.(*ast.MemberExpr); ok && member.SealedConstructor {
		object := newLoadObject(nil)
		object.set(member.Field, payload)
		return []loadOrigin{loadObjectOrigin(object)}
	}
	return payload
}

func (a *loadDemandAnalysis) lookupObject(expression *ast.Ident) []loadOrigin {
	if origins, exists := a.lookup(expression.Name); exists {
		return origins
	}
	if !a.objectStorage || expression.IsListType() || !inferred.StructuredType(expression.GetTypeTag()) {
		return nil
	}
	origins := []loadOrigin{loadObjectOrigin(newLoadObject(nil))}
	a.scopes[0][expression.Name] = origins
	return origins
}

func (a *loadDemandAnalysis) objectField(object *loadObject, member *ast.MemberExpr) []loadOrigin {
	if origins, exists := object.fields[member.Field]; exists {
		return origins
	}
	initial := object.initialFields()
	if origins, exists := initial.fields[member.Field]; exists {
		return origins
	}
	var origins []loadOrigin
	if len(object.base) != 0 {
		// Dispatch the original typed model path without re-evaluating its AST.
		origins = a.memberOrigins(object.base, member)
	}
	if !member.IsListType() && inferred.StructuredType(member.GetTypeTag()) {
		origins = []loadOrigin{loadObjectOrigin(newLoadObject(origins))}
	}
	initial.set(member.Field, origins)
	return origins
}

func (a *loadDemandAnalysis) assignObject(base []loadOrigin, member *ast.MemberExpr, value []loadOrigin, statement *ast.AssignStmt) bool {
	if len(base) == 0 || slices.ContainsFunc(base, func(origin loadOrigin) bool { return origin.demand.object == nil }) {
		return false
	}
	expected := &ast.TypeRef{Name: member.GetTypeTag(), IsList: member.IsListType(), Nullable: member.IsNullable()}
	for _, origin := range base {
		bound := a.bindObject(value, statement.Value, expected)
		if a.controlDepth != 0 || statement.Op != "=" {
			bound = append(slices.Clone(a.objectField(origin.demand.object, member)), bound...)
		}
		origin.demand.object.set(member.Field, bound)
	}
	return true
}

func (a *loadDemandAnalysis) assignObjectBinding(scope map[string][]loadOrigin, target *ast.Ident, previous, value []loadOrigin, statement *ast.AssignStmt) bool {
	if !slices.ContainsFunc(previous, func(origin loadOrigin) bool { return origin.demand.object != nil }) && !slices.ContainsFunc(value, func(origin loadOrigin) bool { return origin.demand.object != nil }) {
		return false
	}
	expected := &ast.TypeRef{Name: target.GetTypeTag(), IsList: target.IsListType(), Nullable: target.IsNullable()}
	bound := a.bindObject(value, statement.Value, expected)
	if a.controlDepth != 0 {
		bound = append(slices.Clone(previous), bound...)
	}
	scope[target.Name] = bound
	return true
}

func (a *loadDemandAnalysis) conditionalBlock(body *ast.Block, bindings map[string][]loadOrigin, lastValue, publicReturn bool) []loadOrigin {
	a.controlDepth++
	defer func() { a.controlDepth-- }()
	return a.block(body, bindings, lastValue, publicReturn)
}
