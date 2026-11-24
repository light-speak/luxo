#!/usr/bin/env bash
set -euo pipefail

[[ "$#" == 2 || ( "$#" == 3 && "$3" == --plan-only ) ]]
repo=$(git -C "$1" rev-parse --show-toplevel)
formal="$(cd "$(dirname "$2")" && pwd -P)/$(basename "$2")"
plan="${3:-}"
evidence="$repo/.tmp/hosted-columnar-merge-pr71-formal"
assets=8c98529d6bcc09ae80e42a7ee1023cd0568edaf1
baseline=1117eb9ffcc0beef977abceec656a351b0988389
head=4177d556d135cff1cd27e42798c0b082cf09f9b3
tested=3baa5b629d86749c6f5dc38dd9e540e0d11c39e9
head_tree=175294b28abcbb21b68ba40fd6288ce63e2e1547
candidate_tree=7fad83cac47d54aa755ea22f6acc5e9b1c73f113
formal_sha=094c81b14547cfbe5aa21d6b402c4b2574925fbafb701e503d9ac09354efb993
package=pkg/lux/luvia
benchmark='BenchmarkMergeDestination|benchmarkMergeDestination|appendMergedColumnar|AppendMergedColumnar|AppendMergedSelected'
cpu='AMD EPYC 7763 64-Core Processor'

sha() {
  python3 -c 'import hashlib, pathlib, sys; print(hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest())' "$1"
}
asset() {
  local name="$1" target="$2" expected="$3"
  git -C "$repo" show "$assets:scripts/$name" > "$target"
  [[ "$(sha "$target")" == "$expected" ]]
}

[[ ! -L "$repo/.tmp" ]]
mkdir -p "$repo/.tmp"
mkdir "$evidence"
scratch=$(mktemp -d "$repo/.tmp/pr71-columnar.XXXXXXXX")
trap 'rm -rf -- "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export LC_ALL=C
[[ "$(sha "$formal")" == "$formal_sha" ]]
[[ "$(git -C "$repo" rev-parse "${assets}^{tree}")" == a2867c9ef16a916ff5912b9397be1fef5a783e37 ]]
[[ "$(git -C "$repo" rev-parse "${head}^{tree}")" == "$head_tree" ]]
[[ "$(git -C "$repo" rev-parse "${baseline}^{tree}")" == 97e7de78aafc023063c8ad6e44ecfaa3dab04d35 ]]
git -C "$repo" merge-base --is-ancestor "$baseline" "$head"
asset diagnose-hosted-perf.sh "$scratch/sampler.sh" 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
asset diagnose-pr83-pr84.sh "$scratch/validator.sh" 8a998e03087873c17f7b5e0675f3880c2dc6e874e61ae579e3e371f9a5242bbc
asset diagnose-columnar-merge.go.txt "$evidence/selected-wire.go.txt" 49e62c8f6e4abd7ec357d06872cf76782805203f9bdd4f3217f06f9f98adddfc
asset diagnose-columnar-merge.patch.b64 "$scratch/candidate.b64" b33bb839e54332981cc8f9f86722b18f4601c8808b05c676ec658e4222376d9d

python3 - "$formal" "$scratch" "$evidence" "$baseline" "$tested" "$cpu" <<'PYTHON'
import base64
import json
from pathlib import Path
import statistics
import sys
import zipfile

formal, scratch, evidence = map(Path, sys.argv[1:4])
baseline, tested, cpu = sys.argv[4:]
package = "pkg/lux/luvia"
failed = "BenchmarkMergeDestination/columnar=true/rows=128/direct=true-2"
leaves = [
    f"BenchmarkMergeDestination/columnar={columnar}/rows={rows}/direct={direct}-2"
    for columnar, rows in (("false", 1), ("true", 1), ("true", 128))
    for direct in ("false", "true")
]
def need(condition, message):
    if not condition:
        raise SystemExit(message)

raw = {}
with zipfile.ZipFile(formal) as bundle:
    def read(name):
        return bundle.read(name).decode().splitlines()
    need(bundle.testzip() is None, "bad formal benchmark archive")
    need(read("benchmark-revisions.txt") == [f"base_sha={baseline}", f"tested_sha={tested}"],
         "formal source revisions changed")
    need(read("benchmark-confirmation-targets.tsv") == [f"{package}\t{failed}"],
         "formal confirmation target changed")
    for group, expected in (("primary", leaves), ("confirmation", [failed])):
        for side in ("base", "head"):
            lines = read(f"benchmark-raw-{group}-{side}.txt")
            for field, value in (("cpu", cpu), ("goos", "linux"), ("goarch", "amd64")):
                need({line.partition(":")[2].strip() for line in lines if line.startswith(field + ":")} == {value},
                     f"formal {field} changed")
            rows = {name: [] for name in expected}
            for line in lines:
                if not line.startswith("BenchmarkMergeDestination/"):
                    continue
                name, iterations, *metrics = line.split()
                need(name in rows and int(iterations) > 0 and metrics[1::2] == ["ns/op", "B/op", "allocs/op"],
                     "invalid formal columnar leaf")
                rows[name].append([float(metrics[0]), int(metrics[2]), int(metrics[4])])
            need(all(len(v) == 10 for v in rows.values()), "missing formal paired observations")
            raw[f"{group}-{side}"] = rows
