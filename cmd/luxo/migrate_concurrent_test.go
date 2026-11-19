package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/spf13/cobra"
)

func TestMigrationConcurrentCommandsDoNotDuplicateSchema(t *testing.T) {
	if name := os.Getenv("LUXO_TEST_MIGRATION_CONCURRENT_CHILD"); name != "" {
		if err := runMigrateDiff(nil, []string{name}); err != nil {
			t.Fatal(err)
		}
		return
	}
	for attempt := range 4 {
		t.Run(fmt.Sprint(attempt), func(t *testing.T) {
			prepareConcurrentMigrationProject(t)
			outputs := runConcurrentMigrationCommands(t)
			files, err := filepath.Glob("migrations/*.sql")
			if err != nil || len(files) != 1 {
				t.Fatalf("concurrent commands duplicated a single schema change: %v %v\n%s\n%s", files, err, outputs[0].String(), outputs[1].String())
			}
			combined := outputs[0].String() + outputs[1].String()
			if strings.Count(combined, "Migration generated") != 1 || strings.Count(combined, "No changes detected") != 1 {
				t.Fatalf("incorrect concurrent command results: %s", combined)
			}
			state, err := codegen.LoadState("migrations/.state.json")
			if err != nil || len(state.Models) != 64 {
				t.Fatalf("concurrent commands lost state: %v %v", state, err)
			}
			entries, err := os.ReadDir("migrations")
			if err != nil || len(entries) != 2 {
				t.Fatalf("concurrent generation left temporary files: %v %v", entries, err)
			}
		})
	}
}

func runConcurrentMigrationCommands(t *testing.T) []*bytes.Buffer {
	t.Helper()
	var outputs []*bytes.Buffer
	var commands []*exec.Cmd
	for _, name := range []string{"left", "right"} {
		output := new(bytes.Buffer)
		command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMigrationConcurrentCommandsDoNotDuplicateSchema$")
		if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
			command.Args = append(command.Args, "-test.gocoverdir="+directory.Value.String())
		}
		command.Env = append(os.Environ(), "LUXO_TEST_MIGRATION_CONCURRENT_CHILD="+name)
		command.Stdout, command.Stderr = output, output
		if err := command.Start(); err != nil {
			t.Error(err)
			break
		}
		commands, outputs = append(commands, command), append(outputs, output)
	}
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Errorf("concurrent migration failed: %v\n%s", err, outputs[index].String())
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	return outputs
}

