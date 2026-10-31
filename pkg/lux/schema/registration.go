package schema

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

type identifierSet struct {
	ids     map[int]struct{}
	names   map[string]struct{}
	owner   string
	maximum int
}

func newIdentifierSet(owner string, maximum, count int) identifierSet {
	return identifierSet{ids: make(map[int]struct{}, count), names: make(map[string]struct{}, count), owner: owner, maximum: maximum}
}

func (set identifierSet) add(name string, id int) error {
	if name == "" || id <= 0 || id > set.maximum {
		return fmt.Errorf("schema: %s member %q ID %d must be in 1..%d with a nonempty name", set.owner, name, id, set.maximum)
	}
	if _, exists := set.ids[id]; exists {
		return fmt.Errorf("schema: %s has duplicate ID %d", set.owner, id)
	}
	if _, exists := set.names[name]; exists {
		return fmt.Errorf("schema: %s has duplicate name %q", set.owner, name)
	}
	set.ids[id], set.names[name] = struct{}{}, struct{}{}
	return nil
}

func validateFields(name string, fields []Field) error {
	if name == "" {
		return fmt.Errorf("schema: declaration must have a nonempty name")
	}
	set := newIdentifierSet(name, codec.MaxSelectionFieldID, len(fields))
	for _, field := range fields {
		if err := set.add(field.Name, field.ID); err != nil {
			return err
		}
	}
	return nil
}

func validateAPIDefinition(a *API) error {
	if a == nil {
		return fmt.Errorf("schema: nil API declaration")
	}
	if a.Name == "" || a.ID <= 0 || a.ID > codec.MaxAPIID {
		return fmt.Errorf("schema: API %q ID %d must be in 1..%d with a nonempty name", a.Name, a.ID, codec.MaxAPIID)
	}
	set := newIdentifierSet(a.Name, codec.MaxParameterID, len(a.Params))
	for _, param := range a.Params {
		if err := set.add(param.Name, param.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Schema) validatedAPIIndex() (map[int]string, error) {
	ids := make(map[int]string, len(s.APIs))
	for name, api := range s.APIs {
		if err := validateAPIDefinition(api); err != nil {
			return nil, err
		}
		if name != api.Name {
			return nil, fmt.Errorf("schema: API key %q does not match %q", name, api.Name)
		}
		if previous, ok := ids[api.ID]; ok {
			return nil, fmt.Errorf("schema: API ID %d is shared by %q and %q", api.ID, previous, name)
		}
		ids[api.ID] = name
	}
	return ids, nil
}

func (s *Schema) validateDeclarations() error {
	if s == nil {
		return fmt.Errorf("schema: nil runtime schema")
	}
	for name, model := range s.Models {
		if model == nil || model.Name != name {
			return fmt.Errorf("schema: model %q is nil or has a different name", name)
		}
		if err := validateFields(name, model.Fields); err != nil {
			return err
		}
	}
	for name, decl := range s.Types {
		if decl == nil || decl.Name != name {
			return fmt.Errorf("schema: type %q is nil or has a different name", name)
		}
		if err := validateFields(name, decl.Fields); err != nil {
			return err
		}
	}
	return nil
}
