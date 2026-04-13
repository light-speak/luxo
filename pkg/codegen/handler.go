package codegen

import (
	"fmt"
	"os"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// crudOps defines the 6 CRUD operations.
var crudOps = []string{"get", "list", "create", "update", "delete", "deleteMany"}

// generateHandlerFile produces handler.gen.go containing CRUD handlers,
// inferred handlers (zero-body APIs), and RegisterHandlers.
func generateHandlerFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	var models []*ast.ModelDecl
	for _, file := range result.Files {
		for _, m := range file.Models {
			if hasCrud(m) {
				models = append(models, m)
			}
		}
	}

	modelMap, inferredAPIs := collectInferredAPIs(result)

	// Check if there are any compiled APIs (body != nil, not @native)
	hasCompiledAPIs := false
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body != nil && !hasDirective(api.Directives, "native") {
				hasCompiledAPIs = true
				break
			}
		}
		if hasCompiledAPIs {
			break
		}
	}

	if len(models) == 0 && len(inferredAPIs) == 0 && !hasCompiledAPIs {
		return nil
	}

	var b strings.Builder
	writeHeader(&b, packageName, "handler.gen.go")
	writeHandlerImports(&b, models)

	// Generate defaultCols for models with @hidden fields (excludes hidden from SELECT *)
	for _, m := range models {
		if hasHiddenFields(m) {
			generateDefaultCols(&b, m, enums)
		}
	}

	generateCRUDHandlers(&b, models, enums)
	inferredNames := generateInferredHandlers(&b, inferredAPIs, modelMap, enums)
	compiledNames := generateCompiledHandlers(&b, result, modelMap)

	// RegisterHandlers function (CRUD + inferred + compiled)
	allInferred := append(inferredNames, compiledNames...)
	generateRegisterFuncWithInferred(&b, models, allInferred)

	return []byte(b.String())
}

func generateCRUDHandlers(b *strings.Builder, models []*ast.ModelDecl, enums map[string]bool) {
	for _, m := range models {
		rels := analyzeRelations(m, enums)
		ops := crudOperations(m)
		for _, op := range ops {
			generateHandler(b, m, op, enums, rels)
		}
		generateFilterParser(b, m, enums)
		generateSorterParser(b, m)
		if len(rels) > 0 {
			generateRelationResolver(b, m, rels)
			generateListRelationResolver(b, m, rels)
		}
	}
}

func generateInferredHandlers(b *strings.Builder, apis []*ast.ApiDecl, modelMap map[string]*ast.ModelDecl, enums map[string]bool) []string {
	var names []string
	for _, api := range apis {
		inf := inferAPI(api.Name, modelMap)
		if inf != nil {
			if errMsg := ValidateInferredReturnType(api, inf); errMsg != "" {
				fmt.Fprintf(os.Stderr, "warning: %s:%d: %s\n", api.Pos.File, api.Pos.Line, errMsg)
			}
			generateInferredHandler(b, api, inf, enums, modelMap[inf.ModelName])
			names = append(names, api.Name)
		}
	}
	return names
}

func generateCompiledHandlers(b *strings.Builder, result *semantic.Result, modelMap map[string]*ast.ModelDecl) []string {
	var names []string
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body != nil && !hasDirective(api.Directives, "native") {
				compileAPIBody(b, api, modelMap)
				names = append(names, api.Name)
			}
		}
	}
	return names
}

// writeFKEnsure generates ensureField calls for relation key columns.
// BelongsTo needs the FK column (e.g., user_id); HasOne/HasMany need the local key (e.g., id).
func writeFKEnsure(b *strings.Builder, rels []Relation) {
	for _, rel := range rels {
		col := str.ToSnakeCase(rel.LocalKey)
		fmt.Fprintf(b, "\t\tcols = ensureField(cols, %q)\n", col)
	}
}

// writeHandlerImports writes handler.gen.go imports.
func writeHandlerImports(b *strings.Builder, models []*ast.ModelDecl) {
	hasHash := false
	for _, m := range models {
		for _, f := range m.Fields {
			if hasDirective(f.Directives, "hash") {
				hasHash = true
			}
		}
	}
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\t\"strings\"\n")
	b.WriteString("\n\t\"github.com/bytedance/sonic\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	if hasHash {
		b.WriteString("\tluxocrypto \"github.com/light-speak/luxo/pkg/lux/crypto\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/errors\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/selection\"\n")
	b.WriteString(")\n\n")
}

