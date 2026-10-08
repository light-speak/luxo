package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
)

func TestApprovalOptionsFailClosed(t *testing.T) {
	for _, args := range [][]string{
		{"--unknown"}, {"--approved-costs", "missing"},
		{"--base-sha", approvedBase},
		{"--approved-costs", "missing", "--base-sha", strings.Repeat("z", 40)},
		{"--approved-costs", "missing", "--base-sha", approvedBase},
	} {
		if _, _, err := parseGateOptions(args); err == nil {
			t.Errorf("invalid options accepted: %v", args)
		}
	}
}

func TestApprovalDuplicateRecords(t *testing.T) {
	var policy costPolicy
	if err := json.Unmarshal([]byte(approvedPolicy), &policy); err != nil {
		t.Fatal(err)
	}
	first := policy.Approvals[0]
	for _, sameID := range []bool{true, false} {
		duplicate := first
		if !sameID {
			duplicate.ID = "another-approval"
		}
		policy.Approvals = []approvedCost{first, duplicate}
		if _, err := policy.forBaseline(approvedBase); err == nil {
			t.Fatal("duplicate approval accepted")
		}
	}
}

func TestApprovalRequiresCompleteMeasurementsAndAuditOutput(t *testing.T) {
	policy := writeRunInput(t, approvedPolicy)
	complete := compilerCostCSV("~", 256, 2)
	for _, unit := range []string{"B/op", "allocs/op"} {
		incomplete := strings.Replace(complete, ","+unit+",", ",unsupported,", 1)
		path := writeRunInput(t, incomplete)
		err := run([]string{"--approved-costs", policy, "--base-sha", approvedBase, path, path}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "requires both") {
			t.Fatalf("missing %s error = %v", unit, err)
		}
	}
	path := writeRunInput(t, complete)
	if err := run([]string{"--approved-costs", policy, "--base-sha", approvedBase, path, path}, errorWriter{}); err == nil || !strings.Contains(err.Error(), "write failure") {
		t.Fatalf("audit output error = %v", err)
	}
}

func TestMeasurementsRejectInvalidValuesAndDuplicates(t *testing.T) {
	for _, value := range []string{"-1", "NaN", "+Inf", "invalid"} {
		for _, pair := range [][2]string{{value, "10"}, {"10", value}} {
			if _, err := parseMeasurement(pair[0], pair[1]); err == nil {
				t.Errorf("invalid measurements accepted: %v", pair)
			}
		}
	}
	for _, delta := range []string{"NaN%", "+Inf%"} {
		if _, _, err := parseDelta(delta); err == nil {
			t.Errorf("invalid delta accepted: %s", delta)
		}
	}
	input := benchstatCSV("~", "~", "n=10")
	for _, invalid := range []string{
		strings.Replace(input, ",10,0%,11,0%,", ",NaN,0%,11,0%,", 1),
		input + "\n" + benchstatTable("allocs/op", "~", "n=10"),
	} {
		if _, err := evaluate(strings.NewReader(invalid)); err == nil {
			t.Fatal("invalid CSV accepted")
		}
	}
}

const approvedBase = "dbd269d586a71739b69dd6acb1a26b85f50483d0"

