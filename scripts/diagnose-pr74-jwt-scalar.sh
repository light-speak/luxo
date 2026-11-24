#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
driver="$script_dir/diagnose-pr74-jwt-scalar.py"
mode="${1:-run}"
[[ $# -le 1 && ( "$mode" == run || "$mode" == --plan-only ) ]]
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr74-jwt-scalar.XXXXXXXX")
evidence="$repo/.tmp/hosted-pr74-jwt-scalar"
if [[ "$mode" == --plan-only ]]; then
  evidence="$scratch/evidence"
fi
evidence_created=false

cleanup_pr74() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr74-jwt-scalar.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$evidence_created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$evidence/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_pr74 EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
evidence_created=true

config_policy() {
  case "$1" in
    jwt|combined) printf 'yes';;
    base|scalar) printf 'no';;
    *) return 1;;
  esac
}

compile_pr74() {
  local role package case_id variant _name filter _context directory source
  local -a packages
  while IFS= read -r package; do
    packages+=("$package")
  done < <(awk -F '\t' '$3 == "default" {print $2}' "$evidence/cases.tsv" | sort -u)
  for role in base jwt scalar combined; do
    source="$scratch/source/$role"
    sh "$source/scripts/setup-modules.sh" "$source" > "$evidence/workspace-$role.log" 2>&1
    cp "$source/go.work" "$evidence/go-$role.work.txt"
  done
  (
    cd "$scratch/source/base"
    for package in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$package" "./scripts/$package"
    done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  ) > "$evidence/tools.log" 2>&1

  for role in base jwt scalar combined; do
    source="$scratch/source/$role"
    for package in "${packages[@]}"; do
      "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$role" "$package"
      record_pr74_symbols "$role" "$package"
    done > "$evidence/compile-$role.log" 2>&1
    record_binaries "$role"
    compile_diagnostic_fixture "$role" json-int pkg/lux/jsonutil integer_diagnostic_test.go integer.go.txt
    compile_diagnostic_fixture "$role" config pkg/lux/auth pr74_config_diagnostic_test.go config.go.txt
    record_pr74_symbols "$role-json-int" pkg/lux/jsonutil
    record_pr74_symbols "$role-config" pkg/lux/auth
    record_pr74_tests "$role"
    while IFS=$'\t' read -r case_id package variant _name filter _context; do
      directory="$scratch/bin/$role"
      if [[ "$variant" != default ]]; then directory+="-$variant"; fi
      PR74_EXPECT_PLACEHOLDER_REJECTION="$(config_policy "$role")" \
        "$scratch/bin/benchrun" run "$source" "$directory" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        -test.timeout=90s > "$evidence/inventory-$role-$case_id.txt" 2>> "$evidence/benchmark-stderr.txt"
    done < "$evidence/cases.tsv"
  done
}

record_pr74_symbols() {
  local role="$1" package="$2" leaf pattern binary
  leaf="${package##*/}"
  binary="$scratch/bin/$role/pkg-lux-$leaf.test"
  pattern='pkg/lux/(api|auth|luvia|rpc|schema|jsonutil)[.].*(AuthUpgradeHeaders|ConfigBoundary|loadConfigFromEnv|ParseInt64|verifyFull|StaticJWT|LRPCBufferedColdRead|RawJSONArrayPool|SchemaDepthOutput|BinaryRequestIDBounds|RequestStaticJSONParams|StaticValueParams)'
  go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$role-$leaf.nm"
  go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$role-$leaf.objdump"
  test -s "$evidence/symbols-$role-$leaf.nm"
  grep -q '^TEXT ' "$evidence/symbols-$role-$leaf.objdump"
}

