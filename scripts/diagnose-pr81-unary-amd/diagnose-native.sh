#!/usr/bin/env bash
set -euo pipefail

[[ "$#" == 1 || ( "$#" == 2 && "$2" == --plan-only ) ]]
formal="$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")"
plan="${2:-}"
repo=$(git rev-parse --show-toplevel)
driver=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
main_revision=32a10c114ac3c282b4ce46189b646d28eb767b1b
main_tree=3ddf747cccd83c2110b3dbeb5ce6c099c4de7c8e
previous=1117eb9ffcc0beef977abceec656a351b0988389
pr_head=12e7743153a02a5c1cfc997e84ff5b6993a267bd
pr_tree=3dcde2a43cd604846985abd590aaa1950549929e
candidate_tree=e3fd039c3c24e1137c8a11f596df4f7ede6604f6
sampler_revision=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
sampler_digest=9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
patch_digest=47a0ebe1b04654ee3f2957766ac8f4021d1d11fad2c9bd333f01047181cb6161
package=pkg/lux/rpc
profile=default
cpu='AMD EPYC 9V74 80-Core Processor'
filter='^BenchmarkRPC_Echo$|^BenchmarkRPCTraceLocal$'

digest() {
  python3 -c 'import hashlib, pathlib, sys; print(hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest())' "$1"
}

[[ ! -L "$repo/.tmp" ]]
mkdir -p "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr81-unary-amd.XXXXXXXX")
trap 'rm -rf -- "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
evidence="$repo/.tmp/pr81-unary-amd-evidence"
if [[ "$plan" == --plan-only ]]; then
  evidence="$repo/.tmp/pr81-unary-amd-plan"
fi
[[ ! -L "$evidence" ]]
mkdir -p "$evidence"

if [[ -z "$plan" ]]; then
  [[ "$(uname -sm)" == 'Linux x86_64' ]]
  [[ "$(LC_ALL=C lscpu | awk -F: '$1 == "Model name" {sub(/^[[:space:]]+/, "", $2); sub(/[[:space:]]+$/, "", $2); print $2}')" == "$cpu" ]]
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64)" == $'linux\namd64\nv1' ]]
  [[ -z "$(go env GOFLAGS)" ]]
fi
git -C "$repo" show "${sampler_revision}:scripts/diagnose-hosted-perf.sh" > "$scratch/sampler.sh"
[[ "$(digest "$scratch/sampler.sh")" == "$sampler_digest" ]]
# shellcheck source=/dev/null
source "$scratch/sampler.sh"
base="$main_revision"
export LC_ALL=C GOWORK=off
mkdir "$scratch/tmp"
export TMPDIR="$scratch/tmp"

python3 "$driver/verify.py" formal "$formal" "$evidence"
python3 - "$driver/unary-fast.patch.b64" "$scratch/candidate.patch" <<'PYTHON'
import base64
import pathlib
import sys
source, target = map(pathlib.Path, sys.argv[1:])
target.write_bytes(base64.b64decode(b"".join(source.read_bytes().splitlines()), validate=True))
PYTHON
[[ "$(digest "$scratch/candidate.patch")" == "$patch_digest" ]]
[[ "$(git -C "$repo" rev-parse "${base}^{tree}")" == "$main_tree" ]]
[[ "$(git -C "$repo" merge-base "$base" "$pr_head")" == "$previous" ]]
store="$scratch/git"
git -C "$repo" init --quiet --bare "$store"
git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$store/objects/info/alternates"
graft=(git --git-dir="$store")
"${graft[@]}" read-tree "$base"
git -C "$repo" diff --binary "$previous" "$pr_head" > "$scratch/pr.patch"
"${graft[@]}" apply --cached --whitespace=error-all "$scratch/pr.patch"
[[ "$("${graft[@]}" write-tree)" == "$pr_tree" ]]
[[ "$("${graft[@]}" rev-parse "${pr_tree}:pkg")" == "$(git -C "$repo" rev-parse "${pr_head}:pkg")" ]]
"${graft[@]}" apply --cached --whitespace=error-all "$scratch/candidate.patch"
[[ "$("${graft[@]}" write-tree)" == "$candidate_tree" ]]
"${graft[@]}" diff --check "$pr_tree" "$candidate_tree"
[[ "$("${graft[@]}" diff --name-only "$pr_tree" "$candidate_tree")" == $'pkg/lux/rpc/client.go\npkg/lux/rpc/pool_lifecycle_test.go' ]]
"${graft[@]}" apply --cached --reverse --whitespace=error-all "$scratch/candidate.patch"
[[ "$("${graft[@]}" write-tree)" == "$pr_tree" ]]

