package main

import (
	"io"
	"strings"
	"testing"
)

const advisoryPlatform = targetedPlatform + "cpu: AMD EPYC 9V45 96-Core Processor\n"

func TestAdvisoryTimingKeepsTimeVisibleWithoutConfirmation(t *testing.T) {
	primary := writeRunInput(t, advisoryPlatform+
		targetedCSV("pkg/semantic", "TimeOnly-2", "+5.01%", "~", "~")+
		targetedCSV("pkg/semantic", "AtLimit-2", "+5.00%", "~", "~"))
	for _, tc := range []struct{ probe, want string }{
		{"--confirmation-targets", ""},
		{"--requires-confirmation", "false\n"},
	} {
		var output strings.Builder
		if err := run([]string{"--advisory-timing", tc.probe, primary}, &output); err != nil || output.String() != tc.want {
			t.Fatalf("%s output = %q, error = %v, want %q", tc.probe, output.String(), err, tc.want)
		}
	}
	var output strings.Builder
	if err := run([]string{"--advisory-timing", primary}, &output); err != nil ||
		!strings.Contains(output.String(), "advisory timing regression (primary): github.com/light-speak/luxo/pkg/semantic/TimeOnly-2 sec/op +5.01%") ||
		!strings.Contains(output.String(), "performance gate passed") || strings.Contains(output.String(), "AtLimit-2") {
		t.Fatalf("advisory output = %q, error = %v", output.String(), err)
	}
	if err := run([]string{"--advisory-timing", primary}, errorWriter{}); err == nil || !strings.Contains(err.Error(), "write failure") {
		t.Fatalf("advisory write error = %v", err)
	}
	if err := run([]string{"--advisory-timing", "--targeted-confirmation", primary, primary}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "requires primary regression candidates") {
		t.Fatalf("targeted confirmation without an allocation candidate error = %v", err)
	}
	if err := run([]string{"--advisory-timing", primary, primary}, io.Discard); err != nil {
		t.Fatalf("valid full confirmation error = %v", err)
	}
	output.Reset()
	if err := run([]string{primary, primary}, &output); err == nil || err.Error() != "performance gate rejected 1 regression(s)" ||
		!strings.Contains(output.String(), "confirmed performance regression") || strings.Contains(output.String(), "advisory") {
		t.Fatalf("strict output = %q, error = %v", output.String(), err)
	}
}

func TestAdvisoryTimingStillConfirmsAllocationRegressions(t *testing.T) {
	const pkg = "pkg/semantic"
	primary := writeRunInput(t, advisoryPlatform+
		targetedCSV(pkg, "Bytes/after-2", "+6%", "+1%", "~")+
		targetedCSV(pkg, "Bytes/before-2", "+6%", "~", "~")+
		targetedCSV(pkg, "Allocs-2", "+6%", "~", "+1%")+
		targetedCSV(pkg, "TimeOnly-2", "+6%", "~", "~"))
	var output strings.Builder
	const targets = "pkg/semantic\tBenchmarkAllocs-2\npkg/semantic\tBenchmarkBytes/after-2\npkg/semantic\tBenchmarkBytes/before-2\n"
	if err := run([]string{"--advisory-timing", "--confirmation-targets", primary}, &output); err != nil || output.String() != targets {
		t.Fatalf("targets = %q, error = %v", output.String(), err)
	}
	if err := run([]string{"--advisory-timing", primary}, io.Discard); err == nil || !strings.Contains(err.Error(), "requires confirmation for 2") {
		t.Fatalf("unconfirmed allocation gate error = %v", err)
	}
	for _, tc := range []struct{ allocationDelta, wantError string }{
		{"+1%", "performance gate rejected 2 regression(s)"},
		{"~", ""},
	} {
		confirmation := writeRunInput(t, advisoryPlatform+
			targetedCSV(pkg, "Bytes/before-2", "+7%", "~", "~")+
			targetedCSV(pkg, "Bytes/after-2", "+7%", tc.allocationDelta, "~")+
			targetedCSV(pkg, "Allocs-2", "+7%", "~", tc.allocationDelta))
		output.Reset()
		err := run([]string{"--advisory-timing", "--targeted-confirmation", primary, confirmation}, &output)
		if (err == nil) != (tc.wantError == "") || err != nil && err.Error() != tc.wantError {
			t.Fatalf("confirmation output = %q, error = %v", output.String(), err)
		}
		if !strings.Contains(output.String(), "advisory timing regression (primary): "+benchmarkPackagePrefix+pkg+"/TimeOnly-2") ||
			!strings.Contains(output.String(), "advisory timing regression (confirmation): "+benchmarkPackagePrefix+pkg+"/Bytes/after-2") {
			t.Fatalf("missing timing evidence in %q", output.String())
		}
	}
}