record_pr74_tests() {
  local role="$1" source="$scratch/source/$1" auth_filter luvia_filter
  auth_filter='TestLoadConfig|TestLoadConfigMissing|TestTokensExpireAtExactBoundary|TestPurposeEnvelopeRejectsMalformedValues'
  luvia_filter='^TestValidateJWTConfigurationAcceptsValidConfiguration$'
  if [[ "$(config_policy "$role")" == yes ]]; then
    auth_filter+='|TestLoadConfigRejectsScaffoldSecret'
    luvia_filter='^(TestValidateJWTConfigurationAcceptsValidConfiguration|TestServeRejectsScaffoldJWTSecret)$'
  fi
  "$scratch/bin/benchrun" run "$source" "$scratch/bin/$role" pkg/lux/auth \
    -test.run="^($auth_filter)$" -test.v -test.timeout=90s > "$evidence/test-$role-auth.txt" 2>&1
  "$scratch/bin/benchrun" run "$source" "$scratch/bin/$role" pkg/lux/luvia \
    -test.run="$luvia_filter" -test.v -test.timeout=90s > "$evidence/test-$role-luvia.txt" 2>&1
  "$scratch/bin/benchrun" run "$source" "$scratch/bin/$role-json-int" pkg/lux/jsonutil \
    -test.run='^(TestParseInt64|TestParseInt64OnlyTrimsJSONWhitespace|FuzzStaticJSONScalars)$' \
    -test.v -test.timeout=90s > "$evidence/test-$role-jsonutil.txt" 2>&1
  grep -q '^--- PASS: TestTokensExpireAtExactBoundary ' "$evidence/test-$role-auth.txt"
  grep -q '^--- PASS: TestPurposeEnvelopeRejectsMalformedValues ' "$evidence/test-$role-auth.txt"
  grep -q '^--- PASS: TestParseInt64 ' "$evidence/test-$role-jsonutil.txt"
  grep -q '^--- PASS: TestValidateJWTConfigurationAcceptsValidConfiguration ' "$evidence/test-$role-luvia.txt"
  if [[ "$(config_policy "$role")" == yes ]]; then
    grep -q '^--- PASS: TestLoadConfigRejectsScaffoldSecret ' "$evidence/test-$role-auth.txt"
    grep -q '^--- PASS: TestServeRejectsScaffoldJWTSecret ' "$evidence/test-$role-luvia.txt"
  fi
}

sample_pr74() {
  local group offset pair case_id package variant _name filter context index
  local comparison first role
  local -a comparisons=(aa aj as ac)
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    offset=0
    if [[ "$group" == confirmation ]]; then offset=1; fi
    for pair in {1..10}; do
      printf -v pair '%02d' "$pair"
      first=right
      if (( (10#$pair + offset) % 2 == 1 )); then first=left; fi
      while IFS=$'\t' read -r case_id package variant _name filter context; do
        for index in {0..3}; do
          comparison="${comparisons[(10#$pair + offset + 10#$case_id + index) % 4]}"
          case "$comparison" in
            aa) role=base;;
            aj) role=jwt;;
            as) role=scalar;;
            ac) role=combined;;
          esac
          if [[ "$context" != behavior ]]; then
            run_pair "$group" "$pair" "$comparison" "$role" "$first" "$case_id" "$package" "$variant" "$filter"
          elif [[ "$first" == left ]]; then
            PR74_EXPECT_PLACEHOLDER_REJECTION=no run_leg "$group" "$pair" "$comparison" left base "$case_id" "$package" "$variant" "$filter"
            PR74_EXPECT_PLACEHOLDER_REJECTION="$(config_policy "$role")" \
              run_leg "$group" "$pair" "$comparison" right "$role" "$case_id" "$package" "$variant" "$filter"
          else
            PR74_EXPECT_PLACEHOLDER_REJECTION="$(config_policy "$role")" \
              run_leg "$group" "$pair" "$comparison" right "$role" "$case_id" "$package" "$variant" "$filter"
            PR74_EXPECT_PLACEHOLDER_REJECTION=no run_leg "$group" "$pair" "$comparison" left base "$case_id" "$package" "$variant" "$filter"
          fi
        done
      done < "$evidence/cases.tsv"
    done
  done
}

