package schema

import (
	"encoding/json"
	"fmt"
	"sort"

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
	Module string   `json:"module,omitempty"`
	Values []string `json:"values"`
}

// TypeUsage describes whether a structured type is used for API input,
// output, both directions, or is currently unreachable from public APIs.
type TypeUsage string

const (
	TypeUsageUnused      TypeUsage = "unused"
	TypeUsageInput       TypeUsage = "input"
	TypeUsageOutput      TypeUsage = "output"
	TypeUsageInputOutput TypeUsage = "inputOutput"
)

// TypeDecl describes a plain data type (non-DB, like AuthPayload).
type TypeDecl struct {
	Name   string    `json:"name"`
	Module string    `json:"module,omitempty"`
	Usage  TypeUsage `json:"usage,omitempty"`
	Fields []Field   `json:"fields"`
}

// AsModel converts TypeDecl to a Model for Binary↔JSON conversion.
// Types use the same field-based binary encoding as models.
func (td *TypeDecl) AsModel() *Model {
	// Copy the fields slice so we don't mutate td.Fields (JSONPrefix append).
	fields := make([]Field, len(td.Fields))
	copy(fields, td.Fields)
	m := &Model{Name: td.Name, Module: td.Module, Fields: fields}
	m.byID = make(map[int]*Field, len(m.Fields))
	m.byName = make(map[string]*Field, len(m.Fields))
	for i := range m.Fields {
		f := &m.Fields[i]
		f.JSONPrefix = append(f.JSONPrefix[:0:0], '"')
		f.JSONPrefix = append(f.JSONPrefix, f.Name...)
		f.JSONPrefix = append(f.JSONPrefix, '"', ':')
		m.byID[f.ID] = f
		m.byName[f.Name] = f
	}
	return m
}

// Model describes a model's fields for binary ↔ JSON conversion.
type Model struct {
	Name   string    `json:"name"`
	Module string    `json:"module,omitempty"`
	Usage  TypeUsage `json:"usage,omitempty"`
	Fields []Field   `json:"fields"`
	byID   map[int]*Field
	byName map[string]*Field
}

// Field describes a single model field.
type Field struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	Type       FieldType `json:"type"`
	TypeName   string    `json:"typeName,omitempty"` // original Luxo type name (User, MemberRole, etc.)
	Nullable   bool      `json:"nullable,omitempty"`
	IsList     bool      `json:"isList,omitempty"`
	Relation   bool      `json:"relation,omitempty"` // true if this is a relation field (not a DB column)
	Computed   bool      `json:"computed,omitempty"` // true if this is a selectable non-persistent field
	PrimaryKey bool      `json:"primaryKey,omitempty"`
	// Federation: which module defined this field (empty = same module as the model)
	Module string `json:"module,omitempty"`
	// Federation: FK field name for resolving cross-module relations (e.g. "userId")
	ForeignKey string `json:"foreignKey,omitempty"`
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
	FieldEnum    // string on wire
	FieldModel   // nested model (sub-message)
	FieldUUID    // 16-byte fixed value
	FieldDecimal // decimal string
	FieldJSON    // length-prefixed raw JSON
)

// API describes an API's params and return type.
type API struct {
	ID               int     `json:"id"`
	Name             string  `json:"name"`
	Module           string  `json:"module"`
	ReturnType       string  `json:"returnType,omitempty"`
	ReturnList       bool    `json:"returnList,omitempty"`
	Paginated        bool    `json:"paginated,omitempty"`
	Stream           bool    `json:"stream,omitempty"`
	Params           []Param `json:"params,omitempty"`
	Deprecated       bool    `json:"deprecated,omitempty"`
	DeprecatedReason string  `json:"deprecatedReason,omitempty"`
}

// Param describes an API parameter.
type Param struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	Type       FieldType `json:"type"`
	TypeName   string    `json:"typeName,omitempty"`
	IsList     bool      `json:"isList,omitempty"`     // true for array params (in/notIn → [T])
	Nullable   bool      `json:"nullable,omitempty"`   // true when the DSL type has a ? suffix
	HasDefault bool      `json:"hasDefault,omitempty"` // true when the DSL parameter declares a default
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
	FieldUUID:     "UUID",
	FieldDecimal:  "Decimal",
	FieldJSON:     "JSON",
}