// collectInferredAPIs builds a model map and collects zero-body APIs.
func collectInferredAPIs(result *semantic.Result) (map[string]*ast.ModelDecl, []*ast.ApiDecl) {
	modelMap := make(map[string]*ast.ModelDecl)
	for _, file := range result.Files {
		for _, m := range file.Models {
			modelMap[m.Name] = m
		}
	}
	var apis []*ast.ApiDecl
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body == nil && !hasDirective(api.Directives, "native") {
				apis = append(apis, api)
			}
		}
	}
	return modelMap, apis
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
func generateHandler(b *strings.Builder, m *ast.ModelDecl, op string, enums map[string]bool, rels []Relation) {
	name := m.Name
	idType := idGoType(m)

	apiName := crudAPIName(name, op)
	hasRels := len(rels) > 0
	hidden := hasHiddenFields(m)

	// colsExpr: uses defaultCols when no $select for models with @hidden fields
	colsExpr := "selection.SQLColumns(req.Select)"
	if hidden {
		colsExpr = fmt.Sprintf("selection.SQLColumnsOr(req.Select, default%sCols)", name)
	}

	switch op {
	case "get":
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		writeParamID(b, idType, "\t\t")
		fmt.Fprintf(b, "\t\tcols := %s\n", colsExpr)
		writeFKEnsure(b, rels)
		fmt.Fprintf(b, "\t\tresult, err := app.%s.Where(%sWhere.Id.Eq(id)).Select(cols...).First(ctx)\n", name, name)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif result == nil {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q, ID: id})\n\t\t}\n", name)
		if hasRels {
			fmt.Fprintf(b, "\t\tif err := resolve%sRelations(ctx, app, result, req.Select); err != nil {\n", name)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		}
		fmt.Fprintf(b, "\t\tresult.WriteJSON(req.Buf, req.Select)\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")

	case "list":
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		fmt.Fprintf(b, "\t\tcols := %s\n", colsExpr)
		writeFKEnsure(b, rels)
		fmt.Fprintf(b, "\t\tconds := parse%sFilters(req.Filters)\n", name)
		// Note: @soft deleted_at filter is already in Client.Where(), not added here
		fmt.Fprintf(b, "\t\tq := app.%s.Where(conds...).Select(cols...)\n", name)
		fmt.Fprintf(b, "\t\tif sorts := parse%sSorters(req.Sorters); len(sorts) > 0 {\n", name)
		fmt.Fprintf(b, "\t\t\tq = q.OrderBy(sorts...)\n")
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t\tresults, total, err := q.Limit(req.PageSize).Offset((req.Page - 1) * req.PageSize).AllWithCount(ctx)\n")
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		if hasRels {
			fmt.Fprintf(b, "\t\tif err := resolve%sListRelations(ctx, app, results, req.Select); err != nil {\n", name)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		}
		// Write paginated response: {items: [...], total: N, page: N, pageSize: N}
		lower := str.LowerFirst(name)
		fmt.Fprintf(b, "\t\treq.Buf.AppendString(`{\"items\":`)\n")
		fmt.Fprintf(b, "\t\t%sListJSON(results).WriteJSON(req.Buf, req.Select)\n", lower)
		fmt.Fprintf(b, "\t\treq.Buf.AppendString(`,\"total\":`)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendInt(total)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendString(`,\"page\":`)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendInt(int64(req.Page))\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendString(`,\"pageSize\":`)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendInt(int64(req.PageSize))\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendByte('}')\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")

	case "create":
		generateCreateHandler(b, m, apiName, enums)

	case "update":
		generateUpdateHandler(b, m, apiName, idType, enums)

	case "delete":
		soft := isSoftDelete(m)
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		writeParamID(b, idType, "\t\t")
		if soft {
			fmt.Fprintf(b, "\t\tn, err := app.%s.SoftDelete(ctx, %sWhere.Id.Eq(id))\n", name, name)
		} else {
			fmt.Fprintf(b, "\t\tn, err := app.%s.Where(%sWhere.Id.Eq(id)).Delete(ctx)\n", name, name)
		}
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif n == 0 {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q, ID: id})\n\t\t}\n", name)
		fmt.Fprintf(b, "\t\treq.Buf.AppendString(`{\"deleted\":`)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendInt(n)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendByte('}')\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")

	case "deleteMany":
		soft := isSoftDelete(m)
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		fmt.Fprintf(b, "\t\tvar ids []%s\n", idType)
		fmt.Fprintf(b, "\t\tif err := sonic.Unmarshal(req.Params[\"ids\"], &ids); err != nil {\n")
		fmt.Fprintf(b, "\t\t\treturn fmt.Errorf(\"param ids: %%w\", err)\n")
		fmt.Fprintf(b, "\t\t}\n")
		if soft {
			fmt.Fprintf(b, "\t\tn, err := app.%s.SoftDelete(ctx, %sWhere.Id.In(ids...))\n", name, name)
		} else {
			fmt.Fprintf(b, "\t\tn, err := app.%s.Where(%sWhere.Id.In(ids...)).Delete(ctx)\n", name, name)
		}
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendString(`{\"deleted\":`)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendInt(n)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendByte('}')\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")
	}
}

