package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// RelationType describes how two models are related.
type RelationType int

const (
	BelongsTo RelationType = iota // user: User, FK = userId (on this model)
	HasMany                       // posts: [Post], FK = Post.userId (on the other model)
	HasOne                        // profile: Profile, FK = Profile.userId (on the other model)
)

// Relation describes a single model relationship.
type Relation struct {
	FieldName  string       // field name in the model (e.g., "user", "posts")
	TargetName string       // target model name (e.g., "User", "Post")
	Type       RelationType // belongsTo, hasMany, hasOne
	LocalKey   string       // key on this model (e.g., "userId" for belongsTo, "id" for hasMany)
	RemoteKey  string       // key on target model (e.g., "id" for belongsTo, "userId" for hasMany)
	IsList     bool         // [Post] vs Post
	FKNullable bool         // true if the FK field is nullable (e.g., userId: Int?)
	KeyGoType  string       // Go type of the local key (e.g., "int64", "uuid.UUID")
}

// analyzeRelations extracts all relations from a model's fields.
func analyzeRelations(m *ast.ModelDecl, enums map[string]bool) []Relation {
	var relations []Relation
	for _, f := range m.Fields {
		if f.Type == nil || f.Computed != nil {
			continue
		}
		if !isRelationField(f, enums) {
			continue
		}

		rel := Relation{
			FieldName:  f.Name,
			TargetName: f.Type.Name,
			IsList:     f.Type.IsList,
		}

		// Check for explicit @by
		byDirective := findDirective(f.Directives, "by")
		if byDirective != nil {
			rel.RemoteKey, rel.LocalKey = extractByArgs(byDirective)
			if rel.IsList {
				rel.Type = HasMany
			} else if hasFKField(m.Fields, rel.LocalKey) {
				rel.Type = BelongsTo
			} else {
				rel.Type = HasOne
			}
		} else {
			// Auto-infer
			if rel.IsList {
				rel.Type = HasMany
				rel.RemoteKey = str.LowerFirst(m.Name) + "Id"
				rel.LocalKey = "id"
			} else {
				fkName := str.LowerFirst(rel.TargetName) + "Id"
				if hasFKField(m.Fields, fkName) {
					rel.Type = BelongsTo
					rel.LocalKey = fkName
					rel.RemoteKey = "id"
				} else {
					rel.Type = HasOne
					rel.RemoteKey = str.LowerFirst(m.Name) + "Id"
					rel.LocalKey = "id"
				}
			}
		}

		// Check if the FK field is nullable (e.g., userId: Int?)
		rel.FKNullable = isFKNullable(m.Fields, rel.LocalKey)

		// Determine Go type of the local key field
		rel.KeyGoType = fkGoType(m.Fields, rel.LocalKey)

		relations = append(relations, rel)
	}
	return relations
}

// loadCallInfo describes a unique load() call pattern found in .luxo code.
type loadCallInfo struct {
	modelName string   // "Post"
	argNames  []string // ["userId", "type"] — named args; empty = PK load
	argTypes  []string // Go type per argName, resolved from the model field decl
	//          (e.g. ["int64", "string"]); parallel to argNames.
}

// loaderNameFromLoadCall returns the DataLoader field name for a load call.
// Post.load(id) → "ExtendPost"
// Post.load(userId: x) → "PostByUserId"
// Post.load(userId: x, type: y) → "PostByUserIdAndType"
func loaderNameFromLoadCall(lc loadCallInfo) string {
	if len(lc.argNames) == 0 {
		return "Extend" + lc.modelName
	}
	name := lc.modelName + "By"
	for i, arg := range lc.argNames {
		if i > 0 {
			name += "And"
		}
		name += str.Capitalize(arg)
	}
	return name
}

// buildModelFieldIndex maps each model name to its field declarations, merging
// in extend fields. Used to resolve load() argument types from the schema.
func buildModelFieldIndex(result *semantic.Result) map[string][]*ast.FieldDecl {
	idx := make(map[string][]*ast.FieldDecl)
	for _, file := range result.Files {
		for _, m := range file.Models {
			idx[m.Name] = append(idx[m.Name], m.Fields...)
		}
		for _, e := range file.Extends {
			idx[e.Name] = append(idx[e.Name], e.Fields...)
		}
	}
	return idx
}

