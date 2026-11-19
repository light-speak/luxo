package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/codegen"
	pgdialect "github.com/light-speak/luxo/pkg/lux/dialect/pg"
	"github.com/light-speak/luxo/pkg/lux/pg"
	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
	"github.com/spf13/cobra"
)

func TestMigrationNameCannotEscapeOutputDirectory(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.WriteFile("victim.sql", []byte("keep existing SQL\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := writeMigrationFile(nil, nil, "../../../victim", pgdialect.Dialect{})
	if err == nil || path != "" {
		t.Errorf("migration name escaped output directory: path=%q error=%v", path, err)
	}
	if data, err := os.ReadFile("victim.sql"); err != nil || string(data) != "keep existing SQL\n" {
		t.Errorf("migration name overwrote another SQL file: content=%q error=%v", data, err)
	}
}

func TestMigrationOutputPreservesExistingSQL(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("migrations", 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("migrations", "collision.sql")
	if err := os.WriteFile(path, []byte("keep existing migration\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	_, err := writeMigrationOutput(nil, nil, pgdialect.Dialect{}, migrationOutput{fileName: "collision.sql", header: "-- replacement\n"})
	if !errors.Is(err, os.ErrExist) {
		t.Errorf("existing SQL was not rejected: %v", err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestMigrationSequenceSkipsExistingOrdinals(t *testing.T) {
	date := time.Now().Format("20060102")
	for _, sample := range []struct {
		name  string
		files []string
		want  string
	}{
		{"legacy_gap", []string{"001_first.sql", "009_latest.sql"}, "_010_next.sql"},
		{"dated_gap", []string{date + "_002_existing.sql"}, "_003_next.sql"},
		{"mixed", []string{"010_legacy.sql", date + "_008_existing.sql"}, "_011_next.sql"},
		{"unnumbered", []string{"manual.sql"}, "_002_next.sql"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.Mkdir("migrations", 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range sample.files {
				if err := os.WriteFile(filepath.Join("migrations", name), []byte("keep existing SQL\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path, err := writeMigrationFile(nil, nil, "next", pgdialect.Dialect{})
			if err != nil || !strings.HasSuffix(path, sample.want) {
				t.Fatalf("migration ordinal reused: path=%q want suffix=%q error=%v", path, sample.want, err)
			}
		})
	}
}

func TestMigrationDiffPreservesSQLAcrossSequenceGap(t *testing.T) {
	enterCacheGenerationScratch(t)
	t.Setenv("DATABASE_DRIVER", "pg")
	for _, path := range []string{"origin", "migrations"} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	existing := filepath.Join("migrations", time.Now().Format("20060102")+"_002_repeat.sql")
	for name, content := range map[string]string{
		"origin/main.luxo":       "model Probe @noTime { id: Int @id }",
		"migrations/.state.json": "{\"models\":{}}\n",
		existing:                 "keep existing migration\n",
	} {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	output, err := captureCLIResponse(t, func() error { return runMigrateDiff(nil, []string{"repeat"}) })
	if err != nil || !strings.Contains(output, "Migration generated") {
		t.Fatal("valid new migration failed", output, err)
	}
	if data, err := os.ReadFile(existing); err != nil || string(data) != "keep existing migration\n" {
		t.Errorf("sequence gap overwrote existing SQL: %q %v", data, err)
	}
	paths, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(paths) != 2 {
		t.Errorf("new migration replaced existing SQL instead of creating a second file: %v %v", paths, err)
	}
}

func TestMigrationSequenceRejectsUnreadableDirectory(t *testing.T) {
	for _, sample := range []struct {
		name string
		mode os.FileMode
	}{
		{"unreadable", 0000},
		{"unsearchable", 0444},
	} {
		t.Run(sample.name, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.Mkdir("migrations", 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("migrations/009_existing.sql", []byte("keep\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod("migrations", sample.mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod("migrations", 0700) })
			path, err := writeMigrationFile(nil, nil, "next", pgdialect.Dialect{})
			if err == nil || path != "" || !errors.Is(err, os.ErrPermission) {
				t.Errorf("inaccessible migration directory accepted: path=%q error=%v", path, err)
			}
		})
	}
}

func TestMigrationSequenceFilesystemBoundaries(t *testing.T) {
	for _, sample := range []struct {
		name  string
		files []string
		want  int
	}{
		{"empty", nil, 1},
		{"non_sql", []string{"999_ignore.txt", ".state.json"}, 1},
		{"date_without_ordinal", []string{"20261113_label.sql"}, 20261114},
		{"date_bad_ordinal", []string{"20261113_bad_label.sql"}, 20261114},
		{"empty_ordinal", []string{"20261113__label.sql", "_label.sql"}, 20261114},
		{"no_numeric_prefix", []string{"manual.sql", "text_001.sql"}, 3},
		{"zero", []string{"000_zero.sql"}, 2},
	} {
		t.Run(sample.name, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.MkdirAll("migrations/999_directory.sql", 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range sample.files {
				if err := os.WriteFile(filepath.Join("migrations", name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			seq, err := nextMigrationSeq()
			if err != nil || seq != sample.want {
				t.Fatalf("sequence=%d want=%d error=%v", seq, sample.want, err)
			}
		})
	}
	for _, name := range []string{strconv.Itoa(int(^uint(0)>>1)) + "_max.sql", "999999999999999999999999999999_overflow.sql"} {
		t.Run(name, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.Mkdir("migrations", 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join("migrations", name), []byte("keep\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before := addProjectSnapshot(t)
			path, err := writeMigrationFile(nil, nil, "next", pgdialect.Dialect{})
			if err == nil || path != "" {
				t.Errorf("exhausted sequence accepted: path=%q error=%v", path, err)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func TestMigrationOutputRejectsEscapesAndExistingTargets(t *testing.T) {
	for _, sample := range []string{"outside_directory", "outside_file", "dangling", "directory", "parent_path"} {
		t.Run(sample, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			for _, dir := range []string{"outside", "project/migrations"} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile("outside/sentinel.sql", []byte("keep\n"), 0600); err != nil {
				t.Fatal(err)
			}
			name := "next.sql"
			switch sample {
			case "outside_directory":
				if err := os.Remove("project/migrations"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../outside", "project/migrations"); err != nil {
					t.Fatal(err)
				}
			case "outside_file":
				if err := os.Symlink("../../outside/sentinel.sql", "project/migrations/next.sql"); err != nil {
					t.Fatal(err)
				}
			case "dangling":
				if err := os.Symlink("missing.sql", "project/migrations/next.sql"); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir("project/migrations/next.sql", 0700); err != nil {
					t.Fatal(err)
				}
			case "parent_path":
				name = "../../outside/sentinel.sql"
			}
			before := addProjectSnapshot(t)
			if err := os.Chdir("project"); err != nil {
				t.Fatal(err)
			}
			path, err := writeMigrationOutput(nil, nil, pgdialect.Dialect{}, migrationOutput{fileName: name})
			if err == nil || path != "" {
				t.Errorf("unsafe target accepted: path=%q error=%v", path, err)
			}
			if err := os.Chdir(".."); err != nil {
				t.Fatal(err)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func TestMigrationDiffWriteFailurePreservesState(t *testing.T) {
	enterCacheGenerationScratch(t)
	t.Setenv("DATABASE_DRIVER", "pg")
	if err := os.Mkdir("origin", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("migrations", 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"origin/main.luxo":       "model Probe @noTime { id: Int @id }",
		"migrations/.state.json": "{\"models\":{}}\n",
	} {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := addProjectSnapshot(t)
	if err := os.Chmod("migrations", 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod("migrations", 0700) })
	output, err := captureCLIResponse(t, func() error { return runMigrateDiff(nil, []string{"next"}) })
	if err == nil || strings.Contains(output, "Migration generated") {
		t.Errorf("write failure reported success: output=%q error=%v", output, err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestMigrationSquashPreservesExistingOutput(t *testing.T) {
	enterCacheGenerationScratch(t)
	t.Setenv("DATABASE_DRIVER", "pg")
	for _, dir := range []string{"origin", "migrations"} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("origin/main.luxo", []byte("model Probe @noTime { id: Int @id }"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := captureCLIResponse(t, func() error { return runMigrateSquash(nil, nil) })
	if err != nil || !strings.Contains(output, "Migrations squashed") {
		t.Fatalf("initial squash failed: %q %v", output, err)
	}
	before := addProjectSnapshot(t)
	output, err = captureCLIResponse(t, func() error { return runMigrateSquash(nil, nil) })
	if !errors.Is(err, os.ErrExist) || output != "" {
		t.Errorf("existing squash replaced: output=%q error=%v", output, err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestMigrationOutputRootAndFilenameErrors(t *testing.T) {
	for _, name := range []string{"", "../outside.sql", "nested/output.sql", "/absolute.sql"} {
		t.Run(name, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			before := addProjectSnapshot(t)
			path, err := writeMigrationOutput(nil, nil, pgdialect.Dialect{}, migrationOutput{fileName: name})
			if err == nil || path != "" {
				t.Errorf("invalid output name accepted: %q %v", path, err)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
	t.Run("root_permission", func(t *testing.T) {
		enterCacheGenerationScratch(t)
		if err := os.Chmod(".", 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(".", 0700) })
		path, err := writeMigrationOutput(nil, nil, pgdialect.Dialect{}, migrationOutput{fileName: "new.sql"})
		if !errors.Is(err, os.ErrPermission) || path != "" {
			t.Errorf("unreadable root accepted: %q %v", path, err)
		}
	})
	t.Run("directory_is_file", func(t *testing.T) {
		enterCacheGenerationScratch(t)
		if err := os.WriteFile("migrations", []byte("keep\n"), 0600); err != nil {
			t.Fatal(err)
		}
		before := addProjectSnapshot(t)
		path, err := writeMigrationFile(nil, nil, "new", pgdialect.Dialect{})
		if err == nil || path != "" {
			t.Errorf("file used as output directory: %q %v", path, err)
		}
		assertAddProjectSnapshot(t, before)
	})
}

func TestMigrationCLISetupFailuresPreserveProject(t *testing.T) {
	for _, command := range []struct {
		name   string
		run    func(*cobra.Command, []string) error
		schema bool
	}{
		{"up", runMigrateUp, false}, {"down", runMigrateDown, false},
		{"status", runMigrateStatus, false}, {"verify", runMigrateVerify, false},
		{"diff", runMigrateDiff, true}, {"squash", runMigrateSquash, true},
	} {
		modes := []string{"env_directory", "invalid_port"}
		if command.schema {
			modes = []string{"env_directory", "mysql", "unknown", "invalid_origin"}
		}
		for _, mode := range modes {
			t.Run(command.name+"/"+mode, func(t *testing.T) {
				cmd := prepareMigrationCLISetupFailure(t, mode)
				before := addProjectSnapshot(t)
				output, err := captureCLIResponse(t, func() error { return command.run(cmd, nil) })
				if err == nil || output != "" {
					t.Fatalf("failed setup reported success: output=%q error=%v", output, err)
				}
				switch mode {
				case "env_directory":
					if !strings.Contains(err.Error(), ".env") {
						t.Errorf("environment error lost path: %v", err)
					}
				case "invalid_port":
					if !strings.Contains(err.Error(), "port") || !strings.Contains(err.Error(), "connect:") {
						t.Errorf("configuration error lost: %v", err)
					}
				case "mysql", "unknown":
					if !strings.Contains(err.Error(), "DATABASE_DRIVER") {
						t.Errorf("driver error lost: %v", err)
					}
				}
				assertAddProjectSnapshot(t, before)
			})
		}
	}
}

func prepareMigrationCLISetupFailure(t *testing.T, mode string) *cobra.Command {
	t.Helper()
	enterCacheGenerationScratch(t)
	t.Setenv("DATABASE_DRIVER", "pg")
	for _, dir := range []string{"origin", "migrations"} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"origin/main.luxo":            "model Probe @noTime { id: Int @id }",
		"migrations/.state.json":      "{\"models\":{}}\n",
		"migrations/001_existing.sql": "keep existing SQL\n",
	} {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := newMigrationCLICommand(t)
	switch mode {
	case "env_directory":
		if err := os.Mkdir(".env", 0700); err != nil {
			t.Fatal(err)
		}
	case "invalid_port":
		t.Setenv("DATABASE_PORT", "invalid-port")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		command.SetContext(ctx)
	case "mysql", "unknown":
		t.Setenv("DATABASE_DRIVER", mode)
	case "invalid_origin":
		if err := os.WriteFile("origin/main.luxo", []byte("model {"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return command
}

func TestMigrationStateCommitPreservesPermissions(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("migrations", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("migrations/.state.json", []byte("{\"models\":{}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := writeMigrationWithState(&codegen.SchemaState{Models: map[string]*codegen.ModelState{}}, func() (string, error) {
		return writeMigrationFile(nil, nil, "next", pgdialect.Dialect{})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat("migrations/.state.json")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state permissions changed: %v %v", info, err)
	}
	if _, err := codegen.LoadState("migrations/.state.json"); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir("migrations")
	if err != nil || len(files) != 2 {
		t.Fatalf("staging files leaked: %v %v", files, err)
	}
}

func TestMigrationStatePreparationRejectsInvalidTargets(t *testing.T) {
	for _, mode := range []string{"state_directory", "state_symlink_cycle", "state_readonly", "directory_file", "root_unreadable", "root_readonly", "invalid_state"} {
		t.Run(mode, func(t *testing.T) {
			prepareMigrationStateTargetFailure(t, mode)
			desired := &codegen.SchemaState{Models: map[string]*codegen.ModelState{}}
			if mode == "invalid_state" {
				desired = nil
			}
			before := addProjectSnapshot(t)
			directory, directoryErr := os.Getwd()
			if directoryErr != nil {
				t.Fatal(directoryErr)
			}
			if mode == "root_unreadable" || mode == "root_readonly" {
				permission := os.FileMode(0000)
				if mode == "root_readonly" {
					permission = 0500
				}
				if err := os.Chmod(directory, permission); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(directory, 0700) })
			}
			called := false
			path, err := writeMigrationWithState(desired, func() (string, error) {
				called = true
				return writeMigrationFile(nil, nil, "next", pgdialect.Dialect{})
			})
			if err == nil || path != "" || called {
				t.Errorf("invalid state target reached SQL output: path=%q called=%v error=%v", path, called, err)
			}
			if mode == "root_unreadable" || mode == "root_readonly" {
				if err := os.Chmod(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func prepareMigrationStateTargetFailure(t *testing.T, mode string) {
	t.Helper()
	enterCacheGenerationScratch(t)
	if mode == "directory_file" {
		if err := os.WriteFile("migrations", []byte("keep\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode == "root_readonly" {
		return
	}
	if err := os.Mkdir("migrations", 0700); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "state_directory":
		if err := os.Mkdir("migrations/.state.json", 0700); err != nil {
			t.Fatal(err)
		}
	case "state_symlink_cycle":
		if err := os.Symlink(".state.json", "migrations/.state.json"); err != nil {
			t.Fatal(err)
		}
	case "state_readonly":
		if err := os.WriteFile("migrations/.state.json", []byte("keep\n"), 0400); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationStateSQLFailureRemovesOwnedStaging(t *testing.T) {
	enterCacheGenerationScratch(t)
	before := addProjectSnapshot(t)
	path, err := writeMigrationWithState(&codegen.SchemaState{Models: map[string]*codegen.ModelState{}}, func() (string, error) {
		return writeMigrationFile(nil, nil, "../invalid", pgdialect.Dialect{})
	})
	if err == nil || path != "" || !strings.Contains(err.Error(), "migration name") {
		t.Fatalf("SQL validation error lost: %q %v", path, err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestMigrationStatePreservesSymlinkTargets(t *testing.T) {
	for _, kind := range []string{"internal", "external", "absolute", "dangling", "chain", "writeonly_parent"} {
		t.Run(kind, func(t *testing.T) {
			target, link := prepareMigrationStateSymlink(t, kind)
			output, err := captureCLIResponse(t, func() error { return runMigrateSquash(nil, nil) })
			if kind == "writeonly_parent" {
				if err := os.Chmod(filepath.Dir(target), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err != nil || !strings.Contains(output, "Migrations squashed") {
				t.Fatalf("state symlink rejected: output=%q error=%v", output, err)
			}
			assertMigrationStateSymlink(t, target, link, kind)
		})
	}
}

func prepareMigrationStateSymlink(t *testing.T, kind string) (string, string) {
	t.Helper()
	enterCacheGenerationScratch(t)
	t.Setenv("DATABASE_DRIVER", "pg")
	for _, dir := range []string{"project/origin", "project/migrations", "project/cache", "shared"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("project/origin/main.luxo", []byte("model Probe @noTime { id: Int @id }"), 0600); err != nil {
		t.Fatal(err)
	}
	target, link := "shared/state.json", "../../shared/state.json"
	if kind == "internal" {
		target, link = "project/cache/state.json", "../cache/state.json"
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "absolute" {
		link = absolute
	}
	if kind != "dangling" {
		if err := os.WriteFile(target, []byte("{\"models\":{}}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "chain" {
		if err := os.Symlink("state.json", "shared/alias.json"); err != nil {
			t.Fatal(err)
		}
		link = "../../shared/alias.json"
	}
	if err := os.Symlink(link, "project/migrations/.state.json"); err != nil {
		t.Fatal(err)
	}
	if kind == "writeonly_parent" {
		directory := filepath.Dir(absolute)
		if err := os.Chmod(directory, 0333); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(directory, 0700) })
	}
	t.Chdir("project")
	return absolute, link
}

func assertMigrationStateSymlink(t *testing.T, target, link, kind string) {
	t.Helper()
	if got, err := os.Readlink("migrations/.state.json"); err != nil || got != link {
		t.Fatalf("state symlink replaced: %q %v", got, err)
	}
	state, err := codegen.LoadState(target)
	if err != nil || state.Models["Probe"] == nil {
		t.Fatalf("symlink target state not committed: %v %v", state, err)
	}
	if kind != "dangling" {
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("target permissions changed: %v %v", info, err)
		}
	}
	if kind == "chain" {
		if got, err := os.Readlink(filepath.Join(filepath.Dir(target), "alias.json")); err != nil || got != "state.json" {
			t.Fatalf("intermediate symlink replaced: %q %v", got, err)
		}
	}
	for _, dir := range []string{filepath.Dir(target), "migrations"} {
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".tmp") {
				t.Errorf("staging file leaked: %s", file.Name())
			}
		}
	}
}

func TestMigrationStateReadonlySymlinkParentPreservesProject(t *testing.T) {
	target, _ := prepareMigrationStateSymlink(t, "external")
	directory := filepath.Dir(target)
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	project := addProjectSnapshot(t)
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(directory, 0700) })
	output, err := captureCLIResponse(t, func() error { return runMigrateSquash(nil, nil) })
	if !errors.Is(err, os.ErrPermission) || output != "" {
		t.Fatalf("non-replaceable state target accepted: output=%q error=%v", output, err)
	}
	after, err := os.ReadFile(target)
	if err != nil || string(after) != string(before) {
		t.Fatalf("non-replaceable state target changed: before=%q after=%q error=%v", before, after, err)
	}
	assertAddProjectSnapshot(t, project)
}

func TestMigrationInvalidNameDoesNotPrepareState(t *testing.T) {
	target, _ := prepareMigrationStateSymlink(t, "external")
	directory := filepath.Dir(target)
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(directory, 0700) })
	before := addProjectSnapshot(t)
	output, err := captureCLIResponse(t, func() error { return runMigrateDiff(nil, []string{"../invalid"}) })
	if err == nil || !strings.Contains(err.Error(), "migration name") || output != "" {
		t.Fatalf("invalid name reached state preparation: output=%q error=%v", output, err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestMigrationSquashCollisionDoesNotPrepareState(t *testing.T) {
	target, _ := prepareMigrationStateSymlink(t, "external")
	path := filepath.Join("migrations", time.Now().Format("20060102")+"_001_initial.sql")
	if err := os.WriteFile(path, []byte("keep existing SQL\n"), 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(target)
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(directory, 0700) })
	before := addProjectSnapshot(t)
	output, err := captureCLIResponse(t, func() error { return runMigrateSquash(nil, nil) })
	if !errors.Is(err, os.ErrExist) || output != "" {
		t.Fatalf("existing SQL reached state preparation: output=%q error=%v", output, err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestMigrationStateRenameFailureRemovesSQL(t *testing.T) {
	enterCacheGenerationScratch(t)
	path, err := writeMigrationWithState(&codegen.SchemaState{Models: map[string]*codegen.ModelState{}}, func() (string, error) {
		path, err := writeMigrationFile(nil, nil, "next", pgdialect.Dialect{})
		if err != nil {
			return "", err
		}
		if err := os.Mkdir("migrations/.state.json", 0700); err != nil {
			return "", err
		}
		return path, nil
	})
	if err == nil || path != "" || !strings.Contains(err.Error(), "rename") {
		t.Fatalf("state commit error lost: %q %v", path, err)
	}
	after := addProjectSnapshot(t)
	if len(after) != 3 || after["migrations/.state.json"] != "directory" {
		t.Fatalf("failed commit left SQL/staging or deleted another actor's directory: %v", after)
	}
}

func TestMigrationCLIApplyAndReport(t *testing.T) {
	db := isolatedMigrationCountDatabase(t)
	command := newMigrationCLICommand(t)
	assertMigrationCLICommand(t, db, command, runMigrateStatus, nil, 0, "No migrations found")
	writeMigrationCountFixture(t)
	for _, sample := range []struct {
		name    string
		run     func(*cobra.Command, []string) error
		args    []string
		dry     bool
		applied int64
		want    []string
	}{
		{"pending", runMigrateStatus, nil, false, 0, []string{"001_base.sql", "002_marker.sql", "(pending)"}},
		{"dry_pending", runMigrateUp, nil, true, 0, []string{"DRY RUN", "CREATE TABLE cli_rollback_probe", "002_marker.sql"}},
		{"apply", runMigrateUp, nil, false, 2, []string{"2 migration(s) applied", "001_base.sql", "002_marker.sql"}},
		{"dry_applied", runMigrateUp, nil, true, 2, []string{"No pending migrations"}},
		{"apply_again", runMigrateUp, nil, false, 2, []string{"No pending migrations"}},
		{"applied_status", runMigrateStatus, nil, false, 2, []string{"001_base.sql", "002_marker.sql"}},
		{"rollback", runMigrateDown, []string{"2"}, false, 0, []string{"2 migration(s) rolled back", "001_base.sql", "002_marker.sql"}},
		{"rollback_empty", runMigrateDown, nil, false, 0, []string{"No migrations to rollback"}},
		{"pending_again", runMigrateStatus, nil, false, 0, []string{"001_base.sql", "002_marker.sql", "(pending)"}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			dryRun = sample.dry
			output := assertMigrationCLICommand(t, db, command, sample.run, sample.args, sample.applied, sample.want...)
			if sample.name == "applied_status" && strings.Contains(output, "(pending)") {
				t.Fatal("applied migrations were reported pending", output)
			}
			if sample.applied == 2 {
				payload, err := pg.QueryScalar[string](t.Context(), db, "SELECT payload FROM cli_rollback_probe WHERE id=1")
				if err != nil || payload != "keep" {
					t.Fatal("command changed applied data", payload, err)
				}
			}
		})
	}
}

func newMigrationCLICommand(t *testing.T) *cobra.Command {
	t.Helper()
	previous := dryRun
	dryRun = false
	t.Cleanup(func() { dryRun = previous })
	command := &cobra.Command{}
	command.SetContext(t.Context())
	return command
}

func assertMigrationCLICommand(t *testing.T, db *pg.DB, command *cobra.Command, run func(*cobra.Command, []string) error, args []string, applied int64, want ...string) string {
	t.Helper()
	output, err := captureCLIResponse(t, func() error { return run(command, args) })
	if err != nil {
		t.Fatal(err, output)
	}
	for _, text := range want {
		if !strings.Contains(output, text) {
			t.Fatalf("migration command omitted %q: %s", text, output)
		}
	}
	assertMigrationCLIHistory(t, db, applied)
	return output
}

func assertMigrationCLIHistory(t *testing.T, db *pg.DB, want int64) {
	t.Helper()
	count, err := pg.QueryScalar[int64](t.Context(), db, "SELECT count(*) FROM _migrations")
	if err != nil || count != want {
		t.Fatalf("migration history changed: got=%d want=%d error=%v", count, want, err)
	}
	if want == 0 {
		count, err = pg.QueryScalar[int64](t.Context(), db, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('cli_rollback_probe','cli_rollback_marker')")
		if err != nil || count != 0 {
			t.Fatal("empty migration history retained application tables", count, err)
		}
	}
}

func TestMigrationCLIVerifyTableDrift(t *testing.T) {
	db := isolatedMigrationCountDatabase(t)
	writeMigrationCountFixture(t)
	state := `{"models":{"Probe":{"table":"cli_rollback_probe"},"Marker":{"table":"cli_rollback_marker"}}}`
	if err := os.WriteFile(filepath.Join("migrations", ".state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	command := newMigrationCLICommand(t)
	assertMigrationCLICommand(t, db, command, runMigrateUp, nil, 2, "2 migration(s) applied")
	assertMigrationCLICommand(t, db, command, runMigrateVerify, nil, 2, "Database matches expected origin")
	if _, err := pg.Exec(t.Context(), db, "CREATE TABLE cli_unexpected(id bigint)"); err != nil {
		t.Fatal(err)
	}
	output, err := captureCLIResponse(t, func() error { return runMigrateVerify(command, nil) })
	if err == nil || !strings.Contains(err.Error(), "1 drift(s) found") || !strings.Contains(output, "unexpected table: cli_unexpected") {
		t.Fatal("table drift was reported healthy", output, err)
	}
	assertMigrationCLIHistory(t, db, 2)
	if _, err := pg.Exec(t.Context(), db, "DROP TABLE cli_unexpected"); err != nil {
		t.Fatal(err)
	}
	assertMigrationCLICommand(t, db, command, runMigrateVerify, nil, 2, "Database matches expected origin")
}

func TestMigrationCLISQLFailurePreservesCommittedSteps(t *testing.T) {
	db := isolatedMigrationCountDatabase(t)
	writeMigrationCountFixture(t)
	path := filepath.Join("migrations", "002_marker.sql")
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(valid), "CREATE TABLE cli_rollback_marker(id bigint PRIMARY KEY);", "CREATE TABLE cli_rollback_marker(id bigint PRIMARY KEY); SELECT * FROM cli_missing_table;", 1)
	if err := os.WriteFile(path, []byte(broken), 0600); err != nil {
		t.Fatal(err)
	}
	command := newMigrationCLICommand(t)
	output, err := captureCLIResponse(t, func() error { return runMigrateUp(command, nil) })
	if err == nil || !strings.Contains(err.Error(), "migrate up") || output != "" {
		t.Fatal("failed SQL reported success", output, err)
	}
	assertMigrationCLIHistory(t, db, 1)
	count, err := pg.QueryScalar[int64](t.Context(), db, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='cli_rollback_marker'")
	if err != nil || count != 0 {
		t.Fatal("failed migration retained an uncommitted table", count, err)
	}
	payload, err := pg.QueryScalar[string](t.Context(), db, "SELECT payload FROM cli_rollback_probe WHERE id=1")
	if err != nil || payload != "keep" {
		t.Fatal("failed step changed earlier committed data", payload, err)
	}
	if err := os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	assertMigrationCLICommand(t, db, command, runMigrateUp, nil, 2, "1 migration(s) applied", "002_marker.sql")
}

func TestMigrationNameRejectsInvalidComponentsBeforeOutput(t *testing.T) {
	for _, name := range []string{"", "nested/name", `nested\name`, "line\nbreak", "line\rbreak", "nul\x00name", string([]byte{0xff})} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			enterCacheGenerationScratch(t)
			path, err := writeMigrationFile(nil, nil, name, pgdialect.Dialect{})
			if err == nil || path != "" || !strings.Contains(err.Error(), "migration name") {
				t.Errorf("invalid migration component reached output: path=%q error=%v", path, err)
			}
			if _, err := os.Stat("migrations"); !os.IsNotExist(err) {
				t.Errorf("invalid migration name created an output directory: %v", err)
			}
		})
	}
}

func TestMigrationNamePreservesValidComponents(t *testing.T) {
	for _, name := range []string{"add_column", "添加字段", "schema version", "fix.v2", "trailing space ", ".hidden", "name..dots", ".", ".."} {
		t.Run(name, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			path, err := writeMigrationFile(nil, nil, name, pgdialect.Dialect{})
			if err != nil || filepath.Dir(path) != "migrations" || !strings.HasSuffix(path, "_"+name+".sql") {
				t.Fatalf("valid migration name changed: path=%q error=%v", path, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), "-- Migration: "+name+"\n") {
				t.Fatal("migration header changed", string(data), err)
			}
		})
	}
}

func TestMigrationDiffInvalidNamePreservesState(t *testing.T) {
	enterCacheGenerationScratch(t)
	t.Setenv("DATABASE_DRIVER", "pg")
	for _, directory := range []string{"origin", "migrations"} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"origin/main.luxo":       "model Probe @noTime { id: Int @id }",
		"migrations/.state.json": "{\"models\":{}}\n",
		"victim.sql":             "keep existing SQL\n",
	}
	for name, content := range files {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	output, err := captureCLIResponse(t, func() error { return runMigrateDiff(nil, []string{"../../../victim"}) })
	if err == nil || output != "" {
		t.Errorf("invalid migration name reported success: output=%q error=%v", output, err)
	}
	for name, want := range files {
		if data, err := os.ReadFile(name); err != nil || string(data) != want {
			t.Errorf("invalid migration name changed %s: %v", name, err)
		}
	}
}

func TestMigrationDownRejectsInvalidCountBeforeEnvironment(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir(".env", 0700); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"abc", "1junk", "1.5", "0", "-1", "", " 1", "1 ", "0x10", "9223372036854775808", "999999999999999999999999"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			output, err := captureCLIResponse(t, func() error { return runMigrateDown(nil, []string{value}) })
			if err == nil || !strings.Contains(err.Error(), "rollback count") || output != "" {
				t.Fatalf("invalid count reached configuration or reported success: output=%q error=%v", output, err)
			}
		})
	}
}

func TestMigrationDownInvalidCountPreservesAppliedSchema(t *testing.T) {
	db := isolatedMigrationCountDatabase(t)
	writeMigrationCountFixture(t)
	runner, err := migrate.New(t.Context(), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.Close)
	for _, value := range []string{"abc", "1junk", "1.5", "0", "-1"} {
		t.Run(value, func(t *testing.T) {
			if _, err := runner.Up(t.Context()); err != nil {
				t.Fatal(err)
			}
			command := &cobra.Command{}
			command.SetContext(t.Context())
			output, err := captureCLIResponse(t, func() error { return runMigrateDown(command, []string{value}) })
			if err == nil || !strings.Contains(err.Error(), "rollback count") || output != "" {
				t.Errorf("invalid count executed rollback: output=%q error=%v", output, err)
			}
			valueAfter, err := pg.QueryScalar[string](t.Context(), db, "SELECT payload FROM cli_rollback_probe WHERE id=1")
			if err != nil || valueAfter != "keep" {
				t.Errorf("invalid count changed stored data: value=%q error=%v", valueAfter, err)
			}
			applied, err := pg.QueryScalar[int64](t.Context(), db, "SELECT count(*) FROM _migrations")
			if err != nil || applied != 2 {
				t.Errorf("invalid count changed migration history: count=%d error=%v", applied, err)
			}
		})
	}
}

func TestMigrationDownValidCountUsesRequestedLimit(t *testing.T) {
	db := isolatedMigrationCountDatabase(t)
	writeMigrationCountFixture(t)
	runner, err := migrate.New(t.Context(), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.Close)
	for _, sample := range []struct {
		name string
		args []string
		left int64
	}{
		{"default", nil, 1}, {"one", []string{"1"}, 1}, {"positive-sign", []string{"+1"}, 1},
		{"leading-zero", []string{"01"}, 1}, {"two", []string{"2"}, 0}, {"more-than-applied", []string{"3"}, 0},
	} {
		t.Run(sample.name, func(t *testing.T) {
			if _, err := runner.Up(t.Context()); err != nil {
				t.Fatal(err)
			}
			command := &cobra.Command{}
			command.SetContext(t.Context())
			if _, err := captureCLIResponse(t, func() error { return runMigrateDown(command, sample.args) }); err != nil {
				t.Fatal(err)
			}
			applied, err := pg.QueryScalar[int64](t.Context(), db, "SELECT count(*) FROM _migrations")
			if err != nil || applied != sample.left {
				t.Fatalf("wrong rollback count: remaining=%d want=%d error=%v", applied, sample.left, err)
			}
			if sample.left == 1 {
				value, err := pg.QueryScalar[string](t.Context(), db, "SELECT payload FROM cli_rollback_probe WHERE id=1")
				if err != nil || value != "keep" {
					t.Fatal("earlier migration was rolled back", value, err)
				}
			}
		})
	}
}

func writeMigrationCountFixture(t *testing.T) {
	t.Helper()
	if err := os.Mkdir("migrations", 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"001_base.sql":   "-- ====== UP ======\nCREATE TABLE cli_rollback_probe(id bigint PRIMARY KEY,payload text NOT NULL); INSERT INTO cli_rollback_probe VALUES(1,'keep');\n-- ====== DOWN ======\nDROP TABLE cli_rollback_probe;\n",
		"002_marker.sql": "-- ====== UP ======\nCREATE TABLE cli_rollback_marker(id bigint PRIMARY KEY);\n-- ====== DOWN ======\nDROP TABLE cli_rollback_marker;\n",
	} {
		if err := os.WriteFile(filepath.Join("migrations", name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func isolatedMigrationCountDatabase(t *testing.T) *pg.DB {
	t.Helper()
	connection := os.Getenv("LUXO_TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("requires isolated LUXO_TEST_DATABASE_URL")
	}
	enterCacheGenerationScratch(t)
	u, err := url.Parse(connection)
	if err != nil || u.User == nil {
		t.Fatal("invalid test database URL")
	}
	admin, err := pg.NewDB(t.Context(), connection)
	if err != nil {
		t.Fatal(err)
	}
	name := "luxo_cli_count_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := pg.Exec(t.Context(), admin, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pg.Exec(ctx, admin, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	password, _ := u.User.Password()
	for key, value := range map[string]string{"DATABASE_HOST": u.Hostname(), "DATABASE_PORT": u.Port(), "DATABASE_USER": u.User.Username(), "DATABASE_PASSWORD": password, "DATABASE_PREFIX": name, "DATABASE_SSL": u.Query().Get("sslmode")} {
		t.Setenv(key, value)
	}
	db, err := pg.NewDB(t.Context(), migrate.BuildDatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestMigrationSQLErrorsDoNotCreateOutput(t *testing.T) {
	enterCacheGenerationScratch(t)
	ops := []codegen.DiffOp{{Kind: codegen.DropTable, Table: strings.Repeat("a", 64)}}
	path, err := writeMigrationFile(ops, nil, "invalid", pgdialect.Dialect{})
	if err == nil || path != "" || !strings.Contains(err.Error(), "generate migration SQL") {
		t.Fatal("SQL validation did not reach the CLI caller", path, err)
	}
	if _, err := os.Stat("migrations"); !os.IsNotExist(err) {
		t.Fatal("rejected SQL created a migration directory", err)
	}
}

func TestMigrationDiffFailureDoesNotWriteInitialOutput(t *testing.T) {
	enterCacheGenerationScratch(t)
	path, err := writeInitialMigration(nil, pgdialect.Dialect{}, migrationOutput{fileName: "initial.sql"})
	if err == nil || path != "" || !strings.Contains(err.Error(), "diff migration state") {
		t.Fatal("invalid state did not propagate through squash writer", path, err)
	}
	if _, err := os.Stat("migrations"); !os.IsNotExist(err) {
		t.Fatal("invalid initial diff created output", err)
	}
}

func TestMigrationSQLRejectsOldStateWithoutChangingFiles(t *testing.T) {
	for _, sample := range []struct{ name, state, prefix string }{
		{"sql", `{"models":{"Old":{"table":"` + strings.Repeat("a", 64) + `","columns":{}}}}`, "generate migration SQL"},
		{"diff", `{"models":{"Probe":{"table":"legacy","columns":{"id":{"type":"BIGINT","pk":true}}}}}`, "diff migration state"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			t.Setenv("DATABASE_DRIVER", "pg")
			for _, directory := range []string{"origin", "migrations"} {
				if err := os.Mkdir(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				"origin/main.luxo":        "model Probe @noTime { id: Int @id }",
				"migrations/.state.json":  sample.state,
				"migrations/existing.sql": "-- keep existing migration\n",
			}
			for name, content := range files {
				if err := os.WriteFile(name, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := runMigrateDiff(nil, nil); err == nil || !strings.Contains(err.Error(), sample.prefix) {
				t.Fatal("old state bypassed migration SQL validation", err)
			}
			for name, want := range files {
				if got, err := os.ReadFile(name); err != nil || string(got) != want {
					t.Fatal("invalid old state changed an existing file", name, string(got), err)
				}
			}
			if entries, err := os.ReadDir("migrations"); err != nil || len(entries) != 2 {
				t.Fatal("invalid old state created output", entries, err)
			}
		})
	}
}

func TestMigrationDefaultErrorsPreserveExistingFiles(t *testing.T) {
	for _, operation := range []string{"diff", "squash"} {
		t.Run(operation, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			t.Setenv("DATABASE_DRIVER", "pg")
			for _, directory := range []string{"origin", "migrations"} {
				if err := os.Mkdir(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				"origin/main.luxo":        "model Probe @noTime { value: String = \"" + string([]byte{0xff}) + "\" }",
				"migrations/.state.json":  "{\"models\":{}}\n",
				"migrations/existing.sql": "-- existing migration\n",
			}
			for name, content := range files {
				if err := os.WriteFile(name, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if operation == "diff" {
				err = runMigrateDiff(nil, nil)
			} else {
				err = runMigrateSquash(nil, nil)
			}
			if err == nil || !strings.Contains(err.Error(), "compute migration state") || !strings.Contains(err.Error(), "Probe.value") {
				t.Fatal("migration default error was not propagated", err)
			}
			for name, want := range files {
				if got, err := os.ReadFile(name); err != nil || string(got) != want {
					t.Fatal("rejected migration changed an existing file", name, string(got), err)
				}
			}
			entries, err := os.ReadDir("migrations")
			if err != nil || len(entries) != 2 {
				t.Fatal("rejected migration created output", entries, err)
			}
		})
	}
}

func TestMigrationCommandsPropagateMetadataSchemaErrors(t *testing.T) {
	for _, sample := range []struct {
		name, prefix string
		run          func(*cobra.Command, []string) error
		dry          bool
	}{
		{"dry-run", "dry run:", runMigrateUp, true},
		{"down", "migrate down:", runMigrateDown, false},
		{"status", "", runMigrateStatus, false},
		{"verify", "verify:", runMigrateVerify, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := isolatedMigrationCountDatabase(t)
			if _, err := pg.Exec(t.Context(), db, "CREATE TABLE _migrations (unexpected bigint); INSERT INTO _migrations VALUES (7)"); err != nil {
				t.Fatal(err)
			}
			before := dryRun
			t.Cleanup(func() { dryRun = before })
			dryRun = sample.dry
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			output, err := captureCLIResponse(t, func() error { return sample.run(cmd, nil) })
			var state interface{ SQLState() string }
			if !errors.As(err, &state) || state.SQLState() != "42703" || !strings.HasPrefix(err.Error(), sample.prefix) || output != "" {
				t.Fatalf("migration metadata failure was not preserved: error=%v output=%q", err, output)
			}
			value, err := pg.QueryScalar[int64](t.Context(), db, "SELECT unexpected FROM _migrations")
			if err != nil || value != 7 {
				t.Fatal("failed migration command modified incompatible metadata", value, err)
			}
		})
	}
}
