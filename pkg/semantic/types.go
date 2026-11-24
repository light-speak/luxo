package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// TypeRef returns an independent declaration-shaped copy of this resolved type.
func (t *ResolvedType) TypeRef() *ast.TypeRef {
	if t == nil {
		return nil
	}
	ref := &ast.TypeRef{Name: t.Name, Nullable: t.Nullable, IsList: t.IsList}
	for _, argument := range t.TypeArgs {
		ref.TypeArgs = append(ref.TypeArgs, argument.TypeRef())
	}
	for _, item := range t.Tuple {
		ref.Tuple = append(ref.Tuple, item.TypeRef())
	}
	return ref
}

// TypeKind represents the category of a resolved type.
type TypeKind int

const (
	TypeUnknown TypeKind = iota
	TypeInt
	TypeFloat
	TypeString
	TypeBool
	TypeDateTime
	TypeDuration
	TypeUUID
	TypeDecimal
	TypeBytes
	TypeJSON
	TypeVoid // internal only, not user-facing
	TypeModel
	TypeInterface
	TypeEnum
	TypeSealed
	TypeCustom       // user-defined type
	TypeGeneric      // Page<T>, Result<T>
	TypeTuple        // (Post, Video, Product) polymorphic
	TypeQueryBuilder // Model.where(...).sum(...) chain query
)

// String returns the display name for a TypeKind.
func (k TypeKind) String() string {
	switch k {
	case TypeModel:
		return "model"
	case TypeInterface:
		return "interface"
	case TypeEnum:
		return "enum"
	case TypeSealed:
		return "sealed"
	case TypeCustom:
		return "type"
	case TypeGeneric:
		return "generic"
	case TypeTuple:
		return "tuple"
	case TypeQueryBuilder:
		return "query builder"
	default:
		return "type"
	}
}

// ResolvedType represents a fully resolved type after semantic analysis.
type ResolvedType struct {
	Kind     TypeKind
	Name     string
	Nullable bool
	IsList   bool

	// Fields for model/interface/type/sealed
	Fields map[string]*FieldInfo

	// Enum values
	EnumValues []string

	// Sealed variants
	Variants []*SealedVariantInfo

	// Generic type arguments: Page<User> → TypeArgs = [User]
	TypeArgs []*ResolvedType

	// Tuple types: (Post, Video, Product)
	Tuple []*ResolvedType

	// ModelType references the underlying model for query builder types
	ModelType *ResolvedType

	// Parents (inheritance)
	Parents []*ResolvedType

	// Position of declaration
	Pos token.Position
}

// FieldInfo represents a resolved field.
type FieldInfo struct {
	Name       string
	Type       *ResolvedType
	Nullable   bool
	HasDefault bool
	Computed   bool
	IsMethod   bool     // true if this field is a method requiring ()
	Directives []string // directive names for quick lookup
	Pos        token.Position
	Doc        string
}

// SealedVariantInfo represents a variant of a sealed type.
type SealedVariantInfo struct {
	Name   string
	Fields []*FieldInfo
	source *ast.SealedVariant // Resolve payloads after all type names are registered.
}

const (
	builtinIntName      = "Int"
	builtinFloatName    = "Float"
	builtinStringName   = "String"
	builtinBooleanName  = "Boolean"
	builtinDateTimeName = "DateTime"
	builtinDurationName = "Duration"
	builtinUUIDName     = "UUID"
	builtinDecimalName  = "Decimal"
	builtinBytesName    = "Bytes"
	builtinJSONName     = "JSON"
	builtinResultName   = "Result"
	builtinChannelName  = "Channel"
	builtinPageName     = "Page"
	builtinCursorName   = "Cursor"
	builtinIdentityName = "Identity"
)

// IsBuiltinTypeName reports whether name is reserved by the built-in type catalog.
func IsBuiltinTypeName(name string) bool {
	return isBuiltinTypeName(name)
}

// IsBuiltinTypeNameBytes checks the same catalog using an existing byte buffer.
func IsBuiltinTypeNameBytes(name []byte) bool {
	return isBuiltinTypeName(name)
}

func isBuiltinTypeName[Name ~string | ~[]byte](name Name) bool {
	switch string(name) {
	case builtinIntName, builtinFloatName, builtinStringName, builtinBooleanName,
		builtinDateTimeName, builtinDurationName, builtinUUIDName, builtinDecimalName,
		builtinBytesName, builtinJSONName, builtinResultName, builtinChannelName,
		builtinPageName, builtinCursorName, builtinIdentityName:
		return true
	default:
		return false
	}
}

