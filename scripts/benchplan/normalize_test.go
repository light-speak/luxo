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
