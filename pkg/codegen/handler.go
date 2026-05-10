package codegen

import (
	"fmt"
	"os"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// crudOps defines the 6 CRUD operations.
var crudOps = []string{"get", "list", "create", "update", "delete", "deleteMany"}

// handlerFeatures holds feature detection flags for handler imports.
type handlerFeatures struct {
	hasOrGroups       bool
	hasSortable       bool
	hasAwait          bool
	hasTransaction    bool
	hasTemplateStr    bool
	hasAuth           bool
	hasCrypto         bool
	hasTimeFunc       bool
	hasEmit           bool
	crossEventImports map[string]string // module → alias for cross-module emit
}

// detectHandlerFeatures scans models and APIs to determine which imports are needed.
func detectHandlerFeatures(result *semantic.Result, models []*ast.ModelDecl, inferredAPIs []*ast.ApiDecl, modelMap map[string]*ast.ModelDecl) handlerFeatures {
	var f handlerFeatures

	// Check if any inferred API has OR groups (need strconv import)
	for _, api := range inferredAPIs {
		inf := InferAPI(api.Name, modelMap)
		if inf != nil && len(inf.Groups) > 1 {
			f.hasOrGroups = true
			break
		}
	}

	// Check if any CRUD model has sortable fields (need "strings" import)
	for _, m := range models {
		for _, fd := range m.Fields {
			if hasDirective(fd.Directives, "sortable") && fd.Type != nil && fd.Computed == nil {
				f.hasSortable = true
				break
			}
		}
		if f.hasSortable {
			break
		}
	}

	// Check compiled APIs for await, transaction, and template strings
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body != nil && !hasDirective(api.Directives, "native") {
				if bodyContainsAwait(api.Body) {
					f.hasAwait = true
				}
				if bodyContainsTransaction(api.Body) {
					f.hasTransaction = true
				}
			}
			if api.Body != nil && bodyContainsTemplateString(api.Body) {
				f.hasTemplateStr = true
			}
		}
	}

	f.hasAuth = detectAuthNeeded(result, models)

	// Scan compiled API bodies for crypto and time function usage
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body == nil {
				continue
			}
			scanBodyForBuiltins(api.Body, &f)
		}
	}

	return f
}

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

	// Check if there are any compiled APIs or service fns
	hasCompiledAPIs := false
	hasServiceFns := false
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body != nil && !hasDirective(api.Directives, "native") {
				hasCompiledAPIs = true
			}
		}
		for _, fn := range file.Functions {
			if hasDirective(fn.Directives, "service") {
				hasServiceFns = true
			}
		}
	}

	if len(models) == 0 && len(inferredAPIs) == 0 && !hasCompiledAPIs && !hasServiceFns {
		return nil
	}

	features := detectHandlerFeatures(result, models, inferredAPIs, modelMap)

	var b strings.Builder
	writeHeader(&b, packageName, "handler.gen.go")

	writeHandlerImports(&b, result, models, features)

	// Generate defaultCols for models with @hidden fields (excludes hidden from SELECT *)
	for _, m := range models {
		if hasHiddenFields(m) {
			generateDefaultCols(&b, m, enums)
		}
	}

	compiledNames := generateCompiledHandlers(&b, result, modelMap)
	inferredNames := generateInferredHandlers(&b, inferredAPIs, modelMap, enums)

	// Build set of compiled/inferred names to avoid generating duplicate CRUD handlers
	compiledSet := make(map[string]bool, len(compiledNames)+len(inferredNames))
	for _, n := range compiledNames {
		compiledSet[n] = true
	}
	for _, n := range inferredNames {
		compiledSet[n] = true
	}
	generateCRUDHandlers(&b, models, enums, compiledSet)

	// Collect API directives for middleware wrapping
	apiDirectives := collectAPIDirectives(result)

	// RegisterHandlers function (CRUD + inferred + compiled)
	allInferred := append(inferredNames, compiledNames...)
	generateRegisterFuncWithInferred(&b, models, allInferred, apiDirectives)

	// fn @service handlers
	serviceNames := generateServiceFnHandlers(&b, result, modelMap)
	generateRegisterServiceFns(&b, serviceNames)

	// DataLoader RPC endpoints — batch load for each model (cluster mode)
	generateBatchLoadHandlers(&b, models)

	return []byte(b.String())
}

func generateCRUDHandlers(b *strings.Builder, models []*ast.ModelDecl, enums map[string]bool, skipNames map[string]bool) {
	// Collect all model relations for recursive resolve support
	modelRels := make(map[string]bool)
	for _, m := range models {
		rels := analyzeRelations(m, enums)
		if len(rels) > 0 {
			modelRels[m.Name] = true
		}
	}

	// Generate MaxRelationDepth constant
	b.WriteString("// MaxRelationDepth limits recursive relation resolution to prevent infinite loops.\n")
	b.WriteString("// Override via RELATION_MAX_DEPTH env var.\n")
	b.WriteString("var MaxRelationDepth = 5\n\n")

	for _, m := range models {
		rels := analyzeRelations(m, enums)
		ops := crudOperations(m)
		for _, op := range ops {
			// Skip if a compiled/inferred API with the same name exists
			if skipNames[crudAPIName(m.Name, op)] {
				continue
			}
			generateHandler(b, m, op, enums, rels)
		}
		generateFilterParser(b, m, enums)
		generateSorterParser(b, m)
		if len(rels) > 0 {
			generateRelationResolver(b, m, rels, modelRels)
			generateListRelationResolver(b, m, rels, modelRels)
		}
	}
}

func generateInferredHandlers(b *strings.Builder, apis []*ast.ApiDecl, modelMap map[string]*ast.ModelDecl, enums map[string]bool) []string {
	var names []string
	for _, api := range apis {
		inf := InferAPI(api.Name, modelMap)
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
	enumSet := CollectEnumsFromResult(result)
	var names []string
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body != nil && !hasDirective(api.Directives, "native") {
				compileAPIBody(b, api, modelMap, enumSet)
				names = append(names, api.Name)
			}
		}
	}
	return names
}

