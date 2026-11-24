package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func TestIsolationProfileCommand(t *testing.T) {
	for _, tc := range []struct{ pkg, want string }{
		{"pkg/lux/api", "api-non-identity\t.\n"},
		{"pkg/lux/luvia", "luvia-primary-trace\t^BenchmarkPrimaryKeys$|^BenchmarkTraceEncodingComparison$\n" +
			"luvia-gateway-identity\t^BenchmarkGatewayIdentity$\n"},
		{"pkg/codegen", "codegen-integer-literals\t^BenchmarkCompileDecimalIntegerLiterals$\n" +
			"codegen-global-receiver\t^BenchmarkGlobalReceiverName$\n"},
		{"pkg/lux", "lux-short-path\t^BenchmarkStrictFilterContract$|^BenchmarkDecimalAlignment$\n"},
		{"pkg/lux/rpc", ""},
	} {
		var output bytes.Buffer
		if err := run([]string{"isolation-profile", tc.pkg}, nil, &output, io.Discard); err != nil || output.String() != tc.want {
			t.Fatalf("discovery profile for %s = %q, %v", tc.pkg, &output, err)
		}
	}
	if err := run([]string{"isolation-profile", benchlayout.CodegenPackage}, nil, closedIsolationWriter{}, io.Discard); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("isolation output failure = %v", err)
	}
}

type closedIsolationWriter struct{}

func (closedIsolationWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestLinkedOverlaysPreserveFixtureBoundaries(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pkg, profile string
		assert       func(*testing.T, map[string]string, string)
	}{
		{benchlayout.APIPackage, benchlayout.APINonIdentityProfile, verifyAPIOverlay},
		{benchlayout.LuxPackage, benchlayout.LuxShortPathProfile, verifyLuxShortPathOverlay},
		{benchlayout.LuviaPackage, benchlayout.LuviaPrimaryTraceProfile, verifyLuviaOverlay},
		{benchlayout.LuviaPackage, benchlayout.LuviaGatewayIdentityProfile, verifyLuviaGatewayIdentityOverlay},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			file, cleanup, err := compileOverlay(root, tc.pkg, false, tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := cleanup(); err != nil {
					t.Error(err)
				}
			}()
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var overlay struct{ Replace map[string]string }
			if err := json.Unmarshal(data, &overlay); err != nil {
				t.Fatal(err)
			}
			tc.assert(t, overlay.Replace, filepath.Join(root, tc.pkg))
		})
	}
}

func verifyAPIOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	if len(replace) != 4 {
		t.Fatalf("API overlay changed %d files", len(replace))
	}
	for _, name := range []string{"identity_reader_bench_test.go", "identity_reader_fuzz_test.go", "identity_reader_test.go", "identity_value_test.go"} {
		if replacement, present := replace[filepath.Join(dir, name)]; !present || replacement != "" {
			t.Fatalf("API overlay did not omit %s", name)
		}
	}
}

func verifyLuviaOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	for _, name := range []string{"metrics_json_reference_test.go", "metrics_json_test.go", "primary_keys_baseline_test.go", "primary_keys_bench_test.go"} {
		if _, present := replace[filepath.Join(dir, name)]; present {
			t.Fatalf("Luvia overlay altered retained source %s", name)
		}
	}
	if v, present := replace[filepath.Join(dir, "convenience_keys_bench_test.go")]; !present || v != "" {
		t.Fatal("Luvia overlay retained an unrelated test file")
	}
	fixture := filepath.Join(dir, "metrics_retention_test.go")
	data, err := os.ReadFile(replace[fixture])
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(data, []byte("func observationTestTrace("))
	if start < 0 || !bytes.Contains(original, data[start:]) || bytes.Count(data, []byte("\nfunc ")) != 1 {
		t.Fatalf("Luvia observation projection changed its retained declaration:\n%s", data)
	}
}

func TestAPIOverlayRejectsLostAndAdditionalBenchmarkFixtures(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.APIPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeAPIBenchmarkOverlay(pkg, workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing API fixture error = %v", err)
	}
	name := filepath.Join(benchlayout.APIPackage, "identity_reader_bench_test.go")
	for _, tc := range []struct{ content, want string }{
		{"package api\nfunc BenchmarkUnrelated() {}", "non-identity benchmark"},
		{"package api\nfunc TestMain() {}", "cannot isolate test package setup"},
	} {
		writeBenchmarkRunFixture(t, root, name, tc.content)
		if _, err := writeAPIBenchmarkOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("API fixture error = %v, want %s", err, tc.want)
		}
	}
}

func TestLuviaOverlayRejectsLostAndChangedBenchmarkFixtures(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.LuviaPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeLuviaBenchmarkOverlay(pkg, workspace); !os.IsNotExist(err) {
		t.Fatalf("missing Luvia package error = %v", err)
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.LuviaPackage, "regular.go"), "package luvia")
	if _, err := writeLuviaBenchmarkOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "missing Luvia primary and trace test fixture") {
		t.Fatalf("missing Luvia fixture error = %v", err)
	}
	name := filepath.Join(benchlayout.LuviaPackage, "metrics_retention_test.go")
	writeBenchmarkRunFixture(t, root, name, "package luvia\nfunc wrongName() {}")
	if _, err := writeLuviaBenchmarkOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "missing observationTestTrace") {
		t.Fatalf("missing Luvia observation helper error = %v", err)
	}
	target := filepath.Join(pkg, "helper_test.go")
	if err := os.Symlink("regular.go", target); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLuviaBenchmarkOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink substitution error = %v", err)
	}
}
