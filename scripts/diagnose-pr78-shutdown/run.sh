#!/usr/bin/env bash
set -euo pipefail

[[ $# == 2 || ( $# == 3 && "$3" == --plan-only ) ]]
lane="$1"
historical="$(cd "$2" && pwd -P)"
plan="${3:-}"
repo=$(git rev-parse --show-toplevel)
driver=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
diagnostic=${LUXO_NATIVE_DIAGNOSTIC:-pr78-shutdown}
historical_base=65f1892e175487503aa36caa86d1eb8a0805f073
historical_head=397af9c8c6fc6dc807e1dbcbe2e05d8913f58944
sampler=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
sampler_sum=9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
patch_sum=65c0194f9d5354e2934eb1cf79015393652a6f98a72036f2f38c81fe354d219e
patch_file=shutdown.patch.b64
product_base=55f982e4237a9e196cc0cb1ef84b1f97570c0d04
base_tree=85e9600484d2531099ef28b2e5284b3ea4aed67c
head_tree=32ef8e8c41a5c21a22180759b5ac0d6ce6253dbd
patch_parent_commit=
patch_parent_tree=
product_paths=(pkg/lux/h2transport/{limits_test,serve_conn_test,server,server_test,shutdown_test}.go)
reference_paths=()
symbols='\.(Benchmark(AuthMiddlewareIsolation|GatewayIdentity|MergeDestination|TraceEncodingComparison|RPCPoolCloseSafetyReverse|ShutdownNoConnections|ShutdownOneOpeningConnection|PriorKnowledgeConnectionLifecycle)|benchmark(PoolSerialAfter|MergeDestination)|extractBearerToken|appendTraceRecord|\(\*(Pool|Server)\)\.(Put|GetContext|Shutdown|beginShutdown|retireConnection))'
case "$diagnostic" in
  pr78-shutdown) ;;
  pr83-f9)
    [[ "$lane" == 9v45 ]]
    driver="$repo/scripts/diagnose-pr83-f9"
    source "$driver/settings.sh"
    ;;
  pr143-allocation)
    [[ "$lane" == 6975 ]]
    driver="$repo/scripts/diagnose-pr143-allocation"
    source "$driver/settings.sh"
    ;;
  *) printf 'Unknown native diagnostic: %s\n' "$diagnostic" >&2; exit 2 ;;
esac
export LC_ALL=C GOWORK=off GOTOOLCHAIN=local PYTHONDONTWRITEBYTECODE=1
cpu=$(python3 "$driver/verify.py" model "$lane")

if [[ -z "$plan" ]]; then
  [[ "$(uname -sm)" == 'Linux x86_64' ]]
  actual=$(lscpu | awk -F: '$1 == "Model name" {gsub(/^[[:space:]]+|[[:space:]]+$/, "", $2); print $2}')
  [[ "$actual" == "$cpu" ]]
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  [[ -z "$(go env GOFLAGS)" ]]
fi

[[ ! -L "$repo/.tmp" ]]
mkdir -p "$repo/.tmp"
evidence="$repo/.tmp/$diagnostic-evidence-$lane"
if [[ -n "$plan" ]]; then evidence="$repo/.tmp/$diagnostic-plan-$lane"; fi
[[ ! -L "$evidence" ]]
mkdir -p "$evidence"
scratch=$(mktemp -d "$repo/.tmp/$diagnostic-stage.XXXXXXXX")
trap 'rm -rf -- "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$scratch/tmp"
export TMPDIR="$scratch/tmp" GOTMPDIR="$scratch/tmp"
python3 "$driver/verify.py" plan "$lane" "$historical" "$evidence"
git -C "$repo" show "$sampler:scripts/diagnose-hosted-perf.sh" > "$scratch/sampler.sh"
printf '%s  %s\n' "$sampler_sum" "$scratch/sampler.sh" | shasum -a 256 -c -
# The pinned helper supplies environment, binary receipts and CSV normalization.
source "$scratch/sampler.sh"
# The historical sampler declares its own defaults. Restore this run's binding.
base="$product_base"
[[ "$benchtime" == 200ms ]]
git -C "$repo" init --quiet --bare "$scratch/objects.git"
git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$scratch/objects.git/objects/info/alternates"
graft=(git "--git-dir=$scratch/objects.git")
python3 - "$driver/$patch_file" "$scratch/product.patch" <<'PY'
from base64 import b64decode
from pathlib import Path
import sys
source, target = map(Path, sys.argv[1:])
target.write_bytes(b64decode(b"".join(source.read_bytes().splitlines()), validate=True))
PY
printf '%s  %s\n' "$patch_sum" "$scratch/product.patch" | shasum -a 256 -c -
if [[ "$diagnostic" == pr78-shutdown ]]; then (
  cd "$driver"
  shasum -a 256 -c <<'SHA'
f1af98d2511b5ce90a3c521c577c8aea58809c09377ccffe71716bf8328ecff3  empty.fixture.go.txt
ea6127592cb1c6ae965007266ffead6e01eed7026e128e9346e722af64b397bb  opening.fixture.go.txt
SHA
); fi
[[ "$("${graft[@]}" rev-parse "$base^{tree}")" == "$base_tree" ]]
if [[ -n "$patch_parent_commit" ]]; then
  [[ "$("${graft[@]}" rev-parse "$patch_parent_commit^{tree}")" == "$patch_parent_tree" ]]
  parents=$("${graft[@]}" cat-file commit "$patch_parent_commit" | sed -n -e '/^$/q' -e 's/^parent //p')
  [[ "$parents" == "$base" ]]