// collectLoadCalls scans all API/fn bodies for Model.load(...) calls
// and returns unique (model, args) combinations for DataLoader generation.
func collectLoadCalls(result *semantic.Result) []loadCallInfo {
	seen := make(map[string]bool) // dedup key: "Post:userId,type"
	var calls []loadCallInfo
	fieldIndex := buildModelFieldIndex(result)

	for _, file := range result.Files {
		// Scan API bodies
		for _, api := range file.APIs {
			if api.Body == nil {
				continue
			}
			for _, stmt := range api.Body.Stmts {
				scanLoadCalls(stmt, seen, &calls, fieldIndex)
			}
		}
		// Scan fn bodies
		for _, fn := range file.Functions {
			if fn.Body == nil {
				continue
			}
			for _, stmt := range fn.Body.Stmts {
				scanLoadCalls(stmt, seen, &calls, fieldIndex)
			}
		}
	}
	return calls
}

// scanLoadCalls recursively scans an AST statement for Model.load(...) calls.
func scanLoadCalls(stmt ast.Stmt, seen map[string]bool, calls *[]loadCallInfo, fieldIndex map[string][]*ast.FieldDecl) {
	switch s := stmt.(type) {
	case *ast.ValStmt:
		scanLoadCallsExpr(s.Value, seen, calls, fieldIndex)
	case *ast.ReturnStmt:
		if s.Value != nil {
			scanLoadCallsExpr(s.Value, seen, calls, fieldIndex)
		}
	case *ast.ExprStmt:
		scanLoadCallsExpr(s.Expr, seen, calls, fieldIndex)
	case *ast.IfStmt:
		if s.Then != nil {
			for _, st := range s.Then.Stmts {
				scanLoadCalls(st, seen, calls, fieldIndex)
			}
		}
	case *ast.ForStmt:
		if s.Body != nil {
			for _, st := range s.Body.Stmts {
				scanLoadCalls(st, seen, calls, fieldIndex)
			}
		}
	}
}

// scanLoadCallsExpr recursively scans an expression for Model.load(...) calls.
func scanLoadCallsExpr(expr ast.Expr, seen map[string]bool, calls *[]loadCallInfo, fieldIndex map[string][]*ast.FieldDecl) {
	if expr == nil {
		return
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "load" {
		return
	}
	ident, ok := member.Object.(*ast.Ident)
	if !ok {
		return
	}
	modelName := ident.Name
	// First char uppercase = model type
	if len(modelName) == 0 || modelName[0] < 'A' || modelName[0] > 'Z' {
		return
	}

	fields := fieldIndex[modelName]
	var argNames []string
	var argTypes []string
	for _, arg := range call.Args {
		if arg.Name != "" {
			argNames = append(argNames, arg.Name)
			// Resolve the key type from the model field decl (defaults to int64
			// for FK columns when the field can't be found).
			argTypes = append(argTypes, fkGoType(fields, arg.Name))
		}
	}

	// Dedup
	key := modelName + ":"
	for i, n := range argNames {
		if i > 0 {
			key += ","
		}
		key += n
	}
	if seen[key] {
		return
	}
	seen[key] = true
	*calls = append(*calls, loadCallInfo{modelName: modelName, argNames: argNames, argTypes: argTypes})
}

// collectExtendModels finds cross-module extended model names for load-by-PK DataLoader.
// For each `extend User { ... }` where User is from another module, we need a
// DataLoader that loads User by primary key.
func collectExtendModels(result *semantic.Result) []string {
	// Collect which models are defined in each module
	moduleModels := make(map[string]map[string]bool)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		if moduleModels[modName] == nil {
			moduleModels[modName] = make(map[string]bool)
		}
		for _, m := range file.Models {
			moduleModels[modName][m.Name] = true
		}
	}
	seen := make(map[string]bool)
	var models []string
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, ext := range file.Extends {
			if moduleModels[modName][ext.Name] {
				continue // same module, not cross-module
			}
			if seen[ext.Name] {
				continue
			}
			seen[ext.Name] = true
			models = append(models, ext.Name)
		}
	}
	return models
}

