package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// luxImport is the import path for the Luxo runtime package.
const luxImport = "github.com/light-speak/luxo/pkg/lux"

// modelTableName is shared by queries, migrations and mutation declarations.
func modelTableName(name string) string { return str.ToSnakeCase(name) + "s" }

// SQL names are rendered once during generation, never on the request path.
func (g *GeneratorContext) sqlIdentifier(name string) string {
	return g.sqlDialect.QuoteIdentifier(name)
}
func (g *GeneratorContext) sqlColumn(name string) string {
	return g.sqlIdentifier(str.ToSnakeCase(name))
}
func (g *GeneratorContext) sqlTable(name string) string { return g.sqlIdentifier(modelTableName(name)) }

func (g *GeneratorContext) sqlAggregateColumn(name string) string {
	if name == "" {
		return ""
	}
	return g.sqlColumn(name)
}

// generateQueryBuilder generates the type-safe query builder for a model.
// Uses runtime generics (lux.Query[T], lux.CreateBase[T], lux.UpdateBase[T])
// to minimize generated code — only scanner, client, fields, where, and
// typed Set methods are generated per model.
func (g *GeneratorContext) generateQueryBuilder(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	tableName := modelTableName(name)
	soft := isSoftDelete(m)

	// @soft: ensure deletedAt field exists for scanner/struct generation
	fields := m.Fields
	if soft && !hasDeletedAtField(m.Fields) {
		fields = append(append([]*ast.FieldDecl{}, m.Fields...), softDeleteField())
	}

	// Filter out relation fields — they're not DB columns
	var dbFields []*ast.FieldDecl
	for _, f := range fields {
		if f.Computed == nil && !isRelationField(f, enums) {
			dbFields = append(dbFields, f)
		}
	}

	g.generateScanner(b, &ast.ModelDecl{Name: m.Name, Fields: dbFields, Directives: m.Directives}, enums)
	g.generateClient(b, name, tableName, dbFields, soft)
	g.generateQueryFacade(b, name, dbFields, true, m.Directives)
	generateQueryUpdateBuilder(b, name, dbFields, m.Directives)
	g.generateFieldConstants(b, name, dbFields)
	g.generateWhereFields(b, name, dbFields)
	g.generateCreateBuilder(b, name, dbFields, enums, m.Directives)
	g.generateCreateManyBuilder(b, name, dbFields, m.Directives)
	g.generateUpdateBuilder(b, name, dbFields, m.Directives)
}

// generateExtendQueryBuilder generates minimal query support for extend models:
// scanner, client (read-only: Find/Where), field constants, and where helpers.
// No create/update builders — extend models are read-only from the referencing module.
func generateExtendQueryBuilder(b *strings.Builder, ext *ast.ExtendDecl) {
	defaultGenerator().generateExtendQueryBuilder(b, ext)
}

func (g *GeneratorContext) generateExtendQueryBuilder(b *strings.Builder, ext *ast.ExtendDecl) {
	name := ext.Name
	tableName := modelTableName(name)

	// Filter out relation fields
	var dbFields []*ast.FieldDecl
	for _, f := range ext.Fields {
		if f.Computed != nil || f.Type == nil {
			continue
		}
		// Skip relation fields (type is a model name — capitalized, not a primitive)
		typeName := f.Type.Name
		if typeName != "" && typeName[0] >= 'A' && typeName[0] <= 'Z' {
			switch typeName {
			case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "JSON", "Decimal", "Bytes":
				// primitive — keep
			default:
				if !f.Type.IsList {
					continue // skip single-model relation fields
				}
				continue // skip list-model relation fields too
			}
		}
		dbFields = append(dbFields, f)
	}

	generateScanner(b, &ast.ModelDecl{Name: name, Fields: dbFields}, nil)
	g.generateExtendClient(b, name, tableName, dbFields)
	g.generateQueryFacade(b, name, dbFields, false)
	g.generateFieldConstants(b, name, dbFields)
	g.generateWhereFields(b, name, dbFields)
}