// generateServiceFnHandlers compiles fn @service bodies to Go handlers.
// Handles both compiled fn (with body) and @native fn (delegating to NativeResolver).
// Returns service fn names for RegisterServiceFns generation.
func generateServiceFnHandlers(b *strings.Builder, result *semantic.Result, modelMap map[string]*ast.ModelDecl) []string {
	enumSet := CollectEnumsFromResult(result)
	var names []string
	for _, file := range result.Files {
		for _, fn := range file.Functions {
			if !hasDirective(fn.Directives, "service") {
				continue
			}
			if hasDirective(fn.Directives, "native") {
				// @native @service — delegate to NativeResolver
				generateNativeServiceHandler(b, fn)
			} else if fn.Body != nil {
				// Compiled fn @service
				compileFnBody(b, fn, modelMap, enumSet)
			}
			names = append(names, fn.Name)
		}
	}
	return names
}

// generateNativeServiceHandler generates a handler that delegates to NativeResolver.
func generateNativeServiceHandler(b *strings.Builder, fn *ast.FnDecl) {
	name := fn.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(name))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")

	// Parse params
	var paramNames []string
	for _, p := range fn.Params {
		goType := resolveGoType(p.Type)
		method := paramMethod(goType)
		if method == "" {
			fmt.Fprintf(b, "\t\tvar %s %s\n", p.Name, goType)
			fmt.Fprintf(b, "\t\tif err := req.ParamJSON(%q, &%s); err != nil {\n", p.Name, p.Name)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\t%s, err := req.Param%s(%q)\n", p.Name, method, p.Name)
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		}
		paramNames = append(paramNames, p.Name)
	}

	// Call NativeResolver
	fmt.Fprintf(b, "\t\tresult, err := app.Resolver.%s(ctx", str.Capitalize(name))
	for _, pn := range paramNames {
		fmt.Fprintf(b, ", %s", pn)
	}
	fmt.Fprintf(b, ")\n")
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")

	// Write response — always binary (Luvia converts to JSON if needed)
	if fn.ReturnType != nil {
		goType := resolveGoType(fn.ReturnType)
		switch goType {
		case "int64":
			fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, result)\n")
		case "float64":
			fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendFixed64(req.Buf.B, result)\n")
		case "string":
			fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendString(req.Buf.B, result)\n")
		case "bool":
			fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendBool(req.Buf.B, result)\n")
		default:
			fmt.Fprintf(b, "\t\tresult.WriteLuxo(req.Buf, req.FieldMask)\n")
		}
	}
	fmt.Fprintf(b, "\t\treturn nil\n")
	fmt.Fprintf(b, "\t}\n}\n\n")
}

// generateRegisterServiceFns generates RegisterServiceFns with svc: prefix.
func generateRegisterServiceFns(b *strings.Builder, serviceNames []string) {
	if len(serviceNames) == 0 {
		return
	}
	b.WriteString("// RegisterServiceFns registers fn @service handlers with the router.\n")
	b.WriteString("// Service fns use svc: prefix to distinguish from API handlers.\n")
	b.WriteString("func RegisterServiceFns(router *api.Router, app *App) {\n")

	for _, name := range serviceNames {
		svcName := "svc:" + name
		fmt.Fprintf(b, "\trouter.Handle(%q, handle%s(app))\n", svcName, str.Capitalize(name))
		writeAPIRegistration(b, svcName)
	}

	b.WriteString("}\n\n")
}

// writeFKEnsure generates ensureField calls for relation key columns.
// BelongsTo needs the FK column (e.g., user_id); HasOne/HasMany need the local key (e.g., id).
func writeFKEnsure(b *strings.Builder, rels []Relation) {
	seen := make(map[string]bool)
	for _, rel := range rels {
		col := str.ToSnakeCase(rel.LocalKey)
		if seen[col] {
			continue
		}
		seen[col] = true
		fmt.Fprintf(b, "\t\tcols = ensureField(cols, %q)\n", col)
	}
}