fi
patch_tree="${patch_parent_tree:-$base_tree}"
"${graft[@]}" read-tree "$patch_tree"
"${graft[@]}" apply --cached --check --whitespace=error-all "$scratch/product.patch"
"${graft[@]}" apply --cached --whitespace=error-all "$scratch/product.patch"
[[ "$("${graft[@]}" write-tree)" == "$head_tree" ]]
"${graft[@]}" diff --check "$patch_tree" "$head_tree"
"${graft[@]}" diff --name-only "$patch_tree" "$head_tree" > "$evidence/product-paths.txt"
printf '%s\n' "${product_paths[@]}" | diff -u - "$evidence/product-paths.txt"
"${graft[@]}" apply --cached --reverse --whitespace=error-all "$scratch/product.patch"
[[ "$("${graft[@]}" write-tree)" == "$patch_tree" ]]
printf 'base=%s\nbase_tree=%s\nhead_tree=%s\nhistorical_base=%s\nhistorical_head=%s\nsampler=%s\npatch_sha256=%s\n' \
  "$base" "$base_tree" "$head_tree" "$historical_base" "$historical_head" "$sampler" "$patch_sum" > "$evidence/revisions.txt"
if [[ -n "$patch_parent_commit" ]]; then
  printf 'patch_parent=%s\npatch_parent_tree=%s\n' "$patch_parent_commit" "$patch_tree" >> "$evidence/revisions.txt"
  "${graft[@]}" diff --name-status "$base_tree" "$head_tree" > "$evidence/base-to-candidate.tsv"
  for path in "${reference_paths[@]}"; do
    expected=$("${graft[@]}" rev-parse "$base_tree:$path")
    [[ "$("${graft[@]}" rev-parse "$patch_tree:$path")" == "$expected" ]]
    [[ "$("${graft[@]}" rev-parse "$head_tree:$path")" == "$expected" ]]
    printf '%s %s\n' "$expected" "$path"
  done > "$evidence/reference-git-objects.txt"
fi
"${graft[@]}" ls-tree "$base_tree" -- go.mod go.sum scripts pkg/lux/{api,luvia,rpc,h2transport} > "$evidence/base-blobs.txt"
"${graft[@]}" ls-tree "$head_tree" -- go.mod go.sum scripts pkg/lux/{api,luvia,rpc,h2transport} > "$evidence/head-blobs.txt"
if [[ -n "$plan" ]]; then exit 0; fi

collect_host > "$evidence/environment.txt"
mkdir "$scratch/bin" "$scratch/source"
for role in base head; do
  src="$scratch/source/$role"
  mkdir -p "$src/.tmp"
  tree="$base_tree"
  if [[ "$role" == head ]]; then tree="$head_tree"; fi
  "${graft[@]}" archive "$tree" | tar -xf - -C "$src"
  if [[ "$lane" == 7763 ]]; then
    pkg="$src/pkg/lux/h2transport"
    for sample in empty opening; do
      fixture="$pkg/audit_shutdown_${sample}_bench_test.go"
      [[ ! -e "$fixture" && ! -L "$fixture" ]]
      cp "$driver/$sample.fixture.go.txt" "$fixture"
    done
  fi
  sh "$src/scripts/setup-modules.sh" "$src"
done
(
  cd "$scratch/source/base"
  for tool in benchrun benchplan benchgate; do
    go build -o "$scratch/bin/$tool" "./scripts/$tool"
  done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
)
"$scratch/bin/benchrun" performance-profile > "$evidence/official-profile.txt"
if [[ -f "$evidence/original-compile-profile.txt" ]]; then
  cmp "$evidence/original-compile-profile.txt" "$evidence/official-profile.txt"
fi
go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-version.txt"
grep -Fq "$benchstat_version" "$evidence/benchstat-version.txt"
binary_dir() {
  local role="$1" profile="$2" bin="$scratch/bin/$1"
  if [[ "$profile" != default ]]; then bin+="-$profile"; fi
  printf '%s\n' "$bin"
}

