#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

[[ $# == 2 ]] || exit 2
mode=$2
case "$mode" in run|--plan-only) ;; *) exit 2 ;; esac
repo=$(git -C "$1" rev-parse --show-toplevel)
sampler=${PR91_SAMPLER_FILE:-"$repo/scripts/diagnose-hosted-perf.sh"}
printf '%s  %s\n' 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 "$sampler" | sha256sum -c -
source "$sampler"

base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
head=679921d60ff6c03ba14c2e8ded529a1dd0dd7723
tested=12cc26047f412add167c76acb496c5ae6a3f99e8
product=d42af1b5eaf3f1867c90c7f0ef6c3596ff744be5
package=pkg/codegen
target=BenchmarkGlobalReceiverName
filter="^$target$"
cpu='AMD EPYC 7763 64-Core Processor'

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr91-receiver-only.XXXXXXXX")
root="$scratch/evidence"
if [[ "$mode" == run ]]; then root="$repo/.tmp/hosted-pr91-receiver-only"; fi
created=false
cleanup() {
  local rc=$?
  trap - EXIT
  [[ "$scratch" == "$repo"/.tmp/pr91-receiver-only.* && -d "$scratch" && ! -L "$scratch" ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$created"; then printf 'exit=%s\nowned_scratch_removed=true\n' "$rc" > "$root/cleanup.txt"; fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$root"
created=true
cp "${BASH_SOURCE[0]}" "$sampler" "$root/"
printf 'base=%s\nhead=%s\ntested=%s\ntested_tree=%s\noriginal_run=45368483415\n' "$base" "$head" "$tested" "$product" > "$root/revisions.txt"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = ba252c7cace8908cdfcd7c203bc9dc11bb2ba746
test "$(git -C "$repo" rev-parse "$head^{tree}")" = "$product"
git -C "$repo" merge-base --is-ancestor "$base" "$head"
git -C "$repo" diff --exit-code "$base" "$product" -- "$package/identifiers.go" "$package/global_value_bench_test.go" \
  scripts/benchgate scripts/benchplan scripts/benchrun
git -C "$repo" diff --name-status "$base" "$product" > "$root/product-files.tsv"

verify() {
  python3 - "$@" <<'PY'
import csv
import hashlib
import json
from pathlib import Path
from statistics import median
import sys
from zipfile import ZipFile

action, *args = sys.argv[1:]
pkg = "pkg/codegen"
module = f"github.com/light-speak/luxo/{pkg}"
root = "BenchmarkGlobalReceiverName"
names = [f"{root}/{name}-2" for name in ("User", "_Record", "用户")]
cpu = "AMD EPYC 7763 64-Core Processor"
groups, conditions = ("primary", "confirmation"), ("default", "isolated")

def digest(data):
    return hashlib.sha256(data).hexdigest()

def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")

def samples(path):
    lines = path.read_text().splitlines()
    assert [line.rstrip() for line in lines[:4]] == ["goos: linux", "goarch: amd64", f"pkg: {module}", f"cpu: {cpu}"], path
    assert lines[-1] == "PASS", path
    rows = [line.split() for line in lines if line.startswith("Benchmark")]
    assert sorted(row[0] for row in rows) == sorted(names), path
    assert all(len(row) == 8 and row[3::2] == ["ns/op", "B/op", "allocs/op"] and int(row[1]) > 0 for row in rows), path
    return rows

if action == "prior":
    artifact, evidence = map(Path, args[:2])
    assert digest(artifact.read_bytes()) == "305dd9410e174043594da33e61b9a9d2fcf937b4617bd32451dee7b23e0234c1"
    base, tested = args[2:]
    prior = {}
    with ZipFile(artifact) as ZIP:
        assert ZIP.read("benchmark-revisions.txt").decode().splitlines() == [f"base_sha={base}", f"tested_sha={tested}"]
        assert f"{pkg}\t{names[1]}" in ZIP.read("benchmark-confirmation-targets.tsv").decode().splitlines()
        expected = json.loads(ZIP.read("benchmark-expected.json"))
        assert all({(pkg, name) for name in names} <= {(row["Package"], row["Name"]) for row in expected[side]}
                   for side in ("Base", "Head"))
        for group in groups:
            prior[group] = []
            for side in ("base", "head"):
                lines = ZIP.read(f"benchmark-raw-{group}-{side}.txt").decode().splitlines()
                assert {line.rstrip() for line in lines if line.startswith("cpu: ")} == {f"cpu: {cpu}"}
                rows = [line.split() for line in lines if (line.split() or [""])[0] == names[1]]
                assert len(rows) == 10 and all(row[4] == row[6] == "0" for row in rows)
                prior[group].append(median(float(row[2]) for row in rows))
    write_json(evidence / "prior.json", {"artifact": 14908597395, "cpu": cpu, "mediansNs": prior})
elif action == "overlay":
    directory, overlay = map(Path, args[:2])
    expected_tests = int(args[2])
    receiver = directory / "global_value_bench_test.go"
    data = receiver.read_bytes()
    assert digest(data) == "7280547b02504a4e07437808232485a13425c0d8ead9c968c95fda47ec6252e9"
    start = data.index(b"func BenchmarkGlobalReceiverName(")
    assert data[:start].count(b"\n") == 45
    declaration = data[start:]
    assert digest(declaration) == "695b3f5c8acbe1b61d3c944209054b6e8f6768927aaef47c425f840327b5ebda"
    header = b'package codegen\n\nimport "testing"\n'
    projected = header + b"\n" * (45 - header.count(b"\n")) + declaration
    assert digest(projected) == "330fc5c6785cae1fd2e89810a1ec945e9384669a9efaa3af47cc5e3a2bf74da3"
    replacement = overlay.with_suffix(".go")
    replacement.write_bytes(projected)
    files = sorted(directory.glob("*_test.go"))
    assert len(files) == expected_tests and all(file.is_file() and not file.is_symlink() for file in files)
    write_json(overlay, {"Replace": {str(file): str(replacement) if file == receiver else "" for file in files}})
elif action == "inventory":
    evidence, = map(Path, args)
    for condition in conditions:
        for role in ("base", "head"):
            rows = samples(evidence / condition / f"inventory-{role}.raw")
            assert all(row[1] == "1" for row in rows)
    rows = [{"Package": pkg, "Name": name} for name in names]
    for condition in conditions:
        write_json(evidence / condition / "expected.json", {"Base": rows, "Head": rows})
elif action == "raw":
    evidence, = map(Path, args)
    order = {condition: [] for condition in conditions}
    combined = []
    for offset, group in enumerate(groups):
        for pair in range(1, 11):
            odd = (pair + offset) % 2 == 1
            for comparison in (("ab", "aa") if odd else ("aa", "ab")):
                for condition in (conditions if odd else conditions[::-1]):
                    combined.append([group, f"{pair:02}", comparison, condition])
                    for side in (("left", "right") if odd else ("right", "left")):
                        role = "head" if comparison == "ab" and side == "right" else "base"
                        file = f"raw/{group}.{pair:02}.{comparison}.01.{side}.txt"
                        samples(evidence / condition / file)
                        order[condition].append([group, f"{pair:02}", comparison, "01", side, role, condition, file])
    def recorded(file):
        return list(csv.reader(file.read_text().splitlines()[1:], delimiter="\t"))
    assert recorded(evidence / "condition-order.tsv") == combined
    for condition, rows in order.items():
        path = evidence / condition
        assert recorded(path / "order.tsv") == rows
        assert {file.relative_to(path).as_posix() for file in (path / "raw").iterdir()} == {row[-1] for row in rows}
    assert sum(map(len, order.values())) == 160
    write_json(evidence / "raw-inventory.json", {"arms": 160, "observations": 480})
else:
    raise SystemExit("unknown verification action")
PY
}

if [[ "$mode" == --plan-only ]]; then
  git -C "$repo" show "$product:$package/global_value_bench_test.go" | sha256sum
  cat "$root/revisions.txt" "$root/product-files.tsv"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local GOAMD64=v1 CGO_ENABLED=1
mkdir -p "$TMPDIR" "$scratch/bin"
[[ "$(go env GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'go1.27.1\nlinux\namd64\nv1\n1' ]]
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
collect_host > "$root/environment.txt"
mapfile -t model < <(sed -n 's/^Model name:[[:space:]]*//p' "$root/environment.txt")
[[ ${#model[@]} == 1 && "${model[0]}" == "$cpu" ]] || exit 3
verify prior "$repo/.tmp/pr91-receiver-formal-01.zip" "$root" "$base" "$tested"

for role in base head; do
  revision="$base"
  tests=200
  if [[ "$role" == head ]]; then revision="$product"; tests=202; fi
  source_dir="$scratch/source/$role"
  mkdir -p "$source_dir/.tmp" "$scratch/bin/$role-isolated"
  git -C "$repo" archive "$revision" | tar -xf - -C "$source_dir"
  sh "$source_dir/scripts/setup-modules.sh" "$source_dir" > "$root/workspace-$role.log"
  cp "$source_dir/go.work" "$root/go-$role.work.txt"
  verify overlay "$source_dir/$package" "$root/$role-overlay.json" "$tests"
done
cmp "$root/base-overlay.go" "$root/head-overlay.go"
(
  cd "$scratch/source/base"
  for tool in benchrun benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  (cd "$scratch" && go mod download -json "golang.org/x/perf@$benchstat_version") > "$root/benchstat-module.json"
  perf_dir=$(jq -er 'select(.Sum == "h1:M+kM03JKR23KkrutTue5KEQYDSbZTZTJ4FJ1v5zYrHM=") | .Dir' "$root/benchstat-module.json")
  (cd "$perf_dir" && go build -o "$scratch/bin/benchstat" ./cmd/benchstat)
  go version -m "$scratch/bin/benchstat"
) > "$root/tools.log" 2>&1

for condition in default isolated; do mkdir "$root/$condition"; done
for role in base head; do
  source_dir="$scratch/source/$role"
  "$scratch/bin/benchrun" compile "$source_dir" "$scratch/bin/$role" "$package" > "$root/$role-default-compile.log" 2>&1
  (
    cd "$source_dir"
    GOWORK="$PWD/go.work" go test -trimpath -c -overlay="$root/$role-overlay.json" "./$package" \
      -o "$scratch/bin/$role-isolated/pkg-codegen.test"
  ) > "$root/$role-isolated-compile.log" 2>&1
  "$scratch/bin/benchrun" run "$source_dir" "$scratch/bin/$role" "$package" \
    -test.run='^TestReceiverName$' -test.v -test.count=1 > "$root/$role-correctness.log"
  grep -Fq -- '--- PASS: TestReceiverName (' "$root/$role-correctness.log"
  "$scratch/bin/benchrun" run "$source_dir" "$scratch/bin/$role-isolated" "$package" -test.list='.*' \
    > "$root/$role-isolated-list.txt"
  test "$(cat "$root/$role-isolated-list.txt")" = "$target"
  for condition in default isolated; do
    evidence="$root/$condition"
    binaries="$role"
    if [[ "$condition" == isolated ]]; then binaries+="-isolated"; fi
    record_binaries "$binaries"
    binary="$scratch/bin/$binaries/pkg-codegen.test"
    go tool nm -size "$binary" > "$scratch/symbols.nm"
    grep -E '/codegen\.(BenchmarkGlobalReceiverName|receiverName)' "$scratch/symbols.nm" > "$evidence/$role-symbols.nm"
    go tool objdump -s '/codegen\.(BenchmarkGlobalReceiverName|receiverName)' "$binary" > "$evidence/$role-symbols.objdump"
    awk '$3 == "T" || $3 == "t"' "$scratch/symbols.nm" | sha256sum > "$evidence/$role-text-layout.sha256"
    "$scratch/bin/benchrun" run "$source_dir" "$scratch/bin/$binaries" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      -test.timeout=90s > "$evidence/inventory-$role.raw"
  done
done
verify inventory "$root"
for condition in default isolated; do
  mkdir "$root/$condition/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$root/$condition/order.tsv"
done
printf 'group\tpair\tcomparison\tcondition\n' > "$root/condition-order.tsv"
for group in primary confirmation; do
  offset=0
  if [[ "$group" == confirmation ]]; then offset=1; fi
  for pair in {1..10}; do
    printf -v sample '%02d' "$pair"
    if (((pair + offset) % 2)); then
      first=left
      comparisons=(ab aa)
      order=(default isolated)
    else
      first=right
      comparisons=(aa ab)
      order=(isolated default)
    fi
    for comparison in "${comparisons[@]}"; do
      other=base
      if [[ "$comparison" == ab ]]; then other=head; fi
      for condition in "${order[@]}"; do
        evidence="$root/$condition"
        printf '%s\t%s\t%s\t%s\n' "$group" "$sample" "$comparison" "$condition" >> "$root/condition-order.tsv"
        run_pair "$group" "$sample" "$comparison" "$other" "$first" 01 "$package" "$condition" "$filter"
      done
    done
  done
done
verify raw "$root"
for condition in default isolated; do
  evidence="$root/$condition"
  analyze_groups
done
for condition in default isolated; do
  grep -Fxq $'aa\t0' "$root/$condition/verdicts.tsv" || exit 3
done
grep -Fq 'confirmed performance regression: github.com/light-speak/luxo/pkg/codegen/GlobalReceiverName/_Record-2 sec/op ' \
  "$root/default/ab.benchgate.log" || exit 4
grep -Fxq $'ab\t0' "$root/isolated/verdicts.tsv"