// generateCreateHandler generates a create handler that reads params and calls Create().
func generateCreateHandler(b *strings.Builder, m *ast.ModelDecl, apiName string, enums map[string]bool) {
	name := m.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
	fmt.Fprintf(b, "\t\tbuilder := app.%s.Create()\n", name)

	for _, f := range m.Fields {
		if skipHandlerField(f, enums) {
			continue
		}
		setter := "Set" + str.Capitalize(f.Name)
		optional := (f.Type != nil && f.Type.Nullable) || f.Default != nil
		if optional {
			fmt.Fprintf(b, "\t\tif req.HasParam(%q) {\n", f.Name)
			generateParamSet(b, f, setter, "\t\t\t", enums)
			fmt.Fprintf(b, "\t\t}\n")
		} else {
			generateParamSet(b, f, setter, "\t\t", enums)
		}
	}

	fmt.Fprintf(b, "\t\tresult, err := builder.Exec(ctx)\n")
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	fmt.Fprintf(b, "\t\tresult.WriteJSON(req.Buf, req.Select)\n")
	fmt.Fprintf(b, "\t\treturn nil\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "}\n\n")
}

// generateUpdateHandler generates an update handler.
func generateUpdateHandler(b *strings.Builder, m *ast.ModelDecl, apiName, idType string, enums map[string]bool) {
	name := m.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
	writeParamID(b, idType, "\t\t")
	fmt.Fprintf(b, "\t\texisting, err := app.%s.Find(ctx, id)\n", name)
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	fmt.Fprintf(b, "\t\tif existing == nil {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q, ID: id})\n\t\t}\n", name)
	tableName := str.ToSnakeCase(name) + "s"
	fmt.Fprintf(b, "\t\tbuilder := new%sUpdateBuilder(app.%s.db, %q, id)\n", name, name, tableName)

	for _, f := range m.Fields {
		if skipHandlerField(f, enums) || f.Name == "id" || hasDirective(f.Directives, "immutable") {
			continue
		}
		setter := "Set" + str.Capitalize(f.Name)
		fmt.Fprintf(b, "\t\tif req.HasParam(%q) {\n", f.Name)
		generateParamSet(b, f, setter, "\t\t\t", enums)
		fmt.Fprintf(b, "\t\t}\n")
	}
	fmt.Fprintf(b, "\t\tresult, err := builder.Exec(ctx)\n")
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	fmt.Fprintf(b, "\t\tresult.WriteJSON(req.Buf, req.Select)\n")
	fmt.Fprintf(b, "\t\treturn nil\n")
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
	goType := resolveGoType(f.Type)

	// Enum fields are strings in JSON
	isEnum := f.Type != nil && enums[f.Type.Name]
	method := paramMethod(goType)
	if method == "" && isEnum {
		method = "String"
	}

	if method == "" {
		// Custom type — use ParamJSON
		fmt.Fprintf(b, "%svar %s %s\n", indent, varName, goType)
		fmt.Fprintf(b, "%sif err := req.ParamJSON(%q, &%s); err != nil {\n", indent, f.Name, varName)
		fmt.Fprintf(b, "%s\treturn fmt.Errorf(\"param %s: %%w\", err)\n", indent, f.Name)
		fmt.Fprintf(b, "%s}\n", indent)
	} else {
		fmt.Fprintf(b, "%s%s, err := req.Param%s(%q)\n", indent, varName, method, f.Name)
		fmt.Fprintf(b, "%sif err != nil {\n", indent)
		fmt.Fprintf(b, "%s\treturn fmt.Errorf(\"param %s: %%w\", err)\n", indent, f.Name)
		fmt.Fprintf(b, "%s}\n", indent)
	}

	// @hash: auto-hash password before save
	if hasDirective(f.Directives, "hash") {
		fmt.Fprintf(b, "%s%s, err = luxocrypto.HashPassword(%s)\n", indent, varName, varName)
		fmt.Fprintf(b, "%sif err != nil {\n", indent)
		fmt.Fprintf(b, "%s\treturn fmt.Errorf(\"hash %s: %%w\", err)\n", indent, f.Name)
		fmt.Fprintf(b, "%s}\n", indent)
	}

	argExpr := varName
	if f.Type != nil && f.Type.Nullable && isEnum {
		// Nullable enum: cast to enum type, then take pointer
		tmpVar := varName + "Enum"
		fmt.Fprintf(b, "%s%s := %s(%s)\n", indent, tmpVar, f.Type.Name, varName)
		argExpr = "&" + tmpVar
	} else if f.Type != nil && f.Type.Nullable {
		argExpr = "&" + varName
	} else if isEnum {
		argExpr = f.Type.Name + "(" + varName + ")"
	}
	fmt.Fprintf(b, "%sbuilder.%s(%s)\n", indent, setter, argExpr)
}

