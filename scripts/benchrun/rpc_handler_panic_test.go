package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRPCHandlerPanicOverlayRetainsNativeWholeFixture(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	const profile, name, hash = "rpc-handler-panic", "handler_panic_bench_test.go", "d3f4a6e1b9e267997b4ab7d33a432e3345f32cc7fe4c248fa5fb47e5d94e9338"
	if !strings.Contains(performanceProfile+"\n", "\n"+profile+"-overlay-v1 "+name+"="+hash+"\n") {
		t.Fatal("missing native input fingerprint")
	}
	for _, baseline := range []bool{false, true} {
		path, cleanup, err := compileOverlay(root, "pkg/lux/rpc", baseline, profile)
		if err != nil || path == "" {
			t.Fatalf("RPC overlay: %s %v", path, err)
		}
		t.Cleanup(func() {
			if err := cleanup(); err != nil {
				t.Error(err)
			}
		})
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var overlay struct{ Replace map[string]string }
		if err := json.Unmarshal(body, &overlay); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(root, "pkg/lux/rpc", name))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != hash {
			t.Fatal("original native fixture changed")
		}
		verifyOnlyListedTestFiles(t, overlay.Replace, filepath.Join(root, "pkg/lux/rpc"), name)
	}
}

func TestRPCHandlerPanicOverlayRejectsUnverifiedInputs(t *testing.T) {
	const pkg, name, profile = "pkg/lux/rpc", "handler_panic_bench_test.go", "rpc-handler-panic"
	body, err := os.ReadFile(filepath.Join("../..", pkg, name))
	if err != nil {
		t.Fatal(err)
	}
	root := newBenchmarkRunTree(t)
	writeBenchmarkRunFixture(t, root, filepath.Join(pkg, "ordinary_test.go"), "package rpc\n")
	check := func(message string) {
		t.Helper()
		_, cleanup, err := compileOverlay(root, pkg, false, profile)
		if cleanup != nil || err == nil || !strings.Contains(err.Error(), message) {
			t.Fatalf("want %s with cleanup, got %v", message, err)
		}
	}
	check("no such file")
	writeBenchmarkRunFixture(t, root, filepath.Join(pkg, name), "// edited\n"+string(body))
	check("does not match the verified source")
	writeBenchmarkRunFixture(t, root, filepath.Join(pkg, name), string(body))
	writeBenchmarkRunFixture(t, root, filepath.Join(pkg, "ordinary_test.go"), "package rpc\nfunc TestMain() {}\n")
	check("cannot isolate test package setup")
}

func TestRPCBenchmarkOverlayRejectsUnknownProfile(t *testing.T) {
	if path, cleanup, err := rpcBenchmarkOverlay("unused", "pkg/lux/rpc", "not-rpc"); path != "" || cleanup != nil || err == nil {
		t.Fatalf("unsupported RPC overlay=%s,%v", path, err)
	}
}
