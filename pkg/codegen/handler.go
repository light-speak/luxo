package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// crudOps defines the 5 CRUD operations.
var crudOps = []string{"get", "list", "create", "update", "delete"}

// generateHandlerFile produces handler.gen.go containing CRUD handlers and RegisterHandlers.
// Returns nil if there are no @crud models.
func generateHandlerFile(result *semantic.Result, packageName string) []byte {
	var models []*ast.ModelDecl
	for _, file := range result.Files {
		for _, m := range file.Models {
			if hasCrud(m) {
				models = append(models, m)
			}
		}
	}
	if len(models) == 0 {
		return nil
	}

	var b strings.Builder
	writeHeader(&b, packageName, "handler.gen.go")

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	b.WriteString(")\n\n")

	// Collect enum names to distinguish enum fields from relations.
	enums := make(map[string]bool)
	for _, file := range result.Files {
		for _, e := range file.Enums {
			enums[e.Name] = true
		}
	}

	// Generate handlers per model
	for _, m := range models {
		ops := crudOperations(m)
		for _, op := range ops {
			generateHandler(&b, m, op, enums)
		}
	}

	// RegisterHandlers function
	generateRegisterFunc(&b, models)

	return []byte(b.String())
}

// hasCrud checks if model has @crud directive.
func hasCrud(m *ast.ModelDecl) bool {
	return hasDirective(m.Directives, "crud")
}

// crudOperations returns the list of CRUD ops enabled for a model.
func crudOperations(m *ast.ModelDecl) []string {
	for _, d := range m.Directives {
		if d.Name != "crud" {
			continue
		}
		// @crud without args → all 5 ops
		if len(d.Args) == 0 {
			return crudOps
		}
		// @crud(only: [...]) or @crud(except: [...])
		for _, arg := range d.Args {
			if arg.Name == "only" {
				return extractListArg(arg.Value)
			}
			if arg.Name == "except" {
				except := extractListArg(arg.Value)
				return filterOps(crudOps, except)
			}
		}
		return crudOps
	}
	return nil
}

// extractListArg extracts string values from a list expression [get, list, ...].
func extractListArg(expr ast.Expr) []string {
	list, ok := expr.(*ast.ListExpr)
	if !ok {
		return nil
	}
	var result []string
	for _, elem := range list.Items {
		if ident, ok := elem.(*ast.Ident); ok {
			result = append(result, ident.Name)
		}
	}
	return result
}

// filterOps returns ops minus excluded ones.
func filterOps(all, except []string) []string {
	set := make(map[string]bool, len(except))
	for _, e := range except {
		set[e] = true
	}
	var result []string
	for _, op := range all {
		if !set[op] {
			result = append(result, op)
		}
	}
	return result
}

// generateHandler generates a single CRUD handler function.
func generateHandler(b *strings.Builder, m *ast.ModelDecl, op string, enums map[string]bool) {
	name := m.Name
	idType := idGoType(m)

	switch op {
	case "get":
		apiName := "get" + name
		fmt.Fprintf(b, `func handle%s(app *App) api.HandlerFunc {
	return func(ctx context.Context, req *api.Request) (any, error) {
		id, err := req.Param%s("id")
		if err != nil {
			return nil, err
		}
		return app.%s.Find(ctx, id)
	}
}

`, capitalize(apiName), paramMethod(idType), name)

	case "list":
		apiName := "list" + pluralize(name)
		fmt.Fprintf(b, `func handle%s(app *App) api.HandlerFunc {
	return func(ctx context.Context, req *api.Request) (any, error) {
		return app.%s.Where().All(ctx)
	}
}

`, capitalize(apiName), name)

	case "create":
		apiName := "create" + name
		generateCreateHandler(b, m, apiName, enums)

	case "update":
		apiName := "update" + name
		generateUpdateHandler(b, m, apiName, idType, enums)

	case "delete":
		apiName := "delete" + name
		soft := isSoftDelete(m)
		if soft {
			fmt.Fprintf(b, `func handle%s(app *App) api.HandlerFunc {
	return func(ctx context.Context, req *api.Request) (any, error) {
		id, err := req.Param%s("id")
		if err != nil {
			return nil, err
		}
		n, err := app.%s.SoftDelete(ctx, %sWhere.Id.Eq(id))
		if err != nil {
			return nil, err
		}
		return map[string]int64{"deleted": n}, nil
	}
}

`, capitalize(apiName), paramMethod(idType), name, name)
		} else {
			fmt.Fprintf(b, `func handle%s(app *App) api.HandlerFunc {
	return func(ctx context.Context, req *api.Request) (any, error) {
		id, err := req.Param%s("id")
		if err != nil {
			return nil, err
		}
		n, err := app.%s.Where(%sWhere.Id.Eq(id)).Delete(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"deleted": n}, nil
	}
}

`, capitalize(apiName), paramMethod(idType), name, name)
		}
	}
}

// generateCreateHandler generates a create handler that reads params and calls Create().
func generateCreateHandler(b *strings.Builder, m *ast.ModelDecl, apiName string, enums map[string]bool) {
	name := m.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", capitalize(apiName))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) (any, error) {\n")
	fmt.Fprintf(b, "\t\tbuilder := app.%s.Create()\n", name)

	for _, f := range m.Fields {
		if skipHandlerField(f, enums) {
			continue
		}
		setter := "Set" + capitalize(f.Name)
		nullable := f.Type != nil && f.Type.Nullable
		if nullable {
			fmt.Fprintf(b, "\t\tif req.HasParam(%q) {\n", f.Name)
			generateParamSet(b, f, setter, "\t\t\t", enums)
			fmt.Fprintf(b, "\t\t}\n")
		} else {
			generateParamSet(b, f, setter, "\t\t", enums)
		}
	}

	fmt.Fprintf(b, "\t\treturn builder.Exec(ctx)\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "}\n\n")
}