// hasHiddenFields checks if a model has any @hidden fields.
func hasHiddenFields(m *ast.ModelDecl) bool {
	for _, f := range m.Fields {
		if hasDirective(f.Directives, "hidden") {
			return true
		}
	}
	return false
}

// generateDefaultCols generates a defaultCols variable that excludes @hidden fields.
// Used when no $select is provided — prevents querying hidden columns.
func generateDefaultCols(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	fmt.Fprintf(b, "// default%sCols excludes @hidden fields from SELECT *.\n", m.Name)
	fmt.Fprintf(b, "var default%sCols = []string{", m.Name)
	first := true
	for _, f := range m.Fields {
		if f.Computed != nil || isRelationField(f, enums) || hasDirective(f.Directives, "hidden") {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", str.ToSnakeCase(f.Name))
		first = false
	}
	b.WriteString("}\n\n")
}

// generateRelationResolver generates a resolve<Model>Relations function
// that loads relation fields via DataLoader based on $select.
func generateRelationResolver(b *strings.Builder, m *ast.ModelDecl, rels []Relation) {
	name := m.Name
	lower := strings.ToLower(name[:1]) + name[1:]

	fmt.Fprintf(b, "// resolve%sRelations loads relation fields for %s based on $select.\n", name, name)
	fmt.Fprintf(b, "func resolve%sRelations(ctx context.Context, app *App, %s *%s, fields []*selection.Field) error {\n", name, lower, name)
	fmt.Fprintf(b, "\tif %s == nil {\n\t\treturn nil\n\t}\n", lower)

	for _, rel := range rels {
		fieldName := rel.FieldName
		loaderField := loaderFieldName(name, rel)
		localKey := rel.LocalKey
		goLocalKey := str.Capitalize(localKey)
		goFieldName := str.Capitalize(fieldName)

		fmt.Fprintf(b, "\tfor _, f := range fields {\n")
		fmt.Fprintf(b, "\t\tif f.Name == %q && f.Children != nil {\n", fieldName)
		fmt.Fprintf(b, "\t\t\tchildCols := selection.SQLColumns(f.Children)\n")
		if rel.FKNullable {
			fmt.Fprintf(b, "\t\t\tif %s.%s != nil {\n", lower, goLocalKey)
			fmt.Fprintf(b, "\t\t\t\tresult, err := app.loaders.%s.Load(ctx, *%s.%s, childCols)\n",
				loaderField, lower, goLocalKey)
			fmt.Fprintf(b, "\t\t\t\tif err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\t\t%s.%s = result\n", lower, goFieldName)
			fmt.Fprintf(b, "\t\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\t\tresult, err := app.loaders.%s.Load(ctx, %s.%s, childCols)\n",
				loaderField, lower, goLocalKey)
			fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\t%s.%s = result\n", lower, goFieldName)
		}

		fmt.Fprintf(b, "\t\t\tbreak\n")
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t}\n")
	}

	fmt.Fprintf(b, "\treturn nil\n")
	fmt.Fprintf(b, "}\n\n")
}

