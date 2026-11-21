//go:build !darwin && !linux

package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func runOwnedFixtureCommand(_ context.Context, _ *exec.Cmd) error {
	return fmt.Errorf("owned SDK interoperability processes require Linux or macOS, got %s", runtime.GOOS)
}
