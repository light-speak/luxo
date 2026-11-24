#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
mode="${1:-run}"
[[ $# -le 1 && ( "$mode" == run || "$mode" == --plan-only ) ]]
source "$script_dir/diagnose-hosted-perf.sh"
base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
base_tree=ba252c7cace8908cdfcd7c203bc9dc11bb2ba746
candidate=3a9b9a316f06b9f1cc3a3d7007bff27765998a1c
patch_sha=7189b019741deb212baa05fe0b41a2d2b372cd01d6a5491bf2d516421ba5eeb8
[[ "$(sha256sum "$script_dir/diagnose-hosted-perf.sh" | awk '{print $1}')" == 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 ]]
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/cli-add-shared-names.XXXXXXXX")
evidence="$repo/.tmp/hosted-cli-add-shared-names"
if [[ "$mode" == --plan-only ]]; then evidence="$scratch/evidence"; fi
evidence_created=false

cleanup_shared_names() {
  local result=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/cli-add-shared-names.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$evidence_created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$result" > "$evidence/cleanup.txt"
  fi
  exit "$result"
}
trap cleanup_shared_names EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
evidence_created=true

asset="$repo/.github/qualification/cli-add.patch.b64"
python3 - "$asset" "$evidence/candidate.patch" <<'PY'
from base64 import b64decode
from pathlib import Path
import sys

asset, destination = map(Path, sys.argv[1:])
destination.write_bytes(b64decode(asset.read_bytes()))
PY
printf '%s  %s\n' "$patch_sha" "$evidence/candidate.patch" | sha256sum -c -
store="$scratch/git"
git init --bare --quiet "$store"
git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$store/objects/info/alternates"
test "$(git --git-dir="$store" rev-parse "$base^{tree}")" = "$base_tree"
git --git-dir="$store" read-tree "$base"
git --git-dir="$store" apply --cached --whitespace=error "$evidence/candidate.patch"
test "$(git --git-dir="$store" write-tree)" = "$candidate"
git --git-dir="$store" diff --cached --name-only "$base" > "$evidence/product-paths.txt"
test "$(wc -l < "$evidence/product-paths.txt" | tr -d ' ')" = 12
git --git-dir="$store" apply --cached --reverse --whitespace=error "$evidence/candidate.patch"
test "$(git --git-dir="$store" write-tree)" = "$base_tree"
printf 'base=%s\nbase_tree=%s\nproduct_tree=%s\npatch_sha256=%s\n' "$base" "$base_tree" "$candidate" "$patch_sha" > "$evidence/revisions.txt"

cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/lux/str	default	BenchmarkToSnakeCase-2	^BenchmarkToSnakeCase$
02	pkg/codegen	default	BenchmarkFieldIdentifierValidation/1-2	^BenchmarkFieldIdentifierValidation$/^(1|128)$
CASES
python3 - "$evidence" <<'PY'
import json
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
leaves = [
    {"Package": "pkg/lux/str", "Name": "BenchmarkToSnakeCase-2"},
    {"Package": "pkg/codegen", "Name": "BenchmarkFieldIdentifierValidation/1-2"},
    {"Package": "pkg/codegen", "Name": "BenchmarkFieldIdentifierValidation/128-2"},
]
(evidence/"expected.json").write_text(json.dumps({"Base": leaves, "Head": leaves}, indent=2) + "\n")
PY
git --git-dir="$store" show "$base_tree:pkg/lux/str/str_test.go" > "$scratch/str-fixture.txt"
git --git-dir="$store" show "$base_tree:pkg/codegen/codegen_bench_test.go" > "$scratch/codegen-fixture.txt"
grep -q '^func BenchmarkToSnakeCase(' "$scratch/str-fixture.txt"
grep -q '^func BenchmarkFieldIdentifierValidation(' "$scratch/codegen-fixture.txt"
if [[ "$mode" == --plan-only ]]; then
  run_leg() {
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\traw/%s.%s.%s.%s.%s.txt\n' \
      "$1" "$2" "$3" "$6" "$4" "$5" "$8" "$1" "$2" "$3" "$6" "$4" >> "$evidence/order.tsv"
  }
  sample_groups
  test "$(( $(wc -l < "$evidence/order.tsv") - 1 ))" = 160
  cat "$evidence/revisions.txt" "$evidence/cases.tsv"
  printf 'Verified 160 planned benchmark processes.\n'
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir -p "$TMPDIR" "$scratch/source/base" "$scratch/source/head" "$scratch/bin/base" "$scratch/bin/head" "$evidence/pairs"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
git --git-dir="$store" archive "$base_tree" | tar -xf - -C "$scratch/source/base"
git --git-dir="$store" archive "$candidate" | tar -xf - -C "$scratch/source/head"
for role in base head; do
  dir="$scratch/source/$role"
  mkdir "$dir/.tmp"
  sh "$dir/scripts/setup-modules.sh" "$dir"
  cp "$dir/go.work" "$evidence/go-$role.work.txt"
done
(
  cd "$repo"
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  cd "$scratch/source/base"
  for tool in benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/tools.log" 2>&1

for package in pkg/lux/str pkg/codegen; do
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
done
for role in base head; do
  record_binaries "$role"
  while IFS=$'\t' read -r id package variant name filter; do
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      -test.timeout=90s > "$evidence/inventory-$role-$id.txt"
  done < "$evidence/cases.tsv"
done
python3 - "$evidence" <<'INVENTORY'
from pathlib import Path
import json
import sys

e = Path(sys.argv[1])
expected = json.loads((e/"expected.json").read_text())["Base"]
for case in (e/"cases.tsv").read_text().splitlines():
    index, package, _, _, _ = case.split("\t")
    names = sorted(row["Name"] for row in expected if row["Package"] == package)
    for role in ("base", "head"):
        path = e/f"inventory-{role}-{index}.txt"
        lines = path.read_text().splitlines()
        rows = [line.split() for line in lines if line.startswith("Benchmark")]
        if (sorted(row[0] for row in rows) != names or lines[-1:] != ["PASS"]
                or not all(len(row) == 8 and row[1] == "1" and row[3::2] == ["ns/op", "B/op", "allocs/op"] for row in rows)):
            raise SystemExit(f"unexpected 1x inventory: {path}")
INVENTORY

sample_groups
test "$(( $(wc -l < "$evidence/order.tsv") - 1 ))" = 160
grep -h '^cpu: ' "$evidence"/inventory-*.txt "$evidence"/raw/*.txt | sort -u > "$evidence/cpu-consistency.txt"
test "$(wc -l < "$evidence/cpu-consistency.txt" | tr -d ' ')" = 1
analyze_groups
awk 'NR>1 && $2 != 0 {failed=1} END {exit failed}' "$evidence/verdicts.tsv"