func (g *GeneratorContext) generateExtendClient(b *strings.Builder, name, tableName string, fields []*ast.FieldDecl) {
	tableName = g.sqlIdentifier(tableName)
	scanFn := "scan" + name

	idType := "int64"
	idFieldName := g.externalModelIDFieldName(name)
	idGoName := str.Capitalize(idFieldName)
	for _, f := range fields {
		if f.Name == idFieldName && f.Type != nil {
			idType = resolveGoType(f.Type)
			break
		}
	}

	fmt.Fprintf(b, `// %sClient provides read-only query access for the external %s model.
type %sClient struct {
	db *pg.DB
}

// Find returns a %s by ID or NotFound.
func (c *%sClient) Find(ctx context.Context, id %s) (*%s, error) {
	return c.Where(%sWhere.%s.Eq(id)).FirstRequired(ctx)
}

// Where starts a query with conditions.
func (c *%sClient) Where(conds ...lux.Condition) *%sQuery {
	return (*%sQuery)(pg.NewQuery(c.db, %q, %s, conds))
}

`, name, name, name,
		name, name, idType, name, name, idGoName,
		name, name, name, tableName, scanFn)
}

// --- Client ---

func (g *GeneratorContext) generateClient(b *strings.Builder, name, tableName string, fields []*ast.FieldDecl, soft bool) {
	tableName = g.sqlIdentifier(tableName)
	var deletedColumn string
	if soft {
		deletedColumn = g.sqlColumn("deletedAt")
	}
	scanFn := "scan" + name

	primaryKey := primaryKeyField(&ast.ModelDecl{Name: name, Fields: fields})
	idType := "int64"
	idGoName := "Id"
	if primaryKey != nil {
		idType = resolveGoType(primaryKey.Type)
		idGoName = str.Capitalize(primaryKey.Name)
	}

	fmt.Fprintf(b, `// %sClient is the entry point for %s queries.
type %sClient struct {
	db *pg.DB
}

// Find returns a %s by ID or NotFound.
func (c *%sClient) Find(ctx context.Context, id %s) (*%s, error) {
	return c.Where(%sWhere.%s.Eq(id)).FirstRequired(ctx)
}

`, name, name, name,
		name, name, idType, name, name, idGoName)

	// Where — @soft models inject deleted_at IS NULL by default
	if soft {
		fmt.Fprintf(b, `// Where starts a query with conditions. @soft: auto-filters deleted records.
func (c *%sClient) Where(conds ...lux.Condition) *%sQuery {
	conds = append(conds, lux.NewTimeField(%q).IsNull())
	return (*%sQuery)(pg.NewQuery(c.db, %q, %s, conds))
}

// WithDeleted starts a query that includes soft-deleted records.
func (c *%sClient) WithDeleted(conds ...lux.Condition) *%sQuery {
	return (*%sQuery)(pg.NewQuery(c.db, %q, %s, conds))
}

`, name, name, deletedColumn, name, tableName, scanFn,
			name, name, name, tableName, scanFn)
	} else {
		fmt.Fprintf(b, `// Where starts a query with conditions.
func (c *%sClient) Where(conds ...lux.Condition) *%sQuery {
	return (*%sQuery)(pg.NewQuery(c.db, %q, %s, conds))
}

`, name, name, name, tableName, scanFn)
	}

	fmt.Fprintf(b, `// Create starts a single-row create builder.
func (c *%sClient) Create() *%sCreateBuilder {
	return &%sCreateBuilder{CreateBase: pg.CreateBase[%s]{Db: c.db, Table: %q, Scan: %s}}
}

// CreateMany starts a batch create builder.
func (c *%sClient) CreateMany() *%sCreateManyBuilder {
	return &%sCreateManyBuilder{db: c.db, table: %q}
}

`, name, name, name, name, tableName, scanFn,
		name, name, name, tableName)

	// Count — returns total count (optionally with conditions)
	if soft {
		fmt.Fprintf(b, `// Count returns the number of non-deleted records matching conditions.
func (c *%sClient) Count(ctx context.Context, conds ...lux.Condition) (int64, error) {
	conds = append(conds, lux.NewTimeField(%q).IsNull())
	return pg.NewQuery[%s](c.db, %q, %s, conds).Count(ctx)
}

// Exists returns true if any non-deleted record matches conditions.
func (c *%sClient) Exists(ctx context.Context, conds ...lux.Condition) (bool, error) {
	return c.Where(conds...).Exists(ctx)
}

`, name, deletedColumn, name, tableName, scanFn, name)
	} else {
		fmt.Fprintf(b, `// Count returns the number of records matching conditions.
func (c *%sClient) Count(ctx context.Context, conds ...lux.Condition) (int64, error) {
	return pg.NewQuery[%s](c.db, %q, %s, conds).Count(ctx)
}

// Exists returns true if any record matches conditions.
func (c *%sClient) Exists(ctx context.Context, conds ...lux.Condition) (bool, error) {
	return c.Where(conds...).Exists(ctx)
}

`, name, name, tableName, scanFn, name)
	}

	// @soft: SoftDelete and ForceDelete
	if soft {
		fmt.Fprintf(b, `// SoftDelete marks matching records as deleted (sets deleted_at).
func (c *%sClient) SoftDelete(ctx context.Context, conds ...lux.Condition) (int64, error) {
	conds = append(conds, lux.NewTimeField(%q).IsNull())
	q := pg.NewQuery[%s](c.db, %q, %s, conds)
	return q.Update(ctx, lux.SetField{Col: %q, Val: time.Now()})
}

// ForceDelete permanently removes matching records from the database.
func (c *%sClient) ForceDelete(ctx context.Context, conds ...lux.Condition) (int64, error) {
	q := pg.NewQuery[%s](c.db, %q, %s, conds)
	return q.Delete(ctx)
}

// Restore un-deletes soft-deleted records matching the conditions.
func (c *%sClient) Restore(ctx context.Context, conds ...lux.Condition) (int64, error) {
	q := pg.NewQuery[%s](c.db, %q, %s, conds)
	return q.Update(ctx, lux.SetField{Col: %q, Val: nil})
}

`, name, deletedColumn, name, tableName, scanFn, deletedColumn,
			name, name, tableName, scanFn,
			name, name, tableName, scanFn, deletedColumn)
	}
}