// BuiltinTypes returns the built-in primitive types with their properties.
func BuiltinTypes() map[string]*ResolvedType {
	intType := &ResolvedType{Kind: TypeInt, Name: builtinIntName, Fields: map[string]*FieldInfo{}}
	floatType := &ResolvedType{Kind: TypeFloat, Name: builtinFloatName, Fields: map[string]*FieldInfo{}}
	boolType := &ResolvedType{Kind: TypeBool, Name: builtinBooleanName, Fields: map[string]*FieldInfo{}}

	stringType := newStringType(intType, floatType, boolType)

	identityType := newIdentityType()

	return map[string]*ResolvedType{
		builtinIntName:      intType,
		builtinFloatName:    floatType,
		builtinStringName:   stringType,
		builtinBooleanName:  boolType,
		builtinDateTimeName: {Kind: TypeDateTime, Name: builtinDateTimeName, Fields: map[string]*FieldInfo{}},
		builtinDurationName: {Kind: TypeDuration, Name: builtinDurationName, Fields: map[string]*FieldInfo{}},
		builtinUUIDName:     {Kind: TypeUUID, Name: builtinUUIDName, Fields: map[string]*FieldInfo{}},
		builtinDecimalName:  {Kind: TypeDecimal, Name: builtinDecimalName, Fields: map[string]*FieldInfo{}},
		builtinBytesName:    {Kind: TypeBytes, Name: builtinBytesName, Fields: map[string]*FieldInfo{}},
		builtinJSONName:     {Kind: TypeJSON, Name: builtinJSONName, Fields: map[string]*FieldInfo{}},
		builtinResultName:   {Kind: TypeGeneric, Name: builtinResultName, Fields: map[string]*FieldInfo{}},
		builtinChannelName:  {Kind: TypeGeneric, Name: builtinChannelName, Fields: map[string]*FieldInfo{}},
		builtinPageName: {Kind: TypeGeneric, Name: builtinPageName, Fields: map[string]*FieldInfo{
			"items":    {Name: "items", Type: nil},
			"total":    {Name: "total", Type: intType},
			"page":     {Name: "page", Type: intType},
			"pageSize": {Name: "pageSize", Type: intType},
		}},
		builtinCursorName: {Kind: TypeGeneric, Name: builtinCursorName, Fields: map[string]*FieldInfo{
			"items":      {Name: "items", Type: nil},
			"nextCursor": {Name: "nextCursor", Type: stringType, Nullable: true},
			"hasMore":    {Name: "hasMore", Type: boolType},
		}},
		builtinIdentityName: identityType,
	}
}

func newStringType(intType, floatType, boolType *ResolvedType) *ResolvedType {
	stringType := &ResolvedType{Kind: TypeString, Name: builtinStringName}
	// Method records belong to this compilation and remain independently mutable.
	fields := [...]FieldInfo{
		// properties
		{Name: "length", Type: intType},
		{Name: "isEmpty", Type: boolType},
		{Name: "size", Type: intType},
		// transform — return String
		{Name: "lowercase", Type: stringType, IsMethod: true},
		{Name: "uppercase", Type: stringType, IsMethod: true},
		{Name: "trim", Type: stringType, IsMethod: true},
		{Name: "trimStart", Type: stringType, IsMethod: true},
		{Name: "trimEnd", Type: stringType, IsMethod: true},
		{Name: "reversed", Type: stringType, IsMethod: true},
		// Method counts and parameter types are checked by the String call validator.
		{Name: "contains", Type: boolType, IsMethod: true},
		{Name: "startsWith", Type: boolType, IsMethod: true},
		{Name: "endsWith", Type: boolType, IsMethod: true},
		{Name: "matches", Type: boolType, IsMethod: true},
		{Name: "replace", Type: stringType, IsMethod: true},
		{Name: "replaceAll", Type: stringType, IsMethod: true},
		{Name: "split", Type: &ResolvedType{Kind: TypeString, Name: "String", IsList: true}, IsMethod: true},
		{Name: "substring", Type: stringType, IsMethod: true},
		{Name: "repeat", Type: stringType, IsMethod: true},
		{Name: "padStart", Type: stringType, IsMethod: true},
		{Name: "padEnd", Type: stringType, IsMethod: true},
		{Name: "toInt", Type: &ResolvedType{Kind: TypeGeneric, Name: "Result", TypeArgs: []*ResolvedType{intType}}, IsMethod: true},
		{Name: "toFloat", Type: &ResolvedType{Kind: TypeGeneric, Name: "Result", TypeArgs: []*ResolvedType{floatType}}, IsMethod: true},
	}
	stringType.Fields = make(map[string]*FieldInfo, len(fields))
	for index := range fields {
		field := &fields[index]
		stringType.Fields[field.Name] = field
	}
	return stringType
}

