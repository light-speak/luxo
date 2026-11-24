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

base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
head=ec041a07b105c9caf9e327a2cbf73de772582d00
product=b0b7d9b7c51aa67f14bf8545f05e2339ae26f187
package=pkg/lux/rpc
fixture="$package/dataloader_scope_test.go"
fixture_sha=703f63a439f3c49804857a7abd26aa14a9d32b67f15777f941d85dc173cd2006
inventory_sha=d007176ac7a25fe9a876d86ac94900318597f882c3fb56e071c057e41d6b6e9e
target_cpu='AMD EPYC 7763 64-Core Processor'
binary=pkg-lux-rpc.test
test "$(git -C "$repo" merge-tree --write-tree "$base" "$head")" = "$product"
git -C "$repo" diff --exit-code "$base" "$product" -- "$package" scripts/benchgate scripts/benchplan

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr71-rpc-auth-link.XXXXXXXX")
root="$repo/.tmp/hosted-pr71-rpc-auth-link"
if [[ "$mode" != run ]]; then root="$scratch/evidence"; fi
created=false
cleanup_link() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr71-rpc-auth-link.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$root/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_link EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$root"
created=true
printf 'base=%s\nhead=%s\ntested_tree=%s\nreference_run=45371201347\nreference_artifact=14908914130\nofficial_inventory_run=45334352793\n' \
  "$base" "$head" "$product" > "$root/revisions.txt"
cp "${BASH_SOURCE[0]}" "$sampler" "$root/"
cat > "$root/protocol.txt" <<'PROTOCOL'
Default executables are the original frozen pair. Each isolated executable
uses the same overlay except for one deleted correctness-only test fixture.
Ordinary correctness, coverage, source files, workload selection and the
formal performance workflow remain separate from this diagnostic.
The historical plan fixes all 214 names in each of the four 1x inventories.
Only the two confirmed RPC leaves and two unchanged sibling controls are
timed. Both linkage conditions share the same runner and two groups of ten
alternating A/A and A/H pairs. The original gate and base-bound approvals
apply to each condition separately.
PROTOCOL
targets=(
  BenchmarkLRPCPoolLimits/serial/before
  BenchmarkLRPCPoolLimits/serial/after
  BenchmarkRPCHandlerCompletion/unobserved/success
  BenchmarkRPCHandlerCompletion/unobserved/error
)
filter='^BenchmarkLRPCPoolLimits$/^serial$/^(before|after)$|^BenchmarkRPCHandlerCompletion$/^unobserved$/^(success|error)$'
printf '%s-2\n' "${targets[@]}" | LC_ALL=C sort > "$root/targets.txt"
jq -Rn --arg package "$package" '[inputs | {Package:$package,Name:.}] | {Base:.,Head:.}' \
  < "$root/targets.txt" > "$root/expected.json"
if [[ "$mode" == --plan-only ]]; then
  cat "$root/revisions.txt" "$root/targets.txt"
  printf 'full_inventory=214 timed_leaves=4 process_arms=160 target_cpu=%s\n' "$target_cpu"
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
  printf '%s  %s\n' "$fixture_sha" "$directory/$fixture" | sha256sum -c -
  test "$(grep -c '^func TestLRPCBackgroundLoadsKeepVerifiedTenant(' "$directory/$fixture")" = 1
  if grep -Eq '^func (Benchmark|Fuzz|TestMain|init)' "$directory/$fixture"; then exit 6; fi
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
jq -e --arg name "${fixture##*/}" --arg sha "$fixture_sha" \
  '.Complete and (.Packages | length) == 1 and .Packages[0].FixtureSHA[$name] == $sha' "$root/frozen-pair.json" >/dev/null

for role in base head; do
  directory="$scratch/source/$role"
  original="$scratch/pair/fixtures/$package/$role.json"
  replacement="$scratch/$role-isolated.json"
  jq -e --arg f "$directory/$fixture" '.Replace[$f] != null and .Replace[$f] != ""' "$original" >/dev/null
  jq --arg f "$directory/$fixture" '.Replace[$f] = ""' "$original" > "$replacement"
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
reference="$root/inventory-base-default.names"
for role in base head; do
  for condition in default isolated; do
    bin_role="$role"
    if [[ "$condition" == isolated ]]; then bin_role="$role-isolated"; fi
    record_binaries "$bin_role"
    go tool nm -size "$scratch/bin/$bin_role/$binary" > "$root/symbols-$role-$condition.nm"
    grep -q 'rpc.BenchmarkLRPCPoolLimits$' "$root/symbols-$role-$condition.nm"
    grep -q 'rpc.BenchmarkRPCHandlerCompletion$' "$root/symbols-$role-$condition.nm"
    if [[ "$condition" == default ]]; then
      grep -q 'auth.signWithSubject$' "$root/symbols-$role-$condition.nm"
      grep -q 'rpc_test.TestLRPCBackgroundLoadsKeepVerifiedTenant$' "$root/symbols-$role-$condition.nm"
    elif grep -Eq '(auth.signWithSubject|rpc_test.TestLRPCBackgroundLoadsKeepVerifiedTenant)$' "$root/symbols-$role-$condition.nm"; then
      exit 7
    fi
    stem="$root/inventory-$role-$condition"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$bin_role" "$package" \
      -test.run='^$' -test.bench=. -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      -test.timeout=180s > "$stem.raw"
    grep -Fxq PASS "$stem.raw"
    "$scratch/bin/benchplan" --normalize "$stem.raw" "$stem.txt"
    awk '/^Benchmark/ {print $1}' "$stem.txt" | LC_ALL=C sort > "$stem.names"
    test "$(wc -l < "$stem.names")" -eq 214
    cmp "$reference" "$stem.names"
    printf '%s  %s\n' "$inventory_sha" "$stem.names" | sha256sum -c -
  done
