#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
driver="$script_dir/diagnose-pr91-attribution.py"
assets="$script_dir/diagnose-pr91"
if [[ "${1:-}" == --plan-only ]]; then
  [[ $# -eq 3 ]]
  exec python3 "$driver" plan "$3" "$2"
fi
[[ $# -eq 1 && ( "$1" == codegen || "$1" == semantic ) ]]
diagnostic_suite="$1"
repo=$(git rev-parse --show-toplevel)
mkdir -p "$repo/.tmp"
evidence="$repo/.tmp/hosted-pr91-attribution-$diagnostic_suite"
mkdir "$evidence"
scratch=$(mktemp -d "$repo/.tmp/hosted-pr91-attribution.$diagnostic_suite.XXXXXXXX")

cleanup_attribution() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/hosted-pr91-attribution.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  printf 'exit=%s\nowned_scratch_removed=true\nbackground_processes_started=0\n' "$status" > "$evidence/cleanup.txt"
  exit "$status"
}
trap cleanup_attribution EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source" "$evidence/binaries"

sampler="$script_dir/diagnose-hosted-perf.sh"
printf '%s  %s\n' '9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072' "$sampler" | sha256sum -c
# Reuse the exact prior sampler, process launcher, normalization, and raw gate protocol.
source "$sampler"
base=01126509def82c0465e3e703f64268233fbec651
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
python3 "$driver" plan "$evidence" "$diagnostic_suite"
cp "$assets/pins.json" "$evidence/source-pins.json"
cp "$assets/tested-merge.patch.b64" "$evidence/tested-merge.patch.b64"
cp "$driver" "$evidence/driver.py"
cp "${BASH_SOURCE[0]}" "$evidence/entry.sh"
cp "$sampler" "$evidence/sampler.sh"

cat > "$evidence/protocol.txt" <<'PROTOCOL'
PR91 base 01126509def82c0465e3e703f64268233fbec651 versus tested merge 115b4ec23f3d438e5f22af48c4f3c9f6e37ca30a.
The candidate is reconstructed from the base archive plus the frozen patch; its complete Git source tree must equal 819c037453efcac48187981efb0a2ceb69393f55 before compilation.
Original benchmark bodies, helper sources, setup, inputs, assertions, and all package test source hashes are pinned. No benchmark substitute or padding is added.
The codegen product conditions symmetrically restore exactly four original test paths on both arms. Production source is unchanged by this operation.
Semantic has no product condition: these four codegen test paths are not dependencies of its executable. Semantic retains the changed production inferred dependency.
Same native Linux amd64v1 runner inside each matrix job; Go1.27.1 CGO1, original benchrun -trimpath, default linker, no custom PGO or ldflags.
Two groups of ten alternating process pairs for every A/A and A/B; 200ms, CPU2, benchmem, benchstat alpha .01.
Original significant time >5% and bytes/allocations >0% thresholds apply, with the same unit rejected in both groups and exact baseline-scoped approved costs for A/B only.
A/A rejection invalidates attribution and fails the job. A/B rejection is retained in verdicts and does not itself fail this diagnostic collection job.
Full seven original failures, nearby controls, and real Analyze/Generate change paths remain in the fixed inventory. Existing PrimaryKeyLoaderMetadata/128/loaders primary-only target is also retained.
The old pagination implementation can generate different incorrect outputs; Analyze/Generate controls report compilation cost and do not authorize correctness or performance exceptions.
The workflow stores actual executables, build IDs, source before/after manifests, complete nm sizes, selected objdump, raw process records, and gate verdicts.
No DB, service, browser, container, or additional benchmark fixture is required. Results do not waive the original release gate or bind a later source revision.
PROTOCOL

python3 "$driver" prepare "$repo" "$scratch/source" "$diagnostic_suite" "$evidence"
roles=(base head)
if [[ "$diagnostic_suite" == codegen ]]; then
  roles+=(base-product head-product)
fi
for role in "${roles[@]}"; do
  source_dir="$scratch/source/$role"
  sh "$scratch/source/base/scripts/setup-modules.sh" "$source_dir" > "$evidence/workspace-$role.log" 2>&1
  cp "$source_dir/go.work" "$evidence/go-$role.work.txt"
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
    pattern='codegen\..*(BenchmarkGlobalReceiverName|BenchmarkCompileDecimalIntegerLiterals|receiverName|compileLiteral|compileNumericLiteral|decimalIntegerLiteral|compileLocalAlias)|strings\.(TrimLeft|ToLower)|unicode/utf8\.DecodeRuneInString'
    grep -Eq 'codegen\.BenchmarkGlobalReceiverName' "$evidence/nm-$role.txt"
    grep -Eq 'codegen\.BenchmarkCompileDecimalIntegerLiterals' "$evidence/nm-$role.txt"
  else
    pattern='semantic\..*(BenchmarkScopeLookup|benchmarkScopeLookup|LookupReadOnly|\.Lookup|BenchmarkFindClosestString|findClosestString|consider)|runtime\.mapaccess1_faststr'
    grep -Eq 'semantic\.BenchmarkScopeLookup' "$evidence/nm-$role.txt"
    grep -Eq 'semantic\.BenchmarkFindClosestString' "$evidence/nm-$role.txt"
  fi
  go tool objdump -s "$pattern" "$binary" > "$evidence/objdump-$role.txt"
  grep -Eq '^TEXT ' "$evidence/objdump-$role.txt"
  grep -E 'internal/inferred|compileLocalAlias|compileDestructuredLocal|analyzeSelectedQueryProjections' \
    "$evidence/nm-$role.txt" > "$evidence/dependency-symbols-$role.txt"
}

for role in "${roles[@]}"; do
  if [[ "$role" == base-product ]]; then
    # The four symmetric restores leave the complete baseline tree identical.
    # Reuse its exact executable so only the candidate's test linkage can differ.
    mkdir "$scratch/bin/$role"
    cp "$scratch/bin/base/pkg-codegen.test" "$scratch/bin/$role/pkg-codegen.test"
    printf 'Reused exact base executable after verified complete source-tree identity.\n' > "$evidence/compile-$role.log"
  else
    "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" "pkg/$diagnostic_suite" \
      > "$evidence/compile-$role.log" 2>&1
  fi
  collect_binary "$role"
  python3 "$driver" source-check "$scratch/source/$role" "$role" "$evidence/source-roles.json" \
    "$evidence/source-$role.after-build.json"
done
if [[ "$diagnostic_suite" == codegen ]]; then
  cmp "$evidence/binaries/base.test" "$evidence/binaries/base-product.test"
  printf 'base and base-product complete executables are byte-identical\n' > "$evidence/baseline-product-identity.txt"
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
  python3 "$driver" source-check "$scratch/source/$role" "$role" "$evidence/source-roles.json" \
    "$evidence/source-$role.after-samples.json"
done

failed_control=0
printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
while IFS=$'\t' read -r comparison left right; do
  cp "$evidence/$comparison.expected.json" "$evidence/expected.json"
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  status=0
  gate_args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
  if [[ "$left" != "$right" ]]; then
    gate_args=(--approved-costs "$scratch/source/$right/scripts/benchgate/approved-costs.json" --base-sha "$base" "${gate_args[@]}")
  fi
  "$scratch/bin/benchgate" "${gate_args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
  printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
  if (( status != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log"
    if [[ "$left" == "$right" ]]; then
      failed_control=1
    fi
  fi
done < "$evidence/comparisons.tsv"
rm -- "$evidence/expected.json"
cat "$evidence/verdicts.tsv"
exit "$failed_control"
