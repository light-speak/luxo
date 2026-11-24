package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func verifyLuxShortPathOverlay(t *testing.T, replacements map[string]string, pkg string) {
	t.Helper()
	ordinary, err := filepath.Glob(filepath.Join(pkg, "*_test.go"))
	if err != nil || len(ordinary) != len(replacements) {
		t.Fatalf("expected all %d test fixtures in overlay, got %d: %v", len(ordinary), len(replacements), err)
	}
	native := map[string]string{
		"condition_filter_bench_test.go": "03d082b02bcd5afc6fac26ec6bac93e51b3eb36a05b6c79ab12dbe0752249bd6",
		"decimal_bench_test.go":          "b175de4cf8476cc4eeef7f115b142e542b8b82d25b79341916ae8e207ff55c6f",
	}
	for _, path := range ordinary {
		replacement, present := replacements[path]
		want, retained := native[filepath.Base(path)]
		if !present || (replacement != "") != retained {
			t.Fatalf("unexpected short-path projection for %s: %q", path, replacement)
		}
		if retained {
			data, err := os.ReadFile(replacement)
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != want {
				t.Fatalf("Lux fixture %s does not match native evidence: %v", path, err)
			}
		}
	}
}

func TestLuxShortPathProfileBindsProjectionDigests(t *testing.T) {
	var output strings.Builder
	if err := run([]string{"performance-profile"}, nil, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := "\nlux-short-path-overlay-v1 " + luxFilterFixture + "=" + luxShortPathProjections[luxFilterFixture] +
		" " + luxDecimalFixture + "=" + luxShortPathProjections[luxDecimalFixture] + "\n"
	if strings.Count(output.String(), want) != 1 {
		t.Fatalf("profile must bind both Lux fixture projections: %q", output.String())
	}
}

func TestLuxShortPathProjectionFailsClosed(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.LuxPackage)
	work := filepath.Join(root, ".tmp")
	for _, name := range []string{luxFilterFixture, luxDecimalFixture} {
		raw, err := os.ReadFile(filepath.Join("../..", benchlayout.LuxPackage, name))
		if err != nil {
			t.Fatal(err)
		}
		writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.LuxPackage, name), string(raw))
	}
	if _, err := writeLuxShortPathOverlay(pkg, work); err != nil {
		t.Fatal(err)
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.LuxPackage, "unrelated_test.go"), "package lux\nfunc TestMain() {}\n")
	if _, err := writeLuxShortPathOverlay(pkg, work); err == nil || !strings.Contains(err.Error(), "cannot isolate test package setup") {
		t.Fatalf("omitted initialization error = %v", err)
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.LuxPackage, "unrelated_test.go"), "package lux\n")
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.LuxPackage, luxDecimalFixture), "package lux\n")
	if _, err := writeLuxShortPathOverlay(pkg, work); err == nil || !strings.Contains(err.Error(), "native short-path projection") {
		t.Fatalf("changed retained fixture error = %v", err)
	}
}

func TestLuxShortPathRejectsUnsafeFixtureInputs(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.LuxPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeLuxShortPathOverlay(pkg, workspace); !os.IsNotExist(err) {
		t.Fatalf("unavailable package error = %v", err)
	}
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLuxShortPathOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "missing Lux short-path fixture") {
		t.Fatalf("missing required fixture error = %v", err)
	}
	path := filepath.Join(pkg, luxFilterFixture)
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLuxShortPathOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "fixture must be a regular file") {
		t.Fatalf("non-regular fixture error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.LuxPackage, luxFilterFixture), "package other\n")
	if _, err := writeLuxShortPathOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), `unexpected package "other"`) {
		t.Fatalf("unexpected package error = %v", err)
	}
}
