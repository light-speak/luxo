#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
source "$script_dir/diagnose-hosted-perf.sh"
suite="${1:?usage: diagnose-frozen-fixtures.sh pr71|pr72|pr72-array-header|pr74-current [--plan-only]}"
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
  pr74-current)
    base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
    head=f33b2f2a37044555807b53e9998f7df0c5315e20
    product=f83536bbde073df4921b328787615901b38b0b24
    ;;
  pr72-array-header)
    base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
    head=5e4c9d1b3813cc95f98d4088a10c242dca47b8de
    product=a9a9137fdefffe7fecd6f6c81b6367ece9a29022
    array_candidate=362bee3437171d57167a71863de15a10a409a203
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
elif suite == "pr74-current":
    api = ['CanonicalJSONFloat/0/after', 'CanonicalJSONFloat/128/after', 'CanonicalJSONFloat/16/after', 'RawJSONArrayPool/list128/encode', 'RawJSONArrayPool/object/encode', 'RawJSONArrayPool/precision/encode']
    luvia = ['GatewayIdentity/anonymous/before', 'PrimaryKeys/column-small/keyLast=true/baseline=false', 'PrimaryKeys/column-small/keyLast=true/baseline=true']
    # Each after branch compares output to the sibling produced by before.
    api.extend(f"CanonicalJSONFloat/{count}/before" for count in (0, 16, 128))
    # Adjacent context and the six request profiles that call the new extractor.
    luvia.append("GatewayIdentity/anonymous/after")
    luvia.extend(f"AuthUpgradeHeaders/{kind}/{stage}"
                 for kind in ("anonymous-http", "bearer-http", "anonymous-get", "bearer-get", "bearer-ws", "query-ws")
                 for stage in ("before", "current"))
    groups = [("pkg/lux/api", api), ("pkg/lux/luvia", luvia)]
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
if len(expected) != {"pr71": 8, "pr72": 17, "pr72-array-header": 8, "pr74-current": 25}[suite]:
    raise SystemExit("unexpected benchmark inventory")
(output / "cases.tsv").write_text("\n".join(cases) + "\n")
(output / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
(output / "packages.txt").write_text("\n".join(dict.fromkeys(package for package, _ in groups)) + "\n")
PYTHON

git -C "$repo" cat-file -e "$base^{commit}"
git -C "$repo" cat-file -e "$head^{commit}"
printf 'base=%s\nhead=%s\nproduct_tree=%s\n' "$base" "$head" "$product" > "$evidence/revisions.txt"
if [[ -n "$array_candidate" ]]; then
  printf 'restored_array_header_tree=%s\n' "$array_candidate" >> "$evidence/revisions.txt"
fi
if [[ "$suite" == pr74-current ]]; then
  test "$(git -C "$repo" rev-list --parents -n 1 "$head")" = "$head 538dadd85ba2180505b4bb4a5a37ce66be9e8b10 $base"
  test "$(git -C "$repo" rev-parse "$head^{tree}")" = "$product"
  printf '%s\n' 'original_run=45343471370/1' 'original_receipt_sha256=05cc91346293854f73558a20ef181198e2976d4482e5d5d6ce3e1a11c885eefe' \
    'official_targets=9' 'control_targets=16' >> "$evidence/revisions.txt"
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
  array_source=pkg/lux/codec/wire.go
  (
    export GIT_INDEX_FILE="$scratch/array-header.index"
    git -C "$repo" read-tree "$merge"
    wire_blob=$(git -C "$repo" rev-parse "$base:$array_source")
    git -C "$repo" update-index --cacheinfo "100644,$wire_blob,$array_source"
    test "$(git -C "$repo" write-tree)" = "$array_candidate"
  )
  test "$(git -C "$repo" diff --name-only "$merge" "$array_candidate")" = "$array_source"
  git -C "$repo" diff --binary "$merge" "$array_candidate" > "$evidence/array-header.patch"
  mkdir -p "$scratch/source/restored" "$scratch/bin/restored"
  git -C "$repo" archive "$array_candidate" | tar -xf - -C "$scratch/source/restored"
  roles+=(restored)
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
    restored_pair="$scratch/restored-$label"
    rc=0
    "$scratch/bin/benchrun" compile-pair "$scratch/source/base" "$scratch/source/restored" "$restored_pair" "$package" \
      > "$evidence/pairs/$label.restored.compile.log" 2>&1 || rc=$?
    if [[ -f "$restored_pair/fixtures.json" ]]; then cp "$restored_pair/fixtures.json" "$evidence/pairs/$label.restored.json"; fi
    if (( rc != 0 )); then cat "$evidence/pairs/$label.restored.compile.log" >&2; exit "$rc"; fi
    for binary in "$restored_pair/head/"*.test; do
      cmp "$restored_pair/base/${binary##*/}" "$scratch/bin/base/${binary##*/}"
      ln -s "$binary" "$scratch/bin/restored/${binary##*/}"
    done
  fi
done < "$evidence/packages.txt"
for role in "${roles[@]}"; do record_binaries "$role"; done

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
  comparisons+=(ar)
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
            ar) other=restored;;
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
    status=1
  fi
done
cat "$evidence/verdicts.tsv"
exit "$status"
