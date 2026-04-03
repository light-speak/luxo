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
	TypeJson
	TypeFile
	TypeVoid
	TypeModel
	TypeInterface
	TypeEnum
	TypeSealed
	TypeCustom  // user-defined type
	TypeGeneric // Page<T>
	TypeTuple   // (Post, Video, Product) polymorphic
)

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
	Directives []string // directive names for quick lookup
	Pos        token.Position
	Doc        string
}

// SealedVariantInfo represents a variant of a sealed type.
type SealedVariantInfo struct {
	Name   string
	Fields []*FieldInfo
}

// BuiltinTypes returns the built-in primitive types.
func BuiltinTypes() map[string]*ResolvedType {
	return map[string]*ResolvedType{
		"Int":      {Kind: TypeInt, Name: "Int"},
		"Float":    {Kind: TypeFloat, Name: "Float"},
		"String":   {Kind: TypeString, Name: "String"},
		"Boolean":  {Kind: TypeBool, Name: "Boolean"},
		"Bool":     {Kind: TypeBool, Name: "Bool"},
		"DateTime": {Kind: TypeDateTime, Name: "DateTime"},
		"Duration": {Kind: TypeDuration, Name: "Duration"},
		"Json":     {Kind: TypeJson, Name: "Json"},
		"File":     {Kind: TypeFile, Name: "File"},
		"Void":     {Kind: TypeVoid, Name: "Void"},
	}
}

// IsNumeric returns true if the type is numeric.
func (t *ResolvedType) IsNumeric() bool {
	return t.Kind == TypeInt || t.Kind == TypeFloat
}

// IsComparable returns true if the type supports == and !=.
func (t *ResolvedType) IsComparable() bool {
	switch t.Kind {
	case TypeInt, TypeFloat, TypeString, TypeBool, TypeEnum:
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
