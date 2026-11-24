#!/usr/bin/env bash
set -euo pipefail

mode="${1:?usage: benchmark-fixtures.sh plan|compile|observe workspace [shard]}"
work=$(cd "${2:?missing benchmark workspace}" && pwd -P)
bin="$work/bin"
export TMPDIR="$work/test-tmp"
mkdir -p "$TMPDIR"

run_benchmark() {
  local side="$1" binaries="$2" package="$3" filter="$4" duration="$5"
  "$bin/benchrun" run "$work/$side" "$binaries" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime="$duration" \
    -test.count=1 -test.cpu=2 -test.timeout=180s
}

compile_pair() {
  local package="$1" label="${1//\//-}"
  "$bin/benchrun" compile-pair "$work/base" "$work/head" "$work/pairs/$label" "$package"
  jq -e '.Complete == true and (.Packages | length) > 0' "$work/pairs/$label/fixtures.json" >/dev/null
}

compilation_contract() {
  jq -eS '{
    Logical, GoEnvironment, Complete,
    Packages: [.Packages[] | {Physical, Baseline, HeadNative, FixtureSHA, DataSHA}]
  }' "$1"
}

discover_pair() {
  local package="$1" label="${1//\//-}" side role binaries inventory
  mkdir "$work/fixture-discovery/$label"
  for side in base head; do
    "$bin/benchrun" compile "$work/$side" "$bin/native-$side" "$package"
    run_benchmark "$side" "$bin/native-$side" "$package" . 1x \
      > "$work/fixture-discovery/$label/native-$side.raw.log"
    "$bin/benchplan" --normalize "$work/fixture-discovery/$label/native-$side.raw.log" \
      "$work/fixture-discovery/$label/native-$side.log"
  done
  compile_pair "$package"
  cp "$work/pairs/$label/fixtures.json" "$work/fixture-discovery/$label/fixtures.json"
  compilation_contract "$work/pairs/$label/fixtures.json" > "$work/fixture-discovery/$label/compilation.json"
  for side in base head; do
    binaries="$work/pairs/$label/$side"
    inventory="$work/benchmark-inventory/$side/$label"
    run_benchmark "$side" "$binaries" "$package" . 1x > "$inventory.raw.log"
    "$bin/benchplan" --normalize "$inventory.raw.log" "$inventory.log"
  done
  role="$work/fixture-discovery/$label"
  "$bin/benchplan" --verify-frozen-inventory "$package" "$role/native-base.log" "$role/native-head.log" \
    "$work/benchmark-inventory/base/$label.log" "$work/benchmark-inventory/head/$label.log" > "$role/inventory.json"
  for side in base head; do
    "$bin/benchplan" --native-only-plan "$role/inventory.json" "$side" > "$role/observe-$side.tsv"
  done
}

plan() {
  local package label
  mkdir -p "$work/benchmark-inventory/base" "$work/benchmark-inventory/head" "$work/pairs" "$work/fixture-discovery" "$work/benchmark-plan"
  while IFS= read -r package; do
    label="${package//\//-}"
    printf 'Discovering %s\n' "$package" >&2
    discover_pair "$package" > "$work/fixture-discovery/$label.build.log" 2>&1
  done < "$work/benchmark-packages.txt"
  "$bin/benchplan" "$work/benchmark-packages.txt" "$work/benchmark-inventory" "$work/benchmark-plan" \
    > "$work/benchmark-plan/matrix.json"
  local batch shard
  publish_owners() {
    for batch in "$work"/benchmark-plan/shard-*.tsv; do
      shard="${batch##*/shard-}"
      awk -F '\t' -v shard="${shard%.tsv}" '{print $1 "\t" shard}' "$batch"
    done
  }
  publish_owners | awk -F '\t' '!seen[$1]++' | LC_ALL=C sort > "$work/benchmark-plan/fixture-owners.tsv"
  cut -f 1 "$work/benchmark-plan/fixture-owners.tsv" | cmp "$work/benchmark-packages.txt" -
  cat "$work/benchmark-plan/matrix.json"
}

compile_shard() {
  local package filter label side binary
  mkdir -p "$work/pairs" "$bin/base" "$bin/head" "$work/fixture-compilation"
  while IFS=$'\t' read -r package filter; do
    label="${package//\//-}"
    compile_pair "$package"
    local receipt="$work/pairs/$label/fixtures.json"
    cp "$receipt" "$work/fixture-compilation/$label.json"
    compilation_contract "$receipt" | cmp "$work/plan/fixture-discovery/$label/compilation.json" -
    for side in base head; do
      for binary in "$work/pairs/$label/$side/"*.test; do
        test -f "$binary"
        ln -s "$binary" "$bin/$side/${binary##*/}"
      done
    done
  done < "$work/benchmark-batch.tsv"
}

observe_role() {
  local package="$1" label="${1//\//-}" side="$2" found_package filter n
  local receipt="$work/plan/fixture-discovery/$label"
  local evidence="$work/fixture-observations/$label-$side"
  "$bin/benchplan" --native-only-plan "$receipt/inventory.json" "$side" > "$evidence.tsv"
  cmp "$receipt/observe-$side.tsv" "$evidence.tsv"
  if [[ ! -s "$evidence.tsv" ]]; then
    return
  fi
  "$bin/benchrun" compile "$work/$side" "$bin/native-$side" "$package"
  IFS=$'\t' read -r found_package filter < "$evidence.tsv"
  test "$found_package" = "$package"
  test -n "$filter"
  for n in {1..10}; do
    run_benchmark "$side" "$bin/native-$side" "$package" "$filter" 200ms
  done > "$evidence.raw.log"
  "$bin/benchplan" --normalize "$evidence.raw.log" "$evidence.log"
  "$bin/benchplan" --verify-native-only "$receipt/inventory.json" "$side" "$evidence.log"
}

observe_shard() {
  local target="${1:?missing shard}" package owner
  [[ "$target" =~ ^[0-9][0-9]$ ]]
  mkdir -p "$work/fixture-observations"
  test -s "$work/plan/benchmark-plan/fixture-owners.tsv"
  while IFS=$'\t' read -r package owner; do
    if [[ "$owner" != "$target" ]]; then continue; fi
    awk -F '\t' -v package="$package" '$1 == package { found = 1 } END { exit !found }' "$work/benchmark-batch.tsv"
    observe_role "$package" base
    observe_role "$package" head
  done < "$work/plan/benchmark-plan/fixture-owners.tsv"
}

case "$mode" in
  plan) plan;;
  compile) compile_shard;;
  observe) observe_shard "${3:-}";;
  *) exit 2;;
esac
