package codegen

import (
	"slices"
	"strings"

	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
)

func (a *loadDemandAnalysis) expression(expr ast.Expr) []loadOrigin {
	origins := a.expressionValue(expr)
	if a.objectStorage && len(origins) != 0 && origins[0].demand.object == nil && !expr.IsListType() && inferred.StructuredType(expr.GetTypeTag()) && !strings.HasSuffix(expr.GetTypeTag(), "QueryBuilder") {
		return []loadOrigin{loadObjectOrigin(newLoadObject(origins))}
	}
	return origins
}

func (a *loadDemandAnalysis) expressionValue(expr ast.Expr) []loadOrigin {
	switch value := expr.(type) {
	case *ast.Ident:
		return a.lookupObject(value)
	case *ast.MemberExpr:
		return a.member(value)
	case *ast.CallExpr:
		return a.call(value)
	case *ast.BinaryExpr:
		a.binary(value)
	case *ast.UnaryExpr:
		origins := a.expression(value.Value)
		if value.Op == "?" {
			return origins
		}
		a.consume(origins)
	case *ast.ElvisExpr:
		return append(slices.Clone(a.expression(value.Left)), a.expression(value.Right)...)
	case *ast.BangElvisExpr:
		return append(slices.Clone(a.expression(value.Left)), a.expression(value.Right)...)
	case *ast.LambdaExpr:
		return a.conditionalBlock(value.Body, lambdaOriginBindings(value, nil), true, false)
	case *ast.ListExpr:
		var origins []loadOrigin
		for _, item := range value.Items {
			origins = append(origins, a.expression(item)...)
		}
		return origins
	case *ast.ObjectExpr:
		return a.object(value)
	case *ast.WhenExpr:
		return a.when(value)
	case *ast.TemplateString:
		for _, part := range value.Parts {
			a.consume(a.expression(part))
		}
	case *ast.RangeExpr:
		a.consume(a.expression(value.Start))
		a.consume(a.expression(value.End))
	case *ast.TransactionExpr:
		return a.block(value.Body, nil, true, false)
	case *ast.AsyncExpr:
		a.consume(a.conditionalBlock(value.Body, nil, true, false))
	case *ast.AwaitExpr:
		return a.conditionalBlock(value.Body, nil, true, false)
	case *ast.YieldExpr:
		origins := a.expression(value.Value)
		a.loopYields = append(a.loopYields, origins...)
		return origins
	case *ast.ForStmt:
		return a.loopValue(value)
	}
	return nil
}

func (a *loadDemandAnalysis) binary(value *ast.BinaryExpr) {
	if value.Op == "==" || value.Op == "!=" {
		if isNullLiteral(value.Left) || isNullLiteral(value.Right) {
			a.presence(a.expression(value.Left))
			a.presence(a.expression(value.Right))
		} else {
			seen := a.consumeEquality(a.expression(value.Left), nil)
			a.consumeEquality(a.expression(value.Right), seen)
		}
		return
	}
	a.consume(a.expression(value.Left))
	a.consume(a.expression(value.Right))
}

// Model equality reads identity. Scalar constructor arguments already record
// their value reads; opaque values retain their complete declared contract.
func (a *loadDemandAnalysis) consumeEquality(origins []loadOrigin, seen map[*loadObject]bool) map[*loadObject]bool {
	for _, origin := range origins {
		if object := origin.demand.object; object != nil {
			if seen == nil {
				seen = make(map[*loadObject]bool)
			}
			if seen[object] {
				continue
			}
			seen[object] = true
			seen = a.consumeEquality(object.base, seen)
			object.eachField(func(fields []loadOrigin) { seen = a.consumeEquality(fields, seen) })
		} else if model := a.originModel(origin); model != nil {
			if len(origin.path) > 0 {
				a.add(origin, []string{primaryKeyFieldName(model)})
			}
		} else {
			a.consume([]loadOrigin{origin})
		}
	}
	return seen
}

func (a *loadDemandAnalysis) call(call *ast.CallExpr) []loadOrigin {
	if isLoadObjectConstructor(call) {
		return a.constructedObject(call)
	}
	if a.selectedQueries || a.collectionQueries {
		if origins, handled := a.selectedQueryCall(call); handled {
			return origins
		}
	}
	for _, argument := range call.Args {
		a.consume(a.expression(argument.Value))
	}
	if model, argument := a.callSource(call); argument != nil {
		if member, ok := call.Func.(*ast.MemberExpr); ok {
			a.expression(member.Object)
		}
		if a.demands[argument] == nil {
			a.demands[argument] = &loadDemand{model: model}
		}
		return []loadOrigin{{demand: a.demands[argument]}}
	}
	if member, ok := call.Func.(*ast.MemberExpr); ok {
		a.consume(a.expression(member.Object))
	} else {
		a.consume(a.expression(call.Func))
	}
	return nil
}

