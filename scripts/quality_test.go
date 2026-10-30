package scripts_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type qualityEntry struct {
	name, script string
}

type qualityWorkflow struct {
	Jobs map[string]struct {
		If             string
		Needs          yaml.Node
		TimeoutMinutes int `yaml:"timeout-minutes"`
		Env            map[string]string
		Strategy       struct {
			FailFast *bool `yaml:"fail-fast"`
			Matrix   struct{ Dart []string }
		}
		Steps []struct {
			Name, Run, Uses, If string
			With, Env           map[string]string
			ContinueOnError     bool `yaml:"continue-on-error"`
		}
	}
}

func TestStorageCIRequiresRealS3(t *testing.T) {
	job := readQualityWorkflow(t).Jobs["test"]
	for key, value := range map[string]string{
		"STORAGE_ENDPOINT": "127.0.0.1:9000", "STORAGE_REGION": "us-east-1",
		"STORAGE_ACCESS_KEY": "luxo-ci-storage", "STORAGE_SECRET_KEY": "luxo-ci-storage-secret",
		"STORAGE_BUCKET": "luxo-ci-storage", "STORAGE_USE_SSL": "false",
	} {
		if job.Env[key] != value {
			t.Errorf("S3 CI environment %s = %q, want %q", key, job.Env[key], value)
		}
	}
	start, tests, stop := -1, -1, -1
	for i, step := range job.Steps {
		switch step.Name {
		case "Start MinIO and verify readiness":
			start = i
			for _, required := range []string{
				"luxo-ci-minio:7aac2a2c5b7c882e68c1ce017d8256be2feea27f",
				"127.0.0.1:9000:9000", "--tmpfs /data:", "MINIO_ROOT_USER=$STORAGE_ACCESS_KEY",
				"MINIO_ROOT_PASSWORD=$STORAGE_SECRET_KEY", "http://$STORAGE_ENDPOINT/minio/health/ready",
				"--fail", "--max-time 5",
			} {
				if !strings.Contains(step.Run, required) {
					t.Errorf("S3 CI startup lost %q", required)
				}
			}
			if step.ContinueOnError {
				t.Error("S3 startup failure must block tests")
			}
		case "Run tests with coverage":
			tests = i
		case "Stop MinIO test container":
			stop = i
			if step.If != "always()" || !strings.Contains(step.Run, "docker rm --force luxo-ci-minio") {
				t.Error("S3 cleanup must run even after test failure")
			}
		}
	}
	if start < 0 || tests <= start || stop <= tests {
		t.Fatalf("S3 lifecycle order invalid: start=%d tests=%d stop=%d", start, tests, stop)
	}
}

func TestStorageCIBuildsPinnedOfficialSource(t *testing.T) {
	job := readQualityWorkflow(t).Jobs["test"]
	checkout, build, start := -1, -1, -1
	for i, step := range job.Steps {
		switch step.Name {
		case "Checkout pinned MinIO source":
			checkout = i
			if step.With["repository"] != "minio/minio" || step.With["ref"] != "7aac2a2c5b7c882e68c1ce017d8256be2feea27f" || step.With["persist-credentials"] != "false" {
				t.Fatal("MinIO source must be pinned without credentials")
			}
		case "Build pinned MinIO test image":
			build = i
			for _, required := range []string{"set -euo pipefail", "GOWORK=off CGO_ENABLED=0 go build -mod=readonly", "FROM scratch", "docker build --tag luxo-ci-minio:7aac2a2c5b7c882e68c1ce017d8256be2feea27f"} {
				if !strings.Contains(step.Run, required) {
					t.Errorf("missing MinIO build constraint %q", required)
				}
			}
			if step.ContinueOnError {
				t.Error("MinIO build errors must block tests")
			}
		case "Start MinIO and verify readiness":
			start = i
		}
	}
	if checkout < 0 || build <= checkout || start <= build {
		t.Fatalf("invalid MinIO source lifecycle: %d %d %d", checkout, build, start)
	}
}

