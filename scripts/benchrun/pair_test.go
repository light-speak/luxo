package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fixturePackage = "pkg/lux/sample"

const fixtureBenchmark = `package sample
import "testing"
var result []byte
func BenchmarkProbe(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() { result = consume() }
}
`

func TestCompilePairPinsRegisteredTestsAndHelpers(t *testing.T) {
	root := benchmarkTestRoot(t)
	base := pairTestRevision(t, root, "base", "package sample\nfunc probe() []byte { return nil }\n")
	head := pairTestRevision(t, root, "head", "package sample\nfunc probe() []byte { return nil }\n")
	writePairTestFile(t, head, fixturePackage+"/helper_test.go", `package sample
import "testing"
func consume() []byte { return []byte("changed fixture result") }
func TestNew(t *testing.T) { t.Log(consume()) }
func BenchmarkNew(b *testing.B) { for b.Loop() {} }
`)
	writePairTestFile(t, head, fixturePackage+"/added_test.go", `package sample
import "testing"
func BenchmarkOneSided(b *testing.B) { for b.Loop() {} }
`)

	oldBase, oldHead := filepath.Join(root, "native-base"), filepath.Join(root, "native-head")
	pairTestRun(t, "compile", base, oldBase, fixturePackage)
	pairTestRun(t, "compile", head, oldHead, fixturePackage)
	if bytes.Equal(pairTestBinary(t, oldBase), pairTestBinary(t, oldHead)) {
		t.Fatal("native compilation unexpectedly used the same test roots")
	}
	if listing := pairTestRun(t, "run", head, oldHead, fixturePackage, "-test.list=."); !strings.Contains(listing, "BenchmarkOneSided") {
		t.Fatalf("head-only benchmark was lost: %s", listing)
	}

	output := filepath.Join(root, "paired")
	pairTestRun(t, "compile-pair", base, head, output, fixturePackage)
	newBase, newHead := filepath.Join(output, "base"), filepath.Join(output, "head")
	if !bytes.Equal(pairTestBinary(t, newBase), pairTestBinary(t, newHead)) {
		t.Fatal("identical production and pinned tests must compile to identical binaries")
	}
	listing := pairTestRun(t, "run", head, newHead, fixturePackage, "-test.list=.")
	if strings.TrimSpace(listing) != "BenchmarkProbe" {
		t.Fatalf("unexpected paired roots: %s", listing)
	}
	if _, err := os.Stat(filepath.Join(output, "fixtures.json")); err != nil {
		t.Fatalf("missing compile receipt: %v", err)
	}
}

func TestCompilePairPinsExternalTestsInNestedModule(t *testing.T) {
	root := benchmarkTestRoot(t)
	product := "package sample\nfunc probe() []byte { return nil }\nfunc Exported() []byte { return probe() }\n"
	base := pairTestRevision(t, root, "base", product)
	head := pairTestRevision(t, root, "head", product)
	for _, side := range []string{base, head} {
		writePairTestFile(t, side, "go.work", "go 1.27.1\nuse (\n.\n./pkg/lux/sample\n)\n")
		writePairTestFile(t, side, fixturePackage+"/go.mod", "module github.com/light-speak/luxo/pkg/lux/sample\ngo 1.27.1\n")
	}
	writePairTestFile(t, base, fixturePackage+"/external_test.go", `package sample_test
import (
	"testing"
	"github.com/light-speak/luxo/pkg/lux/sample"
)
func BenchmarkExternal(b *testing.B) { for b.Loop() { _ = sample.Exported() } }
`)
	writePairTestFile(t, head, fixturePackage+"/external_test.go", `package sample_test
import "testing"
func BenchmarkExternalHeadOnly(b *testing.B) { for b.Loop() {} }
`)
	output := filepath.Join(root, "paired")
	pairTestRun(t, "compile-pair", base, head, output, fixturePackage)
	baseBin, headBin := filepath.Join(output, "base"), filepath.Join(output, "head")
	if !bytes.Equal(pairTestBinary(t, baseBin), pairTestBinary(t, headBin)) {
		t.Fatal("nested-module binaries differ with equal production and test roots")
	}
	listing := strings.Fields(pairTestRun(t, "run", head, headBin, fixturePackage, "-test.list=."))
	if strings.Join(listing, ",") != "BenchmarkProbe,BenchmarkExternal" {
		t.Fatalf("external test roots not pinned: %v", listing)
	}
}

