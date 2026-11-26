#!/usr/bin/env bash
set -euo pipefail

[[ "$#" == 1 || ( "$#" == 2 && "$2" == --plan-only ) ]]
formal="$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")"
plan="${2:-}"
repo=$(git rev-parse --show-toplevel)
driver=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
main_revision=a01b630181c07c53f8bd346d5ac489e3404584b3
main_tree=5c5579c596f1928fb9fb473ec2f67cd622046366
pr_head=12e7743153a02a5c1cfc997e84ff5b6993a267bd
pr_tree=975e2465698065c8a56af806ae3baabc13155440
sampler_revision=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
sampler_digest=9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
patch_digest=198a892ae98b7a850fbc24b80c38381f9cf06fdf5201896ddf826fc5fdded7b7
package=pkg/lux/rpc
profile=default
cpu='AMD EPYC 9V74 80-Core Processor'
filter='^(BenchmarkRPCTraceLocal|BenchmarkRPC_Ping|BenchmarkRPC_Echo|BenchmarkRPCStructuredErrorRoundTrip)$'

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
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  [[ -z "$(go env GOFLAGS)" ]]
fi
git -C "$repo" show "${sampler_revision}:scripts/diagnose-hosted-perf.sh" > "$scratch/sampler.sh"
[[ "$(digest "$scratch/sampler.sh")" == "$sampler_digest" ]]
# shellcheck source=/dev/null
source "$scratch/sampler.sh"
base="$main_revision"
export LC_ALL=C GOWORK=off GOTOOLCHAIN=local
mkdir "$scratch/tmp"
export TMPDIR="$scratch/tmp"

python3 "$driver/verify.py" formal "$formal" "$evidence"
python3 - "$driver/unary-fast.patch.b64" "$scratch/pr.patch" <<'PYTHON'
import base64
import pathlib
import sys
source, target = map(pathlib.Path, sys.argv[1:])
target.write_bytes(base64.b64decode(b"".join(source.read_bytes().splitlines()), validate=True))
PYTHON
[[ "$(digest "$scratch/pr.patch")" == "$patch_digest" ]]
[[ "$(git -C "$repo" rev-parse "${base}^{tree}")" == "$main_tree" ]]
store="$scratch/git"
git -C "$repo" init --quiet --bare "$store"
git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$store/objects/info/alternates"
graft=(git --git-dir="$store")
"${graft[@]}" read-tree "$base"
"${graft[@]}" apply --cached --check --whitespace=error-all "$scratch/pr.patch"
"${graft[@]}" apply --cached --whitespace=error-all "$scratch/pr.patch"
[[ "$("${graft[@]}" write-tree)" == "$pr_tree" ]]
"${graft[@]}" diff --check "$main_tree" "$pr_tree"
cat > "$evidence/product-paths.txt" <<'PATHS'
pkg/lux/rpc/client.go
pkg/lux/rpc/connection_reader_test.go
pkg/lux/rpc/error.go
pkg/lux/rpc/frame_buffered_test.go
pkg/lux/rpc/pool_conn.go
pkg/lux/rpc/pool_lifecycle_test.go
pkg/lux/rpc/rpc_test.go
pkg/lux/rpc/text_write_test.go
PATHS
"${graft[@]}" diff --name-only "$main_tree" "$pr_tree" | diff -u "$evidence/product-paths.txt" -
"${graft[@]}" apply --cached --reverse --whitespace=error-all "$scratch/pr.patch"
[[ "$("${graft[@]}" write-tree)" == "$main_tree" ]]

version_tree() {
  case "$1" in
    base) printf '%s\n' "$main_tree" ;;
    head) printf '%s\n' "$pr_tree" ;;
    *) return 1 ;;
  esac
}

printf 'base %s %s\nreviewed_head %s\ncurrent_lease %s\nsampler %s %s\npatch %s\n' \
  "$base" "$main_tree" "$pr_head" "$pr_tree" "$sampler_revision" "$sampler_digest" "$patch_digest" > "$evidence/revisions.txt"
for role in base head; do
  tree=$(version_tree "$role")
  for path in "$package"/{trace_local_bench_test.go,error_data_bench_test.go,public_boundary_test.go,client.go,pool_conn.go,pool_lifecycle_test.go,rpc_test.go} \
    scripts/benchgate/approved-costs.json scripts/benchrun/main.go; do
    "${graft[@]}" show "$tree:$path" > "$scratch/source-file"
    printf '%s  %s\n' "$(digest "$scratch/source-file")" "$path"
  done > "$evidence/sources-$role.sha256"
  grep -Fxq 'bd60873a66ef965a610d9aecedb15921b7cd60d18889c3245dcacbd71719b8ce  pkg/lux/rpc/trace_local_bench_test.go' "$evidence/sources-$role.sha256"
done
for path in "$package"/{trace_local_bench_test.go,error_data_bench_test.go,public_boundary_test.go} scripts/benchgate/approved-costs.json scripts/benchrun/main.go; do
  [[ "$(grep -F "  $path" "$evidence/sources-base.sha256")" == "$(grep -F "  $path" "$evidence/sources-head.sha256")" ]]
done
# The test file also contains regressions. Bind only the unchanged TCP benchmark declarations.
for role in base head; do
  "${graft[@]}" show "$(version_tree "$role"):$package/rpc_test.go" > "$scratch/rpc-test.go"
  python3 "$driver/verify.py" tcp "$scratch/rpc-test.go" > "$evidence/tcp-$role.sha256"