func TestRepositoryApprovedCostBudget(t *testing.T) {
	costs, err := loadApprovedCosts("approved-costs.json", approvedBase)
	if err != nil {
		t.Fatal(err)
	}
	cost := costs[costKey{"github.com/light-speak/luxo/pkg/semantic", "AnalyzeDemoFile-2"}]
	if len(costs) != 17 || cost.ID != "beta-compiler-demo-v1-validation" || cost.MaxBytes != 11280 || cost.MaxAllocs != 116 {
		t.Fatalf("unexpected reviewed repository budget: %+v", costs)
	}
	for _, test := range []struct {
		name         string
		primaryBytes float64
		confirmBytes float64
		allocations  float64
		timeDelta    string
		base         string
		pass         bool
	}{
		{"previous smaller cost", 258, 253.5, 2, "~", approvedBase, true},
		{"measured groups", 11262.5, 11261.5, 116, "~", approvedBase, true},
		{"exact ceiling", 11280, 11280, 116, "~", approvedBase, true},
		{"fraction over ceiling", 11280.5, 11280.5, 116, "~", approvedBase, false},
		{"primary over ceiling", 11281, 11261.5, 116, "~", approvedBase, false},
		{"confirmation over ceiling", 11262.5, 11281, 116, "~", approvedBase, false},
		{"extra allocation", 11262.5, 11261.5, 117, "~", approvedBase, false},
		{"time regression", 11262.5, 11261.5, 116, "+6%", approvedBase, false},
		{"expired baseline", 11262.5, 11261.5, 116, "~", strings.Repeat("a", 40), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			primary := writeRunInput(t, compilerCostCSV(test.timeDelta, test.primaryBytes, test.allocations))
			confirmation := writeRunInput(t, compilerCostCSV(test.timeDelta, test.confirmBytes, test.allocations))
			err := run([]string{"--approved-costs", "approved-costs.json", "--base-sha", test.base, primary, confirmation}, io.Discard)
			if (err == nil) != test.pass {
				t.Fatalf("gate error = %v, want pass=%t", err, test.pass)
			}
		})
	}
}

func TestColdCompilationBudgets(t *testing.T) {
	for _, sample := range []struct {
		pkg, name                       string
		nanoseconds, bytes, allocations int64
	}{
		{"codegen", "Generate-2", 4850000, 1053545, 22002},
		{"codegen", "GenerateModelFile-2", 2237, 3649, 5},
		{"lockfile", "Update-2", 12936, 9744, 44},
		{"lockfile", "SaveLoad-2", 0, 19355, 66},
		{"semantic", "Analyze-2", 5791, 4184, 17},
		{"semantic", "AnalyzeLargeFile-2", 0, 64, 0},
	} {
		t.Run(sample.pkg+"/"+sample.name, func(t *testing.T) {
			costs, err := loadApprovedCosts("approved-costs.json", approvedBase)
			if err != nil {
				t.Fatal(err)
			}
			cost, ok := costs[costKey{"github.com/light-speak/luxo/pkg/" + sample.pkg, sample.name}]
			if !ok || cost.MaxNanoseconds != float64(sample.nanoseconds) || cost.MaxBytes != sample.bytes || cost.MaxAllocs != sample.allocations {
				t.Fatalf("incorrect cold compilation budget: %+v", cost)
			}
			testColdCompilationBudget(t, cost)
		})
	}
}

func TestRuntimeCombinationBudgets(t *testing.T) {
	costs, err := loadApprovedCosts("approved-costs.json", approvedBase)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		pkg, name string
		ns        float64
	}{
		{"api", "ParseBinaryRequestPaginatedDefaults-2", 11.435},
		{"codec", "ReadStringVsArena/Arena-2", 35.285},
		{"codec", "ReadStringVsArena/NoArena-2", 37.320},
		{"codec", "ColumnarStringArena-2", 659.400},
		{"codec", "ColumnarStringNoArena-2", 721.250},
		{"codec", "RowWrite10-2", 53.700},
		{"codec", "EncodeEvent-2", 1.005},
		{"codec", "DecodeEvent-2", 6.355},
	} {
		t.Run(sample.name, func(t *testing.T) {
			cost, ok := costs[costKey{"github.com/light-speak/luxo/pkg/lux/" + sample.pkg, sample.name}]
			if !ok || float64(cost.MaxNanoseconds) != sample.ns || cost.MaxBytes != 0 || cost.MaxAllocs != 0 {
				t.Fatalf("incorrect runtime budget: %+v", cost)
			}
			testColdCompilationBudget(t, cost)
		})
	}
}

func TestEventSnapshotBudget(t *testing.T) {
	costs, err := loadApprovedCosts("approved-costs.json", approvedBase)
	if err != nil {
		t.Fatal(err)
	}
	cost, ok := costs[costKey{"github.com/light-speak/luxo/pkg/lux/event", "ChanBusEmit-2"}]
	if !ok || cost.MaxNanoseconds != 82.105 || cost.MaxBytes != 72 || cost.MaxAllocs != 4 {
		t.Fatalf("incorrect event snapshot budget: %+v", cost)
	}
	testColdCompilationBudget(t, cost)
}

