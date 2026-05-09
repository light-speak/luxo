package schema

import (
	"encoding/json"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// Schema holds runtime metadata for Luxo binary ↔ JSON conversion.
// Used by Luvia to convert responses without requiring model structs.
// Loaded from luxo.lock + codegen-emitted schema registration.
type Schema struct {
	Models map[string]*Model    `json:"models"`
	APIs   map[string]*API      `json:"apis"`
	Enums  map[string]*Enum     `json:"enums,omitempty"`
	Types  map[string]*TypeDecl `json:"types,omitempty"`
}

// Enum describes an enum type with its values.
type Enum struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// TypeDecl describes a plain data type (non-DB, like AuthPayload).
type TypeDecl struct {
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}

// Model describes a model's fields for binary ↔ JSON conversion.
type Model struct {
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
	byID   map[int]*Field
	byName map[string]*Field
}

// Field describes a single model field.
type Field struct {
	ID       int       `json:"id"`
	Name     string    `json:"name"`
	Type     FieldType `json:"type"`
	TypeName string    `json:"typeName,omitempty"` // original Luxo type name (User, MemberRole, etc.)
	Nullable bool      `json:"nullable,omitempty"`
	IsList   bool      `json:"isList,omitempty"`
	Relation bool      `json:"relation,omitempty"` // true if this is a relation field (not a DB column)
	// Pre-computed JSON prefix: `"name":` as bytes for zero-alloc writing
	JSONPrefix []byte `json:"-"`
}

// FieldType identifies the wire type for binary encoding/decoding.
type FieldType int

const (
	FieldInt FieldType = iota
	FieldFloat
	FieldString
	FieldBool
	FieldDateTime // int64 unix timestamp
	FieldDuration // int64 nanoseconds
	FieldBytes
	FieldEnum  // string on wire
	FieldModel // nested model (sub-message)
)

// API describes an API's params and return type.
type API struct {
	ID               int     `json:"id"`
	Name             string  `json:"name"`
	Module           string  `json:"module"`
	ReturnType       string  `json:"returnType,omitempty"`
	ReturnList       bool    `json:"returnList,omitempty"`
	Paginated        bool    `json:"paginated,omitempty"`
	Params           []Param `json:"params,omitempty"`
	Deprecated       bool    `json:"deprecated,omitempty"`
	DeprecatedReason string  `json:"deprecatedReason,omitempty"`
}

// Param describes an API parameter.
type Param struct {
	ID   int       `json:"id"`
	Name string    `json:"name"`
	Type FieldType `json:"type"`
}

// fieldTypeNames maps FieldType to its string representation for JSON.
var fieldTypeNames = [...]string{
	FieldInt:      "Int",
	FieldFloat:    "Float",
	FieldString:   "String",
	FieldBool:     "Boolean",
	FieldDateTime: "DateTime",
	FieldDuration: "Duration",
	FieldBytes:    "Bytes",
	FieldEnum:     "Enum",
	FieldModel:    "Model",
}

// String returns the Luxo type name for the field type.
func (t FieldType) String() string {
	if int(t) < len(fieldTypeNames) {
		return fieldTypeNames[t]
	}
	return "Unknown"
}

// MarshalJSON outputs FieldType as a string.
func (t FieldType) MarshalJSON() ([]byte, error) {
	if int(t) < len(fieldTypeNames) {
		return json.Marshal(fieldTypeNames[t])
	}
	return json.Marshal("Unknown")
}

// ToJSON serializes the schema to JSON for introspection.
func (s *Schema) ToJSON() ([]byte, error) {
	return json.Marshal(s)
}

// New creates an empty schema.
func New() *Schema {
	return &Schema{
		Models: make(map[string]*Model),
		APIs:   make(map[string]*API),
		Enums:  make(map[string]*Enum),
		Types:  make(map[string]*TypeDecl),
	}
}

// RegisterEnum adds an enum definition to the schema.
func (s *Schema) RegisterEnum(e *Enum) {
	s.Enums[e.Name] = e
}

// RegisterType adds a type declaration to the schema.
func (s *Schema) RegisterType(t *TypeDecl) {
	s.Types[t.Name] = t
}

// RegisterModel adds a model definition to the schema.
func (s *Schema) RegisterModel(m *Model) {
	m.byID = make(map[int]*Field, len(m.Fields))
	m.byName = make(map[string]*Field, len(m.Fields))
	for i := range m.Fields {
		f := &m.Fields[i]
		// Pre-compute JSON prefix: `"fieldName":`
		f.JSONPrefix = append(f.JSONPrefix, '"')
		f.JSONPrefix = append(f.JSONPrefix, f.Name...)
		f.JSONPrefix = append(f.JSONPrefix, '"', ':')
		m.byID[f.ID] = f
		m.byName[f.Name] = f
	}
	s.Models[m.Name] = m
}

// RegisterAPI adds an API definition to the schema.
func (s *Schema) RegisterAPI(a *API) {
	s.APIs[a.Name] = a
}

// FieldByID returns a model field by its binary field ID.
func (m *Model) FieldByID(id int) *Field {
	return m.byID[id]
}

// FieldByName returns a model field by its name.
func (m *Model) FieldByName(name string) *Field {
	return m.byName[name]
}

// SelectToFieldMask converts a selection.Field list to a binary FieldMask.
// Maps field names to field IDs using the model schema.
func SelectToFieldMask(fields []*selection.Field, model *Model) []byte {
	if len(fields) == 0 {
		return nil // nil = select all
	}
	var mask []byte
	for _, f := range fields {
		if f.Children != nil {
			continue // skip relation fields
		}
		mf := model.FieldByName(f.Name)
		if mf != nil {
			mask = codec.FieldMaskSet(mask, mf.ID)
		}
	}
	return mask
}