done
cmp "$evidence/tcp-base.sha256" "$evidence/tcp-head.sha256"
[[ "$plan" != --plan-only ]] || exit 0

collect_host > "$evidence/environment.txt"
mkdir -p "$scratch/bin"
for role in base head; do
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
for role in base head; do
  source_dir="$scratch/source/$role"
  command=compile-performance
  if [[ "$role" == base ]]; then command=compile-performance-baseline; fi
  "$scratch/bin/benchrun" "$command" "$scratch/source/$role" "$scratch/bin/$role" "$package" "$profile" > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  binary="$scratch/bin/$role/pkg-lux-rpc.test"
  symbols='[.]((BenchmarkRPCTraceLocal|BenchmarkRPC_Ping|BenchmarkRPC_Echo|BenchmarkRPCStructuredErrorRoundTrip)([.]func[0-9]+)*|[(][*]Client[)][.](callRequest|exchangeRequest|releaseReply|CallWithMaskTargetContext|finishUnaryError)|[(][*]poolConn[)][.]readResponse|[(][*]Pool[)][.]Put|decodeCallResponse|decodeCallError|readClientFrame)$'
  go tool nm -size "$binary" | awk -v pattern="$symbols" '$4 ~ pattern' > "$evidence/symbols-$role.nm"
  for name in BenchmarkRPCTraceLocal BenchmarkRPC_Ping BenchmarkRPC_Echo BenchmarkRPCStructuredErrorRoundTrip exchangeRequest CallWithMaskTargetContext; do
    grep -Eq "[.]$name$" "$evidence/symbols-$role.nm"
  done
  go tool objdump -s "$symbols" "$binary" > "$evidence/symbols-$role.objdump"
  test -s "$evidence/symbols-$role.objdump"
  checks=(TraceLocalClientResponseAndReuse ClientExchangeDeadlineFailuresDoNotRecycleConnections ReusedRequestsKeepResponsesOwned)
  if [[ "$role" == head ]]; then
    checks+=(ClientClosesConnectionOnTrailingResponseBytes ClientRetiresLeaseAfterInvalidResponse ConnectionReaderKeepsInFlightPayloadOwned)
  fi
  selector="^Test($(IFS='|'; printf '%s' "${checks[*]}"))$"
  "$scratch/bin/benchrun" run-performance "$source_dir" "$scratch/bin/$role" "$package" "$profile" \
    -test.run="$selector" -test.count=1 -test.cpu=2 -test.timeout=90s -test.v > "$evidence/tests-$role.txt" 2>&1
  for name in "${checks[@]}"; do
    grep -Eq "^--- PASS: Test$name " "$evidence/tests-$role.txt"
  done
  "$scratch/bin/benchrun" run-performance "$source_dir" "$scratch/bin/$role" "$package" "$profile" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 \
    -test.cpu=2 -test.timeout=90s > "$evidence/inventory-$role.txt" 2>&1
  python3 "$driver/verify.py" inventory "$evidence/inventory-$role.txt"
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
printf 'comparison\tleft\tright\naa\tbase\tbase\nbb\thead\thead\nab\tbase\thead\n' > "$evidence/roles.tsv"
comparisons=(aa bb ab)
for group in primary confirmation; do
  offset=0
  if [[ "$group" == confirmation ]]; then offset=1; fi
  for iteration in {1..10}; do
    printf -v sample '%02d' "$iteration"
    for index in {0..2}; do
      comparison=${comparisons[$(( (index + iteration + offset) % 3 ))]}
      first=left
      if (( (iteration + offset) % 2 == 0 )); then first=right; fi
      case "$comparison" in
        aa) pair "$group" "$sample" aa "$first" base base ;;
        bb) pair "$group" "$sample" bb "$first" head head ;;
        ab) pair "$group" "$sample" ab "$first" base head ;;
      esac
    done
  done
done
python3 "$driver/verify.py" samples "$evidence"
for role in base head; do
  cp "$evidence/binaries-$role.txt" "$scratch/before-$role.txt"
  record_binaries "$role"
  cmp "$scratch/before-$role.txt" "$evidence/binaries-$role.txt"
done

printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
control_failed=0
comparison_status=0
for comparison in "${comparisons[@]}"; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  if [[ "$comparison" == ab ]]; then
    "$scratch/bin/benchgate" --approved-costs "$scratch/source/base/scripts/benchgate/approved-costs.json" \
      --base-sha "$base" "$evidence/ab.primary.csv" "$evidence/ab.confirmation.csv" \
      > "$evidence/ab.benchgate.log" 2>&1 || comparison_status=$?
    printf 'ab\t%s\n' "$comparison_status" >> "$evidence/verdicts.tsv"
    continue
  fi
  status=0
  "$scratch/bin/benchgate" "$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv" \
    > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
  printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
  if (( status != 0 )); then control_failed=1; fi
  for group in primary confirmation; do
    stem="$evidence/$comparison.$group"
    "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
      base="$stem.right.txt" head="$stem.left.txt" > "$stem.reverse.csv" 2> "$stem.reverse.warnings.txt"
  done
  status=0
  "$scratch/bin/benchgate" "$evidence/$comparison.primary.reverse.csv" "$evidence/$comparison.confirmation.reverse.csv" \
    > "$evidence/$comparison.reverse.benchgate.log" 2>&1 || status=$?
  printf '%s-reverse\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
  if (( status != 0 )); then control_failed=1; fi
done
cat "$evidence/verdicts.tsv"
if (( control_failed != 0 )); then exit 4; fi
exit "$comparison_status"
