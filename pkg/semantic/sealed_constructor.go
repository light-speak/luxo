package semantic

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func (a *Analyzer) checkSealedPayloadParameters(fields []*ast.ParamDecl) {
	for _, field := range fields {
		if field.Spread {
			a.addError(field.Pos, "sealed payload field '%s' cannot be variadic; use an explicit list type such as [Int] / 密封载荷字段 '%s' 不能是不定参数，请使用 [Int] 等显式列表类型", field.Name, field.Name)
		}
	}
}

// Bind only independent compilation nodes; editor arguments keep source spelling.
func normalizeSealedCompilationArguments(source *Result, nodes, root *ast.CloneMap) error {
	if source.valueTypes == nil || !source.valueTypes.sealed {
		return nil
	}
	for expression, target := range nodes.Exprs {
		call, ok := expression.(*ast.CallExpr)
		if !ok {
			continue
		}
		member, ok := call.Func.(*ast.MemberExpr)
		if !ok || !member.SealedConstructor {
			continue
		}
		var fields []*ast.ParamDecl
		found := false
		if call.ConstructorType != nil {
			if owner := source.Types[call.ConstructorType.Name]; owner != nil {
				for _, variant := range owner.Variants {
					if variant.Name == member.Field && variant.source != nil {
						fields = variant.source.Fields
						found = true
						break
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("sealed constructor %q has no matching payload declaration", member.Field)
		}
		if err := normalizeSealedCallArguments(target.(*ast.CallExpr), fields, nodes, root); err != nil {
			return err
		}
	}
	return nil
}

// Marked constructors have complete parsed arguments; full-first readonly compilation retains
// canonical payload TypeRefs in the root clone map before template lowering.
// Keep both missing-value and missing-clone errors for incomplete metadata.
func normalizeSealedCallArguments(call *ast.CallExpr, fields []*ast.ParamDecl, nodes, root *ast.CloneMap) error {
	var localAssigned [16]bool
	assigned := localAssigned[:]
	if len(fields) > len(assigned) {
		assigned = make([]bool, len(fields))
	}
	assigned = assigned[:len(fields)]
	namedSeen := false
	for index, argument := range call.Args {
		if argument == nil || argument.Value == nil {
			return fmt.Errorf("sealed constructor argument requires a value")
		}
		position := index
		if argument.Name != "" {
			namedSeen, position = true, -1
			for index, field := range fields {
				if field.Name == argument.Name {
					position = index
					break
				}
			}
		} else if namedSeen {
			return fmt.Errorf("sealed constructor positional argument follows a named argument")
		}
		if position < 0 || position >= len(fields) || assigned[position] {
			return fmt.Errorf("sealed constructor argument %q has no unique payload field", argument.Name)
		}
		field := fields[position]
		cloned := nodes.TypeRefs[field.Type]
		if cloned == nil {
			// Template clones reuse the root compilation's canonical payload types.
			cloned = root.TypeRefs[field.Type]
			if cloned == nil {
				return fmt.Errorf("sealed constructor payload field %q has no cloned type", field.Name)
			}
		}
		argument.Name, argument.ExpectedType = field.Name, cloned
		assigned[position] = true
	}
	return nil
}

// Call-target checking admits only identifiers or members; identifiers return before this assertion.
// Retain the non-member fallback for inconsistent call metadata.
func (a *Analyzer) checkSealedConstructor(call *ast.CallExpr, scope *Scope) (*ResolvedType, bool) {
	if a.valueTypes == nil || !a.valueTypes.sealed {
		return nil, false
	}
	if ident, ok := call.Func.(*ast.Ident); ok {
		if symbol := scope.Lookup(ident.Name); symbol != nil && symbol.Kind == SymSealed {
			a.rejectSealedConstructor(call, ident.Name, call.Args, scope)
			return nil, true
		}
		return nil, false
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		return nil, false
	}
	member.SealedConstructor = false
	ident, ok := member.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	symbol := scope.Lookup(ident.Name)
	if symbol == nil || symbol.Kind != SymSealed {
		return nil, false
	}
	return a.checkSealedVariantConstructor(call, member, symbol.Type, scope), true
}

func (a *Analyzer) rejectSealedConstructor(expression ast.Expr, name string, arguments []*ast.NamedArg, scope *Scope) {
	a.reportSealedValue(expression.GetPos(), name)
	a.checkObjectExprValues(arguments, scope)
}

func (a *Analyzer) reportSealedValue(position token.Position, name string) {
	a.addError(position, "sealed type '%s' requires a variant constructor / 密封类型 '%s' 必须通过变体构造", name, name)
}

func (a *Analyzer) checkSealedVariantConstructor(call *ast.CallExpr, member *ast.MemberExpr, owner *ResolvedType, scope *Scope) *ResolvedType {
	before := len(a.errors)
	if a.moduleMap != nil && a.currentFile != nil {
		if module := a.moduleMap[a.currentFile.Name]; module != nil {
			a.checkNameVisibility(owner.Name, module, call.Pos)
		}
	}
	var variant *SealedVariantInfo
	for _, candidate := range owner.Variants {
		if candidate.Name == member.Field {
			variant = candidate
			break
		}
	}
	if variant == nil || member.SafeCall {
		a.addError(member.Pos, "unknown sealed variant '%s.%s' / 未知密封变体 '%s.%s'", owner.Name, member.Field, owner.Name, member.Field)
		a.checkObjectExprValues(call.Args, scope)
		return nil
	}
	arguments := make([]*ResolvedType, len(call.Args))
	complete := true
	for index, argument := range call.Args {
		if argument == nil || argument.Value == nil {
			a.addError(call.Pos, "constructor argument requires a value / 构造参数需要值")
			complete = false
			continue
		}
		arguments[index] = a.checkExpr(argument.Value, scope)
	}
	if !complete {
		return nil
	}
	a.checkFunctionArguments(call, &ast.FnDecl{Name: owner.Name + "." + variant.Name, Params: variant.source.Fields}, arguments)
	if len(a.errors) == before {
		member.SealedConstructor = true
		call.ConstructorType = owner.TypeRef()
		call.ConstructorType.Pos = call.Pos
		a.setExprResolvedType(member.Object, owner)
		a.setExprResolvedType(member, owner)
	}
	return owner.AsNonNull()
}

func (a *Analyzer) checkSealedWhenExhaustive(expression *ast.WhenExpr, subject *ResolvedType) {
	matched := make(map[string]bool, len(subject.Variants))
	matchedNull := false
	for _, branch := range expression.Branches {
		if literal, ok := branch.Condition.(*ast.Literal); ok && literal.Kind == token.Null {
			matchedNull = true
		}
		if branch.IsType == "" {
			continue
		}
		if subject.IsList {
			a.addError(expression.Pos, "sealed variant patterns require a scalar subject / 密封变体分支要求非列表主体")
			continue
		}
		if !hasSealedVariant(subject, branch.IsType) {
			a.addError(expression.Pos, "unknown sealed variant '%s.%s' / 未知密封变体 '%s.%s'", subject.Name, branch.IsType, subject.Name, branch.IsType)
		}
		matched[branch.IsType] = true
	}
	if expression.Else != nil {
		return
	}
	if subject.IsList {
		a.addError(expression.Pos, "when expression must have 'else' branch / when 表达式必须有 'else' 分支")
		return
	}
	for _, variant := range subject.Variants {
		if !matched[variant.Name] {
			a.addError(expression.Pos, "when on sealed type '%s' is not exhaustive, missing variant '%s' / when 未穷举密封类型 '%s'，缺少变体 '%s'", subject.Name, variant.Name, subject.Name, variant.Name)
		}
	}
	if subject.Nullable && !matchedNull {
		a.addError(expression.Pos, "when on nullable sealed type '%s' is not exhaustive, missing null / when 未穷举可空密封类型 '%s'，缺少 null", subject.Name, subject.Name)
	}
}

func hasSealedVariant(subject *ResolvedType, name string) bool {
	for _, variant := range subject.Variants {
		if variant.Name == name {
			return true
		}
	}
	return false
}

func (a *Analyzer) checkEmptySealedWhen(expression *ast.WhenExpr, subject *ResolvedType, required bool) {
	if required && subject != nil && subject.Kind == TypeSealed && !subject.IsList &&
		len(subject.Variants) == 0 && len(expression.Branches) == 0 && expression.Else == nil {
		a.addError(expression.Pos, "cannot infer the result type of an empty sealed when / 无法推断空密封 when 的结果类型")
	}
}