for role in base head; do
  src="$scratch/source/$role"
  while IFS=$'\t' read -r case_id package profile filter; do
    bin=$(binary_dir "$role" "$profile")
    mkdir -p "$bin"
    compile=compile-performance
    if [[ "$role" == base ]]; then compile=compile-performance-baseline; fi
    "$scratch/bin/benchrun" "$compile" "$src" "$bin" "$package" "$profile" \
      > "$evidence/compile-$role-$case_id.log" 2>&1
    "$scratch/bin/benchrun" run-performance "$src" "$bin" "$package" "$profile" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 \
      -test.cpu=2 -test.timeout=90s > "$evidence/inventory-$role-$case_id.txt" 2>&1
    python3 "$driver/verify.py" inventory "$lane" "$case_id" "$evidence/inventory-$role-$case_id.txt"
    # Preserve timed and changed functions for linked-layout diagnosis.
    binary_name=${package//\//-}
    if [[ "$profile" != default ]]; then binary_name+="-$profile"; fi
    binary="$bin/$binary_name.test"
    go tool nm -size "$binary" | grep -E -- "$symbols" > "$evidence/symbols-$role-$case_id.nm"
    go tool objdump -s "$symbols" "$binary" > "$evidence/symbols-$role-$case_id.objdump"
    [[ -s "$evidence/symbols-$role-$case_id.nm" ]]
    [[ -s "$evidence/symbols-$role-$case_id.objdump" ]]
  done < "$evidence/cases.tsv"
done
if [[ "$lane" == 7763 ]]; then
  tests=(RepeatedTimedOutShutdownLeavesNoWaiters ShutdownWaitsForLastConnectionAfterClose ShutdownDrainsAcceptedResponse ServeConnShutdownCancelsOpening)
  selector="^Test($(IFS='|'; printf '%s' "${tests[*]}"))$"
  "$scratch/bin/benchrun" run-performance "$scratch/source/head" "$scratch/bin/head" pkg/lux/h2transport default \
    -test.run="$selector" -test.count=1 -test.cpu=2 -test.timeout=90s -test.v > "$evidence/shutdown-correctness.log" 2>&1
  for test_name in "${tests[@]}"; do
    grep -Eq "^--- PASS: Test$test_name " "$evidence/shutdown-correctness.log"
  done
fi
for bin in "$scratch"/bin/{base,head}*; do record_binaries "${bin##*/}"; done

mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
python3 "$driver/verify.py" schedule "$lane" > "$scratch/schedule.tsv"
while IFS=$'\t' read -r group number comparison case_id side role variant file; do
  row=$(sed -n "$((10#$case_id))p" "$evidence/cases.tsv")
  IFS=$'\t' read -r planned_case package profile filter <<< "$row"
  [[ "$case_id" == "$planned_case" && "$variant" == "$profile" ]]
  bin=$(binary_dir "$role" "$profile")
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$group" "$number" "$comparison" "$case_id" "$side" "$role" "$profile" "$file" >> "$evidence/order.tsv"
  "$scratch/bin/benchrun" run-performance "$scratch/source/$role" "$bin" "$package" "$profile" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime="$benchtime" \
    -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/$file" 2>> "$evidence/benchmark-stderr.txt"
done < "$scratch/schedule.tsv"
comparisons=(aa bb ab)
python3 "$driver/verify.py" samples "$lane" "$evidence"
for bin in "$scratch"/bin/{base,head}*; do
  role=${bin##*/}
  cp "$evidence/binaries-$role.txt" "$scratch/previous-$role.txt"
  record_binaries "$role"
  cmp "$scratch/previous-$role.txt" "$evidence/binaries-$role.txt"
done
printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
control_failed=0
product_status=0
for comparison in "${comparisons[@]}"; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  options=()
  if [[ "$comparison" == ab ]]; then
    options=(--approved-costs "$scratch/source/base/scripts/benchgate/approved-costs.json" --base-sha "$base")
  fi
  status=0
  "$scratch/bin/benchgate" "${options[@]}" "$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv" \
    > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
  printf '%s\t%d\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
  if [[ "$comparison" == ab ]]; then product_status=$status; continue; fi
  if (( status != 0 )); then control_failed=1; fi
  for group in primary confirmation; do
    stem="$evidence/$comparison.$group"
    "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
      base="$stem.right.txt" head="$stem.left.txt" > "$stem.reverse.csv" 2> "$stem.reverse.warnings.txt"
  done
  status=0
  "$scratch/bin/benchgate" "$evidence/$comparison.primary.reverse.csv" "$evidence/$comparison.confirmation.reverse.csv" \
    > "$evidence/$comparison.reverse.benchgate.log" 2>&1 || status=$?
  printf '%s-reverse\t%d\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
  if (( status != 0 )); then control_failed=1; fi
done
cat "$evidence/verdicts.tsv"
if (( control_failed != 0 )); then exit 4; fi
exit "$product_status"
