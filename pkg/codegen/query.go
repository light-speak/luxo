package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// luxImport is the import path for the Luxo runtime package.
const luxImport = "github.com/light-speak/luxo/pkg/lux"

// generateQueryBuilder generates the type-safe query builder for a model.
// Uses runtime generics (lux.Query[T], lux.CreateBase[T], lux.UpdateBase[T])
// to minimize generated code — only scanner, client, fields, where, and
// typed Set methods are generated per model.
func generateQueryBuilder(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	tableName := str.ToSnakeCase(name) + "s" // simple pluralize
	soft := isSoftDelete(m)

	// @soft: ensure deletedAt field exists for scanner/struct generation
	fields := m.Fields
	if soft && !hasDeletedAtField(m.Fields) {
		fields = append(append([]*ast.FieldDecl{}, m.Fields...), softDeleteField())
	}

	// Filter out relation fields — they're not DB columns
	var dbFields []*ast.FieldDecl
	for _, f := range fields {
		if !isRelationField(f, enums) {
			dbFields = append(dbFields, f)
		}
	}

	generateScanner(b, &ast.ModelDecl{Name: m.Name, Fields: dbFields, Directives: m.Directives})
	generateClient(b, name, tableName, dbFields, soft)
	generateFieldConstants(b, name, dbFields)
	generateWhereFields(b, name, dbFields)
	generateCreateBuilder(b, name, dbFields)
	generateCreateManyBuilder(b, name, dbFields)
	generateUpdateBuilder(b, name, dbFields)
}

// generateExtendQueryBuilder generates minimal query support for extend models:
// scanner, client (read-only: Find/Where), field constants, and where helpers.
// No create/update builders — extend models are read-only from the referencing module.
func generateExtendQueryBuilder(b *strings.Builder, ext *ast.ExtendDecl) {
	name := ext.Name
	tableName := str.ToSnakeCase(name) + "s"

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

	generateScanner(b, &ast.ModelDecl{Name: name, Fields: dbFields})
	generateExtendClient(b, name, tableName, dbFields)
	generateFieldConstants(b, name, dbFields)
	generateWhereFields(b, name, dbFields)
}

func generateExtendClient(b *strings.Builder, name, tableName string, fields []*ast.FieldDecl) {
	scanFn := "scan" + name

	idType := "int64"
	for _, f := range fields {
		if f.Name == "id" && f.Type != nil {
			idType = resolveGoType(f.Type)
			break
		}
	}

	fmt.Fprintf(b, `// %sClient provides read-only query access for the external %s model.
type %sClient struct {
	db *pg.DB
}

// Find returns a %s by primary key.
func (c *%sClient) Find(ctx context.Context, id %s) (*%s, error) {
	return c.Where(%sWhere.Id.Eq(id)).First(ctx)
}

// Where starts a query with conditions.
func (c *%sClient) Where(conds ...lux.Condition) *pg.Query[%s] {
	return pg.NewQuery(c.db, %q, %s, conds)
}

`, name, name, name,
		name, name, idType, name, name,
		name, name, tableName, scanFn)
}

// --- Client ---

