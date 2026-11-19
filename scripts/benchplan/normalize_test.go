package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizePreservesMeasurementsAcrossStartupLogs(t *testing.T) {
	raw := "pkg: example\nBenchmarkRPC_Echo-2 \t Luxo RPC on localhost\n1\t12 ns/op\t0 B/op\t0 allocs/op\nPASS\n"
	var output bytes.Buffer
	if err := normalize(strings.NewReader(raw), &output); err != nil {
		t.Fatal(err)
	}
	want := "pkg: example\nBenchmarkRPC_Echo-2\t1\t12 ns/op\t0 B/op\t0 allocs/op\nPASS\n"
	if output.String() != want {
		t.Fatalf("normalized=%q", output.String())
	}
	output.Reset()
	if err := normalize(strings.NewReader(want), &output); err != nil || output.String() != want {
		t.Fatalf("normalization must be idempotent: %v %q", err, output.String())
	}
}

func TestNormalizePreservesBareNamesAndBlankStartupLines(t *testing.T) {
	raw := "pkg: example\n\nBenchmarkRPC_Echo-2\n2026/11/06 00:50:00 server starting\n\n10 12 ns/op 0 B/op 0 allocs/op\n\nPASS\n"
	var output bytes.Buffer
	if err := normalize(strings.NewReader(raw), &output); err != nil {
		t.Fatal(err)
	}
	want := "pkg: example\nBenchmarkRPC_Echo-2\t10 12 ns/op 0 B/op 0 allocs/op\nPASS\n"
	if output.String() != want {
		t.Fatalf("lost bare name or misclassified startup log: %q", output.String())
	}
}

func TestNormalizeNeverInventsMissingMeasurements(t *testing.T) {
	for _, raw := range []string{
		"BenchmarkRPC_Echo-2 log\nPASS\n", "BenchmarkRPC_Echo-2 log\n",
		"BenchmarkRPC_Echo-2 log\nBenchmarkOther-2 1 12 ns/op\nPASS\n",
		"BenchmarkRPC_Echo-2 log\npkg: other\n1 12 ns/op\nPASS\n",
		"BenchmarkRPC_Echo-2 log\nFAIL\n", "Benchmark?-2 log\n1 12 ns/op\nPASS\n",
	} {
		if err := normalize(strings.NewReader(raw), &bytes.Buffer{}); err == nil {
			t.Errorf("accepted missing or ambiguous measurement %q", raw)
		}
	}
}

func TestNormalizeRejectsMalformedMetricsBeforeAValidRow(t *testing.T) {
	const header = "pkg: github.com/light-speak/luxo/pkg/a\n"
	const tail = "10 12 ns/op 0 B/op 0 allocs/op\n"
	for _, malformed := range []string{
		"10 NaN ns/op 0 B/op 0 allocs/op", "10 +Inf ns/op 0 B/op 0 allocs/op",
		"10 -1 ns/op 0 B/op 0 allocs/op", "0 12 ns/op 0 B/op 0 allocs/op",
		"18446744073709551616 12 ns/op 0 B/op 0 allocs/op",
		"10 broken ns/op 0 B/op 0 allocs/op", "10 12 ns/op 0 B/op 0",
		"10", "10 12", "10 broken", "1.5", "NaN", "1e9999",
	} {
		for _, prefix := range []string{"BenchmarkRead-2 ", "BenchmarkRead-2 startup\n"} {
			raw := header + strings.Repeat("BenchmarkRead-2 "+tail, 9) + prefix + malformed + "\n" + tail + "PASS\n"
			var output bytes.Buffer
			if err := normalize(strings.NewReader(raw), &output); err == nil {
				verified := verifySamples(strings.NewReader(output.String()), []sampleKey{{Package: "pkg/a", Name: "BenchmarkRead-2"}}) == nil
				t.Errorf("concealed malformed metrics %q; verification accepted=%v", malformed, verified)
			}
		}
	}
}

func TestNormalizeRejectsMeasurementsWithoutBenchmarkIdentity(t *testing.T) {
	const row = "10 12 ns/op 0 B/op 0 allocs/op\n"
	for _, extra := range []string{row, "10 NaN ns/op 0 B/op 0 allocs/op\n", "10 12\n"} {
		raw := "pkg: github.com/light-speak/luxo/pkg/a\n" + strings.Repeat("BenchmarkRead-2 "+row, 10) + extra + "PASS\n"
		if err := normalize(strings.NewReader(raw), &bytes.Buffer{}); err == nil {
			t.Errorf("discarded an unidentified measurement: %q", extra)
		}
	}
}

func TestNormalizeFailuresAndUnrelatedLogs(t *testing.T) {
	for _, input := range []io.Reader{failingIO{}, strings.NewReader("BenchmarkA-2 pending\n")} {
		if err := normalize(input, io.Discard); err == nil {
			t.Fatal("ignored read or incomplete result")
		}
	}
	if err := normalize(strings.NewReader("PASS\n"), failingIO{}); err == nil {
		t.Fatal("ignored output failure")
	}
	for _, value := range []string{"NaN", "Inf", "-1", "nope"} {
		if measurementTail(strings.Fields("1 " + value + " ns/op")) {
			t.Fatalf("accepted %q", value)
		}
	}
	var output bytes.Buffer
	if err := normalize(strings.NewReader("BenchmarkA-2 pending\nstartup log\n1 12 ns/op\nPASS\n"), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "BenchmarkA-2\t1 12 ns/op") {
		t.Fatal("lost complete result")
	}
}

func TestNormalizeFileBoundaries(t *testing.T) {
	root := benchmarkTempDir(t)
	raw, target := filepath.Join(root, "raw"), filepath.Join(root, "target")
	for _, args := range [][]string{nil, {"--normalize", raw, raw}, {"--normalize", raw, target}} {
		if err := normalizeFiles(args); err == nil {
			t.Fatal("accepted invalid file arguments")
		}
	}
	writeFixture(t, raw, "BenchmarkA-2 1 12 ns/op\nPASS\n")
	if err := run([]string{"--normalize", raw, target}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := normalizeFiles([]string{"--normalize", raw, target}); err == nil {
		t.Fatal("overwrote existing evidence")
	}
}
