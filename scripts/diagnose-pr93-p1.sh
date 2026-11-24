#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
driver="$script_dir/diagnose-pr93-p1.py"
assets="$script_dir/diagnose-pr93-p1"
if [[ "${1:-}" == --plan-only ]]; then
  [[ $# -eq 3 ]]
  exec python3 "$driver" plan "$3" "$2"
fi
[[ $# -eq 1 && ( "$1" == codegen || "$1" == semantic ) ]]
diagnostic_suite="$1"
repo=$(git rev-parse --show-toplevel)
mkdir -p "$repo/.tmp"
evidence="$repo/.tmp/hosted-pr93-p1-$diagnostic_suite"
mkdir "$evidence"
scratch=$(mktemp -d "$repo/.tmp/hosted-pr93-p1.$diagnostic_suite.XXXXXXXX")

cleanup_diagnostic() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/hosted-pr93-p1.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  printf 'exit=%s\nowned_scratch_removed=true\nbackground_processes_started=0\n' "$status" > "$evidence/cleanup.txt"
  exit "$status"
}
trap cleanup_diagnostic EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source" "$evidence/binaries"
sampler="$script_dir/diagnose-hosted-perf.sh"
printf '%s  %s\n' '9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072' "$sampler" | sha256sum -c
source "$sampler"
base=f853ea49f14a2eb3126f54a831a39d7ea05585db
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
python3 "$driver" plan "$evidence" "$diagnostic_suite"
cp "$assets/pins.json" "$evidence/source-pins.json"
cp "$assets/official.patch.b64" "$assets/p1.patch.b64" "$evidence/"
cp "$driver" "$evidence/driver.py"
cp "${BASH_SOURCE[0]}" "$evidence/entry.sh"
cp "$sampler" "$evidence/sampler.sh"
cat > "$evidence/protocol.txt" <<'PROTOCOL'
Exact original official PR93 A=f853ea49f14a2eb3126f54a831a39d7ea05585db; H=79a068f59aec77b4fea6be7eb05aa18f2ce37c45, tree 6b589706043ba3680eead06bf2b3fdb2879178fc.
Original A/A and A/H retain the unmodified actual official test sets and all nine prior confirmed timing targets. Source trees are reconstructed and verified before compilation.
P1-control is production H plus only the new regression tests. P1-head has the exact same complete test inputs plus the one production dedup-map guard.
This symmetric-test comparison measures production H versus H+P1 without attributing new test linkage to the optimization. Original H remains separate for the exact original A/H comparison.
The only P1 direct paths are ErrorDeclarationValidation/fields_1 and AnalyzeSealedPayloads/many_variants. All other seven original targets remain measured controls, not blockers for independent P1 evidence.
The same runner inside each matrix job; Go1.27.1, Linux amd64v1, CGO1, original benchrun -trimpath and default linker; no padding, no custom PGO or ldflags.
Every comparison has two groups of ten alternating pairs at 200ms, CPU2, benchmem; benchstat alpha .01; original >5% significant time and >0% significant B/alloc rejection rules in both groups.
Original A/H retains the exact original baseline-scoped cost policy. No cost exemptions apply to P1 A/A or P1 A/B.
All full-comparison rejections are preserved. Only rejection in the two direct P1 A/A or direct P1 A/B gates fails independent P1 collection; unrelated regressions do not decide that scope.
Independent improvement is established only if both direct time leaves improve significantly in both groups, direct A/A is clean, and direct A/B has no confirmed time/memory/allocation regression. Otherwise its evidence JSON says not-established.
Workflow success indicates valid collection, not release acceptance. An absent measured gain is not turned into a gain; original PR93 failures remain open regardless of independent P1 outcome.
Direct views are derived from the same raw measured processes, without resampling, fake fixtures, changed loops, changed thresholds or mixed baselines.
The complete actual executables, build IDs, nm-size, key objdump, source/test fingerprints, process records, CSV and per-comparison verdicts are retained. No DB/container/service is needed.
This nine-leaf scope does not replace the eventual full affected-arity and release performance gates for any final source revision.
PROTOCOL

python3 "$driver" prepare "$repo" "$scratch/source" "$evidence"
roles=(base official-head p1-control p1-head)
for role in "${roles[@]}"; do
  sh "$scratch/source/base/scripts/setup-modules.sh" "$scratch/source/$role" > "$evidence/workspace-$role.log" 2>&1
  cp "$scratch/source/$role/go.work" "$evidence/go-$role.work.txt"
done
(
  cd "$scratch/source/base"
  export GOWORK="$PWD/go.work"
  for tool in benchrun benchplan benchgate; do
    go build -o "$scratch/bin/$tool" "./scripts/$tool"
  done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/compile-tools.log" 2>&1

collect_binary() {
  local role="$1" binary="$scratch/bin/$1/pkg-$diagnostic_suite.test" pattern
  record_binaries "$role"
  cp "$binary" "$evidence/binaries/$role.test"
  go tool nm -size "$binary" > "$evidence/nm-$role.txt"
  if [[ "$diagnostic_suite" == codegen ]]; then
    pattern='codegen\..*(BenchmarkGlobalReceiverName|BenchmarkWhereCondition|receiverName|compileWhere|compileLiteral)|semantic\..*resolveParameterTypes'
    grep -Fq 'codegen.BenchmarkGlobalReceiverName' "$evidence/nm-$role.txt"
    grep -Fq 'codegen.BenchmarkWhereCondition' "$evidence/nm-$role.txt"
  else
    pattern='semantic\..*(Benchmark(AnalyzeSealedPayloads|CollectEventEmits|ErrorDeclarationValidation|FindClosestString|ReachableEventEmits|ScopeLookup)|resolveParameterTypes|resolveTypeRefInContext|collectReachableEmits|LookupReadOnly|findClosestString|consider)|ast\..*(Walk|walkStmt|walkExpr)|runtime\.(makemap|mapaccess|mapassign)'
    grep -Fq 'semantic.(*Analyzer).resolveParameterTypes' "$evidence/nm-$role.txt"
  fi
  go tool objdump -s "$pattern" "$binary" > "$evidence/objdump-$role.txt"
  grep -Eq '^TEXT ' "$evidence/objdump-$role.txt"
}

for role in "${roles[@]}"; do
  "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" "pkg/$diagnostic_suite" \
    > "$evidence/compile-$role.log" 2>&1
  collect_binary "$role"
  python3 "$driver" source-check "$scratch/source/$role" "$role" "$evidence/source-$role.after-build.json"
done
if [[ "$diagnostic_suite" == codegen ]]; then
  if cmp -s "$evidence/binaries/official-head.test" "$evidence/binaries/p1-control.test"; then
    printf 'complete H and symmetric-test control executables are byte-identical\n' > "$evidence/dependency-test-identity.txt"
  else
    printf 'executables differ; retain both and inspect; no dependency-test layout claim is made\n' > "$evidence/dependency-test-identity.txt"
  fi
fi

while IFS=$'\t' read -r role case_id package variant filter; do
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s \
    > "$evidence/inventory-$role-$case_id.txt" 2>> "$evidence/benchmark-stderr.txt"
done < "$evidence/inventory.tsv"
python3 "$driver" inventory "$evidence" "$diagnostic_suite"
mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
while IFS=$'\t' read -r group sample comparison case_id side role variant raw package filter; do
  run_leg "$group" "$sample" "$comparison" "$side" "$role" "$case_id" "$package" "$variant" "$filter"
done < "$evidence/schedule.tsv"
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
python3 "$driver" samples "$evidence" "$diagnostic_suite"
for role in "${roles[@]}"; do
  python3 "$driver" source-check "$scratch/source/$role" "$role" "$evidence/source-$role.after-samples.json"
done

printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
while IFS=$'\t' read -r comparison left right; do
  cp "$evidence/$comparison.expected.json" "$evidence/expected.json"
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  status=0
  gate_args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
  if [[ "$comparison" == official-ah ]]; then
    gate_args=(--approved-costs "$scratch/source/official-head/scripts/benchgate/approved-costs.json" --base-sha "$base" "${gate_args[@]}")
  fi
  "$scratch/bin/benchgate" "${gate_args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
  printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
  if (( status != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log"
  fi
done < "$evidence/comparisons.tsv"
rm -- "$evidence/expected.json"
cat "$evidence/verdicts.tsv"

normalize_direct() {
  local comparison="$1" label="$2" group side stem
  for group in primary confirmation; do
    stem="$evidence/p1-direct-$label.$group"
    for side in left right; do
      "$scratch/bin/benchplan" --normalize "$evidence/derived/$comparison.$group.$side.raw.txt" "$stem.$side.txt"
    done
    "$scratch/bin/benchplan" --verify "$evidence/p1-direct.expected.json" "$stem.left.txt" "$stem.right.txt"
    "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
      base="$stem.left.txt" head="$stem.right.txt" > "$stem.csv" 2> "$stem.warnings.txt"
  done
}

independent_failure=0
if [[ "$diagnostic_suite" == semantic ]]; then
  python3 "$driver" derive-direct "$evidence"
  normalize_direct p1-aa aa
  normalize_direct p1-ab ab
  direct_aa=0
  direct_ab=0
  "$scratch/bin/benchgate" "$evidence/p1-direct-aa.primary.csv" "$evidence/p1-direct-aa.confirmation.csv" \
    > "$evidence/p1-direct-aa.benchgate.log" 2>&1 || direct_aa=$?
  "$scratch/bin/benchgate" "$evidence/p1-direct-ab.primary.csv" "$evidence/p1-direct-ab.confirmation.csv" \
    > "$evidence/p1-direct-ab.benchgate.log" 2>&1 || direct_ab=$?
  for label in aa ab; do
    name="direct_$label"
    if (( ${!name} != 0 )); then
      grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/p1-direct-$label.benchgate.log"
      independent_failure=1
    fi
  done
  printf '{"aa":%s,"ab":%s}\n' "$direct_aa" "$direct_ab" > "$evidence/p1-direct-status.json"
  python3 "$driver" report-direct "$evidence"
else
  printf 'P1 direct-path outcome is evaluated only by the semantic job; these two original codegen leaves remain controls.\n' \
    > "$evidence/p1-independent-not-applicable.txt"
fi
exit "$independent_failure"
