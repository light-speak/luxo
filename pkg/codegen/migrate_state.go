package codegen

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/semantic"
)

// SchemaState represents the database schema at a point in time.
type SchemaState struct {
	Models map[string]*ModelState `json:"models"`
}

// ModelState represents a single table's state.
type ModelState struct {
	Table   string                  `json:"table"`
	Columns map[string]*ColumnState `json:"columns"`
	Indexes []lux.IndexInfo         `json:"indexes,omitempty"`
}

// ColumnState represents a single column's state.
type ColumnState struct {
	Type     string `json:"type"`
	Nullable bool   `json:"nullable,omitempty"`
	PK       bool   `json:"pk,omitempty"`
	Unique   bool   `json:"unique,omitempty"`
	Serial   bool   `json:"serial,omitempty"`
	Default  string `json:"default,omitempty"`
}

// LoadState reads the schema state from a file. Returns empty state if not found.
func LoadState(path string) (*SchemaState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SchemaState{Models: make(map[string]*ModelState)}, nil
		}
		return nil, err
	}
	var s SchemaState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Models == nil {
		s.Models = make(map[string]*ModelState)
	}
	return &s, nil
}

// SaveState writes the schema state to a file.
func SaveState(path string, s *SchemaState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// ComputeState builds the desired schema state from the current AST.
func ComputeState(result *semantic.Result, enums map[string]bool, dialect lux.Dialect) *SchemaState {
	s := &SchemaState{Models: make(map[string]*ModelState)}

	for _, file := range result.Files {
		for _, m := range file.Models {
			ms := &ModelState{
				Table:   toSnakeCase(m.Name) + "s",
				Columns: make(map[string]*ColumnState),
			}

			for _, f := range m.Fields {
				if f.Computed != nil || isRelationField(f, enums) {
					continue
				}
				col := buildColumnState(f, dialect)
				if col != nil {
					ms.Columns[toSnakeCase(f.Name)] = col
				}
			}

			// Auto timestamps
			tsType := dialect.ColumnType("DateTime", false, nil)
			if !hasDirective(m.Directives, "noTime") {
				if _, ok := ms.Columns["created_at"]; !ok {
					ms.Columns["created_at"] = &ColumnState{Type: tsType, Default: "NOW()"}
				}
				if _, ok := ms.Columns["updated_at"]; !ok {
					ms.Columns["updated_at"] = &ColumnState{Type: tsType, Default: "NOW()"}
				}
			}

			// Soft delete
			if isSoftDelete(m) {
				if _, ok := ms.Columns["deleted_at"]; !ok {
					ms.Columns["deleted_at"] = &ColumnState{Type: tsType, Nullable: true}
				}
			}

			// Indexes — all types
			ms.Indexes = collectIndexes(m, ms.Table)

			s.Models[m.Name] = ms
		}
	}

	return s
}

// buildColumnState creates a ColumnState from a field declaration.
func buildColumnState(f *ast.FieldDecl, dialect lux.Dialect) *ColumnState {
	if f.Type == nil || f.Type.IsList {
		return nil
	}

	isSerial := hasDirective(f.Directives, "serial")
	dirs := extractDirectiveInfos(f.Directives)
	sqlType := dialect.ColumnType(f.Type.Name, isSerial, dirs)

	cs := &ColumnState{
		Type:     sqlType,
		Nullable: f.Type.Nullable,
		PK:       isSerial || hasDirective(f.Directives, "id"),
		Unique:   hasDirective(f.Directives, "unique"),
		Serial:   isSerial,
	}
	if f.Default != nil {
		if lit, ok := f.Default.(*ast.Literal); ok {
			cs.Default = defaultSQL(f.Type.Name, lit.Value)
		} else if ident, ok := f.Default.(*ast.Ident); ok {
			// Enum default: Role.USER → 'USER'
			parts := strings.SplitN(ident.Name, ".", 2)
			if len(parts) == 2 {
				cs.Default = "'" + parts[1] + "'"
			}
		}
	}
	return cs
}

// collectIndexes extracts all index definitions from a model.
func collectIndexes(m *ast.ModelDecl, table string) []lux.IndexInfo {
	var indexes []lux.IndexInfo

	// Model-level @index(fields: [...]) → composite index
	for _, d := range m.Directives {
		if d.Name == "index" {
			cols := extractIndexFields(d)
			if len(cols) > 0 {
				name := "idx_" + table + "_" + strings.Join(cols, "_")
				indexes = append(indexes, lux.IndexInfo{Name: name, Kind: lux.CompositeIndex, Columns: cols})
			}
		}
	}

	// Field-level directives
	for _, f := range m.Fields {
		if f.Computed != nil {
			continue
		}
		col := toSnakeCase(f.Name)

		if hasDirective(f.Directives, "index") {
			indexes = append(indexes, lux.IndexInfo{
				Name: "idx_" + table + "_" + col, Kind: lux.BTreeIndex, Columns: []string{col},
			})
		}
		if hasDirective(f.Directives, "search") {
			indexes = append(indexes, lux.IndexInfo{
				Name: "idx_" + table + "_" + col + "_search", Kind: lux.SearchIndex, Columns: []string{col},
			})
		}
		if hasDirective(f.Directives, "brin") {
			indexes = append(indexes, lux.IndexInfo{
				Name: "idx_" + table + "_" + col + "_brin", Kind: lux.BrinIndex, Columns: []string{col},
			})
		}
	}

	return indexes
}

// extractIndexFields extracts field names from @index(fields: [name, email]).
func extractIndexFields(d *ast.Directive) []string {
	for _, arg := range d.Args {
		if arg.Name == "fields" {
			if list, ok := arg.Value.(*ast.ListExpr); ok {
				var cols []string
				for _, item := range list.Items {
					if ident, ok := item.(*ast.Ident); ok {
						cols = append(cols, toSnakeCase(ident.Name))
					}
				}
				return cols
			}
		}
	}
	return nil
}

// defaultSQL converts a Luxo default value to SQL representation.
func defaultSQL(luxoType, value string) string {
	switch luxoType {
	case "String":
		return "'" + value + "'"
	case "Boolean":
		if value == "true" {
			return "TRUE"
		}
		return "FALSE"
	default:
		return value
	}
}

// extractDirectiveInfos converts AST directives to the dialect-layer DirectiveInfo.
func extractDirectiveInfos(directives []*ast.Directive) []lux.DirectiveInfo {
	var infos []lux.DirectiveInfo
	for _, d := range directives {
		info := lux.DirectiveInfo{Name: d.Name}
		if len(d.Args) > 0 {
			if lit, ok := d.Args[0].Value.(*ast.Literal); ok {
				info.Arg = lit.Value
			}
		}
		infos = append(infos, info)
	}
	return infos
}

// DiffOp represents a single schema change operation.
type DiffOp struct {
	Kind      DiffKind
	Model     string // model name
	Table     string // table name
	Column    string // column name (for column ops)
	OldColumn *ColumnState
	NewColumn *ColumnState
	Index     *lux.IndexInfo // index info (for index ops)
}

// DiffKind is the type of schema change.
type DiffKind int

const (
	CreateTable DiffKind = iota
	DropTable
	AddColumn
	DropColumn
	AlterColumn
	AddIndex
	DropIndex
)

// Diff compares two schema states and returns the operations needed to go from current to desired.
func Diff(current, desired *SchemaState) []DiffOp {
	var ops []DiffOp

	// New models → CREATE TABLE
	for name, dm := range desired.Models {
		if _, ok := current.Models[name]; !ok {
			ops = append(ops, DiffOp{Kind: CreateTable, Model: name, Table: dm.Table})
		}
	}

	// Removed models → DROP TABLE
	for name, cm := range current.Models {
		if _, ok := desired.Models[name]; !ok {
			ops = append(ops, DiffOp{Kind: DropTable, Model: name, Table: cm.Table})
		}
	}

	// Changed models → column diff
	for name, dm := range desired.Models {
		cm, ok := current.Models[name]
		if !ok {
			continue // handled by CreateTable
		}
		ops = append(ops, diffColumns(name, dm.Table, cm, dm)...)
		ops = append(ops, diffIndexes(name, dm.Table, cm, dm)...)
	}

	return ops
}

// diffColumns compares columns between current and desired model states.
func diffColumns(model, table string, current, desired *ModelState) []DiffOp {
	var ops []DiffOp

	// New columns
	for col, dc := range desired.Columns {
		if _, ok := current.Columns[col]; !ok {
			ops = append(ops, DiffOp{Kind: AddColumn, Model: model, Table: table, Column: col, NewColumn: dc})
		}
	}

	// Removed columns
	for col, cc := range current.Columns {
		if _, ok := desired.Columns[col]; !ok {
			ops = append(ops, DiffOp{Kind: DropColumn, Model: model, Table: table, Column: col, OldColumn: cc})
		}
	}

	// Changed columns
	for col, dc := range desired.Columns {
		cc, ok := current.Columns[col]
		if !ok {
			continue
		}
		if cc.Type != dc.Type || cc.Nullable != dc.Nullable || cc.Unique != dc.Unique || cc.Default != dc.Default {
			ops = append(ops, DiffOp{Kind: AlterColumn, Model: model, Table: table, Column: col, OldColumn: cc, NewColumn: dc})
		}
	}

	return ops
}

// diffIndexes compares indexes between current and desired.
func diffIndexes(model, table string, current, desired *ModelState) []DiffOp {
	var ops []DiffOp

	currentIdx := make(map[string]*lux.IndexInfo, len(current.Indexes))
	for i := range current.Indexes {
		currentIdx[current.Indexes[i].Name] = &current.Indexes[i]
	}
	desiredIdx := make(map[string]*lux.IndexInfo, len(desired.Indexes))
	for i := range desired.Indexes {
		desiredIdx[desired.Indexes[i].Name] = &desired.Indexes[i]
	}

	for name, idx := range desiredIdx {
		if _, ok := currentIdx[name]; !ok {
			ops = append(ops, DiffOp{Kind: AddIndex, Model: model, Table: table, Index: idx})
		}
	}
	for name, idx := range currentIdx {
		if _, ok := desiredIdx[name]; !ok {
			ops = append(ops, DiffOp{Kind: DropIndex, Model: model, Table: table, Index: idx})
		}
	}

	return ops
}

// GenerateMigrationSQL generates up/down SQL from diff operations using the given dialect.
func GenerateMigrationSQL(ops []DiffOp, desired *SchemaState, dialect lux.Dialect) (up, down string) {
	var upB, downB strings.Builder
	for _, op := range ops {
		generateOpSQL(&upB, &downB, op, desired, dialect)
	}
	return upB.String(), downB.String()
}

func generateOpSQL(upB, downB *strings.Builder, op DiffOp, desired *SchemaState, d lux.Dialect) {
	switch op.Kind {
	case CreateTable:
		ms := desired.Models[op.Model]
		upB.WriteString(generateCreateTableSQL(ms, d))
		fmt.Fprintf(downB, "DROP TABLE IF EXISTS %s;\n", op.Table) // down is always real DROP (reverting a CREATE)
	case DropTable:
		upB.WriteString(d.DropTable(op.Table))
		fmt.Fprintf(downB, "-- Recreate %s table manually if needed\n", op.Table)
	case AddColumn:
		upB.WriteString(d.AddColumn(op.Table, d.ColumnDef(op.Column, toColumnInfo(op.NewColumn))))
		fmt.Fprintf(downB, "ALTER TABLE %s DROP COLUMN %s;\n", op.Table, op.Column) // down reverts ADD, always real DROP
	case DropColumn:
		upB.WriteString(d.DropColumn(op.Table, op.Column))
		downB.WriteString(d.AddColumn(op.Table, d.ColumnDef(op.Column, toColumnInfo(op.OldColumn))))
	case AlterColumn:
		generateAlterColumnSQL(upB, downB, op, d)
	case AddIndex:
		upB.WriteString(d.CreateIndexSQL(*op.Index, op.Table))
		downB.WriteString(d.DropIndex(op.Index.Name))
	case DropIndex:
		upB.WriteString(d.DropIndex(op.Index.Name))
		downB.WriteString(d.CreateIndexSQL(*op.Index, op.Table))
	}
}

func generateAlterColumnSQL(upB, downB *strings.Builder, op DiffOp, d lux.Dialect) {
	if op.OldColumn.Type != op.NewColumn.Type {
		upB.WriteString(d.AlterColumnType(op.Table, op.Column, op.NewColumn.Type))
		downB.WriteString(d.AlterColumnType(op.Table, op.Column, op.OldColumn.Type))
	}
	if op.OldColumn.Nullable != op.NewColumn.Nullable {
		if op.NewColumn.Nullable {
			upB.WriteString(d.DropNotNull(op.Table, op.Column))
			downB.WriteString(d.SetNotNull(op.Table, op.Column))
		} else {
			upB.WriteString(d.SetNotNull(op.Table, op.Column))
			downB.WriteString(d.DropNotNull(op.Table, op.Column))
		}
	}
	if !op.OldColumn.Unique && op.NewColumn.Unique {
		upB.WriteString(d.AddUnique(op.Table, op.Column))
		downB.WriteString(d.DropUnique(op.Table, op.Column))
	}
	if op.OldColumn.Unique && !op.NewColumn.Unique {
		upB.WriteString(d.DropUnique(op.Table, op.Column))
		downB.WriteString(d.AddUnique(op.Table, op.Column))
	}
	if op.OldColumn.Default != op.NewColumn.Default {
		if op.NewColumn.Default != "" {
			fmt.Fprintf(upB, "ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;\n", op.Table, op.Column, op.NewColumn.Default)
		} else {
			fmt.Fprintf(upB, "ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT;\n", op.Table, op.Column)
		}
		if op.OldColumn.Default != "" {
			fmt.Fprintf(downB, "ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;\n", op.Table, op.Column, op.OldColumn.Default)
		} else {
			fmt.Fprintf(downB, "ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT;\n", op.Table, op.Column)
		}
	}
}

// generateCreateTableSQL produces CREATE TABLE using dialect.
func generateCreateTableSQL(ms *ModelState, d lux.Dialect) string {
	// Sort columns: PK first, then alphabetical
	type colEntry struct {
		name string
		cs   *ColumnState
	}
	var entries []colEntry
	for name, cs := range ms.Columns {
		entries = append(entries, colEntry{name, cs})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].cs.PK != entries[j].cs.PK {
			return entries[i].cs.PK
		}
		return entries[i].name < entries[j].name
	})

	columns := make([]lux.ColumnEntry, 0, len(entries))
	for _, e := range entries {
		columns = append(columns, lux.ColumnEntry{Name: e.name, Info: toColumnInfo(e.cs)})
	}
	return d.CreateTable(ms.Table, columns, ms.Indexes)
}