// generateDataLoaderFile produces dataloader.gen.go with loader types and batch function signatures.
func generateDataLoaderFile(result *semantic.Result, packageName string, enums map[string]bool, externalSoftModels map[string]bool, driver DBDriver) []byte {
	var allRelations []struct {
		modelName string
		relations []Relation
	}

	for _, file := range result.Files {
		for _, m := range file.Models {
			rels := analyzeRelations(m, enums)
			if len(rels) > 0 {
				allRelations = append(allRelations, struct {
					modelName string
					relations []Relation
				}{m.Name, rels})
			}
		}
	}

	// Collect cross-module extend models for load-by-PK DataLoader
	extendModels := collectExtendModels(result)

	// Collect multi-condition load() calls from API/fn bodies
	loadCalls := collectLoadCalls(result)
	// Filter out PK loads (already handled by extendModels) — keep only FK/multi-condition
	var fkLoadCalls []loadCallInfo
	for _, lc := range loadCalls {
		if len(lc.argNames) > 0 {
			fkLoadCalls = append(fkLoadCalls, lc)
		}
	}

	if len(allRelations) == 0 && len(extendModels) == 0 && len(fkLoadCalls) == 0 {
		return nil
	}

	// Merge local + external @soft model names for DataLoader filtering
	softModels := make(map[string]bool)
	for k, v := range externalSoftModels {
		softModels[k] = v
	}
	for _, file := range result.Files {
		for _, m := range file.Models {
			if isSoftDelete(m) {
				softModels[m.Name] = true
			}
		}
	}

	hasRemote := len(extendModels) > 0 || len(fkLoadCalls) > 0

	var b strings.Builder
	writeHeader(&b, packageName, "dataloader.gen.go")
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	if hasRemote {
		b.WriteString("\t\"fmt\"\n")
	}
	b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/dataloader\"\n")
	fmt.Fprintf(&b, "\tpg %q\n", driver.DriverImport())
	if hasRemote {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/rpc\"\n")
	}
	b.WriteString(")\n\n")

	// Generate batch function types (deduplicate by type name)
	seenTypes := make(map[string]bool)
	for _, mr := range allRelations {
		for _, rel := range mr.relations {
			typeName := loaderTypeName(mr.modelName, rel)
			if seenTypes[typeName] {
				continue
			}
			seenTypes[typeName] = true
			generateLoaderType(&b, mr.modelName, rel)
		}
	}

	// Generate extend model load-by-PK types
	for _, name := range extendModels {
		typeName := "Extend" + name + "ByIdLoader"
		if !seenTypes[typeName] {
			seenTypes[typeName] = true
			fmt.Fprintf(&b, "// %s is the batch function for loading %s by primary key (cross-module).\n", typeName, name)
			fmt.Fprintf(&b, "type %s = dataloader.BatchFn[int64, *%s]\n\n", typeName, name)
		}
	}

	// Generate FK/multi-condition load types and composite key structs
	for _, lc := range fkLoadCalls {
		loaderName := loaderNameFromLoadCall(lc)
		typeName := loaderName + "Loader"
		if seenTypes[typeName] {
			continue
		}
		seenTypes[typeName] = true

		if len(lc.argNames) == 1 {
			// Single FK: BatchFn[<keyType>, []*Post]
			fmt.Fprintf(&b, "// %s is the batch function for %s.load(%s: ...).\n", typeName, lc.modelName, lc.argNames[0])
			fmt.Fprintf(&b, "type %s = dataloader.BatchFn[%s, []*%s]\n\n", typeName, lc.argTypes[0], lc.modelName)
		} else {
			// Composite key: generate key struct + BatchFn
			keyType := loaderName + "Key"
			fmt.Fprintf(&b, "// %s is the composite key for %s.load(%s).\n", keyType, lc.modelName, strings.Join(lc.argNames, ", "))
			fmt.Fprintf(&b, "type %s struct {\n", keyType)
			for i, arg := range lc.argNames {
				// Field type resolved from the model declaration (FK → int64, etc.).
				fmt.Fprintf(&b, "\t%s %s\n", str.Capitalize(arg), lc.argTypes[i])
			}
			fmt.Fprintf(&b, "}\n\n")
			fmt.Fprintf(&b, "// %s is the batch function for %s.load(%s).\n", typeName, lc.modelName, strings.Join(lc.argNames, ", "))
			fmt.Fprintf(&b, "type %s = dataloader.BatchFn[%s, []*%s]\n\n", typeName, keyType, lc.modelName)
		}
	}

	// Generate Loaders struct with actual Loader instances
	generateLoadersStruct(&b, allRelations, seenTypes, extendModels, fkLoadCalls)
	generateDefaultLoaders(&b, allRelations, softModels, extendModels, fkLoadCalls)
	generateRemoteLoaders(&b, allRelations, softModels, extendModels, fkLoadCalls)

	return []byte(b.String())
}

// generateLoaderType generates the BatchFn type alias for a relation.
func generateLoaderType(b *strings.Builder, modelName string, rel Relation) {
	loaderName := loaderTypeName(modelName, rel)
	localGoType := rel.KeyGoType
	if rel.IsList {
		fmt.Fprintf(b, "// %s is the batch function for %s.%s (hasMany).\n", loaderName, modelName, rel.FieldName)
		fmt.Fprintf(b, "type %s = dataloader.BatchFn[%s, []%s]\n\n", loaderName, localGoType, rel.TargetName)
	} else {
		fmt.Fprintf(b, "// %s is the batch function for %s.%s (%s).\n", loaderName, modelName, rel.FieldName, relTypeName(rel.Type))
		fmt.Fprintf(b, "type %s = dataloader.BatchFn[%s, *%s]\n\n", loaderName, localGoType, rel.TargetName)
	}
}

