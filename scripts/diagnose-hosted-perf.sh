#!/usr/bin/env bash
set -euo pipefail

base=65f1892e175487503aa36caa86d1eb8a0805f073
pr74=eea8d680fb411e650c15bc655632cf9041a2e964
pr78=0a5dc510b726b9078c462b0907138786adf223ed
benchstat_version=v0.0.0-20260825160852-19be9d8e6c70
benchtime=200ms

write_cases() {
  if [[ "$suite" == pr74 ]]; then
    cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/lux/api	default	BenchmarkStaticValueParams/Duration/JSON/static-2	^BenchmarkStaticValueParams$/^Duration$/^JSON$/^static$
02	pkg/lux/api	default	BenchmarkStaticValueParams/Duration/Binary/static-2	^BenchmarkStaticValueParams$/^Duration$/^Binary$/^static$
CASES
    local case_id=3 label
    for label in short-positive short-negative large-positive large-negative max-int64 min-int64 syntax-error positive-overflow negative-overflow leading-zero; do
      printf '%02d\tpkg/lux/jsonutil\tjson-int\tBenchmarkDiagnosticParseInt64/%s-2\t^BenchmarkDiagnosticParseInt64$/^%s$\n' \
        "$case_id" "$label" "$label" >> "$evidence/cases.tsv"
      case_id=$((case_id + 1))
    done
  else
    cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/lux/h2transport	default	BenchmarkTaskDeadlineReplacement/1/conditional-2	^BenchmarkTaskDeadlineReplacement$/^1$/^conditional$
02	pkg/lux/luvia	default	BenchmarkGatewayIdentity/anonymous/before-2	^BenchmarkGatewayIdentity$/^anonymous$/^before$
03	pkg/lux/luvia	default	BenchmarkGatewayIdentity/anonymous/after-2	^BenchmarkGatewayIdentity$/^anonymous$/^after$
04	pkg/lux/h2transport	shutdown	BenchmarkShutdownOneOpeningConnection-2	^BenchmarkShutdownOneOpeningConnection$
05	pkg/lux/h2transport	default	BenchmarkTaskDeadlineReplacement/8/conditional-2	^BenchmarkTaskDeadlineReplacement$/^8$/^conditional$
06	pkg/lux/h2transport	default	BenchmarkTaskDeadlineReplacement/128/conditional-2	^BenchmarkTaskDeadlineReplacement$/^128$/^conditional$
CASES
  fi
  python3 - "$evidence" <<'PYTHON'
import json
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
rows = []
for line in (evidence / "cases.tsv").read_text().splitlines():
    _, package, _, name, _ = line.split("\t")
    rows.append({"Package": package, "Name": name})
(evidence / "expected.json").write_text(json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n")
PYTHON
}

compile_revision() {
  local revision="$1" commit="$2" package
  shift 2
  mkdir -p "$scratch/source/$revision" "$scratch/bin/$revision"
  git -C "$repo" archive "$commit" | tar -xf - -C "$scratch/source/$revision"
  git -C "$repo" show -s --format="$revision %H %T" "$commit" >> "$evidence/revisions.txt"
  if [[ "$revision" == head ]]; then
    apply_optimization
  fi
  (
    set -x
    sh "$scratch/source/base/scripts/setup-modules.sh" "$scratch/source/$revision"
    if [[ "$revision" == base ]]; then
      cd "$scratch/source/base"
      go build -o "$scratch/bin/benchrun" ./scripts/benchrun
      go build -o "$scratch/bin/benchplan" ./scripts/benchplan
      go build -o "$scratch/bin/benchgate" ./scripts/benchgate
      GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
      go version -m "$scratch/bin/benchstat"
    fi
    for package; do
      "$scratch/bin/benchrun" compile "$scratch/source/$revision" "$scratch/bin/$revision" "$package"
    done
  ) >> "$evidence/compile-$revision.log" 2>&1
  cp "$scratch/source/$revision/go.work" "$evidence/go-$revision.work.txt"
  record_binaries "$revision"
}

apply_optimization() {
  python3 - "$repo/scripts/diagnose-optimization-patches.json" "$suite" "$scratch/source/head" "$evidence" <<'PYTHON'
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

bundle, suite, source, evidence = sys.argv[1:]
source, evidence = Path(source), Path(evidence)
asset = json.loads(Path(bundle).read_text())[suite]
patch = asset["patch"].encode()
if hashlib.sha256(patch).hexdigest() != asset["sha256"]:
    raise SystemExit("optimization patch SHA-256 changed")
patch_path = evidence / "optimization.patch"
patch_path.write_bytes(patch)
target = source / asset["source"]
original = hashlib.sha256(target.read_bytes()).hexdigest()
env = dict(os.environ, GIT_CEILING_DIRECTORIES=str(source.parent))
for flags in (["--check"], []):
    subprocess.run(["git", "-C", str(source), "apply", *flags, str(patch_path)], env=env, check=True)
applied = hashlib.sha256(target.read_bytes()).hexdigest()
if original == applied:
    raise SystemExit("optimization did not change its target source")
(evidence / "optimization.json").write_text(json.dumps({
    "suite": suite, "patchSHA256": asset["sha256"], "source": asset["source"],
    "originalSHA256": original, "appliedSHA256": applied,
}, indent=2) + "\n")
PYTHON
}

compile_diagnostic_fixture() {
  local revision="$1" variant="$2" package="$3" virtual_name="$4" fixture_name="$5"
  local overlay="$scratch/$variant-$revision.json" source="$scratch/source/$revision"
  python3 - "$source/$package/$virtual_name" "$evidence/$fixture_name" <<'PYTHON' > "$overlay"
import json
from pathlib import Path
import sys

virtual, fixture = (Path(value) for value in sys.argv[1:])
if virtual.exists():
    raise SystemExit(f"diagnostic overlay must add a new file: {virtual}")
print(json.dumps({"Replace": {str(virtual): str(fixture)}}))
PYTHON
  (
    set -x
    GOFLAGS="-overlay=$overlay" "$scratch/bin/benchrun" compile \
      "$source" "$scratch/bin/$revision-$variant" "$package"
  ) > "$evidence/compile-$revision-$variant.log" 2>&1
  cp "$overlay" "$evidence/$variant-$revision-overlay.json"
  record_binaries "$revision-$variant"
}

record_binaries() {
  local revision="$1" binary
  (
    cd "$scratch/bin/$revision"
    sha256sum ./*.test
    go version -m ./*.test
    for binary in ./*.test; do
      printf '%s ' "$binary"
      go tool buildid "$binary"
    done
  ) > "$evidence/binaries-$revision.txt"
}

run_leg() {
  local group="$1" sample="$2" comparison="$3" side="$4" revision="$5"
  local case_id="$6" package="$7" variant="$8" filter="$9" filename
  local binaries="$scratch/bin/$revision"
  if [[ "$variant" != default ]]; then
    binaries+="-$variant"
  fi
  filename="raw/$group.$sample.$comparison.$case_id.$side.txt"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$group" "$sample" "$comparison" "$case_id" "$side" "$revision" "$variant" "$filename" >> "$evidence/order.tsv"
  "$scratch/bin/benchrun" run "$scratch/source/$revision" "$binaries" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem \
    -test.benchtime="$benchtime" -test.count=1 -test.cpu=2 -test.timeout=90s \
    > "$evidence/$filename" 2>> "$evidence/benchmark-stderr.txt"
}

run_pair() {
  local group="$1" sample="$2" comparison="$3" head="$4" first="$5"
  local case_id="$6" package="$7" variant="$8" filter="$9"
  if [[ "$first" == left ]]; then
    run_leg "$group" "$sample" "$comparison" left base "$case_id" "$package" "$variant" "$filter"
    run_leg "$group" "$sample" "$comparison" right "$head" "$case_id" "$package" "$variant" "$filter"
  else
    run_leg "$group" "$sample" "$comparison" right "$head" "$case_id" "$package" "$variant" "$filter"
    run_leg "$group" "$sample" "$comparison" left base "$case_id" "$package" "$variant" "$filter"
  fi
}

sample_groups() {
  local group group_offset sample case_id package variant name filter first comparison revision
  local -a kind
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    group_offset=0
    if [[ "$group" == confirmation ]]; then
      group_offset=1
    fi
    for sample in {1..10}; do
      printf -v sample '%02d' "$sample"
      while IFS=$'\t' read -r case_id package variant name filter; do
        first=right
        if (( (10#$sample + group_offset) % 2 == 1 )); then
          first=left
        fi
        if (( (10#$sample + 10#$case_id + group_offset) % 2 == 1 )); then
          kind=(aa ab)
        else
          kind=(ab aa)
        fi
        for comparison in "${kind[@]}"; do
          revision=head
          if [[ "$comparison" == aa ]]; then
            revision=base
          fi
          run_pair "$group" "$sample" "$comparison" "$revision" "$first" "$case_id" "$package" "$variant" "$filter"
        done
      done < "$evidence/cases.tsv"
    done
  done
}

normalize_group() {
  local comparison="$1" group="$2" side
  local stem="$evidence/$comparison.$group"
  for side in left right; do
    cat "$evidence"/raw/"$group".*."$comparison".*."$side".txt > "$scratch/combined.txt"
    "$scratch/bin/benchplan" --normalize "$scratch/combined.txt" "$stem.$side.txt"
  done
  "$scratch/bin/benchplan" --verify "$evidence/expected.json" "$stem.left.txt" "$stem.right.txt"
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.left.txt" head="$stem.right.txt" > "$stem.csv" 2> "$stem.warnings.txt"
  "$scratch/bin/benchgate" --requires-confirmation "$stem.csv" > "$stem.requires-confirmation.txt"
}

analyze_groups() {
  local comparison status
  local -a gate_args
  printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for comparison in aa ab; do
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    gate_args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
    if [[ "$comparison" != aa ]]; then
      gate_args=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base" "${gate_args[@]}")
    fi
    status=0
    "$scratch/bin/benchgate" "${gate_args[@]}" \
      > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
    printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
    if (( status != 0 )); then
      grep -q '^performance gate rejected [0-9][0-9]* regression(s)$' "$evidence/$comparison.benchgate.log" ||
        return "$status"
    fi
  done
  cat "$evidence/verdicts.tsv"
}

collect_host() {
  uname -a
  lscpu
  go version
  go env -json GOOS GOARCH GOAMD64 CGO_ENABLED GOEXPERIMENT GOFLAGS CC
  printf 'image=%s image_version=%s runner_arch=%s\n' "${ImageOS:-}" "${ImageVersion:-}" "${RUNNER_ARCH:-}"
  printf 'workflow_revision=%s run=%s attempt=%s\n' "${GITHUB_SHA:-}" "${GITHUB_RUN_ID:-}" "${GITHUB_RUN_ATTEMPT:-}"
  if [[ -r /sys/fs/cgroup/cpu.max ]]; then
    cat /sys/fs/cgroup/cpu.max
  fi
  date -u '+%Y-%m-%dT%H:%M:%SZ'
}

cleanup() {
  rm -rf -- "$scratch"
}

main() {
  local revision package
  local -a packages
  suite="${1:?usage: diagnose-hosted-perf.sh pr74|pr78}"
  case "$suite" in
    pr74) head="$pr74" ;;
    pr78) head="$pr78" ;;
    *) printf 'Unknown diagnostic suite %s\n' "$suite" >&2; return 1 ;;
  esac
  repo=$(git rev-parse --show-toplevel)
  evidence="$repo/.tmp/hosted-perf-evidence"
  mkdir -p "$repo/.tmp"
  mkdir "$evidence"
  scratch=$(mktemp -d "$repo/.tmp/hosted-perf.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  export TMPDIR="$scratch/tmp" GOWORK=off
  mkdir "$TMPDIR"
  [[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64)" == $'linux\namd64\nv1' ]]
  collect_host > "$evidence/environment.txt"
  [[ -z "$(go env GOFLAGS)" ]]
  for revision in "$base" "$head"; do
    [[ "$(git -C "$repo" cat-file -t "$revision")" == commit ]]
  done
  git -C "$repo" merge-base --is-ancestor "$base" "$head"
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
Same runner, two groups (primary, confirmation), 10 alternating pairs per group for each case in A/A and A/B.
A/A uses the same baseline binary for both legs. A/B compares baseline with the pinned PR archive plus its exact optimization patch.
The copied optimization patch and original/applied source SHA-256 are retained. Native default linking is unchanged.
JSON integer diagnostics use the identical overlay on both revisions; all ten cases check values and error classes.
benchmem, 200ms, count=1, cpu=2, test timeout=90s, benchstat alpha=0.01.
benchgate: significant time increase >5%, significant bytes or allocations increase >0%, same unit flagged in both groups.
A/B uses that revision's scoped approved-cost policy. A/A has no approved costs.
The GatewayIdentity before/after fields are fixture variants. Left/right identifies the binary comparison.
The opening-connection Shutdown case uses an identical test overlay and separate binaries on both revisions.
A threshold failure is recorded for diagnosis. A missing case or invalid sample fails this workflow.
PROTOCOL
  write_cases
  packages=()
  while IFS= read -r package; do
    packages+=("$package")
  done < <(cut -f2 "$evidence/cases.tsv" | sort -u)
  compile_revision base "$base" "${packages[@]}"
  compile_revision head "$head" "${packages[@]}"
  if [[ "$suite" == pr78 ]]; then
    cp "$repo/scripts/diagnose-h2-shutdown-opening.go.txt" "$evidence/shutdown-fixture.go.txt"
    sha256sum "$evidence/shutdown-fixture.go.txt" > "$evidence/shutdown-fixture-sha256.txt"
    for revision in base head; do
      compile_diagnostic_fixture "$revision" shutdown pkg/lux/h2transport shutdown_opening_diagnostic_test.go shutdown-fixture.go.txt
    done
  else
    cp "$repo/scripts/diagnose-json-int.go.txt" "$evidence/json-int-fixture.go.txt"
    sha256sum "$evidence/json-int-fixture.go.txt" > "$evidence/json-int-fixture-sha256.txt"
    for revision in base head; do
      compile_diagnostic_fixture "$revision" json-int pkg/lux/jsonutil integer_diagnostic_test.go json-int-fixture.go.txt
    done
  fi
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  analyze_groups
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
