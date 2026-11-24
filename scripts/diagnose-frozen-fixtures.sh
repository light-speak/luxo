#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
source "$script_dir/diagnose-hosted-perf.sh"
suite="${1:?usage: diagnose-frozen-fixtures.sh pr71|pr72|pr72-array-header [mode]}"
mode="${2:-run}"
[[ $# -le 2 ]] || exit 2
case "$mode" in
  run|--plan-only) ;;
  --layout-only|--compare-alignment|--compare-split|--check-split-inventory) [[ "$suite" == pr72-array-header ]] || exit 2;;
  --compare-reader-capsule|--check-reader-capsule) [[ "$suite" == pr72-array-header ]] || exit 2;;
  *) exit 2;;
esac
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
if [[ "$mode" == --layout-only ]]; then evidence="$repo/.tmp/hosted-pr72-reference-layout"; fi
if [[ "$mode" == --compare-alignment ]]; then evidence="$repo/.tmp/hosted-pr72-reference-alignment"; fi
if [[ "$mode" == --compare-split ]]; then evidence="$repo/.tmp/hosted-pr72-reference-split"; fi
if [[ "$mode" == --check-split-inventory ]]; then evidence="$repo/.tmp/hosted-pr72-reference-split-inventory"; fi
if [[ "$mode" == --compare-reader-capsule ]]; then evidence="$repo/.tmp/hosted-pr72-reference-reader"; fi
if [[ "$mode" == --check-reader-capsule ]]; then evidence="$repo/.tmp/hosted-pr72-reference-reader-inventory"; fi
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

python3 - "$suite" "$evidence" "$mode" <<'PYTHON'
import json
from pathlib import Path
import re
import sys

suite, output = sys.argv[1], Path(sys.argv[2])
reader_only = sys.argv[3] in ("--compare-reader-capsule", "--check-reader-capsule")
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
    if reader_only:
        groups = groups[1:]
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
expected_count = 4 if reader_only else (17 if suite == "pr72" else 8)
if len(expected) != expected_count:
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
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
if [[ "$mode" == --check-split-inventory || "$mode" == --check-reader-capsule ]]; then
  test "$(go env GOVERSION)" = go1.27.1
  {
    uname -s -m
    go env -json GOVERSION GOOS GOARCH CGO_ENABLED GOAMD64 GOARM64 GOFLAGS GOEXPERIMENT
  } > "$evidence/environment.txt"