func TestRuntimeTimeBudgetsFailClosed(t *testing.T) {
	cost := approvedCost{ID: "runtime", BaseSHA: approvedBase, Package: "github.com/light-speak/luxo/pkg/lux/codec", Benchmark: "EncodeEvent-2", MaxNanoseconds: 1.005, Reason: "test"}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.001} {
		invalid := cost
		invalid.MaxNanoseconds = value
		if invalid.validate() == nil {
			t.Fatalf("accepted invalid limit: %v", value)
		}
	}
	for _, key := range []costKey{
		{"github.com/light-speak/luxo/pkg/lux/codec", "ColumnarWrite10-2"},
		{"github.com/light-speak/luxo/pkg/lux/api", "RouterObservabilityDisabled-2"},
		{"github.com/light-speak/luxo/pkg/lux/event", "ChanBusEmitUnreviewed-2"},
	} {
		invalid := cost
		invalid.Package, invalid.Benchmark = key.Package, key.Benchmark
		if invalid.validate() == nil {
			t.Fatalf("accepted unreviewed sample: %+v", key)
		}
	}
}

func TestTimeBudgetOnlyAllowsOneRepresentationStep(t *testing.T) {
	cost := approvedCost{Package: "sample", Benchmark: "sample", MaxNanoseconds: 35.285}
	group := coldCostMeasurements(cost)
	key := comparisonKey{cost.Package, cost.Benchmark, "sec/op"}
	value := group.Measurements[key]
	value.Base = 3.8915e-8
	ceiling := value.Base + cost.MaxNanoseconds/1e9
	for _, test := range []struct {
		head float64
		want bool
	}{
		{ceiling, true},
		{math.Nextafter(ceiling, math.Inf(1)), true},
		{math.Nextafter(math.Nextafter(ceiling, math.Inf(1)), math.Inf(1)), false},
		{ceiling + 1e-12, false},
	} {
		value.Head = test.head
		group.Measurements[key] = value
		accepted, err := cost.accepts(group, group)
		if err != nil || accepted != test.want {
			t.Fatalf("head %.20g: accepted=%t, err=%v", test.head, accepted, err)
		}
	}
}

func testColdCompilationBudget(t *testing.T, cost approvedCost) {
	t.Helper()
	for _, test := range []struct {
		name, unit   string
		increase     float64
		confirmation bool
	}{
		{"ceiling", "", 0, false},
		{"primary time", "sec/op", 1e-9, false},
		{"confirmation time", "sec/op", 1e-9, true},
		{"primary bytes", "B/op", 0.5, false},
		{"confirmation bytes", "B/op", 0.5, true},
		{"allocations", "allocs/op", 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			primary, confirmation := coldCostMeasurements(cost), coldCostMeasurements(cost)
			group := primary
			if test.confirmation {
				group = confirmation
			}
			if test.unit != "" {
				key := comparisonKey{cost.Package, cost.Benchmark, test.unit}
				value := group.Measurements[key]
				value.Head += test.increase
				group.Measurements[key] = value
			}
			result := evaluation{Regressions: []regression{{Key: comparisonKey{cost.Package, cost.Benchmark, "B/op"}}}}
			if test.unit == "sec/op" || cost.MaxNanoseconds > 0 {
				result.Regressions = append(result.Regressions, regression{Key: comparisonKey{cost.Package, cost.Benchmark, "sec/op"}})
			}
			err := applyApprovedCosts(&result, primary, confirmation, map[costKey]approvedCost{{cost.Package, cost.Benchmark}: cost}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if (len(result.Regressions) == 0) != (test.unit == "") {
				t.Fatalf("unexpected remaining regressions: %+v", result.Regressions)
			}
		})
	}
}

func coldCostMeasurements(cost approvedCost) evaluation {
	return evaluation{Measurements: map[comparisonKey]measurement{
		{cost.Package, cost.Benchmark, "sec/op"}:    {Base: 1e-6, Head: 1e-6 + float64(cost.MaxNanoseconds)/1e9},
		{cost.Package, cost.Benchmark, "B/op"}:      {Base: 100, Head: 100 + float64(cost.MaxBytes)},
		{cost.Package, cost.Benchmark, "allocs/op"}: {Base: 10, Head: 10 + float64(cost.MaxAllocs)},
	}}
}

