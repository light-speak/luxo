package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/str"
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
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, fmt.Errorf("migration state must be an object")
	}
	if err := validateSchemaState(&s); err != nil {
		return nil, err
	}
	if s.Models == nil {
		s.Models = make(map[string]*ModelState)
	}
	return &s, nil
}

// SaveState writes the schema state to a file.
func SaveState(path string, s *SchemaState) error {
	data, err := MarshalState(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// MarshalState validates and encodes migration metadata with a trailing newline.
func MarshalState(s *SchemaState) ([]byte, error) {
	if err := validateSchemaState(s); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		// Unreachable for the validated, non-recursive state types; retain encoder error propagation.
		return nil, err
	}
	return append(data, '\n'), nil
}

func validateSchemaState(s *SchemaState) error {
	if s == nil {
		return fmt.Errorf("migration state must be an object")
	}
	for name, model := range s.Models {
		if model == nil {
			return fmt.Errorf("migration state model %q must be an object", name)
		}
		for field, column := range model.Columns {
			if column == nil {
				return fmt.Errorf("migration state column %q.%q must be an object", name, field)
			}
		}
	}
	return nil
}

// ComputeState builds the desired schema state from the current AST.
func ComputeState(result *semantic.Result, enums map[string]bool, dialect lux.Dialect) (*SchemaState, error) {
	if result == nil || len(result.Errors) != 0 {
		return nil, fmt.Errorf("migration state requires successful semantic analysis")
	}
	if err := validateDatabaseIdentifiers(result, enums, dialect); err != nil {
		return nil, err
	}
	s := &SchemaState{Models: make(map[string]*ModelState)}
	for _, file := range inferred.ExpandedFiles(result.Files) {
		for _, m := range file.Models {
			ms, err := computeModelState(m, enums, dialect)
			if err != nil {
				return nil, err
			}
			s.Models[m.Name] = ms
		}
	}
	return s, nil
}

func computeModelState(m *ast.ModelDecl, enums map[string]bool, dialect lux.Dialect) (*ModelState, error) {
	ms := &ModelState{Table: modelTableName(m.Name), Columns: make(map[string]*ColumnState)}
	for _, f := range m.Fields {
		if f.Computed != nil || isRelationField(f, enums) {
			continue
		}
		col, err := buildColumnState(f, dialect)
		if err != nil {
			return nil, fmt.Errorf("%s: default for %s.%s: %w", f.Pos, m.Name, f.Name, err)
		}
		if col != nil {
			ms.Columns[str.ToSnakeCase(f.Name)] = col
		}
	}
	// Managed timestamps remain database expressions, not field constants.
	tsType := dialect.ColumnType("DateTime", false, nil)
	if !hasDirective(m.Directives, "noTime") {
		if _, ok := ms.Columns["created_at"]; !ok {
			ms.Columns["created_at"] = &ColumnState{Type: tsType, Default: "NOW()"}
		}
		if _, ok := ms.Columns["updated_at"]; !ok {
			ms.Columns["updated_at"] = &ColumnState{Type: tsType, Default: "NOW()"}
		}
	}
	if isSoftDelete(m) {
		if _, ok := ms.Columns["deleted_at"]; !ok {
			ms.Columns["deleted_at"] = &ColumnState{Type: tsType, Nullable: true}
		}
	}
	ms.Indexes = collectIndexes(m, ms.Table, dialect)
	return ms, nil
}

// buildColumnState creates a ColumnState from a field declaration.
func buildColumnState(f *ast.FieldDecl, dialect lux.Dialect) (*ColumnState, error) {
	if f.Type == nil {
		return nil, nil
	}
	// Scalar array field ([T]) → native array / JSON column (per dialect).
	// Relation lists never reach here (ComputeState skips relation fields).
	// Array columns are always nullable at the DB level: a nil Go slice maps to
	// SQL NULL. Non-nullable lists normalize nil to empty on the wire; nullable
	// lists preserve the distinction between NULL and an empty array.
	cs := &ColumnState{}
	if f.Type.IsList {
		cs.Type, cs.Nullable = dialect.ArrayColumnType(f.Type.Name), true
	} else {
		cs.Serial = hasDirective(f.Directives, "serial")
		cs.Type = dialect.ColumnType(f.Type.Name, cs.Serial, extractDirectiveInfos(f.Directives))
		cs.Nullable = f.Type.Nullable
		cs.PK = cs.Serial || hasDirective(f.Directives, "id")
		cs.Unique = hasDirective(f.Directives, "unique")
	}
	// A database constant cannot execute Go transforms or produce a fresh hash.
	// Generated creates prepare these defaults before calling the database.
	if f.Default != nil {
		value, err := columnDefault(f)
		if err != nil {
			return nil, err
		}
		if !preparedMutationField(f) {
			cs.Default, err = dialect.DefaultSQL(value)
			if err != nil {
				return nil, err
			}
		}
	}
	return cs, nil
}

// collectIndexes extracts all index definitions from a model.
func collectIndexes(m *ast.ModelDecl, table string, dialect lux.Dialect) []lux.IndexInfo {
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
		col := str.ToSnakeCase(f.Name)

		if hasDirective(f.Directives, "index") || hasDirective(f.Directives, "filterable") {
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

	for index := range indexes {
		indexes[index].Name = dialect.GeneratedIdentifier(indexes[index].Name)
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
						cols = append(cols, str.ToSnakeCase(ident.Name))
					}
				}
				return cols
			}
		}
	}
	return nil
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
	OldName   string // old column name (for rename)
	OldColumn *ColumnState
	NewColumn *ColumnState
	Index     *lux.IndexInfo // index info (for index ops)
	Warning   string         // safety warning message
}