// writeHandlerImports writes handler.gen.go imports.
func writeHandlerImports(b *strings.Builder, result *semantic.Result, models []*ast.ModelDecl, feat handlerFeatures) {
	hasOrGroups := feat.hasOrGroups
	hasSortable := feat.hasSortable
	hasAwait := feat.hasAwait
	hasTransaction := feat.hasTransaction
	hasTemplateStr := feat.hasTemplateStr
	hasAuth := feat.hasAuth
	hasHash := scanModelsForHash(models)
	hasTime := scanForTimeImport(result, models)
	needsJSON := scanModelsForJSON(models)
	hasValidation, hasPattern := scanModelsForValidation(models)

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"fmt\"\n")
	if hasOrGroups || hasTemplateStr {
		b.WriteString("\t\"strconv\"\n")
	}
	if hasSortable || hasTemplateStr || hasValidation {
		b.WriteString("\t\"strings\"\n")
	}
	if hasPattern {
		b.WriteString("\t\"regexp\"\n")
	}
	if hasTime || feat.hasTimeFunc {
		b.WriteString("\t\"time\"\n")
	}
	// crypto.randomHex uses luxocrypto import (covered by hasHash || hasCrypto check)
	if needsJSON {
		b.WriteString("\n\t\"encoding/json\"\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	if hasHash || feat.hasCrypto {
		b.WriteString("\tluxocrypto \"github.com/light-speak/luxo/pkg/lux/crypto\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/errors\"\n")
	if hasAuth {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/luvia\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/selection\"\n")
	// Cross-module event imports for emit statements
	for modName, alias := range feat.crossEventImports {
		if globalEventCtx != nil {
			fmt.Fprintf(b, "\t%s \"%s/%s/luxo\"\n", alias, globalEventCtx.ModulePath, modName)
		}
	}
	if hasAwait {
		b.WriteString("\t\"golang.org/x/sync/errgroup\"\n")
	}
	// pg import: batchLoad handlers need pg.QueryRows, transaction needs pg driver
	if len(models) > 0 {
		fmt.Fprintf(b, "\tpg %q\n", DriverPG.DriverImport())
	} else if hasTransaction {
		fmt.Fprintf(b, "\t%q\n", DriverPG.DriverImport())
	}
	b.WriteString(")\n\n")
}

// collectInferredAPIs builds a model map and collects zero-body APIs.
func collectInferredAPIs(result *semantic.Result) (map[string]*ast.ModelDecl, []*ast.ApiDecl) {
	modelMap := make(map[string]*ast.ModelDecl)
	for _, file := range result.Files {
		for _, m := range file.Models {
			modelMap[m.Name] = m
		}
		// Include extend stubs so compiled APIs can reference cross-module models
		for _, ext := range file.Extends {
			if _, exists := modelMap[ext.Name]; !exists {
				modelMap[ext.Name] = &ast.ModelDecl{
					Name:   ext.Name,
					Fields: ext.Fields,
				}
			}
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

	// @withAuth on model → all CRUD handlers require auth
	needAuth := hasDirective(m.Directives, "withAuth")

	switch op {
	case "get":
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		if needAuth {
			writeAuthCheck(b, "\t\t")
		}
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
		generateAggregateFields(b, m, "result", "\t\t")
		fmt.Fprintf(b, "\t\tresult.WriteLuxo(req.Buf, req.FieldMask)\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")

	case "list":
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		if needAuth {
			writeAuthCheck(b, "\t\t")
		}
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
		// Write paginated response — columnar encoding for lists
		fmt.Fprintf(b, "\t\tWriteColumnar%s(req.Buf, results, req.FieldMask)\n", name)
		// Append pagination metadata after columnar data
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, total)\n")
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.Page))\n")
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.PageSize))\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")

	case "create":
		generateCreateHandler(b, m, apiName, enums, needAuth)

	case "update":
		generateUpdateHandler(b, m, apiName, idType, enums, needAuth)

	case "delete":
		soft := isSoftDelete(m)
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		if needAuth {
			writeAuthCheck(b, "\t\t")
		}
		writeParamID(b, idType, "\t\t")
		if soft {
			fmt.Fprintf(b, "\t\tn, err := app.%s.SoftDelete(ctx, %sWhere.Id.Eq(id))\n", name, name)
		} else {
			fmt.Fprintf(b, "\t\tn, err := app.%s.Where(%sWhere.Id.Eq(id)).Delete(ctx)\n", name, name)
		}
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif n == 0 {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q, ID: id})\n\t\t}\n", name)
		fmt.Fprintf(b, "\t\tapi.InvalidateCache(%q)\n", name)
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, n)\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")

	case "deleteMany":
		soft := isSoftDelete(m)
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		if needAuth {
			writeAuthCheck(b, "\t\t")
		}
		switch idType {
		case "int64":
			fmt.Fprintf(b, "\t\tids, err := req.ParamIntArray(\"ids\")\n")
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		case "string":
			fmt.Fprintf(b, "\t\tids, err := req.ParamStringArray(\"ids\")\n")
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		default:
			fmt.Fprintf(b, "\t\tvar ids []%s\n", idType)
			fmt.Fprintf(b, "\t\tif err := json.Unmarshal(req.Params[\"ids\"], &ids); err != nil {\n")
			fmt.Fprintf(b, "\t\t\treturn fmt.Errorf(\"param ids: %%w\", err)\n")
			fmt.Fprintf(b, "\t\t}\n")
		}
		if soft {
			fmt.Fprintf(b, "\t\tn, err := app.%s.SoftDelete(ctx, %sWhere.Id.In(ids...))\n", name, name)
		} else {
			fmt.Fprintf(b, "\t\tn, err := app.%s.Where(%sWhere.Id.In(ids...)).Delete(ctx)\n", name, name)
		}
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif n > 0 {\n")
		fmt.Fprintf(b, "\t\t\tapi.InvalidateCache(%q)\n", name)
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, n)\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")
	}
}

// generateCreateHandler generates a create handler that reads params and calls Create().
func generateCreateHandler(b *strings.Builder, m *ast.ModelDecl, apiName string, enums map[string]bool, needAuth bool) {
	name := m.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
	if needAuth {
		writeAuthCheck(b, "\t\t")
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
			generateParamSet(b, f, setter, "\t\t\t", enums)
			fmt.Fprintf(b, "\t\t}\n")
		} else {
			generateParamSet(b, f, setter, "\t\t", enums)
		}
	}

	// @beforeSave hooks — run before persisting
	for _, f := range m.Fields {
		for _, d := range f.Directives {
			if d.Name == "beforeSave" && d.Body != nil {
				fmt.Fprintf(b, "\t\t// @beforeSave on %s\n", f.Name)
				for _, stmt := range d.Body.Stmts {
					_ = stmt // TODO: compile beforeSave body when expression compiler supports field context
				}
			}
		}
	}

	fmt.Fprintf(b, "\t\tresult, err := builder.Exec(ctx)\n")
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	fmt.Fprintf(b, "\t\tapi.InvalidateCache(%q)\n", m.Name)
	fmt.Fprintf(b, "\t\tresult.WriteLuxo(req.Buf, req.FieldMask)\n")
	fmt.Fprintf(b, "\t\treturn nil\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "}\n\n")
}

