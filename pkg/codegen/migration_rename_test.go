package codegen

import (
	"slices"
	"strings"
	"testing"
)

func mustMigrationDiff(t testing.TB, current, desired *SchemaState, fields map[string]*FieldIDMap) []DiffOp {
	t.Helper()
	ops, err := Diff(current, desired, fields)
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func TestMigrationRenameDependencyGraphs(t *testing.T) {
	for _, targets := range [][]string{{"b", "c"}, {"b", "a"}, {"b", "c", "a"}, {"c", "d", "a", "b"}, {"d", "c", "b"}} {
		t.Run(strings.Join(targets, "_"), func(t *testing.T) {
			old, next := migrationRenameStates(targets)
			fields := migrationRenameIDs(targets)
			ops, err := Diff(old, next, fields)
			if err != nil {
				t.Fatal(err)
			}
			values := make(map[string]string)
			for i := range targets {
				name := string(rune('a' + i))
				values[name] = "value-" + name
			}
			for _, op := range ops {
				if op.Kind != RenameColumn {
					t.Fatal("pure renaming changed the schema definition", op)
				}
				value, exists := values[op.OldName]
				_, occupied := values[op.Column]
				if !exists || occupied {
					t.Fatal("rename would overwrite or access an absent column", op, values)
				}
				delete(values, op.OldName)
				values[op.Column] = value
			}
			for i, target := range targets {
				if values[target] != "value-"+string(rune('a'+i)) {
					t.Fatal("rename changed field identity", target, values)
				}
			}
			second, err := Diff(old, next, fields)
			if err != nil || !slices.EqualFunc(ops, second, func(a, b DiffOp) bool { return a.Kind == b.Kind && a.OldName == b.OldName && a.Column == b.Column }) {
				t.Fatal("same rename input produced a different plan", ops, second, err)
			}
		})
	}
}

func TestMigrationRenameMetadataGuards(t *testing.T) {
	old, next := migrationRenameStates([]string{"c", "c"})
	if ops, err := Diff(old, next, migrationRenameIDs([]string{"c", "c"})); err == nil || ops != nil {
		t.Fatal("multiple identities reached one target", ops, err)
	}
	for _, states := range [][2]*SchemaState{{nil, next}, {old, nil}} {
		if ops, err := Diff(states[0], states[1], nil); err == nil || ops != nil {
			t.Fatal("invalid diff state accepted", ops, err)
		}
	}
	old, next = migrationRenameStates([]string{"a"})
	next.Models["Row"].Table = "other"
	if ops, err := Diff(old, next, nil); err == nil || ops != nil {
		t.Fatal("table identity changed without an explicit migration", ops, err)
	}
	old, next = migrationRenameStates([]string{"b"})
	next.Models["Row"].Columns["b"].PK = true
	if ops, err := Diff(old, next, migrationRenameIDs([]string{"b"})); err == nil || ops != nil {
		t.Fatal("rename hid an unsupported primary-key transition", ops, err)
	}
}

func TestMigrationRenameTemporaryNamesAvoidOccupiedColumns(t *testing.T) {
	p := &columnRenamePlanner{table: "rows", reserved: map[string]bool{}}
	first := p.temporaryName("a", "b")
	second := p.temporaryName("a", "b")
	if first == second || len(second) > 63 {
		t.Fatal("temporary name reused an occupied column", first, second)
	}
	if err := (&columnRenamePlanner{table: "rows"}).breakCycle(); err == nil {
		t.Fatal("invalid internal graph was accepted")
	}
	broken := &columnRenamePlanner{table: "rows", pending: map[string]DiffOp{"orphan": {}}}
	if ops, err := broken.plan(); err == nil || ops != nil {
		t.Fatal("planner hid its invalid graph error", ops, err)
	}
}

func migrationRenameStates(targets []string) (*SchemaState, *SchemaState) {
	old, next := map[string]*ColumnState{}, map[string]*ColumnState{}
	for i, target := range targets {
		old[string(rune('a'+i))] = &ColumnState{Type: "TEXT", Unique: true}
		next[target] = &ColumnState{Type: "TEXT", Unique: true}
	}
	return &SchemaState{Models: map[string]*ModelState{"Row": {Table: "rows", Columns: old}}},
		&SchemaState{Models: map[string]*ModelState{"Row": {Table: "rows", Columns: next}}}
}

func migrationRenameIDs(targets []string) map[string]*FieldIDMap {
	ids := &FieldIDMap{OldByID: map[int]string{}, NewByID: map[int]string{}}
	for i, target := range targets {
		ids.OldByID[i+1], ids.NewByID[i+1] = string(rune('a'+i)), target
	}
	return map[string]*FieldIDMap{"Row": ids}
}

func TestMigrationDiffRejectsAmbiguousOrUnsupportedChanges(t *testing.T) {
	old, next := migrationRenameStates([]string{"b", "c"})
	delete(next.Models["Row"].Columns, "c")
	ids := migrationRenameIDs([]string{"b"})
	if ops, err := Diff(old, next, ids); err == nil || ops != nil {
		t.Fatal("rename overwrote a nonmoving column", ops, err)
	}
	for _, change := range []func(*ColumnState){func(c *ColumnState) { c.PK = true }, func(c *ColumnState) { c.Serial = true }} {
		old, next := migrationRenameStates([]string{"a"})
		change(next.Models["Row"].Columns["a"])
		if ops, err := Diff(old, next, nil); err == nil || ops != nil {
			t.Fatal("unsupported primary/serial change was silently ignored", ops, err)
		}
	}
}

func TestMigrationRenameAllowsReusingVacatedName(t *testing.T) {
	old, next := migrationRenameStates([]string{"b"})
	next.Models["Row"].Columns["a"] = &ColumnState{Type: "BIGINT", Default: "7"}
	fields := migrationRenameIDs([]string{"b"})
	fields["Row"].NewByID[2] = "a"
	ops := mustMigrationDiff(t, old, next, fields)
	if len(ops) != 2 || ops[0].Kind != RenameColumn || ops[1].Kind != AddColumn || ops[1].Column != "a" {
		t.Fatal("a new field reused the old identity instead of the vacated name", ops)
	}
	if ops[1].NewColumn != next.Models["Row"].Columns["a"] {
		t.Fatal("new column metadata was not preserved")
	}
}

func TestMigrationDiffRequiresExplicitKeyIdentityChange(t *testing.T) {
	for _, serial := range []bool{false, true} {
		key := &ColumnState{Type: "BIGINT", PK: !serial, Serial: serial}
		empty := &SchemaState{Models: map[string]*ModelState{"Row": {Table: "rows", Columns: map[string]*ColumnState{"name": {Type: "TEXT"}}}}}
		full := &SchemaState{Models: map[string]*ModelState{"Row": {Table: "rows", Columns: map[string]*ColumnState{"name": {Type: "TEXT"}, "id": key}}}}
		for _, states := range [][2]*SchemaState{{empty, full}, {full, empty}} {
			if ops, err := Diff(states[0], states[1], nil); err == nil || ops != nil {
				t.Fatal("key identity change emitted implicit SQL", ops, err)
			}
		}
	}
}
