package codegen

import (
	"fmt"
	"os"
	"sort"
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
	hasLog            bool
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

	// Scan compiled API bodies for crypto, time, and cross-module emit usage
	curModule := ""
	if len(result.Files) > 0 {
		curModule = moduleNameFromFile(result.Files[0].Name)
	}
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body == nil {
				continue
			}
			scanBodyForBuiltins(api.Body, &f, curModule)
		}
	}

	return f
}

// generateHandlerFile produces handler.gen.go containing CRUD handlers,
// inferred handlers (zero-body APIs), and RegisterHandlers.
func generateHandlerFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	models, allModels := collectHandlerModels(result)
	modelMap, inferredAPIs := collectInferredAPIs(result)
	hasCompiledAPIs, hasNativeAPIs, hasServiceFns := handlerDeclarationKinds(result)
	remoteLoads := remoteLoadCallsForResult(result)
	if len(models) == 0 && len(inferredAPIs) == 0 && !hasCompiledAPIs && !hasNativeAPIs && !hasServiceFns && len(remoteLoads) == 0 {
		return nil
	}

	features := detectHandlerFeatures(result, models, inferredAPIs, modelMap)

	// Body is generated into its own builder first so the import block can be
	// derived from what the generated code actually references (e.g. a module
	// whose native list handlers all became columnar no longer needs codec).
	var b strings.Builder
	generateSelectedColumnHelper(&b)
	for _, model := range collectSelectionModels(result, allModels) {
		generateSQLColumnSelector(&b, model, enums)
	}
	generateComputedResolvers(&b, allModels, modelMap, enums)

	// Generate defaultCols for models with @hidden fields (excludes hidden from SELECT *)
	for _, m := range models {
		if hasHiddenFields(m) {
			generateDefaultCols(&b, m, enums)
		}
	}

	compiledNames := generateCompiledHandlers(&b, result, modelMap)
	nativeNames := generateNativeAPIHandlers(&b, result)
	inferredNames := generateInferredHandlers(&b, inferredAPIs, modelMap, enums)

	// Build set of compiled/inferred names to avoid generating duplicate CRUD handlers
	compiledSet := make(map[string]bool, len(compiledNames)+len(inferredNames)+len(nativeNames))
	for _, n := range compiledNames {
		compiledSet[n] = true
	}
	for _, n := range inferredNames {
		compiledSet[n] = true
	}
	for _, n := range nativeNames {
		compiledSet[n] = true
	}
	generateCRUDHandlers(&b, models, enums, compiledSet, allModels)

	// Collect API directives for middleware wrapping
	apiDirectives := collectAPIDirectives(result)

	// RegisterHandlers function (CRUD + inferred + compiled + native)
	allInferred := append(inferredNames, compiledNames...)
	allInferred = append(allInferred, nativeNames...)
	generateRegisterFuncWithInferred(&b, models, allInferred, apiDirectives)

	// fn @service handlers
	serviceNames := generateServiceFnHandlers(&b, result, modelMap)
	generateRegisterServiceFns(&b, serviceNames)

	// DataLoader RPC endpoints — batch load for each model (cluster mode)
	batchModels := collectBatchLoadModels(models, allModels)
	generateBatchLoadHandlers(&b, batchModels, enums)
	generateRemoteNamedLoadHandlers(&b, result, allModels, remoteLoads, enums)

	// Federation resolve endpoints — svc:resolve:{Model}:{FK} for cross-module extends
	generateFederationResolvers(&b, result, models, enums)

	body := b.String()
	var out strings.Builder
	writeHeader(&out, packageName, "handler.gen.go")
	writeHandlerImports(&out, result, allModels, features, strings.Contains(body, "codec."), body)
	out.WriteString(body)
	return []byte(out.String())
}

func collectHandlerModels(result *semantic.Result) (crudModels, allModels []*ast.ModelDecl) {
	for _, file := range result.Files {
		for _, model := range file.Models {
			allModels = append(allModels, model)
			if hasCrud(model) {
				crudModels = append(crudModels, model)
			}
		}
	}
	return crudModels, allModels
}

func handlerDeclarationKinds(result *semantic.Result) (hasCompiled, hasNative, hasService bool) {
	for _, file := range result.Files {
		for _, api := range file.APIs {
			hasNative = hasNative || hasDirective(api.Directives, "native")
			hasCompiled = hasCompiled || api.Body != nil && !hasDirective(api.Directives, "native")
		}
		hasService = hasService || functionsHaveDirective(file.Functions, "service")
	}
	return hasCompiled, hasNative, hasService
}

func collectSelectionModels(result *semantic.Result, models []*ast.ModelDecl) []*ast.ModelDecl {
	selectionModels := append([]*ast.ModelDecl(nil), models...)
	seen := make(map[string]bool, len(selectionModels))
	for _, model := range selectionModels {
		seen[model.Name] = true
	}
	for _, file := range result.Files {
		for _, ext := range file.Extends {
			if !seen[ext.Name] {
				selectionModels = append(selectionModels, extendStubModel(ext))
				seen[ext.Name] = true
			}
		}
	}
	return selectionModels
}

func generateCRUDHandlers(b *strings.Builder, models []*ast.ModelDecl, enums map[string]bool, skipNames map[string]bool, universes ...[]*ast.ModelDecl) {
	// Collect all model relations for recursive resolve support
	modelRels := make(map[string]bool)
	for _, m := range models {
		rels := analyzeRelations(m, enums)
		if len(rels) > 0 {
			modelRels[m.Name] = true
		}
	}
	computedModels := make(map[string]bool)
	computedUniverse := models
	if len(universes) > 0 {
		computedUniverse = universes[0]
	}
	for _, model := range computedUniverse {
		computedModels[model.Name] = modelHasComputedAggregates(model)
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
			generateRelationResolver(b, m, rels, modelRels, computedModels)
			generateListRelationResolver(b, m, rels, modelRels, computedModels)
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
			if api.Body != nil && !hasDirective(api.Directives, "native") && !hasDirective(api.Directives, "stream") {
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
				generateNativeServiceHandler(b, fn, modelMap, enumSet)
			} else if fn.Body != nil {
				// Compiled fn @service
				compileFnBody(b, fn, modelMap, enumSet)
			}
			names = append(names, fn.Name)
		}
	}
	return names
}

// generateNativeAPIHandlers generates handlers for api @native declarations.
// Returns native API names for registration in RegisterHandlers.
func generateNativeAPIHandlers(b *strings.Builder, result *semantic.Result) []string {
	// Model names — list returns need the []Model → []*Model adapter for
	// WriteColumnar (types get a value-slice writer, models a pointer-slice one)
	models := make(map[string]*ast.ModelDecl)
	enums := make(map[string]bool)
	for _, file := range result.Files {
		for _, m := range file.Models {
			models[m.Name] = m
		}
		for _, enum := range file.Enums {
			enums[enum.Name] = true
		}
	}
	var names []string
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if !hasDirective(api.Directives, "native") {
				continue
			}
			generateNativeAPIHandler(b, api, models, enums)
			names = append(names, api.Name)
		}
	}
	return names
}

// generateNativeAPIHandler generates a single handler for an api @native declaration.
func generateNativeAPIHandler(b *strings.Builder, api *ast.ApiDecl, models map[string]*ast.ModelDecl, enums map[string]bool) {
	name := api.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(name))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")

	// @auth check (reuse writeAuthCheck for role/own/permission support)
	for _, d := range api.Directives {
		if d.Name == "auth" {
			writeAuthCheck(b, "\t\t", d)
			break
		}
	}

	// Parse params
	var paramNames []string
	for _, p := range api.Params {
		goType := resolveGoType(p.Type)
		method := paramMethod(goType)
		if p.Type != nil && p.Type.Nullable {
			method = ""
		}
		if method == "" {
			fmt.Fprintf(b, "\t\tvar %s %s\n", p.Name, goType)
			methodName := paramJSONMethod(p)
			fmt.Fprintf(b, "\t\tif err := req.%s(%q, &%s); err != nil {\n", methodName, p.Name, p.Name)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\t%s, err := req.Param%s(%q)\n", p.Name, method, p.Name)
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		}
		paramNames = append(paramNames, p.Name)
	}

	// Call NativeResolver
	if api.ReturnType != nil {
		fmt.Fprintf(b, "\t\tresult, err := app.Resolver.%s(ctx", str.Capitalize(name))
	} else {
		fmt.Fprintf(b, "\t\t_, err := app.Resolver.%s(ctx", str.Capitalize(name))
	}
	for _, pn := range paramNames {
		fmt.Fprintf(b, ", %s", pn)
	}
	fmt.Fprintf(b, ")\n")
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")

	// Write response
	if api.ReturnType != nil {
		writeNativeReturnEncoding(b, api.ReturnType, models, enums)
	}
	fmt.Fprintf(b, "\t\treturn nil\n")
	fmt.Fprintf(b, "\t}\n}\n\n")
}

