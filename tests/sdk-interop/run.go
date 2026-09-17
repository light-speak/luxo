package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func runFixtureCommand(args []string, gateway bool) error {
	if len(args) == 0 {
		return fmt.Errorf("-run requires a consumer command after --")
	}
	return withFixtureMode(gateway, func(endpoint, scratch string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, args[0], args[1:]...)
		corpus, err := filepath.Abs("pkg/lux/codec/testdata/protocol-v1.json")
		if err != nil {
			return err
		}
		topology := "direct"
		if gateway {
			topology = "gateway"
		}
		command.Env = append(os.Environ(), "LUXO_INTEROP_ENDPOINT="+endpoint,
			"LUXO_INTEROP_SCRATCH="+scratch, "LUXO_INTEROP_CORPUS="+corpus,
			"LUXO_INTROSPECTION_KEY=sdk-interop", "LUXO_INTEROP_TOPOLOGY="+topology)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		return command.Run()
	})
}

func withFixture(use func(endpoint, scratch string) error) (result error) {
	return withFixtureMode(false, use)
}

func withFixtureMode(gateway bool, use func(endpoint, scratch string) error) error {
	return withBuiltFixture(func(binary, scratch string) (result error) {
		deployment, err := startFixtureDeployment(binary, gateway)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, deployment.close()) }()
		return use(deployment.endpoint, scratch)
	})
}

func withBuiltFixture(use func(binary, scratch string) error) (result error) {
	root, err := filepath.Abs(".tmp")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	scratch, err := os.MkdirTemp(root, "sdk-interop-run-")
	if err != nil {
		return err
	}
	defer func() {
		if filepath.Dir(scratch) != root {
			result = errors.Join(result, fmt.Errorf("refusing cleanup outside fixture scratch"))
			return
		}
		result = errors.Join(result, os.RemoveAll(scratch))
	}()
	if err := generateFixture(scratch); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(scratch, "server")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, scratch)
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return err
	}
	return use(binary, scratch)
}

type fixtureProcess struct {
	command    *exec.Cmd
	input      io.WriteCloser
	output     io.ReadCloser
	done       chan error
	closeOnce  sync.Once
	closeError error
}

func startFixture(binary string, args ...string) (*fixtureProcess, error) {
	command := exec.Command(binary, args...)
	command.Stderr = os.Stderr
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	process := &fixtureProcess{command: command, input: input, output: output, done: make(chan error, 1)}
	go func() { process.done <- command.Wait() }()
	return process, nil
}

func (p *fixtureProcess) endpoint() (string, error) {
	return p.endpointScheme("http")
}

func (p *fixtureProcess) endpointScheme(scheme string) (string, error) {
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(p.output).ReadString('\n')
		lines <- strings.TrimSpace(line)
	}()
	select {
	case line := <-lines:
		address, err := url.Parse(line)
		if err != nil || address.Scheme != scheme || address.Hostname() != "127.0.0.1" || address.Port() == "" {
			return "", fmt.Errorf("invalid fixture endpoint %q", line)
		}
		return line, nil
	case <-time.After(10 * time.Second):
		return "", fmt.Errorf("fixture did not become ready")
	}
}

func (p *fixtureProcess) close() error {
	p.closeOnce.Do(func() { p.closeError = p.stop() })
	return p.closeError
}

func (p *fixtureProcess) stop() error {
	inputError := p.input.Close()
	if errors.Is(inputError, os.ErrClosed) {
		inputError = nil
	}
	select {
	case err := <-p.done:
		return errors.Join(inputError, err)
	case <-time.After(10 * time.Second):
		killError := p.command.Process.Kill()
		return errors.Join(inputError, killError, <-p.done, fmt.Errorf("fixture shutdown timed out"))
	}
}
