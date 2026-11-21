package codegen

import (
	"fmt"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"strings"
)

// Pre-staging emitters for regression and benchmark comparisons only.
// They deliberately preserve the old side-effect ordering, never production registration.
// The retired request invalidation call is omitted on both sides; these emitters
// must not be compared numerically with historical runs that included that call.
func generatePreviousCreateHandler(b *strings.Builder, m *ast.ModelDecl, apiName string, enums map[string]bool, needAuth bool, modelAuth *ast.Directive) {
	name := m.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
	if needAuth {
		writeCRUDAuthCheck(b, "\t\t", modelAuth)
	}
	fmt.Fprintf(b, "\t\tbuilder := app.%s.Create()\n", name)

	for _, f := range m.Fields {
		if skipHandlerField(f, enums) {
			continue
		}
		setter := "Set" + str.Capitalize(f.Name)
		optional := (f.Type != nil && f.Type.Nullable) || f.Default != nil
		if optional {
			fmt.Fprintf(b, "\t\tif req.HasParam(%q) {\n", f.Name)
			generatePreviousParamSet(b, f, setter, "\t\t\t", enums)
			fmt.Fprintf(b, "\t\t}\n")
		} else {
			generatePreviousParamSet(b, f, setter, "\t\t", enums)
		}
	}

	// @beforeSave hooks are compiled per-field inside generatePreviousParamSet

	fmt.Fprintf(b, "\t\tresult, err := builder.Exec(ctx)\n")
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	writeComputedResolve(b, m, "[]*"+name+"{result}", "\t\t")
	fmt.Fprintf(b, "\t\tif err := result.WriteLuxo(req.Buf, req.FieldMask); err != nil { return err }\n")
	fmt.Fprintf(b, "\t\treturn nil\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "}\n\n")
}

func generatePreviousUpdateHandler(b *strings.Builder, m *ast.ModelDecl, apiName, idType string, enums map[string]bool, needAuth bool, modelAuth *ast.Directive) {
	name := m.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
	if needAuth {
		writeCRUDAuthCheck(b, "\t\t", modelAuth)
	}
	writeParamID(b, idType, "\t\t")
	fmt.Fprintf(b, "\t\texisting, err := app.%s.Find(ctx, id)\n", name)
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	fmt.Fprintf(b, "\t\tif existing == nil {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q, ID: errors.%s(id)})\n\t\t}\n", name, resourceIDConstructor(idType))
	tableName := modelTableName(name)
	fmt.Fprintf(b, "\t\tbuilder := new%sUpdateBuilder(app.%s.db, %q, id)\n", name, name, tableName)

	for _, f := range m.Fields {
		if skipHandlerField(f, enums) || f.Name == primaryKeyFieldName(m) || hasDirective(f.Directives, "immutable") {
			continue
		}
		setter := "Set" + str.Capitalize(f.Name)
		fmt.Fprintf(b, "\t\tif req.HasParam(%q) {\n", f.Name)
		generatePreviousParamSet(b, f, setter, "\t\t\t", enums)
		fmt.Fprintf(b, "\t\t}\n")
	}
	fmt.Fprintf(b, "\t\tresult, err := builder.Exec(ctx)\n")
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	writeComputedResolve(b, m, "[]*"+name+"{result}", "\t\t")
	fmt.Fprintf(b, "\t\tif err := result.WriteLuxo(req.Buf, req.FieldMask); err != nil { return err }\n")
	fmt.Fprintf(b, "\t\treturn nil\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "}\n\n")
}

func generatePreviousParamSet(b *strings.Builder, f *ast.FieldDecl, setter, indent string, enums map[string]bool) {
	originalIndent := indent
	if f.Type != nil && f.Type.Nullable {
		fmt.Fprintf(b, "%sif req.ParamIsNull(%q) {\n", indent, f.Name)
		fmt.Fprintf(b, "%s\tbuilder.%s(nil)\n", indent, setter)
		fmt.Fprintf(b, "%s} else {\n", indent)
		indent += "\t"
	}
	varName := f.Name + "Val"

	// For nullable fields, use the base type for param extraction, then take pointer
	baseType := f.Type
	if f.Type != nil && f.Type.Nullable {
		base := *f.Type
		base.Nullable = false
		baseType = &base
	}
	goType := resolveGoType(baseType)

	// Enum values are checked before reaching persistence setters.
	isEnum := f.Type != nil && enums[f.Type.Name]
	method := paramMethod(goType)
	if isEnum {
		fmt.Fprintf(b, "%svar %s %s\n", indent, varName, goType)
		writeEnumParamValue(b, f.Name, varName, baseType, false, indent)
	} else {
		fmt.Fprintf(b, "%s%s, err := req.Param%s(%q)\n", indent, varName, method, f.Name)
		fmt.Fprintf(b, "%sif err != nil {\n", indent)
		fmt.Fprintf(b, "%s\treturn fmt.Errorf(\"param %s: %%w\", err)\n", indent, f.Name)
		fmt.Fprintf(b, "%s}\n", indent)
	}

	// Field validation directives
	generateFieldValidation(b, f, varName, indent)

	// @beforeSave: transform field value before persistence
	generateBeforeSave(b, f, varName, indent)

	// @hash: auto-hash password before save
	if hasDirective(f.Directives, "hash") {
		fmt.Fprintf(b, "%s%s, err = luxocrypto.HashPassword(%s)\n", indent, varName, varName)
		fmt.Fprintf(b, "%sif err != nil {\n", indent)
		fmt.Fprintf(b, "%s\treturn fmt.Errorf(\"hash %s: %%w\", err)\n", indent, f.Name)
		fmt.Fprintf(b, "%s}\n", indent)
	}

	argExpr := varName
	if f.Type != nil && f.Type.Nullable && !f.Type.IsList {
		argExpr = "&" + varName
	}
	fmt.Fprintf(b, "%sbuilder.%s(%s)\n", indent, setter, argExpr)
	if f.Type != nil && f.Type.Nullable {
		fmt.Fprintf(b, "%s}\n", originalIndent)
	}
}
