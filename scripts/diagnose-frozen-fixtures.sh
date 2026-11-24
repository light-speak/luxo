#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
source "$script_dir/diagnose-hosted-perf.sh"
suite="${1:?usage: diagnose-frozen-fixtures.sh pr71|pr72|pr72-array-header [--plan-only]}"
mode="${2:-run}"
[[ $# -le 2 && ( "$mode" == run || "$mode" == --plan-only ) ]]
array_candidate=
case "$suite" in
  pr71)
    base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
    head=ec041a07b105c9caf9e327a2cbf73de772582d00
    product=b0b7d9b7c51aa67f14bf8545f05e2339ae26f187
    ;;
  pr72)
    base=a75a0acce23b30e3f99d44b0c47f746901b82bb8
    head=5e4c9d1b3813cc95f98d4088a10c242dca47b8de
    product=4e0c5bc89439be2f1374db2f306ec261bdccc382
    ;;
  pr72-array-header)
    base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
    head=5e4c9d1b3813cc95f98d4088a10c242dca47b8de
    product=a9a9137fdefffe7fecd6f6c81b6367ece9a29022
    array_candidate=b4539db35502ccc1662b40ec75b30a3262e79174
    ;;
  *) exit 2;;
esac

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/frozen-test-fixtures.XXXXXXXX")
evidence="$repo/.tmp/hosted-frozen-fixtures-$suite"
if [[ "$mode" == --plan-only ]]; then evidence="$scratch/evidence"; fi
cleanup_fixture_shadow() {
  local code=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/frozen-test-fixtures.* ]] || exit 99
  rm -rf -- "$scratch"
  exit "$code"
}
trap cleanup_fixture_shadow EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"

python3 - "$suite" "$evidence" <<'PYTHON'
import json
from pathlib import Path
import re
import sys

suite, output = sys.argv[1], Path(sys.argv[2])
if suite == "pr71":
    groups = [
        ("pkg/lux/auth", [f"StaticJWT/Identity/{stage}" for stage in ("Before", "After")]),
        ("pkg/lux/luvia", [f"GatewayIdentity/anonymous/{stage}" for stage in ("before", "after")]),
        ("pkg/lux/api", [f"NullableRootJSON/{kind}/{stage}" for kind in ("int", "zero") for stage in ("plain", "present")]),
    ]
elif suite == "pr72-array-header":
    codec = "pkg/lux/codec"
    groups = [
        (codec, [f"ArrayHeaderWrite/{count}" for count in (0, 128, 1000000, -1)]),
        (codec, [f"ElementPayload/{kind}-nullable/column=true/count=128/version={version}"
                 for kind in ("float", "int") for version in (0, 1)]),
    ]
else:
    codec = "pkg/lux/codec"
    groups = [
        (codec, [f"ArrayHeaderWrite/{count}" for count in (0, 128, 1000000, 1000001, -1)]),
        (codec, [f"ElementPayload/{kind}-nullable/column=true/count={count}/version={version}"
                 for kind, count in (("bool", 0), ("float", 128), ("int", 128)) for version in (0, 1)]),
        (codec, [f"SelectionMaskLimits/empty/{stage}" for stage in ("before", "after")]),
        (codec, [f"WireReadOffsetBounds/{kind}" for kind in ("nullable", "float", "varint-short", "varint-long")]),
    ]
expected, cases = [], []
for index, (package, names) in enumerate(groups, 1):
    names = ["Benchmark" + name for name in names]
    filters = ["/".join("^" + re.escape(part) + "$" for part in name.split("/")) for name in names]
    cases.append(f"{index:02d}\t{package}\tdefault\t{suite}\t" + "|".join(filters))
    expected.extend({"Package": package, "Name": name + "-2"} for name in names)
if len(expected) != (17 if suite == "pr72" else 8):
    raise SystemExit("unexpected benchmark inventory")
