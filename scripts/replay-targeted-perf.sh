#!/usr/bin/env bash
set -euo pipefail

tool_revision=f44ab00dea4fb2a5af233cdf8f9f07952b69c345
base_revision=65f1892e175487503aa36caa86d1eb8a0805f073
head_revision=0a5dc510b726b9078c462b0907138786adf223ed
package=pkg/lux/h2transport
filter='^BenchmarkTaskDeadlineReplacement$'
inventory_only=false
allocation_control=false
for flag in "$@"; do
  case "$flag" in
    --inventory-only) inventory_only=true ;;
    --allocation-control) allocation_control=true ;;
    *) printf 'unknown option: %s\n' "$flag" >&2; exit 2 ;;
  esac
done

workspace="$PWD/.tmp/targeted-replay"
mkdir -p "${workspace%/*}"
mkdir "$workspace"
git archive "$tool_revision" | tar -x -C "$workspace"
mkdir -p "$workspace/.tmp/base" "$workspace/.tmp/head"
git archive "$base_revision" | tar -x -C "$workspace/.tmp/base"
git archive "$head_revision" | tar -x -C "$workspace/.tmp/head"
cd "$workspace"
export GITHUB_WORKSPACE="$workspace"
export GOWORK=off
printf 'tools %s\nbase %s\nhead %s\n' "$tool_revision" "$base_revision" "$head_revision" > .tmp/replay-revisions.txt
printf 'allocation control %s\n' "$allocation_control" >> .tmp/replay-revisions.txt
go version >> .tmp/replay-revisions.txt

if "$allocation_control"; then
  python3 - <<'PYTHON'
from pathlib import Path

header = "func BenchmarkTaskDeadlineReplacement(b *testing.B) {"
call = '\n\tb.Run("diagnostic-allocation", benchmarkReplayAllocation)'
for side, expression in (("base", "replayStorage[:]"), ("head", "make([]byte, 64)")):
    path = Path(f".tmp/{side}/pkg/lux/h2transport/scheduling_test.go")
    source = path.read_text()
    if source.count(header) != 1:
        raise SystemExit(f"missing or repeated deadline benchmark in {side}")
    control = f"""
var replayStorage [64]byte
var replayAllocated []byte

func benchmarkReplayAllocation(b *testing.B) {{
    b.ReportAllocs()
    for b.Loop() {{
        replayAllocated = {expression}
    }}
}}
"""
    path.write_text(source.replace(header, header + call) + control)
PYTHON
  shasum -a 256 .tmp/{base,head}/pkg/lux/h2transport/scheduling_test.go >> .tmp/replay-revisions.txt
fi

# Extract the pinned workflow's literal shell blocks for execution unchanged.
python3 - <<'PYTHON'
from pathlib import Path
from textwrap import dedent

steps = {
    "Install pinned benchstat": "install",
    "Compile benchmark binaries": "compile",
    "Run adaptive interleaved benchmark samples": "sample",
    "Enforce performance baseline": "gate",
}
workflow = Path(".github/workflows/test.yml").read_text().splitlines(keepends=True)
for name, slug in steps.items():
    starts = [i for i, line in enumerate(workflow) if line == f"      - name: {name}\n"]
    if len(starts) != 1:
        raise SystemExit(f"expected one workflow step named {name!r}")
    start = starts[0] + 1
    while workflow[start] != "        run: |\n":
        if workflow[start].startswith("      - "):
            raise SystemExit(f"missing literal run block for {name!r}")
        start += 1
    start += 1
    end = start
    while end < len(workflow) and (not workflow[end].strip() or workflow[end].startswith("          ")):
        end += 1
    body = dedent("".join(workflow[start:end]))
    if not body.strip():
        raise SystemExit(f"empty literal run block for {name!r}")
    Path(f".tmp/replay-{slug}.sh").write_text(body)
PYTHON
for step in install compile sample gate; do
  bash -n ".tmp/replay-$step.sh"
done
printf '%s\t%s\n' "$package" "$filter" > .tmp/benchmark-batch.tsv
bash .tmp/replay-compile.sh