// --- Field constants ---

func (g *GeneratorContext) generateFieldConstants(b *strings.Builder, name string, fields []*ast.FieldDecl) {
	fmt.Fprintf(b, "// %sFields provides column names for field selection.\n", name)
	fmt.Fprintf(b, "var %sFields = struct {\n", name)
	for _, f := range fields {
		if f.Computed != nil {
			continue
		}
		fmt.Fprintf(b, "\t%s string\n", str.Capitalize(f.Name))
	}
	b.WriteString("}{\n")
	for _, f := range fields {
		if f.Computed != nil {
			continue
		}
		col := g.sqlColumn(f.Name)
		fmt.Fprintf(b, "\t%s: %q,\n", str.Capitalize(f.Name), col)
	}
	b.WriteString("}\n\n")
}

// --- Where fields ---

func (g *GeneratorContext) generateWhereFields(b *strings.Builder, name string, fields []*ast.FieldDecl) {
	fmt.Fprintf(b, "// %sWhere provides type-safe field conditions.\n", name)
	fmt.Fprintf(b, "var %sWhere = struct {\n", name)
	for _, f := range fields {
		// Array fields ([T]) have no scalar Condition — exclude from Where.
		// (Array containment queries are a separate, future feature.)
		if f.Computed != nil || (f.Type != nil && f.Type.IsList) {
			continue
		}
		goFieldName := str.Capitalize(f.Name)
		condType := fieldConditionTypeWithDirectives(f)
		fmt.Fprintf(b, "\t%s lux.%s\n", goFieldName, condType)
	}
	b.WriteString("}{\n")
	for _, f := range fields {
		if f.Computed != nil || (f.Type != nil && f.Type.IsList) {
			continue
		}
		col := g.sqlColumn(f.Name)
		condType := fieldConditionTypeWithDirectives(f)
		fmt.Fprintf(b, "\t%s: lux.New%s(%q),\n", str.Capitalize(f.Name), condType, col)
	}
	b.WriteString("}\n\n")
}

