#!/usr/bin/env bash
set -euo pipefail

mode="${1:-run}"
[[ $# -le 1 ]] || exit 2
case "$mode" in run|--plan-only|--inventory-only) ;; *) exit 2;; esac
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
sampler="$script_dir/diagnose-hosted-perf.sh"
helper="$script_dir/diagnose-pr71-linked-fixtures.py"
printf '%s  %s\n' 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 "$sampler" | sha256sum -c -
source "$sampler"

base=925f30ee1dcd45882b29130b8ed02836ed88317e
base_tree=a697ac4025e43a75343715bb5690e55c11e00063
head=ec041a07b105c9caf9e327a2cbf73de772582d00
product=9f4a663562d2a63d8662d60bcb93013a172f263e
driver=0130c1752f2f9a346729e7cbf73df353082402e3
reference_base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
reference_tree=b0b7d9b7c51aa67f14bf8545f05e2339ae26f187
target_cpu='AMD EPYC 7763 64-Core Processor'
packages=(api luvia)
conditions=(default isolated)

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr71-linked-fixtures.XXXXXXXX")
root="$repo/.tmp/hosted-pr71-linked-fixtures"
if [[ "$mode" != run ]]; then root="$scratch/evidence"; fi
created=false
cleanup_linked_fixtures() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr71-linked-fixtures.* ]] || exit 99
  if (( status != 0 )) && [[ "$mode" != run && -d "$root" ]]; then
    for log in "$root"/compile-*.log "$root"/tools.log; do
      if [[ -f "$log" ]]; then tail -n 12 "$log" >&2; fi
    done
  fi
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$root/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_linked_fixtures EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$root"
created=true
printf 'base=%s\nhead=%s\ntested_tree=%s\nfixture_driver=%s\noriginal_inventory_run=45334352793\noriginal_same_model_run=45371201347\noriginal_same_model_artifact=14908914130\n' \
  "$base" "$head" "$product" "$driver" > "$root/revisions.txt"
cp "${BASH_SOURCE[0]}" "$sampler" "$helper" "$root/"

# Record the actual machine before building or timing either condition.
if [[ "$mode" == run ]]; then
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  collect_host > "$root/environment.txt"
  observed=$(awk '/^Model name:/ {sub(/^Model name:[[:space:]]*/, ""); print}' "$root/environment.txt")
  test -n "$observed"
  jq -n --arg expected "$target_cpu" --arg observed "$observed" \
    '{expected:$expected, observed:$observed, matchesRequestedModel:($expected == $observed)}' > "$root/runner-cpu.json"
  if [[ "$observed" != "$target_cpu" ]]; then
    cat "$root/runner-cpu.json"
    exit 3
  fi
fi

# Keep projected merge objects in the owned scratch even on a read-only checkout.
original_objects=$(git -C "$repo" rev-parse --path-format=absolute --git-path objects)
mkdir "$scratch/objects"
export GIT_OBJECT_DIRECTORY="$scratch/objects" GIT_ALTERNATE_OBJECT_DIRECTORIES="$original_objects"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = "$base_tree"
test "$(git -C "$repo" merge-tree --write-tree "$base" "$head")" = "$product"
test "$(git -C "$repo" merge-tree --write-tree "$reference_base" "$head")" = "$reference_tree"
git -C "$repo" diff --exit-code "$base" "$product" -- pkg/lux/api pkg/lux/luvia scripts/benchgate scripts/benchplan internal/benchlayout
git -C "$repo" diff --exit-code "$reference_tree" "$product" -- . \
  ':!pkg/lux/api/binary_bench_test.go' \
  ':!packages/dart/lib/src/http2_exchange.dart' \
  ':!packages/dart/test/fixtures/http2_server.go' \
  ':!packages/dart/test/http2_integration_test.dart'
python3 "$helper" plan "$root"
if [[ "$mode" == --plan-only ]]; then
  cat "$root/revisions.txt" "$root/planned-scopes.json"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local LC_ALL=C
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/tool-source" "$scratch/source/base/.tmp" "$scratch/source/head/.tmp"
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
git -C "$repo" archive "$base" | tar -xf - -C "$scratch/source/base"
git -C "$repo" archive "$product" | tar -xf - -C "$scratch/source/head"
git -C "$repo" archive "$driver" -- go.mod go.sum internal/benchlayout scripts/benchrun | tar -xf - -C "$scratch/tool-source"
for role in base head; do
  directory="$scratch/source/$role"
  sh "$directory/scripts/setup-modules.sh" "$directory" > "$root/workspace-$role.log"