left, right = (raw[f"confirmation-{r}"][failed] for r in ("base", "head"))
need(all(sample[1:] == [0, 0] for sample in left + right), "confirmed allocation did not match")
(evidence / "formal-receipt.json").write_text(json.dumps({
    "run": 45413131379, "artifact": 14931164632, "tested": tested, "cpu": cpu, "confirmedLeaf": failed,
    "mediansNs": [statistics.median(row[0] for row in values) for values in (left, right)],
    "maximumPairedNs": round(max(r[0] - l[0] for l, r in zip(left, right)), 6),
    "raw": raw,
}, indent=2) + "\n")
(evidence / "scope.json").write_text(json.dumps([{"case": "01", "package": package, "leaves": leaves}]) + "\n")
expected = [{"Package": package, "Name": name} for name in leaves]
(evidence / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}) + "\n")
(evidence / "cases.tsv").write_text(f"01\t{package}\tdefault\t{leaves[0]}\t^BenchmarkMergeDestination$\n")
(evidence / "columnar-merge.patch").write_bytes(base64.b64decode(
    b"".join((scratch / "candidate.b64").read_bytes().splitlines()), validate=True))

# Extend only the pinned schedule and its matching validator. All execution and gate helpers stay intact.
def adapt(file, edits):
    source = (scratch / file).read_text()
    for old, new, count in edits:
        need(source.count(old) == count, f"sampler input drift: {old!r}")
        source = source.replace(old, new)
    (evidence / file).write_text(source)

adapt("sampler.sh", [
    ('  if [[ "$first" == left ]]; then\n', '  local left_role=base\n  if [[ "$comparison" == hc ]]; then left_role=head; fi\n  if [[ "$first" == left ]]; then\n', 1),
    ('"$comparison" left base "$case_id"', '"$comparison" left "$left_role" "$case_id"', 2),
    ('kind=(aa ab)', 'kind=(aa bh hc bc)', 1), ('kind=(ab aa)', 'kind=(bc hc bh aa)', 1),
    ('''          revision=head
          if [[ "$comparison" == aa ]]; then
            revision=base
          fi''',
     '''          case "$comparison" in
            aa) revision=base ;;
            bh) revision=head ;;
            hc|bc) revision=candidate ;;
          esac''', 1),
    ('for comparison in aa ab; do', 'for comparison in aa bh hc bc; do', 1),
    ('if [[ "$comparison" != aa ]]; then', 'if [[ "$comparison" == bh || "$comparison" == bc ]]; then', 1),
])
adapt("validator.sh", [
    ('("aa", "ab"), scope', '("aa", "bh", "hc", "bc"), scope', 1),
    ('headers.get("pkg") != f"github.com/light-speak/luxo/{case[\'package\']}" or "cpu" not in headers',
     'headers.get("pkg") != f"github.com/light-speak/luxo/{case[\'package\']}" or headers.get("cpu") != "' + cpu + '"', 1),
    ('revision = "head" if comparison == "ab" and side == "right" else "base"',
     'revision = {"aa": ("base", "base"), "bh": ("base", "head"),\n'
     '                        "hc": ("head", "candidate"), "bc": ("base", "candidate")}[comparison][side == "right"]', 1),
])
PYTHON

candidate_patch="$evidence/columnar-merge.patch"
[[ "$(sha "$candidate_patch")" == 2e248077b67ed819c1970c5297efb6e86ec4e3c7adb6fac917a2558d6415ae13 ]]
store="$scratch/git"
git -C "$repo" init --quiet --bare "$store"
git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$store/objects/info/alternates"
graft=(git --git-dir="$store")
"${graft[@]}" read-tree "$head"
"${graft[@]}" apply --cached --whitespace=error-all "$candidate_patch"
[[ "$("${graft[@]}" write-tree)" == "$candidate_tree" ]]
[[ "$("${graft[@]}" diff --name-only "$head_tree" "$candidate_tree")" == "$package/merger.go" ]]
"${graft[@]}" apply --cached --reverse --whitespace=error-all "$candidate_patch"
[[ "$("${graft[@]}" write-tree)" == "$head_tree" ]]

