package schema

import (
	"fmt"
	"slices"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Sealed describes a closed sum. An empty variant table is uninhabited.
type Sealed struct {
	Name        string    `json:"name"`
	Module      string    `json:"module,omitempty"`
	Usage       TypeUsage `json:"usage,omitempty"`
	Description string    `json:"description,omitempty"`
	Variants    []Variant `json:"variants"`
	byID        map[int]*Variant
	byName      map[string]*Variant
	selection   *Model
}

// Variant keeps default-presence metadata separate from ordinary model fields.
type Variant struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Fields      []Param `json:"fields"`
	Description string  `json:"description,omitempty"`
	row         *Model
}

// RegisterSealed owns its metadata. Resolve references with Prepare at startup.
func (s *Schema) RegisterSealed(declaration *Sealed) error {
	if err := validateSealed(declaration); err != nil {
		return err
	}
	s.registerSealed(declaration)
	return nil
}

func (s *Schema) registerSealed(declaration *Sealed) {
	owned := *declaration
	owned.Variants = make([]Variant, len(declaration.Variants))
	owned.byID = make(map[int]*Variant, len(owned.Variants))
	owned.byName = make(map[string]*Variant, len(owned.Variants))
	owned.selection = &Model{Name: owned.Name, Fields: make([]Field, len(owned.Variants))}
	for index, source := range declaration.Variants {
		variant := &owned.Variants[index]
		*variant = source
		variant.Fields = slices.Clone(source.Fields)
		variant.row = &Model{Name: declaration.Name + "." + source.Name, Fields: make([]Field, len(source.Fields))}
		for fieldIndex, field := range source.Fields {
			variant.row.Fields[fieldIndex] = Field{ID: field.ID, Name: field.Name, Type: field.Type, TypeName: field.TypeName, Nullable: field.Nullable, IsList: field.IsList, Relation: field.Type == FieldModel || field.Type == FieldSealed}
		}
		initializeModel(variant.row)
		owned.selection.Fields[index] = Field{ID: source.ID, Name: source.Name, Type: FieldModel, TypeName: variant.row.Name, Relation: true}
		owned.byID[source.ID], owned.byName[source.Name] = variant, variant
	}
	initializeModel(owned.selection)
	if s.Sealeds == nil {
		s.Sealeds = make(map[string]*Sealed)
	}
	s.Sealeds[owned.Name] = &owned
}

// SelectionModel includes private case directories without publishing DTO types.
// Only selection and its dependency plan may interpret Owner.Variant names.
func (s *Schema) SelectionModel(name string) *Model {
	if len(s.Sealeds) == 0 {
		return s.LookupModel(name)
	}
	if declaration := s.Sealeds[name]; declaration != nil {
		return declaration.selection
	}
	if owner, variant, found := strings.Cut(name, "."); found {
		if declaration := s.Sealeds[owner]; declaration != nil {
			if selected := declaration.byName[variant]; selected != nil {
				return selected.row
			}
		}
	}
	return s.LookupModel(name)
}

func validateSealed(declaration *Sealed) error {
	if declaration == nil || declaration.Name == "" {
		return fmt.Errorf("schema: sealed declaration must have a nonempty name")
	}
	variants := newIdentifierSet(declaration.Name, codec.MaxSelectionFieldID, len(declaration.Variants))
	for _, variant := range declaration.Variants {
		if err := variants.add(variant.Name, variant.ID); err != nil {
			return err
		}
		fields := newIdentifierSet(declaration.Name+"."+variant.Name, codec.MaxSelectionFieldID, len(variant.Fields))
		for _, field := range variant.Fields {
			if err := fields.add(field.Name, field.ID); err != nil {
				return err
			}
			if !field.Type.valid() {
				return fmt.Errorf("schema: sealed %s.%s field %s has unknown type %d", declaration.Name, variant.Name, field.Name, field.Type)
			}
		}
	}
	return nil
}

func (s *Schema) validateSealedDeclarations() error {
	for name, declaration := range s.Sealeds {
		if declaration == nil || declaration.Name != name {
			return fmt.Errorf("schema: sealed %q is nil or has a different name", name)
		}
		if s.Models[name] != nil || s.Types[name] != nil || s.Enums[name] != nil {
			return fmt.Errorf("schema: sealed %q conflicts with another declaration", name)
		}
		if err := validateSealed(declaration); err != nil {
			return err
		}
		for _, variant := range declaration.Variants {
			for _, field := range variant.Fields {
				if err := s.validateSealedReference(field.Type, field.TypeName, true); err != nil {
					return err
				}
			}
		}
	}
	return s.validateSealedRoots()
}

func (s *Schema) validateSealedReference(kind FieldType, name string, payload bool) error {
	if kind == FieldSealed && s.Sealeds[name] == nil {
		return fmt.Errorf("schema: unknown sealed reference %q", name)
	}
	if payload && kind == FieldModel && s.Models[name] == nil && s.Types[name] == nil {
		return fmt.Errorf("schema: unknown sealed payload object reference %q", name)
	}
	if payload && kind == FieldEnum && s.Enums[name] == nil {
		return fmt.Errorf("schema: unknown sealed payload enum reference %q", name)
	}
	return nil
}

func (s *Schema) validateSealedRoots() error {
	for _, model := range s.Models {
		if err := s.validateSealedFields(model.Fields); err != nil {
			return err
		}
	}
	for _, declaration := range s.Types {
		if err := s.validateSealedFields(declaration.Fields); err != nil {
			return err
		}
	}
	for _, api := range s.APIs {
		if api == nil {
			continue
		}
		for _, param := range api.Params {
			if err := s.validateSealedReference(param.Type, param.TypeName, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Schema) validateSealedFields(fields []Field) error {
	for _, field := range fields {
		if err := s.validateSealedReference(field.Type, field.TypeName, false); err != nil {
			return err
		}
	}
	return nil
}