done
(
  cd "$scratch/tool-source"
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  cd "$scratch/source/base"
  go build -o "$scratch/bin/benchplan" ./scripts/benchplan
  if [[ "$mode" == run ]]; then
    go build -o "$scratch/bin/benchgate" ./scripts/benchgate
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  fi
) > "$root/tools.log" 2>&1

for role in base head; do
  mkdir "$scratch/bin/$role" "$scratch/bin/$role-isolated"
done
for pack in "${packages[@]}"; do
  package="pkg/lux/$pack"
  binary="pkg-lux-$pack.test"
  "$scratch/bin/benchrun" compile-pair "$scratch/source/base" "$scratch/source/head" "$scratch/pair-$pack" "$package" \
    > "$root/compile-frozen-$pack.log" 2>&1
  cp "$scratch/pair-$pack/fixtures.json" "$root/$pack/frozen-pair.json"
  for role in base head; do
    overlay=$(python3 "$helper" overlay "$root" "$scratch" "$pack" "$role")
    ln -s "$scratch/pair-$pack/$role/$binary" "$scratch/bin/$role/$binary"
    (
      cd "$scratch/source/$role"
      GOWORK="$PWD/go.work" go test -trimpath -c -overlay="$overlay" "./$package" -o "$scratch/bin/$role-isolated/$binary"
    ) > "$root/compile-$role-$pack-isolated.log" 2>&1
  done
done

for role in base head; do
  evidence="$root"
  record_binaries "$role"
  record_binaries "$role-isolated"
  for pack in "${packages[@]}"; do
    package="pkg/lux/$pack"
    binary="pkg-lux-$pack.test"
    inventory_filter=.
    if [[ "$pack" == luvia ]]; then inventory_filter='^BenchmarkPrimaryKeys$|^BenchmarkTraceEncodingComparison$'; fi
    for condition in "${conditions[@]}"; do
      evidence="$root/$pack/$condition"
      bin_role="$role"
      if [[ "$condition" == isolated ]]; then bin_role="$role-isolated"; fi
      go tool nm -size "$scratch/bin/$bin_role/$binary" | \
        grep -E '/auth\.(signWithSubject|LoadConfig|parseDuration)$|/(api|luvia)\.Benchmark' > "$evidence/$role-symbols.nm"
      for phase in inventory selected; do
        filter="$inventory_filter"
        if [[ "$phase" == selected ]]; then filter=$(cat "$evidence/filter.txt"); fi
        "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$bin_role" "$package" \
          -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
          -test.timeout=300s > "$evidence/$role-$phase.raw" 2> "$evidence/$role-$phase.stderr"
        "$scratch/bin/benchplan" --normalize "$evidence/$role-$phase.raw" "$evidence/$role-$phase.txt"
      done
    done
  done
done
sample_cpu=
if [[ "$mode" == run ]]; then sample_cpu="$target_cpu"; fi
python3 "$helper" inventory "$root" "$sample_cpu"
printf '%s\n' 'Both API inventories and the 26 Luvia leaves match. Each timed filter ran every leaf exactly once on both source roles.'
if [[ "$mode" == --inventory-only ]]; then exit 0; fi

for pack in "${packages[@]}"; do
  for condition in "${conditions[@]}"; do
    evidence="$root/$pack/$condition"
    mkdir "$evidence/raw"
    printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  done
done
printf 'group\tpair\tcomparison\tcondition\tpackage\n' > "$root/condition-order.tsv"
offset=0
for group in primary confirmation; do
  for number in {1..10}; do
    printf -v sample '%02d' "$number"
    if (( (number + offset) % 2 == 1 )); then
      first=left
      comparisons=(ab aa)
      scope_conditions=(default isolated)
      scope_packages=(api luvia)
    else
      first=right
      comparisons=(aa ab)
      scope_conditions=(isolated default)
      scope_packages=(luvia api)
    fi
    for comparison in "${comparisons[@]}"; do
      other=base
      if [[ "$comparison" == ab ]]; then other=head; fi
      for condition in "${scope_conditions[@]}"; do
        for pack in "${scope_packages[@]}"; do
          evidence="$root/$pack/$condition"
          printf '%s\t%s\t%s\t%s\t%s\n' "$group" "$sample" "$comparison" "$condition" "$pack" >> "$root/condition-order.tsv"
          run_pair "$group" "$sample" "$comparison" "$other" "$first" 01 "pkg/lux/$pack" "$condition" "$(cat "$evidence/filter.txt")"
        done
      done
    done
  done
  offset=1
done
python3 "$helper" audit "$root"
for pack in "${packages[@]}"; do
  for condition in "${conditions[@]}"; do
    evidence="$root/$pack/$condition"
    analyze_groups
  done
done
python3 "$helper" summarize "$root"