func prepareConcurrentMigrationProject(t *testing.T) {
	t.Helper()
	enterCacheGenerationScratch(t)
	t.Setenv("DATABASE_DRIVER", "pg")
	for _, dir := range []string{"origin", "migrations"} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var source strings.Builder
	for model := range 64 {
		fmt.Fprintf(&source, "model Probe%03d @noTime { id: Int @id\n", model)
		for field := range 16 {
			fmt.Fprintf(&source, "value%02d: String\n", field)
		}
		source.WriteString("}\n")
	}
	if err := os.WriteFile("origin/main.luxo", []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("migrations/.state.json", []byte("{\"models\":{}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationGenerationLockSerializesAndCleans(t *testing.T) {
	enterCacheGenerationScratch(t)
	var group sync.WaitGroup
	var count, active atomic.Int32
	for range 12 {
		group.Go(func() {
			if err := withMigrationGenerationLock(t.Context(), func() error {
				if current := active.Add(1); current != 1 {
					t.Errorf("overlapping migration critical sections: %d", current)
				}
				defer active.Add(-1)
				runtime.Gosched()
				count.Add(1)
				return nil
			}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if count.Load() != 12 {
		t.Fatalf("lost a serialized operation: %d", count.Load())
	}
	entries, err := os.ReadDir("migrations")
	if err != nil || len(entries) != 0 {
		t.Fatalf("lock artifacts remain: %v %v", entries, err)
	}
}

func TestMigrationGenerationLockCancellationAndObsoleteInode(t *testing.T) {
	enterCacheGenerationScratch(t)
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var obsolete *os.File
	err = withMigrationGenerationLock(t.Context(), func() error {
		var err error
		obsolete, err = root.OpenFile(migrationGenerationLockPath, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		locked, err := tryMigrationGenerationLock(obsolete)
		if err != nil || locked {
			t.Fatalf("independent descriptor bypassed owner: %v %v", locked, err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		err = withMigrationGenerationLock(ctx, func() error {
			t.Error("cancelled waiter entered the critical section")
			return nil
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting did not preserve cancellation: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer obsolete.Close()
	if err := waitMigrationGenerationLock(t.Context(), obsolete); err != nil {
		t.Fatal(err)
	}
	for _, recreated := range []bool{false, true} {
		check := func() error {
			current, err := migrationGenerationLockCurrent(root, obsolete)
			if err != nil || current {
				t.Errorf("obsolete descriptor accepted (recreated=%v): %v %v", recreated, current, err)
			}
			return nil
		}
		if recreated {
			err = withMigrationGenerationLock(t.Context(), check)
		} else {
			err = check()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationGenerationLockFailuresPreserveFiles(t *testing.T) {
	for _, kind := range []string{"callback", "cancelled", "directory", "contents", "symlink", "dangling_symlink", "outside_symlink"} {
		t.Run(kind, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			} else if kind != "callback" {
				prepareInvalidMigrationLock(t, kind)
			}
			before := addProjectSnapshot(t)
			failure := errors.New("generate failed")
			err := withMigrationGenerationLock(ctx, func() error {
				if kind != "callback" {
					t.Error("invalid lock admitted generation")
				}
				return failure
			})
			if err == nil || kind == "callback" && !errors.Is(err, failure) || kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("missing original failure: %v", err)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func prepareInvalidMigrationLock(t *testing.T, kind string) {
	t.Helper()
	if err := os.Mkdir("migrations", 0700); err != nil {
		t.Fatal(err)
	}
	var err error
	switch kind {
	case "directory":
		err = os.Mkdir(migrationGenerationLockPath, 0700)
	case "contents":
		err = os.WriteFile(migrationGenerationLockPath, []byte("keep"), 0600)
	case "symlink", "dangling_symlink", "outside_symlink":
		target := "target"
		if kind == "outside_symlink" {
			parent := filepath.Dir(mustWorkingDirectory(t))
			if filepath.Base(parent) != ".tmp" {
				t.Fatalf("external lock fixture must stay in project .tmp: %s", parent)
			}
			directory, makeErr := os.MkdirTemp(parent, "migration-lock-external-")
			if makeErr != nil {
				t.Fatal(makeErr)
			}
			t.Cleanup(func() {
				if err := os.Remove(filepath.Join(directory, "target")); err != nil {
					t.Error(err)
				}
				if err := os.Remove(directory); err != nil {
					t.Error(err)
				}
			})
			target = filepath.Join(directory, "target")
		}
		if kind != "dangling_symlink" {
			err = os.WriteFile(target, nil, 0600)
		}
		if err == nil {
			if kind != "outside_symlink" {
				target = "../target"
			}
			err = os.Symlink(target, migrationGenerationLockPath)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
}

func mustWorkingDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMigrationGenerationLockOwnerExit(t *testing.T) {
	if os.Getenv("LUXO_TEST_MIGRATION_LOCK_OWNER") == "1" {
		if err := withMigrationGenerationLock(t.Context(), func() error {
			fmt.Println("migration lock acquired")
			_, err := os.Stdin.Read(make([]byte, 1))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	enterCacheGenerationScratch(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMigrationGenerationLockOwnerExit$")
	command.Env = append(os.Environ(), "LUXO_TEST_MIGRATION_LOCK_OWNER=1")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	})
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "migration lock acquired\n" {
		t.Fatalf("owner did not lock: %q %v", line, err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed owner unexpectedly returned success")
	}
	if err := withMigrationGenerationLock(ctx, func() error { return nil }); err != nil {
		t.Fatalf("owner exit left an unrecoverable lock: %v", err)
	}
}

func TestMigrationCommandsWaitForGenerationLock(t *testing.T) {
	for _, kind := range []string{"diff", "squash"} {
		t.Run(kind, func(t *testing.T) {
			prepareConcurrentMigrationProject(t)
			err := withMigrationGenerationLock(t.Context(), func() error {
				before := addProjectSnapshot(t)
				ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
				defer cancel()
				command := &cobra.Command{}
				command.SetContext(ctx)
				var err error
				if kind == "diff" {
					err = runMigrateDiff(command, []string{"next"})
				} else {
					err = runMigrateSquash(command, nil)
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("%s bypassed generation lock or lost cancellation: %v", kind, err)
				}
				assertAddProjectSnapshot(t, before)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationGenerationLockRejectsFilesystemErrors(t *testing.T) {

	enterCacheGenerationScratch(t)
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	created := false
	file, err := lockMigrationGeneration(t.Context(), root, &created)
	if err != nil || !created {
		t.Fatalf("missing directory was not recreated: %v %v", created, err)
	}
	defer file.Close()
	if _, err := file.WriteString("changed by another actor"); err != nil {
		t.Fatal(err)
	}
	if current, err := migrationGenerationLockCurrent(root, file); current || err == nil {
		t.Fatalf("changed lock content was accepted: %v %v", current, err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationGenerationLockCurrent(root, file); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed root error lost: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationGenerationLockCurrent(root, file); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed descriptor error lost: %v", err)
	}
	if err := waitMigrationGenerationLock(t.Context(), file); err == nil {
		t.Fatal("invalid descriptor was accepted for locking")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lockMigrationGeneration(ctx, root, &created); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retry lost cancellation: %v", err)
	}
}

func TestMigrationGenerationLockConcurrentCancellationCleans(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("migrations", 0700); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 256 {
		ctx, cancel := context.WithCancel(t.Context())
		group.Go(func() {
			entered := false
			err := withMigrationGenerationLock(ctx, func() error {
				entered = true
				return nil
			})
			if err != nil && (!errors.Is(err, context.Canceled) || entered) {
				t.Errorf("cancellation corrupted ownership or result: entered=%v err=%v", entered, err)
			}
		})
		runtime.Gosched()
		cancel()
	}
	group.Wait()
	entries, err := os.ReadDir("migrations")
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled generation left lock files: %v %v", entries, err)
	}
}

type migrationWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *migrationWaitContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func runMigrationAfterWaiting(t *testing.T, run func(*cobra.Command, []string) error, change func() error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	observed := &migrationWaitContext{Context: ctx, waiting: make(chan struct{})}
	cmd := &cobra.Command{}
	cmd.SetContext(observed)
	completed := make(chan error, 1)
	started := false
	ownerErr := withMigrationGenerationLock(ctx, func() error {
		started = true
		go func() { completed <- run(cmd, nil) }()
		select {
		case <-observed.waiting:
			return change()
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if !started {
		t.Fatal("migration lock owner did not start", ownerErr)
	}
	result := <-completed
	if ownerErr != nil {
		t.Fatal("migration lock owner failed", ownerErr)
	}
	return result
}

func TestMigrationRechecksStateAfterWaiting(t *testing.T) {
	for _, kind := range []string{"invalid-state", "invalid-diff", "squash-collision"} {
		t.Run(kind, func(t *testing.T) {
			writeGlobalGenerationProject(t, map[string]string{"main": "model Probe @noTime { id: Int @id }"})
			statePath := "migrations/.state.json"
			state := `{"models":{}}`
			writeCoverageFile(t, statePath, state)
			outputPath := filepath.Join("migrations", time.Now().Format("20060102")+"_001_initial.sql")
			run, prefix := runMigrateDiff, "load state:"
			if kind == "squash-collision" {
				run, prefix = runMigrateSquash, "migration output already exists:"
			}
			err := runMigrationAfterWaiting(t, run, func() error {
				if kind == "squash-collision" {
					return os.WriteFile(outputPath, []byte("-- preserve concurrent migration\n"), 0600)
				}
				state = "invalid state"
				if kind == "invalid-diff" {
					state = `{"models":{"Probe":{"table":"legacy","columns":{"id":{"type":"BIGINT","pk":true}}}}}`
					prefix = "diff migration state:"
				}
				return os.WriteFile(statePath, []byte(state), 0600)
			})
			if err == nil || !strings.Contains(err.Error(), prefix) {
				t.Fatalf("migration did not revalidate after lock wait: %v", err)
			}
			if got, err := os.ReadFile(statePath); err != nil || string(got) != state {
				t.Fatal("failed recheck modified migration state", string(got), err)
			}
			assertMigrationRecheckOutput(t, outputPath, kind == "squash-collision")
		})
	}
}

func assertMigrationRecheckOutput(t *testing.T, path string, collision bool) {
	t.Helper()
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || !collision && len(files) != 0 || collision && len(files) != 1 {
		t.Fatal("failed recheck produced migration SQL", files, err)
	}
	if collision {
		if got, err := os.ReadFile(path); err != nil || string(got) != "-- preserve concurrent migration\n" {
			t.Fatal("failed recheck replaced concurrent migration", string(got), err)
		}
	}
	if _, err := os.Stat(migrationGenerationLockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed recheck left generation lock", err)
	}
}