// generateUpdateHandler generates an update handler.
func generateUpdateHandler(b *strings.Builder, m *ast.ModelDecl, apiName, idType string, enums map[string]bool, needAuth bool) {
	name := m.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
	if needAuth {
		writeAuthCheck(b, "\t\t")
	}
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
	fmt.Fprintf(b, "\t\tapi.InvalidateCache(%q)\n", m.Name)
	fmt.Fprintf(b, "\t\tresult.WriteLuxo(req.Buf, req.FieldMask)\n")
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

	// For nullable fields, use the base type for param extraction, then take pointer
	baseType := f.Type
	if f.Type != nil && f.Type.Nullable {
		baseType = &ast.TypeRef{Name: f.Type.Name} // strip nullable
	}
	goType := resolveGoType(baseType)

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
func generateRelationResolver(b *strings.Builder, m *ast.ModelDecl, rels []Relation, modelRels map[string]bool) {
	name := m.Name
	lower := strings.ToLower(name[:1]) + name[1:]

	fmt.Fprintf(b, "// resolve%sRelations loads relation fields for %s based on $select.\n", name, name)
	fmt.Fprintf(b, "// depth limits recursive resolution to prevent infinite loops.\n")
	fmt.Fprintf(b, "func resolve%sRelations(ctx context.Context, app *App, %s *%s, fields []*selection.Field, depth ...int) error {\n", name, lower, name)
	fmt.Fprintf(b, "\tif %s == nil {\n\t\treturn nil\n\t}\n", lower)
	fmt.Fprintf(b, "\td := MaxRelationDepth\n")
	fmt.Fprintf(b, "\tif len(depth) > 0 {\n\t\td = depth[0]\n\t}\n")
	fmt.Fprintf(b, "\tif d <= 0 {\n\t\treturn nil\n\t}\n")

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
			// Recursive resolve for child's relations
			if modelRels[rel.TargetName] {
				fmt.Fprintf(b, "\t\t\t\tif err := resolve%sRelations(ctx, app, result, f.Children, d-1); err != nil {\n", rel.TargetName)
				fmt.Fprintf(b, "\t\t\t\t\treturn err\n\t\t\t\t}\n")
			}
			fmt.Fprintf(b, "\t\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\t\tresult, err := app.loaders.%s.Load(ctx, %s.%s, childCols)\n",
				loaderField, lower, goLocalKey)
			fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\t%s.%s = result\n", lower, goFieldName)
			// Recursive resolve for child's relations
			if modelRels[rel.TargetName] {
				if rel.IsList {
					fmt.Fprintf(b, "\t\t\tif err := resolve%sListRelations(ctx, app, %s.%s, f.Children, d-1); err != nil {\n", rel.TargetName, lower, goFieldName)
				} else {
					fmt.Fprintf(b, "\t\t\tif err := resolve%sRelations(ctx, app, result, f.Children, d-1); err != nil {\n", rel.TargetName)
				}
				fmt.Fprintf(b, "\t\t\t\treturn err\n\t\t\t}\n")
			}
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
func generateListRelationResolver(b *strings.Builder, m *ast.ModelDecl, rels []Relation, modelRels map[string]bool) {
	name := m.Name

	fmt.Fprintf(b, "// resolve%sListRelations batch-loads all relation fields for a list of %s.\n", name, name)
	fmt.Fprintf(b, "// Uses LoadAll — direct dispatch, zero wait.\n")
	fmt.Fprintf(b, "func resolve%sListRelations(ctx context.Context, app *App, items []*%s, fields []*selection.Field, depth ...int) error {\n", name, name)
	fmt.Fprintf(b, "\td := MaxRelationDepth\n")
	fmt.Fprintf(b, "\tif len(depth) > 0 {\n\t\td = depth[0]\n\t}\n")
	fmt.Fprintf(b, "\tif d <= 0 {\n\t\treturn nil\n\t}\n")

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
		fmt.Fprintf(b, "\t\t\tresultMap, err := app.loaders.%s.LoadAll(ctx, keys, childCols)\n", loaderField)
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

		// Recursive resolve for child's relations
		if modelRels[rel.TargetName] {
			if rel.IsList {
				fmt.Fprintf(b, "\t\t\tfor _, item := range items {\n")
				fmt.Fprintf(b, "\t\t\t\tif err := resolve%sListRelations(ctx, app, item.%s, f.Children, d-1); err != nil {\n", rel.TargetName, goFieldName)
				fmt.Fprintf(b, "\t\t\t\t\treturn err\n\t\t\t\t}\n")
				fmt.Fprintf(b, "\t\t\t}\n")
			} else {
				fmt.Fprintf(b, "\t\t\tfor _, item := range items {\n")
				fmt.Fprintf(b, "\t\t\t\tif err := resolve%sRelations(ctx, app, item.%s, f.Children, d-1); err != nil {\n", rel.TargetName, goFieldName)
				fmt.Fprintf(b, "\t\t\t\t\treturn err\n\t\t\t\t}\n")
				fmt.Fprintf(b, "\t\t\t}\n")
			}
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
// Also registers API IDs and param metadata for binary protocol routing.
// Wraps handlers with @cache/@rateLimit middleware when present.
func generateRegisterFuncWithInferred(b *strings.Builder, models []*ast.ModelDecl, inferredNames []string, apiDirs map[string][]*ast.Directive) {
	b.WriteString("// RegisterHandlers registers all API handlers with the router.\n")
	b.WriteString("func RegisterHandlers(router *api.Router, app *App) {\n")

	// Build set of compiled/inferred names to skip duplicate CRUD handlers
	compiledSet := make(map[string]bool, len(inferredNames))
	for _, name := range inferredNames {
		compiledSet[name] = true
	}

	for _, m := range models {
		for _, op := range crudOperations(m) {
			name := crudAPIName(m.Name, op)
			// Skip CRUD handler if a compiled API with the same name exists
			if compiledSet[name] {
				continue
			}
			writeHandlerRegistration(b, name, apiDirs[name])
			writeAPIRegistration(b, name)
		}
	}

	for _, name := range inferredNames {
		writeHandlerRegistration(b, name, apiDirs[name])
		writeAPIRegistration(b, name)
	}

	b.WriteString("}\n")
}

// writeHandlerRegistration writes router.Handle with optional @cache/@rateLimit wrapping.
func writeHandlerRegistration(b *strings.Builder, name string, directives []*ast.Directive) {
	handler := fmt.Sprintf("handle%s(app)", str.Capitalize(name))

	// @cache(ttl) wrapping
	for _, d := range directives {
		if d.Name == "cache" && len(d.Args) > 0 {
			if lit, ok := d.Args[0].Value.(*ast.Literal); ok {
				// ttl is a Duration literal (e.g., "60" seconds or "5m")
				handler = fmt.Sprintf("api.WithCache(%s*time.Second, %s)", lit.Value, handler)
			}
		}
	}

	fmt.Fprintf(b, "\trouter.Handle(%q, %s)\n", name, handler)
}

// collectAPIDirectives collects directives for each API by name.
func collectAPIDirectives(result *semantic.Result) map[string][]*ast.Directive {
	m := make(map[string][]*ast.Directive)
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if len(api.Directives) > 0 {
				m[api.Name] = api.Directives
			}
		}
	}
	return m
}

// generateBatchLoadHandlers generates svc:batchLoad:Model RPC endpoints for each model.
// These endpoints allow remote services to batch-load model data via DataLoader.
// Used in cluster mode: remote service's DataLoader calls this endpoint instead of querying DB.
func generateBatchLoadHandlers(b *strings.Builder, models []*ast.ModelDecl) {
	if len(models) == 0 {
		return
	}

	for _, m := range models {
		name := m.Name
		tableName := str.ToSnakeCase(name) + "s"
		scanFn := "scan" + name

		fmt.Fprintf(b, "// handleBatchLoad%s handles svc:batchLoad:%s — batch load by PK for remote DataLoaders.\n", name, name)
		fmt.Fprintf(b, "func handleBatchLoad%s(app *App) api.HandlerFunc {\n", name)
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		fmt.Fprintf(b, "\t\tkeys, err := req.ParamIntArray(\"keys\")\n")
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tfields, _ := req.ParamStringArray(\"fields\")\n")
		fmt.Fprintf(b, "\t\tconds := []lux.Condition{lux.NewIntField(\"id\").In(keys...)}\n")
		fmt.Fprintf(b, "\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", tableName)
		fmt.Fprintf(b, "\t\trows, err := %s.QueryRows(ctx, app.DB, %s, query, args...)\n", dbPkg, scanFn)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		// Write response: binary array of models
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(rows)))\n")
		fmt.Fprintf(b, "\t\tfor _, row := range rows {\n")
		fmt.Fprintf(b, "\t\t\trow.WriteLuxo(req.Buf, req.FieldMask)\n")
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")
	}

	// RegisterBatchLoaders registers batch load endpoints
	b.WriteString("// RegisterBatchLoaders registers batch load RPC endpoints for all models.\n")
	b.WriteString("// Remote services call these endpoints to batch-load model data (cluster mode).\n")
	b.WriteString("func RegisterBatchLoaders(router *api.Router, app *App) {\n")
	for _, m := range models {
		svcName := "svc:batchLoad:" + m.Name
		fmt.Fprintf(b, "\trouter.Handle(%q, handleBatchLoad%s(app))\n", svcName, m.Name)
	}
	b.WriteString("}\n\n")
}