printf 'base %s\nhead %s archive %s tree %s\ncandidate %s\n' "$baseline" "$tested" "$head" "$head_tree" "$candidate_tree" > "$evidence/revisions.txt"
for role in base head candidate; do
  case "$role" in base) tree=$baseline ;; head) tree=$head_tree ;; candidate) tree=$candidate_tree ;; esac
  for name in merger.go merger_bench_test.go merger_test.go; do
    "${graft[@]}" show "$tree:$package/$name" > "$scratch/fixture.go"
    digest=$(sha "$scratch/fixture.go")
    expected=2c30da7b52cf1697b13d260ab7dbe74154b77a4d124817ca21b5f367fe6acf51
    case "$name/$role" in
      merger.go/candidate) expected=4f72470b71fa742893a0791c87ee39e3b2c719b34c30b642ebe8cc731a44a0fb ;;
      merger_bench_test.go/*) expected=0c115ad4099c7ed845f35321f01bde38d53bb414bdabd3eeefe4a2e32051c34f ;;
      merger_test.go/*) expected=0cba44db8b444555efdb427368e2ef26145bbd53c66c229e70d2beced5ff6023 ;;
    esac
    [[ "$digest" == "$expected" ]]
    printf '%s  %s\n' "$digest" "$package/$name"
  done > "$evidence/sources-$role.sha256"
done
[[ "$plan" != --plan-only ]] || exit 0

source "$evidence/sampler.sh"
source "$evidence/validator.sh"
base="$baseline"
benchmarks="$benchmark"
packages=("$package")
export GOWORK=off GOTOOLCHAIN=local TMPDIR="$scratch/tmp"
mkdir -p "$TMPDIR" "$scratch/bin"
probe="$(go env GOOS):$(go env GOARCH)"
[[ "$(go env GOVERSION)" == go1.27.1 && "$(go env GOHOSTOS):$(go env GOHOSTARCH)" == "$probe" ]]
[[ "$probe:$(go env GOAMD64):$(go env CGO_ENABLED)" == linux:amd64:v1:1 && -z "$(go env GOFLAGS)" ]]
[[ "$(lscpu | awk -F: '$1 == "Model name" {sub(/^[[:space:]]+/, "", $2); sub(/[[:space:]]+$/, "", $2); print $2}')" == "$cpu" ]]
collect_host > "$evidence/environment.txt"
for role in base head candidate; do
  case "$role" in base) tree=$baseline ;; head) tree=$head_tree ;; candidate) tree=$candidate_tree ;; esac
  checkout="$scratch/source/$role"
  mkdir -p "$checkout/.tmp" "$scratch/bin/$role"
  "${graft[@]}" archive "$tree" | tar -xf - -C "$checkout"
  sh "$checkout/scripts/setup-modules.sh" "$checkout"
  if [[ "$role" == base ]]; then
    (
      cd "$checkout"
      for name in benchrun benchplan benchgate; do
        go build -o "$scratch/bin/$name" "./scripts/$name"
      done
      GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    ) > "$evidence/compile-tools.log" 2>&1
    go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
    "$scratch/bin/benchrun" performance-profile > "$evidence/compile-profile.txt"
  fi
  "$scratch/bin/benchrun" compile-performance "$checkout" "$scratch/bin/$role" "$package" default > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  record_layout "$role"
  grep -Eq '[.]BenchmarkMergeDestination$' "$evidence/symbols-$role-luvia.nm"
  grep -Eq '[.]appendMergedColumnar$' "$evidence/symbols-$role-luvia.nm"
  "$scratch/bin/benchrun" run "$checkout" "$scratch/bin/$role" "$package" -test.timeout=90s \
    -test.run='^Test(AppendMerged.*|MergeColumnar.*|Integration_(PlanSplitMerge|ColumnarFederation)|Resolved(NullRemainsPresent|ResponseUsesCanonicalFieldValues))$' \
    -test.count=1 -test.v > "$evidence/merge-$role.log"
  for test in AppendMergedSelectedKeyPositions Integration_ColumnarFederation ResolvedNullRemainsPresent ResolvedResponseUsesCanonicalFieldValues; do
    grep -Fq -- "--- PASS: Test$test " "$evidence/merge-$role.log"
  done
  if [[ "$role" != base ]]; then
    compile_diagnostic_fixture "$role" selected "$package" columnar_merge_diagnostic_test.go selected-wire.go.txt
    "$scratch/bin/benchrun" run "$checkout" "$scratch/bin/$role-selected" "$package" -test.timeout=90s \
      -test.run='^TestDiagnosticMergeSelectedNullable$' -test.count=1 -test.v > "$evidence/selected-$role.log"
    grep -Fq -- '--- PASS: TestDiagnosticMergeSelectedNullable/row ' "$evidence/selected-$role.log"
    grep -Fq -- '--- PASS: TestDiagnosticMergeSelectedNullable/columnar-128 ' "$evidence/selected-$role.log"
  fi
done
sample_groups
verify_output samples
for role in base head candidate; do
  cp "$evidence/binaries-$role.txt" "$scratch/before-$role.txt"
  record_binaries "$role"
  cmp "$scratch/before-$role.txt" "$evidence/binaries-$role.txt"
done
analyze_groups
grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"
