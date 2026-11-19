package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestResolveMigrationStateReadlinkPermission(t *testing.T) {
	if os.Getenv("LUXO_TEST_MIGRATION_NONDUMPABLE") == "1" {
		runMigrationReadlinkChild(t)
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResolveMigrationStateReadlinkPermission$")
	command.Env = append(os.Environ(), "LUXO_TEST_MIGRATION_NONDUMPABLE=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
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
	defer func() {
		input.Close()
		if err := command.Wait(); err != nil {
			t.Errorf("helper failed: %v: %s", err, stderr.String())
		}
	}()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil {
		t.Fatalf("read helper PID: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid helper PID %q: %v", line, err)
	}
	assertMigrationReadlinkPermission(t, filepath.Join("/proc", strconv.Itoa(pid), "exe"))
}

func runMigrationReadlinkChild(t *testing.T) {
	// Keep procfs metadata visible while denying another process the link target.
	if _, _, err := syscall.RawSyscall6(syscall.SYS_PRCTL, 4, 0, 0, 0, 0, 0); err != 0 {
		t.Fatalf("disable process dumpability: %v", err)
	}
	fmt.Println(os.Getpid())
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func assertMigrationReadlinkPermission(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected visible procfs symlink: info=%v err=%v", info, err)
	}
	_, err = os.Readlink(path)
	if err == nil {
		t.Skip("host permissions allow reading another non-dumpable process's exe link")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected procfs permission failure: %v", err)
	}
	target, info, err := resolveMigrationStatePath(path)
	var pathError *os.PathError
	if target != "" || info != nil || !errors.Is(err, os.ErrPermission) ||
		!errors.As(err, &pathError) || pathError.Op != "readlink" || pathError.Path != path {
		t.Fatalf("resolveMigrationStatePath(%q) = %q, %v, %v", path, target, info, err)
	}
}