func generateClient(b *strings.Builder, name, tableName string, fields []*ast.FieldDecl, soft bool) {
	scanFn := "scan" + name

	// Determine id field type for Find method.
	idType := "int64" // default
	for _, f := range fields {
		if f.Name == "id" && f.Type != nil {
			idType = resolveGoType(f.Type)
			break
		}
	}

	fmt.Fprintf(b, `// %sClient is the entry point for %s queries.
type %sClient struct {
	db *pg.DB
}

// Find returns a %s by primary key.
func (c *%sClient) Find(ctx context.Context, id %s) (*%s, error) {
	return c.Where(%sWhere.Id.Eq(id)).First(ctx)
}

`, name, name, name,
		name, name, idType, name, name)

	// Where — @soft models inject deleted_at IS NULL by default
	if soft {
		fmt.Fprintf(b, `// Where starts a query with conditions. @soft: auto-filters deleted records.
func (c *%sClient) Where(conds ...lux.Condition) *pg.Query[%s] {
	conds = append(conds, lux.NewTimeField("deleted_at").IsNull())
	return pg.NewQuery(c.db, %q, %s, conds)
}

// WithDeleted starts a query that includes soft-deleted records.
func (c *%sClient) WithDeleted(conds ...lux.Condition) *pg.Query[%s] {
	return pg.NewQuery(c.db, %q, %s, conds)
}

`, name, name, tableName, scanFn,
			name, name, tableName, scanFn)
	} else {
		fmt.Fprintf(b, `// Where starts a query with conditions.
func (c *%sClient) Where(conds ...lux.Condition) *pg.Query[%s] {
	return pg.NewQuery(c.db, %q, %s, conds)
}

`, name, name, tableName, scanFn)
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
	conds = append(conds, lux.NewTimeField("deleted_at").IsNull())
	return pg.NewQuery[%s](c.db, %q, %s, conds).Count(ctx)
}

// Exists returns true if any non-deleted record matches conditions.
func (c *%sClient) Exists(ctx context.Context, conds ...lux.Condition) (bool, error) {
	return c.Where(conds...).Exists(ctx)
}

`, name, name, tableName, scanFn, name)
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
	conds = append(conds, lux.NewTimeField("deleted_at").IsNull())
	q := pg.NewQuery[%s](c.db, %q, %s, conds)
	return q.Update(ctx, lux.SetField{Col: "deleted_at", Val: time.Now()})
}

// ForceDelete permanently removes matching records from the database.
func (c *%sClient) ForceDelete(ctx context.Context, conds ...lux.Condition) (int64, error) {
	q := pg.NewQuery[%s](c.db, %q, %s, conds)
	return q.Delete(ctx)
}

// Restore un-deletes soft-deleted records matching the conditions.
func (c *%sClient) Restore(ctx context.Context, conds ...lux.Condition) (int64, error) {
	q := pg.NewQuery[%s](c.db, %q, %s, conds)
	return q.Update(ctx, lux.SetField{Col: "deleted_at", Val: nil})
}

`, name, name, tableName, scanFn,
			name, name, tableName, scanFn,
			name, name, tableName, scanFn)
	}
}

// --- Field constants ---

func generateFieldConstants(b *strings.Builder, name string, fields []*ast.FieldDecl) {
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
		col := str.ToSnakeCase(f.Name)
		fmt.Fprintf(b, "\t%s: %q,\n", str.Capitalize(f.Name), col)
	}
	b.WriteString("}\n\n")
}

// --- Where fields ---