done

printf 'role\treference_sha256\tcurrent_sha256\n' > "$root/default-fingerprints.tsv"
printf 'base\t%s\t%s\n' d6b4f57d188ba0cb21310a0f6b68090760d001d18f66263767af3d2ad5228fdd \
  "$(awk 'NR == 1 {print $1}' "$root/binaries-base.txt")" >> "$root/default-fingerprints.tsv"
printf 'head\t%s\t%s\n' b100494b81807e6bd78e6e9f9f4cc044e32bebe91875990585f36ab1545c0772 \
  "$(awk 'NR == 1 {print $1}' "$root/binaries-head.txt")" >> "$root/default-fingerprints.tsv"

if [[ "$mode" == --inventory-only ]]; then
  printf '%s\n' 'Four complete 214-leaf inventories match the original plan; signer only appears in both default executables.'
  exit 0
fi

# Place each consecutive pair entirely in one linkage condition, interleaving
# the two conditions while preserving the shared sampler's order within each.
for condition in default isolated; do
  mkdir -p "$root/$condition/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$root/$condition/order.tsv"
  cp "$root/expected.json" "$root/$condition/expected.json"
done
printf 'group\tpair\tcomparison\tcondition\n' > "$root/condition-order.tsv"
offset=0
for group in primary confirmation; do
  for number in {1..10}; do
    printf -v sample '%02d' "$number"
    if (( (number + offset) % 2 == 1 )); then
      first=left
      comparisons=(ab aa)
    else
      first=right
      comparisons=(aa ab)
    fi
    for comparison in "${comparisons[@]}"; do
      other=base
      if [[ "$comparison" == ab ]]; then other=head; fi
      conditions=(default isolated)
      if (( (number + offset) % 2 == 0 )); then conditions=(isolated default); fi
      for condition in "${conditions[@]}"; do
        evidence="$root/$condition"
        printf '%s\t%s\t%s\t%s\n' "$group" "$sample" "$comparison" "$condition" >> "$root/condition-order.tsv"
        run_pair "$group" "$sample" "$comparison" "$other" "$first" 01 "$package" "$condition" "$filter"
      done
    done
  done
  offset=1
done

python3 - "$root" "$target_cpu" <<'PYTHON'
from collections import Counter
import csv
import json
from pathlib import Path
import sys

root, target = Path(sys.argv[1]), sys.argv[2]
wanted = Counter((root / "targets.txt").read_text().splitlines())
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
    with (current / "order.tsv").open() as stream:
        header, *observed = csv.reader(stream, delimiter="\t")
    assert header == ["group", "pair", "comparison", "case", "side", "revision", "variant", "raw"]
    assert [tuple(row) for row in observed] == expected
    assert {str(p.relative_to(current)) for p in (current / "raw").glob("*.txt")} == {r[-1] for r in expected}
    for r in expected:
        lines = (current / r[-1]).read_text().splitlines()
        assert lines[:3] == ["goos: linux", "goarch: amd64", "pkg: github.com/light-speak/luxo/pkg/lux/rpc"]
        assert lines[3].startswith("cpu: ") and lines[-1] == "PASS"
        models.add(lines[3][5:].strip())
        measures = [item.split() for item in lines[4:-1]]
        assert Counter(row[0] for row in measures) == wanted
        assert all(int(row[1]) > 0 and row[3::2] == ["ns/op", "B/op", "allocs/op"] for row in measures)
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
PYTHON

for condition in default isolated; do
  evidence="$root/$condition"
  analyze_groups
done
cat "$root/runner-cpu.json"
for condition in default isolated; do
  grep -Fxq $'aa\t0' "$root/$condition/verdicts.tsv"
done
if ! jq -e .matchesRequestedModel "$root/runner-cpu.json" >/dev/null; then
  printf '%s\n' 'Runner model did not match. Both linkage conditions are retained as observations.'
  exit 3
fi
for leaf in LRPCPoolLimits/serial/after RPCHandlerCompletion/unobserved/error; do
  if ! grep -Fq "confirmed performance regression: github.com/light-speak/luxo/$package/$leaf-2 sec/op " \
    "$root/default/ab.benchgate.log"; then
    printf 'Original default-build signal did not repeat for %s. Linkage attribution is inconclusive.\n' "$leaf"
    exit 4
  fi
done
grep -Fxq $'ab\t0' "$root/isolated/verdicts.tsv"