func TestStorageCIStartupPropagatesFailures(t *testing.T) {
	var startup string
	for _, step := range readQualityWorkflow(t).Jobs["test"].Steps {
		if step.Name == "Start MinIO and verify readiness" {
			startup = step.Run
		}
	}
	if startup == "" {
		t.Fatal("missing S3 startup command")
	}
	for _, failure := range []string{"none", "docker", "curl"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newQualityFixture(t)
			for _, tool := range []string{"docker", "curl"} {
				body := ""
				if tool == failure {
					body = "exit 42"
				}
				fixture.tool(t, tool, body, false)
			}
			output, log, err := fixture.run(t, "export STORAGE_ACCESS_KEY=key STORAGE_SECRET_KEY=secret STORAGE_ENDPOINT=127.0.0.1:9000\n"+startup)
			if (err != nil) != (failure != "none") {
				t.Fatalf("failure=%s err=%v output=%s log=%s", failure, err, output, log)
			}
			if failure == "docker" && strings.Contains(log, "curl ") {
				t.Fatal("startup continued after container failure")
			}
			if failure != "docker" && !strings.Contains(log, "curl ") {
				t.Fatal("startup did not probe S3 readiness")
			}
		})
	}
}

func readQualityWorkflow(t *testing.T) qualityWorkflow {
	t.Helper()
	var workflow qualityWorkflow
	if err := yaml.Unmarshal([]byte(readQualityFile(t, "../.github/workflows/test.yml")), &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func TestCompilerRaceCIIncludesGeneratedChildren(t *testing.T) {
	job := readQualityWorkflow(t).Jobs["race"]
	if job.TimeoutMinutes != 15 {
		t.Errorf("compiler race job needs a bounded 15 minute lifecycle, got %d", job.TimeoutMinutes)
	}
	found := 0
	for _, step := range job.Steps {
		if step.Name != "Verify compiler state isolation" {
			continue
		}
		found++
		if !slices.Contains(strings.Fields(step.Env["GOFLAGS"]), "-race") {
			t.Error("generated Go test children must inherit race instrumentation through GOFLAGS")
		}
		for _, required := range []string{"./pkg/codegen", "./pkg/semantic", "./pkg/lockfile", "-count=1", "-timeout=10m"} {
			if !strings.Contains(step.Run, required) {
				t.Errorf("compiler race command lost %q", required)
			}
		}
		if step.ContinueOnError {
			t.Error("compiler race failures must block the workflow")
		}
	}
	if found != 1 {
		t.Fatalf("expected exactly one compiler race step, got %d", found)
	}
}

func qualityEntries(t *testing.T) []qualityEntry {
	t.Helper()
	entries := []qualityEntry{
		{"pre-commit", readQualityFile(t, "pre-commit")},
		{"lint", readQualityFile(t, "lint.sh")},
	}
	workflow := readQualityWorkflow(t)
	var checks []string
	for _, step := range workflow.Jobs["lint"].Steps {
		if step.Name == "Gofmt check" || step.Name == "Gocyclo check" {
			checks = append(checks, step.Run)
		}
	}
	if len(checks) != 2 {
		t.Fatal("expected both formatting and complexity CI steps")
	}
	return append(entries, qualityEntry{"CI", "set -e\n" + strings.Join(checks, "\n")})
}

func TestSDKInteropCIIncludesBothTopologies(t *testing.T) {
	workflow := readQualityWorkflow(t)
	for job, consumer := range map[string]string{
		"sdk-kotlin":            "./packages/kotlin/gradlew -p tests/sdk-interop/kotlin build",
		"sdk-swift-conformance": "bash tests/sdk-interop/swift/run.sh",
	} {
		for _, mode := range []string{"-run --", "-run -gateway --", "-run -enum-upgrade --", "-run -enum-upgrade -gateway --"} {
			command := "go run ./tests/sdk-interop " + mode + " " + consumer
			found := 0
			for _, step := range workflow.Jobs[job].Steps {
				if strings.Contains(step.Run, command) {
					found++
					if step.ContinueOnError {
						t.Errorf("%s must not ignore %s failure", job, mode)
					}
				}
			}
			if found != 1 {
				t.Errorf("%s needs exactly one %s consumer step; got %d", job, mode, found)
			}
		}
	}
}

func TestSwiftInteropRequiresLockedDependencies(t *testing.T) {
	script := readQualityFile(t, "../tests/sdk-interop/swift/run.sh")
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			fixture := newQualityFixture(t)
			body := `locked=false
for arg in "$@"; do
  if [ "$arg" = --disable-automatic-resolution ]; then locked=true; fi
done
if [ "$locked" != true ]; then echo 'missing locked resolution'; exit 24; fi`
			if fail {
				body += "\necho locked-build-failed; exit 42"
			}
			fixture.tool(t, "swift", body, false)
			command := "export LUXO_INTEROP_SCRATCH=" + shellQualityQuote(string(fixture)) +
				" LUXO_SWIFT_PATH=" + shellQualityQuote(string(fixture)) + "\n" + script
			output, log, err := fixture.run(t, command)
			if (err != nil) != fail || !strings.Contains(log, "--disable-automatic-resolution") {
				t.Fatalf("Swift must require the lock and propagate failure: %v\n%s\n%s", err, output, log)
			}
			if fail && !strings.Contains(output, "locked-build-failed") {
				t.Fatalf("lost Swift build failure: %s", output)
			}
		})
	}
	ignored := readQualityFile(t, "../tests/sdk-interop/swift/.gitignore")
	if strings.Contains(ignored, "Package.resolved") {
		t.Fatal("Swift interop must not ignore its release dependency lock")
	}
}

