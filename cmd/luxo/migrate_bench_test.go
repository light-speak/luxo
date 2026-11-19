package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pgdialect "github.com/light-speak/luxo/pkg/lux/dialect/pg"
)

var migrationCountErrorText string

var migrationNameErrorText string

type migrationCommandBenchmarkCase struct {
	name, command                                                    string
	large, linked                                                    bool
	readonlyState, readonlyParent, collision, unchanged, invalidName bool
}

func BenchmarkCLIMigrationTransaction(b *testing.B) {
	for _, sample := range []migrationCommandBenchmarkCase{
		{name: "diff_small", command: "diff"}, {name: "squash_small", command: "squash"},
		{name: "diff_large", command: "diff", large: true}, {name: "squash_large", command: "squash", large: true},
		{name: "diff_linked", command: "diff", linked: true}, {name: "squash_linked", command: "squash", linked: true},
		{name: "readonly_state", command: "diff", readonlyState: true},
		{name: "readonly_parent", command: "squash", linked: true, readonlyParent: true},
		{name: "squash_collision", command: "squash", collision: true},
		{name: "diff_unchanged", command: "diff", unchanged: true},
		{name: "invalid_name", command: "diff", invalidName: true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			quietModuleGeneration(b)
			b.Setenv("DATABASE_DRIVER", "pg")
			statePath := setupMigrationCommandBenchmark(b, sample)
			baseline := os.Getenv("LUXO_MIGRATION_STATE_BASELINE") == "1"
			wantError := sample.readonlyState || sample.collision || sample.invalidName || sample.readonlyParent && !baseline
			b.ReportAllocs()
			for b.Loop() {
				err := runMigrationCommandBenchmark(sample)
				if (err != nil) != wantError {
					b.Fatal("unexpected migration result", sample.name, err)
				}
				migrationNameErrorText = ""
				if err != nil {
					migrationNameErrorText = err.Error()
				}
				cleanupMigrationCommandBenchmark(b, sample, statePath, baseline)
			}
		})
	}
}

func setupMigrationCommandBenchmark(b *testing.B, sample migrationCommandBenchmarkCase) string {
	b.Helper()
	for _, dir := range []string{"origin", "migrations"} {
		if err := os.Mkdir(dir, 0700); err != nil {
			b.Fatal(err)
		}
	}
	models, fields := 1, 0
	if sample.large {
		models, fields = 32, 16
	}
	var source strings.Builder
	for i := range models {
		fmt.Fprintf(&source, "model Probe%03d @noTime { id: Int @id\n", i)
		for j := range fields {
			fmt.Fprintf(&source, "value%02d: String\n", j)
		}
		source.WriteString("}\n")
	}
	if err := os.WriteFile("origin/main.luxo", []byte(source.String()), 0600); err != nil {
		b.Fatal(err)
	}
	statePath := "migrations/.state.json"
	if sample.linked {
		if err := os.Mkdir("cache", 0700); err != nil {
			b.Fatal(err)
		}
		statePath = "cache/state.json"
		if err := os.Symlink("../cache/state.json", "migrations/.state.json"); err != nil {
			b.Fatal(err)
		}
	}
	if err := os.WriteFile(statePath, []byte("{\"models\":{}}\n"), 0600); err != nil {
		b.Fatal(err)
	}
	if sample.unchanged || sample.collision {
		prepareMigrationCommandOutput(b, sample)
	}
	absolute, err := filepath.Abs(statePath)
	if err != nil {
		b.Fatal(err)
	}
	if sample.readonlyState {
		if err := os.Chmod(absolute, 0400); err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { os.Chmod(absolute, 0600) })
	}
	if sample.readonlyParent {
		directory := filepath.Dir(absolute)
		if err := os.Chmod(directory, 0500); err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { os.Chmod(directory, 0700) })
	}
	return statePath
}

func prepareMigrationCommandOutput(b *testing.B, sample migrationCommandBenchmarkCase) {
	b.Helper()
	if err := runMigrationCommandBenchmark(sample); err != nil {
		b.Fatal(err)
	}
	paths, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(paths) != 1 {
		b.Fatal("healthy migration control", paths, err)
	}
	if sample.unchanged {
		if err := os.Remove(paths[0]); err != nil {
			b.Fatal(err)
		}
	}
}

