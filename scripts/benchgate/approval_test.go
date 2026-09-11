package main

import (
	"encoding/json"
	"fmt"
	"io"
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
	if len(costs) != 1 || cost.MaxBytes != 272 || cost.MaxAllocs != 2 {
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
		{"measured groups", 258, 253.5, 2, "~", approvedBase, true},
		{"exact ceiling", 272, 272, 2, "~", approvedBase, true},
		{"fraction over ceiling", 272.5, 272.5, 2, "~", approvedBase, false},
		{"primary over ceiling", 273, 258, 2, "~", approvedBase, false},
		{"confirmation over ceiling", 258, 273, 2, "~", approvedBase, false},
		{"extra allocation", 258, 258, 3, "~", approvedBase, false},
		{"time regression", 258, 258, 2, "+6%", approvedBase, false},
		{"expired baseline", 258, 258, 2, "~", strings.Repeat("a", 40), false},
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
	var output strings.Builder
	output.WriteString("pkg: github.com/light-speak/luxo/pkg/semantic\n")
	for _, metric := range []struct {
		unit     string
		base     float64
		increase float64
		delta    string
	}{
		{"sec/op", 10, 0, timeDelta},
		{"B/op", 446000, byteIncrease, "+0.06%"},
		{"allocs/op", 3585, allocationIncrease, "+0.06%"},
	} {
		table := benchstatTable(metric.unit, metric.delta, "n=10")
		table = strings.ReplaceAll(table, "EncodeEvent-2", "AnalyzeDemoFile-2")
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
	if err != nil || !strings.Contains(output.String(), "approved compilation cost: fn-declaration-index") {
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
