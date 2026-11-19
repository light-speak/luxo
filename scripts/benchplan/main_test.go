package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeMovedBenchmarkIdentity(t *testing.T) {
	input := "pkg: github.com/light-speak/luxo/pkg/lux/cache/redis\nBenchmarkRedis-2 1 12 ns/op 0 B/op 0 allocs/op\nPASS\n"
	var out strings.Builder
	if err := normalize(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "pkg: github.com/light-speak/luxo/pkg/lux/cache\n") || !strings.Contains(out.String(), "BenchmarkRedis-2 1 12 ns/op") {
		t.Fatal(out.String())
	}
}

func TestReadInventory(t *testing.T) {
	input := "goos: linux\npkg: example\nBenchmarkRead/small-2 1 12.0 ns/op 0 B/op 0 allocs/op\nBenchmarkRead/large-2 1 34 ns/op 8 B/op 1 allocs/op\nPASS\n"
	got, err := readInventory(strings.NewReader(input))
	if err != nil || len(got["BenchmarkRead"]) != 2 {
		t.Fatalf("inventory=%v err=%v", got, err)
	}
	for _, bad := range []string{
		"PASS\n", strings.ReplaceAll(input, "PASS", "FAIL"),
		strings.ReplaceAll(input, "PASS\n", ""),
		strings.ReplaceAll(input, "BenchmarkRead/large-2", "BenchmarkRead/small-2"),
		strings.ReplaceAll(input, " 1 34 ", " 0 34 "),
		strings.ReplaceAll(input, "BenchmarkRead/large-2", "BenchmarkRead/large-8"),
	} {
		if _, err := readInventory(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted invalid inventory %q", bad)
		}
	}
}