normalize_pr74() {
  local context="$1" comparison="$2" group="$3" stem="$4" side path
  for side in left right; do
    awk -F '\t' -v context="$context" -v comparison="$comparison" -v group="$group" -v side="$side" \
      'NR==FNR { scope[$1]=$6; next } $1==group && $3==comparison && $5==side && scope[$4]==context { print $8 }' \
      "$evidence/cases.tsv" "$evidence/order.tsv" |
      while IFS= read -r path; do cat "$evidence/$path"; done > "$scratch/combined.txt"
    "$scratch/bin/benchplan" --normalize "$scratch/combined.txt" "$stem.$side.txt"
  done
  "$scratch/bin/benchplan" --verify "$evidence/$context.expected.json" "$stem.left.txt" "$stem.right.txt"
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.left.txt" head="$stem.right.txt" > "$stem.csv" 2> "$stem.warnings.txt"
  "$scratch/bin/benchgate" --requires-confirmation "$stem.csv" > "$stem.requires-confirmation.txt"
}

analyze_pr74() {
  local context comparison status exit_status=0 stem
  printf 'context\tcomparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for context in historical direct; do
    for comparison in aa aj as ac; do
      stem="$evidence/$context.$comparison"
      normalize_pr74 "$context" "$comparison" primary "$stem.primary"
      normalize_pr74 "$context" "$comparison" confirmation "$stem.confirmation"
      status=0
      "$scratch/bin/benchgate" "$stem.primary.csv" "$stem.confirmation.csv" > "$stem.benchgate.log" 2>&1 || status=$?
      printf '%s\t%s\t%s\n' "$context" "$comparison" "$status" >> "$evidence/verdicts.tsv"
      if (( status != 0 )); then
        grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$stem.benchgate.log" || return "$status"
        exit_status=1
      fi
    done
  done
  cat "$evidence/verdicts.tsv"
  return "$exit_status"
}

python3 "$driver" plan "$evidence"
python3 "$driver" prepare "$repo" "$scratch" "$evidence"
source "$script_dir/diagnose-hosted-perf.sh"
if [[ "$mode" == --plan-only ]]; then
  run_leg() {
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\traw/%s.%s.%s.%s.%s.txt\n' \
      "$1" "$2" "$3" "$6" "$4" "$5" "$8" "$1" "$2" "$3" "$6" "$4" >> "$evidence/order.tsv"
  }
  sample_pr74
  python3 "$driver" order "$evidence"
  cat "$evidence/roles.tsv" "$evidence/cases.tsv"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir "$TMPDIR" "$scratch/bin"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
cp "$driver" "${BASH_SOURCE[0]}" "$evidence/"
cat > "$evidence/protocol.txt" <<'PROTOCOL'
All four archives derive from exact a75a0ac. A/A reuses one executable in both legs.
A/J tests the five-file configuration repair, A/S tests the integer optimization, A/C tests their exact combined source.
Historical cases use each role's default linked test package and byte-identical timed fixture files. The JWT and combined trees link their normal added regressions.
The old official runs used larger package batches. This diagnostic groups selected leaves by fixture and shard.
Historical and direct equivalent controls have separate strict verdicts. Direct configuration and integer fixtures are identical across their four respective overlays.
The default-secret case is a separate behavior observation. A/S accept, J/C reject, with each role checked by the same probe. Its raw and signed results cannot waive another leaf.
Two groups of ten alternating pairs per comparison. The order of the four comparisons rotates per case and pair.
Default link, native linux/amd64 v1, Go 1.27.1, 200ms, CPU2, benchmem. Alpha .01, significant time increase >5%, bytes and allocations increase >0%, with two-group confirmation. No cost exemptions.
A failed A/A leaves the corresponding scope inconclusive. Full official PR checks remain required.
PROTOCOL
compile_pr74
python3 "$driver" inventory "$evidence" > "$evidence/inventory-cpu.txt"
python3 "$driver" sources "$scratch/source"
date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
sample_pr74
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
python3 "$driver" samples "$evidence"
python3 "$driver" sources "$scratch/source"
analyze_pr74
