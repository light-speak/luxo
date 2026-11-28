#!/bin/bash
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"
base=de7360c78f1e6f50edfe837b2f2471f9d6ed9410
head=99a25aab7de63de85b5137820e9e85d557c0e158
base_tree=78a0ec2db7e24a2d68a8e53152dbb84228cc4502
head_tree=7372cc5019201862c8b96c85d45bdbb285824991
package=pkg/lux/luvia
evidence="$root/.tmp/pr143-luvia-amd-evidence"

bind_checkout() {
  test "$GITHUB_REPOSITORY" = light-speak/luxo
  test "$GITHUB_EVENT_NAME" = push
  test "$GITHUB_REF" = refs/heads/diagnostics/pr143-luvia-readiness-amd
  test "$RUNNER_KIND" = github-hosted
  test "$(git rev-parse HEAD)" = "$GITHUB_SHA"
  test "$(git show -s --format=%P HEAD)" = "$head"
  expected=$'A\t.github/workflows/diagnose-pr143-luvia-amd.yml\nA\tscripts/diagnose-pr143-luvia-amd/run.sh'
  test "$(git diff --name-status "$head" HEAD)" = "$expected"
  test "$(git rev-parse "$head^{tree}")" = "$head_tree"
}

route() {
  bind_checkout
  mkdir -p "$evidence"
  models=$(awk -F ': ' '/^model name/{print $2}' /proc/cpuinfo | sort -u)
  printf '%s\n' "$models" > "$evidence/cpu-model.txt"
  lscpu --json > "$evidence/lscpu.json"
  cat /proc/sys/kernel/random/boot_id > "$evidence/boot-id.txt"
  if test "$models" = 'AMD EPYC 7763 64-Core Processor'; then
    test "$(uname -sm)" = 'Linux x86_64'
    test "$(getconf _NPROCESSORS_ONLN)" = 4
    grep -qx 'VERSION_ID="24.04"' /etc/os-release
    printf 'matched=true\n' >> "$GITHUB_OUTPUT"
  else
    printf 'matched=false\n' >> "$GITHUB_OUTPUT"
  fi
}

write_manifest() {
  local group=$1
  shift
  python3 -I -B - "$evidence/$group" "$package" "$@" <<'PY'
import json
from pathlib import Path
import sys

prefix, package, *names = sys.argv[1:]
assert names and len(set(names)) == len(names)
selector = '|'.join('/'.join(f'^{part}$' for part in name.split('/')) for name in names)
leaves = [{'Package': package, 'Name': name + '-2'} for name in names]
Path(prefix + '.selector').write_text(selector + '\n')
Path(prefix + '.expected.json').write_text(json.dumps({'Base': leaves, 'Head': leaves}) + '\n')
PY
}

build_arm() {
  local side=$1 mode=$2 source=$3 command=$4 flags= binary
  if test "$mode" = aligned; then flags=-ldflags=-funcalign=64; fi
  mkdir -p "$work/$mode/$side"
  GOFLAGS="$flags" "$bin/benchrun" "$command" "$source" "$work/$mode/$side" "$package" default
  binary="$work/$mode/$side/pkg-lux-luvia.test"
  sha256sum "$binary" > "$evidence/$mode-$side.sha256"
  go version -m "$binary" > "$evidence/$mode-$side.build.txt"
  go tool buildid "$binary" > "$evidence/$mode-$side.buildid"
  go tool nm -size -sort address "$binary" > "$work/$mode-$side.symbols"
  grep -E "$symbols" "$work/$mode-$side.symbols" > "$evidence/$mode-$side.symbols"
  go tool objdump -s "$symbols" "$binary" > "$evidence/$mode-$side.objdump"
}

sample() {
  local group=$1 mode=$2 side=$3 target=$4 selector source
  source="$work/$side"
  selector=$(cat "$evidence/$group.selector")
  "$bin/benchrun" run-performance "$source" "$work/$mode/$side" "$package" default \
    -test.run='^$' -test.bench="$selector" -test.benchmem -test.benchtime=200ms -test.count=1 -test.cpu=2 >> "$target"
}

