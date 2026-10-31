package api

import (
	"fmt"
	"maps"
	"slices"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// Register adds an API during startup. Repeating the same binding is harmless;
// changing either side of an existing binding requires a new registry.
func (r *APIRegistry) Register(name string, id int) error {
	return r.register(name, id, true)
}

func (r *APIRegistry) register(name string, id int, prepare bool) error {
	if name == "" || id <= 0 || id > codec.MaxAPIID {
		return fmt.Errorf("registry: API %q ID %d must be in 1..%d with a nonempty name", name, id, codec.MaxAPIID)
	}
	if previous, ok := r.idToName[id]; ok {
		if previous != name {
			return fmt.Errorf("registry: API ID %d belongs to %q, not %q", id, previous, name)
		}
		return nil
	}
	if previous, ok := r.nameToID[name]; ok && previous != id {
		return fmt.Errorf("registry: API %q already has ID %d, not %d", name, previous, id)
	}
	r.idToName[id], r.nameToID[name] = name, id
	if prepare {
		r.refreshBinaryPlan(name)
	}
	return nil
}

// RegisterParams owns the ordered parameter metadata and builds static names.
// Registration happens only at startup, never concurrently with requests.
func (r *APIRegistry) RegisterParams(name string, params []ParamMeta) error {
	if exists, err := r.checkParams(name, params); exists || err != nil {
		return err
	}
	r.installParams(name, slices.Clone(params), true)
	return nil
}

func (r *APIRegistry) checkParams(name string, params []ParamMeta) (bool, error) {
	if name == "" {
		return false, fmt.Errorf("registry: parameter owner must have a nonempty API name")
	}
	if previous, ok := r.paramOrder[name]; ok {
		if sameRegisteredParams(previous, params) {
			return true, nil
		}
		return false, fmt.Errorf("registry: conflicting parameters for API %q", name)
	}
	return false, validateRegistryParams(name, params)
}

// Only accept a newly allocated, registry-owned slice here. Public registration
// clones caller input; schema conversion already supplies an independent slice.
func (r *APIRegistry) installParams(name string, owned []ParamMeta, prepare bool) {
	names := make([]string, len(owned))
	for i, param := range owned {
		names[i] = param.Name
	}
	r.paramOrder[name], r.paramNames[name] = owned, names
	if prepare {
		r.refreshBinaryPlan(name)
	}
}

func sameRegisteredParams(previous, incoming []ParamMeta) bool {
	if len(previous) != len(incoming) {
		return false
	}
	if slices.EqualFunc(previous, incoming, sameRegisteredParam) {
		return true
	}
	// Declaration order is not wire identity. Keep the existing slot order
	// while checking the alternate order by ID, including duplicate rejection.
	remaining := make(map[int]ParamMeta, len(previous))
	for _, param := range previous {
		remaining[param.FieldID] = param
	}
	for _, param := range incoming {
		old, ok := remaining[param.FieldID]
		if !ok || !sameRegisteredParam(old, param) {
			return false
		}
		delete(remaining, param.FieldID)
	}
	return true
}

func sameRegisteredParam(a, b ParamMeta) bool {
	if a == b {
		return true
	}
	// Scalar names can be explicit in schema metadata and omitted in generated
	// handlers. Named enums and structured types must still match exactly.
	if a.Type != "Enum" && a.Type != "Model" {
		if a.TypeName == a.Type {
			a.TypeName = ""
		}
		if b.TypeName == b.Type {
			b.TypeName = ""
		}
	}
	return a == b
}

func validateRegistryParams(api string, params []ParamMeta) error {
	ids := make(map[int]struct{}, len(params))
	names := make(map[string]struct{}, len(params))
	for _, param := range params {
		if param.Name == "" || param.FieldID <= 0 || param.FieldID > codec.MaxParameterID {
			return fmt.Errorf("registry: API %q parameter %q ID %d must be in 1..%d with a nonempty name", api, param.Name, param.FieldID, codec.MaxParameterID)
		}
		_, duplicateID := ids[param.FieldID]
		_, duplicateName := names[param.Name]
		if duplicateID || duplicateName {
			return fmt.Errorf("registry: API %q has duplicate parameter name or ID: %q/%d", api, param.Name, param.FieldID)
		}
		ids[param.FieldID], names[param.Name] = struct{}{}, struct{}{}
	}
	return nil
}

// RegisterSchemaAPIs validates and installs a complete set of routing metadata.
// Existing identical bindings are retained; any conflict leaves all state intact.
func (r *APIRegistry) RegisterSchemaAPIs(s *schema.Schema) error {
	if s == nil {
		return fmt.Errorf("registry: nil schema")
	}
	candidate := &APIRegistry{
		idToName: maps.Clone(r.idToName), nameToID: maps.Clone(r.nameToID),
		paramOrder: maps.Clone(r.paramOrder), paramNames: maps.Clone(r.paramNames), schema: s,
	}
	for name, definition := range s.APIs {
		if definition == nil || definition.Name != name {
			return fmt.Errorf("registry: schema API %q is nil or has a different name", name)
		}
		if err := candidate.register(name, definition.ID, false); err != nil {
			return err
		}
		params := schemaParamMetadata(definition.Params)
		exists, err := candidate.checkParams(name, params)
		if err != nil {
			return err
		}
		if !exists {
			candidate.installParams(name, params, false)
		}
	}
	if err := s.Prepare(); err != nil {
		return err
	}
	candidate.rebuildBinaryPlans()
	*r = *candidate
	return nil
}

func schemaParamMetadata(params []schema.Param) []ParamMeta {
	result := make([]ParamMeta, len(params))
	for i, param := range params {
		result[i] = ParamMeta{
			Name: param.Name, Type: param.Type.String(), TypeName: param.TypeName,
			FieldID: param.ID, IsList: param.IsList, Nullable: param.Nullable,
		}
	}
	return result
}