func TestDartCIPinsBothSupportedToolchains(t *testing.T) {
	job := readQualityWorkflow(t).Jobs["sdk-dart"]
	versions := job.Strategy.Matrix.Dart
	if len(versions) != 2 || versions[0] != "3.13.0" || versions[1] != "3.13.4" {
		t.Fatalf("Dart CI must pin minimum and current stable versions, got %v", versions)
	}
	pubspec := readQualityFile(t, "../packages/dart/pubspec.yaml")
	if !strings.Contains(pubspec, `sdk: ">=`+versions[0]+` <4.0.0"`) {
		t.Fatal("CI minimum must match the declared SDK lower bound")
	}
	setups, reports, analyses, vm, web := 0, 0, 0, 0, 0
	for _, step := range job.Steps {
		if step.Uses == "dart-lang/setup-dart@v1" {
			setups++
			if step.With["sdk"] != "${{ matrix.dart }}" {
				t.Error("Dart setup must use the exact matrix version")
			}
		}
		if step.Name == "Verify Dart toolchain" {
			reports++
			if step.Env["DART_EXPECTED"] != "${{ matrix.dart }}" {
				t.Error("Dart verification must use the selected version")
			}
		}
		if strings.Contains(step.Run, "dart analyze --fatal-infos") {
			analyses++
		}
		if step.Run == "dart test --concurrency=1" {
			vm++
		}
		if strings.Contains(step.Run, "dart test -p node -c dart2js -c dart2wasm") {
			web++
		}
		if step.ContinueOnError {
			t.Errorf("Dart CI must not ignore %s failures", step.Name)
		}
	}
	if setups != 1 || reports != 1 || analyses != 1 || vm != 1 || web != 1 {
		t.Fatalf("expected one shared setup/report/analyze/VM/Web suite, got %d/%d/%d/%d/%d", setups, reports, analyses, vm, web)
	}
}

func TestNodeCIUsesVerifiedPinnedToolchain(t *testing.T) {
	workflow := readQualityWorkflow(t)
	for _, job := range []string{"sdk-ts", "sdk-dart"} {
		verified := false
		for _, step := range workflow.Jobs[job].Steps {
			if step.Uses == "actions/setup-node@v7" && step.With["node-version-file"] != ".node-version" {
				t.Errorf("%s must use the shared Node version file", job)
			}
			if strings.Contains(step.Run, "scripts/check-node.sh") && !step.ContinueOnError {
				verified = true
			}
		}
		if !verified {
			t.Errorf("%s must verify the installed Node version", job)
		}
	}
}

func TestNodeToolchainGate(t *testing.T) {
	helper := readQualityFile(t, "check-node.sh")
	for _, test := range []struct {
		name, version, body, diagnostic string
		pass                            bool
	}{
		{"matching", "26.9.0\n", "echo v26.9.0", "Node v26.9.0", true},
		{"missing newline", "26.9.0", "echo v26.9.0", "Node v26.9.0", true},
		{"old version", "26.9.0\n", "echo v26.4.0", "Expected Node v26.9.0", false},
		{"preview", "26.9.0\n", "echo v26.9.0-rc.1", "Expected Node v26.9.0", false},
		{"failed command", "26.9.0\n", "echo node-failed >&2; exit 3", "node-failed", false},
		{"empty pin", "", "echo v26.9.0", "Invalid Node version pin", false},
		{"floating pin", "node\n", "echo v26.9.0", "Invalid Node version pin", false},
		{"missing patch", "26.9\n", "echo v26.9.0", "Invalid Node version pin", false},
		{"extra component", "26.9.0.1\n", "echo v26.9.0", "Invalid Node version pin", false},
		{"empty component", "26..0\n", "echo v26.9.0", "Invalid Node version pin", false},
		{"leading dot", ".26.9\n", "echo v26.9.0", "Invalid Node version pin", false},
		{"trailing dot", "26.9.\n", "echo v26.9.0", "Invalid Node version pin", false},
		{"multiple lines", "26.9.0\n27.0.0\n", "echo v26.9.0", "Invalid Node version pin", false},
		{"second unterminated line", "26.9.0\n27.0.0", "echo v26.9.0", "Invalid Node version pin", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newQualityFixture(t)
			fixture.write(t, "scripts/check-node.sh", helper)
			fixture.write(t, ".node-version", test.version)
			fixture.tool(t, "node", test.body, false)
			for _, command := range []string{"sh scripts/check-node.sh", "cd scripts && sh check-node.sh"} {
				output, _, err := fixture.run(t, command)
				if (err == nil) != test.pass || !strings.Contains(output, test.diagnostic) {
					t.Errorf("unexpected result: %v\n%s", err, output)
				}
			}
		})
	}
}

