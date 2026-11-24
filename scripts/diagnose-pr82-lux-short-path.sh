#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=diagnose-hosted-perf.sh
source "$script_dir/diagnose-hosted-perf.sh"
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
base=925f30ee1dcd45882b29130b8ed02836ed88317e
head=0bcc72b6861423c53b615d3b80a5cad36688d4ff
product=6a37c2f1ffd8a8dcf66058d3f50dea6972e9316f
package=pkg/lux
roots='^(BenchmarkStrictFilterContract|BenchmarkDecimalAlignment)$'
evidence="$repo/.tmp/hosted-pr82-lux-short-path"
helper="$script_dir/diagnose-pr82-lux-evidence.py"
mode=${1:-native}
[[ $# -le 1 && ( "$mode" == native || "$mode" == --inventory-only ) ]]
mkdir -p "$repo/.tmp"
mkdir "$evidence"
if [[ "$mode" == native ]]; then
  collect_host > "$evidence/environment.txt"
  shard=$(python3 "$helper" select "$evidence")
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  python3 "$helper" prior "$evidence" "$repo/.tmp/pr82-short-path-$shard.zip"
fi
scratch=$(mktemp -d "$repo/.tmp/pr82-lux-short-path.XXXXXXXX")
trap cleanup EXIT
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source"
[[ "$(go env GOVERSION)" == go1.27.1 && -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
merge=$(git -C "$repo" merge-tree --write-tree "$base" "$head")
test "$merge" = "$product"
printf 'base=%s\nhead=%s\nproduct_tree=%s\n' "$base" "$head" "$merge" > "$evidence/revisions.txt"
git -C "$repo" diff --name-status "$base" "$merge" > "$evidence/product-paths.tsv"
for role in base head; do
  revision="$base"
  if [[ "$role" == head ]]; then revision="$product"; fi
  dir="$scratch/source/$role"
  mkdir -p "$dir" "$scratch/bin/$role" "$scratch/bin/$role-isolated"
  git -C "$repo" archive "$revision" | tar -xf - -C "$dir"
  sh "$dir/scripts/setup-modules.sh" "$dir"
  cp "$dir/go.work" "$evidence/go-$role.work.txt"
  (
    cd "$dir"
    sha256sum "$package/condition_filter.go" "$package/condition_filter_bench_test.go" "$package/decimal.go" "$package/decimal_bench_test.go"
  ) > "$evidence/source-$role.sha256"
done
cmp "$evidence/source-base.sha256" "$evidence/source-head.sha256"
(
  cd "$scratch/source/base"
  for tool in benchrun benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  go build -o "$scratch/bin/project" "$script_dir/diagnose-pr82-lux-overlay/main.go"
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
    sha256sum condition_filter_bench_test.go decimal_bench_test.go
  ) > "$evidence/projection-$role.sha256"
  "$scratch/bin/benchrun" compile "$dir" "$scratch/bin/$role" "$package" > "$evidence/compile-$role.log" 2>&1
  (
    cd "$dir"
    export GOWORK="$dir/go.work"
    go test -trimpath -c "-overlay=$scratch/projection-$role/overlay.json" "./$package" \
      -o "$scratch/bin/$role-isolated/pkg-lux.test"
  ) > "$evidence/compile-$role-isolated.log" 2>&1
  record_binaries "$role"
  record_binaries "$role-isolated"
  for variant in default isolated; do
    binary_root="$scratch/bin/$role"
    if [[ "$variant" == isolated ]]; then binary_root+="-isolated"; fi
    "$scratch/bin/benchrun" run "$dir" "$binary_root" "$package" -test.list='.*' > "$evidence/$role-$variant.roots"
    "$scratch/bin/benchrun" run "$dir" "$binary_root" "$package" \
      -test.run='^$' -test.bench="$roots" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s \
      > "$evidence/$role-$variant.inventory.txt"
  done
  "$scratch/bin/benchrun" run "$dir" "$scratch/bin/$role" "$package" \
    -test.run=. -test.v -test.timeout=90s > "$evidence/$role-ordinary-tests.log"
done
cmp "$evidence/projection-base.sha256" "$evidence/projection-head.sha256"
python3 "$helper" inventory "$evidence"
if [[ "$mode" == --inventory-only ]]; then exit 0; fi

for role in base head; do
  for variant in default isolated; do
    root="$scratch/bin/$role"
    if [[ "$variant" == isolated ]]; then root+="-isolated"; fi
    go tool nm -size "$root/pkg-lux.test" > "$evidence/$role-$variant.nm"
    go tool objdump -s 'lux\.(Benchmark(StrictFilterContract|DecimalAlignment).*|TimeField\.(FilterOp|Gte)|comparisonOperator|AddDecimal|SubDecimal|.*rawCond.*ToSQL|appendRebasedSQL)' \
      "$root/pkg-lux.test" > "$evidence/$role-$variant.asm"
  done
done
python3 "$helper" code "$evidence"

comparisons=(aa ab isolated-aa isolated-ab)
filter=$(cat "$evidence/filter.txt")
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
