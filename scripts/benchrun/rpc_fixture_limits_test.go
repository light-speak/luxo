//go:build darwin || linux

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestWritePerformanceOverlayReportsWriteFailure(t *testing.T) {
	root := newBenchmarkRunTree(t)
	directory := filepath.Join(root, ".tmp")
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	limited := original
	limited.Cur = 0
	if !signal.Ignored(syscall.SIGXFSZ) {
		signal.Ignore(syscall.SIGXFSZ)
		defer signal.Reset(syscall.SIGXFSZ)
	}
	err := func() error {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
				t.Fatal(err)
			}
		}()
		_, _, err := writePerformanceOverlay(directory, filepath.Join(root, rpcBoundaryPath))
		return err
	}()
	if !errors.Is(err, syscall.EFBIG) {
		t.Fatalf("limited fixture write returned %v", err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("failed write left overlay entries %v, %v", entries, err)
	}
}

func TestCompileBenchmarksReportsOverlayCleanupFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		exitCode int
	}{{"compiler success", 0}, {"compiler failure", 7}} {
		t.Run(test.name, func(t *testing.T) {
			root := newBenchmarkRunTree(t)
			writeBenchmarkRunFixture(t, root, rpcBoundaryPath, isolatedRPCFixture)
			compiler := filepath.Join(root, "tool", "go")
			writeBenchmarkRunFixture(t, root, "tool/go", `#!/bin/sh
set -eu
for argument do
    case "$argument" in
        -overlay=*) overlay=${argument#-overlay=} ;;
    esac
done
case "$overlay" in
    "$PWD"/.tmp/bench-rpc-overlay-*.json) ;;
    *) exit 5 ;;
esac
mv "$overlay" "$overlay.saved"
mkdir "$overlay"
: > "$overlay/fixture"
exit "$BENCHRUN_TEST_COMPILER_EXIT"
`)
			if err := os.Chmod(compiler, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", filepath.Dir(compiler)+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("BENCHRUN_TEST_COMPILER_EXIT", strconv.Itoa(test.exitCode))
			args := []string{"compile-performance", root, filepath.Join(root, "bin"), "pkg/lux/rpc"}
			err := run(args, nil, io.Discard, io.Discard)
			var cleanupErr *os.PathError
			if !errors.As(err, &cleanupErr) || cleanupErr.Op != "remove" || filepath.Dir(cleanupErr.Path) != filepath.Join(root, ".tmp") {
				t.Fatalf("expected the overlay cleanup error, got %v", err)
			}
			var compileErr *exec.ExitError
			if found := errors.As(err, &compileErr); found != (test.exitCode != 0) || found && compileErr.ExitCode() != test.exitCode {
				t.Fatalf("unexpected compiler status, got %v", err)
			}
		})
	}
}