// writeAPIRegistration generates router.Registry.Register + RegisterParams calls.
func writeAPIRegistration(b *strings.Builder, name string) {
	id := getAPIID(name)
	if id == 0 {
		return
	}
	fmt.Fprintf(b, "\trouter.Registry.Register(%q, %d)\n", name, id)
	params := getAPIParamIDs(name)
	if len(params) == 0 {
		return
	}
	fmt.Fprintf(b, "\trouter.Registry.RegisterParams(%q, []api.ParamMeta{\n", name)
	for paramName, paramID := range params {
		ptype := resolveParamTypeFromAST(name, paramName)
		fmt.Fprintf(b, "\t\t{Name: %q, Type: %q, FieldID: %d},\n", paramName, ptype, paramID)
	}
	b.WriteString("\t})\n")
}

// resolveParamTypeFromAST looks up the actual Luxo type for a param from AST data.
// Falls back to inferParamType heuristic if no AST info available.
func resolveParamTypeFromAST(apiName, paramName string) string {
	if apiParamTypes != nil {
		if params, ok := apiParamTypes[apiName]; ok {
			if t, ok := params[paramName]; ok {
				return t
			}
		}
	}
	return inferParamType(paramName)
}

// inferParamType infers Luxo type from param name for binary param metadata.
// Falls back to "String" for unknown patterns. For compiled APIs with AST type info,
// this is overridden by resolveParamType when available.
func inferParamType(name string) string {
	switch {
	case name == "id" || strings.HasSuffix(name, "Id"):
		return "Int"
	case name == "page" || name == "pageSize" || name == "limit" || name == "offset" ||
		name == "priority" || name == "minutes" || name == "quantity" || name == "count":
		return "Int"
	case strings.HasPrefix(name, "is") || name == "active" || name == "published":
		return "Boolean"
	case name == "amount" || name == "price" || name == "balance" || name == "score" ||
		name == "total" || name == "budget":
		return "Float"
	default:
		return "String"
	}
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
			if hasDirective(f.Directives, "search") {
				fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewSearchField(%q).FilterOp(f.Operator, f.Value))\n", col)
			} else {
				fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewStringField(%q).FilterOp(f.Operator, f.Value))\n", col)
			}
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

	// Check if any @sortable fields exist
	hasSortable := false
	for _, f := range m.Fields {
		if hasDirective(f.Directives, "sortable") && f.Type != nil && f.Computed == nil {
			hasSortable = true
			break
		}
	}

	fmt.Fprintf(b, "// parse%sSorters converts request sorters to ORDER BY clauses.\n", name)
	fmt.Fprintf(b, "// Only @sortable fields are allowed.\n")
	fmt.Fprintf(b, "func parse%sSorters(sorters []api.Sorter) []string {\n", name)

	if !hasSortable {
		fmt.Fprintf(b, "\treturn nil\n")
		fmt.Fprintf(b, "}\n\n")
		return
	}

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

// bodyContainsAwait checks if an API body block contains any AwaitExpr.
func bodyContainsAwait(block *ast.Block) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Stmts {
		if es, ok := stmt.(*ast.ExprStmt); ok {
			if _, ok := es.Expr.(*ast.AwaitExpr); ok {
				return true
			}
		}
	}
	return false
}

