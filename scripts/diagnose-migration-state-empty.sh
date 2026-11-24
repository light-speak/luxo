#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
driver="$script_dir/diagnose-migration-state-empty.py"
mode="${1:-run}"
[[ $# -le 1 && ( "$mode" == run || "$mode" == --plan-only ) ]]
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/migration-state-empty.XXXXXXXX")
evidence="$repo/.tmp/hosted-migration-state-empty"
if [[ "$mode" == --plan-only ]]; then
  evidence="$scratch/evidence"
fi
evidence_created=false

cleanup_empty_state() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/migration-state-empty.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$evidence_created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$evidence/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_empty_state EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence" "$scratch/source"
evidence_created=true
(
  cd "$script_dir"
  sha256sum -c - <<'HASHES'
9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072  diagnose-hosted-perf.sh
f3256f03528dd99edfbece1634999b198cda4eebc0da08eb99cf790b06d643c7  diagnose-pr93-p1.py
1ce905093a9dfb152fceb0e4e2f1497c2a421f810d360865d0f9d2d1a541a5af  diagnose-pr99-codegen.py
234cd09c1cdffbc7834f0c08bcb88ba198a0664413b4258a9f5c6293f3ba7e3f  diagnose-pr99-codegen/benchmark-expected.json
e07cbf06e123c14d9d041a879226c7f2958139deb4129f91dee5207ae9bfb432  diagnose-migration-state-empty.patch.b64
HASHES
) | tee "$evidence/inputs.txt"
source "$script_dir/diagnose-hosted-perf.sh"
python3 "$driver" plan "$evidence"
python3 "$driver" prepare "$repo" "$scratch/source" "$evidence"

if [[ "$mode" == --plan-only ]]; then
  run_leg() {
    local group="$1" round="$2" comparison="$3" side="$4" role="$5" case_id="$6" variant="$8"
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\traw/%s.%s.%s.%s.%s.txt\n' \
      "$group" "$round" "$comparison" "$case_id" "$side" "$role" "$variant" \
      "$group" "$round" "$comparison" "$case_id" "$side" >> "$evidence/order.tsv"
  }
  sample_groups
  python3 "$driver" order "$evidence"
  cat "$evidence/sources.json"
  printf 'Verified 15 historical leaves and 80 planned process arms.\n'
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir "$TMPDIR" "$scratch/bin"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
cp "$scratch/source/candidate.patch" "$driver" "${BASH_SOURCE[0]}" "$evidence/"
cat > "$evidence/protocol.txt" <<'PROTOCOL'
Exact main 133f54d plus symmetric regression test on both sides; head also applies the source change. The benchmark fixture and complete test linkage inputs are identical.
A/A uses the same executable. A/B uses the two unmodified default test links on the same native runner. Two groups of ten alternating 200ms pairs per comparison, CPU2 and benchmem.
Both groups retain all 15 migration-state leaves. Benchstat alpha .01, significant time regression threshold >5%, significant memory or allocations >0%, no approved-cost expansion.
Qualification requires clean A/A, clean A/B, and a statistically significant empty validation improvement in both groups. This evidence covers the independent optimization and does not supersede the full PR gate.
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

symbols='codegen\.(BenchmarkMigrationStateValidation|validateSchemaState)|runtime\.(mapIterStart|mapIterNext)|internal/runtime/maps\..*Iter\)\.(Init|Next)'
for role in base head; do
  "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" pkg/codegen > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  binary="$scratch/bin/$role/pkg-codegen.test"
  go tool nm -size "$binary" | awk -v pattern="$symbols" '$4 ~ pattern' > "$evidence/nm-$role.txt"
  grep -Fq 'codegen.validateSchemaState' "$evidence/nm-$role.txt"
  go tool objdump -s "$symbols" "$binary" > "$evidence/objdump-$role.txt"
  grep -q '^TEXT ' "$evidence/objdump-$role.txt"
  python3 "$script_dir/diagnose-pr99-codegen.py" layout "$evidence/nm-$role.txt" "$evidence/layout-$role.tsv"
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" pkg/codegen \
    -test.run='^TestMarshalStatePreservesEncodingAndValidation$' -test.v -test.timeout=90s > "$evidence/test-$role.log" 2>&1
  grep -q '^--- PASS: TestMarshalStatePreservesEncodingAndValidation ' "$evidence/test-$role.log"
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" pkg/codegen \
    -test.run='^$' -test.bench='^BenchmarkMigrationStateValidation$' -test.benchmem \
    -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/inventory-$role.txt" 2>> "$evidence/benchmark-stderr.txt"
done
python3 "$driver" inventory "$evidence" > "$evidence/inventory-cpu.txt"
python3 "$driver" check-sources "$scratch/source"

date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
sample_groups
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
python3 "$driver" samples "$evidence" > "$evidence/sample-cpu.txt"
python3 "$driver" check-sources "$scratch/source"

printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
for comparison in aa ab; do
  for group in primary confirmation; do
    normalize_group "$comparison" "$group"
  done
  stem="$evidence/$comparison"
  status=0
  "$scratch/bin/benchgate" "$stem.primary.csv" "$stem.confirmation.csv" > "$stem.benchgate.log" 2>&1 || status=$?
  if (( status != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$stem.benchgate.log"
  fi
  printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
done
python3 "$driver" qualify "$evidence"
