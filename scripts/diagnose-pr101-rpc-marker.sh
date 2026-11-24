#!/usr/bin/env bash
set -euo pipefail

# Run from the diagnostic checkout with an independently restored candidate index.
if (( $# < 1 || $# > 2 )) || [[ "${2:-run}" != run && "${2:-run}" != --inventory-only ]]; then
  printf 'usage: %s <restored-candidate-root> [--inventory-only]\n' "$0" >&2
  exit 2
fi
candidate=$(cd "$1" && pwd -P)
mode="${2:-run}"
repo=$(git rev-parse --show-toplevel)
base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
base_tree=ba252c7cace8908cdfcd7c203bc9dc11bb2ba746
head=8f6633bfc9f325c857e1217112a5e2039e2d85de
head_tree=74f2f384715be4780412fa34a60ca997158fe058
candidate_tree=011cb034493a360bfd6e0494faed10918f0104fd
combined_tree=1f632ddd6d4117fc1f2268618f31ceac972d20fb
patch_sha=34d7f8c66c41e6388225c166dba1c75df02ab26197a382b5ab183d31e58eb1a6
sampler_revision=61521c06948d6f4cdf04cd94abb6a19fdd2c5ac7
sampler_sha=9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
legacy_fixture=99658ed681f4110df0db3fc688931fa61cd656f3c62477c04a1ffd4bb567ca8e
modern_fixture=555e0a031f0f1286bca7233b6fd5c28854e5a3de3b29aad6e05e5019bbf4c578
rpc=pkg/lux/rpc
fixture="$rpc/public_boundary_test.go"
binary=pkg-lux-rpc.test

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr101-rpc-marker.XXXXXXXX")
evidence="$scratch/evidence"
if [[ "$mode" == run ]]; then evidence="$repo/.tmp/pr101-rpc-marker-evidence"; fi
created=false
cleanup_marker_proof() {
  local result=$?
  trap - EXIT
  [[ "$scratch" == "$repo"/.tmp/pr101-rpc-marker.* && -d "$scratch" && ! -L "$scratch" ]] || exit 99
  if (( result != 0 )) && [[ -f "$evidence/build.log" ]]; then tail -n 50 "$evidence/build.log" >&2; fi
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$result" > "$evidence/cleanup.txt"
  fi
  exit "$result"
}
trap cleanup_marker_proof EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
created=true
mkdir -p "$scratch"/{bin,tmp,tools,source/base/.tmp,source/head/.tmp}
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local LC_ALL=C
test "$(go env GOVERSION)" = go1.27.1
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
if [[ "$mode" == run ]]; then
  test "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'linux\namd64\nv1\n1'
fi

git -C "$repo" show "$sampler_revision:scripts/diagnose-hosted-perf.sh" > "$scratch/sampler.sh"
printf '%s  %s\n' "$sampler_sha" "$scratch/sampler.sh" | sha256sum -c -
# The shared sampler defines its own legacy example revisions.
source "$scratch/sampler.sh"
base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
head=8f6633bfc9f325c857e1217112a5e2039e2d85de
if [[ "$mode" == run ]]; then collect_host > "$evidence/environment.txt"; fi

restore_pair() {
  local store="$scratch/objects.git"
  test "$(git -C "$candidate" rev-parse --show-toplevel)" = "$candidate"
  test "$(git -C "$candidate" rev-parse HEAD)" = "$base"
  test "$(git -C "$candidate" write-tree)" = "$candidate_tree"
  git -C "$candidate" diff --exit-code
  test "$(git -C "$repo" rev-parse "$base^{tree}")" = "$base_tree"
  test "$(git -C "$repo" rev-parse "$head^")" = "$base"
  test "$(git -C "$repo" rev-parse "$head^{tree}")" = "$head_tree"
  git -C "$candidate" diff --no-color --no-ext-diff --full-index --binary "$base" "$candidate_tree" > "$scratch/candidate.patch"
  printf '%s  %s\n' "$patch_sha" "$scratch/candidate.patch" | sha256sum -c -

  git init -q --bare "$store"
  git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$store/objects/info/alternates"
  git --git-dir="$store" read-tree "$head"
  git --git-dir="$store" apply --cached --check --whitespace=error "$scratch/candidate.patch"
  git --git-dir="$store" apply --cached --whitespace=error "$scratch/candidate.patch"
  test "$(git --git-dir="$store" write-tree)" = "$combined_tree"
  git --git-dir="$store" archive "$combined_tree" | tar -xf - -C "$scratch/source/head"
  git --git-dir="$store" apply --cached --reverse --whitespace=error "$scratch/candidate.patch"
  test "$(git --git-dir="$store" write-tree)" = "$head_tree"
  git -C "$repo" archive "$base" | tar -xf - -C "$scratch/source/base"
  git -C "$candidate" archive "$candidate_tree" | tar -xf - -C "$scratch/tools"

  printf '%s\n' "base $base $base_tree" "head $head $head_tree" "marker $candidate_tree $patch_sha" \
    "head-plus-marker $combined_tree" "sampler $sampler_revision $sampler_sha" > "$evidence/revisions.txt"
  printf '%s  %s\n' "$legacy_fixture" "$scratch/source/base/$fixture" \
    "$modern_fixture" "$scratch/source/head/$fixture" | sha256sum -c -
  sha256sum "$scratch/source/"{base,head}"/$fixture" > "$evidence/fixture-source.sha256"
}

compile_inventory() {
  local role="$1" variant="$2" command="$3" path stem testbin
  path="$scratch/bin/$role-$variant"
  stem="$evidence/$role-$variant"
  "$scratch/bin/benchrun" "$command" "$scratch/source/$role" "$path" "$rpc"
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$path" "$rpc" \
    -test.run='^$' -test.bench=. -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
    -test.timeout=180s > "$stem.inventory.raw"
  "$scratch/bin/benchplan" --normalize "$stem.inventory.raw" "$stem.inventory.txt"
  awk '/^Benchmark/ {print $1}' "$stem.inventory.txt" | sort > "$stem.leaves"
  test "$(wc -l < "$stem.leaves")" -eq 214
  testbin="$path/$binary"
  { sha256sum "$testbin"; go tool buildid "$testbin"; go version -m "$testbin"; } > "$stem.binary.txt"
  go tool nm "$testbin" > "$scratch/$role-$variant.nm"
  awk '$NF ~ /\.Benchmark/ && $NF ~ /\/rpc(_test)?\./ {print $NF}' "$scratch/$role-$variant.nm" | sort -u > "$stem.symbols"
  test "$(wc -l < "$stem.symbols")" -eq 123
}

verify_full_inventory() {
  local role type suffix
  for role in base head; do
    "$scratch/bin/benchplan" --verify-equal-inventories \
      "$evidence/$role-ordinary.inventory.txt" "$evidence/$role-v2.inventory.txt"
    for suffix in leaves symbols; do
      cmp "$evidence/base-ordinary.$suffix" "$evidence/$role-ordinary.$suffix"
      cmp "$evidence/base-ordinary.$suffix" "$evidence/$role-v2.$suffix"
    done
    for type in TestInternalDispatchCannotBeForgedAcrossPublicTransports TestSchemaFreeRouterStreamBoundary; do
      grep -Eq "/rpc\\.$type$" "$scratch/$role-ordinary.nm"
      if grep -Eq "/rpc\\.$type$" "$scratch/$role-v2.nm"; then return 1; fi
    done
    grep -Eq '/h2transport\.' "$scratch/$role-ordinary.nm"
    if grep -Eq '/h2transport\.' "$scratch/$role-v2.nm"; then return 1; fi
    test -z "$(find "$scratch/source/$role/.tmp" -name 'bench-rpc-overlay-*.json' -print)"
  done
  cmp "$scratch/bin/base-v2/$binary" "$scratch/bin/head-v2/$binary"
  if cmp -s "$scratch/bin/base-ordinary/$binary" "$scratch/bin/head-ordinary/$binary"; then return 1; fi
  printf 'Four complete inventories match (214 leaves and 123 symbols); both v2 executables are byte-identical.\n'
}

plan_selected() {
  cat > "$evidence/targets.tsv" <<'CASES'
pkg/lux/rpc	BenchmarkLRPCDirectRequest/0/traced-after-2
pkg/lux/rpc	BenchmarkLRPCDirectRequest/0/traced-before-2
pkg/lux/rpc	BenchmarkRPCDeadlineClassification/ExpiredEOF/After-2
pkg/lux/rpc	BenchmarkRPCDeadlineClassification/ExpiredEOF/Before-2
pkg/lux/rpc	BenchmarkRPCNilContextGuard/NoToken/After-2
pkg/lux/rpc	BenchmarkRPCNilContextGuard/NoToken/Before-2
pkg/lux/rpc	BenchmarkRPCNilContextGuard/NoToken/GuardOnly-2
CASES
  jq -Rn --arg pkg "$rpc" '[inputs | {Package: $pkg, Name: .}] | {Base: ., Head: .}' \
    < "$evidence/base-v2.leaves" > "$evidence/full-inventory.json"
  "$scratch/bin/benchplan" --confirmation-plan "$evidence/full-inventory.json" "$evidence/targets.tsv" \
    "$evidence/batch.tsv" "$evidence/expected.json"
  test "$(jq '.Base | length' "$evidence/expected.json")" -eq 7
  test "$(wc -l < "$evidence/batch.tsv")" -eq 1
  awk -F '\t' 'NR == FNR {if (FNR == 1) name=$2; next} {print "01\t" $1 "\tv2\t" name "\t" $2}' \
    "$evidence/targets.tsv" "$evidence/batch.tsv" > "$evidence/cases.tsv"
  cat "$evidence/cases.tsv"
}

record_selected() {
  local role="$1" filter="$2" stem="$evidence/$1-selected"
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role-v2" "$rpc" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x \
    -test.count=1 -test.cpu=2 -test.timeout=90s > "$stem.raw"
  "$scratch/bin/benchplan" --normalize "$stem.raw" "$stem.txt"
  awk '/^Benchmark/ {if (NF != 8 || $2 != 1 || $4 != "ns/op" || $6 != "B/op" || $8 != "allocs/op") exit 1; print $1}' \
    "$stem.txt" | sort > "$stem.leaves"
  cut -f2 "$evidence/targets.tsv" | sort | cmp "$stem.leaves" -
}

restore_pair
(
  cd "$scratch/tools"
  for tool in benchrun benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  for role in base head; do sh scripts/setup-modules.sh "$scratch/source/$role"; done
) > "$evidence/build.log" 2>&1
"$scratch/bin/benchrun" performance-profile > "$evidence/compile-profile.txt"
printf 'rpc-boundary-overlay-v2 %s\n' "$legacy_fixture" | cmp "$evidence/compile-profile.txt" -
compile_inventory base ordinary compile
compile_inventory base v2 compile-performance-baseline
compile_inventory head ordinary compile
compile_inventory head v2 compile-performance
verify_full_inventory
plan_selected
IFS=$'\t' read -r planned_package filter < "$evidence/batch.tsv"
test "$planned_package" = "$rpc"
for role in base head; do record_selected "$role" "$filter"; done
if [[ "$mode" == --inventory-only ]]; then
  for role in base head; do head -n 1 "$evidence/$role-v2.binary.txt"; done
  printf 'Both roles ran all seven selected leaves with all three metrics.\n'
  exit 0
fi

GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
for role in base head; do
  executable="$scratch/bin/$role-v2/$binary"
  symbols='rpc\..*(Benchmark(LRPCDirectRequest|RPCDeadlineClassification|RPCNilContextGuard)|prepareTracedCallFrame|contextError(AfterIO|BeforeDeadlineFix)|requestContext|beforeNilContextGuard)'
  go tool nm -size -sort address "$executable" | awk -v re="$symbols" '$NF ~ re' > "$evidence/$role-v2.nm.txt"
  go tool objdump -s "$symbols" "$executable" > "$evidence/$role-v2.disassembly.txt"
  readelf -h -lW "$executable" > "$evidence/$role-v2.elf.txt"
  test -s "$evidence/$role-v2.nm.txt"
  test -s "$evidence/$role-v2.disassembly.txt"
done
cmp "$evidence/base-v2.nm.txt" "$evidence/head-v2.nm.txt"
date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
sample_groups
test "$(find "$evidence/raw" -type f | wc -l)" -eq 80
test "$(wc -l < "$evidence/order.tsv")" -eq 81
grep -h '^cpu: ' "$evidence"/{base,head}-selected.txt "$evidence"/raw/*.txt | sort -u > "$evidence/cpu.txt"
test "$(wc -l < "$evidence/cpu.txt")" -eq 1
analyze_groups
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
printf 'comparison\tbenchgate_exit\naa\t0\nab\t0\n' | cmp "$evidence/verdicts.tsv" -