// bodyContainsTransaction checks if an API body block contains a transaction call.
// transaction { ... } is parsed as CallExpr("transaction", lambda).
func bodyContainsTransaction(block *ast.Block) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Stmts {
		if es, ok := stmt.(*ast.ExprStmt); ok {
			if call, ok := es.Expr.(*ast.CallExpr); ok {
				if ident, ok := call.Func.(*ast.Ident); ok && ident.Name == "transaction" {
					return true
				}
			}
		}
		// Also check val assignments: val result = transaction { ... }
		if vs, ok := stmt.(*ast.ValStmt); ok {
			if call, ok := vs.Value.(*ast.CallExpr); ok {
				if ident, ok := call.Func.(*ast.Ident); ok && ident.Name == "transaction" {
					return true
				}
			}
		}
	}
	return false
}

// bodyContainsTemplateString checks if an API body uses template strings.
func bodyContainsTemplateString(block *ast.Block) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Stmts {
		if vs, ok := stmt.(*ast.ValStmt); ok {
			if _, ok := vs.Value.(*ast.TemplateString); ok {
				return true
			}
		}
		if rs, ok := stmt.(*ast.ReturnStmt); ok && rs.Value != nil {
			if _, ok := rs.Value.(*ast.TemplateString); ok {
				return true
			}
		}
	}
	return false
}

