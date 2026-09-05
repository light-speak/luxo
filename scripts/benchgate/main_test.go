package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateAcceptsNoiseAndImprovements(t *testing.T) {
	result, err := evaluate(strings.NewReader(benchstatCSV("~", "~", "n=10")))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Regressions) != 0 {
		t.Fatalf("regressions = %v", result.Regressions)
	}
	if result.TimeComparisons != 1 || result.AllocationComparisons != 2 {
		t.Fatalf("comparisons = time %d, allocation %d", result.TimeComparisons, result.AllocationComparisons)
	}
}

func TestEvaluateRejectsSignificantTimeRegression(t *testing.T) {
	result, err := evaluate(strings.NewReader(benchstatCSV("+5.01%", "~", "n=10")))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Regressions) != 1 || !strings.Contains(result.Regressions[0], "sec/op +5.01%") {
		t.Fatalf("regressions = %v", result.Regressions)
	}
}

func TestEvaluateRejectsSignificantAllocationRegression(t *testing.T) {
	result, err := evaluate(strings.NewReader(benchstatCSV("-2.00%", "+0.01%", "n=10")))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Regressions) != 2 {
		t.Fatalf("regressions = %v", result.Regressions)
	}
	if !strings.Contains(result.Regressions[0], "B/op +0.01%") {
		t.Fatalf("byte regression = %q", result.Regressions[0])
	}
	if !strings.Contains(result.Regressions[1], "allocs/op +0.01%") {
		t.Fatalf("allocation regression = %q", result.Regressions[1])
	}
}

func TestEvaluateRequiresTenSamplesPerRevision(t *testing.T) {
	for _, samples := range []string{"n=9", "n=10+9"} {
		_, err := evaluate(strings.NewReader(benchstatCSV("~", "~", samples)))
		if err == nil || !strings.Contains(err.Error(), "10 samples") {
			t.Fatalf("samples %q error = %v", samples, err)
		}
	}
}

func TestEvaluateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input io.Reader
		want  string
	}{
		{name: "read error", input: errorReader{}, want: "read failure"},
		{name: "invalid delta", input: strings.NewReader(benchstatCSV("?", "~", "n=10")), want: "invalid delta"},
		{name: "malformed delta", input: strings.NewReader(benchstatCSV("+bad%", "~", "n=10")), want: "invalid delta"},
		{name: "invalid samples", input: strings.NewReader(benchstatCSV("~", "~", "n=bad")), want: "invalid sample count"},
		{name: "missing samples", input: strings.NewReader(benchstatCSV("~", "~", "samples=10")), want: "invalid sample count"},
		{name: "no time", input: strings.NewReader("pkg: example\n" + benchstatTable("B/op", "~", "n=10")), want: "no time comparisons"},
		{name: "no allocations", input: strings.NewReader("pkg: example\n" + benchstatTable("sec/op", "~", "n=10")), want: "no allocation comparisons"},
		{name: "no package", input: strings.NewReader(strings.TrimPrefix(benchstatCSV("~", "~", "n=10"), "pkg: example\n")), want: "no package header"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := evaluate(test.input)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRunReportsSuccessAndRegression(t *testing.T) {
	tests := []struct {
		name      string
		timeDelta string
		wantError bool
		want      string
	}{
		{name: "success", timeDelta: "+5.00%", want: "performance gate passed"},
		{name: "regression", timeDelta: "+5.01%", wantError: true, want: "performance regression"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeRunInput(t, benchstatCSV(test.timeDelta, "~", "n=10"))
			var output strings.Builder
			err := run([]string{path}, &output)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v", err)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("output = %q", output.String())
			}
		})
	}
}

func TestRunRejectsInvalidArgumentsAndPaths(t *testing.T) {
	if err := run(nil, io.Discard); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("usage error = %v", err)
	}
	if err := run([]string{"missing"}, io.Discard); err == nil {
		t.Fatal("missing input error = nil")
	}
	path := writeRunInput(t, "")
	if err := run([]string{path}, io.Discard); err == nil || !strings.Contains(err.Error(), "no time comparisons") {
		t.Fatalf("invalid content error = %v", err)
	}
}

func TestCommandReturnsProcessStatus(t *testing.T) {
	path := writeRunInput(t, benchstatCSV("~", "~", "n=10"))
	if code := command([]string{path}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("success status = %d", code)
	}
	var errorOutput strings.Builder
	if code := command(nil, io.Discard, &errorOutput); code != 1 {
		t.Fatalf("error status = %d", code)
	}
	if !strings.Contains(errorOutput.String(), "usage") {
		t.Fatalf("error output = %q", errorOutput.String())
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failure")
}

func benchstatCSV(timeDelta, allocationDelta, samples string) string {
	return "pkg: example\n" +
		benchstatTable("sec/op", timeDelta, samples) + "\n" +
		benchstatTable("B/op", allocationDelta, samples) + "\n" +
		benchstatTable("allocs/op", allocationDelta, samples)
}

func benchstatTable(unit, delta, samples string) string {
	return strings.Join([]string{
		",base,,head,,",
		"," + unit + ",CI," + unit + ",CI,vs base,P",
		"EncodeEvent-2,10,0%,11,0%," + delta + ",p=0.001 " + samples,
		"geomean,10,,11,,+10.00%,",
	}, "\n")
}

func writeRunInput(t *testing.T, content string) string {
	t.Helper()
	root := filepath.Join("..", "..", ".tmp")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(root, "benchgate-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(filepath.Clean(directory), filepath.Clean(root)+string(filepath.Separator)) {
			t.Fatalf("refusing to clean unexpected directory %s", directory)
		}
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("clean test directory: %v", err)
		}
	})
	path := filepath.Join(directory, "benchstat.csv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
