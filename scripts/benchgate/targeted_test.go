package main

import (
	"io"
	"strings"
	"testing"
)

const targetedPlatform = "goos: linux\ngoarch: amd64\n"

func TestConfirmationTargetsReportUniqueCanonicalLeaves(t *testing.T) {
	primary := targetedPlatform +
		targetedCSV("pkg/semantic", "Analyze/Options=a+b#01-2", "+5.01%", "+1.00%", "~") +
		targetedCSV("pkg/lux/cache", "EncodeEvent/size=10-2", "~", "~", "+1.00%") +
		targetedCSV("pkg/semantic", "Analyze/Clean-2", "~", "~", "~") +
		targetedCSV("pkg/semantic", "Analyze/Bare-2", "+5.01%", "~", "~")

	var output strings.Builder
	if err := run([]string{"--confirmation-targets", writeRunInput(t, primary)}, &output); err != nil {
		t.Fatal(err)
	}
	want := "pkg/lux/cache\tBenchmarkEncodeEvent/size=10-2\n" +
		"pkg/semantic\tBenchmarkAnalyze/Bare-2\n" +
		"pkg/semantic\tBenchmarkAnalyze/Options=a+b#01-2\n"
	if got := output.String(); got != want {
		t.Fatalf("target list = %q, want %q", got, want)
	}

	clean := targetedPlatform + targetedCSV("pkg/semantic", "Analyze-2", "+5.00%", "~", "~")
	output.Reset()
	if err := run([]string{"--confirmation-targets", writeRunInput(t, clean)}, &output); err != nil || output.Len() != 0 {
		t.Fatalf("clean target list = %q, error = %v", output.String(), err)
	}
}

func TestConfirmationTargetsRejectUnusableIdentities(t *testing.T) {
	for _, test := range []struct {
		name string
		pkg  string
		leaf string
	}{
		{"outside module", "other.example/luxo/pkg/semantic", "Analyze-2"},
		{"outside package tree", "github.com/light-speak/luxo/scripts/benchgate", "Analyze-2"},
		{"parent path", "github.com/light-speak/luxo/pkg/../pkg/semantic", "Analyze-2"},
		{"package delimiter", "github.com/light-speak/luxo/pkg/lux\tother", "Analyze-2"},
		{"unsafe path", "github.com/light-speak/luxo/pkg/lux:other", "Analyze-2"},
		{"unmapped adapter path", "github.com/light-speak/luxo/pkg/lux/cache/redis", "Analyze-2"},
		{"missing cpu", "github.com/light-speak/luxo/pkg/semantic", "Analyze"},
		{"different cpu", "github.com/light-speak/luxo/pkg/semantic", "Analyze-8"},
		{"missing benchmark function", "github.com/light-speak/luxo/pkg/semantic", "-2"},
		{"unusable function", "github.com/light-speak/luxo/pkg/semantic", "A|B-2"},
		{"leaf delimiter", "github.com/light-speak/luxo/pkg/semantic", "Analyze/A\r\nB-2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			valid := targetedCSV("pkg/codegen", "Compile-2", "+6%", "~", "~")
			invalid := strings.Replace(
				targetedCSV("pkg/semantic", test.leaf, "+6%", "~", "~"),
				"pkg: github.com/light-speak/luxo/pkg/semantic", "pkg: "+test.pkg, 1,
			)
			var output strings.Builder
			path := writeRunInput(t, targetedPlatform+valid+invalid)
			if err := run([]string{"--confirmation-targets", path}, &output); err == nil || output.Len() != 0 {
				t.Fatalf("accepted invalid target with output %q: %v", output.String(), err)
			}
		})
	}
}

func TestTargetedConfirmationPreservesTwoHits(t *testing.T) {
	primary := writeRunInput(t, targetedPlatform+
		targetedCSV("pkg/semantic", "Analyze-2", "+6%", "~", "~")+
		targetedCSV("pkg/semantic", "Other-2", "~", "+1%", "~")+
		targetedCSV("pkg/codegen", "Clean-2", "~", "~", "~"))

	for _, test := range []struct {
		name      string
		time      string
		bytes     string
		wantError bool
		wantText  string
	}{
		{"noise", "~", "~", false, "unconfirmed performance variation"},
		{"different metric", "~", "+1%", false, "unconfirmed performance variation"},
		{"same metric", "+7%", "~", true, "confirmed performance regression"},
	} {
		t.Run(test.name, func(t *testing.T) {
			confirmation := writeRunInput(t, targetedPlatform+
				targetedCSV("pkg/semantic", "Analyze-2", test.time, test.bytes, "~")+
				targetedCSV("pkg/semantic", "Other-2", "~", "~", "~"))
			var output strings.Builder
			err := run([]string{"--targeted-confirmation", primary, confirmation}, &output)
			if (err != nil) != test.wantError || !strings.Contains(output.String(), test.wantText) {
				t.Fatalf("targeted confirmation output = %q, error = %v", output.String(), err)
			}
			if err == nil && !strings.Contains(output.String(), "primary 3 time and 6 allocation comparisons; targeted confirmation 2 time and 4 allocation comparisons") {
				t.Fatalf("targeted confirmation summary = %q", output.String())
			}
		})
	}
}

