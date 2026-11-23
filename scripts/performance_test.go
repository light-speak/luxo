package scripts_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func performanceStep(t *testing.T, job, name string) string {
	t.Helper()
	for _, step := range readQualityWorkflow(t).Jobs[job].Steps {
		if step.Name == name {
			if step.ContinueOnError {
				t.Fatalf("%s must not ignore failures", name)
			}
			return step.Run
		}
	}
	t.Fatalf("missing %s/%s", job, name)
	return ""
}

func TestPerformanceCIKeepsSamplingAndEvidenceGates(t *testing.T) {
	workflow := readQualityWorkflow(t)
	job := workflow.Jobs["performance-samples"]
	if job.TimeoutMinutes != 60 || job.Strategy.FailFast == nil || *job.Strategy.FailFast {
		t.Fatal("every batch must have a bounded budget for primary and confirmation samples")
	}
	if job.Needs.Value != "performance-scope" || job.If != "needs.performance-scope.outputs.run == 'true'" {
		t.Fatal("sampling must require the successful scope and plan job")
	}
	script := performanceStep(t, "performance-samples", "Run adaptive interleaved benchmark samples")
	for _, required := range []string{
		"{1..10}", "-test.benchtime=200ms", "-test.cpu=2", "-test.benchmem",
		`-test.bench="${filter}"`, "-alpha 0.01",
		"run_group primary .tmp/benchmark-batch.tsv",
		"run_group confirmation .tmp/benchmark-confirmation-batch.tsv",
		"--verify .tmp/benchmark-expected.json .tmp/benchmark-primary-base.txt .tmp/benchmark-primary-head.txt",
		"--verify .tmp/benchmark-confirmation-expected.json .tmp/benchmark-confirmation-base.txt .tmp/benchmark-confirmation-head.txt",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("sampling lost %q", required)
		}
	}
	gate := performanceStep(t, "performance-samples", "Enforce performance baseline")
	if !strings.Contains(gate, `--approved-costs scripts/benchgate/approved-costs.json --base-sha "${BASE_SHA}"`) {
		t.Fatal("performance approvals must remain tied to the exact baseline")
	}
}

func TestPerformanceSummaryFailsClosed(t *testing.T) {
	job := readQualityWorkflow(t).Jobs["performance"]
	if job.If != "always() && github.event_name == 'pull_request'" || len(job.Needs.Content) != 2 {
		t.Fatal("stable performance check must aggregate both jobs even after failure")
	}
	script := performanceStep(t, "performance", "Require every selected benchmark batch")
	for _, test := range []struct {
		scope, samples, selected string
		pass                     bool
	}{
		{"success", "success", "true", true}, {"success", "skipped", "false", true},
		{"success", "failure", "true", false}, {"success", "cancelled", "true", false},
		{"success", "skipped", "true", false}, {"failure", "skipped", "", false},
		{"cancelled", "skipped", "", false}, {"success", "success", "", false},
		{"success", "failure", "false", false},
	} {
		fixture := newQualityFixture(t)
		prefix := fmt.Sprintf("export SCOPE_RESULT=%s SAMPLES_RESULT=%s HAS_BENCHMARKS=%s\n", shellQualityQuote(test.scope), shellQualityQuote(test.samples), shellQualityQuote(test.selected))
		output, _, err := fixture.run(t, prefix+script)
		if (err == nil) != test.pass {
			t.Errorf("%+v: err=%v output=%s", test, err, output)
		}
	}
}

