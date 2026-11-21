//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Each consumer owns a session, not just a process group: SwiftPM and git can
// create new groups. Cooperative test tools must not daemonize into new sessions.
func runOwnedFixtureCommand(ctx context.Context, command *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	status, release, err := startOwnedFixtureCommand(command)
	if err != nil {
		return err
	}
	defer status.Close()
	defer release.Close()
	result := awaitFixtureCommand(ctx, status)
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drainFixtureSession(cleanup, command.Process.Pid, fixtureSessionMembers, killFixtureSessionMember); err != nil {
		result = errors.Join(result, fmt.Errorf("%w (session %d): %w", errFixtureProcessCleanup, command.Process.Pid, err))
	}
	// Keep the anchor alive (or unreaped) until cleanup finishes. Its PID/session
	// cannot be recycled while we are deciding which descendants belong to it.
	if result != nil {
		_ = command.Process.Kill()
	}
	_ = release.Close()
	return errors.Join(result, command.Wait())
}

func startOwnedFixtureCommand(command *exec.Cmd) (*os.File, *os.File, error) {
	if command.Path == "" || len(command.Args) == 0 {
		return nil, nil, errors.New("fixture command requires an executable")
	}
	if command.Cancel != nil || command.SysProcAttr != nil || command.Stdin != nil || len(command.ExtraFiles) != 0 {
		return nil, nil, errors.New("fixture command must leave context, stdin and process ownership to the runner")
	}
	status, statusWriter, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	defer statusWriter.Close()
	releaseReader, release, err := os.Pipe()
	if err != nil {
		status.Close()
		return nil, nil, err
	}
	defer releaseReader.Close()
	// Arguments are passed positionally, never interpolated into shell source.
	// Descendants cannot retain the private status/control descriptors.
	script := `"$@" </dev/null 3>&-
code=$?
printf '%s\n' "$code" >&3
IFS= read -r release || :
exit "$code"`
	command.Args = append([]string{"/bin/sh", "-c", script, "luxo-fixture-owner", command.Path}, command.Args[1:]...)
	command.Path = "/bin/sh"
	command.Stdin = releaseReader
	command.ExtraFiles = []*os.File{statusWriter}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.WaitDelay = 5 * time.Second
	if err := command.Start(); err != nil {
		status.Close()
		release.Close()
		return nil, nil, err
	}
	return status, release, nil
}

func awaitFixtureCommand(ctx context.Context, status *os.File) error {
	done := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(status).ReadString('\n')
		if err == nil {
			code, parseErr := strconv.Atoi(strings.TrimSpace(line))
			if parseErr != nil || code < 0 || code > 255 {
				err = errors.New("invalid fixture exit status")
			}
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func drainFixtureSession(ctx context.Context, session int, members func(int) ([]int, error), kill func(int, int) error) error {
	for {
		pids, err := members(session)
		if err != nil || len(pids) == 0 {
			return err
		}
		for _, pid := range pids {
			err = errors.Join(err, kill(pid, session))
		}
		if err != nil {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func fixtureSessionMembers(session int) ([]int, error) {
	current, err := fixtureProcessSession(0)
	if err != nil {
		return nil, err
	}
	if session <= 1 || session == current {
		return nil, fmt.Errorf("refusing unowned process session %d", session)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/bin/ps", "-e", "-o", "pid=,stat=").Output()
	if err != nil {
		return nil, fmt.Errorf("list fixture processes: %w", err)
	}
	pids, err := parseFixtureProcessIDs(string(output))
	if err != nil {
		return nil, err
	}
	return selectFixtureSession(pids, session, fixtureProcessSession)
}

func parseFixtureProcessIDs(output string) ([]int, error) {
	var pids []int
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, errors.New("invalid process enumeration")
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid < 0 {
			return nil, errors.New("invalid enumerated process ID")
		}
		// Zombies have exited and cannot access scratch; only their parent can reap them.
		if pid > 1 && !strings.HasPrefix(fields[1], "Z") {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func selectFixtureSession(pids []int, session int, getsid func(int) (int, error)) ([]int, error) {
	var members []int
	for _, pid := range pids {
		actual, err := getsid(pid)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if actual == session && pid != session {
			members = append(members, pid)
		}
	}
	return members, nil
}

func killFixtureSessionMember(pid, session int) error {
	if pid <= 1 || pid == session {
		return errors.New("refusing to kill a fixture session anchor")
	}
	actual, err := fixtureProcessSession(pid)
	if errors.Is(err, syscall.ESRCH) || (err == nil && actual != session) {
		return nil
	}
	if err != nil {
		return err
	}
	err = syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
