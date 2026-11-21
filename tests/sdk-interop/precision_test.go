package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratePersistentPrecisionFixture(t *testing.T) {
	t.Chdir("../..")
	root, err := filepath.Abs(".tmp")
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.MkdirTemp(root, "sdk-precision-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(out) != root {
			t.Fatal("unowned fixture directory")
		}
		if err := os.RemoveAll(out); err != nil {
			t.Error(err)
		}
	})
	for _, invalid := range []string{"", root, filepath.Join(out, "missing")} {
		if err := generatePrecisionFixture(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	for index := range 6 {
		modes := make([]bool, 6)
		modes[index] = true
		if err := generatePrecisionMode(out, modes...); err == nil {
			t.Fatalf("accepted incompatible mode %d", index)
		}
	}
	if err := generatePrecisionMode(out); err != nil {
		t.Fatal(err)
	}
	model, err := os.ReadFile(filepath.Join(out, "model.gen.go"))
	if err != nil || !bytes.Contains(model, []byte("type NullableRecord struct")) {
		t.Fatalf("missing persistent nullable record fixture: %v", err)
	}
	if err := generatePrecisionFixture(out); err == nil {
		t.Fatal("overwrote generated fixture")
	}
	proxy, err := os.ReadFile(filepath.Join(out, "gateway_proxy.gen.go"))
	if err != nil || !bytes.Contains(proxy, []byte("func proxyHandler(")) {
		t.Fatalf("missing generated gateway: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated fixture: %v\n%s", err, output)
	}
}
