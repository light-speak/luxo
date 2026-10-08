//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestFixtureOwnedCommandStopsDescendants(t *testing.T) {
	other := exec.Command(os.Args[0], "-test.run=^TestFixtureProcessHelper$")
	other.Env = append(os.Environ(), "LUXO_PROCESS_HELPER=leaf")
	other.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	for _, mode := range []string{"cancel", "deadline", "success", "failure"} {
		t.Run(mode, func(t *testing.T) { testFixtureOwnedDescendants(t, mode) })
	}
	if !fixtureHelperAlive(t, other.Process.Pid) {
		t.Fatal("unrelated session was stopped")
	}
}

func testFixtureOwnedDescendants(t *testing.T, mode string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestFixtureProcessHelper$")
	command.Env = append(os.Environ(), "LUXO_PROCESS_HELPER="+mode)
	testFixtureOwnedProcess(t, mode, command)
}

func TestFixtureOwnedSwiftProcesses(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Foundation process ownership is exercised on macOS")
	}
	source := `import Foundation
let child = Process()
child.executableURL = URL(fileURLWithPath: "/bin/sleep")
child.arguments = ["30"]
try child.run()
FileHandle.standardOutput.write(Data("\(ProcessInfo.processInfo.processIdentifier) \(child.processIdentifier)\n".utf8))
child.waitUntilExit()`
	testFixtureOwnedProcess(t, "cancel", exec.Command("swift", "-e", source))
}

func testFixtureOwnedProcess(t *testing.T, mode string, command *exec.Cmd) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	if mode == "deadline" {
		cancel()
		ctx, cancel = context.WithTimeout(t.Context(), 2*time.Second)
	}
	defer cancel()
	output, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	defer writer.Close()
	command.Stdout, command.Stderr = writer, os.Stderr
	done := make(chan error, 1)
	go func() { done <- runOwnedFixtureCommand(ctx, command) }()
	children := readFixtureChildren(t, output)
	if mode == "cancel" {
		cancel()
		cancel()
	}
	select {
	case err := <-done:
		if (err == nil) != (mode == "success") {
			t.Fatalf("mode %s: %v", mode, err)
		}
		if mode == "cancel" && !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
		if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("lost deadline: %v", err)
		}
		if mode == "failure" {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 7 {
				t.Fatalf("lost exit status: %v", err)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("owned command did not finish")
	}
	for _, pid := range children {
		if fixtureHelperAlive(t, pid) {
			t.Errorf("descendant %d survived %s", pid, mode)
		}
	}
}