// String returns the Luxo type name for the field type.
func (t FieldType) String() string {
	if t >= 0 && int(t) < len(fieldTypeNames) {
		return fieldTypeNames[t]
	}
	return "Unknown"
}

// MarshalJSON outputs FieldType as a string.
func (t FieldType) MarshalJSON() ([]byte, error) {
	if t >= 0 && int(t) < len(fieldTypeNames) {
		return json.Marshal(fieldTypeNames[t])
	}
	return json.Marshal("Unknown")
}

// UnmarshalJSON decodes the stable string form used by luxo.schema.json.
func (t *FieldType) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	for value, candidate := range fieldTypeNames {
		if candidate == name {
			*t = FieldType(value)
			return nil
		}
	}
	return fmt.Errorf("schema: unknown field type %q", name)
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
	if existing := s.Models[m.Name]; existing != nil {
		m = mergeModels(existing, m)
	}
	initializeModel(m)
	s.Models[m.Name] = m
}

func mergeModels(existing, incoming *Model) *Model {
	merged := &Model{Name: existing.Name, Module: existing.Module}
	if incoming.Module != "" {
		merged.Module = incoming.Module
	}
	merged.Fields = append(merged.Fields, existing.Fields...)
	byID := make(map[int]int, len(merged.Fields))
	byName := make(map[string]int, len(merged.Fields))
	for i := range merged.Fields {
		byID[merged.Fields[i].ID] = i
		byName[merged.Fields[i].Name] = i
	}
	for _, field := range incoming.Fields {
		index, exists := byID[field.ID]
		if !exists {
			index, exists = byName[field.Name]
		}
		if exists {
			old := merged.Fields[index]
			field.PrimaryKey = field.PrimaryKey || old.PrimaryKey
			delete(byID, old.ID)
			delete(byName, old.Name)
			merged.Fields[index] = field
		} else {
			index = len(merged.Fields)
			merged.Fields = append(merged.Fields, field)
		}
		byID[field.ID] = index
		byName[field.Name] = index
	}
	sort.Slice(merged.Fields, func(i, j int) bool { return merged.Fields[i].ID < merged.Fields[j].ID })
	return merged
}

func initializeModel(m *Model) {
	m.byID = make(map[int]*Field, len(m.Fields))
	m.byName = make(map[string]*Field, len(m.Fields))
	for i := range m.Fields {
		f := &m.Fields[i]
		// Pre-compute JSON prefix: `"fieldName":`
		f.JSONPrefix = append(f.JSONPrefix[:0], '"')
		f.JSONPrefix = append(f.JSONPrefix, f.Name...)
		f.JSONPrefix = append(f.JSONPrefix, '"', ':')
		m.byID[f.ID] = f
		m.byName[f.Name] = f
	}
}

// RegisterAPI adds an API definition to the schema.
func (s *Schema) RegisterAPI(a *API) {
	s.APIs[a.Name] = a
}

// InferTypeUsage derives input/output roles from API roots and follows nested
// model/type fields transitively. SDK generators use this to keep input DTOs
// strict while representing field-selected output values explicitly.
func (s *Schema) InferTypeUsage() {
	for _, model := range s.Models {
		model.Usage = TypeUsageUnused
	}
	for _, decl := range s.Types {
		decl.Usage = TypeUsageUnused
	}

	visited := make(map[typeUsageVisit]bool, len(s.Models)+len(s.Types))
	for _, api := range s.APIs {
		s.markTypeUsage(api.ReturnType, TypeUsageOutput, visited)
		for _, param := range api.Params {
			s.markTypeUsage(param.TypeName, TypeUsageInput, visited)
		}
	}
}

type typeUsageVisit struct {
	name  string
	usage TypeUsage
}

