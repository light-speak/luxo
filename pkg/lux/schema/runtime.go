package schema

import "slices"

// LookupModel returns read-only field metadata, preferring a model over a type
// with the same name. Types must be registered or explicitly prepared at startup.
// This lookup never constructs metadata, mutates state or acquires locks.
func (s *Schema) LookupModel(name string) *Model {
	if model := s.Models[name]; model != nil {
		return model
	}
	return s.typeModels[name]
}

// Prepare rebuilds runtime indexes after decoding a schema or editing metadata
// directly during startup. RegisterModel/RegisterType already prepare their own
// metadata. Prepare is not safe concurrently with requests or registration.
// Call InferTypeUsage afterwards when API roots or field graphs have changed.
func (s *Schema) Prepare() {
	s.typeModels = make(map[string]*Model, len(s.Types))
	for _, model := range s.Models {
		initializeModel(model)
	}
	for _, decl := range s.Types {
		s.RegisterType(decl)
	}
}

// AsModel returns an independent, mutable copy of a type's field metadata.
// Runtime request paths use Schema.LookupModel instead of copying per request.
func (td *TypeDecl) AsModel() *Model {
	fields := make([]Field, len(td.Fields))
	copy(fields, td.Fields)
	for i := range fields {
		fields[i].Directives = slices.Clone(fields[i].Directives)
		fields[i].JSONPrefix = nil
	}
	m := &Model{
		Name: td.Name, Module: td.Module, Usage: td.Usage, Fields: fields,
		Description: td.Description, Directives: slices.Clone(td.Directives),
	}
	initializeModel(m)
	return m
}
