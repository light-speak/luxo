#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
source "$script_dir/diagnose-hosted-perf.sh"

base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
head=7d73e6896b8348a97ec79f869e4a7966c7238dda
candidate_tree=fee389e4a9703840eb5e36742b94f8137f107395
patch_sha=805cd6719966053ce226421436b78dbc2dfdcba65a7f328738b2554f242294d6
scope="${1:?usage: diagnose-pr103-retained-emission.sh intel|amd [--inventory-only]}"
mode="${2:-run}"
[[ $# -le 2 && ( "$scope" == intel || "$scope" == amd ) ]] || exit 2
[[ "$mode" == run || "$mode" == --inventory-only ]] || exit 2

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
evidence="$repo/.tmp/hosted-pr103-retained-emission-$scope"
mkdir "$evidence"
scratch=$(mktemp -d "$repo/.tmp/pr103-retained-emission.XXXXXXXX")
trap '[[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr103-retained-emission.* ]] && rm -rf -- "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export LC_ALL=C GOWORK=off TMPDIR="$scratch/tmp"
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source"

git -C "$repo" diff --exit-code 7b7a30ed7e61e54ceec7bf7b1a971aeac9872599 -- \
  scripts/diagnose-hosted-perf.sh scripts/benchrun internal/benchlayout scripts/setup-modules.sh
test "$(git -C "$repo" rev-parse "$head^")" = "$base"
test "$(git -C "$repo" rev-parse "$head^{tree}")" = 944c2384588c7a0f85aeb98a300e73d2bb722147
patch="$evidence/candidate.patch"
base64 -d < "$repo/.github/qualification/pr103-retained-emission.patch.b64" > "$patch"
printf '%s  %s\n' "$patch_sha" "$patch" | sha256sum -c -
(
  export GIT_INDEX_FILE="$scratch/candidate.index"
  git -C "$repo" read-tree "$head^{tree}"
  git -C "$repo" apply --cached --check --whitespace=error "$patch"
  git -C "$repo" apply --cached --whitespace=error "$patch"
  test "$(git -C "$repo" write-tree)" = "$candidate_tree"
  git -C "$repo" apply --cached --reverse --check "$patch"
)
git -C "$repo" diff --exit-code "$base" "$candidate_tree" -- \
  scripts/benchplan scripts/benchgate scripts/setup-modules.sh internal/benchlayout
git -C "$repo" diff --name-status "$head" "$candidate_tree" > "$evidence/candidate-files.tsv"
printf 'base=%s\nhead=%s\ncandidate_tree=%s\npatch_sha=%s\n' \
  "$base" "$head" "$candidate_tree" "$patch_sha" > "$evidence/revisions.txt"

test "$(go env GOVERSION)" = go1.27.1
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
if [[ "$mode" == run ]]; then
  test "$(uname -s):$(uname -m)" = Linux:x86_64
  test "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'linux\namd64\nv1\n1'
  collect_host > "$evidence/environment.txt"
else
  {
    uname -a
    go version
    go env -json GOOS GOARCH CGO_ENABLED GOFLAGS GOEXPERIMENT
  } > "$evidence/environment.txt"
fi

python3 - "$evidence" "$scope" <<'PYTHON'
import json
from pathlib import Path
import sys

output, scope = Path(sys.argv[1]), sys.argv[2]
groups = {
    "intel": {
        "GlobalReceiverName": ("User", "_Record", "用户"),
    },
    "amd": {
        "CollectionLengthCompiler": (
            "field", "string-length", "string-size",
            "list-size-method", "list-empty-method", "string-empty-method",
        ),
        "RelationResolverEmission": ("nullable-single", "required-list", "leaf-list", "nullable-list"),
        "NumericRangeValidation": ("none", "positive", "signed_int", "signed_float"),
        "APIParameterMetadata": ("Int", "State"),
    },
}
package = "pkg/codegen"
expected, cases, inventory = [], [], {}
for index, (name, leaves) in enumerate(groups[scope].items(), 1):
    case = f"{index:02}"
    function = "Benchmark" + name
    cases.append(f"{case}\t{package}\tdefault\t{name}\t^{function}$")
    inventory[case] = [f"{function}/{leaf}-2" for leaf in leaves]
    expected.extend({"Package": package, "Name": leaf} for leaf in inventory[case])
(output / "cases.tsv").write_text("\n".join(cases) + "\n")
(output / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
(output / "expected-by-case.json").write_text(json.dumps(inventory, indent=2) + "\n")
PYTHON

fixture_files=(
  pkg/codegen/global_value_bench_test.go
  pkg/codegen/compile_collection_bench_test.go
  pkg/codegen/dataloader_nullable_bench_test.go
  pkg/codegen/numeric_range_bench_test.go
  pkg/codegen/codegen_bench_test.go
)
for role in base head candidate; do
  revision="$role"
  if [[ "$role" == candidate ]]; then revision=candidate_tree; fi
  root="$scratch/source/$role"
  mkdir -p "$root/.tmp"
  git -C "$repo" archive --format=tar --output="$scratch/source.tar" "${!revision}"
  tar -xf "$scratch/source.tar" -C "$root"
  sh "$root/scripts/setup-modules.sh" "$root"
  (cd "$root" && sha256sum "${fixture_files[@]}") > "$evidence/fixtures-$role.sha256"
  cp "$root/go.work" "$evidence/go-$role.work.txt"
  cmp "$evidence/fixtures-base.sha256" "$evidence/fixtures-$role.sha256"
done

(
  cd "$repo"
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  if [[ "$mode" == run ]]; then
    cd "$scratch/source/base"
    for tool in benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  fi
) > "$evidence/tools.log" 2>&1
for role in base head candidate; do
  "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" pkg/codegen \
    > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  while IFS=$'\t' read -r case_id package variant ignored filter; do
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem \
      -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s \
      > "$evidence/inventory-$role-$case_id.txt"
  done < "$evidence/cases.tsv"
done

comparisons=(aa ah ap)
if [[ "$mode" == run ]]; then
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    offset=0
    if [[ "$group" == confirmation ]]; then offset=1; fi
    for number in {1..10}; do
      printf -v sample '%02d' "$number"
      if (((number + offset) % 2 == 1)); then first=left; else first=right; fi
      while IFS=$'\t' read -r case_id package variant ignored filter; do
        for index in 0 1 2; do
          comparison="${comparisons[(index + number + offset + 10#$case_id) % 3]}"
          case "$comparison" in
            aa) other=base;;
            ah) other=head;;
            ap) other=candidate;;
          esac
          run_pair "$group" "$sample" "$comparison" "$other" "$first" "$case_id" "$package" "$variant" "$filter"
        done
      done < "$evidence/cases.tsv"
    done
  done