else
  [[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  collect_host > "$evidence/environment.txt"
fi
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
  if [[ "$mode" != --layout-only && "$mode" != --check-split-inventory && "$mode" != --check-reader-capsule ]]; then
    cd "$scratch/source/base"
    for name in benchplan benchgate; do go build -o "$scratch/bin/$name" "./scripts/$name"; done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  fi
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

if [[ "$mode" == --layout-only ]]; then
  python3 "$script_dir/diagnose-pr72-reference-layout.py" "$evidence" "$scratch/bin"
  exit 0
fi

if [[ "$mode" == --compare-alignment ]]; then
  python3 "$script_dir/diagnose-pr72-reference-layout.py" "$evidence" "$scratch/bin"
  cat > "$evidence/alignment-protocol.txt" <<'PROTOCOL'
Both configurations run two groups of ten interleaved pairs on one runner.
The default binaries must match the prior measurement hashes.
The compared pair retains identical fixtures, sources, build environment and
eight leaf names across configurations. The aligned builds add only
-ldflags=-funcalign=64. The flag changes layout across the linked executable.
A/A and A/P are evaluated separately with the original thresholds for each
configuration. This experiment leaves the official performance gate in place.
PROTOCOL
  for role in base candidate; do
    pair="$scratch/candidate-pkg-lux-codec"
    side=base
    if [[ "$role" == candidate ]]; then side=head; fi
    overlay="$pair/fixtures/pkg/lux/codec/$side.json"
    test -f "$overlay"
    mkdir "$scratch/bin/$role-align64"
    (
      cd "$scratch/source/$role"
      export GOWORK="$PWD/go.work"
      set -x
      sha256sum "$overlay"
      go test -trimpath -c "-overlay=$overlay" -ldflags=-funcalign=64 ./pkg/lux/codec \
        -o "$scratch/bin/$role-align64/pkg-lux-codec.test"
      sha256sum "$overlay"
    ) > "$evidence/compile-$role-align64.log" 2>&1
    record_binaries "$role-align64"
    go tool nm -size "$scratch/bin/$role-align64/pkg-lux-codec.test" > "$evidence/$role-align64.nm.txt"
  done
  python3 - "$evidence" <<'PYTHON'
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
suffixes = (".readColumnIntPtrBeforePayload", ".readColumnFloatPtrBeforePayload", ".benchmarkPayloadRead")
layout = ["role\tsymbol\taddress\tsize"]
for role in ("base", "candidate"):
    seen = {}
    for line in (evidence / f"{role}-align64.nm.txt").read_text().splitlines():
        fields = line.split(maxsplit=3)
        if len(fields) == 4 and fields[2] == "T" and fields[3].endswith(suffixes):
            address, size, _, name = fields
            if name in seen or int(address, 16) % 64 != 0:
                raise SystemExit(f"{role} unexpected aligned layout for {name}: {address}")
            seen[name] = address, size
    for suffix in suffixes:
        if sum(name.endswith(suffix) for name in seen) != 1:
            raise SystemExit(f"{role} missing or ambiguous aligned symbol: {suffix}")
    layout.extend(f"{role}\t{name}\t{address}\t{size}" for name, (address, size) in seen.items())
(evidence / "aligned-hot-symbols.tsv").write_text("\n".join(layout) + "\n")
PYTHON
fi

if [[ -n "$array_candidate" ]]; then
  "$scratch/bin/benchrun" compile "$scratch/source/candidate" "$scratch/bin/candidate-native" pkg/lux/codec
  "$scratch/bin/benchrun" run "$scratch/source/candidate" "$scratch/bin/candidate-native" pkg/lux/codec \
    -test.run='^Test(ArrayHeaderWriteBounds|ArrayHeaderWriteCapacity)$' -test.v -test.timeout=90s \
    > "$evidence/array-header-correctness.log"
  for check in ArrayHeaderWriteBounds ArrayHeaderWriteCapacity; do
    grep -q "^--- PASS: Test$check " "$evidence/array-header-correctness.log"
  done
fi

record_split_profile() {
  local role="$1" variant="$2" overlay="$3" bin_path="$scratch/bin/$1"
  if [[ "$variant" != default ]]; then bin_path+="-$variant"; fi
  (
    cd "$scratch/source/$role"
    GOWORK="$PWD/go.work" go list -json "-overlay=$overlay" ./pkg/lux/codec
  ) > "$evidence/split-files-$role-$variant.json"
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$bin_path" pkg/lux/codec \
    -test.list '^Benchmark' > "$evidence/split-roots-$role-$variant.txt"
}

record_split_inventory() {
  local role="$1" case_id variant filter package unused split bin_path
  while IFS=$'\t' read -r case_id package variant unused filter; do
    split="$(split_variant "$case_id")"
    for variant in default "$split"; do
      bin_path="$scratch/bin/$role"
      if [[ "$variant" != default ]]; then bin_path+="-$variant"; fi
      "$scratch/bin/benchrun" run "$scratch/source/$role" "$bin_path" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem \
        -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s \
        > "$evidence/split-inventory-$role-$variant-$case_id.txt"
    done
  done < "$evidence/cases.tsv"
}

split_variant() {
  case "$1" in
    01) printf '%s\n' writer;;
    02) printf '%s\n' reader;;
    *) return 2;;
  esac
}