func TestCompilePairStillMeasuresProductionChanges(t *testing.T) {
	root := benchmarkTestRoot(t)
	base := pairTestRevision(t, root, "base", "package sample\nfunc probe() []byte { return nil }\n")
	head := pairTestRevision(t, root, "head", `package sample
import "time"
func probe() []byte {
	time.Sleep(5 * time.Millisecond)
	return make([]byte, 256)
}
`)
	output := filepath.Join(root, "paired")
	pairTestRun(t, "compile-pair", base, head, output, fixturePackage)
	before := probeMetrics(t, base, filepath.Join(output, "base"))
	after := probeMetrics(t, head, filepath.Join(output, "head"))
	if after["ns/op"] < 4e6 || after["ns/op"] <= 1.05*before["ns/op"] ||
		after["B/op"] <= before["B/op"] || after["allocs/op"] <= before["allocs/op"] {
		t.Fatalf("paired build obscured a production cost: base=%v head=%v", before, after)
	}
}

func TestCompilePairReportsIncompatibleFixture(t *testing.T) {
	root := benchmarkTestRoot(t)
	base := pairTestRevision(t, root, "base", "package sample\nfunc probe() []byte { return nil }\n")
	head := pairTestRevision(t, root, "head", "package sample\nfunc replacement() []byte { return nil }\n")
	writePairTestFile(t, head, fixturePackage+"/helper_test.go", "package sample\nfunc consume() []byte { return replacement() }\n")
	var log bytes.Buffer
	output := filepath.Join(root, "paired")
	err := run([]string{"compile-pair", base, head, output, fixturePackage}, nil, &log, &log)
	if err == nil || !strings.Contains(err.Error(), "paired") || !strings.Contains(log.String(), "undefined: probe") {
		t.Fatalf("fixture/API mismatch was not reported with compiler details: %v\n%s", err, &log)
	}
	if _, err := os.Stat(filepath.Join(output, "fixtures.json")); err != nil {
		t.Fatalf("failed pairing lost fixture evidence: %v", err)
	}
}

func TestCompilePairRejectsChangedRuntimeFixture(t *testing.T) {
	root := benchmarkTestRoot(t)
	product := "package sample\nfunc probe() []byte { return nil }\n"
	base := pairTestRevision(t, root, "base", product)
	head := pairTestRevision(t, root, "head", product)
	writePairTestFile(t, base, fixturePackage+"/testdata/source.txt", "base")
	writePairTestFile(t, head, fixturePackage+"/testdata/source.txt", "head")
	output := filepath.Join(root, "paired")
	err := run([]string{"compile-pair", base, head, output, fixturePackage}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "testdata/source.txt") {
		t.Fatalf("incompatible runtime fixture was not rejected: %v", err)
	}
}

func TestCompilePairRequiresDefaultBuildFlags(t *testing.T) {
	root := benchmarkTestRoot(t)
	t.Setenv("GOFLAGS", "-ldflags=-s")
	product := "package sample\nfunc probe() []byte { return nil }\n"
	base := pairTestRevision(t, root, "base", product)
	head := pairTestRevision(t, root, "head", product)
	output := filepath.Join(root, "paired")
	err := run([]string{"compile-pair", base, head, output, fixturePackage}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "default Go build flags") {
		t.Fatalf("custom link flags were not rejected: %v", err)
	}
}

func TestCompilePairRejectsReplacingProductionEmbed(t *testing.T) {
	root := benchmarkTestRoot(t)
	product := `package sample
import _ "embed"
//go:embed helper_test.go
var productionFixture string
func probe() []byte { return []byte(productionFixture) }
`
	base := pairTestRevision(t, root, "base", product)
	head := pairTestRevision(t, root, "head", product)
	writePairTestFile(t, head, fixturePackage+"/helper_test.go", "package sample\nfunc consume() []byte { return probe() }\n// updated production embed bytes\n")
	output := filepath.Join(root, "paired")
	err := run([]string{"compile-pair", base, head, output, fixturePackage}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "production embed") {
		t.Fatalf("test overlay may replace a production embed: %v", err)
	}
}

