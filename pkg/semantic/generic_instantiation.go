package semantic

import (
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func (a *Analyzer) registerGenericInstance(typ *ResolvedType, ref *ast.TypeRef, pos token.Position) {
	if len(typ.TypeArgs) == 0 || (typ.Name != "Page" && typ.Name != "Cursor" && a.genericTemplate(typ.Name) == nil) {
		return
	}
	typ.Pos = pos
	if !a.validateGenericValueArguments(typ.TypeArgs, pos) {
		return
	}
	state := a.ensureValueTypes()
	if state.references == nil {
		state.references = make(map[*ast.TypeRef]*ResolvedType)
	}
	state.references[ref] = typ
	if state.ready {
		typ.Fields = a.instantiateGenericFields(typ, 0)
		return
	}
	state.instances = append(state.instances, typ)
}

func (a *Analyzer) validateGenericValueArguments(arguments []*ResolvedType, pos token.Position) bool {
	valid := true
	for _, argument := range arguments {
		if argument.Kind == TypeInterface || argument.Kind == TypeTuple || argument.Kind == TypeVoid ||
			argument.Kind == TypeGeneric && (argument.Name == "Channel" || argument.Name == "Result") && len(argument.TypeArgs) != 0 {
			a.addError(pos, "value generic arguments require a concrete data representation / 值类型泛型实参必须具有具体数据表示")
			valid = false
		}
		if !a.validateGenericValueArguments(argument.TypeArgs, pos) {
			valid = false
		}
	}
	return valid
}

// All template declarations must be resolved before substituting forward references.
func (a *Analyzer) resolveGenericInstanceFields() {
	a.typePassDone = true
	if a.valueTypes == nil {
		return
	}
	a.valueTypes.ready = true
	for _, typ := range a.valueTypes.instances {
		typ.Fields = a.instantiateGenericFields(typ, 0)
	}
	a.valueTypes.instances = nil
}

func (a *Analyzer) instantiateGenericFields(typ *ResolvedType, depth int) map[string]*FieldInfo {
	base := a.types[typ.Name]
	parameters := []string{"T"}
	if declaration := a.genericTemplate(typ.Name); declaration != nil {
		parameters = declaration.TypeParams
	}
	if len(parameters) != len(typ.TypeArgs) {
		return base.Fields
	}
	key := genericInstanceKey(typ)
	if fields, ok := a.valueTypes.fields[key]; ok {
		return fields
	}
	if depth >= 64 {
		a.addError(typ.Pos, "generic instantiation expands beyond 64 object levels / 泛型实例化超过 64 层对象")
		return nil
	}
	if a.valueTypes.fields == nil {
		a.valueTypes.fields = make(map[string]map[string]*FieldInfo)
	}
	fields := make(map[string]*FieldInfo, len(base.Fields))
	a.valueTypes.fields[key] = fields
	arguments := make(map[string]*ResolvedType, len(parameters))
	for index, name := range parameters {
		arguments[name] = typ.TypeArgs[index]
	}
	for name, field := range base.Fields {
		copy := *field
		if name == "items" && (typ.Name == "Page" || typ.Name == "Cursor") {
			copy.Type = a.applyGenericQualifiers(typ.TypeArgs[0], false, true, typ.Pos)
		} else {
			copy.Type = a.substituteGenericType(field.Type, arguments, depth+1)
		}
		copy.Nullable = field.Nullable || copy.Type.Nullable
		if copy.Nullable && !copy.Type.Nullable {
			resolved := *copy.Type
			resolved.Nullable = true
			copy.Type = &resolved
		}
		fields[name] = &copy
	}
	return fields
}

func (a *Analyzer) substituteGenericType(typ *ResolvedType, arguments map[string]*ResolvedType, depth int) *ResolvedType {
	if argument := arguments[typ.Name]; argument != nil && typ.Kind == TypeGeneric && len(typ.TypeArgs) == 0 {
		return a.applyGenericQualifiers(argument, typ.Nullable, typ.IsList, typ.Pos)
	}
	if len(typ.TypeArgs) == 0 && len(typ.Tuple) == 0 {
		return typ
	}
	copy := *typ
	copy.TypeArgs = a.substituteGenericArguments(typ.TypeArgs, arguments, depth)
	copy.Tuple = a.substituteGenericArguments(typ.Tuple, arguments, depth)
	if len(copy.TypeArgs) != 0 && (a.genericTemplate(copy.Name) != nil || copy.Name == "Page" || copy.Name == "Cursor") {
		copy.Fields = a.instantiateGenericFields(&copy, depth)
	}
	return &copy
}

func (a *Analyzer) substituteGenericArguments(types []*ResolvedType, arguments map[string]*ResolvedType, depth int) []*ResolvedType {
	if len(types) == 0 {
		return nil
	}
	result := make([]*ResolvedType, len(types))
	for index, typ := range types {
		result[index] = a.substituteGenericType(typ, arguments, depth)
	}
	return result
}

func (a *Analyzer) applyGenericQualifiers(typ *ResolvedType, nullable, list bool, pos token.Position) *ResolvedType {
	copy := *typ
	if list && (typ.IsList || typ.Nullable || len(typ.Tuple) != 0) {
		a.addError(pos, "list elements must be non-null, non-list named types after generic substitution / 泛型替换后的列表元素必须是非空、非列表的具名类型")
	}
	copy.Nullable = copy.Nullable || nullable
	copy.IsList = copy.IsList || list
	return &copy
}

func genericInstanceKey(typ *ResolvedType) string {
	var result strings.Builder
	writeGenericTypeKey(&result, typ, false)
	return result.String()
}

func writeGenericTypeKey(result *strings.Builder, typ *ResolvedType, qualifiers bool) {
	if qualifiers && typ.IsList {
		result.WriteByte('[')
	}
	result.WriteString(typ.Name)
	writeGenericTypeArguments(result, typ.TypeArgs, '<', '>')
	writeGenericTypeArguments(result, typ.Tuple, '(', ')')
	if qualifiers && typ.IsList {
		result.WriteByte(']')
	}
	if qualifiers && typ.Nullable {
		result.WriteByte('?')
	}
}

func writeGenericTypeArguments(result *strings.Builder, types []*ResolvedType, start, end byte) {
	if len(types) == 0 {
		return
	}
	result.WriteByte(start)
	for index, typ := range types {
		if index != 0 {
			result.WriteByte(',')
		}
		writeGenericTypeKey(result, typ, true)
	}
	result.WriteByte(end)
}
