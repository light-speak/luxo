#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
source "$script_dir/diagnose-hosted-perf.sh"
helper="$script_dir/diagnose-pr71-static-jwt.py"
base=1117eb9ffcc0beef977abceec656a351b0988389
base_tree=97e7de78aafc023063c8ad6e44ecfaa3dab04d35
head=4177d556d135cff1cd27e42798c0b082cf09f9b3
tested=3baa5b629d86749c6f5dc38dd9e540e0d11c39e9
head_tree=175294b28abcbb21b68ba40fd6288ce63e2e1547
artifact=14932692737
formal_run=45413131379
digest=789c9c7220e6a1400461f35d3bae5b1e922e94a8a8c7cd7105604948bec0f9af
expected_cpu='AMD EPYC 9V45 96-Core Processor'
package=pkg/lux/auth
filter='^BenchmarkStaticJWT$/^(Identity|Sign|Verify)$/^(Before|After|LegacyHMAC)$'
evidence="$repo/.tmp/hosted-pr71-static-jwt"

matches_cpu() {
  local model
  model=$(LC_ALL=C lscpu | sed -n 's/^Model name:[[:space:]]*//p')
  [[ "$(uname -s):$(uname -m)" == Linux:x86_64 && "$model" == "$expected_cpu" ]]
}

inspect_binary() {
  local role="$1" variant="$2" bin="$3" record="$evidence/$1-$2"
  local binary="$bin/pkg-lux-auth.test" side="$role"
  local symbols='github\.com/light-speak/luxo/pkg/lux/auth\.(BenchmarkStaticJWT.*|benchmarkStaticJWTPhase.*|testConfig|testClaims.*|signWithSubject.*|Sign.*|Verify.*|verifyPurpose.*|loadConfigFromEnv|parseDuration.*|scale.*Duration.*|\(?\*?Claims\)?\.(ID|Int|Get))$'
  go tool nm -size "$binary" | SYMBOL_FILTER="$symbols" awk '$NF ~ ENVIRON["SYMBOL_FILTER"]' > "$record.nm"
  go tool objdump -s "$symbols" "$binary" > "$record.objdump"
  for symbol in BenchmarkStaticJWT benchmarkStaticJWTPhase signWithSubject; do
    grep -q "/auth\\.$symbol$" "$record.nm"
  done
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$bin" "$package" -test.list='.' > "$record.registry.txt"
  if [[ "$variant" == static-only ]]; then
    side=head
    test "$(cat "$record.registry.txt")" = BenchmarkStaticJWT
  else
    grep -qx TestSignAndVerify "$record.registry.txt"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$bin" "$package" \
      -test.run='^Test(SignAndVerify|ClaimsIntegerTypeBoundary|SignRequiresFutureDeadlineForPositiveLifetime|TokensExpireAtExactBoundary)$' \
      -test.count=1 -test.timeout=90s > "$record.correctness.txt"
  fi
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$bin" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
    -test.timeout=90s > "$record.inventory.txt"
  awk '/^Benchmark/{print $1}' "$record.inventory.txt" | LC_ALL=C sort | cmp "$evidence/$side-names.txt" -
}

compile_role() {
  local role="$1" command=compile-performance bin="$scratch/bin/$1"
  if [[ "$role" == base ]]; then command=compile-performance-baseline; fi
  "$scratch/bin/benchrun" "$command" "$scratch/source/$role" "$bin" "$package" default
  record_binaries "$role"
  inspect_binary "$role" default "$bin"
  GOFLAGS="-overlay=$scratch/$role-overlay.json" "$scratch/bin/benchrun" compile \
    "$scratch/source/$role" "$bin-static-only" "$package"
  record_binaries "$role-static-only"
  inspect_binary "$role" static-only "$bin-static-only"
}

gate_sample() {
  local stem="$1" phase="$2"
  python3 "$helper" gate-rows "$stem" "$phase"
  "$scratch/bin/benchplan" --verify "$stem.$phase.gate.json" "$stem.$phase.gate.left.txt" "$stem.$phase.gate.right.txt"
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.$phase.gate.left.txt" head="$stem.$phase.gate.right.txt" \
    > "$stem.$phase.gate.csv" 2> "$stem.$phase.warnings.txt"
}