for side in base head; do
  mkdir -p ".tmp/replay-inventory/$side"
  binary=".tmp/bin/$side/pkg-lux-h2transport.test"
  shasum -a 256 "$binary" >> .tmp/replay-revisions.txt
  .tmp/bin/benchrun run ".tmp/$side" ".tmp/bin/$side" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem \
    -test.benchtime=1x -test.count=1 -test.cpu=2 > ".tmp/replay-$side-inventory.txt"
  go run ./scripts/benchplan --normalize ".tmp/replay-$side-inventory.txt" \
    ".tmp/replay-inventory/$side/pkg-lux-h2transport.log"
done
printf '%s\n' "$package" > .tmp/replay-packages.txt
go run ./scripts/benchplan .tmp/replay-packages.txt .tmp/replay-inventory .tmp/replay-plan > .tmp/replay-matrix.json
cp .tmp/replay-plan/shard-01.tsv .tmp/benchmark-batch.tsv
cp .tmp/replay-plan/shard-01.json .tmp/benchmark-expected.json

# Local verification can stop after producing the exact inventory and plan.
if "$inventory_only"; then
  cat .tmp/benchmark-expected.json
  exit 0
fi

GOBIN="$PWD/.tmp/bin" bash .tmp/replay-install.sh
TMPDIR="$PWD/.tmp/test-tmp" bash .tmp/replay-sample.sh

gate_status=0
BASE_SHA="$base_revision" bash .tmp/replay-gate.sh > .tmp/replay-gate.log 2>&1 || gate_status=$?
printf '%s\n' "$gate_status" > .tmp/replay-gate.exit
cat .tmp/replay-gate.log
case "$gate_status" in
  0) grep -q '^performance gate passed:' .tmp/replay-gate.log ;;
  1) grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)' .tmp/replay-gate.log ;;
  *) exit "$gate_status" ;;
esac

python3 - "$allocation_control" "$gate_status" <<'PYTHON' | tee .tmp/replay-verdict.txt
import json
from pathlib import Path
import sys

def cases(file):
    data = json.loads(Path(file).read_text())
    base = {(row["Package"], row["Name"]) for row in data["Base"]}
    head = {(row["Package"], row["Name"]) for row in data["Head"]}
    if not base or base != head:
        raise SystemExit("base and head inventories must contain the same cases")
    return base

primary = cases(".tmp/benchmark-expected.json")
expected = {
    ("pkg/lux/h2transport", f"BenchmarkTaskDeadlineReplacement/{count}/{method}-2")
    for count in (1, 8, 128)
    for method in ("scan", "conditional")
}
allocation_control = sys.argv[1] == "true"
control = ("pkg/lux/h2transport", "BenchmarkTaskDeadlineReplacement/diagnostic-allocation-2")
if allocation_control:
    expected.add(control)
if primary != expected:
    raise SystemExit(f"unexpected primary inventory: {sorted(primary)}")
targets = {
    tuple(line.split("\t"))
    for line in Path(".tmp/benchmark-confirmation-targets.tsv").read_text().splitlines()
}
if not targets and not allocation_control:
    print(f"Hosted replay sampled {len(primary)} primary cases and selected no confirmation cases.")
    raise SystemExit(0)
if not targets or not targets < primary:
    raise SystemExit(f"the hosted run must select a nonempty strict subset, got {sorted(targets)}")
if cases(".tmp/benchmark-confirmation-expected.json") != targets:
    raise SystemExit("confirmation inventory differs from selected targets")
if allocation_control:
    verdict = Path(".tmp/replay-gate.log").read_text()
    confirmed = "confirmed performance regression: github.com/light-speak/luxo/pkg/lux/h2transport/TaskDeadlineReplacement/diagnostic-allocation-2"
    if control not in targets or sys.argv[2] != "1" or confirmed not in verdict:
        raise SystemExit("expected the original gate to reject the allocation control")
print(f"Hosted replay sampled {len(primary)} primary cases and {len(targets)} confirmation cases.")
PYTHON
