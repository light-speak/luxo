#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C
base=f9ce659fd7f17bf57cfe0aee7de7ecb8d9b8f2e4
base_tree=68591bb02805a9c5a95e47864c3be1abb6e256cb
head=1b6c73bd67a781beb9e8b783cc7f8f631bf1276e
head_tree=069227acf8a9af5f1276661e70bebf9774ed3f16
run=45752445232
lane=${1:?usage: run.sh lane source-git-dir original.zip evidence-dir [--plan-only]}
case "$lane" in
  codegen)
    model='AMD EPYC 9V45 96-Core Processor'
    artifact=15086504371
    sha=3271f19bb3cb79cfa109723563fb46be7d310ff5b1e5adc153a06d6b846e910e
    pkg=pkg/codegen
    files=(scanner_test.go compile_auth_test.go dataloader_nullable_bench_test.go)
    symbols='(BenchmarkDurationScannerGeneration|BenchmarkRelationLoaderGeneration|generateScanner|durationScanFormat|writeScannerLocals|writeScannerResult|generateDataLoaderFile|CheckReady|generateSingleModuleEntry|fmt[.]Fprintf|str[.](ToSnakeCase|Capitalize))'
    ;;
  rpc)
    model='Intel(R) Xeon(R) 6975P-C'
    artifact=15085914264
    sha=339c65fd224fa84efedd64865bdbbcb0cba069f828db7534067e86a33c3e2f38
    pkg=pkg/lux/rpc
    files=(request_reuse_test.go)
    symbols='(BenchmarkLRPCOwnedRequest|callRequest.*prepare|writePreparedFrame|appendCallBody|retainReadFrame|CheckReady|runtime.memmove)'
    ;;
  *) printf 'Unknown diagnostic lane: %s\n' "$lane" >&2; exit 2 ;;
esac
if [[ ${2:-} == --artifact ]]; then
  printf '%s\t%s\t%s\n' "$artifact" "$sha" "$model"
  exit
fi

repo=$(cd "${2:?missing git source}" && pwd -P)
zip=${3:?missing original archive}
mkdir -p "${4:?missing evidence directory}"
out=$(cd "$4" && pwd -P)
mode=${5:-native}
[[ "$mode" == native || "$mode" == --plan-only ]]
test "$(shasum -a 256 "$zip" | cut -d ' ' -f 1)" = "$sha"
archive() { unzip -p "$zip" "$1"; }
g() { git --no-replace-objects -C "$repo" "$@"; }
test "$(g rev-parse "$base^{tree}")" = "$base_tree"
test "$(g rev-parse "$head^{tree}")" = "$head_tree"
test "$(g cat-file commit "$head" | sed -n 's/^parent //p' | head -n 1)" = "$base"
test "$(archive benchmark-revisions.txt)" = "$(printf 'base_sha=%s\ntested_sha=%s' "$base" "$head")"
expected=$(archive benchmark-environment.txt | sed -n 's/^Model name:[[:space:]]*//p')
test "$expected" = "$model"

