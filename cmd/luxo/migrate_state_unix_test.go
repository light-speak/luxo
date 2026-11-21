//go:build unix

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/spf13/cobra"
)

// The size limit applies only to the already compiled child test process.
func TestMigrationStateWriteFailurePreservesProject(t *testing.T) {
	if os.Getenv("LUXO_TEST_MIGRATION_STATE_CHILD") == "1" {
		runMigrationStateFailureChild(t)
		return
	}
	for _, kind := range []string{"diff", "squash"} {
		for _, phase := range []string{"state", "sql"} {
			for _, layout := range []string{"regular", "linked"} {
				t.Run(kind+"/"+phase+"/"+layout, func(t *testing.T) {
					fields := 24
					if phase == "sql" {
						fields = 0
					}
					prepareMigrationStateFailureProject(t, fields, layout == "linked")
					before := addProjectSnapshot(t)
					cap := migrationStateFailureLimit(t, kind, phase)
					command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMigrationStateWriteFailurePreservesProject$", "-test.v")
					if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
						command.Args = append(command.Args, "-test.gocoverdir="+directory.Value.String())
					}
					command.Env = append(os.Environ(), "LUXO_TEST_MIGRATION_STATE_CHILD=1", "LUXO_TEST_MIGRATION_STATE_KIND="+kind, "LUXO_TEST_MIGRATION_STATE_LIMIT="+strconv.FormatInt(cap, 10))
					output, err := command.CombinedOutput()
					if err != nil || !bytes.Contains(output, []byte("observed file-size error")) {
						t.Fatalf("child did not exercise a state write error: %v\n%s", err, output)
					}
					assertAddProjectSnapshot(t, before)
				})
			}
		}
	}
}

func prepareMigrationStateFailureProject(t *testing.T, fields int, linked bool) {
	t.Helper()
	enterCacheGenerationScratch(t)
	t.Setenv("DATABASE_DRIVER", "pg")
	for _, dir := range []string{"origin", "migrations"} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var source strings.Builder
	source.WriteString("model Probe @noTime { id: Int @id\n")
	for i := range fields {
		fmt.Fprintf(&source, "value%02d: String\n", i)
	}
	source.WriteString("}\n")
	if err := os.WriteFile("origin/main.luxo", []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("migrations/.state.json", []byte("{\"models\":{}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if linked {
		if err := os.Mkdir("cache", 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename("migrations/.state.json", "cache/state.json"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../cache/state.json", "migrations/.state.json"); err != nil {
			t.Fatal(err)
		}
	}
}

func migrationStateFailureLimit(t *testing.T, kind, phase string) int64 {
	t.Helper()
	before, err := os.ReadFile("migrations/.state.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = captureCLIResponse(t, func() error { return invokeMigrationStateCommand(kind) })
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(paths) != 1 {
		t.Fatalf("healthy control SQL paths: %v %v", paths, err)
	}
	sql, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.Stat("migrations/.state.json")
	if err != nil {
		t.Fatal(err)
	}
	first, last := sql.Size(), state.Size()
	if phase == "sql" {
		first, last = state.Size(), sql.Size()
	}
	if last <= first+1 {
		t.Fatalf("no size threshold for %s: sql=%v state=%v", phase, sql, state)
	}
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("migrations/.state.json", before, 0600); err != nil {
		t.Fatal(err)
	}
	return first + 1
}

func runMigrationStateFailureChild(t *testing.T) {
	t.Helper()
	cap, err := strconv.ParseUint(os.Getenv("LUXO_TEST_MIGRATION_STATE_LIMIT"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	original := limit
	defer func() {
		// Coverage reports are written after this helper; the CLI ran under the limit.
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Error(err)
		}
	}()
	limit.Cur = cap
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	err = invokeMigrationStateCommand(os.Getenv("LUXO_TEST_MIGRATION_STATE_KIND"))
	if !errors.Is(err, syscall.EFBIG) {
		t.Fatalf("expected physical file-size error, got %v", err)
	}
	t.Log("observed file-size error:", err)
}

func invokeMigrationStateCommand(kind string) error {
	command := &cobra.Command{}
	command.SetContext(context.Background())
	if kind == "diff" {
		return runMigrateDiff(command, []string{"repeat"})
	}
	return runMigrateSquash(command, nil)
}

func TestMigrationSquashCollisionPrecedesReadonlyMigrationDirectory(t *testing.T) {
	prepareConcurrentMigrationProject(t)
	if _, err := captureCLIResponse(t, func() error { return runMigrateSquash(nil, nil) }); err != nil {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	dir := filepath.Join(mustWorkingDirectory(t), "migrations")
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	output, err := captureCLIResponse(t, func() error { return runMigrateSquash(nil, nil) })
	if !errors.Is(err, os.ErrExist) || output != "" {
		t.Fatalf("collision diagnostic was hidden by lock creation: %v %q", err, output)
	}
	assertAddProjectSnapshot(t, before)
}

func TestMigrationGenerationLockDirectoryRetryFailure(t *testing.T) {
	enterCacheGenerationScratch(t)
	dir := mustWorkingDirectory(t)
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	created := false
	if _, err := lockMigrationGeneration(t.Context(), root, &created); !errors.Is(err, os.ErrPermission) || created {
		t.Fatalf("directory retry hid permission error or took ownership: %v %v", err, created)
	}
}

func TestMigrationGenerationLockRejectsDirectoryPermissions(t *testing.T) {
	for _, mode := range []os.FileMode{0000, 0500} {
		t.Run(fmt.Sprintf("root_%04o", mode), func(t *testing.T) {
			enterCacheGenerationScratch(t)
			dir := mustWorkingDirectory(t)
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(dir, 0700)
			err := withMigrationGenerationLock(t.Context(), func() error {
				t.Error("unwritable project admitted generation")
				return nil
			})
			if !errors.Is(err, os.ErrPermission) {
				t.Fatalf("lost filesystem permission error: %v", err)
			}
		})
	}
}