run_gate() {
  local stem="$1" comparison="$2"
  local -a policy=() args
  gate_sample "$stem" primary
  "$scratch/bin/benchgate" --confirmation-targets "$stem.primary.gate.csv" > "$stem.targets.tsv"
  args=("$stem.primary.gate.csv")
  if [[ -s "$stem.targets.tsv" ]]; then
    gate_sample "$stem" confirmation
    args=(--targeted-confirmation "$stem.primary.gate.csv" "$stem.confirmation.gate.csv")
  fi
  if [[ "$comparison" == ab ]]; then
    policy=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base")
  fi
  verdict=0
  "$scratch/bin/benchgate" "${policy[@]}" "${args[@]}" > "$stem.benchgate.log" 2>&1 || verdict=$?
  if (( verdict != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$stem.benchgate.log"
  fi
}

analyze_profile() {
  local label="$1" case_id="$2" comparison phase side stem verdict
  local out="$evidence/$label"
  printf 'comparison\tgate_exit\n' > "$out/verdicts.tsv"
  for comparison in aa ab; do
    stem="$out/$comparison"
    for phase in primary confirmation; do
      for side in left right; do
        cat "$evidence/raw/$phase".*."$comparison.$case_id.$side.txt" > "$scratch/combined.txt"
        "$scratch/bin/benchplan" --normalize "$scratch/combined.txt" "$stem.$phase.$side.txt"
      done
      "$scratch/bin/benchplan" --verify "$stem.expected.json" "$stem.$phase.left.txt" "$stem.$phase.right.txt"
    done
    run_gate "$stem" "$comparison"
    printf '%s\t%s\n' "$comparison" "$verdict" >> "$out/verdicts.tsv"
  done
  cat "$out/verdicts.tsv"
}

mode="${1:-run}"
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
case "$mode" in
  --probe)
    mkdir "$evidence"
    LC_ALL=C lscpu > "$evidence/runner.txt"
    match=false
    if matches_cpu; then match=true; fi
    printf 'match=%s\n' "$match" | tee -a "$evidence/runner.txt" >> "$GITHUB_OUTPUT"
    exit 0
    ;;
  --fetch)
    matches_cpu
    git -C "$repo" fetch --no-tags origin "$base" "$head" "$tested"
    endpoint="repos/light-speak/luxo/actions/artifacts/$artifact"
    gh api "$endpoint" > "$evidence/artifact-metadata.json"
    jq -e --arg run "$formal_run" --arg head "$head" --arg digest "sha256:$digest" \
      '(.workflow_run.id | tostring) == $run and .workflow_run.head_sha == $head and .digest == $digest and .expired == false' \
      "$evidence/artifact-metadata.json"
    gh api "$endpoint/zip" > "$evidence/official.zip"
    exit 0
    ;;
  --check-inputs) test "$#" = 2;;
  run) matches_cpu;;
  *) exit 2;;
esac
scratch=$(mktemp -d "$repo/.tmp/pr71-static-jwt.XXXXXXXX")
trap '[[ "$scratch" == "$repo"/.tmp/pr71-static-jwt.* && ! -L "$scratch" ]] && rm -rf -- "$scratch"' EXIT
official="$evidence/official.zip"
source_head="$tested"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = "$base_tree"
test "$(git -C "$repo" rev-parse "$head^{tree}")" = "$head_tree"
git -C "$repo" merge-base --is-ancestor "$base" "$head"
git -C "$repo" diff --exit-code "$base" "$head" -- pkg/lux/auth/claims.go scripts/benchplan scripts/benchgate scripts/benchrun
if [[ "$mode" == --check-inputs ]]; then
  evidence="$scratch/evidence"
  mkdir "$evidence"
  official="$2"
  source_head="$head"
else
  test "$(git -C "$repo" rev-list --parents -n 1 "$tested")" = "$tested $base $head"
  test "$(git -C "$repo" rev-parse "$tested^{tree}")" = "$head_tree"
fi
printf 'base=%s\nbase_tree=%s\nhead=%s\ntested=%s\ntested_tree=%s\nformal_run=%s/1\nformal_shard=01\nformal_artifact=%s\n' \
  "$base" "$base_tree" "$head" "$tested" "$head_tree" "$formal_run" "$artifact" > "$evidence/revisions.txt"
for role in base head; do
  revision="$base"
  if [[ "$role" == head ]]; then revision="$source_head"; fi
  mkdir -p "$scratch/source/$role"
  paths=()
  if [[ "$mode" == --check-inputs ]]; then paths=("$package"); fi
  git -C "$repo" archive "$revision" "${paths[@]}" | tar -xf - -C "$scratch/source/$role"
done
python3 "$helper" prepare "$evidence" "$scratch" "$official" "$base" "$tested" "$filter"
if [[ "$mode" == --check-inputs ]]; then
  cat "$evidence/revisions.txt" "$evidence/cases.tsv" "$evidence/setup-digests.json"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/bin"
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
for role in base head; do
  mkdir "$scratch/source/$role/.tmp"
  sh "$scratch/source/$role/scripts/setup-modules.sh" "$scratch/source/$role"
  cp "$scratch/source/$role/go.work" "$evidence/$role.go.work.txt"
done
(
  cd "$scratch/source/head"
  for tool in benchrun benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/tools.log" 2>&1
"$scratch/bin/benchrun" performance-profile > "$evidence/compile-profile.txt"
cmp "$evidence/formal-benchmark-compile-profile.txt" "$evidence/compile-profile.txt"
for role in base head; do compile_role "$role" > "$evidence/$role-compile.log" 2>&1; done
sample_groups
python3 "$helper" verify-raw "$evidence"
analyze_profile ordinary 01
analyze_profile isolated 02

for variant in ordinary isolated; do
  grep -qx $'aa\t0' "$evidence/$variant/verdicts.tsv" || { printf '%s A/A control was rejected\n' "$variant" >&2; exit 1; }
done
if ! grep -qx 'performance gate rejected 1 regression(s)' "$evidence/ordinary/ab.benchgate.log" ||
   ! grep -qF '/StaticJWT/Identity/After-2 sec/op' "$evidence/ordinary/ab.benchgate.log"; then
  printf '%s\n' 'Ordinary A/H did not reproduce exactly the formal Identity/After timing failure' >&2
  exit 1
fi
grep -qx $'ab\t0' "$evidence/isolated/verdicts.tsv" ||
  { printf '%s\n' 'Isolated A/H was rejected; inspect the retained auth and timed symbols' >&2; exit 1; }