// toColumnInfo converts internal ColumnState to dialect-layer ColumnInfo.
func toColumnInfo(cs *ColumnState) lux.ColumnInfo {
	return lux.ColumnInfo{
		Type:     cs.Type,
		Nullable: cs.Nullable,
		PK:       cs.PK,
		Unique:   cs.Unique,
		Serial:   cs.Serial,
		Default:  cs.Default,
	}
}

// AutoMigrationName generates a descriptive name from diff operations.
func AutoMigrationName(ops []DiffOp) string {
	if len(ops) == 0 {
		return "empty"
	}

	// Count by kind
	creates := collectTables(ops, CreateTable)
	drops := collectTables(ops, DropTable)
	adds := collectColumnsByTable(ops, AddColumn)
	alters := collectTables(ops, AlterColumn)

	// Only CREATE TABLEs
	if len(creates) > 0 && len(drops) == 0 && len(adds) == 0 && len(alters) == 0 {
		return "create_" + joinNames(creates, 3)
	}

	// Only ADD COLUMN on a single table
	if len(adds) == 1 && len(creates) == 0 && len(drops) == 0 {
		for table, cols := range adds {
			return "add_" + joinNames(cols, 3) + "_to_" + table
		}
	}

	// Mixed — use "update" + table names
	tables := make(map[string]bool)
	for _, op := range ops {
		tables[op.Table] = true
	}
	var names []string
	for t := range tables {
		names = append(names, t)
	}
	return "update_" + joinNames(names, 3)
}

func collectTables(ops []DiffOp, kind DiffKind) []string {
	seen := make(map[string]bool)
	var result []string
	for _, op := range ops {
		if op.Kind == kind && !seen[op.Table] {
			seen[op.Table] = true
			result = append(result, op.Table)
		}
	}
	return result
}

func collectColumnsByTable(ops []DiffOp, kind DiffKind) map[string][]string {
	result := make(map[string][]string)
	for _, op := range ops {
		if op.Kind == kind {
			result[op.Table] = append(result[op.Table], op.Column)
		}
	}
	return result
}

func joinNames(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, "_")
	}
	first := strings.Join(names[:max], "_")
	return fmt.Sprintf("%s_and_%d_more", first, len(names)-max)
}