work=$(mktemp -d "$out/.pr143-timing.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
mkdir "$work/bin" "$work/tools" "$work/tmp"
printf 'run=%s\nartifact=%s\nsha256=%s\nbase=%s\nbase_tree=%s\nhead=%s\nhead_tree=%s\n' \
  "$run" "$artifact" "$sha" "$base" "$base_tree" "$head" "$head_tree" > "$out/revisions.txt"
for item in benchmark-confirmation-batch.tsv benchmark-confirmation-expected.json benchmark-compile-profile.txt benchmark-gate.txt benchmark-environment.txt; do
  archive "$item" > "$out/original-$item"
done
IFS=$'\t' read -r original_pkg profile filter < "$out/original-benchmark-confirmation-batch.tsv"
test "$original_pkg" = "$pkg"
test "$profile" = default
test -n "$filter"
test "$(wc -l < "$out/original-benchmark-confirmation-batch.tsv" | tr -d ' ')" = 1

for file in "${files[@]}"; do
  a=$(g rev-parse "$base:$pkg/$file")
  test "$a" = "$(g rev-parse "$head:$pkg/$file")"
  printf '%s %s/%s\n' "$a" "$pkg" "$file"
  g show "$base:$pkg/$file" > "$work/$file"
done > "$out/fixtures.git-blobs"
g diff --name-status "$base" "$head" > "$out/product-diff.tsv"
g diff "$base" "$head" -- "$pkg" > "$out/target-source.diff"

declaration() {
  awk -v name="$2" '
    $0 ~ ("^func " name "[(]") { found++; active = 1 }
    active { print; if ($0 == "}") { print ""; active = 0 } }
    END { if (found != 1 || active) exit 1 }
  ' "$work/$1"
}
if [[ "$lane" == codegen ]]; then
  {
    printf '%s\n' 'package codegen' 'import ("strconv"; "strings"; "testing"; "github.com/light-speak/luxo/pkg/ast"; "github.com/light-speak/luxo/pkg/lexer"; "github.com/light-speak/luxo/pkg/parser"; "github.com/light-speak/luxo/pkg/semantic")'
    declaration scanner_test.go BenchmarkDurationScannerGeneration
    declaration compile_auth_test.go analyzePermissionFixture
    declaration dataloader_nullable_bench_test.go relationBenchmarkInput
    declaration dataloader_nullable_bench_test.go BenchmarkRelationLoaderGeneration
  } > "$work/isolated_test.go"
else
  {
    printf '%s\n' 'package rpc' 'import ("fmt"; "io"; "testing")'
    declaration request_reuse_test.go BenchmarkLRPCOwnedRequest
  } > "$work/isolated_test.go"
fi
cp "$work/isolated_test.go" "$out/isolated_test.go.txt"
test "$mode" != --plan-only || exit 0

actual=$(lscpu | sed -n 's/^Model name:[[:space:]]*//p')
test "$actual" = "$model"
test "$(uname -sm)" = 'Linux x86_64'
test "$(go env GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED; } > "$out/environment.txt"
export TMPDIR="$work/tmp" GOWORK=off
g archive "$base" go.mod go.sum scripts/setup-modules.sh scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout |
  tar -x -C "$work/tools"
for tool in benchrun benchplan benchgate; do
  (cd "$work/tools" && go build -o "$work/bin/$tool" "./scripts/$tool")
done
bin="$work/bin"
GOBIN="$bin" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
"$bin/benchrun" performance-profile > "$out/replayed-profile.txt"
cmp "$out/original-benchmark-compile-profile.txt" "$out/replayed-profile.txt"

bench_name="${pkg//\//-}.test"
for arm in a b; do
  rev="$base"
  [[ "$arm" != b ]] || rev="$head"
  directory="$work/$arm"
  mkdir -p "$directory/.tmp"
  g archive "$rev" | tar -x -C "$directory"
  sh "$work/tools/scripts/setup-modules.sh" "$directory"
  compiler=compile-performance-baseline
  [[ "$arm" != b ]] || compiler=compile-performance
  "$bin/benchrun" "$compiler" "$directory" "$bin/default-$arm" "$pkg" default
  mkdir "$bin/isolated-$arm"
  python3 - "$directory/$pkg" "${files[0]}" "$work/isolated_test.go" > "$work/overlay.json" <<'PYTHON'
import json
from pathlib import Path
import sys

package, retained, fixture = sys.argv[1:]
directory = Path(package)
paths = sorted(directory.glob("*_test.go"))
assert directory / retained in paths
print(json.dumps({"Replace": {str(path): fixture if path.name == retained else "" for path in paths}}))
PYTHON
  (cd "$directory" &&
    GOWORK="$directory/go.work" go test -trimpath -c -overlay="$work/overlay.json" "./$pkg" -o "$bin/isolated-$arm/$bench_name")
done

for layout in default isolated; do
  for arm in a b; do
    binary="$bin/$layout-$arm/$bench_name"
    evidence="$out/$layout-$arm"
    go tool nm -size "$binary" > "$work/nm"
    awk -v pattern="$symbols" '$0 ~ pattern' "$work/nm" > "$evidence.symbols.txt"
    go tool objdump -s "$symbols" "$binary" > "$evidence.objdump.txt"
    readelf -SW "$binary" > "$evidence.sections.txt"
    readelf -lW "$binary" > "$evidence.segments.txt"
    objcopy --dump-section ".text=$work/text" --dump-section ".rodata=$work/rodata" "$binary" "$work/link.tmp"
    (cd "$work" && sha256sum text rodata) > "$evidence.sections-sha256"
    rm -- "$work/text" "$work/rodata" "$work/link.tmp"
    sha256sum "$binary" | cut -d ' ' -f 1 > "$evidence.binary-sha256"
    go version -m "$binary" > "$evidence.build-info.txt"
  done
done

sample() {
  local arm="$1" dest="$2"
  "$bin/benchrun" run-performance "$work/$arm" "$bin/$layout-$arm" "$pkg" default \
    '-test.run=^$' "-test.bench=$filter" -test.benchmem -test.benchtime=200ms -test.count=1 -test.cpu=2 -test.timeout=120s >> "$dest"
}
compare() {
  "$bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    "base=$1" "head=$2" > "$3" 2> "$3.warnings"
}
group() {
  local name="$1" left="$2" right="$3" start="$4" round prefix
  prefix="$out/$layout-$name"
  : > "$prefix.left.raw"
  : > "$prefix.right.raw"
  for round in {1..10}; do
    if (( (round + start) % 2 == 0 )); then
      printf '%s\t%s\t%s\tleft,right\n' "$layout" "$name" "$round" >> "$out/order.tsv"
      sample "$left" "$prefix.left.raw"
      sample "$right" "$prefix.right.raw"
    else
      printf '%s\t%s\t%s\tright,left\n' "$layout" "$name" "$round" >> "$out/order.tsv"
      sample "$right" "$prefix.right.raw"
      sample "$left" "$prefix.left.raw"
    fi
  done
  for side in left right; do
    "$bin/benchplan" --normalize "$prefix.$side.raw" "$prefix.$side.txt"
    test "$(sed -n 's/^cpu: //p' "$prefix.$side.txt" | sort -u)" = "$model"
  done
  "$bin/benchplan" --verify "$out/original-benchmark-confirmation-expected.json" "$prefix.left.txt" "$prefix.right.txt"
  compare "$prefix.left.txt" "$prefix.right.txt" "$prefix.csv"
  if [[ "$left" == "$right" ]]; then
    compare "$prefix.right.txt" "$prefix.left.txt" "$prefix.reverse.csv"
  fi
}
gate() {
  local name="$1" code=0
  shift
  "$bin/benchgate" "$@" > "$out/$name.gate.txt" 2>&1 || code=$?
  printf '%s\t%s\n' "$name" "$code" >> "$out/gate-status.tsv"
  if (( code != 0 )); then
    grep -Eq '^performance gate (rejected |requires confirmation for )' "$out/$name.gate.txt" || return "$code"
  fi
}
: > "$out/order.tsv"
: > "$out/gate-status.tsv"
for layout in default isolated; do
  group aa a a 1
  group primary a b 1
  group confirmation a b 0
  group bb b b 0
  for control in aa bb; do
    gate "$layout-$control" "$out/$layout-$control.csv"
    gate "$layout-$control-reverse" "$out/$layout-$control.reverse.csv"
  done
  gate "$layout-ab-primary" "$out/$layout-primary.csv"
  gate "$layout-ab-confirmation" "$out/$layout-confirmation.csv"
  gate "$layout-ab-combined" --approved-costs "$work/tools/scripts/benchgate/approved-costs.json" --base-sha "$base" \
    "$out/$layout-primary.csv" "$out/$layout-confirmation.csv"
done
if awk -F '\t' '$1 ~ /-(aa|bb)(-reverse)?$/ && $2 != 0 { unstable = 1 } END { exit !unstable }' "$out/gate-status.tsv"; then
  printf 'unstable-control\n' | tee "$out/outcome.txt"
  exit 4
fi
status() { awk -F '\t' -v key="$1" '$1 == key {print $2}' "$out/gate-status.tsv"; }
confirmed_times() {
  awk '/^confirmed performance regression: .* sec\/op / { print $4 }' "$1" | sort -u
}
confirmed_times "$out/original-benchmark-gate.txt" > "$out/original-confirmed-time.txt"
confirmed_times "$out/default-ab-combined.gate.txt" > "$out/reproduced-time.txt"
test -s "$out/original-confirmed-time.txt"
comm -23 "$out/original-confirmed-time.txt" "$out/reproduced-time.txt" > "$out/not-reproduced-time.txt"
if [[ "$(status default-ab-combined)" != 1 || -s "$out/not-reproduced-time.txt" ]]; then
  printf 'default-failure-not-reproduced\n' | tee "$out/outcome.txt"
  exit 5
fi
if [[ "$(status isolated-ab-combined)" != 0 ]]; then
  printf 'isolated-strict-failure\n' | tee "$out/outcome.txt"
  exit 6
fi
printf 'default-reproduced-isolated-green\n' | tee "$out/outcome.txt"