func generateWhereFields(b *strings.Builder, name string, fields []*ast.FieldDecl) {
	fmt.Fprintf(b, "// %sWhere provides type-safe field conditions.\n", name)
	fmt.Fprintf(b, "var %sWhere = struct {\n", name)
	for _, f := range fields {
		if f.Computed != nil {
			continue
		}
		goFieldName := str.Capitalize(f.Name)
		condType := fieldConditionType(f.Type)
		fmt.Fprintf(b, "\t%s lux.%s\n", goFieldName, condType)
	}
	b.WriteString("}{\n")
	for _, f := range fields {
		if f.Computed != nil {
			continue
		}
		col := str.ToSnakeCase(f.Name)
		condType := fieldConditionType(f.Type)
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

// --- Create builder ---

// collectAutoFills returns Set statements for auto-managed fields (UUID @auto, createdAt/updatedAt).
func collectAutoFills(fields []*ast.FieldDecl) []string {
	var fills []string
	for _, f := range fields {
		if f.Computed != nil || f.Type == nil {
			continue
		}
		col := str.ToSnakeCase(f.Name)
		if hasDirective(f.Directives, "auto") && f.Type.Name == "UUID" {
			fills = append(fills, fmt.Sprintf("\tb.Set(%q, uuid.Must(uuid.NewV7()))\n", col))
		}
		if f.Type.Name == "DateTime" && (f.Name == "createdAt" || f.Name == "updatedAt") {
			fills = append(fills, fmt.Sprintf("\tb.Set(%q, time.Now())\n", col))
		}
	}
	return fills
}

func generateCreateBuilder(b *strings.Builder, name string, fields []*ast.FieldDecl) {
	fmt.Fprintf(b, `// %sCreateBuilder builds an INSERT statement.
type %sCreateBuilder struct {
	pg.CreateBase[%s]
}

`, name, name, name)

	// Generate Set methods — skip auto-managed, @internal, computed
	for _, f := range fields {
		if f.Computed != nil || hasDirective(f.Directives, "internal") || isAutoManaged(f) {
			continue
		}
		goFieldName := str.Capitalize(f.Name)
		goType := resolveGoType(f.Type)
		col := str.ToSnakeCase(f.Name)
		fmt.Fprintf(b, "func (b *%sCreateBuilder) Set%s(v %s) *%sCreateBuilder {\n", name, goFieldName, goType, name)
		fmt.Fprintf(b, "\tb.Set(%q, v)\n", col)
		fmt.Fprintf(b, "\treturn b\n")
		fmt.Fprintf(b, "}\n\n")
	}

	autoFills := collectAutoFills(fields)
	if len(autoFills) > 0 {
		fmt.Fprintf(b, "func (b *%sCreateBuilder) Exec(ctx context.Context) (*%s, error) {\n", name, name)
		for _, fill := range autoFills {
			b.WriteString(fill)
		}
		fmt.Fprintf(b, "\treturn b.CreateBase.Exec(ctx)\n")
		fmt.Fprintf(b, "}\n\n")
	}
}

// --- Create many builder ---

// autoCol represents an auto-managed column with its Go expression.
type autoCol struct {
	col  string
	expr string
}

// collectAutoCols returns auto-managed columns that need generated values.
func collectAutoCols(fields []*ast.FieldDecl) []autoCol {
	var result []autoCol
	for _, f := range fields {
		if f.Computed != nil || f.Type == nil {
			continue
		}
		col := str.ToSnakeCase(f.Name)
		if hasDirective(f.Directives, "auto") && f.Type.Name == "UUID" {
			result = append(result, autoCol{col, "uuid.Must(uuid.NewV7())"})
		}
		if f.Type.Name == "DateTime" && (f.Name == "createdAt" || f.Name == "updatedAt") {
			result = append(result, autoCol{col, "now"})
		}
	}
	return result
}

// collectUserFields returns fields that are user-provided (not computed, not internal, not auto-managed, not relation).
func collectUserFields(fields []*ast.FieldDecl) []*ast.FieldDecl {
	var result []*ast.FieldDecl
	for _, f := range fields {
		if f.Computed != nil || hasDirective(f.Directives, "internal") || isAutoManaged(f) {
			continue
		}
		result = append(result, f)
	}
	return result
}

// buildCreateManyColumns builds the column list and per-row value extraction strings.
func buildCreateManyColumns(userFields []*ast.FieldDecl, autoCols []autoCol) (cols string, vals string) {
	var allCols strings.Builder
	for i, f := range userFields {
		if i > 0 {
			allCols.WriteString(", ")
		}
		fmt.Fprintf(&allCols, "%q", str.ToSnakeCase(f.Name))
	}
	for _, ac := range autoCols {
		allCols.WriteString(", ")
		fmt.Fprintf(&allCols, "%q", ac.col)
	}

	var valsExtract strings.Builder
	for _, f := range userFields {
		fmt.Fprintf(&valsExtract, "r.%s, ", str.Capitalize(f.Name))
	}
	for _, ac := range autoCols {
		fmt.Fprintf(&valsExtract, "%s, ", ac.expr)
	}
	return allCols.String(), valsExtract.String()
}

func generateCreateManyBuilder(b *strings.Builder, name string, fields []*ast.FieldDecl) {
	userFields := collectUserFields(fields)
	autoCols := collectAutoCols(fields)

	var structFields strings.Builder
	for _, f := range userFields {
		goType := resolveGoType(f.Type)
		fmt.Fprintf(&structFields, "\t%s %s\n", str.Capitalize(f.Name), goType)
	}

	colsStr, valsStr := buildCreateManyColumns(userFields, autoCols)
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
}

// Add appends a record to the batch.
func (b *%sCreateManyBuilder) Add(input %sCreateInput) *%sCreateManyBuilder {
	b.rows = append(b.rows, input)
	return b
}

// Exec inserts all records and returns the created rows.
func (b *%sCreateManyBuilder) Exec(ctx context.Context) ([]*%s, error) {
`, name, name, name, structFields.String(),
		name, name, name,
		name, name, name,
		name, name)

	if hasTimeAuto {
		b.WriteString("\tnow := time.Now()\n")
	}

	fmt.Fprintf(b, "\tcols := []string{%s}\n", colsStr)
	fmt.Fprintf(b, "\trows := make([][]any, len(b.rows))\n")
	fmt.Fprintf(b, "\tfor i, r := range b.rows {\n")
	fmt.Fprintf(b, "\t\trows[i] = []any{%s}\n", valsStr)
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\treturn pg.InsertManyReturning(ctx, b.db, %s, b.table, cols, rows)\n", scanFn)
	fmt.Fprintf(b, "}\n\n")
}

// --- Update builder ---

func generateUpdateBuilder(b *strings.Builder, name string, fields []*ast.FieldDecl) {
	scanFn := "scan" + name

	// Determine id field type.
	idType := "int64"
	for _, f := range fields {
		if f.Name == "id" && f.Type != nil {
			idType = resolveGoType(f.Type)
			break
		}
	}

	fmt.Fprintf(b, `// %sUpdateBuilder builds an UPDATE statement. Exec is inherited from pg.UpdateBase.
type %sUpdateBuilder struct {
	pg.UpdateBase[%s, %s]
}

`, name, name, name, idType)

	fmt.Fprintf(b, `// new%sUpdateBuilder creates an update builder with the scan function pre-set.
func new%sUpdateBuilder(db *pg.DB, table string, id %s) *%sUpdateBuilder {
	return &%sUpdateBuilder{UpdateBase: pg.UpdateBase[%s, %s]{Db: db, Table: table, ID: id, Scan: %s}}
}

`, name, name, idType, name, name, name, idType, scanFn)

	// Generate Set methods — skip auto-managed, @immutable, @internal, computed, relation
	for _, f := range fields {
		if f.Computed != nil || isAutoManaged(f) {
			continue
		}
		if hasDirective(f.Directives, "immutable") || hasDirective(f.Directives, "internal") {
			continue
		}
		goFieldName := str.Capitalize(f.Name)
		goType := resolveGoType(f.Type)
		col := str.ToSnakeCase(f.Name)
		fmt.Fprintf(b, "func (b *%sUpdateBuilder) Set%s(v %s) *%sUpdateBuilder {\n", name, goFieldName, goType, name)
		fmt.Fprintf(b, "\tb.Set(%q, v)\n", col)
		fmt.Fprintf(b, "\treturn b\n")
		fmt.Fprintf(b, "}\n\n")
	}

	// Auto-fill updatedAt on update
	hasUpdatedAt := false
	for _, f := range fields {
		if isAutoOnUpdate(f) {
			hasUpdatedAt = true
			break
		}
	}
	if hasUpdatedAt {
		fmt.Fprintf(b, "func (b *%sUpdateBuilder) Exec(ctx context.Context) (*%s, error) {\n", name, name)
		fmt.Fprintf(b, "\tb.Set(\"updated_at\", time.Now())\n")
		fmt.Fprintf(b, "\treturn b.UpdateBase.Exec(ctx)\n")
		fmt.Fprintf(b, "}\n\n")
	}
}
