#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
driver="$script_dir/diagnose-pr78-pr85-attribution.py"
assets="$script_dir/diagnose-pr78-pr85"
if [[ "${1:-}" == --plan-only ]]; then
  [[ $# -eq 2 ]]
  exec python3 "$driver" plan "$2"
fi
[[ $# -eq 0 ]]

repo=$(git rev-parse --show-toplevel)
mkdir -p "$repo/.tmp"
evidence="$repo/.tmp/hosted-pr78-pr85-evidence"
mkdir "$evidence"
scratch=$(mktemp -d "$repo/.tmp/hosted-pr78-pr85.XXXXXXXX")
cleanup_attribution() {
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/hosted-pr78-pr85.* ]]
  rm -rf -- "$scratch"
}
trap cleanup_attribution EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source"

{
  printf '%s\n' 'Same Linux amd64 runner; native default linker; Go 1.27.1; CGO=1.'
  printf '%s\n' 'Each A/A and A/B has two groups of 10 alternating pairs at 200ms, CPU2, benchmem, alpha .01.'
  printf '%s\n' 'Original benchgate ns/B/alloc thresholds and two hits apply. An A/A rejection invalidates attribution.'
  printf '%s\n' 'A/B rejection remains visible in per-comparison verdicts. Its result does not decide this diagnostic job.'
  printf '%s\n' 'Full and controlled Luvia use separate binaries. API parse and H2 shutdown fixtures each use separate binaries.'
  printf '%s\n' 'Protected logger output intentionally changes. Parse wire bytes are checked independently before timing.'
} > "$evidence/protocol.txt"

sampler="$script_dir/diagnose-hosted-perf.sh"
printf '%s  %s\n' '9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072' "$sampler" | sha256sum -c
# Reuse the pinned process sampler, native compile overlay, evidence, and normalization.
source "$sampler"
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
python3 "$driver" plan "$evidence"
cp "$assets/pins.json" "$evidence/source-pins.json"
base64 --decode "$assets/request-head.patch.b64" > "$evidence/request-head.patch"
cp "$script_dir/diagnose-h2-shutdown-opening.go.txt" "$evidence/shutdown-fixture.go.txt"
cp "$assets/parse-log.go.txt" "$evidence/parse-fixture.go.txt"
{
  printf '%s  %s\n' '6d3c631f63ad4c455e00d66c053a108183963e44eb9f7dd7b76f0ca0ea09d365' "$evidence/request-head.patch"
  printf '%s  %s\n' 'ea6127592cb1c6ae965007266ffead6e01eed7026e128e9346e722af64b397bb' "$evidence/shutdown-fixture.go.txt"
  printf '%s  %s\n' '647c12a482898f8d88427c41fcd70ec16b3934f8942552dd691b6f5ce555ea06' "$evidence/parse-fixture.go.txt"
} > "$evidence/fixture-sha256.txt"
sha256sum -c "$evidence/fixture-sha256.txt"

revision() {
  case "$1" in
    base) printf '%s' 65f1892e175487503aa36caa86d1eb8a0805f073 ;;
    pr78) printf '%s' 397af9c8c6fc6dc807e1dbcbe2e05d8913f58944 ;;
    pr85) printf '%s' 99194f1d7ef5c9745fba2dd8b782cfd1ca5b1aff ;;
    *) return 1 ;;
  esac
}
base=$(revision base)
request_head=pkg/lux/h2transport/request_headers.go

prepare_role() {
  local role="$1" original="${1%-r}" commit source
  commit=$(revision "$original")
  source="$scratch/source/$role"
  git -C "$repo" merge-base --is-ancestor "$base" "$commit"
  mkdir -p "$source/.tmp" "$scratch/bin/$role"
  git -C "$repo" archive "$commit" | tar -xf - -C "$source"
  git -C "$repo" show -s --format="$role %H %T" "$commit" >> "$evidence/revisions.txt"
  python3 "$driver" sources "$source" "$original" before >> "$evidence/request-head-roundtrip.txt"
  if [[ "$role" == *-r ]]; then
    (
      export GIT_CEILING_DIRECTORIES="$scratch/source"
      git -C "$source" apply --check --include="$request_head" "$evidence/request-head.patch"
      git -C "$source" apply --include="$request_head" "$evidence/request-head.patch"
      python3 "$driver" sources "$source" "$original" after >> "$evidence/request-head-roundtrip.txt"
      git -C "$source" apply --reverse --check --include="$request_head" "$evidence/request-head.patch"
      git -C "$source" apply --reverse --include="$request_head" "$evidence/request-head.patch"
      python3 "$driver" sources "$source" "$original" before >> "$evidence/request-head-roundtrip.txt"
      git -C "$source" apply --include="$request_head" "$evidence/request-head.patch"
      python3 "$driver" sources "$source" "$original" after >> "$evidence/request-head-roundtrip.txt"
    )
  fi
  sh "$scratch/source/base/scripts/setup-modules.sh" "$source" > "$evidence/workspace-$role.log" 2>&1
  cp "$source/go.work" "$evidence/go-$role.work.txt"
  (
    cd "$source"
    find pkg internal scripts -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -print0 |
      LC_ALL=C sort -z | xargs -0 sha256sum
    sha256sum go.mod go.sum go.work
  ) > "$evidence/source-$role.sha256"
}

