package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

func modelOwnershipField(model *ast.ModelDecl) *ast.FieldDecl {
	directive := findDirective(model.Directives, "auth")
	if directive == nil {
		return nil
	}
	for _, arg := range directive.Args {
		if arg.Name != "own" {
			continue
		}
		name := ""
		switch value := arg.Value.(type) {
		case *ast.Literal:
			name = value.Value
		case *ast.Ident:
			name = value.Name
		}
		for _, field := range model.Fields {
			if field.Name == name && field.Type != nil && field.Type.Name == "Int" && !field.Type.IsList {
				return field
			}
		}
		panic(&InternalCompilerError{Detail: "model ownership requires a scalar Int field: " + model.Name + "." + name})
	}
	return nil
}

// Model ownership is a record predicate; explicit API ownership remains a
// request-parameter predicate. Other authorization alternatives retain OR logic.
func writeModelAuthorization(b *strings.Builder, indent string, model *ast.ModelDecl, constraints bool) {
	directive := findDirective(model.Directives, "auth")
	owner := modelOwnershipField(model)
	if directive == nil {
		directive = findDirective(model.Directives, "withAuth")
	}
	if owner == nil {
		writeCRUDAuthCheck(b, indent, directive)
		return
	}
	writeAuthCheck(b, indent)
	writeAuthSubjectCheck(b, indent, directive)
	alternatives := *directive
	alternatives.Args = nil
	for _, arg := range directive.Args {
		if arg.Name != "own" {
			alternatives.Args = append(alternatives.Args, arg)
		}
	}
	if !writeAuthorizationDecision(b, indent, &alternatives) {
		fmt.Fprintf(b, "%s_authorized := false\n", indent)
	}
	identityTarget := "_ownID"
	if !constraints && !isCRUDInputField(owner, model, nil, false) {
		identityTarget = "_"
	}
	fmt.Fprintf(b, "%s%s, _ownValid := identity.Get(\"id\").Int64()\n", indent, identityTarget)
	fmt.Fprintf(b, "%sif !_authorized && !_ownValid { return errors.Forbidden }\n", indent)
	if constraints {
		fmt.Fprintf(b, "%svar _ownConditions []lux.Condition\n", indent)
		fmt.Fprintf(b, "%sif !_authorized { _ownConditions = []lux.Condition{%sWhere.%s.Eq(_ownID)} }\n", indent, model.Name, str.Capitalize(owner.Name))
	}
}

// Check the final transformed value before any write reaches the database.
func writeOwnershipMutationCheck(b *strings.Builder, model *ast.ModelDecl, fields []mutationInputField, update bool) {
	owner := modelOwnershipField(model)
	if owner == nil {
		return
	}
	for _, input := range fields {
		if input.field != owner {
			continue
		}
		invalid := owner.Name + "Val != _ownID"
		if owner.Type.Nullable {
			invalid = owner.Name + "Null || " + invalid
		}
		if input.optional {
			if update {
				invalid = owner.Name + "Present && (" + invalid + ")"
			} else {
				invalid = "!" + owner.Name + "Present || " + invalid
			}
		}
		fmt.Fprintf(b, "\t\tif !_authorized && (%s) { return errors.Forbidden }\n", invalid)
		return
	}
	if !update {
		b.WriteString("\t\tif !_authorized { return errors.Forbidden }\n")
	}
}
