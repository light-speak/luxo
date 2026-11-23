#!/usr/bin/env bash
set -euo pipefail

base=65f1892e175487503aa36caa86d1eb8a0805f073
pr71=e79e2a2930213e317eb6e0f1a77e27f22c4502e7
pr72=f86453bc03d6ef7d0210aeb414c5e9a7dd540ed0
benchstat_version=v0.0.0-20260825160852-19be9d8e6c70
benchtime=200ms

write_cases() {
  cat > "$evidence/cases.tsv" <<'CASES'
01	pr71	pkg/lux/api	BenchmarkEventSnapshotDispatch/subscribers=128/typed-2	^BenchmarkEventSnapshotDispatch$/^subscribers=128$/^typed$
02	pr71	pkg/lux/api	BenchmarkModuleColorWarm-2	^BenchmarkModuleColorWarm$
03	pr71	pkg/lux/luvia	BenchmarkMergeDestination/columnar=true/rows=1/direct=true-2	^BenchmarkMergeDestination$/^columnar=true$/^rows=1$/^direct=true$
04	pr72	pkg/lux/api	BenchmarkBinaryListPayload/String/count=0/version=0-2	^BenchmarkBinaryListPayload$/^String$/^count=0$/^version=0$
05	pr72	pkg/lux/api	BenchmarkBinaryListPayload/Model/count=0/version=0-2	^BenchmarkBinaryListPayload$/^Model$/^count=0$/^version=0$
06	pr72	pkg/lux/codec	BenchmarkStringWriteUTF8/nullable/ascii16/after-2	^BenchmarkStringWriteUTF8$/^nullable$/^ascii16$/^after$
07	pr72	pkg/lux/codec	BenchmarkSelectionMaskLimits/empty/after-2	^BenchmarkSelectionMaskLimits$/^empty$/^after$
CASES
  python3 - "$evidence" <<'PY'
import json
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
expected = {"aa": [], "pr71": [], "pr72": []}
for line in (evidence / "cases.tsv").read_text().splitlines():
    _, head, package, name, _ = line.split("\t")
    row = {"Package": package, "Name": name}
    expected["aa"].append(row)
    expected[head].append(row)
for comparison, rows in expected.items():
    (evidence / f"{comparison}.expected.json").write_text(
        json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n"
    )
PY
}

compile_revision() {
  local revision="$1" commit="$2" package binary
  shift 2
  mkdir -p "$scratch/source/$revision" "$scratch/bin/$revision"
  git -C "$repo" archive "$commit" | tar -xf - -C "$scratch/source/$revision"
  git -C "$repo" show -s --format="$revision %H %T" "$commit" >> "$evidence/revisions.txt"
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
  local case_id="$6" package="$7" filter="$8" filename
  filename="raw/$group.$sample.$comparison.$case_id.$side.txt"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$group" "$sample" "$comparison" "$case_id" "$side" "$revision" "$filename" >> "$evidence/order.tsv"
  "$scratch/bin/benchrun" run "$scratch/source/$revision" "$scratch/bin/$revision" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem \
    -test.benchtime="$benchtime" -test.count=1 -test.cpu=2 -test.timeout=90s \
    > "$evidence/$filename" 2>> "$evidence/benchmark-stderr.txt"
}

run_pair() {
  local group="$1" sample="$2" comparison="$3" head="$4" first="$5"
  local case_id="$6" package="$7" filter="$8"
  if [[ "$first" == left ]]; then
    run_leg "$group" "$sample" "$comparison" left base "$case_id" "$package" "$filter"
    run_leg "$group" "$sample" "$comparison" right "$head" "$case_id" "$package" "$filter"
  else
    run_leg "$group" "$sample" "$comparison" right "$head" "$case_id" "$package" "$filter"
    run_leg "$group" "$sample" "$comparison" left base "$case_id" "$package" "$filter"
  fi
}

sample_groups() {
  local group group_offset sample case_id head package name filter first comparison revision
  local -a kind
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    group_offset=0
    if [[ "$group" == confirmation ]]; then
      group_offset=1
    fi
    for sample in {01..10}; do
      while IFS=$'\t' read -r case_id head package name filter; do
        first=right
        if (( (10#$sample + group_offset) % 2 == 1 )); then
          first=left
        fi
        if (( (10#$sample + 10#$case_id + group_offset) % 2 == 1 )); then
          kind=(aa "$head")
        else
          kind=("$head" aa)
        fi
        for comparison in "${kind[@]}"; do
          revision="$comparison"
          if [[ "$comparison" == aa ]]; then
            revision=base
          fi
          run_pair "$group" "$sample" "$comparison" "$revision" "$first" "$case_id" "$package" "$filter"
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
  "$scratch/bin/benchplan" --verify "$evidence/$comparison.expected.json" "$stem.left.txt" "$stem.right.txt"
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.left.txt" head="$stem.right.txt" > "$stem.csv" 2> "$stem.warnings.txt"
  "$scratch/bin/benchgate" --requires-confirmation "$stem.csv" > "$stem.requires-confirmation.txt"
}

analyze_groups() {
  local comparison status
  local -a gate_args
  printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for comparison in aa pr71 pr72; do
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    gate_args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
    if [[ "$comparison" != aa ]]; then
      gate_args=(--approved-costs "$scratch/source/$comparison/scripts/benchgate/approved-costs.json" --base-sha "$base" "${gate_args[@]}")
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
  local revision
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
  for revision in "$base" "$pr71" "$pr72"; do
    [[ "$(git -C "$repo" cat-file -t "$revision")" == commit ]]
  done
  git -C "$repo" merge-base --is-ancestor "$base" "$pr71"
  git -C "$repo" merge-base --is-ancestor "$base" "$pr72"
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
Same runner, two groups (primary, confirmation), 10 alternating pairs per group for each case in A/A and A/B.
A/A uses the same baseline binary for both legs. A/B compares baseline with the case's pinned PR.
benchmem, 200ms, count=1, cpu=2, test timeout=90s, benchstat alpha=0.01.
benchgate: significant time increase >5%, significant bytes or allocations increase >0%, same unit flagged in both groups.
A/B uses that revision's scoped approved-cost policy. A/A has no approved costs.
The before/after benchmark path component belongs to the fixture. Left/right identifies the binary comparison.
A threshold failure is recorded for diagnosis. A missing case or invalid sample fails this workflow.
PROTOCOL
  write_cases
  compile_revision base "$base" pkg/lux/api pkg/lux/codec pkg/lux/luvia
  compile_revision pr71 "$pr71" pkg/lux/api pkg/lux/luvia
  compile_revision pr72 "$pr72" pkg/lux/api pkg/lux/codec
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  analyze_groups
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