// writeAuthCheck generates the identity nil-check guard at the start of a handler.
// Used by CRUD handlers (@withAuth on model) and compiled APIs (@auth on API).
//
// Supported patterns:
//
//	@auth                          → nil check only (any authenticated user)
//	@auth(Admin)                   → role == "Admin"
//	@auth(Admin, Moderator)        → role in ["Admin", "Moderator"]
//	@auth(own: "id")               → identity.ID() == id param
//	@auth(Admin, own: "userId")    → role == "Admin" OR owns resource
//	@auth(permission: { expr })    → compile permission lambda expression
func writeAuthCheck(b *strings.Builder, indent string, directives ...*ast.Directive) {
	fmt.Fprintf(b, "%sidentity := luvia.Identity(ctx)\n", indent)
	fmt.Fprintf(b, "%sif identity == nil {\n", indent)
	fmt.Fprintf(b, "%s\treturn errors.Unauthorized\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
	fmt.Fprintf(b, "%sreq.Buf.Identity = identity\n", indent)

	if len(directives) == 0 || directives[0] == nil {
		return
	}
	d := directives[0]

	// Collect roles (positional args) and named args
	var roles []string
	var ownField string
	var permissionBody *ast.Block

	for _, arg := range d.Args {
		if arg.Name == "" {
			// Positional arg = allowed role name
			if ident, ok := arg.Value.(*ast.Ident); ok {
				roles = append(roles, ident.Name)
			}
		} else if arg.Name == "own" {
			// Ownership: identity.ID() must match this request param
			if lit, ok := arg.Value.(*ast.Literal); ok {
				ownField = lit.Value
			} else if ident, ok := arg.Value.(*ast.Ident); ok {
				ownField = ident.Name
			}
		} else if arg.Name == "permission" {
			// Permission lambda: @auth(permission: { my.role == "SUPER" })
			if lambda, ok := arg.Value.(*ast.LambdaExpr); ok {
				permissionBody = lambda.Body
			}
		}
	}

	// No additional checks needed
	if len(roles) == 0 && ownField == "" && permissionBody == nil {
		return
	}

	// Generate authorization check
	// Multiple conditions are OR'd: role match OR ownership OR permission
	fmt.Fprintf(b, "%s_authorized := false\n", indent)

	// Role check: single or multiple roles
	if len(roles) == 1 {
		fmt.Fprintf(b, "%sif identity.String(\"role\") == %q { _authorized = true }\n", indent, roles[0])
	} else if len(roles) > 1 {
		fmt.Fprintf(b, "%sswitch identity.String(\"role\") {\n", indent)
		for _, role := range roles {
			fmt.Fprintf(b, "%scase %q:\n", indent, role)
		}
		fmt.Fprintf(b, "%s\t_authorized = true\n", indent)
		fmt.Fprintf(b, "%s}\n", indent)
	}

	// Ownership check: extract param inline for early auth
	if ownField != "" {
		fmt.Fprintf(b, "%sif _ownID, _ownErr := req.ParamInt(%q); _ownErr == nil && identity.ID() == _ownID { _authorized = true }\n", indent, ownField)
	}

	// Permission lambda
	if permissionBody != nil {
		// Compile the permission expression — simple case: single ExprStmt that evaluates to bool
		if len(permissionBody.Stmts) == 1 {
			if es, ok := permissionBody.Stmts[0].(*ast.ExprStmt); ok {
				permExpr := compilePermissionExpr(es.Expr)
				if permExpr != "" {
					fmt.Fprintf(b, "%sif %s { _authorized = true }\n", indent, permExpr)
				}
			}
		}
	}

	fmt.Fprintf(b, "%sif !_authorized {\n", indent)
	fmt.Fprintf(b, "%s\treturn errors.Forbidden\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

// compilePermissionExpr compiles a permission lambda expression to Go code.
// Supports: my.field == "value", my.field != "value", my.field == EnumValue
func compilePermissionExpr(expr ast.Expr) string {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return ""
	}

	// Left side: my.field → identity.String("field") or identity.Int("field")
	left := ""
	if member, ok := bin.Left.(*ast.MemberExpr); ok {
		if ident, ok := member.Object.(*ast.Ident); ok && ident.Name == "my" {
			left = fmt.Sprintf("identity.String(%q)", member.Field)
		}
	}
	if left == "" {
		return ""
	}

	// Right side: string literal or ident
	right := ""
	if lit, ok := bin.Right.(*ast.Literal); ok {
		right = fmt.Sprintf("%q", lit.Value)
	} else if ident, ok := bin.Right.(*ast.Ident); ok {
		right = fmt.Sprintf("%q", ident.Name)
	} else if member, ok := bin.Right.(*ast.MemberExpr); ok {
		// Enum.VALUE → "VALUE"
		right = fmt.Sprintf("%q", member.Field)
	}
	if right == "" {
		return ""
	}

	return fmt.Sprintf("%s %s %s", left, bin.Op, right)
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

// detectAuthNeeded checks if luvia import is needed in handler.gen.go.
func detectAuthNeeded(result *semantic.Result, models []*ast.ModelDecl) bool {
	for _, m := range models {
		if hasDirective(m.Directives, "withAuth") {
			return true
		}
	}
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if hasDirective(api.Directives, "auth") && !hasDirective(api.Directives, "native") {
				return true
			}
		}
	}
	return false
}

// scanModelsForHash checks if any CRUD model has @hash fields AND generates write operations.
// Only returns true when generated code will actually call luxocrypto.HashPassword.
func scanModelsForHash(models []*ast.ModelDecl) bool {
	for _, m := range models {
		if !hasCrud(m) {
			continue
		}
		hasHash := false
		for _, f := range m.Fields {
			if hasDirective(f.Directives, "hash") {
				hasHash = true
				break
			}
		}
		if !hasHash {
			continue
		}
		// Only need luxocrypto if CRUD includes write operations
		for _, op := range crudOperations(m) {
			if op == "create" || op == "update" || op == "createMany" || op == "updateMany" || op == "upsert" {
				return true
			}
		}
	}
	return false
}

// scanForTimeImport checks if generated handler code will emit time.* identifiers.
// Only Duration fields/params need time package. DateTime uses int64 (Unix timestamp).
func scanForTimeImport(result *semantic.Result, models []*ast.ModelDecl) bool {
	for _, m := range models {
		for _, f := range m.Fields {
			if f.Type != nil && f.Type.Name == "Duration" {
				return true
			}
		}
	}
	for _, file := range result.Files {
		for _, fn := range file.Functions {
			for _, p := range fn.Params {
				if p.Type != nil && p.Type.Name == "Duration" {
					return true
				}
			}
		}
		for _, api := range file.APIs {
			for _, p := range api.Params {
				if p.Type != nil && p.Type.Name == "Duration" {
					return true
				}
			}
		}
	}
	return false
}

// scanModelsForJSON checks if any CRUD model has deleteMany with non-standard ID type.
func scanModelsForJSON(models []*ast.ModelDecl) bool {
	for _, m := range models {
		if hasCrud(m) {
			idType := idGoType(m)
			if idType != "int64" && idType != "string" {
				for _, op := range crudOperations(m) {
					if op == "deleteMany" {
						return true
					}
				}
			}
		}
	}
	return false
}

// generateFieldValidation generates validation code for field directives.
// Runs after param extraction, before setter call.
func generateFieldValidation(b *strings.Builder, f *ast.FieldDecl, varName, indent string) {
	isString := f.Type != nil && f.Type.Name == "String"
	if isString {
		generateStringValidation(b, f, varName, indent)
	}
	if f.Type != nil && (f.Type.Name == "Int" || f.Type.Name == "Float") {
		generateNumericValidation(b, f, varName, indent)
	}
}

func generateStringValidation(b *strings.Builder, f *ast.FieldDecl, varName, indent string) {
	name := f.Name
	if hasDirective(f.Directives, "notBlank") {
		writeValidationCheck(b, indent, name,
			fmt.Sprintf("strings.TrimSpace(%s) == \"\"", varName), "must not be blank")
	}
	if hasDirective(f.Directives, "email") {
		writeValidationCheck(b, indent, name,
			fmt.Sprintf("!strings.Contains(%s, \"@\") || !strings.Contains(%s, \".\")", varName, varName), "invalid email format")
	}
	for _, d := range f.Directives {
		if d.Name == "minLength" && len(d.Args) > 0 {
			if lit, ok := d.Args[0].Value.(*ast.Literal); ok {
				writeValidationCheck(b, indent, name,
					fmt.Sprintf("len(%s) < %s", varName, lit.Value), "too short, min "+lit.Value)
			}
		}
		if d.Name == "maxLength" && len(d.Args) > 0 {
			if lit, ok := d.Args[0].Value.(*ast.Literal); ok {
				writeValidationCheck(b, indent, name,
					fmt.Sprintf("len(%s) > %s", varName, lit.Value), "too long, max "+lit.Value)
			}
		}
		if d.Name == "pattern" && len(d.Args) > 0 {
			if lit, ok := d.Args[0].Value.(*ast.Literal); ok {
				fmt.Fprintf(b, "%sif matched, _ := regexp.MatchString(%q, %s); !matched {\n", indent, lit.Value, varName)
				fmt.Fprintf(b, "%s\treturn errors.BadRequest.WithData(errors.ParamError{Param: %q, Error: \"invalid format\"})\n", indent, name)
				fmt.Fprintf(b, "%s}\n", indent)
			}
		}
	}
}

func generateNumericValidation(b *strings.Builder, f *ast.FieldDecl, varName, indent string) {
	for _, d := range f.Directives {
		if d.Name == "range" && len(d.Args) >= 2 {
			minLit, minOk := d.Args[0].Value.(*ast.Literal)
			maxLit, maxOk := d.Args[1].Value.(*ast.Literal)
			if minOk && maxOk {
				writeValidationCheck(b, indent, f.Name,
					fmt.Sprintf("%s < %s || %s > %s", varName, minLit.Value, varName, maxLit.Value),
					"out of range ["+minLit.Value+", "+maxLit.Value+"]")
			}
		}
	}
}

func writeValidationCheck(b *strings.Builder, indent, field, cond, errMsg string) {
	fmt.Fprintf(b, "%sif %s {\n", indent, cond)
	fmt.Fprintf(b, "%s\treturn errors.BadRequest.WithData(errors.ParamError{Param: %q, Error: %q})\n", indent, field, errMsg)
	fmt.Fprintf(b, "%s}\n", indent)
}

// scanModelsForValidation checks if any CRUD model has validation directives.
// Returns (hasValidation, hasPattern) — hasValidation needs strings, hasPattern needs regexp.
func scanModelsForValidation(models []*ast.ModelDecl) (bool, bool) {
	hasValidation := false
	hasPattern := false
	for _, m := range models {
		if !hasCrud(m) {
			continue
		}
		for _, f := range m.Fields {
			for _, d := range f.Directives {
				switch d.Name {
				case "notBlank", "email", "minLength", "maxLength":
					hasValidation = true
				case "pattern":
					hasPattern = true
					hasValidation = true
				}
			}
		}
	}
	return hasValidation, hasPattern
}

// generateBeforeSave compiles @beforeSave { body } for a field.
// `it` in the body refers to the field variable.
func generateBeforeSave(b *strings.Builder, f *ast.FieldDecl, varName, indent string) {
	for _, d := range f.Directives {
		if d.Name != "beforeSave" || d.Body == nil || len(d.Body.Stmts) == 0 {
			continue
		}
		es, ok := d.Body.Stmts[0].(*ast.ExprStmt)
		if !ok || es.Expr == nil {
			continue
		}
		code := compileFieldExpr(es.Expr, varName)
		if code != "" {
			fmt.Fprintf(b, "%s%s = %s\n", indent, varName, code)
		}
	}
}

// compileFieldExpr compiles a field-level expression where `it` refers to the field variable.
func compileFieldExpr(expr ast.Expr, itVar string) string {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if member, ok := e.Func.(*ast.MemberExpr); ok {
			obj := compileFieldExpr(member.Object, itVar)
			nargs := len(e.Args)
			arg := func(i int) string {
				if i < nargs {
					return compileFieldExpr(e.Args[i].Value, itVar)
				}
				return ""
			}
			if result := compileStringTransform(member.Field, obj, arg, nargs); result != "" {
				return result
			}
			if result := compileStringQuery(member.Field, obj, arg, nargs); result != "" {
				return result
			}
			if result := compileStringConvert(member.Field, obj); result != "" {
				return result
			}
		}
	case *ast.MemberExpr:
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "it" {
			return itVar + "." + str.Capitalize(e.Field)
		}
	case *ast.Ident:
		if e.Name == "it" {
			return itVar
		}
		return e.Name
	case *ast.Literal:
		if e.Kind == token.Int || e.Kind == token.Float {
			return e.Value
		}
		return fmt.Sprintf("%q", e.Value)
	}
	return ""
}

