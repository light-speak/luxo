package codegen

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
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
	Indexes []string                `json:"indexes,omitempty"`
}

// ColumnState represents a single column's state.
type ColumnState struct {
	Type     string `json:"type"`
	Nullable bool   `json:"nullable,omitempty"`
	PK       bool   `json:"pk,omitempty"`
	Unique   bool   `json:"unique,omitempty"`
	Serial   bool   `json:"serial,omitempty"`
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
func ComputeState(result *semantic.Result, enums map[string]bool) *SchemaState {
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
				col := buildColumnState(f)
				if col != nil {
					ms.Columns[toSnakeCase(f.Name)] = col
				}
			}

			// Auto timestamps
			if !hasDirective(m.Directives, "noTime") {
				if _, ok := ms.Columns["created_at"]; !ok {
					ms.Columns["created_at"] = &ColumnState{Type: "TIMESTAMPTZ"}
				}
				if _, ok := ms.Columns["updated_at"]; !ok {
					ms.Columns["updated_at"] = &ColumnState{Type: "TIMESTAMPTZ"}
				}
			}

			// Soft delete
			if isSoftDelete(m) {
				if _, ok := ms.Columns["deleted_at"]; !ok {
					ms.Columns["deleted_at"] = &ColumnState{Type: "TIMESTAMPTZ", Nullable: true}
				}
			}

			// Indexes
			for _, f := range m.Fields {
				if hasDirective(f.Directives, "index") {
					ms.Indexes = append(ms.Indexes, fmt.Sprintf("idx_%s_%s", ms.Table, toSnakeCase(f.Name)))
				}
			}

			s.Models[m.Name] = ms
		}
	}

	return s
}

// buildColumnState creates a ColumnState from a field declaration.
func buildColumnState(f *ast.FieldDecl) *ColumnState {
	sqlType := resolveColumnType(f)
	if sqlType == "" {
		return nil
	}

	isSerial := hasDirective(f.Directives, "serial")
	if isSerial {
		sqlType = "SERIAL"
	}

	return &ColumnState{
		Type:     sqlType,
		Nullable: f.Type != nil && f.Type.Nullable,
		PK:       isSerial || hasDirective(f.Directives, "id"),
		Unique:   hasDirective(f.Directives, "unique"),
		Serial:   isSerial,
	}
}

// DiffOp represents a single schema change operation.
type DiffOp struct {
	Kind      DiffKind
	Model     string // model name
	Table     string // table name
	Column    string // column name (for column ops)
	OldColumn *ColumnState
	NewColumn *ColumnState
	Index     string // index name (for index ops)
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
		if cc.Type != dc.Type || cc.Nullable != dc.Nullable || cc.Unique != dc.Unique {
			ops = append(ops, DiffOp{Kind: AlterColumn, Model: model, Table: table, Column: col, OldColumn: cc, NewColumn: dc})
		}
	}

	return ops
}

// diffIndexes compares indexes between current and desired.
func diffIndexes(model, table string, current, desired *ModelState) []DiffOp {
	var ops []DiffOp

	currentIdx := make(map[string]bool, len(current.Indexes))
	for _, idx := range current.Indexes {
		currentIdx[idx] = true
	}
	desiredIdx := make(map[string]bool, len(desired.Indexes))
	for _, idx := range desired.Indexes {
		desiredIdx[idx] = true
	}

	for idx := range desiredIdx {
		if !currentIdx[idx] {
			ops = append(ops, DiffOp{Kind: AddIndex, Model: model, Table: table, Index: idx})
		}
	}
	for idx := range currentIdx {
		if !desiredIdx[idx] {
			ops = append(ops, DiffOp{Kind: DropIndex, Model: model, Table: table, Index: idx})
		}
	}

	return ops
}

// GenerateMigrationSQL generates up/down SQL from diff operations.
func GenerateMigrationSQL(ops []DiffOp, desired *SchemaState) (up, down string) {
	var upB, downB strings.Builder
	for _, op := range ops {
		generateOpSQL(&upB, &downB, op, desired)
	}
	return upB.String(), downB.String()
}

