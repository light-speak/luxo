//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployHelmWritePermissionFailurePreservesProject(t *testing.T) {
	prepareDeployProject(t, "example.com/deploy-probe")
	if err := os.MkdirAll("deploy/helm/templates", 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"deploy/helm", "deploy/helm/templates"} {
		absolute, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(absolute, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(absolute, 0700) })
	}
	before := addProjectSnapshot(t)
	output, err := captureCLIResponse(t, func() error { return runDeploy(nil, []string{"helm"}) })
	if !errors.Is(err, os.ErrPermission) || !strings.HasPrefix(err.Error(), "write ") || strings.Contains(output, "generated") {
		t.Fatalf("write permission error was hidden or reported success: %v %q", err, output)
	}
	assertAddProjectSnapshot(t, before)
}
