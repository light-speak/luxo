#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
driver="$script_dir/diagnose-pr72-header-baseline.py"
mode="${1:-run}"
[[ $# -le 1 && ( "$mode" == run || "$mode" == --plan-only ) ]]
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr72-header-baseline.XXXXXXXX")
evidence="$repo/.tmp/hosted-pr72-header-baseline"
if [[ "$mode" == --plan-only ]]; then
  evidence="$scratch/evidence"
fi
evidence_created=false

cleanup_header_baseline() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr72-header-baseline.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$evidence_created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$evidence/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_header_baseline EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
evidence_created=true

record_header_symbols() {
  local role="$1" package="$2" name pattern binary
  name="${package##*/}"
  binary="$scratch/bin/$role/pkg-lux-$name.test"
  pattern='[.](validateArrayCount|validArrayCount|AppendArrayHeader|.*WriteArrayHeader|ReadBytes|ReadVarint|ReadNullable|SplitSelectionMask|selectionMaskParts|validSelectionChildren|legacySplitSelectionMask|payloadReaderCases(Before)?|benchmarkPayloadRead|Plan|Benchmark(ArrayHeaderWrite|ElementPayload|WireReadOffsetBounds|SelectionMaskLimits|PlanSelection).*)$'
  go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$role-$name.nm"
  go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$role-$name.objdump"
  test -s "$evidence/symbols-$role-$name.nm"
  grep -q '^TEXT ' "$evidence/symbols-$role-$name.objdump"
}

compile_header_baseline() {
  local role source name package case_id variant shard filter
  for role in base test head; do
    source="$scratch/source/$role"
    sh "$source/scripts/setup-modules.sh" "$source" > "$evidence/workspace-$role.log" 2>&1
    cp "$source/go.work" "$evidence/go-$role.work.txt"
  done
  (
    cd "$scratch/source/base"
    for name in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$name" "./scripts/$name"
    done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  ) > "$evidence/tools.log" 2>&1
  for role in base test head; do
    source="$scratch/source/$role"
    for package in pkg/lux/codec pkg/lux/luvia; do
      "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$role" "$package"
      record_header_symbols "$role" "$package"
    done > "$evidence/compile-$role.log" 2>&1
    record_binaries "$role"
    while IFS=$'\t' read -r case_id package variant shard filter; do
      "$scratch/bin/benchrun" run "$source" "$scratch/bin/$role" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        -test.timeout=90s > "$evidence/inventory-$role-$case_id.txt" 2>> "$evidence/benchmark-stderr.txt"
    done < "$evidence/cases.tsv"
  done
}

header_boundary_tests() {
  local role name pattern='ReadBytesBoundaries|ReadBytesInvalidOffsets|ReadBytesLongPrefix|ArrayHeaderWriteBounds|SelectionMaskEmptyMeansAll'
  local -a common=(TestReadBytesBoundaries TestReadBytesInvalidOffsets TestReadBytesLongPrefix TestArrayHeaderWriteBounds TestSelectionMaskEmptyMeansAll)
  local -a candidate=(TestArrayHeaderWriteCapacity TestSelectionMaskRejectsUnselectedChildren)
  for role in base test head; do
    local role_pattern="$pattern"
    if [[ "$role" == head ]]; then
      role_pattern+='|ArrayHeaderWriteCapacity|SelectionMaskRejectsUnselectedChildren'
    fi
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" pkg/lux/codec \
      -test.run="^Test($role_pattern)$" -test.v -test.timeout=90s > "$evidence/codec-test-$role.log" 2>&1
    for name in "${common[@]}"; do
      grep -q "^--- PASS: $name " "$evidence/codec-test-$role.log"
    done
    if [[ "$role" == head ]]; then
      for name in "${candidate[@]}"; do
        grep -q "^--- PASS: $name " "$evidence/codec-test-$role.log"
      done
    fi
  done
  "$scratch/bin/benchrun" run "$scratch/source/head" "$scratch/bin/head" pkg/lux/luvia \
    -test.run='^TestPlan(RejectsMalformedSelection|PropagatesDuplicateChildError)$' \
    -test.v -test.timeout=90s > "$evidence/planner-test-head.log" 2>&1
  for name in TestPlanRejectsMalformedSelection TestPlanPropagatesDuplicateChildError; do
    grep -q "^--- PASS: $name " "$evidence/planner-test-head.log"
  done
}

sample_header_baseline() {
  local group offset pair case_id package variant shard filter index side
  local comparison left right role
  local -a comparisons=(aa ab at tb) sides
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    offset=0
    if [[ "$group" == confirmation ]]; then offset=1; fi
    for pair in {1..10}; do
      printf -v pair '%02d' "$pair"
      sides=(right left)
      if (( (10#$pair + offset) % 2 == 1 )); then sides=(left right); fi
      while IFS=$'\t' read -r case_id package variant shard filter; do
        for index in {0..3}; do
          comparison="${comparisons[(10#$pair + offset + 10#$case_id + index) % 4]}"
          case "$comparison" in
            aa) left=base; right=base;;
            ab) left=base; right=head;;
            at) left=base; right=test;;
            tb) left=test; right=head;;
          esac
          for side in "${sides[@]}"; do
            role="$left"
            if [[ "$side" == right ]]; then role="$right"; fi
            run_leg "$group" "$pair" "$comparison" "$side" "$role" "$case_id" "$package" "$variant" "$filter"
          done
        done
      done < "$evidence/cases.tsv"
    done
  done
}

analyze_header_baseline() {
  local comparison status exit_status=0
  printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for comparison in aa ab at tb; do
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    status=0
    "$scratch/bin/benchgate" "$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv" \
      > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
    printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
    if (( status != 0 )); then
      grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log" || return "$status"
      exit_status=1
    fi
  done
  cat "$evidence/verdicts.tsv"
  if ! grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"; then
    printf '%s\n' 'Same-binary control crossed the original threshold. The attribution is inconclusive.'
  fi
  return "$exit_status"
}

python3 "$driver" plan "$evidence"
python3 "$driver" prepare "$repo" "$scratch" "$evidence"
source "$script_dir/diagnose-hosted-perf.sh"
source "$script_dir/diagnose-pr72-rpc-context-inline.sh"
head=5e4c9d1b3813cc95f98d4088a10c242dca47b8de
for revision in base test; do
  pr72_rpc_context_add_base_planner_fixture "$scratch/source/$revision"
done
python3 "$driver" sources "$scratch" "$evidence"

if [[ "$mode" == --plan-only ]]; then
  run_leg() {
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\traw/%s.%s.%s.%s.%s.txt\n' \
      "$1" "$2" "$3" "$6" "$4" "$5" "$8" "$1" "$2" "$3" "$6" "$4" >> "$evidence/order.tsv"
  }
  sample_header_baseline
  python3 "$driver" order "$evidence"
  cat "$evidence/revisions.json" "$evidence/cases.tsv"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir "$TMPDIR" "$scratch/bin"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
cp "$driver" "${BASH_SOURCE[0]}" "$evidence/"
cat > "$evidence/protocol.txt" <<'PROTOCOL'
Same native runner, default Go 1.27.1 linux/amd64 v1 link with the normal workflow module setup, trimpath, and CGO.
A uses official base source. B is the official synthetic tree, reproduced locally from the original pinned parents.
Atest replaces precisely the baseline codec bytes_test.go with the pinned head test file and leaves production byte-identical.
A and Atest include the identical B Planner benchmark function in a separate same-package fixture. It exists in B's normal test file. Planner helpers are byte-identical.
Four paired comparisons: A/A uses one binary, A/B replays the exact codec trees, A/Atest isolates the test-only linkage change, Atest/B uses the alternate test baseline.
Both groups retain all 19 leaves and use 200ms, 10 alternating pairs, CPU2, benchmem, benchstat alpha .01 and the original >5% time or >0% allocation gate.
The historical shard-04 codec primary ran 81 leaves and its confirmation ran only the two rejected headers. These groups use the same five header siblings each time.
Shard-01 codec primary ran 128 leaves and supplied 21 codec confirmation targets. These groups retain six reader controls (old/current) from that source.
Shard-09 codec primary ran 53 leaves and supplied three codec confirmation targets. These groups measure empty selection old/current and all four wire guards.
The official shard-11 Planner leaves exist only on B. This probe captures both baseline roles using the same legitimate inputs.
For a longer context replay, use the codec filter from the original shard-04 benchmark-batch.tsv and the separate benchmark-confirmation-batch.tsv. The official artifact IDs and digests are recorded. A red same-binary control prevents attribution. The other comparisons retain their own verdicts and raw results.
PROTOCOL

compile_header_baseline
header_boundary_tests
python3 "$driver" inventory "$evidence" > "$evidence/inventory-cpu.txt"
date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
sample_header_baseline
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
python3 "$driver" samples "$evidence"
python3 "$driver" sources "$scratch" "$evidence"
analyze_header_baseline
