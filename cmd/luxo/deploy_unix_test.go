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

func TestDeployHelmTemplateWriteFailureRestoresPreviousChart(t *testing.T) {
	prepareDeployProject(t, "example.com/deploy-probe")
	if err := runDeployHelm(t.Context()); err != nil {
		t.Fatal(err)
	}
	writeCoverageFile(t, "deploy/helm/.helmignore", "*.local\n")
	if err := os.Remove("origin/post.luxo"); err != nil {
		t.Fatal(err)
	}
	templates, err := filepath.Abs("deploy/helm/templates")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(templates, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(templates, 0700) })
	before := addProjectSnapshot(t)
	output, err := captureCLIResponse(t, func() error { return runDeployHelm(t.Context()) })
	if !errors.Is(err, os.ErrPermission) || output != "" {
		t.Fatalf("regeneration failure: %v %q", err, output)
	}
	assertAddProjectSnapshot(t, before)
}

func TestDeployHelmRetainsSymlinkForRetiredModule(t *testing.T) {
	prepareDeployProject(t, "example.com/deploy-probe")
	if err := runDeployHelm(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := "deploy/helm/templates/module-post-service.yaml"
	if err := os.Rename(path, "saved-service.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../saved-service.yaml", path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove("origin/post.luxo"); err != nil {
		t.Fatal(err)
	}
	if err := runDeployHelm(t.Context()); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(path); err != nil || target != "../../../saved-service.yaml" {
		t.Fatalf("user symlink changed: %q, %v", target, err)
	}
	assertDeployArtifactContains(t, "saved-service.yaml", ".Values.modules.post")
}

func TestDeployHelmUsesWritableChartUnderReadonlyProject(t *testing.T) {
	prepareDeployProject(t, "example.com/deploy-probe")
	if err := runDeployHelm(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove("origin/post.luxo"); err != nil {
		t.Fatal(err)
	}
	project, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(project, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(project, 0700) })
	if err := runDeployHelm(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat("deploy/helm/templates/module-post-service.yaml"); !os.IsNotExist(err) {
		t.Fatalf("retired template was not removed: %v", err)
	}
}
