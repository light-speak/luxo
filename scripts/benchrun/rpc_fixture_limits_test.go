//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"os/signal"
	"path/filepath"
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
