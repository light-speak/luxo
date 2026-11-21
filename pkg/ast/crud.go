package ast

import "strings"

var allCRUDOperations = [...]string{"get", "list", "create", "update", "delete", "deleteMany"}

// CRUDAPIName is shared by stable-ID assignment and route generation.
func CRUDAPIName(modelName, operation string) string {
	switch operation {
	case "list":
		return "list" + PluralModelName(modelName)
	case "deleteMany":
		return "delete" + PluralModelName(modelName)
	default:
		return operation + modelName
	}
}

// PluralModelName preserves the DSL's simple, deterministic plural naming rules.
func PluralModelName(name string) string {
	if strings.HasSuffix(name, "s") || strings.HasSuffix(name, "x") || strings.HasSuffix(name, "z") {
		return name + "es"
	}
	if strings.HasSuffix(name, "y") && len(name) >= 2 {
		// Only consonant+y → ies (e.g. History→Histories)
		// Vowel+y → just add s (e.g. Gateway→Gateways, Key→Keys)
		prev := name[len(name)-2]
		if prev != 'a' && prev != 'e' && prev != 'i' && prev != 'o' && prev != 'u' &&
			prev != 'A' && prev != 'E' && prev != 'I' && prev != 'O' && prev != 'U' {
			return name[:len(name)-1] + "ies"
		}
	}
	return name + "s"
}

// CRUDOperations returns the operations enabled by a model's @crud directive.
func (m *ModelDecl) CRUDOperations() []string {
	if m == nil {
		return nil
	}
	for _, directive := range m.Directives {
		if directive.Name != "crud" {
			continue
		}
		if len(directive.Args) == 0 {
			return allCRUDOperationNames()
		}
		for _, arg := range directive.Args {
			switch arg.Name {
			case "only":
				return crudOperationList(arg.Value)
			case "except":
				return excludeCRUDOperations(crudOperationList(arg.Value))
			}
		}
		return allCRUDOperationNames()
	}
	return nil
}

func allCRUDOperationNames() []string {
	operations := make([]string, len(allCRUDOperations))
	copy(operations, allCRUDOperations[:])
	return operations
}

func crudOperationList(expr Expr) []string {
	list, ok := expr.(*ListExpr)
	if !ok {
		return nil
	}
	operations := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		if ident, ok := item.(*Ident); ok {
			operations = append(operations, ident.Name)
		}
	}
	return operations
}

func excludeCRUDOperations(excluded []string) []string {
	operations := make([]string, 0, len(allCRUDOperations))
	for _, operation := range allCRUDOperations {
		if !containsCRUDOperation(excluded, operation) {
			operations = append(operations, operation)
		}
	}
	return operations
}

func containsCRUDOperation(operations []string, target string) bool {
	for _, operation := range operations {
		if operation == target {
			return true
		}
	}
	return false
}