func fieldConditionType(t *ast.TypeRef) string {
	if t == nil {
		return "StringField"
	}
	switch t.Name {
	case "Int":
		return "IntField"
	case "Float":
		return "FloatField"
	case "String":
		return "StringField"
	case "Boolean":
		return "BoolField"
	case "DateTime":
		return "TimeField"
	case "UUID":
		return "UUIDField"
	case "Decimal":
		return "DecimalField"
	default:
		return "StringField" // enum and others use string comparison
	}
}

// fieldConditionTypeWithDirectives returns the condition type considering @search.
// String fields with @search use SearchField for tsvector-backed full-text search.
func fieldConditionTypeWithDirectives(f *ast.FieldDecl) string {
	if f.Type != nil && f.Type.Name == "String" && hasDirective(f.Directives, "search") {
		return "SearchField"
	}
	return fieldConditionType(f.Type)
}

// --- Create builder ---

// collectAutoFills returns Set statements for auto-managed fields (UUID @auto, createdAt/updatedAt).
func (g *GeneratorContext) collectAutoFills(model string, fields []*ast.FieldDecl, receiver string, modelDirectives ...[]*ast.Directive) []string {
	var fills []string
	for _, column := range collectAutoCols(fields, modelDirectives...) {
		expression := column.expr
		if expression == "now" {
			expression = "time.Now()"
		}
		var fill strings.Builder
		g.writeAutomaticSet(&fill, model, column.field, expression, receiver, "\t", "return nil, err")
		fills = append(fills, fill.String())
	}
	return fills
}

func (g *GeneratorContext) generateCreateBuilder(b *strings.Builder, name string, fields []*ast.FieldDecl, enums map[string]bool, modelDirectives ...[]*ast.Directive) {
	fmt.Fprintf(b, `// %sCreateBuilder builds an INSERT statement.
type %sCreateBuilder struct {
	pg.CreateBase[%s]
`, name, name, name)
	setters := mutationSetterFields(fields, nil, false, modelDirectives...)
	generateMutationSlots(b, setters)
	fallible := hasFallibleSave(setters)
	if fallible {
		b.WriteString("\t_prepareErr error\n")
	}
	b.WriteString("}\n\n")
	for _, f := range setters {
		g.generateMutationSetter(b, name, "Create", f, fallible)
	}

	autoFills := g.collectAutoFills(name, fields, "create", modelDirectives...)
	defaults := preparedCreateDefaults(fields, modelDirectives...)
	if len(autoFills) > 0 || fallible || len(defaults) > 0 {
		fmt.Fprintf(b, "func (b *%sCreateBuilder) Exec(ctx context.Context) (*%s, error) {\n", name, name)
		if fallible {
			b.WriteString("\tif b._prepareErr != nil { return nil, b._prepareErr }\n")
		}
		b.WriteString("\tcreate := b.CreateBase\n")
		if len(defaults) > 0 {
			g.generatePreparedCreateDefaults(b, name, defaults, enums)
		}
		for _, fill := range autoFills {
			b.WriteString(fill)
		}
		b.WriteString("\treturn create.Exec(ctx)\n")
		fmt.Fprintf(b, "}\n\n")
	}
}

// --- Create many builder ---

// autoCol represents an auto-managed column with its Go expression.
type autoCol struct {
	col   string
	expr  string
	field *ast.FieldDecl
}