// DiffKind is the type of schema change.
type DiffKind int

const (
	CreateTable DiffKind = iota
	DropTable
	AddColumn
	DropColumn
	AlterColumn
	RenameColumn
	AddIndex
	DropIndex
)

// FieldIDMap maps field ID → column name for rename detection.
// Built from luxo.lock: old state maps old names, new state maps new names.
type FieldIDMap struct {
	OldByID map[int]string // field ID → old column name
	NewByID map[int]string // field ID → new column name
}

// Diff compares two schema states and returns the operations needed.
// fieldIDs is optional — when provided, enables rename detection via luxo.lock.
func Diff(current, desired *SchemaState, fieldIDs map[string]*FieldIDMap) ([]DiffOp, error) {
	if err := validateSchemaState(current); err != nil {
		return nil, fmt.Errorf("current migration state: %w", err)
	}
	if err := validateSchemaState(desired); err != nil {
		return nil, fmt.Errorf("desired migration state: %w", err)
	}
	var ops []DiffOp
	desiredNames := sortedMigrationKeys(desired.Models)

	for _, name := range desiredNames {
		dm := desired.Models[name]
		if _, ok := current.Models[name]; !ok {
			ops = append(ops, DiffOp{Kind: CreateTable, Model: name, Table: dm.Table})
		}
	}

	for _, name := range sortedMigrationKeys(current.Models) {
		cm := current.Models[name]
		if _, ok := desired.Models[name]; !ok {
			ops = append(ops, DiffOp{Kind: DropTable, Model: name, Table: cm.Table})
		}
	}

	for _, name := range desiredNames {
		dm := desired.Models[name]
		cm, ok := current.Models[name]
		if !ok {
			continue
		}
		var fim *FieldIDMap
		if fieldIDs != nil {
			fim = fieldIDs[name]
		}
		if cm.Table != dm.Table {
			return nil, fmt.Errorf("model %s changes table identity; use an explicit migration", name)
		}
		columns, err := diffColumns(name, dm.Table, cm, dm, fim)
		if err != nil {
			return nil, err
		}
		ops = append(ops, columns...)
		ops = append(ops, diffIndexes(name, dm.Table, cm, dm)...)
	}

	// Remove obsolete indexes before column changes, and build new indexes last.
	// The reverse migration can then restore original column names before indexes.
	slices.SortStableFunc(ops, func(left, right DiffOp) int {
		return migrationIndexPhase(left.Kind) - migrationIndexPhase(right.Kind)
	})
	return addSafetyWarnings(ops), nil
}

