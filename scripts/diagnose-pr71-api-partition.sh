#!/usr/bin/env bash
set -euo pipefail

mode="${1:-run}"
[[ $# -le 1 ]] || exit 2
case "$mode" in run|--plan-only|--inventory-only) ;; *) exit 2;; esac
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
sampler="$script_dir/diagnose-hosted-perf.sh"
printf '%s  %s\n' 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 "$sampler" | sha256sum -c -
source "$sampler"

base=a79928f9673744f6c7020466272acea2fc4dbb5d
head=ec041a07b105c9caf9e327a2cbf73de772582d00
product=e43de36d2e39eabea59124c5d4d5baa4e4d280d2
package=pkg/lux/api
binary=pkg-lux-api.test
target_cpu='AMD EPYC 7763 64-Core Processor'
base_tree=d17eb6f305a92034f50b8ad174eb61829b100ed1
original_sha=a280083c39ec5b38e6cbf31270c09867071e29da9fb519f1a19294b558dab330
identity_sha=974a52714718340b241af1d9746d48db6e33ec70f4972de278aea37d5cca53ea
complement_sha=02c1401e26206cf3f5c9d69ab50539129bfc5118b1513ffbd00cfd4573891a05
identity_pattern='^BenchmarkIdentity(ConditionValidity|EnumArray|SubjectVisibility)/'
test "$(git -C "$repo" rev-parse "$base^{tree}")" = "$base_tree"
test "$(git -C "$repo" merge-tree --write-tree "$base" "$head")" = "$product"
git -C "$repo" diff --exit-code "$base" "$product" -- "$package" scripts/benchgate scripts/benchplan
reference_base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
reference_tree=b0b7d9b7c51aa67f14bf8545f05e2339ae26f187
test "$(git -C "$repo" rev-parse "$base^1")" = "$reference_base"
test "$(git -C "$repo" merge-tree --write-tree "$reference_base" "$head")" = "$reference_tree"
git -C "$repo" diff --exit-code "$reference_tree" "$product" -- . ':!pkg/lux/api/binary_bench_test.go'

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr71-api-partition.XXXXXXXX")
root="$repo/.tmp/hosted-pr71-api-partition"
if [[ "$mode" != run ]]; then root="$scratch/evidence"; fi
created=false
cleanup_partition() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr71-api-partition.* ]] || exit 99
  if (( status != 0 )) && [[ "$mode" != run && -d "$root" ]]; then
    for log in "$root"/compile-*.log "$root"/inventory-*.raw; do
      if [[ -f "$log" ]]; then tail -n 12 "$log" >&2; fi
    done
  fi
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$root/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_partition EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$root"
created=true
printf 'base=%s\nhead=%s\ntested_tree=%s\noriginal_inventory_run=45334352793\noriginal_same_model_run=45371201347\noriginal_same_model_artifact=14908914130\n' \
  "$base" "$head" "$product" > "$root/revisions.txt"
cp "${BASH_SOURCE[0]}" "$sampler" "$root/"
cat > "$root/signing-fixtures.sha256" <<'PINS'
20d45a1c2a71419d6c2c44e33b49e691cf15f3522f6e1041541dbce5097a3c5f  pkg/lux/api/identity_reader_bench_test.go
f4e765934e382ad361aedac0dea15864efcdeb553ef9e6556adb66b65c0765a2  pkg/lux/api/identity_reader_fuzz_test.go
600116795f36c98c7268237d2df5fefabb9d5708cf25ba9219ea4b1dcf21cf4c  pkg/lux/api/identity_reader_test.go
619664ee4f2a8c715c97bc3a661b9a845d1dbd8411bb575a7ac672ec4ee3d560  pkg/lux/api/identity_value_test.go
PINS

if [[ "$mode" == --plan-only ]]; then
  cat "$root/revisions.txt" "$root/signing-fixtures.sha256"
  printf 'full=1010 ordinary_identity=31 isolated_complement=979 timed_default=13 timed_isolated=11 target_cpu=%s\n' "$target_cpu"
  exit 0
fi
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local LC_ALL=C
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source/base/.tmp" "$scratch/source/head/.tmp"
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
if [[ "$mode" == run ]]; then
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  collect_host > "$root/environment.txt"
fi
git -C "$repo" archive "$base" | tar -xf - -C "$scratch/source/base"
git -C "$repo" archive "$product" | tar -xf - -C "$scratch/source/head"
for role in base head; do
  directory="$scratch/source/$role"
  (
    cd "$directory"
    sha256sum -c "$root/signing-fixtures.sha256"
  ) > "$root/source-$role-check.txt"
  sh "$directory/scripts/setup-modules.sh" "$directory" > "$root/workspace-$role.log"
