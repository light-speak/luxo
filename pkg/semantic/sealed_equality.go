package semantic

import (
	"slices"

	"github.com/light-speak/luxo/pkg/token"
)

func (a *Analyzer) checkSealedEquality(owner *ResolvedType, pos token.Position) {
	if owner.IsList {
		a.addError(pos, "sealed type '%s' does not support equality for list values / 密封类型 '%s' 的列表不支持相等比较", owner.Name, owner.Name)
		return
	}
	var localPending [16]*ResolvedType
	pending := append(localPending[:0], owner)
	var localSeen [8]string
	seen := localSeen[:0]
	for len(pending) != 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if slices.Contains(seen, current.Name) {
			continue
		}
		seen = append(seen, current.Name)
		for _, variant := range current.Variants {
			for _, field := range variant.Fields {
				if !sealedPayloadSupportsEquality(field.Type) {
					a.addError(pos, "sealed payload '%s.%s.%s' does not support equality for '%s' / 密封载荷 '%s.%s.%s' 不支持 '%s' 的相等比较", current.Name, variant.Name, field.Name, formatResolvedType(field.Type), current.Name, variant.Name, field.Name, formatResolvedType(field.Type))
					return
				}
				if field.Type.Kind == TypeSealed {
					pending = append(pending, field.Type)
				}
			}
		}
	}
}

func sealedPayloadSupportsEquality(typ *ResolvedType) bool {
	if typ == nil || typ.IsList {
		return false
	}
	switch typ.Kind {
	case TypeInt, TypeFloat, TypeString, TypeBool, TypeDateTime, TypeDuration, TypeUUID, TypeDecimal, TypeEnum, TypeModel, TypeSealed:
		return true
	case TypeCustom:
		return ordinaryPayloadSupportsEquality(typ)
	default:
		return false
	}
}

// Ordinary object equality retains its current Go storage rule: a nullable
// object and nested custom/model fields are references, while direct list and
// byte/JSON slots make a non-null object value non-comparable.
func ordinaryPayloadSupportsEquality(typ *ResolvedType) bool {
	if typ.Nullable {
		return true
	}
	for _, field := range typ.Fields {
		if field.IsMethod {
			continue
		}
		if field.Type == nil || field.Type.IsList || field.Type.Kind == TypeBytes || field.Type.Kind == TypeJSON {
			return false
		}
		switch field.Type.Kind {
		case TypeUnknown, TypeGeneric, TypeTuple, TypeQueryBuilder:
			return false
		}
	}
	return true
}