version_tree() {
  case "$1" in
    base) printf '%s\n' "$main_tree" ;;
    head) printf '%s\n' "$pr_tree" ;;
    candidate) printf '%s\n' "$candidate_tree" ;;
    *) return 1 ;;
  esac
}

printf 'base %s %s\nhead %s %s\ncandidate %s\nsampler %s %s\npatch %s\n' \
  "$base" "$main_tree" "$pr_head" "$pr_tree" "$candidate_tree" "$sampler_revision" "$sampler_digest" "$patch_digest" > "$evidence/revisions.txt"
for role in base head candidate; do
  tree=$(version_tree "$role")
  for path in "$package"/{trace_local_bench_test.go,public_boundary_test.go,client.go,pool_conn.go,pool_lifecycle_test.go,rpc_test.go} \
    scripts/benchgate/approved-costs.json scripts/benchrun/main.go; do
    "${graft[@]}" show "$tree:$path" > "$scratch/source-file"
    printf '%s  %s\n' "$(digest "$scratch/source-file")" "$path"
  done > "$evidence/sources-$role.sha256"
  grep -Fxq 'bd60873a66ef965a610d9aecedb15921b7cd60d18889c3245dcacbd71719b8ce  pkg/lux/rpc/trace_local_bench_test.go' "$evidence/sources-$role.sha256"
done
for path in "$package"/{trace_local_bench_test.go,public_boundary_test.go} scripts/benchgate/approved-costs.json scripts/benchrun/main.go; do
  for role in head candidate; do
    [[ "$(grep -F "  $path" "$evidence/sources-base.sha256")" == "$(grep -F "  $path" "$evidence/sources-$role.sha256")" ]]
  done
done
# The whole RPC test file changed in the PR. Verify the original timed Echo declaration separately.
for role in base head candidate; do
  "${graft[@]}" show "$(version_tree "$role"):$package/rpc_test.go" > "$scratch/rpc-test.go"
  python3 "$driver/verify.py" echo "$scratch/rpc-test.go" >> "$scratch/echo-source.sha256"
done
[[ "$(sort -u "$scratch/echo-source.sha256" | wc -l | tr -d ' ')" == 1 ]]
sort -u "$scratch/echo-source.sha256" > "$evidence/echo-source.sha256"
[[ "$plan" != --plan-only ]] || exit 0

collect_host > "$evidence/environment.txt"
mkdir -p "$scratch/bin"
for role in base head candidate; do
  path="$scratch/source/$role"
  mkdir -p "$path/.tmp" "$scratch/bin/$role"
  "${graft[@]}" archive "$(version_tree "$role")" | tar -xf - -C "$path"
  sh "$scratch/source/base/scripts/setup-modules.sh" "$path"
