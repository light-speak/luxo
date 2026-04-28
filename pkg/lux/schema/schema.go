package schema

import (
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// Schema holds runtime metadata for Luxo binary ↔ JSON conversion.
// Used by Luvia to convert responses without requiring model structs.
// Loaded from luxo.lock + codegen-emitted schema registration.
type Schema struct {
	Models map[string]*Model // modelName → model metadata
	APIs   map[string]*API   // apiName → API metadata
}

// Model describes a model's fields for binary ↔ JSON conversion.
type Model struct {
	Name   string
	Fields []Field // ordered by field ID
	byID   map[int]*Field
	byName map[string]*Field
}

// Field describes a single model field.
type Field struct {
	ID       int
	Name     string // JSON/luxo field name (camelCase)
	Type     FieldType
	Nullable bool
	IsList   bool
	// Pre-computed JSON prefix: `"name":` as bytes for zero-alloc writing
	JSONPrefix []byte
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
	ID         int // stable API ID from luxo.lock (for RPC routing)
	Name       string
	Module     string // owning module name
	ReturnType string // model name or scalar type
	ReturnList bool   // true for list return
	Paginated  bool   // true for CRUD list (items + total + page + pageSize)
	Params     []Param
}

// Param describes an API parameter.
type Param struct {
	ID   int
	Name string
	Type FieldType
}

// New creates an empty schema.
func New() *Schema {
	return &Schema{
		Models: make(map[string]*Model),
		APIs:   make(map[string]*API),
	}
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