func (a *loadDemandAnalysis) selectedQueryCall(call *ast.CallExpr) ([]loadOrigin, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		return nil, false
	}
	if member.Object.IsListType() && builtin.CollectionMethod(member.Field) == builtin.CollectionLambda && len(call.Args) == 1 {
		callback, ok := call.Args[0].Value.(*ast.LambdaExpr)
		if !ok {
			return nil, false
		}
		origins := a.expression(member.Object)
		result := a.conditionalBlock(callback.Body, lambdaOriginBindings(callback, origins), true, false)
		switch member.Field {
		case "map":
			return result, true
		case "filter", "sortBy":
			a.consume(result)
			return origins, true
		default:
			a.consume(result)
			return nil, true
		}
	}
	if !a.selectedQueries || !strings.HasSuffix(member.Object.GetTypeTag(), "QueryBuilder") {
		return nil, false
	}
	if member.Field == "select" {
		return nil, false
	}
	for _, argument := range call.Args {
		a.consume(a.expression(argument.Value))
	}
	origins := a.expression(member.Object)
	if strings.HasSuffix(call.GetTypeTag(), "QueryBuilder") || a.models[call.GetTypeTag()] != nil {
		return origins, true
	}
	return nil, true
}

// Explicit parameters preserve an outer it; only an implicit parameter or an
// explicit parameter named it shadows that binding.
func lambdaOriginBindings(callback *ast.LambdaExpr, origins []loadOrigin) map[string][]loadOrigin {
	if len(callback.Params) == 0 {
		return map[string][]loadOrigin{"it": origins}
	}
	bindings := make(map[string][]loadOrigin, len(callback.Params))
	for _, name := range callback.Params {
		bindings[name] = nil
	}
	bindings[callback.Params[0]] = origins
	return bindings
}

func (a *loadDemandAnalysis) callSource(call *ast.CallExpr) (string, *ast.NamedArg) {
	if a.source != nil {
		if model, ok := a.source(call); ok {
			argument := a.origins[call]
			if argument == nil {
				argument = &ast.NamedArg{Value: call}
				a.origins[call] = argument
			}
			return model, argument
		}
		return "", nil
	}
	if model, ok := directLoadModel(call); ok && len(call.Args) > 0 {
		return model, call.Args[0]
	}
	return "", nil
}

func (a *loadDemandAnalysis) member(member *ast.MemberExpr) []loadOrigin {
	origins := a.expression(member.Object)
	if builtin.IsDebugProperty(member) {
		return origins
	}
	return a.memberOrigins(origins, member)
}

func (a *loadDemandAnalysis) memberOrigins(origins []loadOrigin, member *ast.MemberExpr) []loadOrigin {
	result := make([]loadOrigin, 0, len(origins))
	for _, origin := range origins {
		if origin.demand.object != nil {
			result = append(result, a.objectField(origin.demand.object, member)...)
			continue
		}
		path := append(slices.Clone(origin.path), member.Field)
		structured := a.models[origin.demand.model] == nil
		if structured || validModelSelectionPath(origin.demand.model, path, a.models) {
			value := loadOrigin{demand: origin.demand, path: path}
			if structured && !inferred.StructuredType(member.GetTypeTag()) || !structured && a.originModel(value) == nil {
				a.add(value, nil)
			}
			result = append(result, value)
		} else {
			a.consume([]loadOrigin{origin})
		}
	}
	return result
}

func (a *loadDemandAnalysis) when(value *ast.WhenExpr) []loadOrigin {
	a.consume(a.expression(value.Subject))
	var result []loadOrigin
	for _, branch := range value.Branches {
		a.consume(a.expression(branch.Condition))
		result = append(result, a.branch(branch.Body)...)
	}
	return append(result, a.branch(value.Else)...)
}

func (a *loadDemandAnalysis) branch(expr ast.Expr) []loadOrigin {
	if block, ok := expr.(*ast.LambdaExpr); ok {
		return a.conditionalBlock(block.Body, nil, true, false)
	}
	return a.expression(expr)
}
