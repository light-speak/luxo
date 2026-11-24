#!/usr/bin/env bash
set -euo pipefail

scope="${1:?specify epyc-7763 or epyc-9v74}"
mode="${2:-run}"
[[ $# -le 2 ]] || exit 2
case "$mode" in run|--plan-only|--inventory-only) ;; *) exit 2;; esac
case "$scope" in
  epyc-7763) target_cpu='AMD EPYC 7763 64-Core Processor' ;;
  epyc-9v74) target_cpu='AMD EPYC 9V74 80-Core Processor' ;;
  *) exit 2 ;;
esac

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
sampler="$script_dir/diagnose-hosted-perf.sh"
printf '%s  %s\n' 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 "$sampler" | sha256sum -c -
source "$sampler"
base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
head=679921d60ff6c03ba14c2e8ded529a1dd0dd7723
tested=12cc26047f412add167c76acb496c5ae6a3f99e8
head_tree=d42af1b5eaf3f1867c90c7f0ef6c3596ff744be5
test "$(git -C "$repo" rev-parse "$base^{tree}")" = ba252c7cace8908cdfcd7c203bc9dc11bb2ba746
test "$(git -C "$repo" rev-parse "$head^{tree}")" = "$head_tree"
git -C "$repo" merge-base --is-ancestor "$base" "$head"
fixtures=(
  pkg/codegen/global_value_bench_test.go
  pkg/codegen/numeric_literal_bench_test.go
  pkg/codegen/codegen_bench_test.go
  pkg/codegen/compile_pagination_bench_test.go
  pkg/semantic/analyzer_bench_test.go
  pkg/semantic/scope_bench_test.go
)
git -C "$repo" diff --exit-code "$base" "$head" -- "${fixtures[@]}" scripts/benchrun scripts/benchplan scripts/benchgate \
  scripts/setup-modules.sh go.mod go.sum

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr91-amd.$scope.XXXXXXXX")
evidence="$repo/.tmp/hosted-pr91-amd-$scope"
if [[ "$mode" != run ]]; then evidence="$scratch/evidence"; fi
created=false
cleanup_pr91() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr91-amd.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$evidence/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_pr91 EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
created=true
printf 'base_sha=%s\npr_head=%s\ntested_sha=%s\ntested_tree=%s\noriginal_workflow_run=45368483415\n' \
  "$base" "$head" "$tested" "$head_tree" > "$evidence/revisions.txt"
cp "${BASH_SOURCE[0]}" "$sampler" "$evidence/"
audit="$scratch/audit.py"
cat > "$audit" <<'PYTHON'
from collections import Counter
import csv
import json
from pathlib import Path
import re
import sys

root, scope, target_cpu, phase = Path(sys.argv[1]), *sys.argv[2:5]
prefix = "github.com/light-speak/luxo/"
changed = [
    ("BenchmarkQueryFacadeSource/User/1", "facade"),
    ("BenchmarkPaginationCandidate/object-copy/Analyze", "pagination"),
    ("BenchmarkPaginationCandidate/object-copy/Generate", "pagination"),
]
scopes = {
    "epyc-7763": {
        "pkg/codegen": [
            ("BenchmarkGlobalReceiverName/_Record", "formal-reject"),
            ("BenchmarkGlobalReceiverName/User", "sibling"),
        ] + changed,
    },
    "epyc-9v74": {
        "pkg/codegen": [
            ("BenchmarkCompileDecimalIntegerLiterals/duration_leading_zero", "formal-reject"),
            ("BenchmarkCompileDecimalIntegerLiterals/duration_plain", "formal-reject"),
            ("BenchmarkCompileDecimalIntegerLiterals/int_plain", "sibling"),
        ] + changed,
        "pkg/semantic": [
            ("BenchmarkAnalyzeVariableBindings/discard-read/128", "formal-reject"),
            ("BenchmarkAnalyzeVariableBindings/undefined-control/128", "sibling"),
            ("BenchmarkScopeLookup/depth32/missing", "formal-reject"),
            ("BenchmarkScopeLookup/depth32/parent", "formal-reject"),
            ("BenchmarkScopeLookup/depth32/local", "sibling"),
        ],
    },
}
cases = {f"{i:02d}": (package, rows) for i, (package, rows) in enumerate(scopes[scope].items(), 1)}