func TestInventoryAcceptsZeroDurationSingleIteration(t *testing.T) {
	// Go omits ns/op when a one-iteration discovery completes within clock resolution.
	_, err := readInventory(strings.NewReader("BenchmarkFast-2 1 0 B/op 0 allocs/op\nPASS\n"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestPlanPreservesPairedAndUnpairedBenchmarks(t *testing.T) {
	groups := []benchGroup{
		{Package: "pkg/a", Name: "BenchmarkA", Base: 2, Head: 3, Common: true},
		{Package: "pkg/b", Name: "BenchmarkB", Base: 1, Head: 1, Common: true},
		{Package: "pkg/a", Name: "BenchmarkNew", Head: 120},
		{Package: "pkg/b", Name: "BenchmarkRemoved", Base: 8},
		{Package: "pkg/b", Name: "BenchmarkOther", Head: 70},
	}
	shards, err := makePlan(groups, 8)
	if err != nil || len(shards) != 2 {
		t.Fatalf("shards=%v err=%v", shards, err)
	}
	seen := map[string]int{}
	for _, shard := range shards {
		paired := false
		for _, group := range shard.Groups {
			seen[group.Package+"/"+group.Name]++
			paired = paired || group.Common
		}
		if !paired {
			t.Fatal("shard has no baseline comparison")
		}
	}
	for _, group := range groups {
		if seen[group.Package+"/"+group.Name] != 1 {
			t.Fatalf("missing or repeated group: %+v", group)
		}
	}
	for i, j := 0, len(groups)-1; i < j; i, j = i+1, j-1 {
		groups[i], groups[j] = groups[j], groups[i]
	}
	reordered, err := makePlan(groups, 8)
	if err != nil || !reflect.DeepEqual(shards, reordered) {
		t.Fatalf("plan depends on discovery order: %v", err)
	}
}

func TestPlanRejectsMissingBaselineAndInvalidCapacity(t *testing.T) {
	if compareGroups(benchGroup{Common: true}, benchGroup{}) >= 0 || compareGroups(benchGroup{}, benchGroup{Common: true}) <= 0 {
		t.Fatal("paired groups must seed every batch before new groups")
	}
	if compareGroups(benchGroup{Name: "BenchmarkA"}, benchGroup{Name: "BenchmarkB"}) >= 0 {
		t.Fatal("equal-weight groups in the same package must be ordered by name")
	}
	for _, limit := range []int{0, -1, 257} {
		if _, err := makePlan([]benchGroup{{Common: true}}, limit); err == nil {
			t.Fatalf("accepted limit %d", limit)
		}
	}
	if _, err := makePlan([]benchGroup{{Head: 10}}, 8); err == nil {
		t.Fatal("new benchmarks alone must not masquerade as a baseline comparison")
	}
}

func TestJoinInventories(t *testing.T) {
	base := inventory{"BenchmarkA": {"BenchmarkA/old-2": {}}, "BenchmarkShared": {"BenchmarkShared-2": {}}}
	head := inventory{"BenchmarkA": {"BenchmarkA/new-2": {}}, "BenchmarkShared": {"BenchmarkShared-2": {}}, "BenchmarkNew": {"BenchmarkNew-2": {}}}
	groups := joinInventories("pkg/a", base, head)
	if len(groups) != 3 {
		t.Fatalf("groups=%v", groups)
	}
	for _, group := range groups {
		if group.Common != (group.Name == "BenchmarkShared") {
			t.Fatalf("only identical leaf names can establish paired evidence: %+v", group)
		}
	}
}

func TestSampleVerificationRequiresEveryLeafTenTimes(t *testing.T) {
	expected := []sampleKey{{Package: "pkg/a", Name: "BenchmarkRead/small-2"}}
	valid := "pkg: github.com/light-speak/luxo/pkg/a\n" + strings.Repeat("BenchmarkRead/small-2 10 12 ns/op 0 B/op 0 allocs/op\n", 10) + "PASS\n"
	if err := verifySamples(strings.NewReader(valid), expected); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(valid, "BenchmarkRead/small-2 10 12 ns/op 0 B/op 0 allocs/op\n", "", 1),
		strings.ReplaceAll(valid, "BenchmarkRead/small-2", "BenchmarkRead/other-2"),
		strings.ReplaceAll(valid, "pkg/a", "pkg/b"),
		strings.ReplaceAll(valid, "PASS", "FAIL"),
		"PASS\n", strings.ReplaceAll(valid, "pkg: github.com/light-speak/luxo/pkg/a\n", ""),
	} {
		if err := verifySamples(strings.NewReader(bad), expected); err == nil {
			t.Errorf("accepted incomplete or mismatched evidence %q", bad)
		}
	}
}

func TestSampleVerificationRequiresEveryStandardMetric(t *testing.T) {
	expected := []sampleKey{{Package: "pkg/a", Name: "BenchmarkRead-2"}}
	header := "pkg: github.com/light-speak/luxo/pkg/a\n"
	valid := "BenchmarkRead-2 10 12 ns/op 0 B/op 0 allocs/op\n"
	for _, tail := range []string{
		"10 0 B/op 0 allocs/op",
		"10 12 ns/op 0 allocs/op",
		"10 12 ns/op 0 B/op",
		"10 12 ns/op 13 ns/op 0 B/op 0 allocs/op",
		"10 12 ns/op 0 B/op 1 B/op 0 allocs/op",
		"10 12 ns/op 0 B/op 0 allocs/op 1 allocs/op",
	} {
		raw := header + strings.Repeat(valid, 9) + "BenchmarkRead-2 " + tail + "\nPASS\n"
		if err := verifySamples(strings.NewReader(raw), expected); err == nil {
			t.Errorf("accepted missing or duplicate metric: %s", tail)
		}
	}
	for _, tail := range []string{
		"10 12 ns/op 0 B/op 0 allocs/op 42 MB/s",
		"10 0 B/op 0 allocs/op 12 ns/op",
	} {
		raw := header + strings.Repeat("BenchmarkRead-2 "+tail+"\n", 10) + "PASS\n"
		if err := verifySamples(strings.NewReader(raw), expected); err != nil {
			t.Fatalf("rejected complete metrics with custom units or order: %s: %v", tail, err)
		}
	}
}

func TestBenchmarkEvidenceRejectsPackageFailureSummaries(t *testing.T) {
	expected := []sampleKey{{Package: "pkg/a", Name: "BenchmarkRead/small-2"}}
	measurement := "BenchmarkRead/small-2 10 12 ns/op 0 B/op 0 allocs/op\n"
	for _, failure := range []string{"FAIL\tgithub.com/light-speak/luxo/pkg/a\t[build failed]", "FAIL github.com/light-speak/luxo/pkg/a 0.010s"} {
		t.Run(failure, func(t *testing.T) {
			one := "pkg: github.com/light-speak/luxo/pkg/a\n" + measurement + "PASS\n" + failure + "\n"
			if _, err := readInventory(strings.NewReader(one)); err == nil {
				t.Error("inventory accepted package failure")
			}
			if err := normalize(strings.NewReader(one), io.Discard); err == nil {
				t.Error("normalization accepted package failure")
			}
			ten := "pkg: github.com/light-speak/luxo/pkg/a\n" + strings.Repeat(measurement, 10) + "PASS\n" + failure + "\n"
			if err := verifySamples(strings.NewReader(ten), expected); err == nil {
				t.Error("sample verification accepted package failure")
			}
		})
	}
}

func TestBenchmarkFailureMarkerBoundaries(t *testing.T) {
	for _, line := range []string{"FAIL", "FAIL\tpkg/a", "FAIL pkg/a", "--- FAIL: BenchmarkA (0.01s)"} {
		if !isBenchmarkFailure(line) {
			t.Errorf("missed failure: %q", line)
		}
	}
	for _, line := range []string{"PASS", "FAILURE diagnostic example", "log: FAIL pkg/a", "BenchmarkFAIL-2 1 1 ns/op"} {
		if isBenchmarkFailure(line) {
			t.Errorf("misclassified ordinary log: %q", line)
		}
	}
}

func TestBenchmarkEvidenceRequiresCompletedRuns(t *testing.T) {
	header := "pkg: github.com/light-speak/luxo/pkg/a\n"
	row := "BenchmarkRead-2 10 12 ns/op 0 B/op 0 allocs/op\n"
	expected := []sampleKey{{Package: "pkg/a", Name: "BenchmarkRead-2"}}
	for _, raw := range []string{
		header + strings.Repeat(row, 10),
		header + strings.Repeat(row, 5) + "PASS\n" + header + strings.Repeat(row, 5),
		header + strings.Repeat(row, 5) + header + strings.Repeat(row, 5) + "PASS\n",
	} {
		if err := verifySamples(strings.NewReader(raw), expected); err == nil {
			t.Errorf("accepted unfinished samples: %q", raw)
		}
	}
	for _, raw := range []string{
		header + row + "PASS\n" + "BenchmarkOther-2 10 12 ns/op\n",
		header + row + header + "BenchmarkOther-2 10 12 ns/op\nPASS\n",
	} {
		if _, err := readInventory(strings.NewReader(raw)); err == nil {
			t.Errorf("accepted unfinished inventory: %q", raw)
		}
	}
	valid := strings.Repeat(header+row+"PASS\n", 10)
	if err := verifySamples(strings.NewReader(valid), expected); err != nil {
		t.Fatal(err)
	}
}
