package semantic

import "github.com/light-speak/luxo/pkg/token"

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
}

// BuiltinTypes returns the built-in primitive types with their properties.
func BuiltinTypes() map[string]*ResolvedType {
	intType := &ResolvedType{Kind: TypeInt, Name: "Int", Fields: map[string]*FieldInfo{}}
	floatType := &ResolvedType{Kind: TypeFloat, Name: "Float", Fields: map[string]*FieldInfo{}}
	boolType := &ResolvedType{Kind: TypeBool, Name: "Boolean", Fields: map[string]*FieldInfo{}}

	stringType := &ResolvedType{Kind: TypeString, Name: "String", Fields: map[string]*FieldInfo{
		// properties
		"length":  {Name: "length", Type: intType},
		"isEmpty": {Name: "isEmpty", Type: boolType},
		"size":    {Name: "size", Type: intType},
		// transform — return String (self-ref resolved below)
		"lowercase": {Name: "lowercase", Type: nil},
		"uppercase": {Name: "uppercase", Type: nil},
		"trim":      {Name: "trim", Type: nil},
		"trimStart": {Name: "trimStart", Type: nil},
		"trimEnd":   {Name: "trimEnd", Type: nil},
		"reversed":  {Name: "reversed", Type: nil},
		// methods that take args — return type varies but LSP won't error
		"contains":   {Name: "contains", Type: boolType},
		"startsWith": {Name: "startsWith", Type: boolType},
		"endsWith":   {Name: "endsWith", Type: boolType},
		"matches":    {Name: "matches", Type: boolType},
		"replace":    {Name: "replace", Type: nil},
		"replaceAll": {Name: "replaceAll", Type: nil},
		"split":      {Name: "split", Type: nil},
		"substring":  {Name: "substring", Type: nil},
		"repeat":     {Name: "repeat", Type: nil},
		"padStart":   {Name: "padStart", Type: nil},
		"padEnd":     {Name: "padEnd", Type: nil},
		"toInt":      {Name: "toInt", Type: intType},
		"toFloat":    {Name: "toFloat", Type: floatType},
	}}
	// self-reference for string methods that return String
	stringReturnMethods := []string{
		"lowercase", "uppercase", "trim", "trimStart", "trimEnd", "reversed",
		"replace", "replaceAll", "substring", "repeat", "padStart", "padEnd",
	}
	for _, m := range stringReturnMethods {
		stringType.Fields[m].Type = stringType
	}
	// split returns list (approximate — not typed as [String] but won't error)
	stringType.Fields["split"].Type = &ResolvedType{Kind: TypeString, Name: "String", IsList: true}

	identityType := &ResolvedType{Kind: TypeModel, Name: "Identity", Fields: map[string]*FieldInfo{
		"id":   {Name: "id", Type: intType},
		"load": {Name: "load", Type: nil, IsMethod: true, Doc: ".load(fields...): Identity — Lazy load user fields / 懒加载用户字段"}, // my.load(fields...) — lazy load
		"role": {Name: "role", Type: nil},                                                                                      // role from JWT stores, type resolved at codegen
	}}
	// self-reference: load() returns Identity
	identityType.Fields["load"].Type = identityType

	return map[string]*ResolvedType{
		"Int":      intType,
		"Float":    floatType,
		"String":   stringType,
		"Boolean":  boolType,
		"DateTime": {Kind: TypeDateTime, Name: "DateTime", Fields: map[string]*FieldInfo{}},
		"Duration": {Kind: TypeDuration, Name: "Duration", Fields: map[string]*FieldInfo{}},
		"UUID":     {Kind: TypeUUID, Name: "UUID", Fields: map[string]*FieldInfo{}},
		"Decimal":  {Kind: TypeDecimal, Name: "Decimal", Fields: map[string]*FieldInfo{}},
		"Bytes":    {Kind: TypeBytes, Name: "Bytes", Fields: map[string]*FieldInfo{}},
		"Result":   {Kind: TypeGeneric, Name: "Result", Fields: map[string]*FieldInfo{}},
		"Channel":  {Kind: TypeGeneric, Name: "Channel", Fields: map[string]*FieldInfo{}},
		"Page": {Kind: TypeGeneric, Name: "Page", Fields: map[string]*FieldInfo{
			"items":    {Name: "items", Type: nil},
			"total":    {Name: "total", Type: intType},
			"page":     {Name: "page", Type: intType},
			"pageSize": {Name: "pageSize", Type: intType},
		}},
		"Cursor": {Kind: TypeGeneric, Name: "Cursor", Fields: map[string]*FieldInfo{
			"items":      {Name: "items", Type: nil},
			"nextCursor": {Name: "nextCursor", Type: stringType, Nullable: true},
			"hasMore":    {Name: "hasMore", Type: boolType},
		}},
		"Identity": identityType,
	}
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
	for _, parent := range t.Parents {
		if f := parent.LookupField(name); f != nil {
			return f
		}
	}
	return nil
}
