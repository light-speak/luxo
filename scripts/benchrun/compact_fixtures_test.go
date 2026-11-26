package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompactFixturesMatchIntelProjectionsInBothArms(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pkg, profile string
		fixtures     map[string]string
	}{
		{"pkg/lux/api", "api-static-value-params", map[string]string{
			"request_value_bench_test.go":      "e18ce1b22bd7037e8d987cd61f82310f52c9fa583e85b04752c5bea3c67486c3",
			"request_value_test.go":            "5e977cca9dd55aff5c1c5a02a6a862c5b6c8ac21d275dd055c23cd893394f769",
			"request_legacy_reference_test.go": "7b9c4f7403501466b38d166e0c52b986c214a0c6e1ee3b26c6efe7a49d6251e5",
		}},
		{"pkg/lux/rpc", "rpc-frame-reuse", map[string]string{
			"frame_prepared_bench_test.go": "c7407c7b91bd9a0f352f7059b64df3a97274f4904563c12f7fde6a3e9a683ad5",
		}},
		{"pkg/lux/pg", "pg-duration-write-reject", map[string]string{
			"duration_bind_bench_test.go": "549c6791300094dbf71c0c220bbd6a1bd86b6d0c1e7c7b7da97b90506ae2b322",
		}},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			for _, baseline := range []bool{true, false} {
				verifyCompactFixtureOverlay(t, root, tc.pkg, tc.profile, tc.fixtures, baseline)
			}
		})
	}
}

func verifyCompactFixtureOverlay(t *testing.T, root, pkg, profile string, fixtures map[string]string, baseline bool) {
	t.Helper()
	overlay, cleanup, err := compileOverlay(root, pkg, baseline, profile)
	if err != nil || overlay == "" {
		t.Fatalf("overlay for baseline %v = %q, %v", baseline, overlay, err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Error(err)
		}
	})
	body, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatal(err)
	}
	var data struct{ Replace map[string]string }
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	var names []string
	for name, witness := range fixtures {
		names = append(names, name)
		path := filepath.Join(root, pkg, name)
		rendered, err := os.ReadFile(data.Replace[path])
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(rendered))
		if got != witness || !strings.Contains(performanceProfile, name+"="+witness) {
			t.Fatalf("fixture %s differs from its bound native witness: %s", name, got)
		}
		delete(data.Replace, path)
	}
	verifyOnlyListedTestFiles(t, data.Replace, filepath.Join(root, pkg), names...)
}

func TestPGDurationWriteRejectRequiresConcreteAdapter(t *testing.T) {
	root := newBenchmarkRunTree(t)
	writeBenchmarkRunFixture(t, root, "pkg/lux/dialect/pg/read_only_test.go", "package pg")
	for _, command := range []string{"compile-performance", "compile-performance-baseline", "run-performance"} {
		err := run([]string{command, root, filepath.Join(root, "bin"), "pkg/lux/pg", "pg-duration-write-reject"}, nil, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "missing benchmark package pkg/lux/pg") {
			t.Fatalf("%s selected the dialect package: %v", command, err)
		}
	}
}

func TestCompactFixtureRejectsChangedFrozenPrevious(t *testing.T) {
	root := newBenchmarkRunTree(t)
	writeBenchmarkRunFixture(t, root, "pkg/lux/api/request_legacy_reference_test.go", "package api")
	_, cleanup, err := compileOverlay(root, "pkg/lux/api", false, "api-static-value-params")
	if cleanup != nil || err == nil || !strings.Contains(err.Error(), "verified source") {
		t.Fatalf("changed frozen previous reader accepted: %v", err)
	}
}
