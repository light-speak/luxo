package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfirmationPlanWritesOnlyPairedLeaves(t *testing.T) {
	root := benchmarkTempDir(t)
	expected := filepath.Join(root, "primary.json")
	writeFixture(t, expected, `{"Base":[{"Package":"pkg/a","Name":"BenchmarkRead/a+b-2"},{"Package":"pkg/a","Name":"BenchmarkRead/skipped-2"},{"Package":"pkg/z","Name":"BenchmarkWrite-2"}],"Head":[{"Package":"pkg/a","Name":"BenchmarkRead/a+b-2"},{"Package":"pkg/a","Name":"BenchmarkRead/skipped-2"},{"Package":"pkg/z","Name":"BenchmarkWrite-2"}]}`)
	targets := filepath.Join(root, "targets.tsv")
	writeFixture(t, targets, "pkg/z\tBenchmarkWrite-2\npkg/a\tBenchmarkRead/a+b-2\n")
	batch := filepath.Join(root, "batch.tsv")
	confirmation := filepath.Join(root, "confirmation.json")
	if err := run([]string{"--confirmation-plan", expected, targets, batch, confirmation}, io.Discard); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(batch)
	if err != nil || string(got) != "pkg/a\t^BenchmarkRead$/^a\\+b$\npkg/z\t^BenchmarkWrite$\n" {
		t.Fatalf("confirmation batch = %q, %v", got, err)
	}
	sample := filepath.Join(root, "sample.txt")
	complete := "pkg: github.com/light-speak/luxo/pkg/a\n" +
		strings.Repeat("BenchmarkRead/a+b-2 1 12 ns/op 0 B/op 0 allocs/op\n", 10) + "PASS\n" +
		"pkg: github.com/light-speak/luxo/pkg/z\n" +
		strings.Repeat("BenchmarkWrite-2 1 12 ns/op 0 B/op 0 allocs/op\n", 10) + "PASS\n"
	verify := []string{"--verify", confirmation, sample, sample}
	writeFixture(t, sample, complete)
	if err := run(verify, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(complete, "BenchmarkWrite-2 1 12 ns/op 0 B/op 0 allocs/op\n", "", 1),
		strings.Replace(complete, "PASS", "BenchmarkRead/skipped-2 1 12 ns/op 0 B/op 0 allocs/op\nPASS", 1),
	} {
		writeFixture(t, sample, bad)
		if err := run(verify, io.Discard); err == nil {
			t.Fatal("accepted incorrect confirmation evidence")
		}
	}
}

func TestConfirmationPlanRejectsUnpairedOrMalformedTargets(t *testing.T) {
	root := benchmarkTempDir(t)
	expected := filepath.Join(root, "primary.json")
	writeFixture(t, expected, `{"Base":[{"Package":"pkg/a","Name":"BenchmarkRead/common-2"}],"Head":[{"Package":"pkg/a","Name":"BenchmarkRead/common-2"},{"Package":"pkg/a","Name":"BenchmarkRead/new-2"}]}`)
	targets := filepath.Join(root, "targets.tsv")
	batch := filepath.Join(root, "batch.tsv")
	confirmation := filepath.Join(root, "confirmation.json")
	for _, contents := range []string{
		"", "pkg/a\tBenchmarkRead/new-2\n", "pkg/z\tBenchmarkRead/common-2\n",
		"pkg/a\tBenchmarkRead/common-2\npkg/a\tBenchmarkRead/common-2\n", "pkg/a BenchmarkRead/common-2\n",
		"../a\tBenchmarkRead/common-2\n", "pkg/a\tBenchmarkRead/common-4\n", "pkg/a\tBenchmarkRead/with space-2\n",
	} {
		writeFixture(t, targets, contents)
		args := []string{"--confirmation-plan", expected, targets, batch, confirmation}
		if err := run(args, io.Discard); err == nil {
			t.Errorf("accepted invalid targets %q", contents)
		}
	}
}

