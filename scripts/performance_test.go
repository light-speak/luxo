package scripts_test

import (
	"fmt"
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
	if job.TimeoutMinutes != 30 || job.Strategy.FailFast == nil || *job.Strategy.FailFast {
		t.Fatal("every batch must finish independently within the existing timeout")
	}
	if job.Needs.Value != "performance-scope" || job.If != "needs.performance-scope.outputs.run == 'true'" {
		t.Fatal("sampling must require the successful scope and plan job")
	}
	script := performanceStep(t, "performance-samples", "Run adaptive interleaved benchmark samples")
	for _, required := range []string{
		"{1..10}", "-test.benchtime=200ms", "-test.cpu=2", "-test.benchmem",
		`-test.bench="${filter}"`, "-alpha 0.01", "run_group confirmation",
		"--verify .tmp/benchmark-expected.json .tmp/benchmark-primary-base.txt .tmp/benchmark-primary-head.txt",
		"--verify .tmp/benchmark-expected.json .tmp/benchmark-confirmation-base.txt .tmp/benchmark-confirmation-head.txt",
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
	fixture.write(t, ".tmp/benchmark-batch.tsv", "pkg/a\t^(BenchmarkA|BenchmarkB)$\n")
	for _, revision := range []string{"base", "head"} {
		fixture.write(t, ".tmp/"+revision+"/pkg/a/keep", "")
		fixture.write(t, ".tmp/bin/"+revision+"/pkg-a.test", "#!/bin/sh\nprintf '"+revision+" %s\\n' \"$*\" >> \"$QUALITY_LOG\"\n")
	}
	fixture.write(t, ".tmp/bin/benchstat", "#!/bin/sh\nexit 0\n")
	fixture.write(t, ".tmp/bin/benchrun", "#!/bin/sh\nset -eu\ntest \"$1\" = run\nbenchmark_bin=$3\nshift 4\nexec \"$benchmark_bin/pkg-a.test\" \"$@\"\n")
	fixture.tool(t, "go", fmt.Sprintf(`case "$*" in *--requires-confirmation*) echo %t ;; esac`, confirmation), false)
	return fixture
}

func TestPerformanceSamplingAlternatesBothRevisions(t *testing.T) {
	script := performanceStep(t, "performance-samples", "Run adaptive interleaved benchmark samples")
	for _, confirmation := range []bool{false, true} {
		fixture := samplingFixture(t, confirmation)
		output, log, err := fixture.run(t, "export GITHUB_WORKSPACE="+shellQualityQuote(string(fixture))+"\n"+script)
		if err != nil {
			t.Fatalf("sampling failed: %v\n%s", err, output)
		}
		var runs []string
		for _, line := range strings.Split(log, "\n") {
			if strings.HasPrefix(line, "base ") || strings.HasPrefix(line, "head ") {
				runs = append(runs, line)
			}
		}
		groups := 1
		if confirmation {
			groups = 2
		}
		if len(runs) != groups*20 {
			t.Fatalf("got %d runs", len(runs))
		}
		for i, line := range runs {
			want := "base"
			if i%4 == 1 || i%4 == 2 {
				want = "head"
			}
			if !strings.HasPrefix(line, want+" ") || !strings.Contains(line, "-test.bench=^(BenchmarkA|BenchmarkB)$") {
				t.Fatalf("run %d lost order/filter: %s", i, line)
			}
		}
		if strings.Count(log, "./scripts/benchplan --verify") != groups {
			t.Fatal("each sample group must verify completeness")
		}
	}
}

func TestPerformanceSamplingStopsOnProcessFailure(t *testing.T) {
	fixture := samplingFixture(t, false)
	fixture.write(t, ".tmp/bin/base/pkg-a.test", "#!/bin/sh\nexit 42\n")
	script := performanceStep(t, "performance-samples", "Run adaptive interleaved benchmark samples")
	_, log, err := fixture.run(t, "export GITHUB_WORKSPACE="+shellQualityQuote(string(fixture))+"\n"+script)
	if err == nil || strings.Contains(log, "head ") || strings.Contains(log, "--requires-confirmation") {
		t.Fatalf("continued after failed benchmark: err=%v log=%s", err, log)
	}
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
