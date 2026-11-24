#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
driver="$script_dir/diagnose-pr99-codegen.py"
assets="$script_dir/diagnose-pr99-codegen"
plan_only=false
if [[ $# == 1 && "$1" == --plan-only ]]; then
  plan_only=true
elif [[ $# != 0 ]]; then
  printf 'Usage: %s [--plan-only]\n' "$0" >&2
  exit 2
fi

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/hosted-pr99-codegen.XXXXXXXX")
if "$plan_only"; then
  evidence="$scratch/evidence"
else
  evidence="$repo/.tmp/hosted-pr99-codegen"
fi
evidence_created=false

cleanup_pr99() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/hosted-pr99-codegen.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if ! "$plan_only" && "$evidence_created"; then
    printf 'exit=%s\nowned_scratch_removed=true\nbackground_processes_started=0\n' "$status" > "$evidence/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_pr99 EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
evidence_created=true

(
  cd "$script_dir"
  sha256sum -c - <<'HASHES'
9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072  diagnose-hosted-perf.sh
f3256f03528dd99edfbece1634999b198cda4eebc0da08eb99cf790b06d643c7  diagnose-pr93-p1.py
0d7d19a81588ebdc2e4e36a8e567c011a9327e8c94efdec1f9069a4388658df3  diagnose-pr99-codegen/benchmark-batch.tsv
1137b9fd8d7c47cc842f3776bfba171e1c57b495dc9f27dfcb27edd7382aad65  diagnose-pr99-codegen/benchmark-confirmation-batch.tsv
d6978a626553f6a59251cb23df4cd4701baff053ac55618e074624f55d9e353e  diagnose-pr99-codegen/benchmark-confirmation-expected.json
b834ea613579bdc2e3824d8166e632ed55e1ed66adf26326fda993fa7f994278  diagnose-pr99-codegen/benchmark-confirmation-targets.tsv
234cd09c1cdffbc7834f0c08bcb88ba198a0664413b4258a9f5c6293f3ba7e3f  diagnose-pr99-codegen/benchmark-expected.json
b5c4cddc7890572960d7447b6e46ee829e586f75121a559d66850c3d45d4246c  diagnose-pr99-codegen/official.patch.b64
HASHES
) | tee "$evidence/input-checks.txt"
mkdir "$scratch/source"
python3 "$driver" plan "$evidence"
python3 "$driver" prepare "$repo" "$scratch/source" "$evidence"
if "$plan_only"; then
  cat "$evidence/source.json"
  exit 0
fi

source "$script_dir/diagnose-hosted-perf.sh"
base=578f507518b3fc9fede3c17930fa4b6cf9af7ea4
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir "$TMPDIR" "$scratch/bin"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
cp "$assets"/* "$evidence/"
cp "$driver" "${BASH_SOURCE[0]}" "$evidence/"
cat > "$evidence/protocol.txt" <<'PROTOCOL'
Replays PR 99 Tests 45298161121 attempt 1 shard 05. Baseline is 578f507; the candidate is reconstructed from the original diff and matches synthetic merge tree 13b45287.
Primary uses the original one-process 52-leaf filter. Confirmation uses the historical one-leaf filter. A/A uses the same baseline executable twice. A/B uses the original two full test binaries, default linker, trimpath and no test overlay.
Both comparisons use two groups of ten alternating 200ms pairs, CPU2, benchmem, Go 1.27.1, Linux amd64 v1 and CGO1. Record the runner CPU; the original was Intel Xeon 6975P-C.
The original >5% significant time and >0% significant memory/allocation thresholds and benchstat alpha .01 apply. Original baseline-bound costs only apply to A/B.
Fixed-leaf verdicts use the full primary processes projected to the selected leaf, against the separately executed targeted confirmation. The complete primary CSV and any other provisional leaves are retained.
A full scope verdict is computed only when the new primary is clean or its confirmation targets exactly match the historical single leaf. If the new primary also selects other leaves, full scope requires new confirmation.
The result is diagnostic evidence. It does not certify a release or prove linkage layout caused the original difference.
PROTOCOL

for role in base head; do
  sh "$scratch/source/$role/scripts/setup-modules.sh" "$scratch/source/$role" > "$evidence/workspace-$role.log" 2>&1
  cp "$scratch/source/$role/go.work" "$evidence/go-$role.work.txt"
done
(
  cd "$scratch/source/base"
  for tool in benchrun benchplan benchgate; do
    go build -o "$scratch/bin/$tool" "./scripts/$tool"
  done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/tools.log" 2>&1

symbol_pattern='codegen\.(BenchmarkMigrationStateValidation|validateSchemaState|SaveState|LoadState|MarshalState)|runtime\.(mapIterStart|mapIterNext)|internal/runtime/maps\..*Iter\)\.(Init|Next)'
for role in base head; do
  "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" pkg/codegen > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  binary="$scratch/bin/$role/pkg-codegen.test"
  go tool nm -size "$binary" | awk -v pattern="$symbol_pattern" '$4 ~ pattern' > "$evidence/nm-$role.txt"
  grep -Fq 'codegen.validateSchemaState' "$evidence/nm-$role.txt"
  grep -Fq 'codegen.BenchmarkMigrationStateValidation.func' "$evidence/nm-$role.txt"
  grep -Fq 'runtime.mapIterStart' "$evidence/nm-$role.txt"
  python3 "$driver" layout "$evidence/nm-$role.txt" "$evidence/layout-$role.tsv"
  go tool objdump -s "$symbol_pattern" "$binary" > "$evidence/objdump-$role.txt"
  grep -Eq '^TEXT ' "$evidence/objdump-$role.txt"

  for group in primary confirmation; do
    batch="$assets/benchmark-batch.tsv"
    if [[ "$group" == confirmation ]]; then
      batch="$assets/benchmark-confirmation-batch.tsv"
    fi
    IFS=$'\t' read -r package filter < "$batch"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s \
      > "$evidence/inventory-$role-$group.txt" 2>> "$evidence/benchmark-stderr.txt"
  done
done
python3 "$driver" inventory "$evidence" > "$evidence/inventory-cpu.txt"
python3 "$driver" check-sources "$scratch/source"

mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
while IFS=$'\t' read -r group sample comparison case_id side role variant raw package filter; do
  run_leg "$group" "$sample" "$comparison" "$side" "$role" "$case_id" "$package" "$variant" "$filter"
done < "$evidence/schedule.tsv"
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
python3 "$driver" samples "$evidence" > "$evidence/sample-cpu.txt"
python3 "$driver" check-sources "$scratch/source"

analyze_fixed_leaf() {
  local comparison="$1" side target stem="$evidence/$1"
  IFS=$'\t' read -r _ target < "$assets/benchmark-confirmation-targets.tsv"
  for side in left right; do
    awk -v target="$target" '$1 !~ /^Benchmark/ || $1 == target' "$stem.primary.$side.txt" \
      > "$stem.fixed-primary.$side.txt"
  done
  "$scratch/bin/benchplan" --verify "$assets/benchmark-confirmation-expected.json" "$stem.fixed-primary.left.txt" "$stem.fixed-primary.right.txt"
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.fixed-primary.left.txt" head="$stem.fixed-primary.right.txt" \
    > "$stem.fixed-primary.csv" 2> "$stem.fixed-primary.warnings.txt"
}

record_gate() {
  local log="$1" status=0
  shift
  "$scratch/bin/benchgate" "$@" > "$log" 2>&1 || status=$?
  if (( status != 0 )); then
    if ! grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$log"; then
      cat "$log" >&2
      return 2
    fi
  fi
  printf '%s\n' "$status"
}

printf 'comparison\tfixed_leaf_exit\tfull_scope_verdict\n' > "$evidence/verdicts.tsv"
for comparison in aa ab; do
  stem="$evidence/$comparison"
  cp "$assets/benchmark-expected.json" "$evidence/expected.json"
  normalize_group "$comparison" primary
  cp "$assets/benchmark-confirmation-expected.json" "$evidence/expected.json"
  normalize_group "$comparison" confirmation
  analyze_fixed_leaf "$comparison"
  cost_args=()
  if [[ "$comparison" == ab ]]; then
    cost_args=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base")
  fi
  fixed=$(record_gate "$stem.fixed.benchgate.log" "${cost_args[@]}" "$stem.fixed-primary.csv" "$stem.confirmation.csv")
  "$scratch/bin/benchgate" --confirmation-targets "$stem.primary.csv" > "$stem.primary-targets.tsv"
  if [[ ! -s "$stem.primary-targets.tsv" ]]; then
    full=$(record_gate "$stem.full.benchgate.log" "$stem.primary.csv")
  elif cmp -s "$stem.primary-targets.tsv" "$assets/benchmark-confirmation-targets.tsv"; then
    full=$(record_gate "$stem.full.benchgate.log" "${cost_args[@]}" --targeted-confirmation "$stem.primary.csv" "$stem.confirmation.csv")
  else
    full=needs-other-confirmation
  fi
  printf '%s\t%s\t%s\n' "$comparison" "$fixed" "$full" >> "$evidence/verdicts.tsv"
done
rm -- "$evidence/expected.json"
cat "$evidence/verdicts.tsv"
