#!/usr/bin/env bash
set -euo pipefail

mode="${1:?usage: run-benchmark-samples.sh sample|enforce workspace [base-sha]}"
work=$(cd "${2:?missing benchmark workspace}" && pwd -P)
bin="$work/bin"
export TMPDIR="$work/test-tmp"
mkdir -p "$TMPDIR"

run_package() {
  local side="$1" output="$2" package="$3" filter="$4"
  "$bin/benchrun" run "$work/$side" "$bin/$side" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=200ms \
    -test.count=1 -test.cpu=2 >> "$output"
}

run_group() {
  local group="$1" batch="$2" sample package filter first second
  local stem="$work/benchmark-raw-$group"
  : > "$stem-base.txt"
  : > "$stem-head.txt"
  for sample in {1..10}; do
    first=base second=head
    if (( sample % 2 == 0 )); then first=head second=base; fi
    while IFS=$'\t' read -r package filter; do
      run_package "$first" "$stem-$first.txt" "$package" "$filter"
      run_package "$second" "$stem-$second.txt" "$package" "$filter"
    done < "$batch"
  done
  "$bin/benchplan" --normalize "$stem-base.txt" "$work/benchmark-$group-base.txt"
  "$bin/benchplan" --normalize "$stem-head.txt" "$work/benchmark-$group-head.txt"
}

compare_group() {
  local group="$1" expected="$2"
  local base="base=$work/benchmark-$group-base.txt" head="head=$work/benchmark-$group-head.txt"
  "$bin/benchplan" --verify "$expected" "${base#base=}" "${head#head=}"
  "$bin/benchstat" -alpha 0.01 "$base" "$head"
  "$bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    "$base" "$head" > "$work/benchstat-$group.csv" 2> "$work/benchstat-$group-warnings.txt"
}

sample() {
  test -s "$work/benchmark-batch.tsv"
  run_group primary "$work/benchmark-batch.tsv"
  compare_group primary "$work/benchmark-expected.json"
  local targets="$work/benchmark-confirmation-targets.tsv"
  "$bin/benchgate" --confirmation-targets "$work/benchstat-primary.csv" > "$targets"
  if [[ ! -s "$targets" ]]; then
    printf 'Primary samples are clean; confirmation samples are unnecessary.\n'
    return
  fi
  printf 'Primary samples found regression candidates. Running targeted confirmation samples.\n'
  "$bin/benchplan" --confirmation-plan "$work/benchmark-expected.json" "$targets" \
    "$work/benchmark-confirmation-batch.tsv" "$work/benchmark-confirmation-expected.json"
  test -s "$work/benchmark-confirmation-batch.tsv"
  test -s "$work/benchmark-confirmation-expected.json"
  run_group confirmation "$work/benchmark-confirmation-batch.tsv"
  compare_group confirmation "$work/benchmark-confirmation-expected.json"
}

enforce() {
  local base="${1:?missing benchmark baseline}"
  [[ "$base" =~ ^[0-9a-f]{40}$ ]]
  local -a approval=(--approved-costs "$work/head/scripts/benchgate/approved-costs.json" --base-sha "$base")
  test -s "$work/benchstat-primary.csv"
  test -f "$work/benchmark-confirmation-targets.tsv"
  if [[ -s "$work/benchmark-confirmation-targets.tsv" ]]; then
    test -s "$work/benchstat-confirmation.csv"
    "$bin/benchgate" "${approval[@]}" --targeted-confirmation "$work/benchstat-primary.csv" "$work/benchstat-confirmation.csv"
  else
    test ! -s "$work/benchstat-confirmation.csv"
    "$bin/benchgate" "${approval[@]}" "$work/benchstat-primary.csv"
  fi
}

case "$mode" in
  sample) sample;;
  enforce) enforce "${3:-}";;
  *) exit 2;;
esac
