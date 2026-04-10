package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
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
				rel.RemoteKey = lowerFirst(m.Name) + "Id"
				rel.LocalKey = "id"
			} else {
				fkName := lowerFirst(rel.TargetName) + "Id"
				if hasFKField(m.Fields, fkName) {
					rel.Type = BelongsTo
					rel.LocalKey = fkName
					rel.RemoteKey = "id"
				} else {
					rel.Type = HasOne
					rel.RemoteKey = lowerFirst(m.Name) + "Id"
					rel.LocalKey = "id"
				}
			}
		}

		relations = append(relations, rel)
	}
	return relations
}

// generateDataLoaderFile produces dataloader.gen.go with loader types and batch function signatures.
func generateDataLoaderFile(result *semantic.Result, packageName string, enums map[string]bool, externalSoftModels map[string]bool) []byte {
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

	if len(allRelations) == 0 {
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

	var b strings.Builder
	writeHeader(&b, packageName, "dataloader.gen.go")
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/dataloader\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/pg\"\n")
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

	// Generate Loaders struct with actual Loader instances
	generateLoadersStruct(&b, allRelations, seenTypes)
	generateDefaultLoaders(&b, allRelations, softModels)

	return []byte(b.String())
}

// generateLoaderType generates the BatchFn type alias for a relation.
func generateLoaderType(b *strings.Builder, modelName string, rel Relation) {
	loaderName := loaderTypeName(modelName, rel)
	localGoType := "int64"
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
}, seenTypes map[string]bool) {
	entries := deduplicateLoaders(allRelations)

	// Struct
	b.WriteString("// Loaders holds DataLoader instances for all relations.\n")
	b.WriteString("type Loaders struct {\n")
	for _, e := range entries {
		if e.rel.IsList {
			fmt.Fprintf(b, "\t%s *dataloader.Loader[int64, []%s]\n", e.fieldName, e.rel.TargetName)
		} else {
			fmt.Fprintf(b, "\t%s *dataloader.Loader[int64, *%s]\n", e.fieldName, e.rel.TargetName)
		}
	}
	b.WriteString("}\n\n")

	// Constructor
	b.WriteString("// NewLoaders creates Loaders from batch functions.\n")
	b.WriteString("func NewLoaders(cfg dataloader.Config,\n")
	var params []string
	for _, e := range entries {
		params = append(params, fmt.Sprintf("\t%sFn %s", lowerFirst(e.fieldName), e.typeName))
	}
	b.WriteString(strings.Join(params, ",\n"))
	b.WriteString(",\n) Loaders {\n")
	b.WriteString("\treturn Loaders{\n")
	for _, e := range entries {
		fmt.Fprintf(b, "\t\t%s: dataloader.New(%sFn, cfg),\n", e.fieldName, lowerFirst(e.fieldName))
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
}, softModels map[string]bool) {
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

	b.WriteString("\t)\n")
	b.WriteString("}\n")
}

// generateBatchFunc generates an inline batch function for a relation.
// Uses raw pg.QueryRows with the module's scan function — no cross-module Client dependency.
func generateBatchFunc(b *strings.Builder, modelName string, rel Relation, softTarget bool) {
	targetTable := toSnakeCase(rel.TargetName) + "s"
	remoteCol := toSnakeCase(rel.RemoteKey)
	scanFn := "scan" + rel.TargetName
	goRemoteField := capitalize(rel.RemoteKey) // camelCase → PascalCase (e.g., userId → UserId)

	if rel.IsList {
		fmt.Fprintf(b, "\t\tfunc(ctx context.Context, keys []int64, fields []string) (map[int64][]%s, error) {\n", rel.TargetName)
		fmt.Fprintf(b, "\t\t\tfields = ensureField(fields, %q)\n", remoteCol)
		fmt.Fprintf(b, "\t\t\tconds := []lux.Condition{lux.NewIntField(%q).In(keys...)}\n", remoteCol)
		if softTarget {
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewTimeField(\"deleted_at\").IsNull())\n")
		}
		fmt.Fprintf(b, "\t\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", targetTable)
		fmt.Fprintf(b, "\t\t\trows, err := pg.QueryRows(ctx, app.DB, %s, query, args...)\n", scanFn)
		fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\tresult := make(map[int64][]%s, len(keys))\n", rel.TargetName)
		fmt.Fprintf(b, "\t\t\tfor _, row := range rows {\n")
		fmt.Fprintf(b, "\t\t\t\tresult[row.%s] = append(result[row.%s], *row)\n", goRemoteField, goRemoteField)
		fmt.Fprintf(b, "\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\treturn result, nil\n")
		fmt.Fprintf(b, "\t\t},\n")
	} else {
		fmt.Fprintf(b, "\t\tfunc(ctx context.Context, keys []int64, fields []string) (map[int64]*%s, error) {\n", rel.TargetName)
		fmt.Fprintf(b, "\t\t\tfields = ensureField(fields, %q)\n", remoteCol)
		fmt.Fprintf(b, "\t\t\tconds := []lux.Condition{lux.NewIntField(%q).In(keys...)}\n", remoteCol)
		if softTarget {
			fmt.Fprintf(b, "\t\t\tconds = append(conds, lux.NewTimeField(\"deleted_at\").IsNull())\n")
		}
		fmt.Fprintf(b, "\t\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", targetTable)
		fmt.Fprintf(b, "\t\t\trows, err := pg.QueryRows(ctx, app.DB, %s, query, args...)\n", scanFn)
		fmt.Fprintf(b, "\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\tresult := make(map[int64]*%s, len(keys))\n", rel.TargetName)
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
		return pluralize(rel.TargetName) + "By" + capitalize(rel.RemoteKey) + "Loader"
	}
	return rel.TargetName + "By" + capitalize(rel.RemoteKey) + "Loader"
}

// loaderFieldName returns the field name for a loader in the Loaders struct.
func loaderFieldName(modelName string, rel Relation) string {
	return capitalize(modelName) + capitalize(rel.FieldName)
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

// lowerFirst returns the string with the first character lowered.
func lowerFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
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