fi

python3 - "$evidence" "$scope" "$mode" <<'PYTHON'
from collections import Counter
import csv
import json
from pathlib import Path
import re
import sys

evidence, scope, mode = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
cases = json.loads((evidence / "expected-by-case.json").read_text())
expected_cpu = {
    "intel": "INTEL(R) XEON(R) PLATINUM 8573C",
    "amd": "AMD EPYC 7763 64-Core Processor",
}[scope]
hosts = set()
number = r"\d+(?:\.\d+)?(?:[eE][+-]?\d+)?"
benchmark = re.compile(rf"(Benchmark\S+)\s+(\d+)\s+{number} ns/op\s+{number} B/op\s+{number} allocs/op")

def require(condition, message):
    if not condition:
        raise SystemExit(message)

def verify_sample(path, leaves, one_iteration=False):
    lines = [line.strip() for line in path.read_text().splitlines()]
    headers = {}
    for name in ("goos", "goarch", "pkg", "cpu"):
        found = [line.partition(": ")[2] for line in lines if line.startswith(name + ": ")]
        require(len(found) == 1 and found[0], f"{path}: invalid {name}")
        headers[name] = found[0]
    require(headers["pkg"] == "github.com/light-speak/luxo/pkg/codegen" and lines[-1:] == ["PASS"],
            f"{path}: benchmark package or result changed")
    measurements = [benchmark.fullmatch(line) for line in lines if line.startswith("Benchmark")]
    require(all(measurements), f"{path}: incomplete measurements")
    require(Counter(row[1] for row in measurements) == Counter(leaves), f"{path}: wrong leaf inventory")
    if one_iteration:
        require(all(row[2] == "1" for row in measurements), f"{path}: expected 1x smoke")
    hosts.add((headers["goos"], headers["goarch"], headers["cpu"].casefold()))

for role in ("base", "head", "candidate"):
    for case, leaves in cases.items():
        verify_sample(evidence / f"inventory-{role}-{case}.txt", leaves, one_iteration=True)

paths, ordered_pairs = set(), []
if mode == "run":
    comparisons = ("aa", "ah", "ap")
    for offset, group in enumerate(("primary", "confirmation")):
        for pair in range(1, 11):
            sides = ("left", "right") if (pair + offset) % 2 else ("right", "left")
            for case, leaves in cases.items():
                for index in range(len(comparisons)):
                    comparison = comparisons[(index + pair + offset + int(case)) % len(comparisons)]
                    for side in sides:
                        role = {"aa": "base", "ah": "head", "ap": "candidate"}[comparison] if side == "right" else "base"
                        raw = f"raw/{group}.{pair:02}.{comparison}.{case}.{side}.txt"
                        paths.add(raw)
                        ordered_pairs.append([group, f"{pair:02}", comparison, case, side, role, "default", raw])
                        verify_sample(evidence / raw, leaves)
    with (evidence / "order.tsv").open(newline="") as manifest:
        observed = list(csv.reader(manifest, delimiter="\t"))
    require(observed == ["group pair comparison case side revision variant raw".split(), *ordered_pairs],
            "raw comparison schedule differs from the requested interleaving")
    require({p.relative_to(evidence).as_posix() for p in (evidence / "raw").iterdir()} == paths,
            "raw file inventory disagrees with the comparison schedule")
