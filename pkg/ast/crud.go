package ast

var allCRUDOperations = [...]string{"get", "list", "create", "update", "delete", "deleteMany"}

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
