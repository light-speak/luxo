package main

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const frozenFixturePackage = "pkg/lux/cache"

func frozenFixtureProcess(names ...string) string {
	output := "goos: linux\ngoarch: amd64\npkg: github.com/light-speak/luxo/" + frozenFixturePackage + "\ncpu: test runner\n"
	for _, name := range names {
		output += name + " 1 12 ns/op 0 B/op 0 allocs/op\n"
	}
	return output + "PASS\n"
}

func frozenFixtureArguments(t *testing.T, inventories ...string) []string {
	t.Helper()
	dir := benchmarkTempDir(t)
	args := []string{"--verify-frozen-inventory", frozenFixturePackage}
	for i, name := range []string{"native-base", "native-head", "frozen-base", "frozen-head"} {
		path := filepath.Join(dir, name+".log")
		writeFixture(t, path, inventories[i])
		args = append(args, path)
	}
	return args
}

func TestFrozenInventoryRetainsHistoricalAndNativeOnlyLeaves(t *testing.T) {
	// The native inventories can contain several physical packages normalized
	// to one historical package. A removed native leaf remains in the gate.
	common := "BenchmarkLookup/common[+]-2"
	oldOnly := "BenchmarkRemoved/old-2"
	newOnly := "BenchmarkLookup/new-2"
	base := frozenFixtureProcess(common) + frozenFixtureProcess(oldOnly)
	head := frozenFixtureProcess(common, newOnly)
	var output bytes.Buffer
	if err := run(frozenFixtureArguments(t, base, head, base, base), &output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Native                         expectedSamples
		Frozen                         expectedSamples
		NativeBaseOnly, NativeHeadOnly []sampleKey
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	old := []sampleKey{{frozenFixturePackage, common}, {frozenFixturePackage, oldOnly}}
	current := []sampleKey{{frozenFixturePackage, common}, {frozenFixturePackage, newOnly}}
	if !reflect.DeepEqual(result.Native, expectedSamples{Base: old, Head: current}) ||
		!reflect.DeepEqual(result.Frozen, expectedSamples{Base: old, Head: old}) ||
		!reflect.DeepEqual(result.NativeBaseOnly, old[1:]) ||
		!reflect.DeepEqual(result.NativeHeadOnly, current[1:]) {
		t.Fatalf("incomplete native or frozen scope: %+v", result)
	}
}

func TestFrozenInventoryRejectsScopeMismatch(t *testing.T) {
	const old = "BenchmarkRead/old-2"
	const common = "BenchmarkRead/common-2"
	const newLeaf = "BenchmarkRead/new-2"
	baseline := frozenFixtureProcess(common, old)
	head := frozenFixtureProcess(common, newLeaf)
	for _, sample := range []struct {
		name, frozenBase, frozenHead, want string
	}{
		{"both sides silently drop old leaf", frozenFixtureProcess(common), frozenFixtureProcess(common), old},
		{"head drops historical leaf", baseline, frozenFixtureProcess(common), old},
		{"base has an extra leaf", frozenFixtureProcess(common, old, newLeaf), baseline, newLeaf},
		{"head has extra new leaf", baseline, frozenFixtureProcess(common, old, newLeaf), newLeaf},
		{"wrong package identity", baseline, strings.Replace(baseline, frozenFixturePackage, "pkg/lux/storage", 1), "benchmark package"},
		{"different machine", baseline, strings.Replace(baseline, "test runner", "other runner", 1), "benchmark platforms"},
		{"missing iterations", baseline, strings.Replace(baseline, " 1 ", " 3 ", 1), "one iteration"},
		{"incomplete metrics", baseline, strings.Replace(baseline, " 0 allocs/op", "", 1), "allocs/op"},
		{"no native completion", baseline, strings.TrimSuffix(baseline, "PASS\n"), "PASS"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var output bytes.Buffer
			err := run(frozenFixtureArguments(t, baseline, head, sample.frozenBase, sample.frozenHead), &output)
			if err == nil || !strings.Contains(err.Error(), sample.want) || output.Len() != 0 {
				t.Fatalf("expected scope error mentioning %q, got %v output=%q", sample.want, err, output.String())
			}
		})
	}
}

func TestFrozenInventoryDoesNotInventNativeEvidence(t *testing.T) {
	only := frozenFixtureProcess("BenchmarkLookUp-2")
	if err := run(frozenFixtureArguments(t, only, "PASS\n", only, only), io.Discard); err == nil {
		t.Fatal("accepted a missing native benchmark inventory")
	}
	if err := run([]string{"--verify-frozen-inventory"}, io.Discard); err == nil {
		t.Fatal("accepted incomplete frozen inventory arguments")
	}
}

func TestFrozenInventoryRequiresRecordedRoleMetadata(t *testing.T) {
	first := frozenFixtureProcess("BenchmarkLookup/first-2")
	second := frozenFixtureProcess("BenchmarkLookup/second-2")
	for _, sample := range []struct{ raw, want string }{
		{strings.TrimPrefix(first, "goos: linux\n"), "missing benchmark platform"},
		{first + strings.Replace(second, "goarch: amd64", "goarch: arm64", 1), "mixed benchmark platform"},
		{strings.Replace(first, "pkg: github.com/light-speak/luxo/"+frozenFixturePackage+"\n", "", 1),
			"no benchmark package identity"},
	} {
		err := run(frozenFixtureArguments(t, first, first, sample.raw, first), io.Discard)
		if err == nil || !strings.Contains(err.Error(), sample.want) {
			t.Errorf("expected %q, got %v", sample.want, err)
		}
	}
	args := frozenFixtureArguments(t, first, first, first, first)
	args[3] += ".missing"
	if err := run(args, io.Discard); err == nil || !strings.Contains(err.Error(), "native head inventory") {
		t.Fatalf("missing native evidence returned %v", err)
	}
}