func TestDartCIRejectsToolchainDrift(t *testing.T) {
	var script string
	for _, step := range readQualityWorkflow(t).Jobs["sdk-dart"].Steps {
		if step.Name == "Verify Dart toolchain" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("missing Dart toolchain verification step")
	}
	for _, test := range []struct {
		name, body string
		wantError  bool
	}{
		{"exact stdout", "echo 'Dart SDK version: 3.13.4 (stable) (date) on linux_x64'", false},
		{"exact stderr", "echo 'Dart SDK version: 3.13.4 (stable) (date) on linux_x64' >&2", false},
		{"older", "echo 'Dart SDK version: 3.13.3 (stable) (date) on linux_x64'", true},
		{"newer", "echo 'Dart SDK version: 3.13.5 (stable) (date) on linux_x64'", true},
		{"preview", "echo 'Dart SDK version: 3.13.4 (beta) (date) on linux_x64'", true},
		{"prefix collision", "echo 'Dart SDK version: 3.13.40 (stable) (date) on linux_x64'", true},
		{"unrecognized", "echo 'unexpected compiler'", true},
		{"empty", "", true},
		{"failed tool", "echo 'Dart SDK version: 3.13.4 (stable) (date) on linux_x64'; exit 2", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newQualityFixture(t)
			fixture.tool(t, "dart", test.body, false)
			output, log, err := fixture.run(t, "set -e\nexport DART_EXPECTED=3.13.4\n"+script)
			if (err != nil) != test.wantError {
				t.Fatalf("toolchain check error = %v; output:\n%s", err, output)
			}
			if log != "dart --version\n" {
				t.Errorf("verification should only inspect the tool, got %q", log)
			}
		})
	}
}

func TestQualityGatesRejectToolFailures(t *testing.T) {
	tests := []struct {
		name, tool, body, diagnostic string
		missing                      bool
	}{
		{"unformatted", "gofmt", "echo pkg/unformatted.go", "pkg/unformatted.go", false},
		{"format parse error", "gofmt", "echo 'gofmt: invalid syntax' >&2; exit 2", "gofmt: invalid syntax", false},
		{"format silent failure", "gofmt", "exit 2", "", false},
		{"format warning", "gofmt", "echo 'formatter warning' >&2", "formatter warning", false},
		{"missing formatter", "gofmt", "", "gofmt", true},
		{"complexity violation", "gocyclo", "echo '26 sample Complex pkg/sample.go:1:1'; exit 1", "26 sample Complex pkg/sample.go:1:1", false},
		{"unsupported syntax", "gocyclo", "echo 'gocyclo: method must have no type parameters' >&2; exit 1", "gocyclo: method must have no type parameters", false},
		{"complexity silent failure", "gocyclo", "exit 2", "", false},
		{"unexpected stdout", "gocyclo", "echo 'unexpected tool output'", "unexpected tool output", false},
		{"unexpected stderr", "gocyclo", "echo 'unexpected tool diagnostic' >&2", "unexpected tool diagnostic", false},
		{"missing complexity tool", "gocyclo", "", "gocyclo", true},
	}
	for _, entry := range qualityEntries(t) {
		for _, test := range tests {
			t.Run(entry.name+"/"+test.name, func(t *testing.T) {
				fixture := newQualityFixture(t)
				fixture.tool(t, test.tool, test.body, test.missing)
				output, log, err := fixture.run(t, entry.script)
				if err == nil {
					t.Errorf("invalid tool result passed the gate:\n%s", output)
				}
				if output == "" || !strings.Contains(output, test.diagnostic) {
					t.Errorf("missing diagnostic %q:\n%s", test.diagnostic, output)
				}
				blocked := []string{"go test", "go mod tidy"}
				if test.tool == "gofmt" {
					blocked = append(blocked, "go vet", "gocyclo")
				}
				assertQualityStopped(t, log, blocked...)
			})
		}
	}
}