if phase == "plan":
    expected, case_lines, describe = [], [], []
    for case, (package, rows) in cases.items():
        patterns = []
        for name, role in rows:
            patterns.append("/".join("^" + re.escape(segment) + "$" for segment in name.split("/")))
            expected.append({"Package": package, "Name": name + "-2"})
            describe.append({"package": package, "name": name, "role": role})
        case_lines.append("\t".join((case, package, "default", scope, "|".join(patterns))))
    assert len(expected) == (5 if scope == "epyc-7763" else 11)
    (root / "cases.tsv").write_text("\n".join(case_lines) + "\n")
    (root / "packages.txt").write_text("\n".join(scopes[scope]) + "\n")
    (root / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
    (root / "scope.json").write_text(json.dumps({
        "targetCpu": target_cpu, "leaves": describe, "expectedProcessArms": 80 * len(cases),
    }, indent=2) + "\n")
    sys.exit(0)

goos, goarch = sys.argv[5:]
cpus = set()

def audit(path, case, inventory):
    package, rows = cases[case]
    lines = path.read_text().splitlines()
    assert lines[:3] == ["goos: " + goos, "goarch: " + goarch, "pkg: " + prefix + package], path
    assert len(lines) == len(rows) + 5 and lines[-1] == "PASS", path
    assert lines[3].startswith("cpu: ") and lines[3][5:].strip(), path
    cpus.add(lines[3][5:].strip())
    seen = []
    for line in lines[4:-1]:
        name, iterations, ns, ns_unit, memory, mem_unit, allocations, alloc_unit = line.split()
        assert int(iterations) > 0 and (not inventory or int(iterations) == 1), path
        assert (ns_unit, mem_unit, alloc_unit) == ("ns/op", "B/op", "allocs/op"), path
        assert all(re.fullmatch(r"[0-9]+(?:\.[0-9]+)?", item) for item in (ns, memory, allocations)), path
        seen.append(name)
    assert Counter(seen) == Counter(name + "-2" for name, _ in rows), path

for role in ("base", "head"):
    for case in cases:
        audit(root / f"inventory-{role}-{case}.txt", case, True)

if phase == "samples":
    expected = []
    for offset, group in enumerate(("primary", "confirmation")):
        for sample in range(1, 11):
            for case in cases:
                sides = ("left", "right") if (sample + offset) % 2 else ("right", "left")
                comparisons = ("aa", "ab") if (sample + int(case) + offset) % 2 else ("ab", "aa")
                for comparison in comparisons:
                    for side in sides:
                        role = "head" if comparison == "ab" and side == "right" else "base"
                        raw = f"raw/{group}.{sample:02d}.{comparison}.{case}.{side}.txt"
                        expected.append((group, f"{sample:02d}", comparison, case, side, role, "default", raw))
    with (root / "order.tsv").open() as stream:
        header, *actual = csv.reader(stream, delimiter="\t")
    assert header == ["group", "pair", "comparison", "case", "side", "revision", "variant", "raw"]
    assert [tuple(row) for row in actual] == expected
    assert {str(path.relative_to(root)) for path in (root / "raw").glob("*.txt")} == {row[-1] for row in expected}
    for row in expected:
        audit(root / row[-1], row[3], False)
assert len(cpus) == 1, cpus
observed = next(iter(cpus))
if goos == "linux" and (root / "environment.txt").exists():
    models = {line.split(":", 1)[1].strip() for line in (root / "environment.txt").read_text().splitlines()
              if line.startswith("Model name:")}
    assert models == {observed}, (models, observed)
(root / "runner-cpu.json").write_text(json.dumps({
    "expected": target_cpu, "observed": observed, "matchesRequestedModel": observed == target_cpu,
}, indent=2) + "\n")
PYTHON
python3 "$audit" "$evidence" "$scope" "$target_cpu" plan
if [[ "$mode" == --plan-only ]]; then
  cat "$evidence/revisions.txt" "$evidence/scope.json"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local LC_ALL=C
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source"
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
if [[ "$mode" == run ]]; then
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  collect_host > "$evidence/environment.txt"
fi
goos=$(go env GOOS)
goarch=$(go env GOARCH)
for role in base head; do
  directory="$scratch/source/$role"
  mkdir -p "$directory/.tmp"
  git -C "$repo" archive "${!role}" | tar -xf - -C "$directory"
  sh "$directory/scripts/setup-modules.sh" "$directory" > "$evidence/workspace-$role.log"
  (cd "$directory" && sha256sum "${fixtures[@]}") > "$evidence/fixtures-$role.sha256"
done
cmp "$evidence/fixtures-base.sha256" "$evidence/fixtures-head.sha256"
(
  cd "$scratch/source/base"
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  if [[ "$mode" == run ]]; then
    for tool in benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  fi
) > "$evidence/tools.log" 2>&1

for role in base head; do
  while IFS=$'\t' read -r case_id package variant name filter; do
    "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" "$package" \
      > "$evidence/compile-$role-$case_id.log" 2>&1
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      -test.timeout=90s > "$evidence/inventory-$role-$case_id.txt" 2>> "$evidence/benchmark-stderr.txt"
  done < "$evidence/cases.tsv"
  record_binaries "$role"
done
python3 "$audit" "$evidence" "$scope" "$target_cpu" inventory "$goos" "$goarch"
if [[ "$mode" == --inventory-only ]]; then
  cat "$evidence/scope.json" "$evidence/runner-cpu.json"
  exit 0
fi

sample_groups
python3 "$audit" "$evidence" "$scope" "$target_cpu" samples "$goos" "$goarch"
for role in base head; do
  (cd "$scratch/source/$role" && sha256sum -c "$evidence/fixtures-$role.sha256") > "$evidence/fixtures-$role.final.log"
done
analyze_groups
status=0
for comparison in aa ab; do
  if ! grep -Fxq "$comparison"$'\t0' "$evidence/verdicts.tsv"; then status=1; fi
done
cat "$evidence/runner-cpu.json"
if ! jq -e .matchesRequestedModel "$evidence/runner-cpu.json" >/dev/null; then
  printf '%s\n' 'Requested EPYC model did not match. Raw measurements are retained as observations.'
  if (( status == 0 )); then status=3; fi
fi
exit "$status"
