package main

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func nativeOnlyReceipt(t *testing.T, base, head []string) (string, frozenInventoryReceipt) {
	t.Helper()
	var output bytes.Buffer
	nativeBase := frozenFixtureProcess(base...)
	if err := run(frozenFixtureArguments(t, nativeBase, frozenFixtureProcess(head...), nativeBase, nativeBase), &output); err != nil {
		t.Fatal(err)
	}
	var receipt frozenInventoryReceipt
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(benchmarkTempDir(t), "inventory.json")
	writeFixture(t, path, output.String())
	return path, receipt
}

func TestNativeOnlyPlanRetainsFamilyContext(t *testing.T) {
	base := []string{"BenchmarkLookup/before-2", "BenchmarkLookup/old[+]-2", "BenchmarkLegacy-2", "BenchmarkUnchanged-2"}
	head := []string{"BenchmarkLookup/before-2", "BenchmarkLookup/after-2", "BenchmarkNew-2", "BenchmarkUnchanged-2"}
	receipt, _ := nativeOnlyReceipt(t, base, head)
	for side, expected := range map[string]string{
		"base": frozenFixturePackage + "\t^(BenchmarkLegacy|BenchmarkLookup)$\n",
		"head": frozenFixturePackage + "\t^(BenchmarkLookup|BenchmarkNew)$\n",
	} {
		var output bytes.Buffer
		if err := run([]string{"--native-only-plan", receipt, side}, &output); err != nil {
			t.Fatal(err)
		}
		if output.String() != expected {
			t.Errorf("%s plan: got %q, want %q", side, output.String(), expected)
		}
	}
}

func nativeOnlyProcess(names ...string) string {
	return strings.ReplaceAll(frozenFixtureProcess(names...), " 1 12 ns/op", " 100 12 ns/op")
}

func verifyNativeOnlyFixture(t *testing.T, receipt, side, input string) error {
	t.Helper()
	output := filepath.Join(benchmarkTempDir(t), "normalized.log")
	writeFixture(t, output, input)
	return run([]string{"--verify-native-only", receipt, side, output}, io.Discard)
}

