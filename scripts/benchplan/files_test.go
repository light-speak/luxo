package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
