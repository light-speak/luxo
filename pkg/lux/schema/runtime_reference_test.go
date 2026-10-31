package schema

import "fmt"

// Freeze the pre-prebuild copy and lookup paths for local performance comparisons.
func asModelBeforePrebuild(td *TypeDecl) *Model {
	fields := make([]Field, len(td.Fields))
	copy(fields, td.Fields)
	m := &Model{Name: td.Name, Module: td.Module, Fields: fields, Description: td.Description, Directives: td.Directives}
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

func lookupModelBeforePrebuild(s *Schema, name string) *Model {
	if model := s.Models[name]; model != nil {
		return model
	}
	return typeModelBeforePrebuild(s, name)
}

//go:noinline
func typeModelBeforePrebuild(s *Schema, name string) *Model {
	if decl := s.Types[name]; decl != nil {
		return asModelBeforePrebuild(decl)
	}
	return nil
}

// Keep strict nested validation identical on both sides; only lookup differs.
func appendNestedJSONBeforePrebuild(dst, blob []byte, f *Field, schemas ...*Schema) ([]byte, error) {
	if len(schemas) == 0 || schemas[0] == nil {
		return dst, fmt.Errorf("schema: nested type %q for field %q requires schema", f.TypeName, f.Name)
	}
	nested := lookupModelBeforePrebuild(schemas[0], f.TypeName)
	if nested == nil {
		return dst, fmt.Errorf("schema: nested type %q for field %q is not registered", f.TypeName, f.Name)
	}
	if f.IsList {
		return BinaryListToJSON(dst, blob, nested, schemas[0])
	}
	return BinaryToJSON(dst, blob, nested, schemas[0])
}