func TestConfirmationPlanReportsFileAndInventoryFailures(t *testing.T) {
	root := benchmarkTempDir(t)
	expected := filepath.Join(root, "primary.json")
	targets := filepath.Join(root, "targets.tsv")
	writeFixture(t, targets, "pkg/a\tBenchmarkRead-2\n")
	const onePair = `{"Base":[{"Package":"pkg/a","Name":"BenchmarkRead-2"}],"Head":[{"Package":"pkg/a","Name":"BenchmarkRead-2"}]}`
	writeFixture(t, expected, onePair)
	batch := filepath.Join(root, "batch.tsv")
	confirmation := filepath.Join(root, "confirmation.json")
	for _, args := range [][]string{
		{"--confirmation-plan"},
		{"--confirmation-plan", filepath.Join(root, "missing"), targets, batch, confirmation},
		{"--confirmation-plan", expected, filepath.Join(root, "missing"), batch, confirmation},
		{"--confirmation-plan", expected, targets, root, confirmation},
		{"--confirmation-plan", expected, targets, batch, root},
	} {
		if err := run(args, io.Discard); err == nil {
			t.Errorf("accepted invalid options %v", args)
		}
	}
	duplicate := strings.Replace(onePair, `"Base":[{`, `"Base":[{"Package":"pkg/a","Name":"BenchmarkRead-2"},{`, 1)
	writeFixture(t, expected, duplicate)
	if err := run([]string{"--confirmation-plan", expected, targets, batch, confirmation}, io.Discard); err == nil {
		t.Fatal("accepted duplicate primary inventory")
	}
}

func TestConfirmationFilterRunsOnlySelectedNestedBenchmarks(t *testing.T) {
	root := benchmarkTempDir(t)
	keys := []sampleKey{
		{Package: "pkg/a", Name: "BenchmarkConfirmationFixture/nested/literal[+]-2"},
		{Package: "pkg/a", Name: "BenchmarkConfirmationFixture/nested/unselected-2"},
		{Package: "pkg/a", Name: "BenchmarkConfirmationFixture/top-2"},
	}
	expected := filepath.Join(root, "primary.json")
	data, err := json.Marshal(expectedSamples{Base: keys, Head: keys})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, expected, string(data))
	targets := filepath.Join(root, "targets.tsv")
	writeFixture(t, targets, "pkg/a\t"+keys[0].Name+"\npkg/a\t"+keys[2].Name+"\n")
	batch := filepath.Join(root, "batch.tsv")
	if err := run([]string{"--confirmation-plan", expected, targets, batch, filepath.Join(root, "confirmation.json")}, io.Discard); err != nil {
		t.Fatal(err)
	}
	plan, err := os.ReadFile(batch)
	if err != nil {
		t.Fatal(err)
	}
	_, filter, _ := strings.Cut(strings.TrimSpace(string(plan)), "\t")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), self, "-test.run=^$", "-test.bench="+filter,
		"-test.benchtime=1x", "-test.cpu=2", "-test.benchmem")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run nested benchmarks: %v\n%s", err, out)
	}
	have, err := readInventory(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range keys {
		if _, present := have["BenchmarkConfirmationFixture"][key.Name]; present != (i != 1) {
			t.Fatalf("wrong benchmark selection for %s\n%s", key.Name, out)
		}
	}
}

func BenchmarkConfirmationFixture(b *testing.B) {
	measure := func(b *testing.B) {
		for b.Loop() {
		}
	}
	b.Run("nested", func(b *testing.B) {
		b.Run("literal[+]", measure)
		b.Run("unselected", measure)
	})
	b.Run("top", measure)
}

func writeFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func inventoryFixture(t *testing.T) (string, string) {
	t.Helper()
	root := benchmarkTempDir(t)
	packages := filepath.Join(root, "packages.txt")
	writeFixture(t, packages, "pkg/a\npkg/b\n")
	for _, revision := range []string{"base", "head"} {
		for _, name := range []string{"a", "b"} {
			writeFixture(t, filepath.Join(root, revision, "pkg-"+name+".log"), "BenchmarkRead-2 1 12 ns/op 0 B/op 0 allocs/op\nPASS\n")
		}
	}
	return root, packages
}