func samplingFixture(t *testing.T, confirmation bool) qualityFixture {
	t.Helper()
	fixture := newQualityFixture(t)
	fixture.write(t, ".tmp/benchmark-batch.tsv", "pkg/a\t^(BenchmarkA|BenchmarkB)$\npkg/control\t^BenchmarkC$\n")
	fixture.write(t, ".tmp/benchmark-expected.json", "primary expectations\n")
	for _, revision := range []string{"base", "head"} {
		for _, binary := range []string{"pkg-a.test", "pkg-control.test"} {
			fixture.write(t, ".tmp/bin/"+revision+"/"+binary, "#!/bin/sh\nrevision="+revision+"\n"+sampleEvidence)
		}
	}
	fixture.write(t, ".tmp/bin/benchstat", "#!/bin/sh\nprintf 'benchstat %s\\n' \"$*\" >> \"$QUALITY_LOG\"\nprintf 'benchmark CSV\\n'\n")
	fixture.write(t, ".tmp/bin/benchrun", `#!/bin/sh
set -eu
test "$1" = run
benchmark_bin=$3
benchmark_package=$4
shift 4
case "$benchmark_package" in
  pkg/a) exec "$benchmark_bin/pkg-a.test" "$@" ;;
  pkg/control) exec "$benchmark_bin/pkg-control.test" "$@" ;;
  *) exit 42 ;;
esac
`)
	fixture.tool(t, "go", fmt.Sprintf("confirmation=%t\n", confirmation)+samplingTools, false)
	return fixture
}

const sampleEvidence = `set -eu
printf '%s %s\n' "$revision" "$*" >> "$QUALITY_LOG"
case "$*" in
  *'-test.bench=^(BenchmarkA|BenchmarkB)$'*) printf 'BenchmarkA-2\nBenchmarkB-2\n' ;;
  *'-test.bench=^BenchmarkC$'*) printf 'BenchmarkC-2\n' ;;
  *'-test.bench=^BenchmarkB$'*)
    test "${FAIL_STAGE:-}" != sample-confirmation
    printf 'BenchmarkB-2\n'
    ;;
  *) exit 42 ;;
esac
`

const samplingTools = `set -eu
case "$1 $2 $3" in
  "run ./scripts/benchplan --normalize")
    /bin/cat "$4" > "$5"
    ;;
  "run ./scripts/benchplan --verify")
    test -s "$4"
    test -s "$5"
    test -s "$6"
    case "$4" in
      .tmp/benchmark-expected.json) test "${FAIL_STAGE:-}" != verify-primary ;;
      .tmp/benchmark-confirmation-expected.json) test "${FAIL_STAGE:-}" != verify-confirmation ;;
      *) exit 42 ;;
    esac
    ;;
  "run ./scripts/benchgate --confirmation-targets")
    test "$4" = .tmp/benchstat-primary.csv
    test -s "$4"
    test "${FAIL_STAGE:-}" != targets
    if [ "$confirmation" = true ]; then
      printf 'pkg/a\tBenchmarkB-2\n'
    fi
    ;;
  "run ./scripts/benchplan --confirmation-plan")
    test "$4" = .tmp/benchmark-expected.json
    test "$5" = .tmp/benchmark-confirmation-targets.tsv
    test "$6" = .tmp/benchmark-confirmation-batch.tsv
    test "$7" = .tmp/benchmark-confirmation-expected.json
    test -s "$4"
    test "$(/bin/cat "$5")" = "$(printf 'pkg/a\tBenchmarkB-2')"
    test "${FAIL_STAGE:-}" != plan
    if [ "${FAIL_STAGE:-}" != empty-plan ]; then
      printf 'pkg/a\t^BenchmarkB$\n' > "$6"
    fi
    printf 'confirmation expectations\n' > "$7"
    ;;
  "run ./scripts/benchgate --approved-costs") ;;
  *) exit 42 ;;
esac
`

func samplingPrefix(fixture qualityFixture, failure string) string {
	return "export GITHUB_WORKSPACE=" + shellQualityQuote(string(fixture)) +
		" BASE_SHA=expected-main FAIL_STAGE=" + shellQualityQuote(failure) + "\n"
}

