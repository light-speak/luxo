#!/usr/bin/env bash
set -euo pipefail
umask 077

base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
base_tree=ba252c7cace8908cdfcd7c203bc9dc11bb2ba746
pair_tools=43f4e182b81b1b725f2c8484bed5f95ed1342352
helper_sha=9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
package=pkg/lux/sample
filter='^BenchmarkProbe$'
comparisons=(native-aa native-fixture native-time native-alloc frozen-aa frozen-fixture frozen-time frozen-alloc)

check_sources() {
  mkdir "$scratch/tools" "$scratch/bin"
  [[ "$(git -C "$repo" show -s --format=%T "$base")" == "$base_tree" ]]
  git -C "$repo" archive "$base" go.mod go.sum internal/benchlayout scripts/benchplan scripts/benchgate |
    tar -xf - -C "$scratch/tools"
  git -C "$repo" archive "$pair_tools" scripts/benchrun | tar -xf - -C "$scratch/tools"
  git -C "$repo" diff --exit-code "$pair_tools" "$base" -- internal/benchlayout
  git -C "$repo" show "$pair_tools:scripts/diagnose-hosted-perf.sh" > "$scratch/helpers.sh"
  python3 - "$scratch/helpers.sh" "$helper_sha" <<'PYTHON'
import hashlib
from pathlib import Path
import sys

if hashlib.sha256(Path(sys.argv[1]).read_bytes()).hexdigest() != sys.argv[2]:
    raise SystemExit("upstream sampling helper bytes changed")
PYTHON
  git -C "$repo" show "$pair_tools:scripts/benchrun/pair_test.go" > "$scratch/pair_test.go"
  git -C "$repo" show -s --format='%H %T' "$base" "$pair_tools" > "$evidence/revisions.txt"
  python3 "$driver" stage "$scratch/source" "$evidence" "$scratch/pair_test.go" "$asset"
  (
    cd "$scratch/tools"
    export GIT_CEILING_DIRECTORIES="$scratch"
    if git rev-parse --show-toplevel >/dev/null 2>&1; then
      printf 'Tool archive must be outside a Git working tree.\n' >&2
      return 1
    fi
    git apply --whitespace=error --check "$evidence/frozen-inventory.patch"
    git apply --whitespace=error "$evidence/frozen-inventory.patch"
    git apply --reverse --check "$evidence/frozen-inventory.patch"
  )
  tar -cf "$evidence/control-source.tar" -C "$scratch" source
}

sample_controls() {
  local group offset=0 i pair first step index comparison role variant revision
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    for i in {1..10}; do
      printf -v pair '%02d' "$i"
      first=right
      if (( (i + offset) % 2 == 1 )); then
        first=left
      fi
      for step in {0..7}; do
        index=$(( (i + 4 * offset + step) % 8 ))
        comparison="${comparisons[$index]}"
        role="${comparison#*-}"
        revision="$role"
        [[ "$role" != aa ]] || revision=base
        variant=default
        [[ "$comparison" != frozen-* ]] || variant=frozen
        run_pair "$group" "$pair" "$comparison" "$revision" "$first" 01 "$package" "$variant" "$filter"
      done
    done
    offset=1
  done
}

plan() {
  run_leg() {
    local group="$1" pair="$2" comparison="$3" side="$4" role="$5"
    local id="$6" pkg="$7" mode="$8" match="$9"
    [[ "$pkg" == "$package" && "$match" == "$filter" ]]
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\traw/%s.%s.%s.%s.%s.txt\n' \
      "$group" "$pair" "$comparison" "$id" "$side" "$role" "$mode" \
      "$group" "$pair" "$comparison" "$id" "$side" >> "$evidence/order.tsv"
  }
  sample_controls
  python3 "$driver" order "$evidence"
  printf 'Plan verified: 2 sample groups, 10 pairs each, 8 comparisons, 320 arms, Linux amd64 Go 1.27.1.\n'
}

compile_tools() (
  set -x
  cd "$scratch/tools"
  export GOWORK=off
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  go build -o "$scratch/bin/benchplan" ./scripts/benchplan
  go build -o "$scratch/bin/benchgate" ./scripts/benchgate
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
)

compile_controls() {
  local role output
  for role in base fixture time alloc; do
    "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" "$package" \
      > "$evidence/compile-native-$role.log" 2>&1
    record_binaries "$role"
  done
  for role in fixture time alloc; do
    output="$scratch/pair-$role"
    if ! "$scratch/bin/benchrun" compile-pair "$scratch/source/base" "$scratch/source/$role" \
      "$output" "$package" > "$evidence/compile-pair-$role.log" 2>&1; then
      if [[ -f "$output/fixtures.json" ]]; then
        cp "$output/fixtures.json" "$evidence/compile-pair-$role.json"
      fi
      return 1
    fi
    cp "$output/fixtures.json" "$evidence/compile-pair-$role.json"
    cp "$output/fixtures/$package/base.json" "$evidence/compile-pair-$role.base-overlay.json"
    cp "$output/fixtures/$package/head.json" "$evidence/compile-pair-$role.head-overlay.json"
    mv "$output/head" "$scratch/bin/$role-frozen"
    record_binaries "$role-frozen"
  done
  mv "$scratch/pair-fixture/base" "$scratch/bin/base-frozen"
  record_binaries base-frozen
}

