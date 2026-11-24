#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
driver="$script_dir/diagnose-pr74-authorization-lookup.py"
mode="${1:-run}"
[[ $# -le 1 && ( "$mode" == run || "$mode" == --plan-only ) ]]
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr74-authorization-lookup.XXXXXXXX")
evidence="$repo/.tmp/hosted-pr74-authorization-lookup"
if [[ "$mode" == --plan-only ]]; then evidence="$scratch/evidence"; fi
evidence_created=false

cleanup_header() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr74-authorization-lookup.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$evidence_created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$evidence/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_header EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
evidence_created=true

record_header_layout() {
  local role="$1" binary pattern function
  binary="$scratch/bin/$role/pkg-lux-luvia.test"
  pattern='pkg/lux/(luvia|api)\..*(AuthMiddleware|extractBearerToken|IsWebSocketUpgrade|AuthUpgradeHeaders|benchmarkAuthUpgrade)'
  go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$role.nm"
  go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$role.objdump"
  grep -Eq '^TEXT .*benchmarkAuthUpgrade\(' "$evidence/symbols-$role.objdump"
  grep -Eq '^TEXT .*AuthMiddleware.*func' "$evidence/symbols-$role.objdump"
  for function in extractBearerToken IsWebSocketUpgrade; do
    # Retain the timed closure even when a helper is inlined into it.
    if grep -q "$function" "$evidence/symbols-$role.nm"; then
      grep -Eq "^TEXT .*$function[(]" "$evidence/symbols-$role.objdump"
      printf '%s standalone\n' "$function"
    else
      printf '%s no standalone symbol; inspect timed caller and source line records\n' "$function"
    fi
  done > "$evidence/layout-$role.txt"
}

compile_header() {
  local role source tool test_filter
  mkdir "$scratch/bin"
  (
    cd "$scratch/source/base"
    for tool in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$tool" "./scripts/$tool"
    done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  ) > "$evidence/tools.log" 2>&1
  for role in base control product; do
    source="$scratch/source/$role"
    sh "$source/scripts/setup-modules.sh" "$source" > "$evidence/workspace-$role.log" 2>&1
    cp "$source/go.work" "$evidence/go-$role.work.txt"
    "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$role" "$package" > "$evidence/compile-$role.log" 2>&1
    record_binaries "$role"
    record_header_layout "$role"
    test_filter='^(TestAuthMiddleware(ValidToken|AcceptsWebSocketQueryToken|RejectsHTTPQueryToken|NoToken|BearerCaseInsensitive)|TestExtractBearerToken(Empty|Short|HeaderValues))$'
    "$scratch/bin/benchrun" run "$source" "$scratch/bin/$role" "$package" \
      -test.run="$test_filter" -test.v -test.timeout=90s > "$evidence/test-$role.txt" 2>&1
    grep -q '^--- PASS: TestAuthMiddlewareAcceptsWebSocketQueryToken ' "$evidence/test-$role.txt"
    if [[ "$role" != base ]]; then
      grep -q '^--- PASS: TestExtractBearerTokenHeaderValues ' "$evidence/test-$role.txt"
    fi
    "$scratch/bin/benchrun" run "$source" "$scratch/bin/$role" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      -test.timeout=90s > "$evidence/inventory-$role.txt" 2>> "$evidence/benchmark-stderr.txt"
  done
}

sample_header() {
  local group pair comparison case_id side role variant raw
  mkdir "$evidence/raw"
  head -n 1 "$evidence/planned-order.tsv" > "$evidence/order.tsv"
  while IFS=$'\t' read -r group pair comparison case_id side role variant raw; do
    run_leg "$group" "$pair" "$comparison" "$side" "$role" "$case_id" "$package" "$variant" "$filter"
  done < <(tail -n +2 "$evidence/planned-order.tsv")
  cmp "$evidence/planned-order.tsv" "$evidence/order.tsv"
}

analyze_header() {
  local comparison status exit_status=0
  printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for comparison in aa ap hh hp; do
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    status=0
    "$scratch/bin/benchgate" "$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv" \
      > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
    printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
    if (( status != 0 )); then
      grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log" || return "$status"
      exit_status=1
    fi
  done
  cat "$evidence/verdicts.tsv"
  return "$exit_status"
}

package=pkg/lux/luvia
filter=$(python3 "$driver" plan "$evidence")
python3 "$driver" prepare "$repo" "$scratch" "$evidence"
source "$script_dir/diagnose-hosted-perf.sh"
if [[ "$mode" == --plan-only ]]; then
  run_leg() {
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\traw/%s.%s.%s.%s.%s.txt\n' \
      "$1" "$2" "$3" "$6" "$4" "$5" "$8" "$1" "$2" "$3" "$6" "$4" >> "$evidence/order.tsv"
  }
  sample_header
  cat "$evidence/sources.json" "$evidence/expected.json"
  printf 'Verified exact schedule: %s processes\n' "$(( $(wc -l < "$evidence/order.tsv") - 1 ))"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir "$TMPDIR"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
cp "$driver" "${BASH_SOURCE[0]}" "$evidence/"
cat > "$evidence/protocol.txt" <<'PROTOCOL'
A is exact public a75a0ac. P is exact JWT-only repair plus authorization lookup.
H matches P byte for byte except the production middleware file uses the JWT-only extractor.
A/A and H/H reuse one executable on both sides. A/P observes the actual release delta.
H/P isolates the extractor with the same added regressions and benchmark fixture on both sides.
Default link, native linux/amd64 v1, Go 1.27.1, six current leaves per process, 200ms, CPU2, benchmem.
Two groups of ten alternating pairs per comparison. Comparison order rotates by pair.
Alpha .01, original significance and timing/bytes/alloc thresholds, two-group confirmation, no exemptions.
A failed A/A makes A/P inconclusive. A failed H/H makes H/P inconclusive. This diagnostic preserves and does not replace the failed official PR 74 results.
PROTOCOL
compile_header
python3 "$driver" sources "$scratch/source"
date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
sample_header
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
python3 "$driver" samples "$evidence" > "$evidence/inventory-cpu.txt"
python3 "$driver" sources "$scratch/source"
analyze_header