// loaderEntry is a deduplicated loader for code generation.
type loaderEntry struct {
	fieldName string
	typeName  string
	rel       Relation
}

// deduplicateLoaders returns unique loaders across all relations.
func deduplicateLoaders(allRelations []struct {
	modelName string
	relations []Relation
}) []loaderEntry {
	seen := make(map[string]bool)
	var entries []loaderEntry
	for _, mr := range allRelations {
		for _, rel := range mr.relations {
			name := loaderFieldName(mr.modelName, rel)
			if seen[name] {
				continue
			}
			seen[name] = true
			entries = append(entries, loaderEntry{
				fieldName: name,
				typeName:  loaderTypeName(mr.modelName, rel),
				rel:       rel,
			})
		}
	}
	return entries
}

// generateLoadersStruct generates the Loaders struct with actual Loader instances.
func generateLoadersStruct(b *strings.Builder, allRelations []struct {
	modelName string
	relations []Relation
}, seenTypes map[string]bool, extendModels []string, fkLoadCalls []loadCallInfo) {
	entries := deduplicateLoaders(allRelations)

	// Struct
	b.WriteString("// Loaders holds DataLoader instances for all relations.\n")
	b.WriteString("type Loaders struct {\n")
	for _, e := range entries {
		if e.rel.IsList {
			fmt.Fprintf(b, "\t%s *dataloader.Loader[%s, []%s]\n", e.fieldName, e.rel.KeyGoType, e.rel.TargetName)
		} else {
			fmt.Fprintf(b, "\t%s *dataloader.Loader[%s, *%s]\n", e.fieldName, e.rel.KeyGoType, e.rel.TargetName)
		}
	}
	// Extend model load-by-PK loaders
	for _, name := range extendModels {
		fmt.Fprintf(b, "\tExtend%s *dataloader.Loader[int64, *%s]\n", name, name)
	}
	// FK/multi-condition loaders from load() calls
	for _, lc := range fkLoadCalls {
		loaderName := loaderNameFromLoadCall(lc)
		if len(lc.argNames) == 1 {
			fmt.Fprintf(b, "\t%s *dataloader.Loader[%s, []*%s]\n", loaderName, lc.argTypes[0], lc.modelName)
		} else {
			keyType := loaderName + "Key"
			fmt.Fprintf(b, "\t%s *dataloader.Loader[%s, []*%s]\n", loaderName, keyType, lc.modelName)
		}
	}
	b.WriteString("}\n\n")

	// Constructor
	b.WriteString("// NewLoaders creates Loaders from batch functions.\n")
	b.WriteString("func NewLoaders(cfg dataloader.Config,\n")
	var params []string
	for _, e := range entries {
		params = append(params, fmt.Sprintf("\t%sFn %s", str.LowerFirst(e.fieldName), e.typeName))
	}
	for _, name := range extendModels {
		params = append(params, fmt.Sprintf("\textend%sFn Extend%sByIdLoader", name, name))
	}
	for _, lc := range fkLoadCalls {
		loaderName := loaderNameFromLoadCall(lc)
		typeName := loaderName + "Loader"
		params = append(params, fmt.Sprintf("\t%sFn %s", str.LowerFirst(loaderName), typeName))
	}
	b.WriteString(strings.Join(params, ",\n"))
	b.WriteString(",\n) Loaders {\n")
	b.WriteString("\treturn Loaders{\n")
	for _, e := range entries {
		fmt.Fprintf(b, "\t\t%s: dataloader.New(%sFn, cfg),\n", e.fieldName, str.LowerFirst(e.fieldName))
	}
	for _, name := range extendModels {
		fmt.Fprintf(b, "\t\tExtend%s: dataloader.New(extend%sFn, cfg),\n", name, name)
	}
	for _, lc := range fkLoadCalls {
		loaderName := loaderNameFromLoadCall(lc)
		fmt.Fprintf(b, "\t\t%s: dataloader.New(%sFn, cfg),\n", loaderName, str.LowerFirst(loaderName))
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")

	// ensureField helper
	b.WriteString("// ensureField adds a field to the list if not already present.\n")
	b.WriteString("func ensureField(fields []string, name string) []string {\n")
	b.WriteString("\tif fields == nil {\n\t\treturn nil\n\t}\n")
	b.WriteString("\tfor _, f := range fields {\n")
	b.WriteString("\t\tif f == name {\n\t\t\treturn fields\n\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn append(fields, name)\n")
	b.WriteString("}\n\n")

	b.WriteString("// SetLoaders injects DataLoader instances into the App.\n")
	b.WriteString("func (a *App) SetLoaders(l Loaders) {\n")
	b.WriteString("\ta.loaders = l\n")
	b.WriteString("}\n\n")

}

// generateDefaultLoaders generates NewDefaultLoaders that uses the module's own Clients.
func generateDefaultLoaders(b *strings.Builder, allRelations []struct {
	modelName string
	relations []Relation
}, softModels map[string]bool, extendModels []string, fkLoadCalls []loadCallInfo) {
	b.WriteString("// NewDefaultLoaders creates Loaders with default batch functions using the module's Clients.\n")
	b.WriteString("// Used in embedded mode — all models share the same database.\n")
	b.WriteString("func NewDefaultLoaders(app *App, cfg dataloader.Config) Loaders {\n")
	b.WriteString("\treturn NewLoaders(cfg,\n")

	seen := make(map[string]bool)
	for _, mr := range allRelations {
		for _, rel := range mr.relations {
			name := loaderFieldName(mr.modelName, rel)
			if seen[name] {
				continue
			}
			seen[name] = true
			generateBatchFunc(b, mr.modelName, rel, softModels[rel.TargetName])
		}
	}

	// Extend model load-by-PK batch functions
	for _, name := range extendModels {
		generateExtendByPKBatchFunc(b, name)
	}

	// FK/multi-condition batch functions from load() calls
	for _, lc := range fkLoadCalls {
		generateFKLoadBatchFunc(b, lc, softModels[lc.modelName])
	}

	b.WriteString("\t)\n")
	b.WriteString("}\n")
}

// generateFKLoadBatchFunc generates a batch function for FK/multi-condition load().
func generateFKLoadBatchFunc(b *strings.Builder, lc loadCallInfo, softTarget bool) {
	tableName := str.ToSnakeCase(lc.modelName) + "s"
	scanFn := "scan" + lc.modelName
	loaderName := loaderNameFromLoadCall(lc)

	fmt.Fprintf(b, "\t\t// %s: %s.load(%s)\n", loaderName, lc.modelName, strings.Join(lc.argNames, ", "))

	if len(lc.argNames) == 1 {
		// Single FK: WHERE col IN (keys)
		col := str.ToSnakeCase(lc.argNames[0])
		goField := str.Capitalize(lc.argNames[0])
		keyType := lc.argTypes[0]
		condField := goTypeToCondField(keyType)
		fmt.Fprintf(b, "\t\tfunc(ctx context.Context, keys []%s, fields []string) (map[%s][]*%s, error) {\n", keyType, keyType, lc.modelName)
		fmt.Fprintf(b, "\t\t\tfields = ensureField(fields, %q)\n", col)
		fmt.Fprintf(b, "\t\t\tconds := []lux.Condition{lux.New%s(%q).In(keys...)}\n", condField, col)
		if softTarget {
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewTimeField(\"deleted_at\").IsNull())\n")
		}
		fmt.Fprintf(b, "\t\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", tableName)
		fmt.Fprintf(b, "\t\t\trows, err := %s.QueryRows(ctx, app.DB, %s, query, args...)\n", dbPkg, scanFn)
		fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\tresult := make(map[%s][]*%s, len(keys))\n", keyType, lc.modelName)
		fmt.Fprintf(b, "\t\t\tfor _, row := range rows {\n")
		fmt.Fprintf(b, "\t\t\t\tresult[row.%s] = append(result[row.%s], row)\n", goField, goField)
		fmt.Fprintf(b, "\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\treturn result, nil\n")
		fmt.Fprintf(b, "\t\t},\n")
	} else {
		// Multi-condition: composite key
		keyType := loaderName + "Key"
		fmt.Fprintf(b, "\t\tfunc(ctx context.Context, keys []%s, fields []string) (map[%s][]*%s, error) {\n", keyType, keyType, lc.modelName)

		// Build per-column distinct key slices (typed per the model field decl).
		for i, arg := range lc.argNames {
			col := str.ToSnakeCase(arg)
			argType := lc.argTypes[i]
			goKeyField := str.Capitalize(arg)
			fmt.Fprintf(b, "\t\t\tfields = ensureField(fields, %q)\n", col)
			fmt.Fprintf(b, "\t\t\t%sSet := make(map[%s]bool, len(keys))\n", arg, argType)
			fmt.Fprintf(b, "\t\t\tfor _, k := range keys {\n")
			fmt.Fprintf(b, "\t\t\t\t%sSet[k.%s] = true\n", arg, goKeyField)
			fmt.Fprintf(b, "\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\t%sKeys := make([]%s, 0, len(%sSet))\n", arg, argType, arg)
			fmt.Fprintf(b, "\t\t\tfor v := range %sSet {\n", arg)
			fmt.Fprintf(b, "\t\t\t\t%sKeys = append(%sKeys, v)\n", arg, arg)
			fmt.Fprintf(b, "\t\t\t}\n")
		}

		// Build WHERE clause
		fmt.Fprintf(b, "\t\t\tconds := []lux.Condition{\n")
		for i, arg := range lc.argNames {
			col := str.ToSnakeCase(arg)
			condField := goTypeToCondField(lc.argTypes[i])
			fmt.Fprintf(b, "\t\t\t\tlux.New%s(%q).In(%sKeys...),\n", condField, col, arg)
		}
		fmt.Fprintf(b, "\t\t\t}\n")
		if softTarget {
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewTimeField(\"deleted_at\").IsNull())\n")
		}

		fmt.Fprintf(b, "\t\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", tableName)
		fmt.Fprintf(b, "\t\t\trows, err := %s.QueryRows(ctx, app.DB, %s, query, args...)\n", dbPkg, scanFn)
		fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")

		// Group by composite key
		fmt.Fprintf(b, "\t\t\tresult := make(map[%s][]*%s, len(keys))\n", keyType, lc.modelName)
		fmt.Fprintf(b, "\t\t\tfor _, row := range rows {\n")
		fmt.Fprintf(b, "\t\t\t\tkey := %s{", keyType)
		for i, arg := range lc.argNames {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%s: row.%s", str.Capitalize(arg), str.Capitalize(arg))
		}
		fmt.Fprintf(b, "}\n")
		fmt.Fprintf(b, "\t\t\t\tresult[key] = append(result[key], row)\n")
		fmt.Fprintf(b, "\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\treturn result, nil\n")
		fmt.Fprintf(b, "\t\t},\n")
	}
}

// generateRemoteLoaders generates NewRemoteLoaders for cluster mode.
// Same-module relations use local DB. Cross-module (extend) loaders use RPC.
func generateRemoteLoaders(b *strings.Builder, allRelations []struct {
	modelName string
	relations []Relation
}, softModels map[string]bool, extendModels []string, fkLoadCalls []loadCallInfo) {
	if len(extendModels) == 0 && len(fkLoadCalls) == 0 {
		return // no cross-module loaders, no need for remote variant
	}

	b.WriteString("// NewRemoteLoaders creates Loaders with RPC-backed batch functions for cross-module models.\n")
	b.WriteString("// Same-module relations use local DB. Cross-module (extend) loaders call remote service.\n")
	b.WriteString("// Used in cluster mode (DEPLOY_MODE=cluster).\n")
	b.WriteString("func NewRemoteLoaders(app *App, rpcClients map[string]*rpc.Client, cfg dataloader.Config) Loaders {\n")
	b.WriteString("\treturn NewLoaders(cfg,\n")

	// Same-module relations: same as DefaultLoaders (local DB)
	seen := make(map[string]bool)
	for _, mr := range allRelations {
		for _, rel := range mr.relations {
			name := loaderFieldName(mr.modelName, rel)
			if seen[name] {
				continue
			}
			seen[name] = true
			generateBatchFunc(b, mr.modelName, rel, softModels[rel.TargetName])
		}
	}

	// Extend models: RPC batch function
	for _, name := range extendModels {
		generateRemoteExtendByPKBatchFunc(b, name)
	}

	// FK loads: for now use local DB (TODO: route to correct remote service)
	for _, lc := range fkLoadCalls {
		generateFKLoadBatchFunc(b, lc, softModels[lc.modelName])
	}

	b.WriteString("\t)\n")
	b.WriteString("}\n\n")
}

// generateRemoteExtendByPKBatchFunc generates an RPC-backed batch function for cross-module PK loads.
func generateRemoteExtendByPKBatchFunc(b *strings.Builder, modelName string) {
	fmt.Fprintf(b, "\t\t// Extend %s: RPC batch load (cluster mode)\n", modelName)
	fmt.Fprintf(b, "\t\tfunc(ctx context.Context, keys []int64, fields []string) (map[int64]*%s, error) {\n", modelName)
	fmt.Fprintf(b, "\t\t\tclient := rpcClients[%q]\n", modelName)
	fmt.Fprintf(b, "\t\t\tif client == nil {\n")
	fmt.Fprintf(b, "\t\t\t\treturn nil, fmt.Errorf(\"no RPC client for %%s\", %q)\n", modelName)
	fmt.Fprintf(b, "\t\t\t}\n")
	// Encode keys as int64 array param
	fmt.Fprintf(b, "\t\t\tvar enc codec.Encoder\n")
	fmt.Fprintf(b, "\t\t\tenc.WriteFieldInt(1, int64(len(keys)))\n")
	fmt.Fprintf(b, "\t\t\tfor _, k := range keys {\n")
	fmt.Fprintf(b, "\t\t\t\tenc.WriteFieldInt(2, k)\n")
	fmt.Fprintf(b, "\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\tenc.WriteEnd()\n")
	svcName := "svc:batchLoad:" + modelName
	apiID := getAPIID(svcName)
	fmt.Fprintf(b, "\t\t\tresp, err := client.Call(%d, enc.Bytes())\n", apiID)
	fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
	// Decode response: [varint count][model1 ReadLuxo][model2 ReadLuxo]...
	fmt.Fprintf(b, "\t\t\tcount, n := codec.ReadVarint(resp, 0)\n")
	fmt.Fprintf(b, "\t\t\tif n <= 0 {\n\t\t\t\treturn nil, fmt.Errorf(\"invalid batch response\")\n\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\tresult := make(map[int64]*%s, count)\n", modelName)
	fmt.Fprintf(b, "\t\t\tdec := codec.NewDecoder(resp[n:])\n")
	fmt.Fprintf(b, "\t\t\tfor i := uint64(0); i < count; i++ {\n")
	fmt.Fprintf(b, "\t\t\t\titem := &%s{}\n", modelName)
	fmt.Fprintf(b, "\t\t\t\titem.ReadLuxo(dec)\n")
	fmt.Fprintf(b, "\t\t\t\tresult[item.Id] = item\n")
	fmt.Fprintf(b, "\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\treturn result, nil\n")
	fmt.Fprintf(b, "\t\t},\n")
}

// generateExtendByPKBatchFunc generates a load-by-primary-key batch function for a cross-module model.
// User.load(id) → this batch function queries users WHERE id IN (keys).
func generateExtendByPKBatchFunc(b *strings.Builder, modelName string) {
	tableName := str.ToSnakeCase(modelName) + "s"
	scanFn := "scan" + modelName

	fmt.Fprintf(b, "\t\t// Extend %s: load by primary key (cross-module DataLoader)\n", modelName)
	fmt.Fprintf(b, "\t\tfunc(ctx context.Context, keys []int64, fields []string) (map[int64]*%s, error) {\n", modelName)
	fmt.Fprintf(b, "\t\t\tconds := []lux.Condition{lux.NewIntField(\"id\").In(keys...)}\n")
	fmt.Fprintf(b, "\t\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", tableName)
	fmt.Fprintf(b, "\t\t\trows, err := %s.QueryRows(ctx, app.DB, %s, query, args...)\n", dbPkg, scanFn)
	fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\tresult := make(map[int64]*%s, len(keys))\n", modelName)
	fmt.Fprintf(b, "\t\t\tfor _, row := range rows {\n")
	fmt.Fprintf(b, "\t\t\t\tresult[row.Id] = row\n")
	fmt.Fprintf(b, "\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\treturn result, nil\n")
	fmt.Fprintf(b, "\t\t},\n")
}

// generateBatchFunc generates an inline batch function for a relation.
// Uses raw pg.QueryRows with the module's scan function — no cross-module Client dependency.
func generateBatchFunc(b *strings.Builder, modelName string, rel Relation, softTarget bool) {
	targetTable := str.ToSnakeCase(rel.TargetName) + "s"
	remoteCol := str.ToSnakeCase(rel.RemoteKey)
	scanFn := "scan" + rel.TargetName
	goRemoteField := str.Capitalize(rel.RemoteKey) // camelCase → PascalCase (e.g., userId → UserId)

	keyType := rel.KeyGoType
	if rel.IsList {
		fmt.Fprintf(b, "\t\tfunc(ctx context.Context, keys []%s, fields []string) (map[%s][]%s, error) {\n", keyType, keyType, rel.TargetName)
		fmt.Fprintf(b, "\t\t\tfields = ensureField(fields, %q)\n", remoteCol)
		condField := goTypeToCondField(keyType)
		fmt.Fprintf(b, "\t\t\tconds := []lux.Condition{lux.New%s(%q).In(keys...)}\n", condField, remoteCol)
		if softTarget {
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewTimeField(\"deleted_at\").IsNull())\n")
		}
		fmt.Fprintf(b, "\t\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", targetTable)
		fmt.Fprintf(b, "\t\t\trows, err := pg.QueryRows(ctx, app.DB, %s, query, args...)\n", scanFn)
		fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\tresult := make(map[%s][]%s, len(keys))\n", keyType, rel.TargetName)
		fmt.Fprintf(b, "\t\t\tfor _, row := range rows {\n")
		fmt.Fprintf(b, "\t\t\t\tresult[row.%s] = append(result[row.%s], *row)\n", goRemoteField, goRemoteField)
		fmt.Fprintf(b, "\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\treturn result, nil\n")
		fmt.Fprintf(b, "\t\t},\n")
	} else {
		fmt.Fprintf(b, "\t\tfunc(ctx context.Context, keys []%s, fields []string) (map[%s]*%s, error) {\n", keyType, keyType, rel.TargetName)
		fmt.Fprintf(b, "\t\t\tfields = ensureField(fields, %q)\n", remoteCol)
		condField := goTypeToCondField(keyType)
		fmt.Fprintf(b, "\t\t\tconds := []lux.Condition{lux.New%s(%q).In(keys...)}\n", condField, remoteCol)
		if softTarget {
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewTimeField(\"deleted_at\").IsNull())\n")
		}
		fmt.Fprintf(b, "\t\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", targetTable)
		fmt.Fprintf(b, "\t\t\trows, err := pg.QueryRows(ctx, app.DB, %s, query, args...)\n", scanFn)
		fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\tresult := make(map[%s]*%s, len(keys))\n", keyType, rel.TargetName)
		fmt.Fprintf(b, "\t\t\tfor _, row := range rows {\n")
		fmt.Fprintf(b, "\t\t\t\tresult[row.%s] = row\n", goRemoteField)
		fmt.Fprintf(b, "\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\treturn result, nil\n")
		fmt.Fprintf(b, "\t\t},\n")
	}
}

// loaderTypeName returns the type name for a loader function.
func loaderTypeName(modelName string, rel Relation) string {
	if rel.IsList {
		return pluralize(rel.TargetName) + "By" + str.Capitalize(rel.RemoteKey) + "Loader"
	}
	return rel.TargetName + "By" + str.Capitalize(rel.RemoteKey) + "Loader"
}

// loaderFieldName returns the field name for a loader in the Loaders struct.
func loaderFieldName(modelName string, rel Relation) string {
	return str.Capitalize(modelName) + str.Capitalize(rel.FieldName)
}

// CollectEnumsFromResult collects all enum names from the result (exported for CLI).
func CollectEnumsFromResult(result *semantic.Result) map[string]bool {
	return collectEnums(result)
}

// collectEnums collects all enum names from the result.
func collectEnums(result *semantic.Result) map[string]bool {
	enums := make(map[string]bool)
	for _, file := range result.Files {
		for _, e := range file.Enums {
			enums[e.Name] = true
		}
	}
	return enums
}

// findDirective finds a directive by name, returns nil if not found.
func findDirective(directives []*ast.Directive, name string) *ast.Directive {
	for _, d := range directives {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// extractByArgs extracts (remote, local) from @by directive args.
func extractByArgs(d *ast.Directive) (remote, local string) {
	if len(d.Args) >= 1 {
		if ident, ok := d.Args[0].Value.(*ast.Ident); ok {
			remote = ident.Name
		}
	}
	if len(d.Args) >= 2 {
		if ident, ok := d.Args[1].Value.(*ast.Ident); ok {
			local = ident.Name
		}
	}
	if local == "" {
		local = "id"
	}
	return
}

// hasFKField checks if a field with the given name exists in the field list.
func hasFKField(fields []*ast.FieldDecl, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// goTypeToCondField maps a Go type to the lux condition field type name.
func goTypeToCondField(goType string) string {
	switch goType {
	case "int64":
		return "IntField"
	case "string":
		return "StringField"
	case "uuid.UUID":
		return "UUIDField"
	default:
		return "IntField"
	}
}

// fkGoType returns the Go type of a FK field by name. Defaults to "int64".
func fkGoType(fields []*ast.FieldDecl, fkName string) string {
	for _, f := range fields {
		if f.Name == fkName && f.Type != nil {
			return mapBaseType(f.Type.Name)
		}
	}
	return "int64"
}

// isFKNullable checks if a FK field is nullable.
func isFKNullable(fields []*ast.FieldDecl, fkName string) bool {
	for _, f := range fields {
		if f.Name == fkName && f.Type != nil {
			return f.Type.Nullable
		}
	}
	return false
}

// relTypeName returns a display name for a relation type.
func relTypeName(t RelationType) string {
	switch t {
	case BelongsTo:
		return "belongsTo"
	case HasMany:
		return "hasMany"
	case HasOne:
		return "hasOne"
	}
	return "unknown"
}