func TestTimeApprovalEligibilityIsExact(t *testing.T) {
	for _, key := range []costKey{
		{"github.com/light-speak/luxo/pkg/lux/auth", "VerifyUncached-2"},
		{"github.com/light-speak/luxo/pkg/codegen", "Generate-4"},
		{"github.com/light-speak/luxo/pkg/codegen", "LogicalExpression/fallibleAnd-2"},
		{"github.com/light-speak/luxo/pkg/semantic", "AnalyzeDemoFile-2"},
		{"github.com/light-speak/luxo/pkg/semantic", "AnalyzeLargeFile-2"},
		{"github.com/light-speak/luxo/pkg/lockfile", "SaveLoad-2"},
		{"github.com/light-speak/luxo/pkg/lockfile", "Load-2"},
	} {
		cost := approvedCost{ID: "unreviewed-time", BaseSHA: approvedBase, Package: key.Package, Benchmark: key.Benchmark, MaxBytes: 1, MaxNanoseconds: 1, Reason: "test"}
		if err := cost.validate(); err == nil {
			t.Errorf("unreviewed time sample accepted: %+v", key)
		}
	}
	cost := approvedCost{ID: "negative-time", BaseSHA: approvedBase, Package: "github.com/light-speak/luxo/pkg/codegen", Benchmark: "Generate-2", MaxBytes: 1, MaxNanoseconds: -1, Reason: "test"}
	if err := cost.validate(); err == nil {
		t.Fatal("negative time limit accepted")
	}
}

func TestTimeApprovalRequiresBothTimeMeasurements(t *testing.T) {
	cost := approvedCost{ID: "time", BaseSHA: approvedBase, Package: "github.com/light-speak/luxo/pkg/codegen", Benchmark: "Generate-2", MaxBytes: 1, MaxNanoseconds: 1, Reason: "test"}
	for _, missingGroup := range []int{0, 1} {
		groups := []evaluation{coldCostMeasurements(cost), coldCostMeasurements(cost)}
		delete(groups[missingGroup].Measurements, comparisonKey{cost.Package, cost.Benchmark, "sec/op"})
		if _, err := cost.accepts(groups...); err == nil || !strings.Contains(err.Error(), "requires sec/op") {
			t.Fatalf("missing time measurement in group %d: %v", missingGroup, err)
		}
	}
}

func TestRepositoryApprovalDoesNotCoverOtherBenchmarksOrPackages(t *testing.T) {
	for _, replacement := range [][2]string{
		{"AnalyzeDemoFile-2", "Analyze-2"},
		{"/pkg/semantic", "/pkg/codegen"},
		{"/pkg/semantic", "/pkg/lux/api"},
	} {
		input := strings.ReplaceAll(compilerCostCSV("~", 11262.5, 116), replacement[0], replacement[1])
		path := writeRunInput(t, input)
		if err := run([]string{"--approved-costs", "approved-costs.json", "--base-sha", approvedBase, path, path}, io.Discard); err == nil {
			t.Fatalf("repository approval widened to %s", replacement[1])
		}
	}
}