done
(
  cd "$scratch/source/base"
  for name in benchrun benchplan benchgate; do
    go build -o "$scratch/bin/$name" "./scripts/$name"
  done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
)
"$scratch/bin/benchrun" performance-profile > "$evidence/compile-profile.txt"
grep -Fxf "$evidence/rpc-profile.txt" "$evidence/compile-profile.txt" > /dev/null
for role in base head candidate; do
  command=compile-performance
  if [[ "$role" == base ]]; then command=compile-performance-baseline; fi
  "$scratch/bin/benchrun" "$command" "$scratch/source/$role" "$scratch/bin/$role" "$package" "$profile" > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  binary="$scratch/bin/$role/pkg-lux-rpc.test"
  symbols='[.]((BenchmarkRPCTraceLocal|BenchmarkRPC_Echo)([.]func[0-9]+)*|[(][*]Client[)][.](callRequest|exchangeRequest|releaseReply)|[(][*]poolConn[)][.]readResponse|[(][*]Pool[)][.]Put|decodeCallResponse|readClientFrame)$'
  go tool nm -size "$binary" | awk -v pattern="$symbols" '$4 ~ pattern' > "$evidence/symbols-$role.nm"
  for name in BenchmarkRPCTraceLocal BenchmarkRPC_Echo callRequest exchangeRequest; do
    grep -Eq "[.]$name$" "$evidence/symbols-$role.nm"
  done
  go tool objdump -s "$symbols" "$binary" > "$evidence/symbols-$role.objdump"
  test -s "$evidence/symbols-$role.objdump"
  checks='^Test(TraceLocalClientResponseAndReuse|ClientExchangeDeadlineFailuresDoNotRecycleConnections|ReusedRequestsKeepResponsesOwned)$'
  "$scratch/bin/benchrun" run-performance "$path" "$scratch/bin/$role" "$package" "$profile" \
    -test.run="$checks" -test.count=1 -test.cpu=2 -test.timeout=90s -test.v > "$evidence/tests-$role.txt" 2>&1
  for name in TraceLocalClientResponseAndReuse ClientExchangeDeadlineFailuresDoNotRecycleConnections ReusedRequestsKeepResponsesOwned; do
    grep -Eq "^--- PASS: Test$name " "$evidence/tests-$role.txt"
  done
done

pair() {
  local group="$1" sample="$2" comparison="$3" first="$4" left="$5" right="$6"
  if [[ "$first" == left ]]; then
    run_leg "$group" "$sample" "$comparison" left "$left" 01 "$package" "$profile" "$filter"
    run_leg "$group" "$sample" "$comparison" right "$right" 01 "$package" "$profile" "$filter"
  else
    run_leg "$group" "$sample" "$comparison" right "$right" 01 "$package" "$profile" "$filter"
    run_leg "$group" "$sample" "$comparison" left "$left" 01 "$package" "$profile" "$filter"
  fi
}

mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
printf 'comparison\tleft\tright\naa\tbase\tbase\nbh\tbase\thead\nhc\thead\tcandidate\nbc\tbase\tcandidate\n' > "$evidence/roles.tsv"
comparisons=(aa bh hc bc)
for group in primary confirmation; do
  offset=0
  if [[ "$group" == confirmation ]]; then offset=1; fi
  for iteration in {1..10}; do
    printf -v sample '%02d' "$iteration"
    for index in {0..3}; do
      comparison=${comparisons[$(( (index + iteration + offset) % 4 ))]}
      first=left
      if (( (iteration + offset) % 2 == 0 )); then first=right; fi
      case "$comparison" in
        aa) pair "$group" "$sample" aa "$first" base base ;;
        bh) pair "$group" "$sample" bh "$first" base head ;;
        hc) pair "$group" "$sample" hc "$first" head candidate ;;
        bc) pair "$group" "$sample" bc "$first" base candidate ;;
      esac
    done
  done
done
python3 "$driver/verify.py" samples "$evidence"
for role in base head candidate; do
  cp "$evidence/binaries-$role.txt" "$scratch/before-$role.txt"
  record_binaries "$role"
  cmp "$scratch/before-$role.txt" "$evidence/binaries-$role.txt"
done

printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
for comparison in "${comparisons[@]}"; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
  if [[ "$comparison" == bh || "$comparison" == bc ]]; then
    args=(--approved-costs "$scratch/source/base/scripts/benchgate/approved-costs.json" --base-sha "$base" "${args[@]}")
  fi
  status=0
  "$scratch/bin/benchgate" "${args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
  if (( status != 0 )); then
    grep -Eq '^performance gate rejected [0-9]+ regression[(]s[)]$' "$evidence/$comparison.benchgate.log"
  fi
  printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
done
cat "$evidence/verdicts.tsv"