done
(
  cd "$repo"
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  cd "$scratch/source/base"
  go build -o "$scratch/bin/benchplan" ./scripts/benchplan
  if [[ "$mode" == run ]]; then
    go build -o "$scratch/bin/benchgate" ./scripts/benchgate
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  fi
) > "$root/tools.log" 2>&1
"$scratch/bin/benchrun" compile-pair "$scratch/source/base" "$scratch/source/head" "$scratch/pair" "$package" \
  > "$root/compile-frozen.log" 2>&1
cp "$scratch/pair/fixtures.json" "$root/frozen-pair.json"
jq -e '.Complete and (.Packages | length) == 1' "$root/frozen-pair.json" >/dev/null

for role in base head; do
  directory="$scratch/source/$role"
  original="$scratch/pair/fixtures/$package/$role.json"
  replacement="$scratch/$role-isolated.json"
  python3 - "$original" "$replacement" "$root/signing-fixtures.sha256" "$root/frozen-pair.json" "$directory" <<'PYTHON'
from hashlib import sha256
import json
from pathlib import Path
import sys
original,replacement,pins,manifest,source=map(Path,sys.argv[1:])
plan=json.loads(manifest.read_text())["Packages"][0]["FixtureSHA"]
overlay=json.loads(original.read_text())
for row in pins.read_text().splitlines():
    digest,name=row.split(maxsplit=1)
    assert plan[Path(name).name] == digest
    key=str(source/name)
    old=Path(overlay["Replace"][key])
    assert old.is_file() and sha256(old.read_bytes()).hexdigest()==digest
    overlay["Replace"][key]=""
replacement.write_text(json.dumps(overlay,sort_keys=True,indent=2)+"\n")
PYTHON
  cp "$original" "$root/$role-default.json"
  cp "$replacement" "$root/$role-isolated.json"
  ln -s "$scratch/pair/$role" "$scratch/bin/$role"
  mkdir "$scratch/bin/$role-isolated"
  (
    cd "$directory"
    GOWORK="$PWD/go.work" go test -trimpath -c -overlay="$replacement" "./$package" -o "$scratch/bin/$role-isolated/$binary"
  ) > "$root/compile-$role-isolated.log" 2>&1
done

evidence="$root"
verify_inventory_digest() {
  local file="$1" count="$2" sha="$3"
  test "$(wc -l < "$file")" -eq "$count"
  printf '%s  %s\n' "$sha" "$file" | sha256sum -c -
}
for role in base head; do
  for condition in default isolated; do
    bin_role="$role"
    if [[ "$condition" == isolated ]]; then bin_role="$role-isolated"; fi
    record_binaries "$bin_role"
    go tool nm -size "$scratch/bin/$bin_role/$binary" > "$root/symbols-$role-$condition.nm"
    stem="$root/inventory-$role-$condition"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$bin_role" "$package" \
      -test.run='^$' -test.bench=. -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      -test.timeout=300s > "$stem.raw" 2> "$stem.stderr"
    grep -Fxq PASS "$stem.raw"
    "$scratch/bin/benchplan" --normalize "$stem.raw" "$stem.txt"
    awk '/^Benchmark/ {print $1}' "$stem.txt" | LC_ALL=C sort > "$stem.names"
  done
  ordinary="$root/inventory-$role-default"
  isolated="$root/inventory-$role-isolated"
  verify_inventory_digest "$ordinary.names" 1010 "$original_sha"
  verify_inventory_digest "$isolated.names" 979 "$complement_sha"
  grep -E "$identity_pattern" "$ordinary.names" > "$root/inventory-$role-identity.names"
  verify_inventory_digest "$root/inventory-$role-identity.names" 31 "$identity_sha"
  if grep -Eq "$identity_pattern" "$isolated.names"; then exit 7; fi
  grep -q 'auth.signWithSubject$' "$root/symbols-$role-default.nm"
  if grep -q 'auth.signWithSubject$' "$root/symbols-$role-isolated.nm"; then exit 7; fi
  cat "$root/inventory-$role-identity.names" "$isolated.names" | LC_ALL=C sort > "$root/inventory-$role-partition.names"
  cmp "$ordinary.names" "$root/inventory-$role-partition.names"