(output / "cases.tsv").write_text("\n".join(cases) + "\n")
(output / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
(output / "packages.txt").write_text("\n".join(dict.fromkeys(package for package, _ in groups)) + "\n")
PYTHON

git -C "$repo" cat-file -e "$base^{commit}"
git -C "$repo" cat-file -e "$head^{commit}"
printf 'base=%s\nhead=%s\nproduct_tree=%s\n' "$base" "$head" "$product" > "$evidence/revisions.txt"
prepare_array_candidate() {
  patch="$evidence/array-header.patch"
  base64 -d < "$repo/.github/qualification/pr72-array-header-unroll.patch.b64" > "$patch"
  printf '%s  %s\n' '14abc7638beba745aabf000a8a3717856e7bcb31936d2e31f84341fa0306bd13' "$patch" | sha256sum -c -
  test "$(git -C "$repo" merge-tree --write-tree "$base" "$head")" = "$product"
  (
    export GIT_INDEX_FILE="$scratch/array-header.index"
    git -C "$repo" read-tree "$product"
    git -C "$repo" apply --cached --whitespace=error --check "$patch"
    git -C "$repo" apply --cached --whitespace=error "$patch"
    test "$(git -C "$repo" write-tree)" = "$array_candidate"
    git -C "$repo" apply --cached --reverse --check "$patch"
  )
  test "$(git -C "$repo" diff --name-only "$product" "$array_candidate")" = \
    "$(printf '%s\n' pkg/lux/codec/array_write_test.go pkg/lux/codec/wire.go)"
  git -C "$repo" diff --check "$product" "$array_candidate"
  printf 'candidate_array_header_tree=%s\n' "$array_candidate" >> "$evidence/revisions.txt"
}
if [[ -n "$array_candidate" ]]; then
  prepare_array_candidate
fi
if [[ "$mode" == --plan-only ]]; then
  cat "$evidence/revisions.txt" "$evidence/cases.tsv"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/source/base" "$scratch/source/head" "$scratch/bin/base" "$scratch/bin/head" "$evidence/pairs"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
merge=$(git -C "$repo" merge-tree --write-tree "$base" "$head")
test "$merge" = "$product"
git -C "$repo" archive "$base" | tar -xf - -C "$scratch/source/base"
git -C "$repo" archive "$merge" | tar -xf - -C "$scratch/source/head"
roles=(base head)
if [[ -n "$array_candidate" ]]; then
  mkdir -p "$scratch/source/candidate" "$scratch/bin/candidate"
  git -C "$repo" archive "$array_candidate" | tar -xf - -C "$scratch/source/candidate"
  roles+=(candidate)
fi
for role in "${roles[@]}"; do
  dir="$scratch/source/$role"
  mkdir -p "$dir/.tmp"
  sh "$dir/scripts/setup-modules.sh" "$dir"
  cp "$dir/go.work" "$evidence/go-$role.work.txt"
done

(
  cd "$repo"
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  cd "$scratch/source/base"
  for name in benchplan benchgate; do go build -o "$scratch/bin/$name" "./scripts/$name"; done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/tools.log" 2>&1

while IFS= read -r package; do
  label="${package//\//-}"
  pair="$scratch/pair-$label"
  rc=0
  "$scratch/bin/benchrun" compile-pair "$scratch/source/base" "$scratch/source/head" "$pair" "$package" \
    > "$evidence/pairs/$label.compile.log" 2>&1 || rc=$?
  if [[ -f "$pair/fixtures.json" ]]; then cp "$pair/fixtures.json" "$evidence/pairs/$label.json"; fi
  if (( rc != 0 )); then cat "$evidence/pairs/$label.compile.log" >&2; exit "$rc"; fi
  for role in base head; do
    for binary in "$pair/$role/"*.test; do ln -s "$binary" "$scratch/bin/$role/${binary##*/}"; done
  done
  if [[ -n "$array_candidate" ]]; then
    candidate_pair="$scratch/candidate-$label"
    rc=0
    "$scratch/bin/benchrun" compile-pair "$scratch/source/base" "$scratch/source/candidate" "$candidate_pair" "$package" \
      > "$evidence/pairs/$label.candidate.compile.log" 2>&1 || rc=$?
    if [[ -f "$candidate_pair/fixtures.json" ]]; then cp "$candidate_pair/fixtures.json" "$evidence/pairs/$label.candidate.json"; fi
    if (( rc != 0 )); then cat "$evidence/pairs/$label.candidate.compile.log" >&2; exit "$rc"; fi
    for binary in "$candidate_pair/head/"*.test; do
      cmp "$candidate_pair/base/${binary##*/}" "$scratch/bin/base/${binary##*/}"
      ln -s "$binary" "$scratch/bin/candidate/${binary##*/}"
    done
  fi
done < "$evidence/packages.txt"
for role in "${roles[@]}"; do record_binaries "$role"; done

if [[ -n "$array_candidate" ]]; then
  "$scratch/bin/benchrun" compile "$scratch/source/candidate" "$scratch/bin/candidate-native" pkg/lux/codec
  "$scratch/bin/benchrun" run "$scratch/source/candidate" "$scratch/bin/candidate-native" pkg/lux/codec \
    -test.run='^Test(ArrayHeaderWriteBounds|ArrayHeaderWriteCapacity)$' -test.v -test.timeout=90s \
    > "$evidence/array-header-correctness.log"
  for check in ArrayHeaderWriteBounds ArrayHeaderWriteCapacity; do
    grep -q "^--- PASS: Test$check " "$evidence/array-header-correctness.log"
  done
fi

# The two Planner leaves were introduced by PR 72. Preserve native head-only observations
# separately. They have no matched historical series and are outside the paired verdict.
if [[ "$suite" == pr72 ]]; then
  "$scratch/bin/benchrun" compile "$scratch/source/head" "$scratch/bin/head-native" pkg/lux/luvia \
    > "$evidence/planner-head-compile.log" 2>&1
  "$scratch/bin/benchrun" run "$scratch/source/head" "$scratch/bin/head-native" pkg/lux/luvia \
    -test.run='^$' -test.bench='^BenchmarkPlanSelection$/^(all|nested)$' -test.benchmem \
    -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/planner-head-only.txt"
  test "$(grep -c '^BenchmarkPlanSelection/' "$evidence/planner-head-only.txt")" = 2
fi

comparisons=(aa ab)
if [[ -z "$array_candidate" ]]; then
  sample_groups
else
  comparisons+=(ap)
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    offset=0
    if [[ "$group" == confirmation ]]; then offset=1; fi
    for number in {1..10}; do
      printf -v sample '%02d' "$number"
      if (((number + offset) % 2 == 1)); then first=left; else first=right; fi
      while IFS=$'\t' read -r case_id package variant name filter; do
        for index in 0 1 2; do
          comparison="${comparisons[(index + number + offset + 10#$case_id) % 3]}"
          case "$comparison" in
            aa) other=base;;
            ab) other=head;;
            ap) other=candidate;;
          esac
          run_pair "$group" "$sample" "$comparison" "$other" "$first" "$case_id" "$package" "$variant" "$filter"
        done
      done < "$evidence/cases.tsv"
    done
  done
fi
test "$(( $(wc -l < "$evidence/order.tsv") - 1 ))" = "$(( 40 * ${#comparisons[@]} * $(wc -l < "$evidence/cases.tsv") ))"
printf 'comparison\tgate_exit\n' > "$evidence/verdicts.tsv"
status=0
for comparison in "${comparisons[@]}"; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  rc=0
  "$scratch/bin/benchgate" "$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv" \
    > "$evidence/$comparison.benchgate.log" 2>&1 || rc=$?
  printf '%s\t%s\n' "$comparison" "$rc" >> "$evidence/verdicts.tsv"
  if (( rc != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log" || exit "$rc"
    # For the three-role run, the original head is the comparison control.
    # Only a failed baseline or tested candidate contributes to its verdict.
    if [[ -z "$array_candidate" || "$comparison" != ab ]]; then status=1; fi
  fi
done
cat "$evidence/verdicts.tsv"
exit "$status"