func TestPerformanceSamplingAlternatesBothRevisions(t *testing.T) {
	script := performanceStep(t, "performance-samples", "Run adaptive interleaved benchmark samples")
	gate := performanceStep(t, "performance-samples", "Enforce performance baseline")
	for _, confirmation := range []bool{false, true} {
		t.Run(fmt.Sprintf("confirm=%t", confirmation), func(t *testing.T) {
			fixture := samplingFixture(t, confirmation)
			output, log, err := fixture.run(t, samplingPrefix(fixture, "")+script+"\n"+gate)
			if err != nil {
				t.Fatalf("sampling failed: %v\n%s\n%s", err, output, log)
			}
			var runs []string
			for _, line := range strings.Split(log, "\n") {
				if strings.HasPrefix(line, "base ") || strings.HasPrefix(line, "head ") {
					runs = append(runs, line)
				}
			}
			groups, totalRuns := 1, 40
			gateArgs := ".tmp/benchstat-primary.csv\n"
			targets := ""
			if confirmation {
				groups, totalRuns = 2, 60
				gateArgs = "--targeted-confirmation .tmp/benchstat-primary.csv .tmp/benchstat-confirmation.csv\n"
				targets = "pkg/a\tBenchmarkB-2\n"
			}
			wantGate := "go run ./scripts/benchgate --approved-costs scripts/benchgate/approved-costs.json --base-sha expected-main " + gateArgs
			if len(runs) != totalRuns || strings.Count(log, "./scripts/benchplan --verify") != groups || !strings.Contains(log, wantGate) {
				t.Fatalf("sampling or gate changed; got %d runs, log:\n%s", len(runs), log)
			}
			assertSampleOrder(t, runs[:40], "^(BenchmarkA|BenchmarkB)$", "^BenchmarkC$")
			if confirmation {
				assertSampleOrder(t, runs[40:], "^BenchmarkB$")
			}
			if got := readQualityFile(t, filepath.Join(string(fixture), ".tmp/benchmark-confirmation-targets.tsv")); got != targets {
				t.Fatalf("confirmation targets = %q, want %q", got, targets)
			}
			for _, revision := range []string{"base", "head"} {
				assertSampleEvidence(t, fixture, "primary", revision, strings.Repeat("BenchmarkA-2\nBenchmarkB-2\nBenchmarkC-2\n", 10))
				if confirmation {
					assertSampleEvidence(t, fixture, "confirmation", revision, strings.Repeat("BenchmarkB-2\n", 10))
				}
			}
		})
	}
}

func assertSampleOrder(t *testing.T, runs []string, filters ...string) {
	t.Helper()
	for sample := range 10 {
		order := []string{"base", "head"}
		if sample%2 == 1 {
			order[0], order[1] = order[1], order[0]
		}
		for group, filter := range filters {
			for leg, revision := range order {
				line := runs[(sample*len(filters)+group)*2+leg]
				if !strings.HasPrefix(line, revision+" ") || !strings.Contains(line, "-test.bench="+filter+" ") {
					t.Fatalf("sample %d has unexpected order/filter %q, want %s %s", sample+1, line, revision, filter)
				}
			}
		}
	}
}

func assertSampleEvidence(t *testing.T, fixture qualityFixture, group, revision, expected string) {
	t.Helper()
	path := filepath.Join(string(fixture), ".tmp", "benchmark-"+group+"-"+revision+".txt")
	if data := readQualityFile(t, path); data != expected {
		t.Fatalf("%s/%s evidence does not cover the expected samples:\n%s", group, revision, data)
	}
}

func TestPerformanceSamplingStopsOnProcessFailure(t *testing.T) {
	fixture := samplingFixture(t, false)
	fixture.write(t, ".tmp/bin/base/pkg-a.test", "#!/bin/sh\nexit 42\n")
	script := performanceStep(t, "performance-samples", "Run adaptive interleaved benchmark samples")
	_, log, err := fixture.run(t, samplingPrefix(fixture, "")+script)
	if err == nil || strings.Contains(log, "head ") || strings.Contains(log, "--confirmation-targets") {
		t.Fatalf("continued after failed benchmark: err=%v log=%s", err, log)
	}
}