func TestRepositoryVerificationCostBudget(t *testing.T) {
	costs, err := loadApprovedCosts("approved-costs.json", approvedBase)
	if err != nil {
		t.Fatal(err)
	}
	cost := costs[costKey{"github.com/light-speak/luxo/pkg/lux/auth", "VerifyUncached-2"}]
	if cost.ID != "beta-jwt-verification-precision" || cost.MaxBytes != 736 || cost.MaxAllocs != 10 {
		t.Fatalf("unexpected reviewed verification budget: %+v", cost)
	}
	for _, test := range []struct {
		name, timeDelta, base, benchmark        string
		primaryBytes, confirmBytes, allocations float64
		pass                                    bool
	}{
		{"exact measured ceiling", "~", approvedBase, "VerifyUncached-2", 736, 736, 10, true},
		{"primary over limit", "~", approvedBase, "VerifyUncached-2", 736.5, 736, 10, false},
		{"confirmation over limit", "~", approvedBase, "VerifyUncached-2", 736, 736.5, 10, false},
		{"extra allocation", "~", approvedBase, "VerifyUncached-2", 736, 736, 11, false},
		{"time regression", "+6%", approvedBase, "VerifyUncached-2", 736, 736, 10, false},
		{"expired baseline", "~", strings.Repeat("a", 40), "VerifyUncached-2", 736, 736, 10, false},
		{"cached verification", "~", approvedBase, "VerifyCached-2", 736, 736, 10, false},
		{"different sample", "~", approvedBase, "VerifyUncached-4", 736, 736, 10, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			csv := func(increase float64) string {
				return allocationCostCSV("github.com/light-speak/luxo/pkg/lux/auth", test.benchmark, test.timeDelta, 1456, increase, 22, test.allocations)
			}
			primary, confirmation := writeRunInput(t, csv(test.primaryBytes)), writeRunInput(t, csv(test.confirmBytes))
			err := run([]string{"--approved-costs", "approved-costs.json", "--base-sha", test.base, primary, confirmation}, io.Discard)
			if (err == nil) != test.pass {
				t.Fatalf("gate error = %v, want pass=%t", err, test.pass)
			}
		})
	}
}

func TestRuntimeApprovalEligibilityIsExact(t *testing.T) {
	cost := approvedCost{ID: "verification", BaseSHA: approvedBase, Package: "github.com/light-speak/luxo/pkg/lux/auth", Benchmark: "VerifyUncached-2", MaxBytes: 736, MaxAllocs: 10, Reason: "Reviewed verification memory only"}
	if err := cost.validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VerifyCached-2", "VerifyUncached-4", "VerifyUncached-2/other", "*", ""} {
		cost.Benchmark = name
		if err := cost.validate(); err == nil {
			t.Errorf("unreviewed runtime benchmark accepted: %q", name)
		}
	}
}

func TestRejectedCostReportsExactMeasurements(t *testing.T) {
	policy := writeRunInput(t, approvedPolicy)
	primary := writeRunInput(t, compilerCostCSV("~", 257.5, 2))
	confirmation := writeRunInput(t, compilerCostCSV("~", 258, 2))
	var output strings.Builder
	err := run([]string{"--approved-costs", policy, "--base-sha", approvedBase, primary, confirmation}, &output)
	if err == nil {
		t.Fatal("over-budget measurements must not be rounded into approval")
	}
	for _, want := range []string{
		"base=446000 head=446257.5 delta=+257.5",
		"base=446000 head=446258 delta=+258",
		"approved limits: +256 B/op, +2 allocs/op",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("missing %q in diagnostics: %s", want, output.String())
		}
	}
}

const approvedPolicy = `{"version":1,"approvals":[{
	"id":"fn-declaration-index",
	"baseSHA":"` + approvedBase + `",
	"package":"github.com/light-speak/luxo/pkg/semantic",
	"benchmark":"AnalyzeDemoFile-2",
	"maxBytes":256,"maxAllocs":2,
	"reason":"Reviewed per-compilation declaration index; no request-path allocation."
}]}`

func compilerCostCSV(timeDelta string, byteIncrease, allocationIncrease float64) string {
	return allocationCostCSV("github.com/light-speak/luxo/pkg/semantic", "AnalyzeDemoFile-2", timeDelta, 446000, byteIncrease, 3585, allocationIncrease)
}

func allocationCostCSV(packageName, benchmark, timeDelta string, baseBytes, byteIncrease, baseAllocs, allocationIncrease float64) string {
	var output strings.Builder
	fmt.Fprintf(&output, "pkg: %s\n", packageName)
	for _, metric := range []struct {
		unit     string
		base     float64
		increase float64
		delta    string
	}{
		{"sec/op", 10, 0, timeDelta},
		{"B/op", baseBytes, byteIncrease, "+0.06%"},
		{"allocs/op", baseAllocs, allocationIncrease, "+0.06%"},
	} {
		table := benchstatTable(metric.unit, metric.delta, "n=10")
		table = strings.ReplaceAll(table, "EncodeEvent-2", benchmark)
		table = strings.Replace(table, ",10,0%,11,0%,", fmt.Sprintf(",%g,0%%,%g,0%%,", metric.base, metric.base+metric.increase), 1)
		output.WriteString(table)
		output.WriteByte('\n')
	}
	return output.String()
}

