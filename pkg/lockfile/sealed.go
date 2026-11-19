package lockfile

import (
	"fmt"
	"slices"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

func collectSealedIDSpaces(current map[string]map[string]bool, declarations []*ast.SealedDecl) {
	for _, declaration := range declarations {
		variants := make(map[string]bool, len(declaration.Variants))
		for _, variant := range declaration.Variants {
			variants[variant.Name] = true
			fields := make(map[string]bool, len(variant.Fields))
			for _, field := range variant.Fields {
				fields[field.Name] = true
			}
			current[declaration.Name+"."+variant.Name] = fields
		}
		current[declaration.Name] = variants
	}
}

func (lf *LockFile) sealedIDSpace(name, kind string) *ModelLock {
	space := lf.Types[name]
	if space == nil {
		space = &ModelLock{NextID: 1, Fields: make(map[string]int)}
		lf.Types[name] = space
	}
	space.setKind(kind)
	return space
}

func assignSealedID(space *ModelLock, name string) {
	if _, present := space.Fields[name]; !present {
		space.Fields[name] = space.NextID
		space.NextID++
	}
}

func (lf *LockFile) updateSealedIDs(declarations []*ast.SealedDecl) {
	for _, declaration := range declarations {
		owner := lf.sealedIDSpace(declaration.Name, "sealed")
		for _, variant := range declaration.Variants {
			assignSealedID(owner, variant.Name)
			payload := lf.sealedIDSpace(declaration.Name+"."+variant.Name, "sealedVariant")
			for _, field := range variant.Fields {
				assignSealedID(payload, field.Name)
			}
			payload.setDefaultFields(variant.Fields)
		}
	}
}

func collectSealedContracts(contracts *wireContracts, declarations []*ast.SealedDecl) {
	if len(declarations) != 0 && contracts.kinds == nil {
		contracts.kinds = make(map[string]string)
	}
	for _, declaration := range declarations {
		contracts.kinds[declaration.Name] = "sealed"
		variants := make(map[string]string, len(declaration.Variants))
		for _, variant := range declaration.Variants {
			variants[variant.Name] = "sealedVariant"
			name := declaration.Name + "." + variant.Name
			contracts.kinds[name] = "sealedVariant"
			fields := make(map[string]string, len(variant.Fields))
			for _, field := range variant.Fields {
				fields[field.Name] = formatTypeRef(field.Type)
				if field.Default != nil {
					contracts.addDefault(name, field.Name)
				}
			}
			contracts.types[name] = fields
		}
		contracts.types[declaration.Name] = variants
	}
}

func compareSealedContracts(locked map[string]*ModelLock, current wireContracts) []BreakingChange {
	var changes []BreakingChange
	for name, previous := range locked {
		if _, present := current.types[name]; present && previous.kind() != current.kinds[name] {
			changes = append(changes, BreakingChange{Path: "type " + name + " kind", Before: printableLockKind(previous.kind()), After: printableLockKind(current.kinds[name])})
		}
		if previous.kind() == "sealedVariant" && current.kinds[name] == "sealedVariant" {
			changes = append(changes, compareSealedDefaults(name, previous, current)...)
		}
		if previous.kind() != "sealed" || current.kinds[name] != "sealed" {
			continue
		}
		for variant := range current.types[name] {
			if _, present := previous.Fields[variant]; !present {
				changes = append(changes, BreakingChange{Path: "type " + name + "." + variant, After: "sealedVariant"})
			}
		}
	}
	return changes
}

func validateSealedLockKind(group, name string, space *ModelLock, spaces map[string]*ModelLock) error {
	if space.typeInfo == nil {
		return nil
	}
	if err := space.validateDefaultFields(name); err != nil {
		return err
	}
	if space.kind() == "" {
		return nil
	}
	if group != "types" {
		return fmt.Errorf("luxo.lock %s.%s kind %q is only valid for types", group, name, space.kind())
	}
	switch space.kind() {
	case "sealed":
		if strings.Contains(name, ".") {
			return fmt.Errorf("luxo.lock sealed owner %q must not contain a payload separator", name)
		}
		for variant := range space.Fields {
			payload := spaces[name+"."+variant]
			if payload == nil || payload.kind() != "sealedVariant" {
				return fmt.Errorf("luxo.lock sealed %s.%s has no payload namespace", name, variant)
			}
		}
	case "sealedVariant":
		owner, variant, found := strings.Cut(name, ".")
		if !found || owner == "" || variant == "" || strings.Contains(variant, ".") {
			return fmt.Errorf("luxo.lock invalid sealed payload namespace %q", name)
		}
		if spaces[owner] == nil || len(space.Fields) != 0 && spaces[owner].kind() != "sealed" {
			return fmt.Errorf("luxo.lock sealed payload %q has no sealed owner", name)
		}
	default:
		return fmt.Errorf("luxo.lock unknown type kind %q in %s", space.kind(), name)
	}
	return nil
}

type typeLockMetadata struct {
	kind       string
	defaultIDs []int
}

func (space *ModelLock) kind() string {
	if space.typeInfo == nil {
		return ""
	}
	return space.typeInfo.kind
}

func (space *ModelLock) ensureTypeInfo() *typeLockMetadata {
	if space.typeInfo == nil {
		space.typeInfo = &typeLockMetadata{}
	}
	return space.typeInfo
}

func (space *ModelLock) setKind(kind string) {
	if kind == "" {
		space.typeInfo = nil
		return
	}
	space.ensureTypeInfo().kind = kind
}

func cloneTypeLockMetadata(source *typeLockMetadata) *typeLockMetadata {
	if source == nil {
		return nil
	}
	return &typeLockMetadata{kind: source.kind, defaultIDs: slices.Clone(source.defaultIDs)}
}

func (space *ModelLock) setDefaultFields(fields []*ast.ParamDecl) {
	var ids []int
	for _, field := range fields {
		if field.Default != nil {
			ids = append(ids, space.Fields[field.Name])
		}
	}
	slices.Sort(ids)
	space.ensureTypeInfo().defaultIDs = ids
}

func (contracts *wireContracts) addDefault(owner, field string) {
	if contracts.defaults == nil {
		contracts.defaults = make(map[string]map[string]bool)
	}
	if contracts.defaults[owner] == nil {
		contracts.defaults[owner] = make(map[string]bool)
	}
	contracts.defaults[owner][field] = true
}

func printableLockKind(kind string) string {
	if kind == "" {
		return "type"
	}
	return kind
}

func compareSealedDefaults(name string, previous *ModelLock, current wireContracts) []BreakingChange {
	var changes []BreakingChange
	for field, typ := range current.types[name] {
		id, existed := previous.Fields[field]
		if !existed {
			if !current.defaults[name][field] {
				changes = append(changes, BreakingChange{Path: "type " + name + "." + field, After: typ})
			}
			continue
		}
		if _, defaulted := slices.BinarySearch(previous.typeInfo.defaultIDs, id); defaulted && !current.defaults[name][field] {
			changes = append(changes, BreakingChange{Path: "type " + name + "." + field + " default", Before: "optional", After: "required"})
		}
	}
	return changes
}

func (space *ModelLock) validateDefaultFields(name string) error {
	ids := space.typeInfo.defaultIDs
	if ids == nil {
		return nil
	}
	if space.kind() != "sealedVariant" {
		return fmt.Errorf("luxo.lock %s default_fields requires a sealedVariant", name)
	}
	remaining := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || remaining[id] {
			return fmt.Errorf("luxo.lock %s has invalid or duplicate default field ID %d", name, id)
		}
		remaining[id] = true
	}
	for _, id := range space.Fields {
		delete(remaining, id)
	}
	for _, id := range ids {
		if remaining[id] {
			return fmt.Errorf("luxo.lock %s default field ID %d is not active", name, id)
		}
	}
	return nil
}