for role in base pr78 pr85 base-r pr78-r pr85-r; do
  prepare_role "$role"
done
(
  cd "$scratch/source/base"
  for tool in benchrun benchplan benchgate; do
    go build -o "$scratch/bin/$tool" "./scripts/$tool"
  done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/compile-tools.log" 2>&1

compile_role() {
  local role="$1" package
  shift
  for package; do
    "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" "$package"
  done > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  local binary="$scratch/bin/$role/pkg-lux-luvia.test"
  go tool nm "$binary" | grep -E 'AuthMiddleware|extractBearerToken|parseRequestHead' > "$evidence/layout-$role-nm.txt"
  go tool objdump -s 'h2transport\.parseRequestHead$' "$binary" > "$evidence/layout-$role-parser.txt"
  go tool objdump -s 'luvia\..*(AuthMiddleware|extractBearerToken)' "$binary" > "$evidence/layout-$role-auth.txt"
}

compile_role base pkg/lux/luvia pkg/lux/h2transport
compile_role pr78 pkg/lux/luvia pkg/lux/h2transport
for role in pr85 base-r pr78-r pr85-r; do
  compile_role "$role" pkg/lux/luvia
done
python3 "$driver" layout "$evidence"
for role in base pr78; do
  compile_diagnostic_fixture "$role" shutdown pkg/lux/h2transport shutdown_opening_diagnostic_test.go shutdown-fixture.go.txt
done
for role in base pr85; do
  compile_diagnostic_fixture "$role" parse pkg/lux/api parse_logging_diagnostic_test.go parse-fixture.go.txt
  policy=original
  [[ "$role" != pr85 ]] || policy=category
  LUXO_DIAGNOSTIC_PARSE_LOG="$policy" "$scratch/bin/benchrun" run "$scratch/source/$role" \
    "$scratch/bin/$role-parse" pkg/lux/api -test.run='^TestDiagnosticWriteParseErrorWire$' -test.v -test.timeout=30s \
    > "$evidence/parse-wire-$role.log" 2>&1
  sed -n 's/.*parse-wire /parse-wire /p' "$evidence/parse-wire-$role.log" > "$evidence/parse-wire-$role.tsv"
  [[ "$(wc -l < "$evidence/parse-wire-$role.tsv")" -eq 6 ]]
done
diff -u "$evidence/parse-wire-base.tsv" "$evidence/parse-wire-pr85.tsv" > "$evidence/parse-wire.diff"

while IFS=$'\t' read -r role case_id package variant filter; do
  binaries="$scratch/bin/$role"
  [[ "$variant" == default ]] || binaries+="-$variant"
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$binaries" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s \
    > "$evidence/inventory-$role-$case_id.txt" 2>> "$evidence/benchmark-stderr.txt"
done < "$evidence/inventory.tsv"
python3 "$driver" inventory "$evidence"

mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
while IFS=$'\t' read -r group sample comparison case_id side role variant raw package filter; do
  run_leg "$group" "$sample" "$comparison" "$side" "$role" "$case_id" "$package" "$variant" "$filter"
done < "$evidence/schedule.tsv"
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
python3 "$driver" samples "$evidence"

failed_control=0
printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
while IFS=$'\t' read -r comparison left right; do
  cp "$evidence/$comparison.expected.json" "$evidence/expected.json"
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  status=0
  gate_args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
  if [[ "$left" == base && "$left" != "$right" ]]; then
    gate_args=(--approved-costs "$scratch/source/$right/scripts/benchgate/approved-costs.json" --base-sha "$base" "${gate_args[@]}")
  fi
  "$scratch/bin/benchgate" "${gate_args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
  printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
  if (( status != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log"
    if [[ "$left" == "$right" ]]; then
      failed_control=1
    fi
  fi
done < "$evidence/comparisons.tsv"
rm -- "$evidence/expected.json"
cat "$evidence/verdicts.tsv"
exit "$failed_control"
