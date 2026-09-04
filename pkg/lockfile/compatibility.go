package lockfile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// BreakingChange describes a schema change that cannot preserve the existing
// binary contract without regenerating every consumer.
type BreakingChange struct {
	Path   string
	Before string
	After  string
}

func (c BreakingChange) String() string {
	if c.After == "" {
		return fmt.Sprintf("%s was removed (was %s)", c.Path, c.Before)
	}
	if c.Before == "" {
		return fmt.Sprintf("%s was added as required (%s)", c.Path, c.After)
	}
	return fmt.Sprintf("%s changed from %s to %s", c.Path, c.Before, c.After)
}

// BreakingChanges compares the current AST with the persisted wire contract.
// Additive response fields and optional API parameters remain compatible.
func (lf *LockFile) BreakingChanges(files []*ast.File) []BreakingChange {
	contracts := collectContracts(files)
	var changes []BreakingChange
	changes = append(changes, compareFields("model", lf.Models, contracts.models, false)...)
	changes = append(changes, compareFields("type", lf.Types, contracts.types, false)...)
	changes = append(changes, compareFields("event", lf.Events, contracts.events, true)...)
	changes = append(changes, lf.compareAPIs(files, contracts.apis)...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

type wireContracts struct {
	models map[string]map[string]string
	types  map[string]map[string]string
	events map[string]map[string]string
	apis   map[string]apiContract
}

type apiContract struct {
	params     map[string]*ast.ParamDecl
	returnType string
}

func collectContracts(files []*ast.File) wireContracts {
	contracts := wireContracts{
		models: make(map[string]map[string]string),
		types:  make(map[string]map[string]string),
		events: make(map[string]map[string]string),
		apis:   make(map[string]apiContract),
	}
	for _, file := range files {
		for _, model := range file.Models {
			contracts.models[model.Name] = fieldTypes(model.Fields)
		}
		for _, extend := range file.Extends {
			fields := contracts.models[extend.Name]
			if fields == nil {
				fields = make(map[string]string)
				contracts.models[extend.Name] = fields
			}
			mergeFieldTypes(fields, extend.Fields)
		}
		for _, declaration := range file.Types {
			contracts.types[declaration.Name] = fieldTypes(declaration.Fields)
		}
		for _, event := range file.Events {
			params := make(map[string]string, len(event.Params))
			for _, param := range event.Params {
				params[param.Name] = formatTypeRef(param.Type)
			}
			contracts.events[event.Name] = params
		}
		for _, api := range file.APIs {
			contracts.apis[api.Name] = newAPIContract(api.Params, api.ReturnType)
		}
		for _, fn := range file.Functions {
			if hasServiceDirective(fn) {
				contracts.apis["svc:"+fn.Name] = newAPIContract(fn.Params, fn.ReturnType)
			}
		}
	}
	return contracts
}

func fieldTypes(fields []*ast.FieldDecl) map[string]string {
	result := make(map[string]string, len(fields))
	mergeFieldTypes(result, fields)
	return result
}

func mergeFieldTypes(result map[string]string, fields []*ast.FieldDecl) {
	for _, field := range fields {
		if field.Type != nil {
			result[field.Name] = formatTypeRef(field.Type)
		}
	}
}

func newAPIContract(params []*ast.ParamDecl, returnType *ast.TypeRef) apiContract {
	contract := apiContract{params: make(map[string]*ast.ParamDecl, len(params)), returnType: formatTypeRef(returnType)}
	for _, param := range params {
		contract.params[param.Name] = param
	}
	return contract
}

func compareFields(kind string, locked map[string]*ModelLock, current map[string]map[string]string, rejectAdditions bool) []BreakingChange {
	var changes []BreakingChange
	for name, entity := range locked {
		fields := current[name]
		for field, id := range entity.Fields {
			path := kind + " " + name + "." + field
			currentType, exists := fields[field]
			previousType := entity.FieldTypes[field]
			if !exists {
				changes = append(changes, BreakingChange{Path: path, Before: contractLabel(previousType, id)})
				continue
			}
			if previousType != "" && previousType != currentType {
				changes = append(changes, BreakingChange{Path: path, Before: previousType, After: currentType})
			}
		}
		if rejectAdditions && entity.FieldTypes != nil {
			for field, currentType := range fields {
				if _, exists := entity.Fields[field]; !exists {
					changes = append(changes, BreakingChange{Path: kind + " " + name + "." + field, After: currentType})
				}
			}
		}
	}
	return changes
}

func contractLabel(typeName string, id int) string {
	if typeName != "" {
		return typeName
	}
	return fmt.Sprintf("field ID %d", id)
}

func (lf *LockFile) compareAPIs(files []*ast.File, contracts map[string]apiContract) []BreakingChange {
	active := activeAPINames(files)
	var changes []BreakingChange
	for name, locked := range lf.APIs {
		if !active[name] {
			changes = append(changes, BreakingChange{Path: "api " + name, Before: fmt.Sprintf("API ID %d", locked.ID)})
			continue
		}
		contract, declared := contracts[name]
		if !declared || !locked.Contract {
			continue
		}
		for param, previousType := range locked.ParamTypes {
			current, exists := contract.params[param]
			path := "api " + name + "." + param
			if !exists {
				changes = append(changes, BreakingChange{Path: path, Before: previousType})
				continue
			}
			currentType := formatTypeRef(current.Type)
			if previousType != currentType {
				changes = append(changes, BreakingChange{Path: path, Before: previousType, After: currentType})
			}
		}
		for param, current := range contract.params {
			if _, exists := locked.ParamTypes[param]; exists || current.Type.Nullable || current.Default != nil {
				continue
			}
			changes = append(changes, BreakingChange{Path: "api " + name + "." + param, After: formatTypeRef(current.Type)})
		}
		if locked.ReturnType != contract.returnType {
			changes = append(changes, BreakingChange{Path: "api " + name + " return", Before: locked.ReturnType, After: contract.returnType})
		}
	}
	return changes
}

func activeAPINames(files []*ast.File) map[string]bool {
	scratch := New()
	scratch.updateAPIs(files)
	names := make(map[string]bool, len(scratch.APIs))
	for name := range scratch.APIs {
		names[name] = true
	}
	return names
}

func (lf *LockFile) updateContractMetadata(files []*ast.File) {
	contracts := collectContracts(files)
	updateFieldMetadata(lf.Models, contracts.models)
	updateFieldMetadata(lf.Types, contracts.types)
	updateFieldMetadata(lf.Events, contracts.events)
	for name, contract := range contracts.apis {
		locked := lf.APIs[name]
		// Defensive guard: Update always registers declared APIs before metadata.
		if locked == nil {
			continue
		}
		locked.ParamTypes = make(map[string]string, len(contract.params))
		locked.Contract = true
		for param, declaration := range contract.params {
			locked.ParamTypes[param] = formatTypeRef(declaration.Type)
		}
		locked.ReturnType = contract.returnType
	}
}

func updateFieldMetadata(locked map[string]*ModelLock, current map[string]map[string]string) {
	for name, entity := range locked {
		fields := current[name]
		entity.FieldTypes = make(map[string]string, len(fields))
		for field, typeName := range fields {
			if _, active := entity.Fields[field]; active {
				entity.FieldTypes[field] = typeName
			}
		}
	}
}

func formatTypeRef(ref *ast.TypeRef) string {
	if ref == nil {
		return "Void"
	}
	var value string
	switch {
	case len(ref.Tuple) > 0:
		parts := make([]string, 0, len(ref.Tuple))
		for _, item := range ref.Tuple {
			parts = append(parts, formatTypeRef(item))
		}
		value = "(" + strings.Join(parts, ",") + ")"
	case len(ref.TypeArgs) > 0:
		parts := make([]string, 0, len(ref.TypeArgs))
		for _, item := range ref.TypeArgs {
			parts = append(parts, formatTypeRef(item))
		}
		value = ref.Name + "<" + strings.Join(parts, ",") + ">"
	case ref.IsList:
		value = "[" + ref.Name + "]"
	default:
		value = ref.Name
	}
	if ref.Nullable {
		value += "?"
	}
	return value
}
