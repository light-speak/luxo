#!/usr/bin/env bash
set -euo pipefail

write_scope() {
  python3 - "$script_dir/diagnose-pr83-pr84.tsv" "$suite" "$evidence" <<'PY'
import json
from pathlib import Path
import sys

catalog, suite, evidence = sys.argv[1:]
evidence = Path(evidence)
batches, selections, expected, seen = [], [], [], set()
for line in Path(catalog).read_text().splitlines():
    case_suite, package, role, names, selector = line.split("\t")
    if case_suite != suite:
        continue
    case_id = f"{len(batches) + 1:02}"
    leaves = names.split("|")
    if role not in {"worker", "decoder", "rejected", "control"} or not selector.startswith("^Benchmark"):
        raise SystemExit("invalid diagnostic case")
    batches.append((case_id, package, "default", leaves[0], selector))
    selections.append({"case": case_id, "package": package, "role": role, "leaves": leaves})
    for name in leaves:
        key = (package, name)
        if key in seen or not name.startswith("Benchmark") or not name.endswith("-2"):
            raise SystemExit(f"invalid/duplicate planned leaf: {key}")
        seen.add(key)
        expected.append({"Package": package, "Name": name})
if not {case["role"] for case in selections} >= {"worker" if suite == "pr83" else "decoder", "rejected"}:
    raise SystemExit("suite must cover both the changed path and rejected leaves")
(evidence / "cases.tsv").write_text("".join("\t".join(row) + "\n" for row in batches))
(evidence / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
(evidence / "scope.json").write_text(json.dumps(selections, indent=2) + "\n")
PY
}

verify_output() {
  python3 - "$evidence" "$probe" "$1" <<'PY'
import csv
import itertools
import json
from pathlib import Path
import re
import sys

evidence, probe, mode = sys.argv[1:]
evidence = Path(evidence)
goos, goarch = probe.split(":")
scope = {r["case"]: r for r in json.loads((evidence / "scope.json").read_text())}
metric = re.compile(r"[0-9]+(?:[.][0-9]+)?")
units = {"ns/op", "B/op", "allocs/op"}

def verify(path, case_id):
    case = scope[case_id]
    headers, leaves, passes = {}, set(), 0
    for line in (evidence / path).read_text().splitlines():
        if line.startswith("Benchmark"):
            name, iterations, *values = line.split()
            measurements = dict(zip(values[1::2], values[0::2]))
            if (name in leaves or not iterations.isdigit() or int(iterations) < 1
                    or (mode == "inventory" and int(iterations) != 1) or len(values) != 2 * len(measurements)
                    or not units <= measurements.keys() or any(not metric.fullmatch(measurements[unit]) for unit in units)):
                raise SystemExit(f"invalid benchmark measurement in {path}: {line}")
            leaves.add(name)
        elif line == "PASS":
            passes += 1
        elif line.strip():
            key, sep, value = line.partition(": ")
            if not sep or key not in {"goos", "goarch", "pkg", "cpu"} or key in headers or not value.strip():
                raise SystemExit(f"invalid benchmark header or output in {path}: {line}")
            headers[key] = value.strip()
    if (passes != 1 or leaves != set(case["leaves"])
            or headers.get("goos") != goos or headers.get("goarch") != goarch
            or headers.get("pkg") != f"github.com/light-speak/luxo/{case['package']}" or "cpu" not in headers):
        raise SystemExit(f"benchmark inventory mismatch in {path}: {leaves}")

if mode == "inventory":
    for revision, case in itertools.product(("base", "head"), scope):
        verify(f"inventory-{revision}-{case}.txt", case)
elif mode == "samples":
    arms = set(itertools.product(("primary", "confirmation"), (f"{n:02}" for n in range(1, 11)),
                                  ("aa", "ab"), scope, ("left", "right")))
    with (evidence / "order.tsv").open() as plan:
        for row in csv.DictReader(plan, delimiter="\t"):
            key = tuple(row[field] for field in ("group", "pair", "comparison", "case", "side"))
            group, pair, comparison, case, side = key
            expected_path = f"raw/{group}.{pair}.{comparison}.{case}.{side}.txt"
            revision = "head" if comparison == "ab" and side == "right" else "base"
            if key not in arms or row["revision"] != revision or row["variant"] != "default" or row["raw"] != expected_path:
                raise SystemExit(f"unexpected benchmark arm: {row}")
            verify(expected_path, case)
            arms.remove(key)
    if arms:
        raise SystemExit(f"missing {len(arms)} benchmark arms")
else:
    raise SystemExit(f"invalid verification mode: {mode}")
PY
}

build_archive() {
  local revision="$1" commit="$2" package
  local source="$scratch/source/$revision"
  mkdir -p "$source" "$scratch/bin/$revision"
  git -C "$repo" show -s --format="$revision %H %T" "$commit" >> "$evidence/revisions.txt"
  git -C "$repo" archive "$commit" | tar -xf - -C "$source"
  sh "$source/scripts/setup-modules.sh" "$source"
  cp "$source/go.work" "$evidence/go-$revision.work.txt"
  if [[ "$revision" == base ]]; then
    (
      cd "$source"
      for tool in benchrun benchplan benchgate; do
        go build -o "$scratch/bin/$tool" "./scripts/$tool"
      done
    ) > "$evidence/compile-tools.log" 2>&1
  fi
  for package in "${packages[@]}"; do
    "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$revision" "$package"
  done > "$evidence/compile-$revision.log" 2>&1
  record_binaries "$revision"
}

record_layout() {
  local revision="$1" package name binary
  local pattern="^github[.]com/light-speak/luxo/pkg/lux/.*[.](${benchmarks}|prepareStreamHandler|configuredOriginPolicy|DecodeDebugTrace|DecodeDebugTraceHeader|decodeDebugTraceHeaderBytes|readDebugSpanList|decodeDebugSpans|finishWorker|stopAdmission|run|parseRequestHead|replaceTaskDeadline|GetContext)([.]func[0-9]+)*$"
  for package in "${packages[@]}"; do
    name="${package##*/}"
    binary="$scratch/bin/$revision/pkg-lux-$name.test"
    go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$revision-$name.nm"
    go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$revision-$name.objdump"
    test -s "$evidence/symbols-$revision-$name.nm"
    test -s "$evidence/symbols-$revision-$name.objdump"
  done
}

probe_inventory() {
  local revision case_id package variant name filter
  for revision in base head; do
    while IFS=$'\t' read -r case_id package variant name filter; do
      "$scratch/bin/benchrun" run "$scratch/source/$revision" "$scratch/bin/$revision" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        > "$evidence/inventory-$revision-$case_id.txt"
    done < "$evidence/cases.tsv"
  done
  verify_output inventory
}

main_diagnostic() {
  local suite="${1:?usage: diagnose-pr83-pr84.sh pr83|pr84-values|pr84-runtime [--inventory-only]}"
  shift
  local inventory_only=false
  if [[ "$#" -eq 1 && "$1" == --inventory-only ]]; then
    inventory_only=true
  elif [[ "$#" -ne 0 ]]; then
    return 2
  fi
  local head tree original_run
  case "$suite" in
    pr83)
      head=f841a4770b83b368730db8c559df4a4da226b736
      tree=827ac6c56bed69eb7e0d80c2394e9d7663e3f62d
      original_run=45219367507
      ;;
    pr84-values|pr84-runtime)
      head=54ca8db0e1a324947f07ec61dfdc340b143bb441
      tree=62a2ee96421c1599bf70654ed00825afdbc8b21e
      original_run=45220719488
      ;;
    *) printf 'Unknown suite %s\n' "$suite" >&2; return 2 ;;
  esac
  repo=$(git rev-parse --show-toplevel)
  local script_dir launch sampler_revision probe benchmarks package revision
  script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
  launch=$(pwd -P)
  sampler_revision=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
  mkdir -p "$launch/.tmp"
  test ! -L "$launch/.tmp"
  evidence="$launch/.tmp/hosted-pr83-pr84-$suite"
  mkdir "$evidence"
  git -C "$repo" show "$sampler_revision:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
  [[ "$(sha256sum "$evidence/sampler.sh" | cut -d' ' -f1)" == 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 ]]
  printf '%s\n' "$sampler_revision" > "$evidence/sampler-revision.txt"
  source "$evidence/sampler.sh"
  [[ "$base" == 65f1892e175487503aa36caa86d1eb8a0805f073 ]]
  [[ "$(git -C "$repo" rev-parse "$head^{tree}")" == "$tree" ]]
  git -C "$repo" merge-base --is-ancestor "$base" "$head"

  scratch=$(mktemp -d "$launch/.tmp/.hosted-pr83-pr84.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  export GOWORK=off GOTOOLCHAIN=local TMPDIR="$scratch/tmp"
  mkdir -p "$TMPDIR" "$scratch/bin"
  probe="$(go env GOOS):$(go env GOARCH)"
  [[ "$(go env GOVERSION)" == go1.27.1 && "$(go env GOHOSTOS):$(go env GOHOSTARCH)" == "$probe" ]]
  [[ -z "$(go env GOFLAGS)" ]]
  if "$inventory_only"; then
    (uname -a && go version && go env -json GOOS GOARCH GOAMD64 CGO_ENABLED GOEXPERIMENT GOFLAGS CC) > "$evidence/environment.txt"
  else
    [[ "$probe:$(go env GOAMD64)" == linux:amd64:v1 ]]
    collect_host > "$evidence/environment.txt"
  fi
  printf 'Original Tests run %s attempt 1\n' "$original_run" > "$evidence/protocol.txt"
  cat >> "$evidence/protocol.txt" <<'PROTOCOL'
One native runner per suite. Original exact tree archives and default-link test binaries.
A/A runs the base binary on both sides. A/B compares the base and original PR.
Both comparisons use two groups of ten alternating pairs per case.
Original 200ms, benchmem, CPU=2, benchstat alpha=.01 and benchgate thresholds.
Approved costs are loaded only for the exact A/B base and source by the pinned gate.
The recorded A/B verdict is diagnostic. An A/A threshold failure is inconclusive and fails this job.
Targeted selection does not replay the original full-shard context or replace the production gate.
PROTOCOL
  write_scope
  local -a packages=()
  while IFS= read -r package; do packages+=("$package"); done < <(cut -f2 "$evidence/cases.tsv" | sort -u)
  benchmarks=$(cut -f4 "$evidence/cases.tsv" | sed -E 's@[/].*@@;s/-2$//' | sort -u | paste -sd '|' -)
  build_archive base "$base"
  build_archive head "$head"
  probe_inventory
  if "$inventory_only"; then return 0; fi
  for revision in base head; do record_layout "$revision"; done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  verify_output samples
  analyze_groups
  grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main_diagnostic "$@"
fi