func TestQualityGatesCheckEveryGoDirectory(t *testing.T) {
	for _, entry := range qualityEntries(t) {
		t.Run(entry.name, func(t *testing.T) {
			output, log, err := newQualityFixture(t).run(t, entry.script)
			if err != nil {
				t.Fatalf("clean tools rejected: %v\n%s", err, output)
			}
			for _, want := range []string{"gofmt -s -l pkg/ cmd/ scripts/ internal/\n", "gocyclo -over 25 pkg/ cmd/ scripts/ internal/\n"} {
				if strings.Count(log, want) != 1 {
					t.Errorf("expected exactly one %q, got:\n%s", want, log)
				}
			}
			if entry.name != "CI" && !strings.Contains(log, "go vet ./pkg/... ./cmd/... ./cmd/luxo/... ./scripts/... ./internal/... ./pkg/lux/pg/... ./pkg/lux/nats/... ./pkg/lux/cache/redis/... ./pkg/lux/storage/s3/...\n") {
				t.Errorf("vet must cover the same directories:\n%s", log)
			}
			if entry.name == "lint" && !strings.Contains(log, "go test ./pkg/... ./cmd/... ./scripts/... ./internal/...") {
				t.Errorf("lint must run gate regression tests:\n%s", log)
			}
			if entry.name == "lint" {
				for _, module := range []string{"cmd/luxo", "pkg/lux/pg", "pkg/lux/nats", "pkg/lux/cache/redis", "pkg/lux/storage/s3"} {
					if !strings.Contains(log, "go test ./"+module+"/...") {
						t.Errorf("lint skipped nested module %s:\n%s", module, log)
					}
				}
			}
		})
	}
}

func TestCIGoCommandsIncludeInternalPackages(t *testing.T) {
	workflow := readQualityWorkflow(t)
	for _, test := range []struct{ job, step, command string }{
		{"test", "Run tests with coverage", "go test ./pkg/... ./cmd/... ./scripts/... ./internal/..."},
		{"lint", "Vet", "go vet ./pkg/... ./cmd/... ./cmd/luxo/... ./scripts/... ./internal/... ./pkg/lux/pg/... ./pkg/lux/nats/... ./pkg/lux/cache/redis/... ./pkg/lux/storage/s3/..."},
		{"lint", "Build", "go build ./pkg/... ./cmd/... ./cmd/luxo/... ./scripts/... ./internal/... ./pkg/lux/pg/... ./pkg/lux/nats/... ./pkg/lux/cache/redis/... ./pkg/lux/storage/s3/..."},
	} {
		t.Run(test.step, func(t *testing.T) {
			found := 0
			for _, step := range workflow.Jobs[test.job].Steps {
				if step.Name != test.step {
					continue
				}
				found++
				if !strings.Contains(step.Run, test.command) || step.ContinueOnError {
					t.Errorf("CI must enforce internal package checks: %s", step.Run)
				}
			}
			if found != 1 {
				t.Fatalf("expected one %q step, got %d", test.step, found)
			}
		})
	}
}

func TestQualityGatesStopAfterGoFailure(t *testing.T) {
	for _, entry := range qualityEntries(t)[:2] {
		for _, failed := range []string{"vet", "test", "mod tidy"} {
			if entry.name == "lint" && failed == "mod tidy" || entry.name == "pre-commit" && failed == "test" {
				continue
			}
			t.Run(entry.name+"/"+failed, func(t *testing.T) {
				fixture := newQualityFixture(t)
				body := fmt.Sprintf("case \"$*\" in %s*) echo 'go stage failed' >&2; exit 2;; esac", shellQualityQuote(failed))
				fixture.tool(t, "go", body, false)
				output, log, err := fixture.run(t, entry.script)
				if err == nil || !strings.Contains(output, "go stage failed") {
					t.Errorf("Go failure was not reported: %v\n%s", err, output)
				}
				if failed == "vet" {
					assertQualityStopped(t, log, "gocyclo", "go mod tidy", "go test")
				}
			})
		}
	}
}

func assertQualityStopped(t *testing.T, log string, blocked ...string) {
	t.Helper()
	for _, command := range blocked {
		if strings.Contains(log, command) {
			t.Errorf("ran %q after failure:\n%s", command, log)
		}
	}
}

