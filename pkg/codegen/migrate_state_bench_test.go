package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// These references preserve the pre-validation migration-only implementation.
func loadStateUncheckedReference(path string) (*SchemaState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SchemaState{Models: make(map[string]*ModelState)}, nil
		}
		return nil, err
	}
	var state SchemaState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Models == nil {
		state.Models = make(map[string]*ModelState)
	}
	return &state, nil
}

func saveStateUncheckedReference(path string, state *SchemaState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func migrationBenchmarkState(models, fields int) *SchemaState {
	state := &SchemaState{Models: make(map[string]*ModelState, models)}
	for i := range models {
		model := &ModelState{Table: "table_" + strconv.Itoa(i), Columns: make(map[string]*ColumnState, fields)}
		for j := range fields {
			model.Columns["field_"+strconv.Itoa(j)] = &ColumnState{Type: "bigint", Nullable: j%2 == 0}
		}
		state.Models["Model"+strconv.Itoa(i)] = model
	}
	return state
}

func migrationBenchmarkPath(b *testing.B) string {
	b.Helper()
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		b.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "migration-bench-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if filepath.Dir(dir) != root {
			b.Fatal("refusing cleanup outside the owned benchmark root")
		}
		if err := os.RemoveAll(dir); err != nil {
			b.Error(err)
		}
	})
	return filepath.Join(dir, "state.json")
}

func BenchmarkMigrationStateValidation(b *testing.B) {
	for _, sample := range []struct {
		name           string
		models, fields int
	}{{"empty", 0, 0}, {"small", 1, 4}, {"large", 128, 16}} {
		b.Run(sample.name, func(b *testing.B) {
			state := migrationBenchmarkState(sample.models, sample.fields)
			path := migrationBenchmarkPath(b)
			if err := SaveState(path, state); err != nil {
				b.Fatal(err)
			}
			b.Run("validate", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := validateSchemaState(state); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("load/reference", func(b *testing.B) { benchmarkMigrationLoad(b, path, loadStateUncheckedReference, sample.models) })
			b.Run("load/strict", func(b *testing.B) { benchmarkMigrationLoad(b, path, LoadState, sample.models) })
			b.Run("save/reference", func(b *testing.B) { benchmarkMigrationSave(b, path, state, saveStateUncheckedReference) })
			b.Run("save/strict", func(b *testing.B) { benchmarkMigrationSave(b, path, state, SaveState) })
		})
	}
}

func benchmarkMigrationLoad(b *testing.B, path string, load func(string) (*SchemaState, error), models int) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		state, err := load(path)
		if err != nil || len(state.Models) != models {
			b.Fatalf("load state: %v", err)
		}
	}
}

func benchmarkMigrationSave(b *testing.B, path string, state *SchemaState, save func(string, *SchemaState) error) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		if err := save(path, state); err != nil {
			b.Fatal(err)
		}
	}
}