func migrationIndexPhase(kind DiffKind) int {
	switch kind {
	case DropIndex:
		return 0
	case AddIndex:
		return 2
	default:
		return 1
	}
}

// diffColumns compares columns between current and desired model states.
func diffColumns(model, table string, current, desired *ModelState, fim *FieldIDMap) ([]DiffOp, error) {
	renames := detectRenames(current, desired, fim)
	renamedOld, renamedNew := renamesSets(renames)

	ops, err := diffRenamedColumns(model, table, renames, current, desired)
	if err != nil {
		return nil, err
	}
	added, err := diffAddedColumns(model, table, current, desired, renamedOld, renamedNew)
	if err != nil {
		return nil, err
	}
	dropped, err := diffDroppedColumns(model, table, current, desired, renamedOld)
	if err != nil {
		return nil, err
	}
	ops = append(ops, added...)
	ops = append(ops, dropped...)
	for _, name := range sortedMigrationKeys(desired.Columns) {
		next := desired.Columns[name]
		if old, exists := current.Columns[name]; exists && !renamedOld[name] && !renamedNew[name] {
			if err := validateColumnTransition(model, name, old, next); err != nil {
				return nil, err
			}
		}
	}
	ops = append(ops, diffChangedColumns(model, table, current, desired, renamedOld, renamedNew)...)
	return ops, nil
}

func renamesSets(renames map[string]string) (old, new_ map[string]bool) {
	old = make(map[string]bool, len(renames))
	new_ = make(map[string]bool, len(renames))
	for o, n := range renames {
		old[o] = true
		new_[n] = true
	}
	return
}

func diffRenamedColumns(model, table string, renames map[string]string, current, desired *ModelState) ([]DiffOp, error) {
	if len(renames) == 0 {
		return nil, nil
	}
	for _, old := range sortedMigrationKeys(renames) {
		if err := validateColumnTransition(model, old, current.Columns[old], desired.Columns[renames[old]]); err != nil {
			return nil, err
		}
	}
	planner, err := newColumnRenamePlanner(model, table, renames, current, desired)
	if err != nil {
		return nil, err
	}
	ops, err := planner.plan()
	if err != nil {
		return nil, err
	}
	// All identities move before type/default changes can observe final names.
	for _, old := range planner.keys {
		next := renames[old]
		oldCS, newCS := current.Columns[old], desired.Columns[next]
		if columnChanged(oldCS, newCS) {
			ops = append(ops, DiffOp{Kind: AlterColumn, Model: model, Table: table, Column: next, OldColumn: oldCS, NewColumn: newCS})
		}
	}
	return ops, nil
}

func diffAddedColumns(model, table string, current, desired *ModelState, renamedOld, renamedNew map[string]bool) ([]DiffOp, error) {
	var ops []DiffOp
	for _, col := range sortedMigrationKeys(desired.Columns) {
		dc := desired.Columns[col]
		if _, ok := current.Columns[col]; ok && !renamedOld[col] || renamedNew[col] {
			continue
		}
		if dc.PK || dc.Serial {
			return nil, columnIdentityError(model, col)
		}
		ops = append(ops, DiffOp{Kind: AddColumn, Model: model, Table: table, Column: col, NewColumn: dc})
	}
	return ops, nil
}