func TestQualityHelperArguments(t *testing.T) {
	for _, args := range []string{"", "format", "unknown pkg/", "'' pkg/"} {
		t.Run(args, func(t *testing.T) {
			output, log, err := newQualityFixture(t).run(t, "sh scripts/check-go.sh "+args)
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) || exitError.ExitCode() != 2 {
				t.Errorf("expected usage exit 2, got %v\n%s", err, output)
			}
			if output == "" || log != "" {
				t.Errorf("invalid arguments must report an error without running tools: output=%q, log=%q", output, log)
			}
		})
	}
	for _, mode := range []string{"format", "complexity"} {
		t.Run(mode+" quoted paths", func(t *testing.T) {
			fixture := newQualityFixture(t)
			body := "for checked_path in \"$@\"; do printf '<%s>\\n' \"$checked_path\" >> \"$QUALITY_LOG\"; done"
			fixture.tool(t, "gofmt", body, false)
			fixture.tool(t, "gocyclo", body, false)
			output, log, err := fixture.run(t, "sh scripts/check-go.sh "+mode+" 'path with space/' 'literal*path/'")
			if err != nil || output != "" {
				t.Fatalf("valid arguments rejected: %v\n%s", err, output)
			}
			if !strings.Contains(log, "<path with space/>\n<literal*path/>\n") {
				t.Errorf("path arguments were split or expanded:\n%s", log)
			}
		})
	}
}

type qualityFixture string

func newQualityFixture(t *testing.T) qualityFixture {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(tmp, "quality gate-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(directory) != tmp {
			t.Fatal("refusing to remove a directory outside project .tmp")
		}
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	fixture := qualityFixture(directory)
	fixture.write(t, "bin/sh", "#!/bin/sh\nexec /bin/sh \"$@\"\n")
	for _, tool := range []string{"go", "gofmt", "gocyclo"} {
		fixture.tool(t, tool, "", false)
	}
	fixture.write(t, "scripts/check-go.sh", readQualityFile(t, "check-go.sh"))
	return fixture
}

func (fixture qualityFixture) tool(t *testing.T, name, body string, missing bool) {
	t.Helper()
	path := "bin/" + name
	if missing {
		if err := os.Remove(filepath.Join(string(fixture), path)); err != nil {
			t.Fatal(err)
		}
		return
	}
	fixture.write(t, path, "#!/bin/sh\nprintf '"+name+" %s\\n' \"$*\" >> \"$QUALITY_LOG\"\n"+body+"\n")
}

func (fixture qualityFixture) write(t *testing.T, name, body string) {
	t.Helper()
	path := filepath.Join(string(fixture), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (fixture qualityFixture) run(t *testing.T, script string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/bash", "-c", script)
	command.Dir = string(fixture)
	logFile := filepath.Join(string(fixture), "calls.log")
	command.Env = []string{"PATH=" + filepath.Join(string(fixture), "bin"), "QUALITY_LOG=" + logFile}
	output, err := command.CombinedOutput()
	log, readErr := os.ReadFile(logFile)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatal(readErr)
	}
	return string(output), string(log), err
}

func readQualityFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func shellQualityQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestSDKInteropBindsCandidateModules(t *testing.T) {
	workflow := readQualityWorkflow(t)
	for _, job := range []string{"sdk-ts", "sdk-dart", "sdk-kotlin", "sdk-swift-conformance"} {
		t.Run(job, func(t *testing.T) {
			goSetup, bindings, firstConsumer, count := -1, -1, -1, 0
			for index, step := range workflow.Jobs[job].Steps {
				if strings.HasPrefix(step.Uses, "actions/setup-go@") {
					goSetup = index
				}
				if step.Uses == "./.github/actions/setup-modules" {
					bindings = index
					count++
					condition := ""
					if job == "sdk-ts" {
						condition = "matrix.package == 'vite-plugin'"
					}
					if step.If != condition || step.ContinueOnError {
						t.Error("module setup must match Go setup and propagate failures")
					}
				}
				consumer := strings.Contains(step.Run, "./tests/sdk-interop") ||
					strings.Contains(step.Run, "pnpm test") || strings.Contains(step.Run, "./gradlew build")
				if consumer && firstConsumer < 0 {
					firstConsumer = index
				}
			}
			if count != 1 || goSetup < 0 || bindings <= goSetup || firstConsumer <= bindings {
				t.Fatalf("invalid module setup order: go=%d bindings=%d consumer=%d count=%d", goSetup, bindings, firstConsumer, count)
			}
		})
	}
}
