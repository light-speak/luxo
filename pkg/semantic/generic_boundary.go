package semantic

import (
	"slices"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

type typeContext uint8

const (
	allowLocalChannel typeContext = 1 << iota
	allowNativeResult
)

func (a *Analyzer) resolveTypeParameters(declaration *ast.TypeDecl) []string {
	if len(declaration.TypeParams) < 2 {
		return declaration.TypeParams
	}
	seen := make(map[string]bool, len(declaration.TypeParams))
	for _, name := range declaration.TypeParams {
		if seen[name] {
			a.addError(declaration.Pos, "duplicate type parameter '%s' in '%s' / 类型 '%s' 中泛型参数 '%s' 重复", name, declaration.Name, declaration.Name, name)
			continue
		}
		seen[name] = true
	}
	return declaration.TypeParams
}

func (a *Analyzer) resolveTypeParameter(ref *ast.TypeRef, pos token.Position) *ResolvedType {
	if !slices.Contains(a.typeParameters, ref.Name) {
		return nil
	}
	if len(ref.TypeArgs) != 0 {
		a.addError(pos, "type parameter '%s' does not accept type arguments / 泛型参数 '%s' 不接受类型实参", ref.Name, ref.Name)
	}
	return &ResolvedType{Kind: TypeGeneric, Name: ref.Name, Nullable: ref.Nullable, IsList: ref.IsList, Pos: pos}
}

func (a *Analyzer) resolveNamedType(ref *ast.TypeRef, pos token.Position, context typeContext) *ResolvedType {
	if ref.Name == "Result" && context&allowNativeResult == 0 {
		a.addError(pos, "Result<T> is only valid as the outer return type of a native fn; it cannot be stored or passed as data / Result<T> 只能作为 native fn 的最外层返回类型，不能作为数据保存或传递")
	}
	typ := a.types[ref.Name]
	if typ == nil {
		a.addErrorWithSuggestion(pos, ref.Name, "unknown type '%s' / 未知类型 '%s'", ref.Name, ref.Name)
		return nil
	}
	if a.isBuiltinIdentityDataType(typ) {
		a.reportIdentityDataType(pos)
		return nil
	}
	if len(ref.TypeArgs) != 0 || ref.Name == "Channel" || ref.Name == "Page" || ref.Name == "Cursor" || typ.Kind == TypeCustom && a.genericTemplate(ref.Name) != nil {
		a.validateGenericTypeRef(ref, typ, pos, context)
	}
	return typ
}

func (a *Analyzer) isBuiltinIdentityDataType(typ *ResolvedType) bool {
	return typ != nil && typ.Name == "Identity" && typ == a.types["Identity"]
}

func (a *Analyzer) reportIdentityDataType(pos token.Position) {
	a.addError(pos, "Identity is a runtime authentication context and has no data representation / Identity 仅用于运行时身份上下文，没有数据表示")
}

func (a *Analyzer) validateGenericTypeRef(ref *ast.TypeRef, typ *ResolvedType, pos token.Position, context typeContext) {
	if declaration := a.genericTemplate(ref.Name); declaration != nil {
		a.checkGenericArgumentCount(ref, len(declaration.TypeParams), pos)
	} else if ref.Name == "Page" || ref.Name == "Cursor" {
		a.checkGenericArgumentCount(ref, 1, pos)
	} else if typ.Kind == TypeCustom && len(ref.TypeArgs) != 0 {
		a.addError(pos, "type '%s' does not accept type arguments / 类型 '%s' 不接受泛型参数", ref.Name, ref.Name)
	}
	if ref.Name == "Channel" {
		if context&allowLocalChannel == 0 {
			a.addError(pos, "Channel<T> is only valid in local functions and variables; it has no wire or storage representation / Channel<T> 只能用于本地函数和变量，没有传输或存储表示")
		}
		if len(ref.TypeArgs) != 1 {
			a.addError(pos, "Channel requires exactly one element type / Channel 必须有且仅有一个元素类型")
		}
	}
	if len(ref.TypeArgs) > 0 && typ.Kind != TypeGeneric && typ.Kind != TypeCustom {
		a.addError(pos, "type '%s' does not accept type arguments / 类型 '%s' 不接受泛型参数", ref.Name, ref.Name)
	}
}

func (a *Analyzer) checkGenericArgumentCount(ref *ast.TypeRef, count int, pos token.Position) {
	if len(ref.TypeArgs) != count {
		a.addError(pos, "type '%s' requires exactly %d type arguments, got %d / 类型 '%s' 需要 %d 个类型实参，实际为 %d", ref.Name, count, len(ref.TypeArgs), ref.Name, count, len(ref.TypeArgs))
	}
}
