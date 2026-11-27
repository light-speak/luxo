package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransportNativeBenchmarkProjections(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pkg, profile string
		digests      map[string]string
	}{
		{"pkg/lux/rpc", "rpc-pool-return-rejection", map[string]string{
			"pool_close_bench_test.go":     "53276ef3d60f8877b547366a08cc40c01d544aefabcb8324b4d08d8d21aa74d4",
			"pool_close_reference_test.go": "",
			"pool_lifecycle_test.go":       "1493983cd599a3494d6ddcd985c0ae811126a47b22d993ec0231b17c4019f522",
		}},
		{"pkg/lux/h2transport", "h2-task-deadline-replacement", map[string]string{
			"scheduling_test.go": "ca6cb3a6f7508e703de80f936f274a143ccf9d516b9167f1b8e5c16a2c0b47b1",
		}},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			if !strings.Contains(performanceProfile, "\n"+tc.profile+"-overlay-v1 ") {
				t.Error("missing native projection fingerprint")
			}
			for _, baseline := range []bool{true, false} {
				verifyTransportNativeOverlay(t, root, tc.pkg, tc.profile, tc.digests, baseline)
			}
			for name := range tc.digests {
				verifyTransportFixtureRejectsDrift(t, root, tc.pkg, tc.profile, name, tc.digests)
			}
		})
	}
}

func verifyTransportNativeOverlay(t *testing.T, root, pkg, profile string, digests map[string]string, baseline bool) {
	t.Helper()
	path, cleanup, err := compileOverlay(root, pkg, baseline, profile)
	if err != nil || path == "" {
		t.Fatalf("missing overlay for baseline=%v: %q, %v", baseline, path, err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Error(err)
		}
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct{ Replace map[string]string }
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, pkg)
	retained := make([]string, 0, len(digests))
	for name, wanted := range digests {
		retained = append(retained, name)
		// An empty projected digest requires the original source path.
		if wanted == "" {
			continue
		}
		source := filepath.Join(dir, name)
		body, err := os.ReadFile(overlay.Replace[source])
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != wanted {
			t.Fatalf("%s SHA = %s, want %s", name, got, wanted)
		}
		delete(overlay.Replace, source)
	}
	verifyOnlyListedTestFiles(t, overlay.Replace, dir, retained...)
}

func verifyTransportFixtureRejectsDrift(t *testing.T, root, pkg, profile, missing string, fixtures map[string]string) {
	t.Helper()
	target := newBenchmarkRunTree(t)
	writeBenchmarkRunFixture(t, target, filepath.Join(pkg, "ordinary_test.go"), "package "+filepath.Base(pkg)+"\n")
	for name := range fixtures {
		if name == missing {
			continue
		}
		path := filepath.Join(pkg, name)
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		writeBenchmarkRunFixture(t, target, path, string(data))
	}
	if _, cleanup, err := compileOverlay(target, pkg, false, profile); cleanup != nil || err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("expected missing fixture %s, got %v", missing, err)
	}
	writeBenchmarkRunFixture(t, target, filepath.Join(pkg, missing), "package "+filepath.Base(pkg)+"\n// changed fixture\n")
	if _, cleanup, err := compileOverlay(target, pkg, true, profile); cleanup != nil || err == nil ||
		!strings.Contains(err.Error(), "verified source") {
		t.Fatalf("changed fixture %s was not rejected: %v", missing, err)
	}
}

func TestTransportFixtureRequiresSignedOrderedProjection(t *testing.T) {
	path, err := filepath.Abs("../../pkg/lux/h2transport/scheduling_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(source))
	for _, tc := range []struct {
		name, want string
		fixture    signedTransportTestFile
	}{
		{"missing source record", "verified source digest", signedTransportTestFile{}},
		{"missing projection digest", "verified projection digest", signedTransportTestFile{sourceSHA: digest, lines: [][2]int{{195, 226}}}},
		{"missing projection spans", "verified source spans", signedTransportTestFile{sourceSHA: digest, projectionSHA: digest}},
		{"overlapping in-range spans", "invalid source span", signedTransportTestFile{sourceSHA: digest, projectionSHA: digest, lines: [][2]int{{195, 226}, {224, 225}}}},
		{"out of range", "invalid source span", signedTransportTestFile{sourceSHA: digest, projectionSHA: digest, lines: [][2]int{{195, 1 << 20}}}},
		{"reversed bounds", "invalid source span", signedTransportTestFile{sourceSHA: digest, projectionSHA: digest, lines: [][2]int{{226, 195}}}},
		{"zero bounds", "invalid source span", signedTransportTestFile{sourceSHA: digest, projectionSHA: digest, lines: [][2]int{{0, 226}}}},
		{"wrong projection digest", "does not match the verified projection", signedTransportTestFile{sourceSHA: digest, projectionSHA: digest, lines: [][2]int{{195, 226}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			got, err := projectTransportTestFile(path, workspace, tc.fixture, true)
			if got != "" || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("malformed projection = %q, %v, want %q", got, err, tc.want)
			}
			if _, err := os.Lstat(filepath.Join(workspace, filepath.Base(path))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid projection wrote a file: %v", err)
			}
		})
	}
}

func TestTransportFixtureReadFailureAndUnknownProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing_test.go")
	if got, err := projectTransportTestFile(path, dir, signedTransportTestFile{}, true); got != "" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing input read = %q, %v", got, err)
	}
	if got, cleanup, err := transportBenchmarkOverlay(dir, "pkg/lux/rpc", "unregistered"); got != "" || cleanup != nil ||
		err == nil || !strings.Contains(err.Error(), "missing signed fixtures") {
		t.Fatalf("unregistered fixture profile = %q, %v", got, err)
	}
}

func TestTransportFixtureRejectsPackageSetupInExcludedFile(t *testing.T) {
	const pkg = "pkg/lux/h2transport"
	const profile = "h2-task-deadline-replacement"
	root := newBenchmarkRunTree(t)
	writeBenchmarkRunFixture(t, root, filepath.Join(pkg, "unrelated_test.go"), "package h2transport\nfunc TestMain() {}\n")
	if path, cleanup, err := compileOverlay(root, pkg, false, profile); path != "" || cleanup != nil ||
		err == nil || !strings.Contains(err.Error(), "cannot isolate test package setup") {
		t.Fatalf("unexpected test setup was accepted: %q, %v", path, err)
	}
}