func TestPerformanceSamplingStopsOnEvidenceFailure(t *testing.T) {
	script := performanceStep(t, "performance-samples", "Run adaptive interleaved benchmark samples")
	gate := performanceStep(t, "performance-samples", "Enforce performance baseline")
	for _, failure := range []struct{ stage, reached, forbidden string }{
		{"verify-primary", "--verify .tmp/benchmark-expected.json .tmp/benchmark-primary-base.txt", "--confirmation-targets"},
		{"targets", "--confirmation-targets .tmp/benchstat-primary.csv", "--confirmation-plan"},
		{"plan", "--confirmation-plan .tmp/benchmark-expected.json", "-test.bench=^BenchmarkB$"},
		{"empty-plan", "--confirmation-plan .tmp/benchmark-expected.json", "-test.bench=^BenchmarkB$"},
		{"sample-confirmation", "-test.bench=^BenchmarkB$", "--verify .tmp/benchmark-confirmation-expected.json"},
		{"verify-confirmation", "--verify .tmp/benchmark-confirmation-expected.json .tmp/benchmark-confirmation-base.txt", "base=.tmp/benchmark-confirmation-base.txt"},
	} {
		t.Run(failure.stage, func(t *testing.T) {
			fixture := samplingFixture(t, true)
			output, log, err := fixture.run(t, samplingPrefix(fixture, failure.stage)+script+"\n"+gate)
			if err == nil || !strings.Contains(log, failure.reached) {
				t.Fatalf("did not reach the %s failure: err=%v output=%s log=%s", failure.stage, err, output, log)
			}
			if strings.Contains(log, failure.forbidden) || strings.Contains(log, "--approved-costs") {
				t.Fatalf("continued after %s failure: %s", failure.stage, log)
			}
		})
	}
}

func TestPerformanceGateRejectsMissingOrUnexpectedEvidence(t *testing.T) {
	gate := performanceStep(t, "performance-samples", "Enforce performance baseline")
	for _, test := range []struct{ name, primary, targets, confirmation string }{
		{"missing primary", "", "pkg/a\tBenchmarkB-2\n", "evidence"},
		{"empty confirmation with targets", "evidence", "pkg/a\tBenchmarkB-2\n", ""},
		{"unexpected confirmation", "evidence", "", "evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := samplingFixture(t, false)
			fixture.write(t, ".tmp/benchstat-primary.csv", test.primary)
			fixture.write(t, ".tmp/benchmark-confirmation-targets.tsv", test.targets)
			fixture.write(t, ".tmp/benchstat-confirmation.csv", test.confirmation)
			_, log, err := fixture.run(t, samplingPrefix(fixture, "")+gate)
			if err == nil || strings.Contains(log, "--approved-costs") {
				t.Fatalf("invalid evidence reached the gate: err=%v log=%s", err, log)
			}
		})
	}
}

func TestPerformanceArtifactRetainsTargetedConfirmationEvidence(t *testing.T) {
	for _, step := range readQualityWorkflow(t).Jobs["performance-samples"].Steps {
		if step.Name != "Preserve performance evidence" {
			continue
		}
		if step.If != "always()" || step.With["name"] != "performance-evidence-${{ matrix.shard }}-${{ github.run_attempt }}" {
			t.Fatal("failed and retried samples must retain attempt-specific evidence")
		}
		for _, path := range []string{
			".tmp/benchmark-confirmation-targets.tsv", ".tmp/benchmark-confirmation-batch.tsv",
			".tmp/benchmark-confirmation-expected.json", ".tmp/benchmark-expected.json", ".tmp/benchmark-batch.tsv",
			".tmp/benchmark-*.txt", ".tmp/benchstat-*.csv",
		} {
			if !strings.Contains(step.With["path"], path+"\n") {
				t.Errorf("performance artifact lost %s", path)
			}
		}
		return
	}
	t.Fatal("missing performance evidence upload")
}

func TestPerformancePlanArtifactUsesProducingAttempt(t *testing.T) {
	workflow := readQualityWorkflow(t)
	for _, step := range workflow.Jobs["performance-samples"].Steps {
		if step.Name != "Load benchmark batch" {
			continue
		}
		if step.Env["PLAN_ARTIFACT"] != "${{ needs.performance-scope.outputs.artifact }}" {
			t.Fatal("a retried sample job must load the original scope job's artifact")
		}
		for _, required := range []string{
			`gh run download "${RUN_ID}" --name "${PLAN_ARTIFACT}"`,
			`shard-${SHARD}.tsv`, `shard-${SHARD}.json`, `test -s .tmp/benchmark-batch.tsv`,
		} {
			if !strings.Contains(step.Run, required) {
				t.Errorf("plan loading lost %q", required)
			}
		}
		return
	}
	t.Fatal("missing benchmark plan loading")
}
