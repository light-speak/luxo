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
func generateDataLoaderFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
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

	var b strings.Builder
	writeHeader(&b, packageName, "dataloader.gen.go")
	b.WriteString("import \"context\"\n\n")

	// Generate loader function types for each relation (deduplicate by type name)
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

	// Generate SetLoaders method on App to inject all loaders
	generateSetLoaders(&b, allRelations)

	return []byte(b.String())
}

// generateLoaderType generates the function type and field for a single relation loader.
func generateLoaderType(b *strings.Builder, modelName string, rel Relation) {
	loaderName := loaderTypeName(modelName, rel)
	localGoType := "int64" // default FK type
	if rel.IsList {
		fmt.Fprintf(b, "// %s loads %s.%s (hasMany).\n", loaderName, modelName, rel.FieldName)
		fmt.Fprintf(b, "// Query: WHERE %s IN (...)\n", toSnakeCase(rel.RemoteKey))
		fmt.Fprintf(b, "type %s func(ctx context.Context, keys []%s) (map[%s][]%s, error)\n\n",
			loaderName, localGoType, localGoType, rel.TargetName)
	} else {
		fmt.Fprintf(b, "// %s loads %s.%s (%s).\n", loaderName, modelName, rel.FieldName, relTypeName(rel.Type))
		fmt.Fprintf(b, "// Query: WHERE %s IN (...)\n", toSnakeCase(rel.RemoteKey))
		fmt.Fprintf(b, "type %s func(ctx context.Context, keys []%s) (map[%s]*%s, error)\n\n",
			loaderName, localGoType, localGoType, rel.TargetName)
	}
}

// generateSetLoaders generates the Loaders struct and SetLoaders function.
func generateSetLoaders(b *strings.Builder, allRelations []struct {
	modelName string
	relations []Relation
}) {
	b.WriteString("// Loaders holds all DataLoader functions for dependency injection.\n")
	b.WriteString("type Loaders struct {\n")
	for _, mr := range allRelations {
		for _, rel := range mr.relations {
			name := loaderFieldName(mr.modelName, rel)
			typeName := loaderTypeName(mr.modelName, rel)
			fmt.Fprintf(b, "\t%s %s\n", name, typeName)
		}
	}
	b.WriteString("}\n\n")

	b.WriteString("// SetLoaders injects DataLoader functions into the App.\n")
	b.WriteString("func (a *App) SetLoaders(l Loaders) {\n")
	b.WriteString("\ta.loaders = l\n")
	b.WriteString("}\n")
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