func runMigrationCommandBenchmark(sample migrationCommandBenchmarkCase) error {
	if sample.command == "squash" {
		return runMigrateSquash(nil, nil)
	}
	name := "repeat"
	if sample.invalidName {
		name = "../invalid"
	}
	return runMigrateDiff(nil, []string{name})
}

func cleanupMigrationCommandBenchmark(b *testing.B, sample migrationCommandBenchmarkCase, statePath string, baseline bool) {
	b.Helper()
	paths, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		b.Fatal(err)
	}
	wantSQL := 1
	if sample.unchanged || sample.invalidName || (sample.readonlyState || sample.readonlyParent) && !baseline {
		wantSQL = 0
	}
	if len(paths) != wantSQL {
		b.Fatal("unexpected migration outputs", sample.name, paths)
	}
	if !sample.collision {
		for _, path := range paths {
			if err := os.Remove(path); err != nil {
				b.Fatal(err)
			}
		}
	}
	if !sample.unchanged && !sample.collision && !sample.invalidName && !sample.readonlyState {
		if err := os.WriteFile(statePath, []byte("{\"models\":{}}\n"), 0600); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCLIMigrationOutput(b *testing.B) {
	for _, sample := range []struct {
		name  string
		count int
		last  int
	}{
		{"empty", 0, 0}, {"dense16", 16, 16}, {"dense1000", 1000, 1000},
		{"sparse999", 1, 999}, {"collision", 0, -1}, {"directory_file", 0, -2},
	} {
		b.Run(sample.name, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			if sample.last == -2 {
				if err := os.WriteFile("migrations", []byte("keep\n"), 0600); err != nil {
					b.Fatal(err)
				}
			} else {
				if err := os.Mkdir("migrations", 0700); err != nil {
					b.Fatal(err)
				}
			}
			for i := 1; i <= sample.count; i++ {
				ordinal := i
				if i == sample.count {
					ordinal = sample.last
				}
				if err := os.WriteFile(filepath.Join("migrations", fmt.Sprintf("20261113_%03d_existing.sql", ordinal)), []byte("keep\n"), 0600); err != nil {
					b.Fatal(err)
				}
			}
			if sample.last == -1 {
				if err := os.WriteFile("migrations/collision.sql", []byte("keep\n"), 0600); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				var path string
				var err error
				if sample.last == -1 {
					path, err = writeMigrationOutput(nil, nil, pgdialect.Dialect{}, migrationOutput{fileName: "collision.sql"})
				} else {
					path, err = writeMigrationFile(nil, nil, "next", pgdialect.Dialect{})
				}
				migrationNameErrorText = ""
				if err != nil {
					migrationNameErrorText = err.Error()
					continue
				}
				if sample.last != -1 {
					if err := os.Remove(path); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// Include file generation/removal on valid paths and Error() on invalid paths.
func BenchmarkCLIMigrationNameWrite(b *testing.B) {
	for _, sample := range []struct{ name, value string }{
		{"ascii", "add_column"}, {"unicode", "添加字段"},
		{"escape", "../../../escaped"}, {"newline", "line\nbreak"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			if err := os.Mkdir("migrations", 0700); err != nil {
				b.Fatal(err)
			}
			directory, err := os.Getwd()
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				path, err := writeMigrationFile(nil, nil, sample.value, pgdialect.Dialect{})
				migrationNameErrorText = ""
				if err != nil {
					migrationNameErrorText = err.Error()
					continue
				}
				absolute, err := filepath.Abs(path)
				if err != nil || filepath.Dir(absolute) != directory && filepath.Dir(absolute) != filepath.Join(directory, "migrations") {
					b.Fatal("benchmark output escaped its owned scratch directory", path, err)
				}
				if err := os.Remove(absolute); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Measure the command's validation/configuration error boundary, including Error().
// The unreadable .env prevents database I/O for both source revisions.
func BenchmarkCLIMigrationDownValidation(b *testing.B) {
	for _, sample := range []struct {
		name string
		args []string
	}{
		{"default", nil}, {"one", []string{"1"}}, {"many", []string{"100"}},
		{"invalid-text", []string{"abc"}}, {"invalid-zero", []string{"0"}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			if err := os.Mkdir(".env", 0700); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				err := runMigrateDown(nil, sample.args)
				if err == nil {
					b.Fatal("configuration fixture reached the database")
				}
				migrationCountErrorText = err.Error()
			}
		})
	}
}
