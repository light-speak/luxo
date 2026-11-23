#!/usr/bin/env bash
set -euo pipefail

context_script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$context_script_dir/diagnose-hosted-perf.sh"

base=65f1892e175487503aa36caa86d1eb8a0805f073
head=f86453bc03d6ef7d0210aeb414c5e9a7dd540ed0
package=pkg/lux/api
target=BenchmarkBinaryListPayload/String/count=0/version=0-2
context_arms=(prefix-aa prefix-ab isolated-aa isolated-ab)

write_context_plan() {
  # Execution order from the first API process of run 45157471703, shard 03.
  cat > "$evidence/prefix.names" <<'PREFIX'
BenchmarkBinaryParamLookup/n=1/stride=1/direct=false-2
BenchmarkBinaryParamLookup/n=1/stride=1/direct=true-2
BenchmarkBinaryParamLookup/n=1/stride=1001/direct=false-2
BenchmarkBinaryParamLookup/n=1/stride=1001/direct=true-2
BenchmarkBinaryParamLookup/n=1/stride=65537/direct=false-2
BenchmarkBinaryParamLookup/n=1/stride=65537/direct=true-2
BenchmarkBinaryParamLookup/n=16/stride=1/direct=false-2
BenchmarkBinaryParamLookup/n=16/stride=1/direct=true-2
BenchmarkBinaryParamLookup/n=16/stride=1001/direct=false-2
BenchmarkBinaryParamLookup/n=16/stride=1001/direct=true-2
BenchmarkBinaryParamLookup/n=16/stride=65537/direct=false-2
BenchmarkBinaryParamLookup/n=16/stride=65537/direct=true-2
BenchmarkBinaryListPayload/Int/count=0/version=0-2
BenchmarkBinaryListPayload/Int/count=0/version=1-2
BenchmarkBinaryListPayload/Int/count=1/version=0-2
BenchmarkBinaryListPayload/Int/count=1/version=1-2
BenchmarkBinaryListPayload/Int/count=128/version=0-2
BenchmarkBinaryListPayload/Int/count=128/version=1-2
BenchmarkBinaryListPayload/Duration/count=0/version=0-2
BenchmarkBinaryListPayload/Duration/count=0/version=1-2
BenchmarkBinaryListPayload/Duration/count=1/version=0-2
BenchmarkBinaryListPayload/Duration/count=1/version=1-2
BenchmarkBinaryListPayload/Duration/count=128/version=0-2
BenchmarkBinaryListPayload/Duration/count=128/version=1-2
BenchmarkBinaryListPayload/Float/count=0/version=0-2
BenchmarkBinaryListPayload/Float/count=0/version=1-2
BenchmarkBinaryListPayload/Float/count=1/version=0-2
BenchmarkBinaryListPayload/Float/count=1/version=1-2
BenchmarkBinaryListPayload/Float/count=128/version=0-2
BenchmarkBinaryListPayload/Float/count=128/version=1-2
BenchmarkBinaryListPayload/DateTime/count=0/version=0-2
BenchmarkBinaryListPayload/DateTime/count=0/version=1-2
BenchmarkBinaryListPayload/DateTime/count=1/version=0-2
BenchmarkBinaryListPayload/DateTime/count=1/version=1-2
BenchmarkBinaryListPayload/DateTime/count=128/version=0-2
BenchmarkBinaryListPayload/DateTime/count=128/version=1-2
BenchmarkBinaryListPayload/String/count=0/version=0-2
PREFIX
  printf '%s\n' "$target" > "$evidence/isolated.names"
  python3 - "$evidence" "$package" "$target" <<'PYTHON'
import json
from pathlib import Path
import re
import sys

directory, package, target = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
for mode, expected_count in (("prefix", 37), ("isolated", 1)):
    names = (directory / f"{mode}.names").read_text().splitlines()
    if len(set(names)) != expected_count or len(names) != expected_count or names[-1] != target:
        raise SystemExit(f"invalid {mode} benchmark list")
    rows = [{"Package": package, "Name": name} for name in names]
    (directory / f"{mode}.expected.json").write_text(json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n")
    filters = [
        "/".join("^" + re.escape(component) + "$" for component in name.removesuffix("-2").split("/"))
        for name in sorted(names)
    ]
    (directory / f"{mode}.filter").write_text("|".join(filters) + "\n")
PYTHON
}

run_context_leg() {
  local arm="$1" group="$2" sample="$3" side="$4" revision="$5"
  local filter filename
  filter=$(< "$evidence/${arm%-*}.filter")
  filename="raw/$arm.$group.$sample.$side.txt"
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$arm" "$group" "$sample" "$side" "$revision" "$filename" >> "$evidence/order.tsv"
  "$scratch/bin/benchrun" run "$scratch/source/$revision" "$scratch/bin/$revision" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem \
    -test.benchtime="$benchtime" -test.count=1 -test.cpu=2 -test.timeout=90s \
    > "$evidence/$filename" 2>> "$evidence/benchmark-stderr.txt"
}

sample_contexts() {
  local group group_offset=0 sample offset arm index revision
  mkdir "$evidence/raw"
  printf 'arm\tgroup\tpair\tside\trevision\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    for sample in {1..10}; do
      printf -v sample '%02d' "$sample"
      for ((offset=0; offset<${#context_arms[@]}; offset++)); do
        index=$(( (10#$sample - 1 + group_offset + offset) % ${#context_arms[@]} ))
        arm="${context_arms[index]}"
        revision=head
        if [[ "$arm" == *-aa ]]; then
          revision=base
        fi
        if (( (10#$sample + group_offset) % 2 == 1 )); then
          run_context_leg "$arm" "$group" "$sample" left base
          run_context_leg "$arm" "$group" "$sample" right "$revision"
        else
          run_context_leg "$arm" "$group" "$sample" right "$revision"
          run_context_leg "$arm" "$group" "$sample" left base
        fi
      done
    done
    group_offset=1
  done
}

validate_process() {
  local raw="$1" mode="$2" full="$3" selection="$4"
  local normalized="$scratch/current-process.txt"
  rm -f -- "$normalized"
  "$scratch/bin/benchplan" --normalize "$raw" "$normalized"
  awk '/^Benchmark/ { print $1 }' "$normalized" > "$scratch/current-order.txt"
  if ! cmp -s "$evidence/$mode.names" "$scratch/current-order.txt"; then
    printf 'Unexpected benchmark order in %s\n' "$raw" >&2
    diff -u "$evidence/$mode.names" "$scratch/current-order.txt" >&2 || true
    return 1
  fi
  awk -v target="$target" -v path="$raw" -v package="github.com/light-speak/luxo/$package" '
    /^goos: / { os++ }
    /^goarch: / { arch++ }
    /^cpu: / { cpu++ }
    /^pkg: / { packages++; if ($2 == package) matches++ }
    /^PASS$/ { passes++ }
    $1 == target || /^(goos|goarch|pkg|cpu): / || /^PASS$/ { print }
    END {
      if (os != 1 || arch != 1 || cpu != 1 || packages != 1 || matches != 1 || passes != 1) {
        print "Incomplete process headers or PASS in " path > "/dev/stderr"
        exit 1
      }
    }
  ' "$normalized" >> "$selection"
  cat "$normalized" >> "$full"
}

analyze_context_group() {
  local arm="$1" group="$2" mode="${1%-*}" side sample
  local stem="$evidence/$arm.$group"
  local full="$scratch/full-$arm.$group"
  for side in left right; do
    : > "$full.$side.txt"
    : > "$stem.$side.txt"
    for sample in {1..10}; do
      printf -v sample '%02d' "$sample"
      validate_process "$evidence/raw/$arm.$group.$sample.$side.txt" "$mode" "$full.$side.txt" "$stem.$side.txt"
    done
  done
  "$scratch/bin/benchplan" --verify "$evidence/$mode.expected.json" "$full.left.txt" "$full.right.txt"
  "$scratch/bin/benchplan" --verify "$evidence/isolated.expected.json" "$stem.left.txt" "$stem.right.txt"
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.left.txt" head="$stem.right.txt" > "$stem.csv" 2> "$stem.warnings.txt"
}

analyze_contexts() {
  local arm status invalid_control=0
  local -a policy
  printf 'arm\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for arm in "${context_arms[@]}"; do
    analyze_context_group "$arm" primary
    analyze_context_group "$arm" confirmation
    policy=("$evidence/$arm.primary.csv" "$evidence/$arm.confirmation.csv")
    if [[ "$arm" == *-ab ]]; then
      policy=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base" "${policy[@]}")
    fi
    status=0
    "$scratch/bin/benchgate" "${policy[@]}" > "$evidence/$arm.benchgate.log" 2>&1 || status=$?
    printf '%s\t%s\n' "$arm" "$status" >> "$evidence/verdicts.tsv"
    if (( status != 0 )); then
      grep -q '^performance gate rejected [0-9][0-9]* regression(s)$' "$evidence/$arm.benchgate.log" ||
        return "$status"
      if [[ "$arm" == *-aa ]]; then
        invalid_control=1
      fi
    fi
  done
  cat "$evidence/verdicts.tsv"
  if (( invalid_control )); then
    printf 'A/A control failed; the diagnostic comparison is inconclusive.\n' >&2
    return 1
  fi
}

context_main() {
  local revision
  repo=$(git rev-parse --show-toplevel)
  evidence="$repo/.tmp/hosted-perf-pr72-context"
  mkdir -p "$repo/.tmp"
  mkdir "$evidence"
  scratch=$(mktemp -d "$repo/.tmp/pr72-context.XXXXXX")
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
  sha256sum "$context_script_dir/diagnose-hosted-perf.sh" "$context_script_dir/diagnose-pr72-benchmark-context.sh" > "$evidence/runner-sha256.txt"
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
One Linux amd64 runner and two default-link test binaries. Prefix and isolated modes each run A/A and A/B.
Two groups of 10 alternating pairs per arm, benchmem, 200ms, count=1, CPU=2, benchstat alpha=0.01.
Prefix is the first 37 benchmark outputs in the original API process. The isolated process runs only its last leaf.
Original time and allocation thresholds and exact source-scoped approved costs apply to the target leaf.
An invalid process or A/A control fails the job. A/B threshold rejections remain in verdicts.tsv and the logs.
This context diagnostic does not determine the PR performance gate or reconstruct the whole shard's history.
PROTOCOL
  write_context_plan
  compile_revision base "$base" "$package"
  compile_revision head "$head" "$package"
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_contexts
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  analyze_contexts
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  context_main "$@"
fi
