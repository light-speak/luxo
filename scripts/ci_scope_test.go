package scripts_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

const requiresProductChecks = "(needs.pr-test-scope.result != 'success' || needs.pr-test-scope.outputs.kind != 'benchmark-only')"
const productCheckScope = "${{ !cancelled() && github.event_name != 'workflow_dispatch' && " + requiresProductChecks + " }}"

func TestCIScopeRequiresOnlyOrdinaryBenchmarkToolFiles(t *testing.T) {
	record := func(mode, status, path string) string {
		const object = " 0123456789abcdef0123456789abcdef01234567"
		return ":" + mode + object + object + " " + status + "\x00" + path + "\x00"
	}
	modified := func(path string) string { return record("100644 100644", "M", path) }
	benchmarkFiles := modified("scripts/benchplan/inventory.go") + modified("scripts/performance_test.go")
	for _, test := range []struct {
		name, changes, want string
	}{
		{"tool edits", benchmarkFiles, "benchmark-only"},
		{"tool add", record("000000 100644", "A", "scripts/benchrun/example_test.go"), "benchmark-only"},
		{"tool delete", record("100644 000000", "D", "scripts/benchgate/obsolete.go"), "benchmark-only"},
		{"empty", "", "full"},
		{"Go runtime", benchmarkFiles + modified("pkg/lux/codec/wire.go"), "full"},
		{"shared compiler", modified("pkg/codegen/compile.go"), "full"},
		{"shared helper", modified("internal/benchlayout/profile.go"), "full"},
		{"Go dependency", modified("go.sum"), "full"},
		{"nested module", modified("scripts/benchrun/go.mod"), "full"},
		{"workflow", benchmarkFiles + modified(".github/workflows/test.yml"), "full"},
		{"classifier", modified("scripts/classify-ci-scope.sh"), "full"},
		{"approved limits", modified("scripts/benchgate/approved-costs.json"), "full"},
		{"symlink", benchmarkFiles + record("100644 120000", "T", "scripts/benchrun/linked.go"), "full"},
		{"renamed runtime file", record("100644 100644", "R100", "scripts/benchrun/wire.go"), "full"},
		{"malformed header", "unknown\x00scripts/benchrun/run.go\x00", "full"},
		{"truncated path", strings.TrimSuffix(benchmarkFiles, "\x00"), "full"},
		{"truncated header", benchmarkFiles + "truncated", "full"},
		{"odd path", modified("scripts/benchrun/odd\nfile.go"), "full"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), "bash", "classify-ci-scope.sh")
			command.Stdin = strings.NewReader(test.changes)
			got, err := command.CombinedOutput()
			if err != nil || !bytes.Equal(got, []byte(test.want+"\n")) {
				t.Fatalf("scope = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestCIScopeRunsProductChecksForUnclassifiedRevisions(t *testing.T) {
	job := readQualityWorkflow(t).Jobs["pr-test-scope"]
	if job.If != "github.event_name == 'pull_request'" || len(job.Steps) != 2 {
		t.Fatal("scope must classify the verified PR merge before running tests")
	}
	step := job.Steps[1]
	if job.Outputs["kind"] != "${{ steps.classification.outputs.kind }}" || step.If != "" || step.ContinueOnError {
		t.Fatal("missing the classification output used by the test jobs")
	}
	for _, test := range []struct {
		name, adjustment, want string
	}{
		{"matching benchmark changes", "", "benchmark-only"},
		{"unavailable checkout", "export CHECKOUT_OUTCOME=failure\n", "full"},
		{"unavailable baseline helper", "export MISSING_HELPER=1\n", "full"},
		{"invalid tested revision", "export TESTED_SHA=unexpected\n", "full"},
		{"different tested base", "export EVENT_BASE_SHA=$EVENT_HEAD_SHA\n", "full"},
		{"different tested head", "export EVENT_HEAD_SHA=$EVENT_BASE_SHA\n", "full"},
		{"incomplete diff", "export DIFF_FAILED=1\n", "full"},
		{"product path", "export DIFF_PATH=pkg/lux/codec/wire.go\n", "full"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runCIScopeWorkflowStep(t, step.Run, test.adjustment); got != "kind="+test.want+"\n" {
				t.Fatalf("classification output = %q, want %q", got, test.want)
			}
		})
	}
}

func runCIScopeWorkflowStep(t *testing.T, step, adjustment string) string {
	t.Helper()
	fixture := newQualityFixture(t)
	fixture.write(t, "trusted-classifier.sh", readQualityFile(t, "classify-ci-scope.sh"))
	fixture.tool(t, "bash", "exec /bin/bash \"$@\"", false)
	fixture.tool(t, "git", `
case "$1" in
  rev-list) printf '%s %s %s\n' "$TESTED_SHA" "$ACTUAL_BASE_SHA" "$ACTUAL_HEAD_SHA" ;;
  show)
    [ "${MISSING_HELPER:-}" != 1 ] || exit 128
    [ "$2" = "$ACTUAL_BASE_SHA:scripts/classify-ci-scope.sh" ] || exit 2
    /bin/cat trusted-classifier.sh ;;
  diff)
    printf ':100644 100644 %s %s M\000%s\000' "$ACTUAL_BASE_SHA" "$ACTUAL_HEAD_SHA" "$DIFF_PATH"
    [ "${DIFF_FAILED:-}" != 1 ] ;;
  *) exit 2 ;;
esac`, false)
	const setup = `
export GITHUB_OUTPUT=output
export CHECKOUT_OUTCOME=success
export TESTED_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export EVENT_BASE_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
export EVENT_HEAD_SHA=cccccccccccccccccccccccccccccccccccccccc
export ACTUAL_BASE_SHA=$EVENT_BASE_SHA ACTUAL_HEAD_SHA=$EVENT_HEAD_SHA
export DIFF_PATH=scripts/benchplan/inventory.go
`
	if output, log, err := fixture.run(t, setup+adjustment+step); err != nil {
		t.Fatalf("scope resolver failed: %v\n%s\n%s", err, output, log)
	}
	return readQualityFile(t, string(fixture)+"/output")
}

func TestCIScopeLeavesCoreAndPerformanceChecksRequired(t *testing.T) {
	workflow := readQualityWorkflow(t)
	for _, name := range []string{"test", "lint", "performance-scope", "performance-samples", "performance"} {
		job, ok := workflow.Jobs[name]
		if !ok || strings.Contains(job.If, "pr-test-scope") || strings.Contains(job.Needs.Value, "pr-test-scope") {
			t.Errorf("CI scope must leave %s required", name)
		}
	}
	for _, name := range []string{"race", "runtime-race", "external-go-consumer", "fuzz", "sdk-ts", "sdk-dart",
		"sdk-kotlin", swiftNativeCIJob} {
		job := workflow.Jobs[name]
		if job.Needs.Value != "pr-test-scope" || job.If != productCheckScope || job.ContinueOnError {
			t.Errorf("%s must run for ordinary events unless the verified output is benchmark-only", name)
		}
	}
}

func TestManualPerformanceQualificationDoesNotRepeatCoreChecks(t *testing.T) {
	workflow := readQualityWorkflow(t)
	for _, name := range []string{"test", "lint"} {
		if workflow.Jobs[name].If != "github.event_name != 'workflow_dispatch'" {
			t.Errorf("%s must run on ordinary push and PR events", name)
		}
	}
}