prepare_codec_split() {
  local role side overlay variant profile split_plan
  for role in base candidate; do
    side=base
    if [[ "$role" == candidate ]]; then side=head; fi
    overlay="$scratch/candidate-pkg-lux-codec/fixtures/pkg/lux/codec/$side.json"
    split_plan="$scratch/split-plan-$role.tsv"
    python3 "$script_dir/diagnose-pr72-codec-split.py" make-overlays \
      "$scratch/source/$role" "$overlay" "$role" "$evidence" > "$split_plan"
    record_split_profile "$role" default "$overlay"
    while IFS=$'\t' read -r variant profile; do
      mkdir "$scratch/bin/$role-$variant"
      (
        cd "$scratch/source/$role"
        export GOWORK="$PWD/go.work"
        set -x
        go test -trimpath -c "-overlay=$profile" ./pkg/lux/codec \
          -o "$scratch/bin/$role-$variant/pkg-lux-codec.test"
      ) > "$evidence/compile-$role-$variant.log" 2>&1
      record_binaries "$role-$variant"
      record_split_profile "$role" "$variant" "$profile"
    done < "$split_plan"
    record_split_inventory "$role"
  done
  python3 "$script_dir/diagnose-pr72-codec-split.py" verify-inventory "$evidence"
}

if [[ "$mode" == --compare-split || "$mode" == --check-split-inventory ]]; then
  if [[ "$mode" == --compare-split ]]; then
    python3 "$script_dir/diagnose-pr72-reference-layout.py" "$evidence" "$scratch/bin"
  fi
  prepare_codec_split
  if [[ "$mode" == --check-split-inventory ]]; then exit 0; fi
fi

prepare_reader_capsule() {
  local role side frozen overlay source profile binary_dir config
  local helper="$script_dir/diagnose-pr72-reader-capsule.py"
  local case_id package unused label filter
  IFS=$'\t' read -r case_id package unused label filter < "$evidence/cases.tsv"
  test "$case_id" = 01
  test "$package" = pkg/lux/codec
  for role in base candidate; do
    source="$scratch/source/$role"
    side=base
    if [[ "$role" == candidate ]]; then side=head; fi
    frozen="$scratch/candidate-pkg-lux-codec/fixtures/pkg/lux/codec/$side.json"
    overlay="$scratch/reader-$role/overlay.json"
    python3 "$helper" make-overlay "$source/$package" "$frozen" "$overlay" "$evidence/reader-fixtures-$role.json"
    binary_dir="$scratch/bin/$role-capsule"
    mkdir "$binary_dir"
    (
      cd "$source"
      export GOWORK="$PWD/go.work"
      go test -trimpath -c "-overlay=$overlay" ./pkg/lux/codec -o "$binary_dir/pkg-lux-codec.test"
    ) > "$evidence/compile-$role-capsule.log" 2>&1
    record_binaries "$role-capsule"
    "$scratch/bin/benchrun" run "$source" "$binary_dir" "$package" -test.list '.*' > "$evidence/reader-roots-$role.txt"
    "$scratch/bin/benchrun" run "$source" "$binary_dir" "$package" \
      -test.run='^$' -test.bench='^BenchmarkElementPayload$' -test.benchmem \
      -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/reader-all-$role.txt"
    for profile in default capsule; do
      config="$frozen"
      binary_dir="$scratch/bin/$role"
      if [[ "$profile" == capsule ]]; then config="$overlay"; binary_dir+="-capsule"; fi
      (
        cd "$source"
        GOWORK="$PWD/go.work" go list -json "-overlay=$config" ./pkg/lux/codec
      ) > "$evidence/reader-files-$role-$profile.json"
      "$scratch/bin/benchrun" run "$source" "$binary_dir" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem \
        -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/reader-targets-$role-$profile.txt"
    done
  done
  python3 "$helper" verify-inventory "$evidence"
}