func readFixtureChildren(t *testing.T, output io.Reader) []int {
	t.Helper()
	line := make(chan string, 1)
	go func() { text, _ := bufio.NewReader(output).ReadString('\n'); line <- text }()
	var children []int
	select {
	case text := <-line:
		for _, value := range strings.Fields(text) {
			pid, err := strconv.Atoi(value)
			if err != nil || pid <= 1 {
				t.Fatalf("invalid owned PID %q", value)
			}
			children = append(children, pid)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("helper did not become ready")
	}
	if len(children) != 2 {
		t.Fatalf("expected child and grandchild, got %v", children)
	}
	for _, pid := range children {
		session, err := fixtureProcessSession(pid)
		if err != nil {
			continue
		}
		t.Cleanup(func() {
			if current, err := fixtureProcessSession(pid); err == nil && current == session {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		})
	}
	return children
}

func TestFixtureProcessEnumeration(t *testing.T) {
	pids, err := parseFixtureProcessIDs(" 0 S\n1 S\n 12 R+\n13 Z\n14 Z+\n15 S\n\n")
	if err != nil || fmt.Sprint(pids) != "[12 15]" {
		t.Fatalf("%v, %v", pids, err)
	}
	for _, text := range []string{"1", "1 S extra", "oops S", "-1 S"} {
		if _, err := parseFixtureProcessIDs(text); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
	lookup := func(pid int) (int, error) {
		if pid == 13 {
			return 0, syscall.ESRCH
		}
		if pid == 14 {
			return 0, syscall.EPERM
		}
		if pid == 15 {
			return 9, nil
		}
		return 12, nil
	}
	pids, err = selectFixtureSession([]int{12, 13, 15, 16}, 12, lookup)
	if err != nil || fmt.Sprint(pids) != "[16]" {
		t.Fatalf("%v, %v", pids, err)
	}
	if _, err := selectFixtureSession([]int{14}, 12, lookup); !errors.Is(err, syscall.EPERM) {
		t.Fatal(err)
	}
	current, err := fixtureProcessSession(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureProcessSession(-1); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("invalid PID: %v", err)
	}
	for _, session := range []int{-1, 0, 1, current} {
		if _, err := fixtureSessionMembers(session); err == nil {
			t.Fatalf("accepted unowned session %d", session)
		}
	}
	for _, pid := range []int{0, 1, current} {
		if err := killFixtureSessionMember(pid, current); err == nil {
			t.Fatalf("accepted anchor %d", pid)
		}
	}
	if err := killFixtureSessionMember(os.Getpid(), -1); os.Getpid() > 1 && err != nil {
		t.Fatal(err)
	}
}

func TestFixtureProcessDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls, killed := 0, []int{}
		members := func(int) ([]int, error) {
			calls++
			if calls < 3 {
				return []int{calls}, nil
			}
			return nil, nil
		}
		kill := func(pid, session int) error {
			if session != 42 {
				t.Fatal(session)
			}
			killed = append(killed, pid)
			return nil
		}
		if err := drainFixtureSession(t.Context(), 42, members, kill); err != nil || fmt.Sprint(killed) != "[1 2]" {
			t.Fatalf("%v %v", killed, err)
		}
		failure := errors.New("cleanup failed")
		if err := drainFixtureSession(t.Context(), 42, func(int) ([]int, error) { return nil, failure }, kill); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		always := func(int) ([]int, error) { return []int{7}, nil }
		if err := drainFixtureSession(t.Context(), 42, always, func(int, int) error { return failure }); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := drainFixtureSession(ctx, 42, always, kill); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
}

func TestFixtureProcessStatus(t *testing.T) {
	for _, text := range []string{"0\n", "255\n", "256\n", "-1\n", "bad\n", ""} {
		t.Run(strconv.Quote(text), func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if _, err := io.WriteString(writer, text); err != nil {
				t.Fatal(err)
			}
			writer.Close()
			err = awaitFixtureCommand(t.Context(), reader)
			if (err == nil) != (text == "0\n" || text == "255\n") {
				t.Fatal(err)
			}
		})
	}
}

func TestFixtureOwnedCommandRejectsInvalidSetup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	command := exec.Command("/bin/sh", "-c", "exit 0")
	if err := runOwnedFixtureCommand(ctx, command); !errors.Is(err, context.Canceled) || command.Process != nil {
		t.Fatal(err)
	}
	for _, command := range []*exec.Cmd{
		{}, exec.CommandContext(t.Context(), "/bin/sh"),
		{Path: "/bin/sh", Args: []string{"sh"}, SysProcAttr: &syscall.SysProcAttr{}},
		{Path: "/bin/sh", Args: []string{"sh"}, Stdin: strings.NewReader("")},
		{Path: "/bin/sh", Args: []string{"sh"}, ExtraFiles: []*os.File{nil}},
	} {
		if err := runOwnedFixtureCommand(t.Context(), command); err == nil || command.Process != nil {
			t.Fatalf("invalid setup: %v", err)
		}
	}
	command = exec.Command("/bin/sh")
	command.Dir = "/no-such-luxo-owned-process-directory"
	if err := runOwnedFixtureCommand(t.Context(), command); err == nil {
		t.Fatal("accepted missing work directory")
	}
}

func TestFixtureOwnedCommandPreservesArguments(t *testing.T) {
	value := "a b ' \" $(printf unintended); *"
	command := exec.Command("/usr/bin/printf", "%s", value)
	var output strings.Builder
	command.Stdout = &output
	if err := runOwnedFixtureCommand(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if output.String() != value {
		t.Fatalf("changed shell arguments: %q", output.String())
	}
}

func fixtureHelperAlive(t *testing.T, pid int) bool {
	t.Helper()
	output, err := exec.Command("/bin/ps", "-e", "-o", "pid=,stat=").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == strconv.Itoa(pid) {
			return !strings.HasPrefix(fields[1], "Z")
		}
	}
	return false
}

func TestFixtureProcessHelper(t *testing.T) {
	mode := os.Getenv("LUXO_PROCESS_HELPER")
	if mode == "" {
		return
	}
	if mode == "leaf" {
		for {
			time.Sleep(time.Hour)
		}
	}
	childMode := "branch"
	if mode == "branch" {
		childMode = "leaf"
	}
	child := exec.Command(os.Args[0], "-test.run=^TestFixtureProcessHelper$")
	child.Env = append(os.Environ(), "LUXO_PROCESS_HELPER="+childMode)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := child.StdoutPipe()
	if err != nil {
		panic(err)
	}
	if err := child.Start(); err != nil {
		panic(err)
	}
	if mode == "branch" {
		fmt.Println(os.Getpid(), child.Process.Pid)
	} else {
		line, err := bufio.NewReader(output).ReadString('\n')
		if err != nil {
			panic(err)
		}
		fmt.Print(line)
	}
	if mode == "success" {
		os.Exit(0)
	}
	if mode == "failure" {
		os.Exit(7)
	}
	_, _ = io.Copy(io.Discard, output)
	_ = child.Wait()
	os.Exit(0)
}