// generateUpdateHandler generates an update handler.
func generateUpdateHandler(b *strings.Builder, m *ast.ModelDecl, apiName, idType string, enums map[string]bool) {
	name := m.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", capitalize(apiName))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) (any, error) {\n")
	fmt.Fprintf(b, "\t\tid, err := req.Param%s(\"id\")\n", paramMethod(idType))
	fmt.Fprintf(b, "\t\tif err != nil {\n")
	fmt.Fprintf(b, "\t\t\treturn nil, err\n")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t\tif _, err := app.%s.Find(ctx, id); err != nil {\n", name)
	fmt.Fprintf(b, "\t\t\treturn nil, err\n")
	fmt.Fprintf(b, "\t\t}\n")
	tableName := toSnakeCase(name) + "s"
	fmt.Fprintf(b, "\t\tbuilder := new%sUpdateBuilder(app.%s.db, %q, id)\n", name, name, tableName)

	for _, f := range m.Fields {
		if skipHandlerField(f, enums) || f.Name == "id" || hasDirective(f.Directives, "immutable") {
			continue
		}
		setter := "Set" + capitalize(f.Name)
		fmt.Fprintf(b, "\t\tif req.HasParam(%q) {\n", f.Name)
		generateParamSet(b, f, setter, "\t\t\t", enums)
		fmt.Fprintf(b, "\t\t}\n")
	}
	fmt.Fprintf(b, "\t\treturn builder.Exec(ctx)\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "}\n\n")
}

// skipHandlerField returns true if a field should be excluded from create/update handlers.
func skipHandlerField(f *ast.FieldDecl, enums map[string]bool) bool {
	if isAutoManaged(f) || hasDirective(f.Directives, "internal") || f.Computed != nil {
		return true
	}
	if f.Name == "id" && hasDirective(f.Directives, "auto") {
		return true
	}
	if isRelationField(f, enums) {
		return true
	}
	return false
}

// isRelationField detects if a field is a relation to another model.
// Non-built-in, non-enum types are relations. [String]/[Int] are scalar arrays, not relations.
func isRelationField(f *ast.FieldDecl, enums map[string]bool) bool {
	if f.Type == nil {
		return false
	}
	// Check the base type name (works for both scalar and list)
	switch f.Type.Name {
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes":
		return false // String, [String], Int, [Int] etc. are never relations
	}
	// Enum types — not relations
	if enums[f.Type.Name] {
		return false
	}
	// Non-builtin, non-enum: it's a relation (Post, [Post], User, etc.)
	return true
}

// generateParamSet writes a param extraction + setter call.
func generateParamSet(b *strings.Builder, f *ast.FieldDecl, setter, indent string, enums map[string]bool) {
	varName := f.Name + "Val"
	method := paramMethod(resolveGoType(f.Type))
	fmt.Fprintf(b, "%s%s, err := req.Param%s(%q)\n", indent, varName, method, f.Name)
	fmt.Fprintf(b, "%sif err != nil {\n", indent)
	fmt.Fprintf(b, "%s\treturn nil, fmt.Errorf(\"param %s: %%w\", err)\n", indent, f.Name)
	fmt.Fprintf(b, "%s}\n", indent)

	argExpr := varName
	if f.Type != nil && f.Type.Nullable {
		// Nullable field: pass pointer
		argExpr = "&" + varName
	} else if f.Type != nil && enums[f.Type.Name] {
		// Enum field: cast string to enum type
		argExpr = f.Type.Name + "(" + varName + ")"
	}
	fmt.Fprintf(b, "%sbuilder.%s(%s)\n", indent, setter, argExpr)
}

// generateRegisterFunc generates the RegisterHandlers function.
func generateRegisterFunc(b *strings.Builder, models []*ast.ModelDecl) {
	b.WriteString("// RegisterHandlers registers all CRUD handlers with the API router.\n")
	b.WriteString("func RegisterHandlers(router *api.Router, app *App) {\n")

	for _, m := range models {
		ops := crudOperations(m)
		name := m.Name
		for _, op := range ops {
			var apiName string
			switch op {
			case "get":
				apiName = "get" + name
			case "list":
				apiName = "list" + pluralize(name)
			case "create":
				apiName = "create" + name
			case "update":
				apiName = "update" + name
			case "delete":
				apiName = "delete" + name
			}
			fmt.Fprintf(b, "\trouter.Handle(%q, handle%s(app))\n", apiName, capitalize(apiName))
		}
	}

	b.WriteString("}\n")
}

// idGoType returns the Go type of the id field.
func idGoType(m *ast.ModelDecl) string {
	for _, f := range m.Fields {
		if f.Name == "id" && f.Type != nil {
			return resolveGoType(f.Type)
		}
	}
	return "int64"
}

// paramMethod returns the Request method name for a Go type.
func paramMethod(goType string) string {
	switch goType {
	case "int64":
		return "Int"
	case "string":
		return "String"
	case "bool":
		return "Bool"
	default:
		return "String"
	}
}

// pluralize adds "s" to a name (simple English pluralization).
func pluralize(name string) string {
	if strings.HasSuffix(name, "s") || strings.HasSuffix(name, "x") || strings.HasSuffix(name, "z") {
		return name + "es"
	}
	if strings.HasSuffix(name, "y") {
		return name[:len(name)-1] + "ies"
	}
	return name + "s"
}