func generateOpSQL(upB, downB *strings.Builder, op DiffOp, desired *SchemaState) {
	switch op.Kind {
	case CreateTable:
		upB.WriteString(generateCreateTableSQL(op.Table, desired.Models[op.Model]))
		fmt.Fprintf(downB, "DROP TABLE IF EXISTS %s;\n", op.Table)
	case DropTable:
		fmt.Fprintf(upB, "-- DROP TABLE IF EXISTS %s; -- DANGEROUS: uncomment to apply\n", op.Table)
		fmt.Fprintf(downB, "-- Recreate %s table manually if needed\n", op.Table)
	case AddColumn:
		fmt.Fprintf(upB, "ALTER TABLE %s ADD COLUMN %s;\n", op.Table, columnDef(op.Column, op.NewColumn))
		fmt.Fprintf(downB, "ALTER TABLE %s DROP COLUMN %s;\n", op.Table, op.Column)
	case DropColumn:
		fmt.Fprintf(upB, "-- ALTER TABLE %s DROP COLUMN %s; -- DANGEROUS: uncomment to apply\n", op.Table, op.Column)
		fmt.Fprintf(downB, "ALTER TABLE %s ADD COLUMN %s;\n", op.Table, columnDef(op.Column, op.OldColumn))
	case AlterColumn:
		generateAlterColumnSQL(upB, downB, op)
	case AddIndex:
		col := strings.TrimPrefix(op.Index, "idx_"+op.Table+"_")
		fmt.Fprintf(upB, "CREATE INDEX %s ON %s (%s);\n", op.Index, op.Table, col)
		fmt.Fprintf(downB, "DROP INDEX IF EXISTS %s;\n", op.Index)
	case DropIndex:
		col := strings.TrimPrefix(op.Index, "idx_"+op.Table+"_")
		fmt.Fprintf(upB, "DROP INDEX IF EXISTS %s;\n", op.Index)
		fmt.Fprintf(downB, "CREATE INDEX %s ON %s (%s);\n", op.Index, op.Table, col)
	}
}

func generateAlterColumnSQL(upB, downB *strings.Builder, op DiffOp) {
	if op.OldColumn.Type != op.NewColumn.Type {
		fmt.Fprintf(upB, "ALTER TABLE %s ALTER COLUMN %s TYPE %s;\n", op.Table, op.Column, op.NewColumn.Type)
		fmt.Fprintf(downB, "ALTER TABLE %s ALTER COLUMN %s TYPE %s;\n", op.Table, op.Column, op.OldColumn.Type)
	}
	if op.OldColumn.Nullable != op.NewColumn.Nullable {
		if op.NewColumn.Nullable {
			fmt.Fprintf(upB, "ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;\n", op.Table, op.Column)
			fmt.Fprintf(downB, "ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;\n", op.Table, op.Column)
		} else {
			fmt.Fprintf(upB, "ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;\n", op.Table, op.Column)
			fmt.Fprintf(downB, "ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;\n", op.Table, op.Column)
		}
	}
	if !op.OldColumn.Unique && op.NewColumn.Unique {
		fmt.Fprintf(upB, "ALTER TABLE %s ADD CONSTRAINT %s_%s_key UNIQUE (%s);\n", op.Table, op.Table, op.Column, op.Column)
		fmt.Fprintf(downB, "ALTER TABLE %s DROP CONSTRAINT %s_%s_key;\n", op.Table, op.Table, op.Column)
	}
	if op.OldColumn.Unique && !op.NewColumn.Unique {
		fmt.Fprintf(upB, "ALTER TABLE %s DROP CONSTRAINT %s_%s_key;\n", op.Table, op.Table, op.Column)
		fmt.Fprintf(downB, "ALTER TABLE %s ADD CONSTRAINT %s_%s_key UNIQUE (%s);\n", op.Table, op.Table, op.Column, op.Column)
	}
}

// generateCreateTableSQL produces CREATE TABLE for a full model state.
func generateCreateTableSQL(table string, ms *ModelState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", table)

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
			return entries[i].cs.PK // PK first
		}
		return entries[i].name < entries[j].name
	})

	var cols []string
	for _, e := range entries {
		cols = append(cols, "  "+columnDef(e.name, e.cs))
	}
	b.WriteString(strings.Join(cols, ",\n"))
	b.WriteString("\n);\n")

	for _, idx := range ms.Indexes {
		col := strings.TrimPrefix(idx, "idx_"+table+"_")
		fmt.Fprintf(&b, "CREATE INDEX %s ON %s (%s);\n", idx, table, col)
	}

	return b.String()
}

// columnDef produces a column definition string like "name TEXT NOT NULL UNIQUE".
func columnDef(name string, cs *ColumnState) string {
	var parts []string
	parts = append(parts, name+" "+cs.Type)
	if !cs.Nullable && !cs.Serial {
		parts = append(parts, "NOT NULL")
	}
	if cs.PK {
		parts = append(parts, "PRIMARY KEY")
	}
	if cs.Unique {
		parts = append(parts, "UNIQUE")
	}
	return strings.Join(parts, " ")
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