// writeNativeReturnEncoding writes the binary encoding for a native API return value.
func writeNativeReturnEncoding(b *strings.Builder, rt *ast.TypeRef, models map[string]*ast.ModelDecl, enums map[string]bool) {
	if rt.IsList {
		writeNativeListEncoding(b, rt, models, enums)
		return
	}
	if model := models[rt.Name]; model != nil {
		writeComputedResolve(b, model, "[]*"+rt.Name+"{&result}", "\t\t")
	}
	writeNativeScalarEncoding(b, rt, "result", enums)
}

// writeNativeListEncoding writes list encoding. Primitive lists are
// length-prefixed; struct lists are columnar — the wire protocol for all
// list responses (Luvia's BinaryListToJSON reads columnar).
func writeNativeListEncoding(b *strings.Builder, rt *ast.TypeRef, models map[string]*ast.ModelDecl, enums map[string]bool) {
	if appendExpr, ok := binaryScalarAppend(rt.Name, "req.Buf.B", "v", enums[rt.Name]); ok {
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(result)))\n")
		fmt.Fprintf(b, "\t\tfor _, v := range result {\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.B = %s\n", appendExpr)
		fmt.Fprintf(b, "\t\t}\n")
		return
	}
	if model := models[rt.Name]; model != nil {
		// Model list — resolver returns []Model but WriteColumnarModel
		// takes []*Model (DB loaders produce pointer slices); adapt.
		fmt.Fprintf(b, "\t\t_ptrs := make([]*%s, len(result))\n", rt.Name)
		fmt.Fprintf(b, "\t\tfor i := range result {\n")
		fmt.Fprintf(b, "\t\t\t_ptrs[i] = &result[i]\n")
		fmt.Fprintf(b, "\t\t}\n")
		writeComputedResolve(b, model, "_ptrs", "\t\t")
		fmt.Fprintf(b, "\t\tWriteColumnar%s(req.Buf, _ptrs, req.FieldMask)\n", rt.Name)
	} else {
		// Type declaration list — value-slice columnar writer
		fmt.Fprintf(b, "\t\tWriteColumnar%s(req.Buf, result, req.FieldMask)\n", rt.Name)
	}
}

// writeNativeScalarEncoding writes encoding for a single return value.
func writeNativeScalarEncoding(b *strings.Builder, rt *ast.TypeRef, varName string, enums map[string]bool) {
	if appendExpr, ok := binaryScalarAppend(rt.Name, "req.Buf.B", varName, enums[rt.Name]); ok {
		fmt.Fprintf(b, "\t\treq.Buf.B = %s\n", appendExpr)
		return
	}
	// Struct type — use WriteLuxo.
	fmt.Fprintf(b, "\t\t%s.WriteLuxo(req.Buf, req.FieldMask)\n", varName)
}

// generateNativeServiceHandler generates a handler that delegates to NativeResolver.
func generateNativeServiceHandler(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, enums map[string]bool) {
	name := fn.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(name))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")

	// Parse params
	var paramNames []string
	for _, p := range fn.Params {
		goType := resolveGoType(p.Type)
		method := paramMethod(goType)
		if p.Type != nil && p.Type.Nullable {
			method = ""
		}
		if method == "" {
			fmt.Fprintf(b, "\t\tvar %s %s\n", p.Name, goType)
			methodName := paramJSONMethod(p)
			fmt.Fprintf(b, "\t\tif err := req.%s(%q, &%s); err != nil {\n", methodName, p.Name, p.Name)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\t%s, err := req.Param%s(%q)\n", p.Name, method, p.Name)
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		}
		paramNames = append(paramNames, p.Name)
	}

	// Call NativeResolver
	if fn.ReturnType == nil {
		fmt.Fprintf(b, "\t\terr := app.Resolver.%s(ctx", str.Capitalize(name))
	} else {
		fmt.Fprintf(b, "\t\tresult, err := app.Resolver.%s(ctx", str.Capitalize(name))
	}
	for _, pn := range paramNames {
		fmt.Fprintf(b, ", %s", pn)
	}
	fmt.Fprintf(b, ")\n")
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")

	// Write response — always binary (Luvia converts to JSON if needed)
	if fn.ReturnType != nil {
		writeNativeReturnEncoding(b, fn.ReturnType, models, enums)
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
// writeSortedCrossModuleImports writes cross-module event imports in deterministic order.
func writeSortedCrossModuleImports(b *strings.Builder, imports map[string]string) {
	if globalEventCtx == nil || len(imports) == 0 {
		return
	}
	modNames := make([]string, 0, len(imports))
	for modName := range imports {
		modNames = append(modNames, modName)
	}
	sort.Strings(modNames)
	for _, modName := range modNames {
		fmt.Fprintf(b, "\t%s \"%s/%s/luxo\"\n", imports[modName], globalEventCtx.ModulePath, modName)
	}
}

// needsFmtImport checks if fmt package is needed (emit + CRUD create/update validation).
func needsFmtImport(models []*ast.ModelDecl, hasEmit bool) bool {
	if hasEmit {
		return true
	}
	for _, m := range models {
		ops := crudOperations(m)
		for _, op := range ops {
			if op == "create" || op == "update" {
				return true
			}
		}
	}
	return false
}

func writeHandlerImports(b *strings.Builder, result *semantic.Result, models []*ast.ModelDecl, feat handlerFeatures, needsCodec bool, generatedBody ...string) {
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
	needsFmt := needsFmtImport(models, feat.hasEmit)
	needsLux := len(models) > 0
	needsErrors := true
	needsSelection := len(models) > 0
	needsPG := len(models) > 0
	needsUUID := false
	needsDecimal := false
	if len(generatedBody) > 0 {
		body := generatedBody[0]
		hasOrGroups = strings.Contains(body, "strconv.")
		hasSortable = strings.Contains(body, "strings.")
		hasTemplateStr = false
		hasAuth = strings.Contains(body, "luvia.")
		hasHash = false
		feat.hasCrypto = strings.Contains(body, "luxocrypto.")
		hasTime = strings.Contains(body, "time.")
		needsJSON = strings.Contains(body, "json.")
		needsFmt = strings.Contains(body, "fmt.")
		hasValidation = false
		hasPattern = strings.Contains(body, "regexp.")
		needsLux = strings.Contains(body, "lux.")
		needsErrors = strings.Contains(body, "errors.")
		needsSelection = strings.Contains(body, "selection.")
		needsPG = strings.Contains(body, "pg.")
		needsUUID = strings.Contains(body, "uuid.")
		needsDecimal = strings.Contains(body, "decimal.")
	}
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	if needsFmt {
		b.WriteString("\t\"fmt\"\n")
	}
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
	if needsLux {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	if needsCodec {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	}
	if hasHash || feat.hasCrypto {
		b.WriteString("\tluxocrypto \"github.com/light-speak/luxo/pkg/lux/crypto\"\n")
	}
	if needsErrors {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/errors\"\n")
	}
	if feat.hasLog {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/luxolog\"\n")
	}
	if hasAuth {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/luvia\"\n")
	}
	if needsSelection {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/selection\"\n")
	}
	if needsUUID {
		b.WriteString("\t\"github.com/google/uuid\"\n")
	}
	if needsDecimal {
		b.WriteString("\t\"github.com/shopspring/decimal\"\n")
	}
	writeSortedCrossModuleImports(b, feat.crossEventImports)
	if hasAwait {
		b.WriteString("\t\"golang.org/x/sync/errgroup\"\n")
	}
	// pg import: batchLoad handlers need pg.QueryRows, transaction needs pg driver
	if needsPG {
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
	idGoName := primaryKeyGoName(m)

	apiName := crudAPIName(name, op)
	hasRels := len(rels) > 0
	hidden := hasHiddenFields(m)

	// @withAuth marks the identity model; @auth protects arbitrary model CRUD.
	modelAuth := findDirective(m.Directives, "auth")
	needAuth := hasDirective(m.Directives, "withAuth") || modelAuth != nil

	switch op {
	case "get":
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		if needAuth {
			writeCRUDAuthCheck(b, "\t\t", modelAuth)
		}
		writeParamID(b, idType, "\t\t")
		fmt.Fprintf(b, "\t\tcols := select%sSQLColumns(req.Select)\n", name)
		if hidden {
			fmt.Fprintf(b, "\t\tif cols == nil { cols = default%sCols }\n", name)
		}
		writeFKEnsure(b, rels)
		fmt.Fprintf(b, "\t\tresult, err := app.%s.Where(%sWhere.%s.Eq(id)).Select(cols...).First(ctx)\n", name, name, idGoName)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif result == nil {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q, ID: id})\n\t\t}\n", name)
		if hasRels {
			fmt.Fprintf(b, "\t\tif err := resolve%sRelations(ctx, app, result, req.Select); err != nil {\n", name)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		}
		writeComputedResolve(b, m, "[]*"+name+"{result}", "\t\t")
		fmt.Fprintf(b, "\t\tresult.WriteLuxo(req.Buf, req.FieldMask)\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")

	case "list":
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		if needAuth {
			writeCRUDAuthCheck(b, "\t\t", modelAuth)
		}
		fmt.Fprintf(b, "\t\tcols := select%sSQLColumns(req.Select)\n", name)
		if hidden {
			fmt.Fprintf(b, "\t\tif cols == nil { cols = default%sCols }\n", name)
		}
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
		writeComputedResolve(b, m, "results", "\t\t")
		// Write paginated response — columnar encoding for lists
		fmt.Fprintf(b, "\t\tWriteColumnar%s(req.Buf, results, req.FieldMask)\n", name)
		// Append pagination metadata after columnar data
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, total)\n")
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.Page))\n")
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.PageSize))\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")

	case "create":
		generateCreateHandler(b, m, apiName, enums, needAuth, modelAuth)

	case "update":
		generateUpdateHandler(b, m, apiName, idType, enums, needAuth, modelAuth)

	case "delete":
		soft := isSoftDelete(m)
		fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		if needAuth {
			writeCRUDAuthCheck(b, "\t\t", modelAuth)
		}
		writeParamID(b, idType, "\t\t")
		if soft {
			fmt.Fprintf(b, "\t\tn, err := app.%s.SoftDelete(ctx, %sWhere.%s.Eq(id))\n", name, name, idGoName)
		} else {
			fmt.Fprintf(b, "\t\tn, err := app.%s.Where(%sWhere.%s.Eq(id)).Delete(ctx)\n", name, name, idGoName)
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
			writeCRUDAuthCheck(b, "\t\t", modelAuth)
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
			fmt.Fprintf(b, "\t\tn, err := app.%s.SoftDelete(ctx, %sWhere.%s.In(ids...))\n", name, name, idGoName)
		} else {
			fmt.Fprintf(b, "\t\tn, err := app.%s.Where(%sWhere.%s.In(ids...)).Delete(ctx)\n", name, name, idGoName)
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
func generateCreateHandler(b *strings.Builder, m *ast.ModelDecl, apiName string, enums map[string]bool, needAuth bool, modelAuth *ast.Directive) {
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
			generateParamSet(b, f, setter, "\t\t\t", enums)
			fmt.Fprintf(b, "\t\t}\n")
		} else {
			generateParamSet(b, f, setter, "\t\t", enums)
		}
	}

	// @beforeSave hooks are compiled per-field inside generateParamSet

	fmt.Fprintf(b, "\t\tresult, err := builder.Exec(ctx)\n")
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	fmt.Fprintf(b, "\t\tapi.InvalidateCache(%q)\n", m.Name)
	writeComputedResolve(b, m, "[]*"+name+"{result}", "\t\t")
	fmt.Fprintf(b, "\t\tresult.WriteLuxo(req.Buf, req.FieldMask)\n")
	fmt.Fprintf(b, "\t\treturn nil\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "}\n\n")
}

