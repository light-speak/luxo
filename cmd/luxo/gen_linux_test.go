package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestIsMarkedGeneratedFileProcReadError(t *testing.T) {
	const path = "/proc/self/mem"
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		t.Skip("procfs self memory is unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Skip("procfs self memory is not presented as a regular file")
	}
	file, err := os.Open(path)
	if os.IsPermission(err) {
		t.Skip("host policy denies opening procfs self memory")
	}
	if err != nil {
		t.Fatal(err)
	}
	var header [1]byte
	_, readErr := io.ReadFull(file, header[:])
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(readErr, syscall.EIO) {
		t.Skipf("host does not reject offset-zero self-memory reads with EIO: %v", readErr)
	}
	marked, err := isMarkedGeneratedFile(path)
	var pathError *os.PathError
	if marked || !errors.Is(err, syscall.EIO) || !errors.As(err, &pathError) ||
		pathError.Op != "read" || pathError.Path != path {
		t.Fatalf("isMarkedGeneratedFile(%q) = %v, %v", path, marked, err)
	}
}

func TestIsMarkedGeneratedFileCloseError(t *testing.T) {
	if os.Getenv("LUXO_TEST_GENERATED_CLOSE_POLICY") == "1" {
		runGeneratedFileClosePolicy(t)
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.v", "-test.run=^TestIsMarkedGeneratedFileCloseError$", "-test.timeout=20s")
	command.Env = append(os.Environ(), "LUXO_TEST_GENERATED_CLOSE_POLICY=1")
	if profile := flag.Lookup("test.coverprofile"); testing.CoverMode() != "" && profile != nil && profile.Value.String() != "" {
		command.Args = append(command.Args, "-test.coverprofile="+profile.Value.String()+".close-child")
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("close-policy child: %v: %s", err, output)
	}
	if strings.Contains(string(output), "--- SKIP:") {
		t.Skipf("close-policy child prerequisite unavailable: %s", output)
	}
	if !strings.Contains(string(output), "--- PASS: TestIsMarkedGeneratedFileCloseError") {
		t.Fatalf("missing close-policy child assertion: %s", output)
	}
	t.Logf("child exited successfully; its remaining descriptors were released by the kernel:\n%s", output)
}

type generatedCloseResult struct {
	marked    bool
	err       error
	policyErr error
	before    int
	after     int
}

func runGeneratedFileClosePolicy(t *testing.T) {
	t.Helper()
	const path = "/proc/self/mem"
	file, err := os.Open(path)
	if os.IsNotExist(err) || os.IsPermission(err) {
		t.Skipf("procfs self-memory prerequisite unavailable: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	results := make(chan generatedCloseResult, 1)
	go func() {
		runtime.LockOSThread()
		result := generatedCloseResult{before: syscall.Gettid()}
		result.policyErr = installGeneratedClosePolicy()
		if result.policyErr == nil {
			result.marked, result.err = isMarkedGeneratedFile(path)
		}
		result.after = syscall.Gettid()
		results <- result
		// Keep the policy confined to this thread until the child exits.
		select {}
	}()
	result := <-results
	if result.policyErr != nil {
		t.Skipf("kernel does not permit the thread-local close policy: %v", result.policyErr)
	}
	var pathError *os.PathError
	if result.marked || !errors.Is(result.err, syscall.EIO) || !errors.As(result.err, &pathError) ||
		pathError.Op != "close" || pathError.Path != path || result.before != result.after {
		t.Fatalf("isMarkedGeneratedFile(%q) under close policy = %+v", path, result)
	}
}

func installGeneratedClosePolicy() error {
	filters := []syscall.SockFilter{
		{Code: 0x20, K: 0},
		{Code: 0x15, Jf: 1, K: uint32(syscall.SYS_CLOSE)},
		{Code: 0x06, K: 0x00050000 | uint32(syscall.EIO)},
		{Code: 0x06, K: 0x7fff0000},
	}
	program := syscall.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	if _, _, err := syscall.RawSyscall6(syscall.SYS_PRCTL, 38, 1, 0, 0, 0, 0); err != 0 {
		return fmt.Errorf("set no-new-privileges: %w", err)
	}
	// PR_SET_SECCOMP installs a thread-local errno policy without TSYNC.
	_, _, err := syscall.RawSyscall6(syscall.SYS_PRCTL, 22, 2, uintptr(unsafe.Pointer(&program)), 0, 0, 0)
	runtime.KeepAlive(filters)
	if err != 0 {
		return fmt.Errorf("install close-EIO kernel policy: %w", err)
	}
	return nil
}