func TestApprovedCompilationCostRequiresConfirmation(t *testing.T) {
	policy := writeRunInput(t, approvedPolicy)
	primary := writeRunInput(t, compilerCostCSV("~", 256, 2))
	var output strings.Builder
	err := run([]string{"--approved-costs", policy, "--base-sha", approvedBase, primary}, &output)
	if err == nil || !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("unconfirmed approval error = %v", err)
	}
	output.Reset()
	err = run([]string{"--requires-confirmation", "--approved-costs", policy, "--base-sha", approvedBase, primary}, &output)
	if err != nil || output.String() != "true\n" {
		t.Fatalf("probe = %q, error %v", output.String(), err)
	}
	output.Reset()
	err = run([]string{"--approved-costs", policy, "--base-sha", approvedBase, primary, primary}, &output)
	if err != nil || !strings.Contains(output.String(), "approved allocation cost: fn-declaration-index") {
		t.Fatalf("approved output = %q, error %v", output.String(), err)
	}
}

func TestApprovedCostDoesNotWeakenOtherGates(t *testing.T) {
	for _, test := range []struct {
		name         string
		primary      string
		confirmation string
		base         string
	}{
		{"allocation limit", compilerCostCSV("~", 256, 3), compilerCostCSV("~", 256, 3), approvedBase},
		{"byte limit", compilerCostCSV("~", 257, 2), compilerCostCSV("~", 257, 2), approvedBase},
		{"confirmation limit", compilerCostCSV("~", 256, 2), compilerCostCSV("~", 512, 2), approvedBase},
		{"primary limit", compilerCostCSV("~", 512, 2), compilerCostCSV("~", 256, 2), approvedBase},
		{"time regression", compilerCostCSV("+6%", 256, 2), compilerCostCSV("+6%", 256, 2), approvedBase},
		{"new baseline", compilerCostCSV("~", 256, 2), compilerCostCSV("~", 256, 2), strings.Repeat("a", 40)},
		{"other benchmark", benchstatCSV("~", "+1%", "n=10"), benchstatCSV("~", "+1%", "n=10"), approvedBase},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := writeRunInput(t, approvedPolicy)
			primary := writeRunInput(t, test.primary)
			confirmation := writeRunInput(t, test.confirmation)
			if err := run([]string{"--approved-costs", policy, "--base-sha", test.base, primary, confirmation}, io.Discard); err == nil {
				t.Fatal("approval bypassed an unrelated or over-budget regression")
			}
		})
	}
}

func TestApprovedCostPolicyRejectsInvalidConfiguration(t *testing.T) {
	for _, input := range []string{
		"{", approvedPolicy + "{}",
		strings.Replace(approvedPolicy, `"version":1`, `"version":2`, 1),
		strings.Replace(approvedPolicy, `"id":`, `"unknown":true,"id":`, 1),
		strings.Replace(approvedPolicy, `"fn-declaration-index"`, `""`, 1),
		strings.Replace(approvedPolicy, approvedBase, "not-a-sha", 1),
		strings.Replace(approvedPolicy, "/pkg/semantic", "/pkg/lux/luvia", 1),
		strings.Replace(approvedPolicy, "AnalyzeDemoFile-2", "*", 1),
		strings.Replace(approvedPolicy, `"maxBytes":256`, `"maxBytes":-1`, 1),
		strings.Replace(approvedPolicy, `"maxAllocs":2`, `"maxAllocs":-1`, 1),
		strings.Replace(approvedPolicy, `"maxBytes":256,"maxAllocs":2`, `"maxBytes":0,"maxAllocs":0`, 1),
		strings.Replace(approvedPolicy, "Reviewed per-compilation declaration index; no request-path allocation.", "", 1),
	} {
		policy := writeRunInput(t, input)
		data := writeRunInput(t, compilerCostCSV("~", 256, 2))
		if err := run([]string{"--approved-costs", policy, "--base-sha", approvedBase, data, data}, io.Discard); err == nil {
			t.Errorf("invalid policy accepted: %s", input)
		}
	}
}