// collectAutoCols returns auto-managed columns that need generated values.
func collectAutoCols(fields []*ast.FieldDecl, modelDirectives ...[]*ast.Directive) []autoCol {
	var result []autoCol
	for _, f := range fields {
		if f.Computed != nil || f.Type == nil {
			continue
		}
		col := str.ToSnakeCase(f.Name)
		if hasDirective(f.Directives, "auto") && f.Type.Name == "UUID" {
			result = append(result, autoCol{col, "uuid.NewV7()", f})
		}
		if f.Type.Name == "DateTime" && (f.Name == "createdAt" || f.Name == "updatedAt") && isAutoManaged(f, modelDirectives...) {
			result = append(result, autoCol{col, "now", f})
		}
	}
	return result
}

// collectUserFields returns fields that are user-provided (not computed, not internal, not auto-managed, not relation).
func collectUserFields(fields []*ast.FieldDecl, modelDirectives ...[]*ast.Directive) []*ast.FieldDecl {
	var result []*ast.FieldDecl
	for _, f := range fields {
		if f.Computed != nil || hasDirective(f.Directives, "internal") || isAutoManaged(f, modelDirectives...) {
			continue
		}
		result = append(result, f)
	}
	return result
}

// buildCreateManyColumns builds the column list and per-row value extraction strings.
func (g *GeneratorContext) buildCreateManyColumns(userFields []*ast.FieldDecl, autoCols []autoCol) (cols string, vals string) {
	var allCols strings.Builder
	for i, f := range userFields {
		if i > 0 {
			allCols.WriteString(", ")
		}
		fmt.Fprintf(&allCols, "%q", g.sqlColumn(f.Name))
	}
	for _, ac := range autoCols {
		if allCols.Len() > 0 {
			allCols.WriteString(", ")
		}
		fmt.Fprintf(&allCols, "%q", g.sqlIdentifier(ac.col))
	}

	var valsExtract strings.Builder
	for _, f := range userFields {
		fmt.Fprintf(&valsExtract, "r.%s, ", str.Capitalize(f.Name))
	}
	for _, ac := range autoCols {
		value := automaticValueReference(ac.field, ac.expr)
		fmt.Fprintf(&valsExtract, "%s, ", value)
	}
	return allCols.String(), valsExtract.String()
}

func (g *GeneratorContext) generateCreateManyBuilder(b *strings.Builder, name string, fields []*ast.FieldDecl, modelDirectives ...[]*ast.Directive) {
	userFields := collectUserFields(fields, modelDirectives...)
	autoCols := collectAutoCols(fields, modelDirectives...)

	var structFields strings.Builder
	for _, f := range userFields {
		goType := resolveGoType(f.Type)
		fmt.Fprintf(&structFields, "\t%s %s\n", str.Capitalize(f.Name), goType)
	}

	generateCreateManyAdd(b, name, userFields)

	colsStr, valsStr := g.buildCreateManyColumns(userFields, autoCols)
	scanFn := "scan" + name

	hasTimeAuto := false
	for _, ac := range autoCols {
		if ac.expr == "now" {
			hasTimeAuto = true
			break
		}
	}

	fmt.Fprintf(b, `// %sCreateInput is the input for batch-creating %s records.
type %sCreateInput struct {
%s}

// %sCreateManyBuilder builds a batch INSERT statement.
type %sCreateManyBuilder struct {
	db    *pg.DB
	table string
	rows  []%sCreateInput
%s}

// Exec inserts all records and returns the created rows.
func (b *%sCreateManyBuilder) Exec(ctx context.Context) ([]*%s, error) {
`, name, name, name, structFields.String(),
		name, name, name, saveErrorField(userFields),
		name, name)

	if hasFallibleSave(userFields) {
		b.WriteString("\tif b._prepareErr != nil { return nil, b._prepareErr }\n")
	}
	if hasTimeAuto {
		b.WriteString("\tnow := time.Now()\n")
	}

	fmt.Fprintf(b, "\tcols := []string{%s}\n", colsStr)
	fmt.Fprintf(b, "\trows := make([][]any, len(b.rows))\n")
	if len(userFields) == 0 {
		b.WriteString("\tfor i := range b.rows {\n")
	} else {
		b.WriteString("\tfor i, r := range b.rows {\n")
	}
	for _, column := range autoCols {
		writeAutomaticValue(b, name, column.field, column.expr, "\t\t", "return nil, err")
	}
	fmt.Fprintf(b, "\t\trows[i] = []any{%s}\n", valsStr)
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\treturn pg.InsertManyReturning(ctx, b.db, %s, b.table, cols, rows)\n", scanFn)
	fmt.Fprintf(b, "}\n\n")
}