func TestCompilePairRejectsReplacingDependencyProductionEmbed(t *testing.T) {
	root := benchmarkTestRoot(t)
	product := `package sample
import fixtures "github.com/light-speak/luxo/pkg/lux"
func probe() []byte { return []byte(fixtures.Payload) }
`
	base := pairTestRevision(t, root, "base", product)
	head := pairTestRevision(t, root, "head", product)
	for _, side := range []string{base, head} {
		writePairTestFile(t, side, "pkg/lux/fixtures.go", `package fixtures
import _ "embed"
//go:embed sample/helper_test.go
var Payload string
`)
	}
	writePairTestFile(t, head, fixturePackage+"/helper_test.go", "package sample\nfunc consume() []byte { return probe() }\n// different production payload\n")
	output := filepath.Join(root, "paired")
	err := run([]string{"compile-pair", base, head, output, fixturePackage}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "production embed") {
		t.Fatalf("test overlay may replace a dependency's production embed: %v", err)
	}
}

func TestReadPackageFixturesKeepsProductionEmbedsOfRecompiledDependencies(t *testing.T) {
	directory := filepath.Join("source", "pkg", "sample")
	rows := []map[string]any{
		{"Dir": filepath.Dir(directory), "ImportPath": "pkg [pkg/sample.test]", "ForTest": "pkg/sample",
			"EmbedFiles": []string{"sample/helper_test.go"}},
		{"Dir": directory, "ImportPath": "pkg/sample", "TestGoFiles": []string{"helper_test.go"}},
		{"Dir": directory, "ImportPath": "pkg/sample [pkg/sample.test]", "ForTest": "pkg/sample",
			"EmbedFiles": []string{"fixture_test.go"}},
	}
	var input bytes.Buffer
	for _, row := range rows {
		if err := json.NewEncoder(&input).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	fixtures, err := readPackageFixtures(directory, input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if actual := strings.Join(fixtures.ProductionEmbeds, ","); actual != "helper_test.go" {
		t.Fatalf("production embeds = %q", actual)
	}
}

func pairTestRevision(t *testing.T, root, name, product string) string {
	t.Helper()
	root = filepath.Join(root, name)
	writePairTestFile(t, root, "go.mod", "module github.com/light-speak/luxo\ngo 1.27.1\n")
	writePairTestFile(t, root, "go.work", "go 1.27.1\nuse .\n")
	writePairTestFile(t, root, fixturePackage+"/sample.go", product)
	writePairTestFile(t, root, fixturePackage+"/helper_test.go", "package sample\nfunc consume() []byte { return probe() }\n")
	writePairTestFile(t, root, fixturePackage+"/sample_bench_test.go", fixtureBenchmark)
	return root
}

func writePairTestFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func pairTestBinary(t *testing.T, directory string) []byte {
	t.Helper()
	binary := filepath.Join(directory, "pkg-lux-sample.test")
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func pairTestRun(t *testing.T, args ...string) string {
	t.Helper()
	var log bytes.Buffer
	if err := run(args, nil, &log, &log); err != nil {
		t.Fatalf("%v: %s", err, log.String())
	}
	return log.String()
}

func probeMetrics(t *testing.T, root, binaries string) map[string]float64 {
	t.Helper()
	log := pairTestRun(t, "run", root, binaries, fixturePackage,
		"-test.run=^$", "-test.bench=^BenchmarkProbe$", "-test.benchtime=1x", "-test.benchmem", "-test.cpu=2")
	for line := range strings.SplitSeq(log, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 8 || fields[0] != "BenchmarkProbe-2" {
			continue
		}
		metrics := make(map[string]float64)
		for index := 2; index < len(fields); index += 2 {
			value, err := strconv.ParseFloat(fields[index], 64)
			if err != nil {
				t.Fatal(err)
			}
			metrics[fields[index+1]] = value
		}
		if len(metrics) != 3 {
			t.Fatalf("incomplete benchmark result: %s", line)
		}
		return metrics
	}
	t.Fatalf("benchmark did not execute: %s", log)
	return nil
}