func TestAdvisoryTimingRejectsIncompleteOrInvalidEvidence(t *testing.T) {
	const pkg = "pkg/semantic"
	rows := targetedCSV(pkg, "Mixed-2", "+6%", "+1%", "~")
	valid := advisoryPlatform + rows
	for _, tc := range []struct {
		name, primary, confirmation, want string
	}{
		{"missing platform", rows, valid, "platform"},
		{"missing cpu", targetedPlatform + rows, valid, "CPU"},
		{"mixed cpu", valid + "cpu: other\n", valid, "CPU"},
		{"wrong cpu suffix", strings.ReplaceAll(valid, "Mixed-2", "Mixed-4"), valid, "benchmark"},
		{"different cpu", valid, strings.ReplaceAll(valid, "AMD EPYC 9V45 96-Core Processor", "other"), "CPU"},
		{"different platform", valid, strings.ReplaceAll(valid, "goos: linux", "goos: darwin"), "platform"},
		{"invalid timed samples", strings.Replace(valid, "n=10", "n=10+9", 1), valid, "10 samples"},
		{"invalid confirmation timing samples", valid, strings.Replace(valid, "n=10", "n=invalid", 1), "invalid sample count"},
		{"invalid timed measurement", valid, strings.Replace(valid, ",10,0%", ",NaN,0%", 1), "invalid benchmark measurement"},
		{"missing confirmation time", valid, strings.Replace(valid, ",sec/op,", ",unknown,", 1), "no time comparisons"},
		{"unexpected confirmation leaf", valid, valid + targetedCSV(pkg, "Extra-2", "~", "~", "~"), "comparison sets differ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run([]string{"--advisory-timing", "--targeted-confirmation", writeRunInput(t, tc.primary), writeRunInput(t, tc.confirmation)}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("evidence error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAdvisoryTimingConfirmationRequiresWritableTimingAudit(t *testing.T) {
	allocation := writeRunInput(t, advisoryPlatform+targetedCSV("pkg/semantic", "Mixed-2", "~", "+1%", "~"))
	mixed := writeRunInput(t, advisoryPlatform+targetedCSV("pkg/semantic", "Mixed-2", "+6%", "+1%", "~"))
	for _, pair := range [][2]string{{mixed, allocation}, {allocation, mixed}} {
		err := run([]string{"--advisory-timing", "--targeted-confirmation", pair[0], pair[1]}, errorWriter{})
		if err == nil || err.Error() != "write failure" {
			t.Fatalf("advisory output write error = %v", err)
		}
	}
}

func TestAdvisoryTimingUsesExistingAllocationApprovals(t *testing.T) {
	primary := writeRunInput(t, advisoryPlatform+compilerCostCSV("+6%", 256, 2))
	for _, tc := range []struct {
		name, policy, base, want string
		bytes                    float64
		fail                     bool
	}{
		{"within bound", "approved-costs.json", approvedBase, "approved allocation cost", 11280, false},
		{"over bound", "approved-costs.json", approvedBase, "sample approval blocked", 11281, true},
		{"another baseline", "approved-costs.json", strings.Repeat("a", 40), "confirmed performance regression", 11280, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			confirmation := writeRunInput(t, advisoryPlatform+compilerCostCSV("+6%", tc.bytes, 2))
			var output strings.Builder
			err := run([]string{"--advisory-timing", "--targeted-confirmation", "--approved-costs", tc.policy, "--base-sha", tc.base, primary, confirmation}, &output)
			if (err != nil) != tc.fail || !strings.Contains(output.String(), tc.want) {
				t.Fatalf("approval output = %q, error = %v", output.String(), err)
			}
		})
	}
	badPolicy := writeRunInput(t, `{"version":2,"approvals":[]}`)
	if err := run([]string{"--advisory-timing", "--approved-costs", badPolicy, "--base-sha", approvedBase, primary}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "unsupported approved cost policy") {
		t.Fatalf("invalid policy error = %v", err)
	}
}

func TestAdvisoryTimingPreservesCombinedApprovalBudget(t *testing.T) {
	const pkg = "pkg/codegen"
	const name = "GenerateModelFile-2"
	// This exact approval's allocation increases fit, but its measured time limit is exceeded.
	mixed := writeRunInput(t, advisoryPlatform+targetedCSV(pkg, name, "+6%", "+1%", "~"))
	timing := writeRunInput(t, advisoryPlatform+targetedCSV(pkg, name, "+6%", "~", "~"))
	flags := []string{"--advisory-timing", "--approved-costs", "approved-costs.json", "--base-sha", approvedBase}
	var output strings.Builder
	err := run(append(flags, "--targeted-confirmation", mixed, mixed), &output)
	if err == nil || err.Error() != "performance gate rejected 1 regression(s)" ||
		!strings.Contains(output.String(), "sample approval blocked by group 1 sec/op") ||
		!strings.Contains(output.String(), "advisory timing regression (primary)") {
		t.Fatalf("expected allocation approval to retain its joint limits, output = %q, error = %v", output.String(), err)
	}
	output.Reset()
	if err := run(append(flags, timing), &output); err != nil || strings.Contains(output.String(), "approved time cost") {
		t.Fatalf("unconfirmed advisory output = %q, error = %v", output.String(), err)
	}
}