// --- Update builder ---

func (g *GeneratorContext) generateUpdateBuilder(b *strings.Builder, name string, fields []*ast.FieldDecl, modelDirectives ...[]*ast.Directive) {
	scanFn := "scan" + name

	primaryKey := primaryKeyField(&ast.ModelDecl{Name: name, Fields: fields})
	idType := "int64"
	idColumn := g.sqlColumn("id")
	if primaryKey != nil {
		idType = resolveGoType(primaryKey.Type)
		idColumn = g.sqlColumn(primaryKey.Name)
	}

	fmt.Fprintf(b, `// %sUpdateBuilder builds an UPDATE statement.
type %sUpdateBuilder struct {
	pg.UpdateBase[%s, %s]
`, name, name, name, idType)
	setters := mutationSetterFields(fields, primaryKey, true, modelDirectives...)
	generateMutationSlots(b, setters)
	fallible := hasFallibleSave(setters)
	if fallible {
		b.WriteString("\t_prepareErr error\n")
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(b, `// new%sUpdateBuilder creates an update builder with the scan function pre-set.
func new%sUpdateBuilder(db *pg.DB, table string, id %s) *%sUpdateBuilder {
	return &%sUpdateBuilder{UpdateBase: pg.UpdateBase[%s, %s]{Db: db, Table: table, IDColumn: %q, ID: id, Scan: %s}}
}

`, name, name, idType, name, name, name, idType, idColumn, scanFn)

	// Generate Set methods — skip auto-managed, @immutable, @internal, computed, relation
	for _, f := range setters {
		g.generateMutationSetter(b, name, "Update", f, fallible)
	}

	// Auto-fill updatedAt on update
	updatedAt := automaticUpdateField(fields, modelDirectives...)
	if updatedAt != nil || fallible {
		fmt.Fprintf(b, "func (b *%sUpdateBuilder) Exec(ctx context.Context) (*%s, error) {\n", name, name)
		g.writeUpdatePreparation(b, name, updatedAt, fallible)
		b.WriteString("\treturn update.Exec(ctx)\n")
		fmt.Fprintf(b, "}\n\n")
		fmt.Fprintf(b, "func (b *%sUpdateBuilder) ExecWhere(ctx context.Context, conditions ...lux.Condition) (*%s, error) {\n", name, name)
		g.writeUpdatePreparation(b, name, updatedAt, fallible)
		b.WriteString("\treturn update.ExecWhere(ctx, conditions...)\n}\n\n")
	}
}

func saveErrorField(fields []*ast.FieldDecl) string {
	if hasFallibleSave(fields) {
		return "\t_prepareErr error\n"
	}
	return ""
}

func (g *GeneratorContext) writeUpdatePreparation(b *strings.Builder, model string, timestamp *ast.FieldDecl, fallible bool) {
	if fallible {
		b.WriteString("\tif b._prepareErr != nil { return nil, b._prepareErr }\n")
	}
	b.WriteString("\tupdate := b.UpdateBase\n")
	if timestamp != nil {
		g.writeAutomaticSet(b, model, timestamp, "time.Now()", "update", "\t", "return nil, err")
	}
}