inventory_controls() {
  local role variant suffix
  for role in base fixture time alloc; do
    for variant in native frozen; do
      suffix=""
      [[ "$variant" != frozen ]] || suffix=-frozen
      "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role$suffix" "$package" \
        -test.run='^TestControlResult$' -test.v -test.count=1 -test.cpu=2 -test.timeout=90s \
        > "$evidence/correctness-$role-$variant.log" 2>&1
      grep -q '^--- PASS: TestControlResult ' "$evidence/correctness-$role-$variant.log"
      "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role$suffix" "$package" \
        -test.run='^$' -test.bench=. -test.benchmem -test.benchtime=1x -test.count=1 \
        -test.cpu=2 -test.timeout=90s > "$evidence/inventory-$role-$variant.log" \
        2> "$evidence/inventory-$role-$variant.stderr"
      "$scratch/bin/benchplan" --normalize "$evidence/inventory-$role-$variant.log" \
        "$scratch/inventory-$role-$variant.txt"
    done
  done
  for role in fixture time alloc; do
    "$scratch/bin/benchplan" --verify-frozen-inventory "$package" \
      "$scratch/inventory-base-native.txt" "$scratch/inventory-$role-native.txt" \
      "$scratch/inventory-base-frozen.txt" "$scratch/inventory-$role-frozen.txt" \
      > "$evidence/inventory-$role.json"
  done
  python3 "$driver" inventories "$evidence"
}

evaluate_controls() {
  local comparison rc raw
  printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for comparison in "${comparisons[@]}"; do
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    rc=0
    "$scratch/bin/benchgate" "$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv" \
      > "$evidence/$comparison.benchgate.log" 2>&1 || rc=$?
    printf '%s\t%s\n' "$comparison" "$rc" >> "$evidence/verdicts.tsv"
  done
  mkdir -p "$scratch/normalized/raw"
  for raw in "$evidence"/raw/*.txt; do
    "$scratch/bin/benchplan" --normalize "$raw" "$scratch/normalized/raw/$(basename "$raw")"
  done
  python3 "$driver" metrics "$evidence" "$scratch/normalized"
  python3 "$driver" verdict "$evidence"
  cat "$evidence/verdicts.tsv"
}

main() {
  local mode="${1:---measure}" assets
  [[ "$#" -le 1 && ( "$mode" == --plan || "$mode" == --measure ) ]]
  repo=$(git rev-parse --show-toplevel)
  assets=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
  driver="$assets/diagnose-frozen-gate-controls.py"
  asset="$assets/diagnose-frozen-gate-controls.patch.b64"
  mkdir -p "$repo/.tmp"
  scratch=$(mktemp -d "$repo/.tmp/frozen-gate-controls.XXXXXXXX")
  trap 'rm -rf -- "$scratch"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  if [[ "$mode" == --plan ]]; then
    evidence="$scratch/plan"
  else
    evidence="$repo/.tmp/hosted-frozen-gate-controls"
  fi
  mkdir "$evidence"
  check_sources
  # Source only the exact, verified sampling implementation.
  source "$scratch/helpers.sh"
  if [[ "$mode" == --plan ]]; then
    plan
    return
  fi
  export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local GOAMD64=v1 CGO_ENABLED=1
  mkdir "$TMPDIR"
  [[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64)" == $'linux\namd64\nv1' ]]
  [[ -z "$(go env GOFLAGS)" && -z "$(go env GOEXPERIMENT)" ]]
  collect_host > "$evidence/environment.txt"
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
One Linux amd64 runner. 2 groups, 10 alternating pairs each, benchmark count=1, cpu=2,
200ms, benchmem, default linking. Original benchstat alpha=.01 and benchgate thresholds,
with both complete groups required and no approved-cost exceptions.
Native and frozen A/A use the same baseline binary on both legs and must pass.
Test-only CPU work must fail native; the byte-identical frozen binaries must pass.
The CPU production change must reject time in both modes and have no allocation increase.
The production allocation must reject B/op and allocs/op in both modes, every pair +256 B/op and +1 alloc/op.
The native candidate-only test leaf is inventoried and archived separately.
PROTOCOL
  compile_tools > "$evidence/compile-tools.log" 2>&1
  compile_controls
  inventory_controls
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_controls
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  evaluate_controls
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