func (s *Schema) markTypeUsage(name string, usage TypeUsage, visited map[typeUsageVisit]bool) {
	if name == "" {
		return
	}
	visit := typeUsageVisit{name: name, usage: usage}
	if visited[visit] {
		return
	}
	visited[visit] = true

	if model := s.Models[name]; model != nil {
		model.Usage = mergeTypeUsage(model.Usage, usage)
		s.markFieldUsage(model.Fields, usage, visited)
		return
	}
	if decl := s.Types[name]; decl != nil {
		decl.Usage = mergeTypeUsage(decl.Usage, usage)
		s.markFieldUsage(decl.Fields, usage, visited)
	}
}

func (s *Schema) markFieldUsage(fields []Field, usage TypeUsage, visited map[typeUsageVisit]bool) {
	for i := range fields {
		s.markTypeUsage(fields[i].TypeName, usage, visited)
	}
}

func mergeTypeUsage(current, next TypeUsage) TypeUsage {
	if current == "" || current == TypeUsageUnused {
		return next
	}
	if current == next || current == TypeUsageInputOutput {
		return current
	}
	return TypeUsageInputOutput
}

// FieldByID returns a model field by its binary field ID.
func (m *Model) FieldByID(id int) *Field {
	return m.byID[id]
}

// FieldByName returns a model field by its name.
func (m *Model) FieldByName(name string) *Field {
	return m.byName[name]
}

// PrimaryKeyField returns the declared primary key, falling back to the
// conventional id field for schemas generated before primary-key metadata.
func (m *Model) PrimaryKeyField() *Field {
	for i := range m.Fields {
		if m.Fields[i].PrimaryKey {
			return &m.Fields[i]
		}
	}
	return m.FieldByName("id")
}

// HasExtendFields returns true if any field belongs to a different module.
func (m *Model) HasExtendFields() bool {
	for i := range m.Fields {
		if m.Fields[i].Module != "" {
			return true
		}
	}
	return false
}

// SelectToFieldMask converts a selection tree to the canonical recursive
// binary selection mask. Unknown fields and invalid nested selections fail.
func SelectToFieldMask(fields []*selection.Field, model *Model, schema *Schema) ([]byte, error) {
	return selectToFieldMask(fields, model, schema, 0)
}

func selectToFieldMask(fields []*selection.Field, model *Model, schema *Schema, depth int) ([]byte, error) {
	if len(fields) == 0 {
		return nil, nil // nil = select all
	}
	if model == nil {
		return nil, fmt.Errorf("selection has no return model")
	}
	if depth >= 32 {
		return nil, fmt.Errorf("selection depth exceeds 32")
	}
	var fieldMask []byte
	children := make([]codec.SelectionMaskChild, 0)
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		if _, exists := seen[f.Name]; exists {
			return nil, fmt.Errorf("duplicate selected field %q on %s", f.Name, model.Name)
		}
		seen[f.Name] = struct{}{}
		mf := model.FieldByName(f.Name)
		if mf == nil {
			return nil, fmt.Errorf("unknown selected field %q on %s", f.Name, model.Name)
		}
		fieldMask = codec.FieldMaskSet(fieldMask, mf.ID)
		if f.Children == nil {
			continue
		}
		if !mf.Relation || schema == nil {
			return nil, fmt.Errorf("field %s.%s does not support nested selection", model.Name, f.Name)
		}
		nested := schema.Models[mf.TypeName]
		if nested == nil {
			if decl := schema.Types[mf.TypeName]; decl != nil {
				nested = decl.AsModel()
			}
		}
		if nested == nil {
			return nil, fmt.Errorf("nested type %q for %s.%s is not registered", mf.TypeName, model.Name, f.Name)
		}
		childMask, err := selectToFieldMask(f.Children, nested, schema, depth+1)
		if err != nil {
			return nil, err
		}
		if len(childMask) > 0 {
			children = append(children, codec.SelectionMaskChild{FieldID: mf.ID, Mask: childMask})
		}
	}
	return codec.AppendSelectionMask(nil, fieldMask, children), nil
}