// generateListRelationResolver generates a batch resolve function for LIST handlers.
// Uses LoadAll (direct dispatch, zero wait) instead of per-item Load.
func generateListRelationResolver(b *strings.Builder, m *ast.ModelDecl, rels []Relation) {
	name := m.Name
	lower := strings.ToLower(name[:1]) + name[1:]
	_ = lower

	fmt.Fprintf(b, "// resolve%sListRelations batch-loads all relation fields for a list of %s.\n", name, name)
	fmt.Fprintf(b, "// Uses LoadAll — direct dispatch, zero wait.\n")
	fmt.Fprintf(b, "func resolve%sListRelations(ctx context.Context, app *App, items []*%s, fields []*selection.Field) error {\n", name, name)

	for _, rel := range rels {
		fieldName := rel.FieldName
		loaderField := loaderFieldName(name, rel)
		localKey := rel.LocalKey
		goLocalKey := str.Capitalize(localKey)
		goFieldName := str.Capitalize(fieldName)

		fmt.Fprintf(b, "\tfor _, f := range fields {\n")
		fmt.Fprintf(b, "\t\tif f.Name == %q && f.Children != nil {\n", fieldName)
		fmt.Fprintf(b, "\t\t\tchildCols := selection.SQLColumns(f.Children)\n")

		// Collect all keys
		fmt.Fprintf(b, "\t\t\tkeys := make([]%s, 0, len(items))\n", rel.KeyGoType)
		if rel.FKNullable {
			fmt.Fprintf(b, "\t\t\tfor _, item := range items {\n")
			fmt.Fprintf(b, "\t\t\t\tif item.%s != nil {\n", goLocalKey)
			fmt.Fprintf(b, "\t\t\t\t\tkeys = append(keys, *item.%s)\n", goLocalKey)
			fmt.Fprintf(b, "\t\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\t\tfor _, item := range items {\n")
			fmt.Fprintf(b, "\t\t\t\tkeys = append(keys, item.%s)\n", goLocalKey)
			fmt.Fprintf(b, "\t\t\t}\n")
		}

		// LoadAll — direct dispatch, zero wait
		if rel.IsList {
			fmt.Fprintf(b, "\t\t\tresultMap, err := app.loaders.%s.LoadAll(ctx, keys, childCols)\n", loaderField)
		} else {
			fmt.Fprintf(b, "\t\t\tresultMap, err := app.loaders.%s.LoadAll(ctx, keys, childCols)\n", loaderField)
		}
		fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n")

		// Map results back
		if rel.FKNullable {
			fmt.Fprintf(b, "\t\t\tfor _, item := range items {\n")
			fmt.Fprintf(b, "\t\t\t\tif item.%s != nil {\n", goLocalKey)
			fmt.Fprintf(b, "\t\t\t\t\titem.%s = resultMap[*item.%s]\n", goFieldName, goLocalKey)
			fmt.Fprintf(b, "\t\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\t\tfor _, item := range items {\n")
			fmt.Fprintf(b, "\t\t\t\titem.%s = resultMap[item.%s]\n", goFieldName, goLocalKey)
			fmt.Fprintf(b, "\t\t\t}\n")
		}

		fmt.Fprintf(b, "\t\t\tbreak\n")
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t}\n")
	}

	fmt.Fprintf(b, "\treturn nil\n")
	fmt.Fprintf(b, "}\n\n")
}

// crudAPIName returns the API endpoint name for a CRUD operation.
func crudAPIName(modelName, op string) string {
	switch op {
	case "list":
		return "list" + pluralize(modelName)
	case "deleteMany":
		return "delete" + pluralize(modelName)
	default:
		return op + modelName
	}
}