comparison() {
  local group=$1 mode=$2 left=$3 right=$4 pair prefix order first second
  prefix="$evidence/$group.$mode.$left$right"
  for pair in {1..10}; do
    if (( pair % 2 )); then
      sample "$group" "$mode" "$left" "$prefix.left.raw.txt"
      sample "$group" "$mode" "$right" "$prefix.right.raw.txt"
    else
      sample "$group" "$mode" "$right" "$prefix.right.raw.txt"
      sample "$group" "$mode" "$left" "$prefix.left.raw.txt"
    fi
  done
  "$bin/benchplan" --normalize "$prefix.left.raw.txt" "$prefix.left.txt"
  "$bin/benchplan" --normalize "$prefix.right.raw.txt" "$prefix.right.txt"
  "$bin/benchplan" --verify "$evidence/$group.expected.json" "$prefix.left.txt" "$prefix.right.txt"
  for order in forward reverse; do
    first=left second=right
    if test "$order" = reverse; then first=right second=left; fi
    "$bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
      "base=$prefix.$first.txt" "head=$prefix.$second.txt" > "$prefix.$order.csv"
    if ! "$bin/benchgate" "$prefix.$order.csv" > "$prefix.$order.gate.txt" 2>&1; then
      grep -Eq '^performance gate rejected [0-9]+ regression\(s\)$' "$prefix.$order.gate.txt"
      test "$left" != "$right" || touch "$evidence/unstable-control"
    fi
  done
}

measure() {
  bind_checkout
  test "$(cat "$evidence/cpu-model.txt")" = 'AMD EPYC 7763 64-Core Processor'
  test "$(cat /proc/sys/kernel/random/boot_id)" = "$(cat "$evidence/boot-id.txt")"
  test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS GOTOOLCHAIN)" = \
    $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1\n\nlocal'
  base_source="$root/.tmp/pr143-amd-base"
  test "$(git -C "$base_source" rev-parse HEAD)" = "$base"
  test "$(git -C "$base_source" rev-parse 'HEAD^{tree}')" = "$base_tree"
  test -z "$(git -C "$base_source" status --porcelain)"
  printf 'base=%s\nbase_tree=%s\nhead=%s\nhead_tree=%s\ndiagnostic=%s\n' \
    "$base" "$base_tree" "$head" "$head_tree" "$GITHUB_SHA" > "$evidence/revisions.txt"
  work=$(mktemp -d "$root/.tmp/pr143-amd-work.XXXXXXXX")
  trap 'test "${work%/*}" = "$root/.tmp" && test ! -L "$work" && rm -rf -- "$work"' EXIT
  ln -s "$base_source" "$work/A"
  mkdir -p "$work/H/.tmp" "$work/A/.tmp" "$work/bin" "$work/tmp"
  git archive "$head" | tar -x -C "$work/H"
  bin="$work/bin"
  export TMPDIR="$work/tmp"
  GOBIN="$bin" GOWORK=off go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
  for tool in benchrun benchplan benchgate; do GOWORK=off go build -o "$bin/$tool" "./scripts/$tool"; done
  "$bin/benchrun" performance-profile > "$evidence/benchmark-compile-profile.txt"
  printf '%s  %s\n' f9c4c57d865d0038632086c464bc9e502b709b5229827d83513b4def8a73cccd \
    "$evidence/benchmark-compile-profile.txt" | sha256sum --check
  sh scripts/setup-modules.sh "$base_source"
  sh scripts/setup-modules.sh "$work/H"
  write_manifest auth BenchmarkAuthUpgradeHeaders/anonymous-get/before BenchmarkAuthUpgradeHeaders/anonymous-get/current \
    BenchmarkAuthUpgradeHeaders/anonymous-http/before BenchmarkAuthUpgradeHeaders/anonymous-http/current BenchmarkResolveParameterIDs/1/max-id
  write_manifest latency BenchmarkLatencyPercentiles/small/previous
  symbols='Benchmark(AuthUpgradeHeaders|ResolveParameterIDs|LatencyPercentiles)|benchmarkAuthUpgrade|[aA]uthMiddleware|[rR]esolveParameterIDs|[lL]atencyPercentiles|[pP]ercentile'
  for mode in stock aligned; do
    build_arm A "$mode" "$base_source" compile-performance-baseline
    build_arm H "$mode" "$work/H" compile-performance
  done
  for side in A H; do
    if cmp -s "$evidence/stock-$side.symbols" "$evidence/aligned-$side.symbols"; then
      printf 'Aligned link left all recorded hot symbol addresses unchanged for %s\n' "$side" >&2
      return 1
    fi
  done
  for group in auth latency; do
    for mode in stock aligned; do
      comparison "$group" "$mode" A A
      comparison "$group" "$mode" H H
      comparison "$group" "$mode" A H
    done
  done
  test ! -e "$evidence/unstable-control"
}

case "${1:-}" in
  route) route ;;
  measure) measure ;;
  *) printf 'usage: %s route|measure\n' "$0" >&2; exit 2 ;;
esac
