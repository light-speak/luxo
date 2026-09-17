package luvia

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestServeHandlesOSSignals(t *testing.T) {
	const childMode = "LUXO_TEST_GATEWAY_SIGNAL_MODE"
	if mode := os.Getenv(childMode); mode != "" {
		if mode == "tls" {
			cert, key := generateTestCert(t)
			t.Setenv("APP_TLS_CERT", cert)
			t.Setenv("APP_TLS_KEY", key)
		}
		if err := New().Serve("signal-test"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support sending interrupt signals with os.Process.Signal")
	}
	for _, mode := range []string{"h2c", "tls"} {
		for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(mode+"/"+sig.String(), func(t *testing.T) {
				runGatewaySignalProcess(t, childMode, mode, sig)
			})
		}
	}
}

func runGatewaySignalProcess(t *testing.T, childMode, mode string, sig os.Signal) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServeHandlesOSSignals$", "-test.count=1")
	cmd.Env = gatewaySignalEnvironment(childMode, mode)
	output := &gatewaySignalOutput{ready: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			<-done
		}
	}()
	select {
	case <-output.ready:
	case err := <-done:
		waited = true
		t.Fatalf("gateway exited before listening: %v\n%s", err, output.log.String())
	case <-ctx.Done():
		t.Fatal("gateway did not start before the test deadline")
	}
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	err := <-done
	waited = true
	log := output.log.String()
	if err != nil || !strings.Contains(log, fmt.Sprintf("Received %s, shutting down", sig)) ||
		!strings.Contains(log, "Server stopped gracefully.") {
		t.Fatalf("gateway signal shutdown: %v\n%s", err, log)
	}
}

// The readiness channel is separate from process completion; logs are read only after Wait.
type gatewaySignalOutput struct {
	log   bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (o *gatewaySignalOutput) Write(p []byte) (int, error) {
	n, err := o.log.Write(p)
	if bytes.Contains(o.log.Bytes(), []byte("Listening on ")) {
		o.once.Do(func() { close(o.ready) })
	}
	return n, err
}

func gatewaySignalEnvironment(childMode, mode string) []string {
	environment := make([]string, 0, len(os.Environ())+12)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "JWT_SECRET=") {
			environment = append(environment, variable)
		}
	}
	return append(environment, childMode+"="+mode, "APP_PORT=0", "APP_TLS_CERT=", "APP_TLS_KEY=",
		"DEPLOY_MODE=embedded", "DATABASE_HOST=", "LUXO_STUDIO_URL=", "LUXO_API_KEY=",
		"GRACEFUL_TIMEOUT=1s", "TIMEOUT_READ=1s", "TIMEOUT_WRITE=1s", "TIMEOUT_IDLE=1s")
}

func gatewayTestDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "luvia-lifecycle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(dir) != root || !strings.HasPrefix(filepath.Base(dir), "luvia-lifecycle-") {
			t.Fatalf("refusing cleanup outside the lifecycle scratch directory: %s", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return dir
}