func diffDroppedColumns(model, table string, current, desired *ModelState, renamedOld map[string]bool) ([]DiffOp, error) {
	var ops []DiffOp
	for _, col := range sortedMigrationKeys(current.Columns) {
		cc := current.Columns[col]
		if _, ok := desired.Columns[col]; ok || renamedOld[col] {
			continue
		}
		if cc.PK || cc.Serial {
			return nil, columnIdentityError(model, col)
		}
		ops = append(ops, DiffOp{Kind: DropColumn, Model: model, Table: table, Column: col, OldColumn: cc})
	}
	return ops, nil
}

func diffChangedColumns(model, table string, current, desired *ModelState, renamedOld, renamedNew map[string]bool) []DiffOp {
	var ops []DiffOp
	for _, col := range sortedMigrationKeys(desired.Columns) {
		dc := desired.Columns[col]
		cc, ok := current.Columns[col]
		if !ok || renamedOld[col] || renamedNew[col] {
			continue
		}
		if columnChanged(cc, dc) {
			ops = append(ops, DiffOp{Kind: AlterColumn, Model: model, Table: table, Column: col, OldColumn: cc, NewColumn: dc})
		}
	}
	return ops
}

func columnChanged(old, new_ *ColumnState) bool {
	return old.Type != new_.Type || old.Nullable != new_.Nullable || old.Unique != new_.Unique || old.Default != new_.Default
}

// detectRenames uses field ID mapping to find renamed columns.
// Returns map[oldColumnName]newColumnName.
func detectRenames(current, desired *ModelState, fim *FieldIDMap) map[string]string {
	if fim == nil {
		return nil
	}
	renames := make(map[string]string)
	for id, oldName := range fim.OldByID {
		newName, ok := fim.NewByID[id]
		if !ok || oldName == newName {
			continue
		}
		// Verify: old name exists in current, new name exists in desired
		if _, inCurrent := current.Columns[oldName]; !inCurrent {
			continue
		}
		if _, inDesired := desired.Columns[newName]; !inDesired {
			continue
		}
		renames[oldName] = newName
	}
	return renames
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

	for _, name := range sortedMigrationKeys(desiredIdx) {
		idx := desiredIdx[name]
		if previous, ok := currentIdx[name]; !ok || !sameIndexDefinition(previous, idx) {
			ops = append(ops, DiffOp{Kind: AddIndex, Model: model, Table: table, Index: idx})
		}
	}
	for _, name := range sortedMigrationKeys(currentIdx) {
		idx := currentIdx[name]
		if next, ok := desiredIdx[name]; !ok || !sameIndexDefinition(idx, next) {
			ops = append(ops, DiffOp{Kind: DropIndex, Model: model, Table: table, Index: idx})
		}
	}

	return ops
}

func sameIndexDefinition(left, right *lux.IndexInfo) bool {
	return left.Kind == right.Kind && left.Lang == right.Lang && slices.Equal(left.Columns, right.Columns)
}

// GenerateMigrationSQL validates all operations before emission and reverses rollback blocks.
func GenerateMigrationSQL(ops []DiffOp, desired *SchemaState, dialect lux.Dialect) (up, down string, err error) {
	if err := validateMigrationSQL(ops, desired, dialect); err != nil {
		return "", "", err
	}
	var upB, downB strings.Builder
	var boundaries []int
	if len(ops) > 1 {
		boundaries = make([]int, len(ops))
	}
	for index, op := range ops {
		generateOpSQL(&upB, &downB, op, desired, dialect)
		if boundaries != nil {
			boundaries[index] = downB.Len()
		}
	}
	if len(ops) <= 1 {
		return upB.String(), downB.String(), nil
	}
	return upB.String(), reverseMigrationSQL(downB.String(), boundaries), nil
}

// Reverse complete operation blocks, retaining statement order inside each block.
func reverseMigrationSQL(sql string, boundaries []int) string {
	var b strings.Builder
	b.Grow(len(sql))
	for index := len(boundaries) - 1; index >= 0; index-- {
		start := 0
		if index > 0 {
			start = boundaries[index-1]
		}
		b.WriteString(sql[start:boundaries[index]])
	}
	return b.String()
}