if [[ "$mode" == --compare-reader-capsule || "$mode" == --check-reader-capsule ]]; then
  if [[ "$mode" == --compare-reader-capsule ]]; then
    python3 "$script_dir/diagnose-pr72-reference-layout.py" "$evidence" "$scratch/bin"
  fi
  prepare_reader_capsule
  if [[ "$mode" == --check-reader-capsule ]]; then exit 0; fi
  python3 "$script_dir/diagnose-pr72-reader-capsule.py" verify-closures "$evidence" "$scratch/bin"
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
  if [[ "$mode" == --compare-alignment ]]; then comparisons=(aa ap aligned-aa aligned-ap); fi
  if [[ "$mode" == --compare-split ]]; then comparisons=(aa ap split-aa split-ap); fi
  if [[ "$mode" == --compare-reader-capsule ]]; then comparisons=(aa ap capsule-aa capsule-ap); fi
  comparison_count="${#comparisons[@]}"
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    offset=0
    if [[ "$group" == confirmation ]]; then offset=1; fi
    for number in {1..10}; do
      printf -v sample '%02d' "$number"
      if (((number + offset) % 2 == 1)); then first=left; else first=right; fi
      while IFS=$'\t' read -r case_id package variant name filter; do
        for ((index=0; index<comparison_count; index++)); do
          comparison="${comparisons[(index + number + offset + 10#$case_id) % comparison_count]}"
          link_variant="$variant"
          case "$comparison" in
            aa) other=base;;
            ab) other=head;;
            ap) other=candidate;;
            aligned-aa) other=base; link_variant=align64;;
            aligned-ap) other=candidate; link_variant=align64;;
            split-aa) other=base; link_variant="$(split_variant "$case_id")";;
            split-ap) other=candidate; link_variant="$(split_variant "$case_id")";;
            capsule-aa) other=base; link_variant=capsule;;
            capsule-ap) other=candidate; link_variant=capsule;;
          esac
          run_pair "$group" "$sample" "$comparison" "$other" "$first" "$case_id" "$package" "$link_variant" "$filter"
        done
      done < "$evidence/cases.tsv"
    done
  done
fi
test "$(( $(wc -l < "$evidence/order.tsv") - 1 ))" = "$(( 40 * ${#comparisons[@]} * $(wc -l < "$evidence/cases.tsv") ))"
if [[ "$mode" == --compare-split ]]; then
  python3 "$script_dir/diagnose-pr72-codec-split.py" verify-samples "$evidence"
fi
if [[ "$mode" == --compare-reader-capsule ]]; then
  python3 "$script_dir/diagnose-pr72-reader-capsule.py" verify-samples "$evidence"
fi
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
    if [[ "$mode" == --compare-alignment ]]; then
      if [[ "$comparison" == aa || "$comparison" == aligned-aa ]]; then status=1; fi
    elif [[ "$mode" == --compare-split || "$mode" == --compare-reader-capsule ]]; then
      if [[ "$comparison" != ap ]]; then status=1; fi
    elif [[ -z "$array_candidate" || "$comparison" != ab ]]; then
      status=1
    fi
  fi
done
cat "$evidence/verdicts.tsv"
if [[ "$mode" == --compare-alignment ]]; then
  for kind in int float; do
    leaf="github.com/light-speak/luxo/pkg/lux/codec/ElementPayload/$kind-nullable/column=true/count=128/version=0-2"
    if ! grep -Fq "confirmed performance regression: $leaf sec/op " "$evidence/ap.benchgate.log"; then
      printf 'Alignment comparison inconclusive: default comparison did not reproduce %s\n' "$leaf" >&2
      status=1
    fi
  done
fi
if [[ "$mode" == --compare-split || "$mode" == --compare-reader-capsule ]]; then
  for kind in int float; do
    leaf="github.com/light-speak/luxo/pkg/lux/codec/ElementPayload/$kind-nullable/column=true/count=128/version=0-2"
    if ! grep -Fq "confirmed performance regression: $leaf sec/op " "$evidence/ap.benchgate.log"; then
      printf 'Fixture comparison inconclusive: default comparison did not reproduce %s\n' "$leaf" >&2
      status=1
    fi
  done
  if [[ "$(grep -c '^confirmed performance regression: ' "$evidence/ap.benchgate.log")" != 2 ]]; then
    printf 'Fixture comparison inconclusive: expected only the two historical V0 timing reports\n' >&2
    status=1
  fi
fi
if [[ "$mode" == --compare-reader-capsule ]]; then
  cat "$evidence/reader-scope.json"
  if ! grep -q '"exactOriginalModel": true' "$evidence/reader-scope.json" && (( status == 0 )); then
    printf '%s\n' 'Different runner model, measurements retained as observations.' >&2
    status=3
  fi
fi
exit "$status"