func TestTargetedConfirmationRejectsInexactEvidence(t *testing.T) {
	first := targetedCSV("pkg/semantic", "Analyze-2", "+6%", "~", "~")
	second := targetedCSV("pkg/semantic", "Other-2", "~", "+1%", "~")
	clean := targetedCSV("pkg/semantic", "Clean-2", "~", "~", "~")
	invalid := targetedCSV("pkg/semantic", "Analyze-8", "+6%", "~", "~")
	for _, test := range []struct {
		name    string
		primary string
		confirm string
		want    string
	}{
		{"omitted", targetedPlatform + first + second, targetedPlatform + first, "comparison sets differ"},
		{"extra", targetedPlatform + first + clean, targetedPlatform + first + clean, "comparison sets differ"},
		{"replacement", targetedPlatform + first + second, targetedPlatform + first + clean, "comparison sets differ"},
		{"clean primary", targetedPlatform + clean, targetedPlatform + clean, "requires primary regression candidates"},
		{"invalid target", targetedPlatform + invalid, targetedPlatform + invalid, "invalid confirmation target"},
		{"different platform", targetedPlatform + first, "goos: darwin\ngoarch: arm64\n" + first, "platform"},
		{"missing platform", targetedPlatform + first, first, "platform"},
		{"missing both platforms", first, first, "platform"},
		{"partial platform", "goos: linux\n" + first, "goos: linux\n" + first, "platform"},
		{"missing unit", targetedPlatform + first, targetedPlatform + first[:strings.Index(first, ",allocs/op,")], "missing paired allocs/op"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := run([]string{"--targeted-confirmation", writeRunInput(t, test.primary), writeRunInput(t, test.confirm)}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("targeted confirmation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestTargetedConfirmationHonorsApprovedCostBounds(t *testing.T) {
	primary := writeRunInput(t, targetedPlatform+compilerCostCSV("~", 256, 2)+
		targetedCSV("pkg/codegen", "Compile-2", "~", "~", "~"))
	for _, test := range []struct {
		name      string
		increase  float64
		wantError bool
		want      string
	}{
		{"approved", 11280, false, "approved allocation cost"},
		{"above bound", 11281, true, "sample approval blocked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			confirmation := writeRunInput(t, targetedPlatform+compilerCostCSV("~", test.increase, 2))
			var output strings.Builder
			err := run([]string{"--targeted-confirmation", "--approved-costs", "approved-costs.json", "--base-sha", approvedBase, primary, confirmation}, &output)
			if (err != nil) != test.wantError || !strings.Contains(output.String(), test.want) {
				t.Fatalf("approval output = %q, error = %v", output.String(), err)
			}
		})
	}
}

func TestTargetedConfirmationCLIRejectsIncompleteOrConflictingOptions(t *testing.T) {
	primary := writeRunInput(t, targetedPlatform+targetedCSV("pkg/semantic", "Analyze-2", "+6%", "~", "~"))
	for _, args := range [][]string{
		{"--confirmation-targets"},
		{"--confirmation-targets", primary, primary},
		{"--targeted-confirmation", primary},
		{"--targeted-confirmation", primary, primary, primary},
		{"--requires-confirmation", "--confirmation-targets", primary},
		{"--targeted-confirmation", "--confirmation-targets", primary, primary},
		{"--targeted-confirmation", "--requires-confirmation", primary, primary},
		{"--confirmation-targets", "missing.csv"},
		{"--targeted-confirmation", "missing.csv", primary},
		{"--targeted-confirmation", primary, "missing.csv"},
	} {
		if err := run(args, io.Discard); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
	if err := run([]string{"--confirmation-targets", primary}, errorWriter{}); err == nil || !strings.Contains(err.Error(), "write failure") {
		t.Fatalf("target output error = %v", err)
	}
}

func targetedCSV(pkg, leaf, timeDelta, byteDelta, allocDelta string) string {
	var result strings.Builder
	result.WriteString("pkg: github.com/light-speak/luxo/" + pkg + "\n")
	name := "\"" + strings.ReplaceAll(leaf, "\"", "\"\"") + "\""
	for _, metric := range []struct{ unit, delta string }{
		{"sec/op", timeDelta},
		{"B/op", byteDelta},
		{"allocs/op", allocDelta},
	} {
		result.WriteString(strings.Replace(benchstatTable(metric.unit, metric.delta, "n=10"), "EncodeEvent-2", name, 1))
		result.WriteByte('\n')
	}
	return result.String()
}