func TestVerifyNativeOnlySamples(t *testing.T) {
	before := "BenchmarkLookup/before-2"
	added := "BenchmarkLookup/new[+]-2"
	removed := "BenchmarkLookup/removed-2"
	receipt, _ := nativeOnlyReceipt(t, []string{before, removed, "BenchmarkUnaffected-2"},
		[]string{before, added, "BenchmarkUnaffected-2"})
	for side, names := range map[string][]string{"base": {before, removed}, "head": {before, added}} {
		t.Run(side, func(t *testing.T) {
			raw := strings.ReplaceAll(strings.Repeat(nativeOnlyProcess(names...), 10),
				"pkg: github.com/light-speak/luxo/pkg/lux/cache", "pkg: github.com/light-speak/luxo/pkg/lux/cache/redis")
			raw = strings.ReplaceAll(raw, "cpu: test runner", "cpu: sample worker")
			var normalized bytes.Buffer
			if err := normalize(strings.NewReader(raw), &normalized); err != nil {
				t.Fatal(err)
			}
			if err := verifyNativeOnlyFixture(t, receipt, side, normalized.String()); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, input := range map[string]string{
		"missing sample": strings.Repeat(nativeOnlyProcess(before, added), 9),
		"mixed CPU": strings.Replace(strings.Repeat(nativeOnlyProcess(before, added), 10), "cpu: test runner",
			"cpu: other worker", 1),
		"missing CPU": strings.ReplaceAll(strings.Repeat(nativeOnlyProcess(before, added), 10), "cpu: test runner\n", ""),
		"wrong OS":    strings.ReplaceAll(strings.Repeat(nativeOnlyProcess(before, added), 10), "goos: linux", "goos: darwin"),
		"orphan sample": strings.Repeat(nativeOnlyProcess(before, added), 9) +
			strings.Replace(nativeOnlyProcess(before, added), "pkg: github.com/light-speak/luxo/"+frozenFixturePackage+"\n", "", 1),
		"extra sample":    strings.Repeat(nativeOnlyProcess(before, added), 11),
		"missing sibling": strings.Repeat(nativeOnlyProcess(added), 10),
		"other-side leaf": strings.Repeat(nativeOnlyProcess(before, removed), 10),
		"extra leaf":      strings.Repeat(nativeOnlyProcess(before, added, "BenchmarkOther-2"), 10),
		"missing metric":  strings.Replace(strings.Repeat(nativeOnlyProcess(before, added), 10), " 0 B/op ", " ", 1),
		"duplicate metric": strings.Replace(strings.Repeat(nativeOnlyProcess(before, added), 10),
			" 0 allocs/op", " 0 allocs/op 0 allocs/op", 1),
		"no final PASS": strings.TrimSuffix(strings.Repeat(nativeOnlyProcess(before, added), 10), "PASS\n"),
		"invalid sample": strings.Replace(strings.Repeat(nativeOnlyProcess(before, added), 10),
			" 100 12 ns/op", " 0 12 ns/op", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifyNativeOnlyFixture(t, receipt, "head", input); err == nil {
				t.Fatal("invalid native-only sample was accepted")
			}
		})
	}
}

func TestNativeOnlyPlanWithoutSamples(t *testing.T) {
	receipt, _ := nativeOnlyReceipt(t, []string{"BenchmarkProbe-2"}, []string{"BenchmarkProbe-2", "BenchmarkNew-2"})
	var output bytes.Buffer
	if err := run([]string{"--native-only-plan", receipt, "base"}, &output); err != nil || output.Len() != 0 {
		t.Fatalf("empty side produced data: %q, %v", output.String(), err)
	}
	if err := verifyNativeOnlyFixture(t, receipt, "base", strings.Repeat(nativeOnlyProcess("BenchmarkProbe-2"), 10)); err == nil {
		t.Fatal("an empty side accepted manufactured evidence")
	}
	if err := run([]string{"--native-only-plan", receipt, "candidate"}, io.Discard); err == nil {
		t.Fatal("invalid side accepted")
	}
}

func TestNativeOnlyPlanRejectsInconsistentReceipt(t *testing.T) {
	_, valid := nativeOnlyReceipt(t, []string{"BenchmarkProbe-2", "BenchmarkRemoved-2"}, []string{"BenchmarkProbe-2", "BenchmarkNew-2"})
	tests := map[string]func(*frozenInventoryReceipt){
		"undeclared native leaf": func(receipt *frozenInventoryReceipt) {
			receipt.NativeBaseOnly = nil
		},
		"repeated native-only leaf": func(receipt *frozenInventoryReceipt) {
			receipt.NativeBaseOnly = append(receipt.NativeBaseOnly, receipt.NativeBaseOnly[0])
		},
		"unexpected paired leaf": func(receipt *frozenInventoryReceipt) {
			receipt.NativeHeadOnly = append(receipt.NativeHeadOnly, sampleKey{frozenFixturePackage, "BenchmarkProbe-2"})
		},
		"made-up leaf": func(receipt *frozenInventoryReceipt) {
			receipt.NativeHeadOnly = []sampleKey{{frozenFixturePackage, "BenchmarkUnrecorded-2"}}
		},
		"missing native side": func(receipt *frozenInventoryReceipt) {
			receipt.Native.Head = nil
		},
		"missing discovery metadata": func(receipt *frozenInventoryReceipt) {
			receipt.Platform = nil
		},
		"repeated historical key": func(receipt *frozenInventoryReceipt) {
			receipt.Frozen.Base = append(receipt.Frozen.Base, receipt.Frozen.Base[0])
		},
		"removed frozen baseline": func(receipt *frozenInventoryReceipt) {
			receipt.Frozen.Head = receipt.Frozen.Head[:1]
		},
		"duplicate native key": func(receipt *frozenInventoryReceipt) {
			receipt.Native.Head = append(receipt.Native.Head, receipt.Native.Head[0])
		},
		"other package": func(receipt *frozenInventoryReceipt) {
			receipt.Native.Head = append(receipt.Native.Head, sampleKey{"pkg/lux/other", "BenchmarkNotMember-2"})
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			receipt := valid
			change(&receipt)
			raw, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(benchmarkTempDir(t), "receipt.json")
			writeFixture(t, path, string(raw))
			if err := run([]string{"--native-only-plan", path, "head"}, io.Discard); err == nil {
				t.Fatal("inconsistent inventory admitted")
			}
		})
	}
}

func TestNativeOnlyFileAndCommandErrors(t *testing.T) {
	dir := benchmarkTempDir(t)
	invalid := filepath.Join(dir, "malformed.json")
	writeFixture(t, invalid, "{")
	missing := filepath.Join(dir, "absent")
	for _, args := range [][]string{
		{"--native-only-plan"},
		{"--verify-native-only"},
		{"--native-only-plan", missing, "base"},
		{"--native-only-plan", invalid, "head"},
		{"--verify-native-only", invalid, "head", missing},
	} {
		var output bytes.Buffer
		if err := run(args, &output); err == nil || output.Len() != 0 {
			t.Errorf("invalid input produced output: %v, %q, %v", args, output.String(), err)
		}
	}

	receipt, _ := nativeOnlyReceipt(t, []string{"BenchmarkRead-2"}, []string{"BenchmarkRead-2", "BenchmarkWrite-2"})
	if err := run([]string{"--native-only-plan", receipt, "head"}, failingIO{}); err == nil {
		t.Fatal("plan output failure was ignored")
	}
	if err := run([]string{"--verify-native-only", receipt, "head", missing}, io.Discard); err == nil {
		t.Fatal("accepted a missing sample file")
	}
}
