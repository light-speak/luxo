#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

if (( $# != 6 )); then
  printf '%s\n' 'usage: diagnose-pr74-gateway-profile-smoke.sh BASE HEAD TOOLS TOOLS_TREE PRODUCT_TREE COMBINED_TREE' >&2
  exit 2
fi
for revision in "$@"; do
  [[ "$revision" =~ ^[0-9a-f]{40}$ ]]
done
base="$1"
head="$2"
tools="$3"
tools_tree="$4"
product_tree="$5"
combined_tree="$6"
repo=$(git rev-parse --show-toplevel)
cd "$repo"
package=pkg/lux/luvia
fixture_sha=ae790127f4acd6df4fe2388d239b2657c999add2e742584284c411bef514e365
mkdir -p .tmp
test ! -L .tmp
evidence="$repo/.tmp/pr74-gateway-profile-smoke"
mkdir "$evidence"
scratch=$(mktemp -d "$repo/.tmp/pr74-gateway-profile-smoke.XXXXXXXX")
trap '[[ "$scratch" == "$repo"/.tmp/pr74-gateway-profile-smoke.* && ! -L "$scratch" ]] && rm -rf -- "$scratch"' EXIT
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local GOAMD64=v1 CGO_ENABLED=1
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/trace-bin"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
uname -a > "$evidence/host.txt"
lscpu >> "$evidence/host.txt"
go version >> "$evidence/host.txt"

git fetch --no-tags origin "$base" "$head" "$tools"
git merge-base --is-ancestor "$base" "$tools"
test "$(git rev-parse "$tools^{tree}")" = "$tools_tree"
test "$(git merge-tree --write-tree "$base" "$head")" = "$product_tree"
test "$(git merge-tree --write-tree "$tools" "$head")" = "$combined_tree"
git diff --exit-code "$base" "$tools" -- pkg cmd go.mod go.sum
git diff --exit-code "$product_tree" "$combined_tree" -- pkg cmd go.mod go.sum
git diff --exit-code "$tools" "$combined_tree" -- scripts/benchrun scripts/benchplan internal/benchlayout
{
  printf 'base=%s\nhead=%s\nproduct_tree=%s\ntools=%s\ntools_tree=%s\ncombined_tree=%s\n' \
    "$base" "$head" "$product_tree" "$tools" "$tools_tree" "$combined_tree"
  printf '%s\n' 'prior_timing_run=45427188380/2' 'prior_timing_artifact=14938031274' \
    'prior_timing_sha256=78f595ad7da1dcb4079a753e98f8c29aa7d7a1f3ddd21572fd1e8cdb746da2e4'
} > "$evidence/revisions.txt"

for role in base head; do
  revision="$base"
  if [[ "$role" == head ]]; then revision="$combined_tree"; fi
  dir="$scratch/$role"
  mkdir -p "$dir/.tmp"
  git archive "$revision" | tar -xf - -C "$dir"
  sh "$dir/scripts/setup-modules.sh" "$dir"
done
(
  cd "$scratch/head"
  for tool in benchrun benchplan; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
)
benchrun="$scratch/bin/benchrun"
benchplan="$scratch/bin/benchplan"
"$benchrun" performance-profile > "$evidence/compile-profile.txt"
"$benchrun" isolation-profile "$package" > "$evidence/profiles.tsv"
printf 'luvia-primary-trace\t^BenchmarkPrimaryKeys$|^BenchmarkTraceEncodingComparison$\nluvia-gateway-identity\t^BenchmarkGatewayIdentity$\n' \
  | cmp "$evidence/profiles.tsv" -

cat > "$scratch/trace-bin/go" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
overlay=
for arg in "$@"; do
  if [[ "$arg" == -overlay=* ]]; then overlay=${arg#-overlay=}; fi
done
test -f "$overlay"
fixture=$(jq -er '.Replace | to_entries[] | select(.key|endswith("/middleware_test.go")) | .value' "$overlay")
test -f "$fixture"
cp "$fixture" "$PROJECTED_FIXTURE"
exec "$REAL_GO" "$@"
SCRIPT
chmod +x "$scratch/trace-bin/go"
real_go=$(command -v go)

discover() {
  local role="$1" profile="$2" filter="$3" key="$evidence/$1/$2"
  "$benchrun" run-performance "$scratch/$role" "$scratch/bin/$role" "$package" "$profile" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
    -test.timeout=180s > "$key.raw.log"
  "$benchplan" --normalize "$key.raw.log" "$key.log"
  awk '/^Benchmark/ { if ($2 != 1) exit 1; print $1 }' "$key.log" | sort > "$key.names"
}

check_ordinary_correctness() {
  local role="$1" bin="$2" dir="$3" filter test_name
  local -a tests=(TestNewGatewayConfiguresStreamIdentityExtraction TestInternalRequestContextPreservesExactIdentity
    TestAuthMiddlewareBearerCaseInsensitive TestAuthMiddlewareAcceptsWebSocketQueryToken TestAuthMiddlewareRejectsHTTPQueryToken)
  if [[ "$role" == head ]]; then
    tests+=(TestExtractBearerTokenHeaderValues TestServeRejectsScaffoldJWTSecret)
  fi
  printf '%s\n' "${tests[@]}" > "$dir/required-tests.txt"
  filter=$(IFS='|'; printf '^(%s)$' "${tests[*]}")
  "$benchrun" run "$scratch/$role" "$bin" "$package" -test.run="$filter" -test.v -test.count=1 \
    -test.timeout=90s > "$dir/auth-correctness.log"
  for test_name in "${tests[@]}"; do
    grep -q "^--- PASS: $test_name " "$dir/auth-correctness.log"
  done
}

compile_isolated() {
  local role="$1" command="$2" bin="$3" dir="$4" profile="$5" filter="$6"
  if [[ "$profile" == luvia-gateway-identity ]]; then
    PROJECTED_FIXTURE="$dir/gateway-identity.go.txt" REAL_GO="$real_go" PATH="$scratch/trace-bin:$PATH" \
      "$benchrun" "$command" "$scratch/$role" "$bin" "$package" "$profile" > "$dir/$profile.compile.log" 2>&1
    printf '%s  %s\n' "$fixture_sha" "$dir/gateway-identity.go.txt" | sha256sum -c - > "$dir/gateway-identity.sha256.txt"
    "$benchrun" run-performance "$scratch/$role" "$bin" "$package" "$profile" -test.list='.' > "$dir/$profile.roots"
    test "$(cat "$dir/$profile.roots")" = BenchmarkGatewayIdentity
  else
    "$benchrun" "$command" "$scratch/$role" "$bin" "$package" "$profile" > "$dir/$profile.compile.log" 2>&1
    "$benchrun" run-performance "$scratch/$role" "$bin" "$package" "$profile" -test.list='^Benchmark' \
      > "$dir/$profile.roots"
    grep -qx BenchmarkPrimaryKeys "$dir/$profile.roots"
    grep -qx BenchmarkTraceEncodingComparison "$dir/$profile.roots"
    if grep -qx BenchmarkGatewayIdentity "$dir/$profile.roots"; then
      printf '%s\n' 'GatewayIdentity appeared in the primary/trace binary' >&2
      return 1
    fi
  fi
  discover "$role" "$profile" "$filter"
  "$benchplan" --verify-profile-inventory "$package" "$profile" "$dir/default.log" "$dir/$profile.log"
}

for role in base head; do
  dir="$evidence/$role"
  bin="$scratch/bin/$role"
  mkdir "$dir"
  command=compile-performance
  if [[ "$role" == base ]]; then command=compile-performance-baseline; fi
  "$benchrun" "$command" "$scratch/$role" "$bin" "$package" default > "$dir/default.compile.log" 2>&1
  discover "$role" default .
  check_ordinary_correctness "$role" "$bin" "$dir"
  while IFS=$'\t' read -r profile filter; do
    compile_isolated "$role" "$command" "$bin" "$dir" "$profile" "$filter"
  done < "$evidence/profiles.tsv"
  test "$(wc -l < "$dir/luvia-gateway-identity.names")" -eq 6
  test "$(grep -c '^BenchmarkPrimaryKeys/' "$dir/luvia-primary-trace.names")" -eq 20
  test "$(grep -c '^BenchmarkTraceEncodingComparison/' "$dir/luvia-primary-trace.names")" -eq 6
  comm -12 "$dir/luvia-gateway-identity.names" "$dir/luvia-primary-trace.names" > "$dir/profile-intersection.names"
  test ! -s "$dir/profile-intersection.names"
  cat "$dir/luvia-gateway-identity.names" "$dir/luvia-primary-trace.names" | sort > "$dir/profile-union.names"
  grep -E '^Benchmark(GatewayIdentity|PrimaryKeys|TraceEncodingComparison)/' "$dir/default.names" \
    | cmp "$dir/profile-union.names" -
  (cd "$bin" && sha256sum ./*.test && go version -m ./*.test) > "$dir/binaries.txt"
  go tool nm "$bin/pkg-lux-luvia-luvia-gateway-identity.test" \
    | awk '/auth[.](LoadConfig|loadConfigFromEnv)|luvia[.]internalRequestContext/' > "$dir/auth-symbols.nm"
  grep -q '/auth\.loadConfigFromEnv$' "$dir/auth-symbols.nm"
done
"$benchplan" --verify-equal-inventories "$evidence/base/default.log" "$evidence/head/default.log"
cmp "$evidence/base/gateway-identity.go.txt" "$evidence/head/gateway-identity.go.txt"