// IsNonNullBoolean excludes nullable values and Boolean collections.
func (t *ResolvedType) IsNonNullBoolean() bool {
	return t != nil && t.Kind == TypeBool && !t.IsList && !t.Nullable
}

// IsNumeric returns true if the type is numeric.
func (t *ResolvedType) IsNumeric() bool {
	return t.Kind == TypeInt || t.Kind == TypeFloat || t.Kind == TypeDecimal
}

// IsComparable returns true if the type supports == and !=.
func (t *ResolvedType) IsComparable() bool {
	switch t.Kind {
	case TypeInt, TypeFloat, TypeString, TypeBool, TypeEnum, TypeUUID, TypeDecimal, TypeDateTime:
		return true
	}
	return false
}

// IsOrderable returns true if the type supports > < >= <=.
func (t *ResolvedType) IsOrderable() bool {
	return t.IsNumeric() || t.Kind == TypeString || t.Kind == TypeDateTime
}

// AsNullable returns a nullable version of this type.
func (t *ResolvedType) AsNullable() *ResolvedType {
	if t.Nullable {
		return t
	}
	copy := *t
	copy.Nullable = true
	return &copy
}

// AsList returns a list version of this type.
func (t *ResolvedType) AsList() *ResolvedType {
	return &ResolvedType{
		Kind:    t.Kind,
		Name:    t.Name,
		IsList:  true,
		Fields:  t.Fields,
		Parents: t.Parents,
	}
}

// AsNonNull returns a non-nullable version of this type.
func (t *ResolvedType) AsNonNull() *ResolvedType {
	if !t.Nullable {
		return t
	}
	copy := *t
	copy.Nullable = false
	return &copy
}

// LookupField returns the field info for a given field name,
// including inherited fields from parents.
func (t *ResolvedType) LookupField(name string) *FieldInfo {
	if f, ok := t.Fields[name]; ok {
		return f
	}
	if len(t.Parents) == 0 {
		return nil
	}
	return t.lookupInheritedField(name, make(map[*ResolvedType]bool))
}

func (t *ResolvedType) lookupInheritedField(name string, visited map[*ResolvedType]bool) *FieldInfo {
	if t == nil || visited[t] {
		return nil
	}
	visited[t] = true
	if field := t.Fields[name]; field != nil {
		return field
	}
	for _, parent := range t.Parents {
		if field := parent.lookupInheritedField(name, visited); field != nil {
			return field
		}
	}
	return nil
}

func (t *ResolvedType) inheritsFrom(name string) bool {
	visited := make(map[*ResolvedType]bool)
	pending := append([]*ResolvedType(nil), t.Parents...)
	for len(pending) > 0 {
		last := len(pending) - 1
		parent := pending[last]
		pending = pending[:last]
		if parent == nil || visited[parent] {
			continue
		}
		if parent.Name == name {
			return true
		}
		visited[parent] = true
		pending = append(pending, parent.Parents...)
	}
	return false
}

func newIdentityType() *ResolvedType {
	identityType := &ResolvedType{Kind: TypeModel, Name: builtinIdentityName, Fields: map[string]*FieldInfo{
		"id":   {Name: "id", Type: nil},                                                                                        // Resolved from stores, otherwise defaults to Int before body analysis.
		"load": {Name: "load", Type: nil, IsMethod: true, Doc: ".load(fields...): Identity — Lazy load user fields / 懒加载用户字段"}, // my.load(fields...) — lazy load
		"role": {Name: "role", Type: nil},                                                                                      // role from JWT stores, type resolved at codegen
	}}
	// self-reference: load() returns Identity
	identityType.Fields["load"].Type = identityType

	return identityType
}
