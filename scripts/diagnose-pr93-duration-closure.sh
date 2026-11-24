#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=diagnose-hosted-perf.sh
source "$script_dir/diagnose-hosted-perf.sh"
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
base=925f30ee1dcd45882b29130b8ed02836ed88317e
head=40153f3e18f9f32baca9f656986a5e696f392a2f
product=c3d43d380ccf634ffb8ec958b269cc7a21945b2f
package=pkg/codegen
filter='^BenchmarkCompileDecimalIntegerLiterals$'
evidence="$repo/.tmp/hosted-pr93-duration-closure"
helper="$script_dir/diagnose-pr93-duration-evidence.py"
mode=${1:-native}
[[ $# -le 1 && ( "$mode" == native || "$mode" == --inventory-only ) ]]
mkdir -p "$repo/.tmp"
mkdir "$evidence"
scratch=$(mktemp -d "$repo/.tmp/pr93-duration-closure.XXXXXXXX")
trap cleanup EXIT
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source"
[[ "$(go env GOVERSION)" == go1.27.1 && -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
if [[ "$mode" == native ]]; then
  collect_host > "$evidence/environment.txt"
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  python3 "$helper" prior "$evidence" "$repo/.tmp/pr93-duration-official-45386469196-1.zip"
  grep -Eq '^Model name:[[:space:]]*AMD EPYC 7763 64-Core Processor$' "$evidence/environment.txt" || {
    printf '%s\n' 'Run the native comparison on the original AMD EPYC 7763 model.' >&2
    exit 3
  }
fi
merge=$(git -C "$repo" merge-tree --write-tree "$base" "$head")
test "$merge" = "$product"
printf 'base=%s\nhead=%s\nproduct_tree=%s\n' "$base" "$head" "$merge" > "$evidence/revisions.txt"
git -C "$repo" diff --exit-code "$base" "$product" -- "$package"
for role in base head; do
  role_tree="$base"
  if [[ "$role" == head ]]; then role_tree="$product"; fi
  dir="$scratch/source/$role"
  mkdir -p "$dir" "$scratch/bin/$role" "$scratch/bin/$role-isolated"
  git -C "$repo" archive "$role_tree" | tar -xf - -C "$dir"
  sh "$dir/scripts/setup-modules.sh" "$dir"
  cp "$dir/go.work" "$evidence/go-$role.work.txt"
  (
    cd "$dir"
    sha256sum "$package/compile.go" "$package/compile_test.go" "$package/generator.go" "$package/numeric_literal_bench_test.go"
  ) > "$evidence/source-$role.sha256"
done
cmp "$evidence/source-base.sha256" "$evidence/source-head.sha256"
(
  cd "$scratch/source/base"
  for tool in benchrun benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  go build -o "$scratch/bin/project" "$script_dir/diagnose-pr93-duration-overlay/main.go"
  if [[ "$mode" == native ]]; then
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  fi
) > "$evidence/tools.log" 2>&1

for role in base head; do
  dir="$scratch/source/$role"
  "$scratch/bin/project" "$dir/$package" "$scratch/projection-$role"
  (
    cd "$scratch/projection-$role"
    sha256sum compile_test.go numeric_literal_bench_test.go
  ) > "$evidence/projection-$role.sha256"
  "$scratch/bin/benchrun" compile "$dir" "$scratch/bin/$role" "$package" > "$evidence/compile-$role.log" 2>&1
  (
    cd "$dir"
    export GOWORK="$dir/go.work"
    go test -trimpath -c "-overlay=$scratch/projection-$role/overlay.json" "./$package" \
      -o "$scratch/bin/$role-isolated/pkg-codegen.test"
  ) > "$evidence/compile-$role-isolated.log" 2>&1
  record_binaries "$role"
  record_binaries "$role-isolated"
  for variant in default isolated; do
    binary_root="$scratch/bin/$role"
    if [[ "$variant" == isolated ]]; then binary_root+="-isolated"; fi
    "$scratch/bin/benchrun" run "$dir" "$binary_root" "$package" -test.list='.*' > "$evidence/$role-$variant.roots"
    "$scratch/bin/benchrun" run "$dir" "$binary_root" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s \
      > "$evidence/$role-$variant.inventory.txt"
  done
  "$scratch/bin/benchrun" run "$dir" "$scratch/bin/$role" "$package" \
    -test.run='^Test(DecimalLeadingZeroLiteralEmitters|DecimalLeadingZeroGeneratesCanonicalSource|NonIntegerLeadingZeroLiteralControls)$' \
    -test.v -test.timeout=90s > "$evidence/$role-ordinary-tests.log"
done
cmp "$evidence/projection-base.sha256" "$evidence/projection-head.sha256"
python3 "$helper" inventory "$evidence"
if [[ "$mode" == --inventory-only ]]; then exit 0; fi

for role in base head; do
  bin="$scratch/bin/$role-isolated/pkg-codegen.test"
  go tool nm -size "$bin" > "$evidence/$role-isolated.nm"
  go tool objdump -s 'codegen\.(BenchmarkCompileDecimalIntegerLiterals.*|compileDurationLiteral|decimalIntegerLiteral|.*compileLiteral)' \
    "$bin" > "$evidence/$role-isolated.asm"
done
python3 "$helper" code "$evidence" "$scratch/bin"

comparisons=(aa ab isolated-aa isolated-ab)
mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
for group in primary confirmation; do
  offset=0
  if [[ "$group" == confirmation ]]; then offset=1; fi
  for pair in {1..10}; do
    printf -v sample '%02d' "$pair"
    if (((pair + offset) % 2 == 1)); then first=left; else first=right; fi
    for index in {0..3}; do
      comparison="${comparisons[(index + pair + offset + 1) % 4]}"
      other=base
      if [[ "$comparison" == *ab ]]; then other=head; fi
      variant=default
      if [[ "$comparison" == isolated-* ]]; then variant=isolated; fi
      run_pair "$group" "$sample" "$comparison" "$other" "$first" 01 "$package" "$variant" "$filter"
    done
  done
done
python3 "$helper" samples "$evidence"
printf 'comparison\tgate_exit\n' > "$evidence/verdicts.tsv"
for comparison in "${comparisons[@]}"; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
  if [[ "$comparison" == *ab ]]; then
    args=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base" "${args[@]}")
  fi
  rc=0
  "$scratch/bin/benchgate" "${args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || rc=$?
  if ((rc != 0)); then grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log"; fi
  printf '%s\t%s\n' "$comparison" "$rc" >> "$evidence/verdicts.tsv"
done
cat "$evidence/verdicts.tsv"
python3 "$helper" verdict "$evidence"
