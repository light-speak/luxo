#!/usr/bin/env bash
set -euo pipefail

attribution_asset() {
  local revision="$1" path="$2" destination="$3" sum="$4"
  git -C "$repo" show "$revision:$path" > "$destination"
  printf '%s  %s\n' "$sum" "$destination" | sha256sum -c -
}

attribution_compile() {
  local role="$1" variant="$2"
  shift 2
  local source="$scratch/source/$role" output="$scratch/bin/$role"
  if [[ "$variant" != default ]]; then output+="-$variant"; fi
  local package
  for package in "$@"; do
    if [[ "$variant" == default ]]; then
      "$scratch/bin/benchrun" compile "$source" "$output" "$package"
    else
      GOFLAGS="-overlay=$evidence/$role-$variant.json" "$scratch/bin/benchrun" compile "$source" "$output" "$package"
    fi
  done
}

attribution_layout() {
  local binary="$1" name="$2" must="${3:-}" removed="${4:-}"
  local pattern='[.](Benchmark(RequestHead|RPCHandlerCompletion|AuthMiddlewareIsolation|ObservationExport|TraceEncodingComparison|RPC_Echo).*|parseRequestHead|benchmarkRPCHandlerCompletion|processCallContext|ConfigureHTTPServer([.]func1)?|AuthMiddleware([.]func[0-9]+)?|authMiddlewareBeforeIsolation([.]func[0-9]+)?|flushTraces|encodeTraces|traceJSONCapacity|appendTraceRecord|startGatewayServerWithBodyLimits|callRequest|exchangeRequest|readResponse|releaseReply)$'
  local all="$scratch/$name.nm"
  go tool nm -size "$binary" > "$all"
  if [[ -n "$must" ]]; then grep -Eq "$must" "$all"; fi
  if [[ -n "$removed" ]] && grep -Eq "$removed" "$all"; then
    printf 'Excluded test import is still linked in %s\n' "$name" >&2
    return 1
  fi
  awk -v pattern="$pattern" '$4 ~ pattern' "$all" > "$evidence/$name.nm"
  test -s "$evidence/$name.nm"
  go tool objdump -s "$pattern" "$binary" > "$evidence/$name.objdump"
}

attribution_build_variants() {
  local role="$1" source="$scratch/source/$1"
  if [[ "$suite" == pr79 ]]; then
    compile_diagnostic_fixture "$role" tls pkg/lux/luvia gateway_tls_diagnostic_test.go gateway-tls.go.txt
    if [[ "$role" == base ]]; then
      ln -s "$scratch/bin/base" "$scratch/bin/base-product"
    else
      python3 "$processor" overlay "$suite" product "$role" "$source" "$scratch/source/base" "$evidence"
      attribution_compile "$role" product pkg/lux/h2transport pkg/lux/luvia
    fi
    record_binaries "$role-product"
    local package
    for package in h2transport luvia; do
      attribution_layout "$scratch/bin/$role-product/pkg-lux-$package.test" "$role-product-$package" 'h2transport.*ConfigureHTTPServer[.]func1'
    done
    attribution_layout "$scratch/bin/$role/pkg-lux-rpc.test" "$role-full-rpc" 'h2transport.*ConfigureHTTPServer[.]func1'
    python3 "$processor" overlay "$suite" lean "$role" "$source" "$scratch/source/base" "$evidence"
    attribution_compile "$role" lean pkg/lux/rpc
    record_binaries "$role-lean"
    attribution_layout "$scratch/bin/$role-lean/pkg-lux-rpc.test" "$role-lean-rpc" 'rpc[.]BenchmarkRPCHandlerCompletion' 'pkg/lux/h2transport[.]'
  else
    attribution_layout "$scratch/bin/$role/pkg-lux-luvia.test" "$role-full-luvia" 'rpc.*exchangeRequest'
    python3 "$processor" overlay "$suite" lean "$role" "$source" "$scratch/source/base" "$evidence"
    attribution_compile "$role" lean pkg/lux/luvia
    record_binaries "$role-lean"
    attribution_layout "$scratch/bin/$role-lean/pkg-lux-luvia.test" "$role-lean-luvia" 'luvia[.]BenchmarkTraceEncodingComparison' 'pkg/lux/rpc[.]'
  fi
}

attribution_test() {
  local package="$1" name="$2"
  "$scratch/bin/benchrun" run "$scratch/source/head" "$scratch/bin/head" "$package" \
    -test.run="^${name}$" -test.count=1 -test.v -test.timeout=90s > "$evidence/test-$name.log" 2>&1
  grep -q "^--- PASS: $name " "$evidence/test-$name.log"
}

attribution_inventory() {
  local role case_id package variant name filter binaries
  for role in base head; do
    while IFS=$'\t' read -r case_id package variant name filter; do
      binaries="$scratch/bin/$role"
      if [[ "$variant" != default ]]; then binaries+="-$variant"; fi
      "$scratch/bin/benchrun" run "$scratch/source/$role" "$binaries" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        > "$evidence/inventory-$role-$case_id.txt"
    done < "$evidence/cases.tsv"
  done
}