// generateUpdateHandler generates an update handler.
func generateUpdateHandler(b *strings.Builder, m *ast.ModelDecl, apiName, idType string, enums map[string]bool, needAuth bool, modelAuth *ast.Directive) {
	name := m.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(apiName))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
	if needAuth {
		writeCRUDAuthCheck(b, "\t\t", modelAuth)
	}
	writeParamID(b, idType, "\t\t")
	fmt.Fprintf(b, "\t\texisting, err := app.%s.Find(ctx, id)\n", name)
	fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	fmt.Fprintf(b, "\t\tif existing == nil {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q, ID: id})\n\t\t}\n", name)
	tableName := str.ToSnakeCase(name) + "s"
	fmt.Fprintf(b, "\t\tbuilder := new%sUpdateBuilder(app.%s.db, %q, id)\n", name, name, tableName)

	for _, f := range m.Fields {
		if skipHandlerField(f, enums) || f.Name == primaryKeyFieldName(m) || hasDirective(f.Directives, "immutable") {
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
	writeComputedResolve(b, m, "[]*"+name+"{result}", "\t\t")
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
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON":
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
	if f.Type != nil && f.Type.Nullable {
		fmt.Fprintf(b, "%s}\n", originalIndent)
	}
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

func generateSQLColumnSelector(b *strings.Builder, model *ast.ModelDecl, enums map[string]bool) {
	primaryKey := primaryKeyFieldName(model)
	fmt.Fprintf(b, "// select%sSQLColumns maps API fields to database columns.\n", model.Name)
	fmt.Fprintf(b, "func select%sSQLColumns(fields []*selection.Field) []string {\n", model.Name)
	b.WriteString("\tif len(fields) == 0 { return nil }\n")
	b.WriteString("\tcols := make([]string, 0, len(fields)+1)\n")
	b.WriteString("\thasPrimaryKey := false\n")
	b.WriteString("\tfor _, field := range fields {\n")
	b.WriteString("\t\tswitch field.Name {\n")
	for _, field := range model.Fields {
		if localKey, ok := computedFieldLocalKey(model, field, enums); ok {
			fmt.Fprintf(b, "\t\tcase %q:\n", field.Name)
			fmt.Fprintf(b, "\t\t\tcols = ensureSelectedColumn(cols, %q)\n", str.ToSnakeCase(localKey))
			if localKey == primaryKey {
				b.WriteString("\t\t\thasPrimaryKey = true\n")
			}
			continue
		}
		if field.Type == nil || field.Computed != nil || isRelationField(field, enums) ||
			hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") {
			continue
		}
		fmt.Fprintf(b, "\t\tcase %q:\n", field.Name)
		fmt.Fprintf(b, "\t\t\tcols = ensureSelectedColumn(cols, %q)\n", str.ToSnakeCase(field.Name))
		if field.Name == primaryKey {
			b.WriteString("\t\t\thasPrimaryKey = true\n")
		}
	}
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	fmt.Fprintf(b, "\tif !hasPrimaryKey { cols = append(cols, %q) }\n", str.ToSnakeCase(primaryKey))
	b.WriteString("\treturn cols\n")
	b.WriteString("}\n\n")
}

func generateSelectedColumnHelper(b *strings.Builder) {
	b.WriteString("func ensureSelectedColumn(columns []string, name string) []string {\n")
	b.WriteString("\tfor _, column := range columns {\n")
	b.WriteString("\t\tif column == name { return columns }\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn append(columns, name)\n")
	b.WriteString("}\n\n")
}

func computedFieldLocalKey(model *ast.ModelDecl, field *ast.FieldDecl, enums map[string]bool) (string, bool) {
	if field.Computed == nil {
		return "", false
	}
	relations := analyzeRelations(model, enums)
	for _, directive := range field.Computed.Directives {
		if len(directive.Args) != 1 {
			continue
		}
		relationName, _, ok := computedAggregateTarget(directive.Name, directive.Args[0].Value)
		if !ok {
			continue
		}
		for _, relation := range relations {
			if relation.FieldName == relationName && relation.IsList {
				return relation.LocalKey, true
			}
		}
	}
	return "", false
}

func firstBoolSet(sets []map[string]bool) map[string]bool {
	if len(sets) == 0 {
		return nil
	}
	return sets[0]
}

func writeNestedComputedResolve(b *strings.Builder, relation Relation, resultExpr, indent string, computedModels map[string]bool) {
	if !computedModels[relation.TargetName] {
		return
	}
	items := resultExpr
	if !relation.IsList {
		items = "[]*" + relation.TargetName + "{" + resultExpr + "}"
	}
	fmt.Fprintf(b, "%sif err := resolve%sComputedFields(ctx, app, %s, f.Children); err != nil { return err }\n",
		indent, relation.TargetName, items)
}

func writeNestedListComputedResolve(b *strings.Builder, relation Relation, computedModels map[string]bool) {
	if !computedModels[relation.TargetName] {
		return
	}
	goField := str.Capitalize(relation.FieldName)
	fmt.Fprintf(b, "\t\t\tcomputedItems := make([]*%s, 0)\n", relation.TargetName)
	b.WriteString("\t\t\tfor _, item := range items {\n")
	if relation.IsList {
		fmt.Fprintf(b, "\t\t\t\tcomputedItems = append(computedItems, item.%s...)\n", goField)
	} else {
		fmt.Fprintf(b, "\t\t\t\tif item.%s != nil { computedItems = append(computedItems, item.%s) }\n", goField, goField)
	}
	b.WriteString("\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\tif err := resolve%sComputedFields(ctx, app, computedItems, f.Children); err != nil { return err }\n", relation.TargetName)
}

// generateRelationResolver generates a resolve<Model>Relations function
// that loads relation fields via DataLoader based on $select.
func generateRelationResolver(b *strings.Builder, m *ast.ModelDecl, rels []Relation, modelRels map[string]bool, computedSets ...map[string]bool) {
	name := m.Name
	lower := strings.ToLower(name[:1]) + name[1:]
	computedModels := firstBoolSet(computedSets)

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
		fmt.Fprintf(b, "\t\t\tchildCols := select%sSQLColumns(f.Children)\n", rel.TargetName)
		if rel.FKNullable {
			fmt.Fprintf(b, "\t\t\tif %s.%s != nil {\n", lower, goLocalKey)
			fmt.Fprintf(b, "\t\t\t\tresult, err := app.loaders.%s.Load(ctx, *%s.%s, childCols)\n",
				loaderField, lower, goLocalKey)
			fmt.Fprintf(b, "\t\t\t\tif err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\t\t%s.%s = result\n", lower, goFieldName)
			writeNestedComputedResolve(b, rel, "result", "\t\t\t\t", computedModels)
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
			writeNestedComputedResolve(b, rel, "result", "\t\t\t", computedModels)
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
func generateListRelationResolver(b *strings.Builder, m *ast.ModelDecl, rels []Relation, modelRels map[string]bool, computedSets ...map[string]bool) {
	name := m.Name
	computedModels := firstBoolSet(computedSets)

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
		fmt.Fprintf(b, "\t\t\tchildCols := select%sSQLColumns(f.Children)\n", rel.TargetName)

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
		writeNestedListComputedResolve(b, rel, computedModels)

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
func generateBatchLoadHandlers(b *strings.Builder, models []*ast.ModelDecl, enumSets ...map[string]bool) {
	if len(models) == 0 {
		return
	}

	for _, m := range models {
		name := m.Name
		tableName := str.ToSnakeCase(name) + "s"
		scanFn := "scan" + name
		idType := modelIDTypeName(m)
		idGoType := idGoType(m)
		idColumn := str.ToSnakeCase(primaryKeyFieldName(m))
		paramMethod := idArrayParamMethod(idType)

		fmt.Fprintf(b, "// handleBatchLoad%s handles svc:batchLoad:%s — batch load by PK for remote DataLoaders.\n", name, name)
		fmt.Fprintf(b, "func handleBatchLoad%s(app *App) api.HandlerFunc {\n", name)
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		fmt.Fprintf(b, "\t\tkeys, err := req.Param%s(\"keys\")\n", paramMethod)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		generateSelectedSQLFields(b, m, firstBoolSet(enumSets))
		fmt.Fprintf(b, "\t\tconds := []lux.Condition{lux.New%s(%q).In(keys...)}\n", goTypeToCondField(idGoType), idColumn)
		fmt.Fprintf(b, "\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", tableName)
		fmt.Fprintf(b, "\t\trows, err := %s.QueryRows(ctx, app.DB, %s, query, args...)\n", dbPkg, scanFn)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		writeComputedResolve(b, m, "rows", "\t\t")
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
		apiID := getAPIID(svcName)
		if apiID > 0 {
			fmt.Fprintf(b, "\trouter.Registry.Register(%q, %d)\n", svcName, apiID)
			fmt.Fprintf(b, "\trouter.Registry.RegisterParams(%q, []api.ParamMeta{{FieldID: 1, Name: \"keys\", Type: %q, IsList: true}})\n", svcName, modelIDTypeName(m))
		}
	}
	b.WriteString("}\n\n")
}

func collectBatchLoadModels(crudModels, allModels []*ast.ModelDecl) []*ast.ModelDecl {
	selected := make(map[string]bool, len(crudModels))
	result := append([]*ast.ModelDecl(nil), crudModels...)
	for _, model := range crudModels {
		selected[model.Name] = true
	}
	if globalEventCtx == nil {
		return result
	}
	for _, model := range allModels {
		if globalEventCtx.remotePKModels[model.Name] && !selected[model.Name] {
			selected[model.Name] = true
			result = append(result, model)
		}
	}
	return result
}

func remoteLoadCallsForResult(result *semantic.Result) []loadCallInfo {
	if globalEventCtx == nil || len(result.Files) == 0 {
		return nil
	}
	return globalEventCtx.remoteLoadCalls[moduleNameFromFile(result.Files[0].Name)]
}

// generateRemoteNamedLoadHandlers emits internal RPC endpoints used by
// cross-module Model.load(field: value, ...) calls.
func generateRemoteNamedLoadHandlers(b *strings.Builder, result *semantic.Result, models []*ast.ModelDecl, calls []loadCallInfo, enumSets ...map[string]bool) {
	if len(calls) == 0 {
		return
	}
	modelByName := make(map[string]*ast.ModelDecl, len(models))
	for _, model := range models {
		modelByName[model.Name] = model
	}
	seen := make(map[string]bool)
	var generated []loadCallInfo
	for _, call := range calls {
		serviceName := loadServiceName(call)
		if seen[serviceName] || modelByName[call.modelName] == nil {
			continue
		}
		seen[serviceName] = true
		generated = append(generated, call)
		generateRemoteNamedLoadHandler(b, modelByName[call.modelName], call, firstBoolSet(enumSets))
	}
	if len(generated) == 0 {
		return
	}
	b.WriteString("// RegisterRemoteLoaders registers named cross-module load endpoints.\n")
	b.WriteString("func RegisterRemoteLoaders(router *api.Router, app *App) {\n")
	for _, call := range generated {
		serviceName := loadServiceName(call)
		handlerName := "handleLoad" + loaderNameFromLoadCall(call)
		fmt.Fprintf(b, "\trouter.Handle(%q, %s(app))\n", serviceName, handlerName)
		if apiID := getAPIID(serviceName); apiID > 0 {
			fmt.Fprintf(b, "\trouter.Registry.Register(%q, %d)\n", serviceName, apiID)
			fmt.Fprintf(b, "\trouter.Registry.RegisterParams(%q, []api.ParamMeta{\n", serviceName)
			for i, argName := range call.argNames {
				fmt.Fprintf(b, "\t\t{FieldID: %d, Name: %q, Type: %q, IsList: true},\n",
					getAPIParamID(serviceName, argName), argName, call.argTypeNames[i])
			}
			b.WriteString("\t})\n")
		}
	}
	b.WriteString("}\n\n")
	_ = result
}

func generateRemoteNamedLoadHandler(b *strings.Builder, model *ast.ModelDecl, call loadCallInfo, enumSets ...map[string]bool) {
	loaderName := loaderNameFromLoadCall(call)
	handlerName := "handleLoad" + loaderName
	keyType := call.argTypes[0]
	keyTypeName := ""
	if len(call.argNames) > 1 {
		keyTypeName = "load" + loaderName + "Key"
		keyType = keyTypeName
		fmt.Fprintf(b, "type %s struct {\n", keyTypeName)
		for i, argName := range call.argNames {
			fmt.Fprintf(b, "\t%s %s\n", str.Capitalize(argName), call.argTypes[i])
		}
		b.WriteString("}\n\n")
	}
	fmt.Fprintf(b, "// %s handles %s.\n", handlerName, loadServiceName(call))
	fmt.Fprintf(b, "func %s(app *App) api.HandlerFunc {\n", handlerName)
	b.WriteString("\treturn func(ctx context.Context, req *api.Request) error {\n")
	for i, argName := range call.argNames {
		fmt.Fprintf(b, "\t\t%sKeys, err := req.Param%s(\"%s\")\n", argName, idArrayParamMethod(call.argTypeNames[i]), argName)
		b.WriteString("\t\tif err != nil { return err }\n")
		if i > 0 {
			fmt.Fprintf(b, "\t\tif len(%sKeys) != len(%sKeys) {\n", argName, call.argNames[0])
			fmt.Fprintf(b, "\t\t\treturn errors.BadRequest.WithData(errors.ParamError{Param: %q, Error: %q})\n",
				argName, "array length must match "+call.argNames[0])
			b.WriteString("\t\t}\n")
		}
	}
	generateSelectedSQLFields(b, model, firstBoolSet(enumSets))
	for _, argName := range call.argNames {
		generateRequiredSQLField(b, str.ToSnakeCase(argName))
	}
	if len(call.argNames) == 1 {
		fmt.Fprintf(b, "\t\tconds := []lux.Condition{lux.New%s(%q).In(%sKeys...)}\n",
			goTypeToCondField(call.argTypes[0]), str.ToSnakeCase(call.argNames[0]), call.argNames[0])
	} else {
		generateCompositeLoadConditions(b, "\t\t", call, true)
	}
	if isSoftDelete(model) {
		b.WriteString("\t\tconds = append(conds, lux.NewTimeField(\"deleted_at\").IsNull())\n")
	}
	fmt.Fprintf(b, "\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", str.ToSnakeCase(model.Name)+"s")
	fmt.Fprintf(b, "\t\trows, err := %s.QueryRows(ctx, app.DB, scan%s, query, args...)\n", dbPkg, model.Name)
	b.WriteString("\t\tif err != nil { return err }\n")
	writeComputedResolve(b, model, "rows", "\t\t")
	fmt.Fprintf(b, "\t\tgrouped := make(map[%s][]*%s, len(%sKeys))\n", keyType, model.Name, call.argNames[0])
	b.WriteString("\t\tfor _, row := range rows {\n")
	fmt.Fprintf(b, "\t\t\tkey := %s\n", loadKeyExpr(call, "row."))
	b.WriteString("\t\t\tgrouped[key] = append(grouped[key], row)\n")
	b.WriteString("\t\t}\n")
	fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(%sKeys)))\n", call.argNames[0])
	fmt.Fprintf(b, "\t\tfor i := range %sKeys {\n", call.argNames[0])
	fmt.Fprintf(b, "\t\t\tkey := %s\n", requestLoadKeyExpr(call, keyTypeName))
	b.WriteString("\t\t\titems := grouped[key]\n")
	b.WriteString("\t\t\treq.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(items)))\n")
	b.WriteString("\t\t\ttmp := api.GetBuf()\n")
	b.WriteString("\t\t\tfor _, item := range items {\n")
	b.WriteString("\t\t\t\ttmp.Reset()\n")
	b.WriteString("\t\t\t\titem.WriteLuxo(tmp, req.FieldMask)\n")
	b.WriteString("\t\t\t\treq.Buf.B = codec.AppendBytes(req.Buf.B, tmp.B)\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tapi.PutBuf(tmp)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\treturn nil\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")
}

func loadKeyExpr(call loadCallInfo, prefix string) string {
	if len(call.argNames) == 1 {
		return prefix + str.Capitalize(call.argNames[0])
	}
	var b strings.Builder
	b.WriteString("load" + loaderNameFromLoadCall(call) + "Key{")
	for i, argName := range call.argNames {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s: %s%s", str.Capitalize(argName), prefix, str.Capitalize(argName))
	}
	b.WriteByte('}')
	return b.String()
}

func requestLoadKeyExpr(call loadCallInfo, keyTypeName string) string {
	if len(call.argNames) == 1 {
		return call.argNames[0] + "Keys[i]"
	}
	var b strings.Builder
	b.WriteString(keyTypeName + "{")
	for i, argName := range call.argNames {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s: %sKeys[i]", str.Capitalize(argName), argName)
	}
	b.WriteByte('}')
	return b.String()
}

func idArrayParamMethod(typeName string) string {
	switch typeName {
	case "String":
		return "StringArray"
	case "UUID":
		return "UUIDArray"
	default:
		return "IntArray"
	}
}

func generateSelectedSQLFields(b *strings.Builder, model *ast.ModelDecl, enumSets ...map[string]bool) {
	enums := firstBoolSet(enumSets)
	b.WriteString("\t\tfieldMask := codec.SelectionMaskFields(req.FieldMask)\n")
	b.WriteString("\t\tvar fields []string\n")
	b.WriteString("\t\tif len(fieldMask) > 0 {\n")
	b.WriteString("\t\t\tfields = make([]string, 0, len(fieldMask))\n")
	fmt.Fprintf(b, "\t\t\tfields = append(fields, %q)\n", str.ToSnakeCase(primaryKeyFieldName(model)))
	for _, field := range model.Fields {
		if localKey, ok := computedFieldLocalKey(model, field, enums); ok {
			if fieldID := getModelFieldID(model.Name, field.Name); fieldID > 0 {
				fmt.Fprintf(b, "\t\t\tif codec.FieldMaskHas(fieldMask, %d) { fields = ensureSelectedColumn(fields, %q) }\n",
					fieldID, str.ToSnakeCase(localKey))
			}
			continue
		}
		if field.Name == primaryKeyFieldName(model) || field.Computed != nil || isRelationField(field, enums) {
			continue
		}
		fieldID := getModelFieldID(model.Name, field.Name)
		if fieldID <= 0 {
			continue
		}
		fmt.Fprintf(b, "\t\t\tif codec.FieldMaskHas(fieldMask, %d) { fields = append(fields, %q) }\n",
			fieldID, str.ToSnakeCase(field.Name))
	}
	b.WriteString("\t\t}\n")
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
	type registeredParam struct {
		name string
		id   int
	}
	registered := make([]registeredParam, 0, len(params))
	activeTypes, hasActiveTypes := apiParamTypes[strings.TrimPrefix(name, "svc:")]
	for paramName, paramID := range params {
		if hasActiveTypes {
			if _, ok := activeTypes[paramName]; !ok {
				continue
			}
		}
		registered = append(registered, registeredParam{name: paramName, id: paramID})
	}
	if len(registered) == 0 {
		return
	}
	sort.Slice(registered, func(i, j int) bool { return registered[i].id < registered[j].id })
	fmt.Fprintf(b, "\trouter.Registry.RegisterParams(%q, []api.ParamMeta{\n", name)
	for _, param := range registered {
		ptype, isList, nullable := resolveParamMetaFromAST(name, param.name)
		fmt.Fprintf(b, "\t\t{Name: %q, Type: %q, FieldID: %d", param.name, ptype, param.id)
		if isList {
			b.WriteString(", IsList: true")
		}
		if nullable {
			b.WriteString(", Nullable: true")
		}
		b.WriteString("},\n")
	}
	b.WriteString("\t})\n")
}

// resolveParamTypeFromAST looks up the actual Luxo type for a param from AST data.
// Falls back to inferParamType heuristic if no AST info available.
func resolveParamTypeFromAST(apiName, paramName string) string {
	typeName, _, _ := resolveParamMetaFromAST(apiName, paramName)
	return typeName
}

func resolveParamMetaFromAST(apiName, paramName string) (string, bool, bool) {
	if apiParamTypes != nil {
		lookupName := strings.TrimPrefix(apiName, "svc:")
		if params, ok := apiParamTypes[lookupName]; ok {
			if t, ok := params[paramName]; ok {
				nullable := strings.HasSuffix(t, "?")
				t = strings.TrimSuffix(t, "?")
				if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
					return strings.TrimSuffix(strings.TrimPrefix(t, "["), "]"), true, nullable
				}
				return t, false, nullable
			}
		}
	}
	return inferParamType(paramName), false, false
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
	if field := primaryKeyField(m); field != nil {
		return resolveGoType(field.Type)
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

func paramJSONMethod(param *ast.ParamDecl) string {
	if param.Type != nil && param.Type.Nullable {
		if param.Default != nil {
			return "ParamJSONOptionalNullable"
		}
		return "ParamJSONNullable"
	}
	if param.Default != nil {
		return "ParamJSONOptional"
	}
	return "ParamJSON"
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

func writeCRUDAuthCheck(b *strings.Builder, indent string, modelAuth *ast.Directive) {
	if modelAuth != nil {
		writeAuthCheck(b, indent, modelAuth)
		return
	}
	writeAuthCheck(b, indent)
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
		fmt.Fprintf(b, "%scase ", indent)
		for index, role := range roles {
			if index > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%q", role)
		}
		b.WriteString(":\n")
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

// detectAuthNeeded checks if luvia import is needed in handler.gen.go.
func detectAuthNeeded(result *semantic.Result, models []*ast.ModelDecl) bool {
	for _, m := range models {
		if hasDirective(m.Directives, "withAuth") || hasDirective(m.Directives, "auth") {
			return true
		}
	}
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if hasDirective(api.Directives, "auth") {
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
		for _, stmt := range d.Body.Stmts {
			switch s := stmt.(type) {
			case *ast.AssignStmt:
				// it = expr → varName = compiledExpr
				if s.Value == nil {
					continue
				}
				code := compileFieldExpr(s.Value, varName)
				if code != "" {
					fmt.Fprintf(b, "%s%s = %s\n", indent, varName, code)
				}
			case *ast.ExprStmt:
				// it.trim() → varName = strings.TrimSpace(varName)
				if s.Expr == nil {
					continue
				}
				code := compileFieldExpr(s.Expr, varName)
				if code != "" {
					fmt.Fprintf(b, "%s%s = %s\n", indent, varName, code)
				}
			}
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
		// Free function call: slugify(it) → slugify(varName)
		if ident, ok := e.Func.(*ast.Ident); ok {
			args := make([]string, 0, len(e.Args))
			for _, a := range e.Args {
				compiled := compileFieldExpr(a.Value, itVar)
				if compiled == "" {
					return ""
				}
				args = append(args, compiled)
			}
			return fmt.Sprintf("%s(%s)", ident.Name, strings.Join(args, ", "))
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

type computedAggregate struct {
	field     *ast.FieldDecl
	directive string
	targetCol string
	fieldID   int
}

type computedAggregateGroup struct {
	relation Relation
	target   *ast.ModelDecl
	fields   []computedAggregate
}

func modelHasComputedAggregates(model *ast.ModelDecl) bool {
	if model == nil {
		return false
	}
	for _, field := range model.Fields {
		if field.Computed == nil {
			continue
		}
		for _, directive := range field.Computed.Directives {
			switch directive.Name {
			case "count", "sum", "avg", "min", "max":
				return true
			}
		}
	}
	return false
}

func writeComputedResolve(b *strings.Builder, model *ast.ModelDecl, itemsExpr, indent string) {
	if !modelHasComputedAggregates(model) {
		return
	}
	fmt.Fprintf(b, "%sif err := resolve%sComputed(ctx, app, %s, req.FieldMask); err != nil { return err }\n",
		indent, model.Name, itemsExpr)
}

func generateComputedResolvers(b *strings.Builder, models []*ast.ModelDecl, modelMap map[string]*ast.ModelDecl, enums map[string]bool) {
	for _, model := range models {
		groups := collectComputedAggregateGroups(model, modelMap, enums)
		if len(groups) == 0 {
			continue
		}
		generateComputedResolver(b, model, groups)
		generateComputedFieldResolver(b, model, groups)
		for _, group := range groups {
			generateComputedGroupResolver(b, model, group)
		}
	}
}

func generateComputedFieldResolver(b *strings.Builder, model *ast.ModelDecl, groups []computedAggregateGroup) {
	fmt.Fprintf(b, "func resolve%sComputedFields(ctx context.Context, app *App, items []*%s, fields []*selection.Field) error {\n", model.Name, model.Name)
	b.WriteString("\tif len(items) == 0 { return nil }\n")
	fmt.Fprintf(b, "\tif len(fields) == 0 { return resolve%sComputed(ctx, app, items, nil) }\n", model.Name)
	for _, group := range groups {
		fmt.Fprintf(b, "\tvar need%s bool\n", str.Capitalize(group.relation.FieldName))
	}
	b.WriteString("\tfor _, field := range fields {\n\t\tswitch field.Name {\n")
	for _, group := range groups {
		for _, aggregate := range group.fields {
			fmt.Fprintf(b, "\t\tcase %q: need%s = true\n", aggregate.field.Name, str.Capitalize(group.relation.FieldName))
		}
	}
	b.WriteString("\t\t}\n\t}\n")
	for _, group := range groups {
		fmt.Fprintf(b, "\tif need%s {\n", str.Capitalize(group.relation.FieldName))
		fmt.Fprintf(b, "\t\tif err := resolve%s%sComputed(ctx, app, items, nil); err != nil { return err }\n",
			model.Name, str.Capitalize(group.relation.FieldName))
		b.WriteString("\t}\n")
	}
	b.WriteString("\treturn nil\n}\n\n")
}

func collectComputedAggregateGroups(model *ast.ModelDecl, modelMap map[string]*ast.ModelDecl, enums map[string]bool) []computedAggregateGroup {
	relations := make(map[string]Relation)
	for _, relation := range analyzeRelations(model, enums) {
		if relation.IsList {
			relations[relation.FieldName] = relation
		}
	}
	var groups []computedAggregateGroup
	groupIndexes := make(map[string]int)
	for _, field := range model.Fields {
		aggregate, relationName, ok := parseComputedAggregate(model.Name, field)
		relation, exists := relations[relationName]
		if !ok || !exists || modelMap[relation.TargetName] == nil {
			continue
		}
		index, exists := groupIndexes[relationName]
		if !exists {
			index = len(groups)
			groupIndexes[relationName] = index
			groups = append(groups, computedAggregateGroup{relation: relation, target: modelMap[relation.TargetName]})
		}
		groups[index].fields = append(groups[index].fields, aggregate)
	}
	return groups
}

func parseComputedAggregate(modelName string, field *ast.FieldDecl) (computedAggregate, string, bool) {
	if field.Computed == nil || field.Type == nil {
		return computedAggregate{}, "", false
	}
	fieldID := getModelFieldID(modelName, field.Name)
	if fieldID == 0 {
		return computedAggregate{}, "", false
	}
	for _, directive := range field.Computed.Directives {
		if len(directive.Args) != 1 {
			continue
		}
		relation, target, ok := computedAggregateTarget(directive.Name, directive.Args[0].Value)
		if ok {
			return computedAggregate{field: field, directive: directive.Name, targetCol: target, fieldID: fieldID}, relation, true
		}
	}
	return computedAggregate{}, "", false
}

func computedAggregateTarget(name string, expr ast.Expr) (relation, target string, ok bool) {
	if name == "count" {
		ident, valid := expr.(*ast.Ident)
		if !valid {
			return "", "", false
		}
		return ident.Name, "", true
	}
	if name != "sum" && name != "avg" && name != "min" && name != "max" {
		return "", "", false
	}
	member, valid := expr.(*ast.MemberExpr)
	if !valid {
		return "", "", false
	}
	ident, valid := member.Object.(*ast.Ident)
	if !valid {
		return "", "", false
	}
	return ident.Name, str.ToSnakeCase(member.Field), true
}

func generateComputedResolver(b *strings.Builder, model *ast.ModelDecl, groups []computedAggregateGroup) {
	fmt.Fprintf(b, "func resolve%sComputed(ctx context.Context, app *App, items []*%s, selectionMask []byte) error {\n", model.Name, model.Name)
	b.WriteString("\tif len(items) == 0 { return nil }\n")
	b.WriteString("\tfieldMask := codec.SelectionMaskFields(selectionMask)\n")
	for _, group := range groups {
		fmt.Fprintf(b, "\tif %s {\n", computedMaskExpr(group.fields))
		fmt.Fprintf(b, "\t\tif err := resolve%s%sComputed(ctx, app, items, fieldMask); err != nil { return err }\n",
			model.Name, str.Capitalize(group.relation.FieldName))
		b.WriteString("\t}\n")
	}
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")
}

func computedMaskExpr(fields []computedAggregate) string {
	var b strings.Builder
	b.WriteString("len(fieldMask) == 0")
	for _, field := range fields {
		fmt.Fprintf(&b, " || codec.FieldMaskHas(fieldMask, %d)", field.fieldID)
	}
	return b.String()
}

func generateComputedGroupResolver(b *strings.Builder, model *ast.ModelDecl, group computedAggregateGroup) {
	valueType := str.LowerFirst(model.Name) + str.Capitalize(group.relation.FieldName) + "ComputedValue"
	fmt.Fprintf(b, "type %s struct {\n", valueType)
	for _, aggregate := range group.fields {
		fmt.Fprintf(b, "\t%s %s\n", str.Capitalize(aggregate.field.Name), resolveGoType(aggregate.field.Type))
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(b, "func resolve%s%sComputed(ctx context.Context, app *App, items []*%s, fieldMask []byte) error {\n",
		model.Name, str.Capitalize(group.relation.FieldName), model.Name)
	generateComputedKeys(b, model, group.relation)
	generateComputedQuery(b, group)
	generateComputedAssignments(b, model, group, valueType)
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")
}

func generateComputedKeys(b *strings.Builder, model *ast.ModelDecl, relation Relation) {
	goKey := str.Capitalize(relation.LocalKey)
	fmt.Fprintf(b, "\tkeys := make([]%s, 0, len(items))\n", relation.KeyGoType)
	b.WriteString("\tfor _, item := range items {\n")
	b.WriteString("\t\tif item == nil { continue }\n")
	fmt.Fprintf(b, "\t\tkeys = append(keys, item.%s)\n", goKey)
	b.WriteString("\t}\n")
	b.WriteString("\tif len(keys) == 0 { return nil }\n")
}

func generateComputedQuery(b *strings.Builder, group computedAggregateGroup) {
	query := computedGroupSQL(group)
	fmt.Fprintf(b, "\tquery := `%s`\n", query)
	b.WriteString("\trows, err := pg.QueryRaw(ctx, app.DB, query, keys)\n")
	b.WriteString("\tif err != nil { return err }\n")
	b.WriteString("\tdefer rows.Close()\n")
}

func computedGroupSQL(group computedAggregateGroup) string {
	var b strings.Builder
	fmt.Fprintf(&b, `SELECT "%s"`, str.ToSnakeCase(group.relation.RemoteKey))
	for _, aggregate := range group.fields {
		b.WriteString(", ")
		b.WriteString(computedAggregateSQLExpr(aggregate))
	}
	fmt.Fprintf(&b, ` FROM "%s" WHERE "%s" = ANY($1)`, str.ToSnakeCase(group.target.Name)+"s", str.ToSnakeCase(group.relation.RemoteKey))
	if isSoftDelete(group.target) {
		b.WriteString(` AND "deleted_at" IS NULL`)
	}
	fmt.Fprintf(&b, ` GROUP BY "%s"`, str.ToSnakeCase(group.relation.RemoteKey))
	return b.String()
}

func computedAggregateSQLExpr(aggregate computedAggregate) string {
	if aggregate.directive == "count" {
		return "COUNT(*)::bigint"
	}
	cast := map[string]string{"Int": "bigint", "Float": "double precision", "Decimal": "numeric"}[aggregate.field.Type.Name]
	return fmt.Sprintf(`COALESCE(%s("%s"), 0)::%s`, strings.ToUpper(aggregate.directive), aggregate.targetCol, cast)
}

func generateComputedAssignments(b *strings.Builder, model *ast.ModelDecl, group computedAggregateGroup, valueType string) {
	fmt.Fprintf(b, "\tvalues := make(map[%s]%s, len(items))\n", group.relation.KeyGoType, valueType)
	b.WriteString("\tfor rows.Next() {\n")
	fmt.Fprintf(b, "\t\tvar key %s\n", group.relation.KeyGoType)
	fmt.Fprintf(b, "\t\tvar value %s\n", valueType)
	b.WriteString("\t\tif err := rows.Scan(&key")
	for _, aggregate := range group.fields {
		fmt.Fprintf(b, ", &value.%s", str.Capitalize(aggregate.field.Name))
	}
	b.WriteString("); err != nil { return err }\n")
	b.WriteString("\t\tvalues[key] = value\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif err := rows.Err(); err != nil { return err }\n")
	fmt.Fprintf(b, "\tfor _, item := range items {\n\t\tif item == nil { continue }\n\t\tvalue, ok := values[item.%s]\n\t\tif !ok { continue }\n", str.Capitalize(group.relation.LocalKey))
	for _, aggregate := range group.fields {
		fmt.Fprintf(b, "\t\tif len(fieldMask) == 0 || codec.FieldMaskHas(fieldMask, %d) { item.%s = value.%s }\n",
			aggregate.fieldID, str.Capitalize(aggregate.field.Name), str.Capitalize(aggregate.field.Name))
	}
	b.WriteString("\t}\n")
}

// scanBodyForBuiltins walks AST to find crypto.*, now(), duration property usage.
func scanBodyForBuiltins(block *ast.Block, f *handlerFeatures, currentModule ...string) {
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
			case "i", "d", "w", "e":
				// Only count as log if object is a string literal or template
				if _, isLit := v.Object.(*ast.Literal); isLit {
					f.hasLog = true
				}
				if _, isTmpl := v.Object.(*ast.TemplateString); isTmpl {
					f.hasLog = true
				}
			}
		case *ast.CallExpr:
			if ident, ok := v.Func.(*ast.Ident); ok && ident.Name == "now" {
				f.hasTimeFunc = true
			}
		}
	})
	// Scan statements for emit (recursive into nested blocks)
	curMod := ""
	if len(currentModule) > 0 {
		curMod = currentModule[0]
	}
	scanStmtsForEmit(block.Stmts, f, curMod)
}

// scanStmtsForEmit recursively walks statements to find EmitStmt in nested blocks.
func scanStmtsForEmit(stmts []ast.Stmt, f *handlerFeatures, curMod string) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.EmitStmt:
			f.hasEmit = true
			if globalEventCtx != nil {
				evModule := globalEventCtx.EventModule[s.EventName]
				if evModule != "" && evModule != curMod {
					if f.crossEventImports == nil {
						f.crossEventImports = make(map[string]string)
					}
					f.crossEventImports[evModule] = evModule + "_luxo"
				}
			}
		case *ast.IfStmt:
			if s.Then != nil {
				scanStmtsForEmit(s.Then.Stmts, f, curMod)
			}
		case *ast.ForStmt:
			if s.Body != nil {
				scanStmtsForEmit(s.Body.Stmts, f, curMod)
			}
		case *ast.ExprStmt:
			// Transaction/Async/Await expressions contain nested blocks
			switch expr := s.Expr.(type) {
			case *ast.TransactionExpr:
				if expr.Body != nil {
					scanStmtsForEmit(expr.Body.Stmts, f, curMod)
				}
			case *ast.AsyncExpr:
				if expr.Body != nil {
					scanStmtsForEmit(expr.Body.Stmts, f, curMod)
				}
			case *ast.AwaitExpr:
				if expr.Body != nil {
					scanStmtsForEmit(expr.Body.Stmts, f, curMod)
				}
			}
		}
	}
}

// --- Federation resolve endpoints ---

// resolveEndpoint describes a svc:resolve:{Model}:{FK} endpoint to generate.
type resolveEndpoint struct {
	model      *ast.ModelDecl
	modelName  string // model being resolved (e.g. "Post")
	tableName  string // SQL table (e.g. "posts")
	fkField    string // FK field on the model (e.g. "userId")
	fkColumn   string // FK SQL column (e.g. "user_id")
	svcName    string // endpoint name (e.g. "svc:resolve:Post:userId")
	scanFn     string // scan function (e.g. "scanPost")
	isList     bool   // true for hasMany (multiple items per FK)
	keyType    string // Luxo primary-key type of the extended model
	keyGoType  string // Go primary-key type used by the target foreign key
	fkNullable bool   // nullable foreign keys are absent from federation groups
}

// generateFederationResolvers generates svc:resolve:{Model}:{FK} endpoints
// for each cross-module extend relationship defined in this module.
//
// Example: Post module has `extend User { posts: [Post] @hasMany }`
// → generates svc:resolve:Post:userId handler that does:
//
//	WHERE user_id IN (keys) → group by user_id → write grouped response
//
// Response format:
//
//	[key_count varint]
//	For each key (in request order):
//	  [item_count varint]
//	  [item1 WriteLuxo] [item2 WriteLuxo] ...
func generateFederationResolvers(b *strings.Builder, result *semantic.Result, models []*ast.ModelDecl, enums map[string]bool) {
	curModule := ""
	if len(result.Files) > 0 {
		curModule = moduleNameFromFile(result.Files[0].Name)
	}

	// Find model names owned by this module (for FK inference)
	modelNames := make(map[string]bool)
	modelDecls := make(map[string]*ast.ModelDecl)
	for _, m := range models {
		modelNames[m.Name] = true
		modelDecls[m.Name] = m
	}

	// Collect resolve endpoints from extends in this module
	var endpoints []resolveEndpoint
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		if modName != curModule {
			continue
		}
		for _, ext := range file.Extends {
			// Only generate if the extended model is NOT owned by this module
			// (cross-module extend)
			if modelNames[ext.Name] {
				continue
			}
			for _, f := range ext.Fields {
				if f.Type == nil || ownerModelHasField(ext.Name, f.Name) || !isRelationField(f, enums) {
					continue
				}
				// This field is a relation (e.g. posts: [Post])
				// The target model must be in this module
				if !modelNames[f.Type.Name] {
					continue
				}
				fk := inferFederationForeignKey(&ast.ModelDecl{Name: ext.Name}, f)
				keyType := externalModelIDTypeName(ext.Name)
				fkField := modelFieldByName(modelDecls[f.Type.Name], fk)
				ep := resolveEndpoint{
					model:      modelDecls[f.Type.Name],
					modelName:  f.Type.Name,
					tableName:  str.ToSnakeCase(f.Type.Name) + "s",
					fkField:    fk,
					fkColumn:   str.ToSnakeCase(fk),
					svcName:    "svc:resolve:" + f.Type.Name + ":" + fk,
					scanFn:     "scan" + f.Type.Name,
					isList:     f.Type.IsList,
					keyType:    keyType,
					keyGoType:  mapBaseType(keyType),
					fkNullable: fkField != nil && fkField.Type != nil && fkField.Type.Nullable,
				}
				endpoints = append(endpoints, ep)
			}
		}
	}

	// Always emit RegisterFederationResolvers below (even with zero endpoints)
	// so the entry point's unconditional per-module call compiles. Endpoint
	// handlers are only generated when cross-module extends exist.

	// Generate handler for each endpoint
	for _, ep := range endpoints {
		fmt.Fprintf(b, "// handleResolve%sBy%s handles %s — federation resolve.\n",
			ep.modelName, str.Capitalize(ep.fkField), ep.svcName)
		fmt.Fprintf(b, "// WHERE %s IN (keys) → group by %s → write grouped response.\n",
			ep.fkColumn, ep.fkColumn)
		fmt.Fprintf(b, "func handleResolve%sBy%s(app *App) api.HandlerFunc {\n",
			ep.modelName, str.Capitalize(ep.fkField))
		fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
		fmt.Fprintf(b, "\t\tkeys, err := req.Param%sArray(\"keys\")\n", ep.keyType)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		generateSelectedSQLFields(b, ep.model, enums)
		generateRequiredSQLField(b, ep.fkColumn)
		// Query: WHERE fk_column IN (keys)
		fmt.Fprintf(b, "\t\tconds := []lux.Condition{lux.New%sField(%q).In(keys...)}\n", ep.keyType, ep.fkColumn)
		fmt.Fprintf(b, "\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", ep.tableName)
		fmt.Fprintf(b, "\t\trows, err := %s.QueryRows(ctx, app.DB, %s, query, args...)\n", dbPkg, ep.scanFn)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\n")
		writeComputedResolve(b, ep.model, "rows", "\t\t")

		// Group by FK field
		fmt.Fprintf(b, "\t\t// Group results by FK value, preserving request key order\n")
		fmt.Fprintf(b, "\t\tgrouped := make(map[%s][]*%s, len(keys))\n", ep.keyGoType, ep.modelName)
		fmt.Fprintf(b, "\t\tfor _, row := range rows {\n")
		fmt.Fprintf(b, "\t\t\tfk := row.%s\n", str.Capitalize(ep.fkField))
		if ep.fkNullable {
			fmt.Fprintf(b, "\t\t\tif fk == nil { continue }\n")
			fmt.Fprintf(b, "\t\t\tgrouped[*fk] = append(grouped[*fk], row)\n")
		} else {
			fmt.Fprintf(b, "\t\t\tgrouped[fk] = append(grouped[fk], row)\n")
		}
		fmt.Fprintf(b, "\t\t}\n\n")

		// Write grouped response: [key_count][per-key: [item_count][len+item1][len+item2]...]
		// Each item is length-prefixed so gateway can split without schema knowledge.
		fmt.Fprintf(b, "\t\t// Write grouped response (key order matches request order)\n")
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(keys)))\n")
		fmt.Fprintf(b, "\t\tfor _, key := range keys {\n")
		fmt.Fprintf(b, "\t\t\titems := grouped[key]\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(items)))\n")
		fmt.Fprintf(b, "\t\t\ttmp := api.GetBuf()\n")
		fmt.Fprintf(b, "\t\t\tfor _, item := range items {\n")
		fmt.Fprintf(b, "\t\t\t\t// Length-prefix: write to pooled buf, then [len][data]\n")
		fmt.Fprintf(b, "\t\t\t\ttmp.Reset()\n")
		fmt.Fprintf(b, "\t\t\t\titem.WriteLuxo(tmp, req.FieldMask)\n")
		fmt.Fprintf(b, "\t\t\t\treq.Buf.B = codec.AppendBytes(req.Buf.B, tmp.B)\n")
		fmt.Fprintf(b, "\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\tapi.PutBuf(tmp)\n")
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n}\n\n")
	}

	// RegisterFederationResolvers function
	b.WriteString("// RegisterFederationResolvers registers federation resolve RPC endpoints.\n")
	b.WriteString("// Gateway calls these to resolve cross-module extend fields.\n")
	b.WriteString("func RegisterFederationResolvers(router *api.Router, app *App) {\n")
	for _, ep := range endpoints {
		fmt.Fprintf(b, "\trouter.Handle(%q, handleResolve%sBy%s(app))\n",
			ep.svcName, ep.modelName, str.Capitalize(ep.fkField))
		// Register with API ID so gateway RPC can call by ID
		apiID := getAPIID(ep.svcName)
		if apiID > 0 {
			fmt.Fprintf(b, "\trouter.Registry.Register(%q, %d)\n", ep.svcName, apiID)
			fmt.Fprintf(b, "\trouter.Registry.RegisterParams(%q, []api.ParamMeta{{FieldID: 1, Name: \"keys\", Type: %q, IsList: true}})\n", ep.svcName, ep.keyType)
		}
	}
	b.WriteString("}\n\n")
}

func modelFieldByName(model *ast.ModelDecl, name string) *ast.FieldDecl {
	if model == nil {
		return nil
	}
	for _, field := range model.Fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

func generateRequiredSQLField(b *strings.Builder, column string) {
	b.WriteString("\t\tif fields != nil {\n")
	b.WriteString("\t\t\thasRequiredField := false\n")
	fmt.Fprintf(b, "\t\t\tfor _, field := range fields { if field == %q { hasRequiredField = true; break } }\n", column)
	fmt.Fprintf(b, "\t\t\tif !hasRequiredField { fields = append(fields, %q) }\n", column)
	b.WriteString("\t\t}\n")
}