func TestRunWritesReproduciblePlans(t *testing.T) {
	root, packages := inventoryFixture(t)
	var output bytes.Buffer
	destination := filepath.Join(root, "plans")
	if err := run([]string{packages, root, destination}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "{\"include\":[{\"shard\":\"01\"},{\"shard\":\"02\"}]}\n" {
		t.Fatalf("matrix=%s", &output)
	}
	for _, item := range []struct{ id, pkg string }{{"01", "a"}, {"02", "b"}} {
		plan, err := os.ReadFile(filepath.Join(destination, "shard-"+item.id+".tsv"))
		if err != nil || string(plan) != "pkg/"+item.pkg+"\t^(BenchmarkRead)$\n" {
			t.Fatalf("plan=%s err=%v", plan, err)
		}
		samplePath := filepath.Join(root, "samples.txt")
		writeFixture(t, samplePath, "pkg: github.com/light-speak/luxo/pkg/"+item.pkg+"\n"+strings.Repeat("BenchmarkRead-2 1 12 ns/op 0 B/op 0 allocs/op\n", 10)+"PASS\n")
		if err := run([]string{"--verify", filepath.Join(destination, "shard-"+item.id+".json"), samplePath, samplePath}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunScalesShardsWithSampleVolume(t *testing.T) {
	root, packages := inventoryFixture(t)
	writeFixture(t, packages, "pkg/a\n")
	var samples strings.Builder
	for group := range 24 {
		for leaf := range 100 {
			fmt.Fprintf(&samples, "BenchmarkGroup%d/leaf%d-2 1 12 ns/op 0 B/op 0 allocs/op\n", group, leaf)
		}
	}
	samples.WriteString("PASS\n")
	for _, revision := range []string{"base", "head"} {
		writeFixture(t, filepath.Join(root, revision, "pkg-a.log"), samples.String())
	}
	var output bytes.Buffer
	destination := filepath.Join(root, "plans")
	if err := run([]string{packages, root, destination}, &output); err != nil {
		t.Fatal(err)
	}
	var matrix struct{ Include []benchShard }
	if err := json.Unmarshal(output.Bytes(), &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Include) != 10 {
		t.Fatalf("planned %d shards for 4800 leaf measurements, want 10", len(matrix.Include))
	}
	total := 0
	for _, shard := range matrix.Include {
		out, err := os.ReadFile(filepath.Join(destination, "shard-"+shard.ID+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var expected expectedSamples
		if err := json.Unmarshal(out, &expected); err != nil {
			t.Fatal(err)
		}
		if len(expected.Base) == 0 || len(expected.Base) != len(expected.Head) {
			t.Fatalf("shard %s is missing paired evidence", shard.ID)
		}
		total += len(expected.Base) + len(expected.Head)
	}
	if total != 4800 {
		t.Fatalf("planned %d leaf measurements, want 4800", total)
	}
}

func TestCommandExitStatus(t *testing.T) {
	var failure bytes.Buffer
	if code := command(nil, io.Discard, &failure); code != 1 || !strings.Contains(failure.String(), "usage:") {
		t.Fatalf("code=%d error=%s", code, &failure)
	}
	root, packages := inventoryFixture(t)
	if code := command([]string{packages, root, filepath.Join(root, "out")}, io.Discard, &failure); code != 0 {
		t.Fatalf("code=%d error=%s", code, &failure)
	}
}

func TestPlanFileFailures(t *testing.T) {
	root, packages := inventoryFixture(t)
	if err := run(nil, io.Discard); err == nil {
		t.Fatal("accepted missing arguments")
	}
	for _, file := range []string{filepath.Join(root, "missing"), root} {
		if _, err := loadGroups(file, root); err == nil {
			t.Fatalf("accepted unreadable package list %s", file)
		}
	}
	for _, contents := range []string{"pkg/../escape", "elsewhere/a", "pkg/a\npkg/a", "pkg/missing"} {
		writeFixture(t, packages, contents)
		if err := run([]string{packages, root, filepath.Join(root, "out")}, io.Discard); err == nil {
			t.Fatalf("accepted packages %q", contents)
		}
	}
	writeFixture(t, packages, "pkg/a")
	writeFixture(t, filepath.Join(root, "head", "pkg-a.log"), "FAIL\n")
	if _, err := loadGroups(packages, root); err == nil {
		t.Fatal("accepted failed head inventory")
	}
	writeFixture(t, filepath.Join(root, "base", "pkg-a.log"), "FAIL\n")
	if _, err := loadGroups(packages, root); err == nil {
		t.Fatal("accepted failed base inventory")
	}
}

func TestPlanOutputFailures(t *testing.T) {
	root, packages := inventoryFixture(t)
	if err := run([]string{packages, root, packages}, io.Discard); err == nil {
		t.Fatal("accepted file as output directory")
	}
	for _, suffix := range []string{"tsv", "json"} {
		destination := filepath.Join(root, suffix)
		if err := os.MkdirAll(filepath.Join(destination, "shard-01."+suffix), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{packages, root, destination}, io.Discard); err == nil {
			t.Fatalf("ignored %s write failure", suffix)
		}
	}
	if err := run([]string{packages, root, filepath.Join(root, "out")}, failingIO{}); err == nil {
		t.Fatal("ignored matrix output failure")
	}
	writeFixture(t, packages, "")
	if err := run([]string{packages, root, filepath.Join(root, "out")}, io.Discard); err == nil {
		t.Fatal("accepted plan without baseline")
	}
}

type failingIO struct{}

func (failingIO) Read([]byte) (int, error)  { return 0, errors.New("read failure") }
func (failingIO) Write([]byte) (int, error) { return 0, errors.New("write failure") }

func TestInvalidMeasurementsAndReadFailures(t *testing.T) {
	for _, line := range []string{
		"BenchmarkA-2", "BenchmarkA-2 nope 1 ns/op", "BenchmarkA-2 1 1 ms/op",
		"Benchmark?-2 1 1 ns/op", "Benchmark-2 1 1 ns/op",
	} {
		if _, err := readInventory(strings.NewReader(line + "\nPASS\n")); err == nil {
			t.Errorf("accepted %q", line)
		}
	}
	if _, err := readInventory(failingIO{}); err == nil {
		t.Fatal("ignored read failure")
	}
	if err := verifySamples(failingIO{}, nil); err == nil {
		t.Fatal("ignored sample read failure")
	}
	if err := verifySamples(strings.NewReader("BenchmarkA-2\n"), nil); err == nil {
		t.Fatal("accepted malformed measurement")
	}
	if err := verifySamples(strings.NewReader("PASS\n"), []sampleKey{{Name: "same"}, {Name: "same"}}); err == nil {
		t.Fatal("accepted duplicate expected sample")
	}
}

func TestVerificationFileFailures(t *testing.T) {
	root := benchmarkTempDir(t)
	if err := verifyFiles(nil); err == nil {
		t.Fatal("accepted missing verification arguments")
	}
	if err := verifyFiles([]string{"--verify", filepath.Join(root, "missing"), "base", "head"}); err == nil {
		t.Fatal("ignored missing expectation file")
	}
	for _, contents := range []string{"broken", "{}", `{"Base":[{"Package":"pkg/a","Name":"BenchmarkA-2"}]}`} {
		file := filepath.Join(root, "expected.json")
		writeFixture(t, file, contents)
		if err := verifyFiles([]string{"--verify", file, filepath.Join(root, "missing"), "head"}); err == nil {
			t.Fatalf("accepted %s", contents)
		}
	}
	file := filepath.Join(root, "samples")
	writeFixture(t, file, "FAIL\n")
	if err := verifySampleFile(file, nil); err == nil {
		t.Fatal("ignored failed sample file")
	}
}

func benchmarkTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "benchplan-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(dir) != root {
			t.Fatal("refusing cleanup outside project .tmp")
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return dir
}