// generateAggregateFields generates SQL sub-queries for @count/@sum/@avg/@min/@max computed fields.
func generateAggregateFields(b *strings.Builder, m *ast.ModelDecl, resultVar, indent string) {
	for _, f := range m.Fields {
		if f.Computed == nil {
			continue
		}
		for _, d := range f.Computed.Directives {
			aggFn := ""
			switch d.Name {
			case "count":
				aggFn = "COUNT"
			case "sum":
				aggFn = "SUM"
			case "avg":
				aggFn = "AVG"
			case "min":
				aggFn = "MIN"
			case "max":
				aggFn = "MAX"
			default:
				continue
			}

			// @count(relation) → table = relation table, fkCol = modelName_id
			if len(d.Args) == 0 {
				continue
			}
			relationName := ""
			targetCol := ""
			if ident, ok := d.Args[0].Value.(*ast.Ident); ok {
				relationName = ident.Name
			}
			if member, ok := d.Args[0].Value.(*ast.MemberExpr); ok {
				if ident, ok := member.Object.(*ast.Ident); ok {
					relationName = ident.Name
					targetCol = str.ToSnakeCase(member.Field)
				}
			}
			if relationName == "" {
				continue
			}

			table := str.ToSnakeCase(relationName) + "s"
			fkCol := str.ToSnakeCase(m.Name) + "_id"
			goField := str.Capitalize(f.Name)

			sql := fmt.Sprintf("lux.AggregateSQL(%q, %q, %q, %q)", aggFn, table, fkCol, targetCol)
			fmt.Fprintf(b, "%s{\n", indent)
			fmt.Fprintf(b, "%s\tvar aggVal int64\n", indent)
			fmt.Fprintf(b, "%s\t_ = pg.QueryRow(ctx, app.DB, %s, %s.Id).Scan(&aggVal)\n", indent, sql, resultVar)
			fmt.Fprintf(b, "%s\t%s.%s = aggVal\n", indent, resultVar, goField)
			fmt.Fprintf(b, "%s}\n", indent)
		}
	}
}

// scanBodyForBuiltins walks AST to find crypto.*, now(), duration property usage.
func scanBodyForBuiltins(block *ast.Block, f *handlerFeatures) {
	if block == nil {
		return
	}
	// Scan expressions
	ast.WalkExprs(block, func(e ast.Expr) {
		switch v := e.(type) {
		case *ast.MemberExpr:
			if ident, ok := v.Object.(*ast.Ident); ok && ident.Name == "crypto" {
				f.hasCrypto = true
			}
			switch v.Field {
			case "days", "hours", "minutes", "seconds", "milliseconds":
				f.hasTimeFunc = true
			}
		case *ast.CallExpr:
			if ident, ok := v.Func.(*ast.Ident); ok && ident.Name == "now" {
				f.hasTimeFunc = true
			}
		}
	})
	// Scan statements for emit
	for _, stmt := range block.Stmts {
		if emit, ok := stmt.(*ast.EmitStmt); ok {
			f.hasEmit = true
			if globalEventCtx != nil {
				evModule := globalEventCtx.EventModule[emit.EventName]
				currentMod := ""
				// currentModule is tricky here — use file from features detection
				if evModule != "" && evModule != currentMod {
					if f.crossEventImports == nil {
						f.crossEventImports = make(map[string]string)
					}
					f.crossEventImports[evModule] = evModule + "_luxo"
				}
			}
		}
	}
}