// generateRegisterFuncWithInferred generates RegisterHandlers with CRUD + inferred handlers.
func generateRegisterFuncWithInferred(b *strings.Builder, models []*ast.ModelDecl, inferredNames []string) {
	b.WriteString("// RegisterHandlers registers all API handlers with the router.\n")
	b.WriteString("func RegisterHandlers(router *api.Router, app *App) {\n")

	for _, m := range models {
		for _, op := range crudOperations(m) {
			name := crudAPIName(m.Name, op)
			fmt.Fprintf(b, "\trouter.Handle(%q, handle%s(app))\n", name, str.Capitalize(name))
		}
	}

	for _, name := range inferredNames {
		fmt.Fprintf(b, "\trouter.Handle(%q, handle%s(app))\n", name, str.Capitalize(name))
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

// writeParamID generates id parameter extraction code for CRUD handlers.
func writeParamID(b *strings.Builder, idType, indent string) {
	method := paramMethod(idType)
	if method != "" {
		fmt.Fprintf(b, "%sid, err := req.Param%s(\"id\")\n", indent, method)
		fmt.Fprintf(b, "%sif err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
	} else {
		fmt.Fprintf(b, "%svar id %s\n", indent, idType)
		fmt.Fprintf(b, "%sif err := req.ParamJSON(\"id\", &id); err != nil {\n", indent)
		fmt.Fprintf(b, "%s\treturn err\n%s}\n", indent, indent)
	}
}

// paramMethod returns the Request method name for a Go type.
// Returns "" for types that require ParamJSON (custom structs, nested arrays, etc.).
func paramMethod(goType string) string {
	switch goType {
	case "int64":
		return "Int"
	case "float64":
		return "Float"
	case "string":
		return "String"
	case "time.Time":
		return "DateTime"
	case "bool":
		return "Bool"
	case "[]int64":
		return "IntArray"
	case "[]string":
		return "StringArray"
	default:
		return "" // custom type — use ParamJSON
	}
}

// generateFilterParser generates a parse{Model}Filters function that converts
// api.Filter to lux.Condition, only allowing @filterable fields.
func generateFilterParser(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	fmt.Fprintf(b, "// parse%sFilters converts request filters to SQL conditions.\n", name)
	fmt.Fprintf(b, "// Only @filterable fields are allowed.\n")
	fmt.Fprintf(b, "func parse%sFilters(filters []api.Filter) []lux.Condition {\n", name)
	fmt.Fprintf(b, "\tvar conds []lux.Condition\n")
	fmt.Fprintf(b, "\tfor _, f := range filters {\n")
	fmt.Fprintf(b, "\t\tswitch f.Field {\n")

	for _, f := range m.Fields {
		if !hasDirective(f.Directives, "filterable") || f.Type == nil || f.Computed != nil {
			continue
		}
		if isRelationField(f, enums) {
			continue
		}
		col := str.ToSnakeCase(f.Name)
		fmt.Fprintf(b, "\t\tcase %q:\n", f.Name)

		switch f.Type.Name {
		case "Int":
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewIntField(%q).FilterOp(f.Operator, f.Value))\n", col)
		case "Float":
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewFloatField(%q).FilterOp(f.Operator, f.Value))\n", col)
		case "String":
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewStringField(%q).FilterOp(f.Operator, f.Value))\n", col)
		case "Boolean":
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewBoolField(%q).FilterOp(f.Operator, f.Value))\n", col)
		case "DateTime":
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewTimeField(%q).FilterOp(f.Operator, f.Value))\n", col)
		default:
			// Enum or other types — treat as string
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewStringField(%q).FilterOp(f.Operator, f.Value))\n", col)
		}
	}

	fmt.Fprintf(b, "\t\t}\n") // end switch
	fmt.Fprintf(b, "\t}\n")   // end for
	fmt.Fprintf(b, "\treturn conds\n")
	fmt.Fprintf(b, "}\n\n")
}

// generateSorterParser generates a parse{Model}Sorters function that converts
// api.Sorter to ORDER BY clauses, only allowing @sortable fields.
func generateSorterParser(b *strings.Builder, m *ast.ModelDecl) {
	name := m.Name
	fmt.Fprintf(b, "// parse%sSorters converts request sorters to ORDER BY clauses.\n", name)
	fmt.Fprintf(b, "// Only @sortable fields are allowed.\n")
	fmt.Fprintf(b, "func parse%sSorters(sorters []api.Sorter) []string {\n", name)
	fmt.Fprintf(b, "\tvar clauses []string\n")
	fmt.Fprintf(b, "\tfor _, s := range sorters {\n")
	fmt.Fprintf(b, "\t\tvar col string\n")
	fmt.Fprintf(b, "\t\tswitch s.Field {\n")

	for _, f := range m.Fields {
		if !hasDirective(f.Directives, "sortable") || f.Type == nil || f.Computed != nil {
			continue
		}
		col := str.ToSnakeCase(f.Name)
		fmt.Fprintf(b, "\t\tcase %q:\n", f.Name)
		fmt.Fprintf(b, "\t\t\tcol = %q\n", col)
	}

	fmt.Fprintf(b, "\t\tdefault:\n")
	fmt.Fprintf(b, "\t\t\tcontinue\n")
	fmt.Fprintf(b, "\t\t}\n") // end switch
	fmt.Fprintf(b, "\t\tif strings.EqualFold(s.Order, \"desc\") {\n")
	fmt.Fprintf(b, "\t\t\tclauses = append(clauses, col+\" DESC\")\n")
	fmt.Fprintf(b, "\t\t} else {\n")
	fmt.Fprintf(b, "\t\t\tclauses = append(clauses, col+\" ASC\")\n")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t}\n") // end for
	fmt.Fprintf(b, "\treturn clauses\n")
	fmt.Fprintf(b, "}\n\n")
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