func generateOpSQL(upB, downB *strings.Builder, op DiffOp, desired *SchemaState, d lux.Dialect) {
	switch op.Kind {
	case CreateTable:
		ms := desired.Models[op.Model]
		upB.WriteString(generateCreateTableSQL(ms, d))
		fmt.Fprintf(downB, "DROP TABLE IF EXISTS %s;\n", d.QuoteIdentifier(op.Table)) // down is always real DROP (reverting a CREATE)
	case DropTable:
		upB.WriteString(d.DropTable(op.Table))
		fmt.Fprintf(downB, "-- Recreate %s table manually if needed\n", op.Table)
	case AddColumn:
		generateAddColumnSQL(upB, op.Table, op.Column, op.NewColumn, d)
		fmt.Fprintf(downB, "ALTER TABLE %s DROP COLUMN %s;\n", d.QuoteIdentifier(op.Table), d.QuoteIdentifier(op.Column)) // down reverts ADD, always real DROP
	case DropColumn:
		upB.WriteString(d.DropColumn(op.Table, op.Column))
		generateAddColumnSQL(downB, op.Table, op.Column, op.OldColumn, d)
	case RenameColumn:
		fmt.Fprintf(upB, "ALTER TABLE %s RENAME COLUMN %s TO %s;\n", d.QuoteIdentifier(op.Table), d.QuoteIdentifier(op.OldName), d.QuoteIdentifier(op.Column))
		if op.OldColumn.Unique {
			upB.WriteString(d.RenameUnique(op.Table, op.OldName, op.Column))
			downB.WriteString(d.RenameUnique(op.Table, op.Column, op.OldName))
		}
		fmt.Fprintf(downB, "ALTER TABLE %s RENAME COLUMN %s TO %s;\n", d.QuoteIdentifier(op.Table), d.QuoteIdentifier(op.Column), d.QuoteIdentifier(op.OldName))
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

func generateAddColumnSQL(b *strings.Builder, table, name string, column *ColumnState, d lux.Dialect) {
	info := toColumnInfo(column)
	info.Unique = false
	b.WriteString(d.AddColumn(table, d.ColumnDef(name, info)))
	if column.Unique {
		b.WriteString(d.AddUnique(table, name))
	}
}

func generateAlterColumnSQL(upB, downB *strings.Builder, op DiffOp, d lux.Dialect) {
	writeAlterColumnSQL(upB, op.Table, op.Column, op.OldColumn, op.NewColumn, d)
	writeAlterColumnSQL(downB, op.Table, op.Column, op.NewColumn, op.OldColumn, d)
}

func writeAlterColumnSQL(b *strings.Builder, table, column string, old, next *ColumnState, d lux.Dialect) {
	typeChanged := old.Type != next.Type
	defaultDropped := typeChanged && old.Default != ""
	if old.Unique && !next.Unique {
		b.WriteString(d.DropUnique(table, column))
	}
	if defaultDropped {
		writeColumnDefaultSQL(b, d.QuoteIdentifier(table), d.QuoteIdentifier(column), "")
	}
	if !old.Nullable && next.Nullable {
		b.WriteString(d.DropNotNull(table, column))
	}
	if typeChanged {
		b.WriteString(d.AlterColumnType(table, column, next.Type))
	}
	if old.Nullable && !next.Nullable {
		b.WriteString(d.SetNotNull(table, column))
	}
	if !old.Unique && next.Unique {
		b.WriteString(d.AddUnique(table, column))
	}
	if old.Default != next.Default || typeChanged && next.Default != "" {
		if next.Default != "" || !defaultDropped {
			writeColumnDefaultSQL(b, d.QuoteIdentifier(table), d.QuoteIdentifier(column), next.Default)
		}
	}
}

// writeColumnDefaultSQL preserves the SQL expression and accepts quoted names.
func writeColumnDefaultSQL(b *strings.Builder, table, column, value string) {
	if value == "" {
		fmt.Fprintf(b, "ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT;\n", table, column)
		return
	}
	fmt.Fprintf(b, "ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;\n", table, column, value)
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
	slices.SortFunc(entries, func(left, right colEntry) int {
		if left.cs.PK != right.cs.PK {
			if left.cs.PK {
				return -1
			}
			return 1
		}
		return strings.Compare(left.name, right.name)
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

// addSafetyWarnings annotates DiffOps with warnings about dangerous operations.
func addSafetyWarnings(ops []DiffOp) []DiffOp {
	for i := range ops {
		ops[i].Warning = classifyWarning(&ops[i])
	}
	return ops
}

func classifyWarning(op *DiffOp) string {
	switch op.Kind {
	case DropTable:
		return "DANGEROUS: table and all data will be lost"
	case DropColumn:
		return "DANGEROUS: column data will be lost"
	case AlterColumn:
		return classifyAlterWarning(op)
	}
	return ""
}

func classifyAlterWarning(op *DiffOp) string {
	if op.OldColumn == nil || op.NewColumn == nil {
		return ""
	}
	// Type change warnings
	if op.OldColumn.Type != op.NewColumn.Type {
		return classifyTypeChange(op.OldColumn.Type, op.NewColumn.Type)
	}
	// Making NOT NULL without default on existing data
	if op.OldColumn.Nullable && !op.NewColumn.Nullable && op.NewColumn.Default == "" {
		return "WARNING: setting NOT NULL without DEFAULT may fail if existing rows have NULL values"
	}
	return ""
}

// classifyTypeChange returns a warning level for type changes.
func classifyTypeChange(oldType, newType string) string {
	// Safe: widening conversions
	safe := map[string][]string{
		"SMALLINT": {"BIGINT", "DOUBLE PRECISION", "DECIMAL", "TEXT"},
		"BIGINT":   {"DOUBLE PRECISION", "DECIMAL", "TEXT"},
		"SERIAL":   {"BIGINT", "TEXT"},
	}
	for _, s := range safe[oldType] {
		if s == newType {
			return ""
		}
	}
	// Narrowing or incompatible
	if isNumeric(oldType) && !isNumeric(newType) {
		return "DANGEROUS: numeric → non-numeric conversion may fail"
	}
	if !isNumeric(oldType) && isNumeric(newType) {
		return "DANGEROUS: non-numeric → numeric conversion may fail if data is not convertible"
	}
	if oldType == "TEXT" && strings.HasPrefix(newType, "VARCHAR") {
		return "WARNING: TEXT → VARCHAR may truncate existing data"
	}
	return fmt.Sprintf("WARNING: type change %s → %s — verify data compatibility", oldType, newType)
}

func isNumeric(t string) bool {
	switch t {
	case "BIGINT", "SMALLINT", "SERIAL", "DOUBLE PRECISION", "DECIMAL":
		return true
	}
	return strings.HasPrefix(t, "DECIMAL(")
}

// BuildFieldIDMaps constructs FieldIDMap for each model by comparing old and new luxo.lock states.
func BuildFieldIDMaps(oldLock, newLock map[string]map[string]int) map[string]*FieldIDMap {
	result := make(map[string]*FieldIDMap)
	// Collect all model names
	models := make(map[string]bool)
	for m := range oldLock {
		models[m] = true
	}
	for m := range newLock {
		models[m] = true
	}

	for model := range models {
		fim := &FieldIDMap{
			OldByID: make(map[int]string),
			NewByID: make(map[int]string),
		}
		if old, ok := oldLock[model]; ok {
			for name, id := range old {
				fim.OldByID[id] = str.ToSnakeCase(name)
			}
		}
		if nw, ok := newLock[model]; ok {
			for name, id := range nw {
				fim.NewByID[id] = str.ToSnakeCase(name)
			}
		}
		result[model] = fim
	}
	return result
}

// sortedMigrationKeys reserves exact key capacity before deterministic traversal.
func sortedMigrationKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