require(len(hosts) == 1, "benchmark processes used different platform or CPU labels")
goos, goarch, cpu = next(iter(hosts))
if mode == "run":
    require((goos, goarch) == ("linux", "amd64"), "native Linux amd64 is required")
    models = re.findall(r"^Model name:\s*(.+)$", (evidence / "environment.txt").read_text(), re.M)
    require(len(models) == 1 and models[0].strip().casefold() == cpu, "raw CPU label differs from lscpu")
(evidence / "scope.json").write_text(json.dumps({
    "scope": scope, "expectedCPU": expected_cpu, "actualCPU": cpu,
    "exactOriginalModel": mode == "run" and cpu == expected_cpu.casefold(),
    "timed": mode == "run", "rawProcessesVerified": len(paths),
    "leaves": sum(map(len, cases.values())),
    "previousOfficialRun": 45355952556,
}, indent=2) + "\n")
PYTHON

archive_manifest() {
  (
    cd "$evidence"
    python3 - <<'PYTHON'
from hashlib import sha256
from pathlib import Path
for file in sorted(Path(".").rglob("*")):
    if file.is_file() and file.name != "archive-files.sha256":
        print(f"{sha256(file.read_bytes()).hexdigest()}  {file.as_posix()}")
PYTHON
  ) > "$evidence/archive-files.sha256"
  (cd "$evidence" && sha256sum -c archive-files.sha256) > /dev/null
}
if [[ "$mode" == --inventory-only ]]; then
  archive_manifest
  cat "$evidence/scope.json"
  exit 0
fi

# Select the official target set out of the complete second group. The complete
# samples and medians remain available even when no confirmation is required.
targeted_verdict() {
  local comparison="$1" stem="$evidence/$1" side
  local -a gate_args=()
  if [[ "$comparison" != aa ]]; then
    gate_args=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base")
  fi
  "$scratch/bin/benchgate" --confirmation-targets "$stem.primary.csv" > "$stem.targets.tsv" || return $?
  if [[ ! -s "$stem.targets.tsv" ]]; then
    "$scratch/bin/benchgate" "${gate_args[@]}" "$stem.primary.csv"
    return $?
  fi
  for side in left right; do
    python3 - "$stem.targets.tsv" "$stem.confirmation.$side.txt" <<'PYTHON' > "$stem.targeted.$side.txt" || return $?
from collections import Counter
from pathlib import Path
import sys

target_path, data_path = map(Path, sys.argv[1:])
targets = [tuple(line.split("\t")) for line in target_path.read_text().splitlines()]
assert len(targets) == len(set(targets)) and all(package == "pkg/codegen" for package, _ in targets)
names = {name for _, name in targets}
seen = Counter()
for line in data_path.read_text().splitlines():
    if not line.startswith("Benchmark"):
        print(line)
    elif (name := line.split()[0]) in names:
        seen[name] += 1
        print(line)
if seen != {name: 10 for name in names}:
    raise SystemExit("targeted confirmation requires exactly ten observations of every target")
PYTHON
  done
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.targeted.left.txt" head="$stem.targeted.right.txt" > "$stem.targeted.csv" 2> "$stem.targeted-warnings.txt" || return $?
  "$scratch/bin/benchgate" "${gate_args[@]}" --targeted-confirmation "$stem.primary.csv" "$stem.targeted.csv"
}

printf 'comparison\tgate_exit\n' > "$evidence/verdicts.tsv"
status=0
for comparison in "${comparisons[@]}"; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  rc=0
  targeted_verdict "$comparison" > "$evidence/$comparison.benchgate.log" 2>&1 || rc=$?
  printf '%s\t%s\n' "$comparison" "$rc" >> "$evidence/verdicts.tsv"
  if (( rc != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log" || exit "$rc"
    if [[ "$comparison" != ah ]]; then status=1; fi
  fi
done
archive_manifest
cat "$evidence/scope.json" "$evidence/verdicts.tsv"
if (( status != 0 )); then exit "$status"; fi
if ! grep -q '"exactOriginalModel": true' "$evidence/scope.json"; then
  printf '%s\n' 'Different runner model, measurements retained as observations.' >&2
  exit 3
fi