done
cmp "$root/inventory-base-partition.names" "$root/inventory-head-partition.names"
printf '%s\n' 'Both 1010-leaf ordinary inventories and both 979-leaf complements match the formal plan. Each original 31-leaf identity subset is retained in the ordinary binary.'
# The timing phase uses only the reproduced API leaves and ordinary identity
# controls. The 31 ordinary identity leaves retain their original signers.
cat > "$root/historical.names" <<'NAMES'
BenchmarkBinaryListPayload/Decimal/count=1/version=0-2
BenchmarkBinaryListPayload/Duration/count=1/version=0-2
BenchmarkModuleColorWarm-2
BenchmarkNullableRootJSON/int/plain-2
BenchmarkNullableRootJSON/int/present-2
BenchmarkNullableRootJSON/zero/plain-2
BenchmarkNullableRootJSON/zero/present-2
BenchmarkRawJSONArrayPool/empty/encode-2
BenchmarkRawJSONArrayPool/precision/encode-2
BenchmarkRawJSONArrayUnicode/empty/strict-2
BenchmarkRegistrySteadyRequest/params=1/json-2
NAMES
cat > "$root/identity-controls.names" <<'NAMES'
BenchmarkIdentityConditionValidity/member/EnumChecked-2
BenchmarkIdentitySubjectVisibility/pointer/subject-2
NAMES
python3 - "$root" "$package" <<'PYTHON_PLAN'
import json
from pathlib import Path
import re
import sys
root, package = Path(sys.argv[1]), sys.argv[2]
historical = (root / "historical.names").read_text().splitlines()
identity = (root / "identity-controls.names").read_text().splitlines()
assert len(set(historical)) == 11 and len(set(identity)) == 2
assert not set(historical) & set(identity)
for condition, wanted in (("default", historical + identity), ("isolated", historical)):
    directory = root / condition
    directory.mkdir()
    for role in ("base", "head"):
        available = (root / f"inventory-{role}-{condition}.names").read_text().splitlines()
        assert set(wanted) <= set(available), (condition, role, set(wanted) - set(available))
    (directory / "names.txt").write_text("\n".join(wanted) + "\n")
    expected = [{"Package": package, "Name": name} for name in wanted]
    (directory / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
    patterns = ["/".join("^" + re.escape(segment) + "$" for segment in name.removesuffix("-2").split("/"))
                for name in wanted]
    (directory / "filter.txt").write_text("|".join(patterns) + "\n")
PYTHON_PLAN
for role in base head; do
  for condition in default isolated; do
    bin_role="$role"
    if [[ "$condition" == isolated ]]; then bin_role="$role-isolated"; fi
    selected="$root/$condition/selected-$role.raw"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$bin_role" "$package" \
      -test.run='^$' -test.bench="$(cat "$root/$condition/filter.txt")" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      -test.timeout=90s > "$selected"
    grep -Fxq PASS "$selected"
    awk '/^Benchmark/ {if ($2 != 1) exit 1; print $1}' "$selected" | LC_ALL=C sort > "$selected.names"
    LC_ALL=C sort "$root/$condition/names.txt" > "$root/$condition/expected.names"
    cmp "$root/$condition/expected.names" "$selected.names"
  done
done
printf '%s\n' 'The narrow filters run 13 ordinary leaves and 11 isolated leaves on each role.'
if [[ "$mode" == --inventory-only ]]; then exit 0; fi

for condition in default isolated; do
  mkdir "$root/$condition/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$root/$condition/order.tsv"
done
printf 'group\tpair\tcomparison\tcondition\n' > "$root/condition-order.tsv"
offset=0
for group in primary confirmation; do
  for number in {1..10}; do
    printf -v sample '%02d' "$number"
    if (( (number + offset) % 2 == 1 )); then
      first=left
      comparisons=(ab aa)
      conditions=(default isolated)
    else
      first=right
      comparisons=(aa ab)
      conditions=(isolated default)
    fi
    for comparison in "${comparisons[@]}"; do
      other=base
      if [[ "$comparison" == ab ]]; then other=head; fi
      for condition in "${conditions[@]}"; do
        evidence="$root/$condition"
        printf '%s\t%s\t%s\t%s\n' "$group" "$sample" "$comparison" "$condition" >> "$root/condition-order.tsv"
        run_pair "$group" "$sample" "$comparison" "$other" "$first" 01 "$package" "$condition" "$(cat "$evidence/filter.txt")"
      done
    done
  done
  offset=1
done

python3 - "$root" "$target_cpu" <<'PYTHON_AUDIT'
from collections import Counter
import csv
import json
from pathlib import Path
import sys

root, target = Path(sys.argv[1]), sys.argv[2]
models, expected_conditions = set(), []
expected_legs = {"default": [], "isolated": []}
for offset, group in enumerate(("primary", "confirmation")):
    for pair in range(1, 11):
        odd = (pair + offset) % 2 == 1
        comparisons = ("ab", "aa") if odd else ("aa", "ab")
        conditions = ("default", "isolated") if odd else ("isolated", "default")
        sides = ("left", "right") if odd else ("right", "left")
        sample = f"{pair:02d}"
        for comparison in comparisons:
            for condition in conditions:
                expected_conditions.append((group, sample, comparison, condition))
                for side in sides:
                    role = "head" if comparison == "ab" and side == "right" else "base"
                    raw = f"raw/{group}.{sample}.{comparison}.01.{side}.txt"
                    expected_legs[condition].append((group, sample, comparison, "01", side, role, condition, raw))
with (root / "condition-order.tsv").open() as stream:
    header, *observed = csv.reader(stream, delimiter="\t")
assert header == ["group", "pair", "comparison", "condition"]
assert [tuple(row) for row in observed] == expected_conditions
for condition, expected in expected_legs.items():
    current = root / condition
    wanted = Counter((current / "names.txt").read_text().splitlines())
    with (current / "order.tsv").open() as stream:
        header, *observed = csv.reader(stream, delimiter="\t")
    assert header == ["group", "pair", "comparison", "case", "side", "revision", "variant", "raw"]
    assert [tuple(row) for row in observed] == expected
    assert {str(p.relative_to(current)) for p in (current / "raw").glob("*.txt")} == {r[-1] for r in expected}
    for row in expected:
        lines = (current / row[-1]).read_text().splitlines()
        assert lines[:3] == ["goos: linux", "goarch: amd64", "pkg: github.com/light-speak/luxo/pkg/lux/api"]
        assert lines[3].startswith("cpu: ") and lines[-1] == "PASS"
        models.add(lines[3][5:].strip())
        measures = [item.split() for item in lines[4:-1]]
        assert Counter(item[0] for item in measures) == wanted
        assert all(int(item[1]) > 0 and item[3::2] == ["ns/op", "B/op", "allocs/op"] for item in measures)
inventories = sorted(root.glob("inventory-*.txt"))
assert len(inventories) == 4
for path in inventories:
    cpu = {line[5:].strip() for line in path.read_text().splitlines() if line.startswith("cpu: ")}
    assert len(cpu) == 1
    models.update(cpu)
host = {line.split(":", 1)[1].strip() for line in (root / "environment.txt").read_text().splitlines()
        if line.startswith("Model name:")}
assert len(models) == 1 and models == host
(root / "runner-cpu.json").write_text(json.dumps({
    "expected": target, "observed": next(iter(models)), "matchesRequestedModel": models == {target},
}, indent=2) + "\n")
PYTHON_AUDIT

for condition in default isolated; do
  evidence="$root/$condition"
  analyze_groups
done
python3 - "$root" <<'PYTHON_RESULT'
import json
from pathlib import Path
import re
import sys
root = Path(sys.argv[1])
pattern = re.compile(r"^confirmed performance regression: github\.com/light-speak/luxo/pkg/lux/api/(.+)-2 (sec/op|B/op|allocs/op) ")
historical = {name.removeprefix("Benchmark").removesuffix("-2")
              for name in (root / "historical.names").read_text().splitlines()}
def rejected(condition):
    return {m.groups() for line in (root / condition / "ab.benchgate.log").read_text().splitlines()
            if (m := pattern.match(line)) is not None}
default = rejected("default")
isolated = rejected("isolated")
reproduced = {name for name, unit in default if name in historical and unit == "sec/op"}
retained = {name for name, unit in isolated if name in reproduced and unit == "sec/op"}
summary = {
    "historicalLeafCount": len(historical),
    "defaultRejects": sorted(default),
    "defaultTimingReproduced": sorted(reproduced),
    "notReproduced": sorted(historical - reproduced),
    "stillRejectedForTimeWhenIsolated": sorted(retained),
    "noLongerRejectedForTimeWhenIsolated": sorted(reproduced - retained),
    "ordinaryIdentityControlRejects": sorted((name, unit) for name, unit in default if name not in historical),
    "isolatedRejects": sorted(isolated),
    "scope": "Linkage experiment only. The original performance gate and ordinary correctness suites remain authoritative.",
}
(root / "diagnosis.json").write_text(json.dumps(summary, indent=2) + "\n")
PYTHON_RESULT
cat "$root/runner-cpu.json" "$root/diagnosis.json"
for condition in default isolated; do
  grep -Fxq $'aa\t0' "$root/$condition/verdicts.tsv"
done
if ! jq -e .matchesRequestedModel "$root/runner-cpu.json" >/dev/null; then
  printf '%s\n' 'Runner model did not match. Both link conditions are retained as observations.'
  exit 3
fi
if ! jq -e '.defaultTimingReproduced | length > 0' "$root/diagnosis.json" >/dev/null; then
  printf '%s\n' 'The current default build did not reproduce an original timing signal. Attribution is inconclusive.'
  exit 4
fi
if ! jq -e '.ordinaryIdentityControlRejects | length == 0' "$root/diagnosis.json" >/dev/null; then
  printf '%s\n' 'An ordinary identity control crossed the original threshold.'
  exit 1
fi
grep -Fxq $'ab\t0' "$root/isolated/verdicts.tsv"