attribution_main() {
  suite="${1:?Usage: diagnose-pr79-pr81-attribution.sh pr79|pr81 [--plan-only]}"
  local plan_only=false
  if [[ $# -eq 2 && "$2" == --plan-only ]]; then plan_only=true
  elif [[ $# -ne 1 ]]; then return 2
  fi
  local -a packages
  case "$suite" in
    pr79) head=e04c586e491b8b215308d10f7228636e38547f5b; packages=(pkg/lux/h2transport pkg/lux/luvia pkg/lux/rpc) ;;
    pr81) head=33ce49477379143a8e3bfcd34a3628ec6a9bc626; packages=(pkg/lux/luvia pkg/lux/rpc) ;;
    *) return 2 ;;
  esac
  local original=4fa61ac9c020db7d6001a4734c887346fc17c765
  local sampler=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
  local launch directory role revision source context file status=0
  repo=$(git rev-parse --show-toplevel)
  launch=$(pwd -P)
  directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
  processor="$directory/diagnose-pr79-pr81-attribution.py"
  mkdir -p "$launch/.tmp"
  test ! -L "$launch/.tmp"
  evidence="$launch/.tmp/hosted-pr79-pr81-attribution-$suite"
  mkdir "$evidence"
  attribution_asset "$original" scripts/diagnose-pr79-pr81-scope.json "$evidence/original-scope.json" \
    deef5a560b4e886001b0889d6cbf6c6af0a61803728513734b80faf02df4b851
  attribution_asset "$sampler" scripts/diagnose-hosted-perf.sh "$evidence/sampler.sh" \
    9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
  source "$evidence/sampler.sh"
  python3 "$processor" plan "$evidence" "$suite"
  if "$plan_only"; then return 0; fi

  export GOTOOLCHAIN=local GOWORK=off
  [[ "$(go env GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 GOVERSION)" == $'linux\namd64\nlinux\namd64\nv1\ngo1.27.1' ]]
  [[ -z "$(go env GOFLAGS)" ]]
  scratch=$(mktemp -d "$launch/.tmp/.hosted-transport-attribution.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  export TMPDIR="$scratch/tmp"
  mkdir "$TMPDIR" "$scratch/bin"
  collect_host > "$evidence/environment.txt"
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
Prior diagnostic 45235944103 confirmed eight PR 79 timing leaves and one PR 81 leaf.
Full retains every original rejected benchmark and the real TLS or RPC path controls.
Product keeps the PR 79 production files with base versions of the two changed test files.
Lean removes only test-level transport imports for the relevant, unchanged benchmark.
Full and attribution arms use the same per-case benchmark filter and one native runner.
Samples interleave contexts, with two groups of ten A/A and A/B alternating pairs per context.
Default linker, Go 1.27.1, 200ms, benchmem, CPU=2, benchstat alpha .01, original thresholds.
A clean A/A and a reproduced full A/B failure are required for a layout inference.
This diagnostic preserves independent threshold outcomes. The original PR gates remain definitive.
PROTOCOL
  for role in base head; do
    revision="$base"
    if [[ "$role" == head ]]; then revision="$head"; fi
    source="$scratch/source/$role"
    mkdir -p "$source"
    git -C "$repo" archive "$revision" | tar -xf - -C "$source"
    git -C "$repo" show -s --format="$role %H %T" "$revision" >> "$evidence/revisions.txt"
    python3 "$processor" sources "$evidence" "$role" "$source"
    sh "$source/scripts/setup-modules.sh" "$source"
    cp "$source/go.work" "$evidence/go-$role.work.txt"
  done
  cmp "$evidence/unchanged-sources-base.sha256" "$evidence/unchanged-sources-head.sha256"
  if [[ "$suite" == pr79 ]]; then
    attribution_asset "$original" scripts/diagnose-pr79-pr81-tls.go.txt "$evidence/gateway-tls.go.txt" \
      b04a8c9907db0afd69912a20c10c4abd0b5f9e9a73f301bd830d602ffcfba954
  fi
  (
    cd "$scratch/source/base"
    for tool in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$tool" "./scripts/$tool"
    done
  ) > "$evidence/tools.log" 2>&1
  for role in base head; do
    attribution_compile "$role" default "${packages[@]}" > "$evidence/compile-$role.log" 2>&1
    record_binaries "$role"
    attribution_build_variants "$role" > "$evidence/compile-$role-attribution.log" 2>&1
    if [[ "$suite" == pr79 ]]; then
      attribution_layout "$scratch/bin/$role/pkg-lux-h2transport.test" "$role-full-h2transport"
      attribution_layout "$scratch/bin/$role/pkg-lux-luvia.test" "$role-full-luvia"
    else
      attribution_layout "$scratch/bin/$role/pkg-lux-rpc.test" "$role-full-rpc"
    fi
  done
  if [[ "$suite" == pr79 ]]; then
    attribution_test pkg/lux/h2transport TestHTTPServerProtocolDispatch
    attribution_test pkg/lux/luvia TestGatewayRejectsIncompleteTLSConfiguration
  else
    attribution_test pkg/lux/rpc TestClientResponseWindowMatchesDirectRead
  fi
  attribution_inventory
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  python3 "$processor" split "$evidence" "$scratch/contexts"
  local report="$evidence"
  mkdir "$report/results"
  for context in "$scratch/contexts/"*; do
    evidence="$context"
    analyze_groups
    mkdir "$report/results/${context##*/}"
    for file in "$context/"*; do
      if [[ -f "$file" ]]; then cp "$file" "$report/results/${context##*/}/"; fi
    done
    if ! grep -Fxq $'aa\t0' "$context/verdicts.tsv"; then status=1; fi
  done
  evidence="$report"
  return "$status"
}

attribution_main "$@"
