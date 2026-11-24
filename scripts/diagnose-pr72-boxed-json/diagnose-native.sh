#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

[[ $# == 3 ]] || { printf 'usage: bash diagnose-native.sh <repository> <a|b> <formal-shard-12.zip>\n' >&2; exit 2; }
repo=$(git -C "$1" rev-parse --show-toplevel)
choice=$2
formal=$(cd "$(dirname "$3")" && pwd -P)/$(basename "$3")
asset=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
case "$choice" in
  a) digest=f00e35e2c75e673373b2d19605ffe97f901317293b0ced21242169e216cc1495 ;;
  b) digest=8ef4193bda342caf699b1230f6c103ff075592de4dc6621b45a87e48cf7c491a ;;
  *) exit 2 ;;
esac
base=4a660a0bcdee1eeb3c7a9dddcc95324d7967bec0
head=8dcb6c8b97615f5148c43ff15e43b93320e1a342
tested=5d9687a90092b629bc1ce7f57f28feead51e0516
tree=147e2705840657a207d6548c415d8d6c30a84228
cpu='AMD EPYC 9V74 80-Core Processor'
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
evidence=$(mktemp -d "$repo/.tmp/pr72-boxed-json-evidence-$choice.XXXXXXXX")
printf 'base=%s\nhead=%s\ntested=%s\nhead_tree=%s\ncandidate=%s\n' "$base" "$head" "$tested" "$tree" "$choice" > "$evidence/revisions.txt"
uname -a > "$evidence/host.txt"
if [[ "$(uname -sm)" != 'Linux x86_64' ]]; then exit 3; fi
lscpu > "$evidence/lscpu.txt"
mapfile -t actual < <(sed -n 's/^Model name:[[:space:]]*//p' "$evidence/lscpu.txt")
if (( ${#actual[@]} != 1 )) || [[ "${actual[0]}" != "$cpu" ]]; then exit 3; fi
export GOAMD64=v1 CGO_ENABLED=1 GOTOOLCHAIN=local GOWORK=off
[[ "$(go env GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'go1.27.1\nlinux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS GOEXPERIMENT CC > "$evidence/go-env.json"
test "$(git -C "$repo" rev-parse "$head^{tree}")" = "$tree"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = c31f7a87236149f126e6ac9edaeab2ccf3fc0076
printf '%s  %s\n' 67c6414b54e7410188c0227b7266c30a16c9c0863fea7a1b8433c281feaddb25 "$formal" | sha256sum -c -
[[ "$(unzip -p "$formal" benchmark-revisions.txt)" == "$(printf 'base_sha=%s\ntested_sha=%s' "$base" "$tested")" ]]
unzip -p "$formal" benchmark-confirmation-targets.tsv | grep -Fx $'pkg/lux/api\tBenchmarkBinaryJSONDocuments/1/boxed-2'
python3 - "$formal" "$cpu" <<'PY'
import sys
from zipfile import ZipFile

target = "BenchmarkBinaryJSONDocuments/1/boxed-2"
with ZipFile(sys.argv[1]) as z:
    for group in ("primary", "confirmation"):
        for revision in ("base", "head"):
            rows = z.read(f"benchmark-raw-{group}-{revision}.txt").decode().splitlines()
            assert {line.removeprefix("cpu: ").strip() for line in rows if line.startswith("cpu: ")} == {sys.argv[2]}
            assert sum(line.startswith(target + " ") or line.startswith(target + "\t") for line in rows) == 10
PY
for patch in "candidate-$choice.patch" regression-tests.patch; do
  if [[ -f "$asset/$patch" ]]; then
    cp "$asset/$patch" "$evidence/"
  else
    base64 --decode "$asset/$patch.b64" > "$evidence/$patch"
  fi
done
printf '%s  %s\n' "$digest" "$evidence/candidate-$choice.patch" \
  e8fcaef135dd9ea662a19e0381ef7fc6474e4f866279ad1d91a7d3f73c0af50f "$evidence/regression-tests.patch" | sha256sum -c -

scratch=$(mktemp -d "$repo/.tmp/pr72-boxed-json-scratch-$choice.XXXXXXXX")
trap 'rm -rf -- "$scratch"' EXIT
export TMPDIR="$scratch/tmp"
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source"
for role in base head candidate; do
  revision=$head
  if [[ "$role" == base ]]; then revision=$base; fi
  dir="$scratch/source/$role"
  mkdir -p "$dir/.tmp"
  git -C "$repo" archive "$revision" | tar -xf - -C "$dir"
  sh "$dir/scripts/setup-modules.sh" "$dir"
done
apply() {
  local dir=$1 patch=$2
  shift 2
  GIT_CEILING_DIRECTORIES="$scratch/source" git -C "$dir" apply "$@" "$patch"
}
apply "$scratch/source/candidate" "$evidence/candidate-$choice.patch"
for role in head candidate; do
  dir="$scratch/source/$role"
  apply "$dir" "$evidence/regression-tests.patch"
  (
    cd "$dir"
    export GOWORK="$dir/go.work"
    go test ./pkg/lux/api ./pkg/lux/codec -run '^Test(EncodeBinaryRequest(BoxedJSONDocuments|RejectsInvalidBoxedJSONDocuments|RejectsOversizedArrays)|ArrayHeader(WriteBounds|WriteCapacity|RejectsEmptyDestinationsWithoutMutation|PreallocatedWritesDoNotAllocate))$' -count=1 -v
  ) > "$evidence/$role-correctness.log" 2>&1
  apply "$dir" "$evidence/regression-tests.patch" -R
done
for role in base head candidate; do
  (cd "$scratch/source/$role" && sha256sum pkg/lux/api/{binary.go,binary_test.go,binary_bench_test.go,json_params.go} pkg/lux/codec/{encoder.go,wire.go,array_write_bench_test.go}) > "$evidence/source-$role.sha256"
done
for fixture in pkg/lux/api/{binary_test.go,binary_bench_test.go} pkg/lux/codec/array_write_bench_test.go; do
  cmp "$scratch/source/head/$fixture" "$scratch/source/candidate/$fixture"
done
(
  cd "$scratch/source/head"
  for tool in benchrun benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  (cd "$scratch" && go mod download -json golang.org/x/perf@v0.0.0-20260825160852-19be9d8e6c70) > "$evidence/benchstat-module.json"
  perf_dir=$(jq -er 'select(.Sum == "h1:M+kM03JKR23KkrutTue5KEQYDSbZTZTJ4FJ1v5zYrHM=") | .Dir' "$evidence/benchstat-module.json")
  (cd "$perf_dir" && go build -o "$scratch/bin/benchstat" ./cmd/benchstat)
  go version -m "$scratch/bin/benchstat"
) > "$evidence/tools.log" 2>&1

runner="$scratch/bin/benchrun"
planner="$scratch/bin/benchplan"
gate="$scratch/bin/benchgate"
"$runner" performance-profile > "$evidence/performance-profile.txt"
python3 - "$evidence/expected.json" <<'PY'
import json
import pathlib
import sys

leaves = [{"Package": "pkg/lux/api", "Name": f"BenchmarkBinaryJSONDocuments/{n}/{kind}-2"}
          for n in (0, 1, 128) for kind in ("boxed", "typed")]
leaves.extend({"Package": "pkg/lux/codec", "Name": f"BenchmarkArrayHeaderWrite/{n}-2"}
              for n in (0, 128, 1000000, 1000001, -1))
pathlib.Path(sys.argv[1]).write_text(json.dumps({"Base": leaves, "Head": leaves}) + "\n")
PY
packages=(pkg/lux/api pkg/lux/codec)
profiles=(api-non-identity default)
roots=('^BenchmarkBinaryJSONDocuments$' '^BenchmarkArrayHeaderWrite$')
for role in base head candidate; do
  compile=compile-performance
  if [[ "$role" == base ]]; then compile=compile-performance-baseline; fi
  for i in 0 1; do
    "$runner" "$compile" "$scratch/source/$role" "$scratch/bin/$role" "${packages[i]}" "${profiles[i]}"
  done > "$evidence/compile-$role.log" 2>&1
  (cd "$scratch/bin/$role" && sha256sum -- ./*.test) > "$evidence/binaries-$role.sha256"
done

mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tside\trevision\tpackage\tprofile\tfile\n' > "$evidence/order.tsv"
comparisons=(aa ah hc ac)
roles_for() {
  case "$1" in
    aa) left=base; right=base ;;
    ah) left=base; right=head ;;
    hc) left=head; right=candidate ;;
    ac) left=base; right=candidate ;;
  esac
}
run_leg() {
  local side=$1 role=$2 i=$3 file
  file="raw/$group.$sample.$comparison.$i.$side.txt"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$group" "$sample" "$comparison" "$side" "$role" \
    "${packages[i]}" "${profiles[i]}" "$file" >> "$evidence/order.tsv"
  "$runner" run-performance "$scratch/source/$role" "$scratch/bin/$role" "${packages[i]}" "${profiles[i]}" \
    -test.run='^$' -test.bench="${roots[i]}" -test.benchmem -test.benchtime=200ms -test.count=1 -test.cpu=2 -test.timeout=90s \
    > "$evidence/$file" 2>> "$evidence/stderr.txt"
  [[ "$(sed -n 's/^cpu: //p' "$evidence/$file" | sed 's/[[:space:]]*$//')" == "$cpu" ]]
}
for group in primary confirmation; do
  offset=0
  if [[ "$group" == confirmation ]]; then offset=1; fi
  for pair in {1..10}; do
    printf -v sample '%02d' "$pair"
    for position in {0..3}; do
      comparison="${comparisons[(position + pair + offset) % 4]}"
      roles_for "$comparison"
      for slot in 0 1; do
        i=$(((slot + pair + offset) % 2))
        if (((pair + offset) % 2)); then
          run_leg left "$left" "$i"
          run_leg right "$right" "$i"
        else
          run_leg right "$right" "$i"
          run_leg left "$left" "$i"
        fi
      done
    done
  done
done

printf 'comparison\tgate_exit\n' > "$evidence/verdicts.tsv"
declare -A status
for comparison in "${comparisons[@]}"; do
  for group in primary confirmation; do
    stem="$evidence/$comparison.$group"
    for side in left right; do
      cat "$evidence"/raw/"$group".*."$comparison".*."$side".txt > "$scratch/combined.txt"
      "$planner" --normalize "$scratch/combined.txt" "$stem.$side.txt"
    done
    "$planner" --verify "$evidence/expected.json" "$stem.left.txt" "$stem.right.txt"
    "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
      base="$stem.left.txt" head="$stem.right.txt" > "$stem.csv" 2> "$stem.warnings.txt"
  done
  args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
  if [[ "$comparison" == ah || "$comparison" == ac ]]; then
    args=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base" "${args[@]}")
  fi
  rc=0
  "$gate" "${args[@]}" > "$evidence/$comparison.gate.log" 2>&1 || rc=$?
  if ((rc != 0)); then grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.gate.log"; fi
  printf '%s\t%s\n' "$comparison" "$rc" >> "$evidence/verdicts.tsv"
  status[$comparison]=$rc
done
cat "$evidence/verdicts.tsv"
if [[ "${status[aa]}" != 0 ]]; then
  printf 'Inconclusive: the same-build control failed.\n' >&2
  exit 3
fi
if [[ "${status[ah]}" != 1 ]] || ! grep -Fq 'confirmed performance regression: github.com/light-speak/luxo/pkg/lux/api/BinaryJSONDocuments/1/boxed-2 sec/op' "$evidence/ah.gate.log"; then
  printf 'Inconclusive: the current head did not reproduce the boxed target.\n' >&2
  exit 3
fi
[[ "${status[hc]}" == 0 && "${status[ac]}" == 0 ]]
python3 - "$evidence" <<'PY'
import csv
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
for comparison in ("hc", "ac"):
    for group in ("primary", "confirmation"):
        unit = None
        improved = False
        with (root / f"{comparison}.{group}.csv").open() as reader:
            for row in csv.reader(reader):
                if len(row) == 7 and not row[0] and row[1] in ("sec/op", "B/op", "allocs/op"):
                    unit = row[1]
                if len(row) != 7 or not row[0].startswith(("BinaryJSONDocuments/", "ArrayHeaderWrite/")):
                    continue
                if row[0] == "BinaryJSONDocuments/1/boxed-2" and unit == "sec/op":
                    improved = row[5].startswith("-")
                if unit in ("B/op", "allocs/op") and float(row[3]) > float(row[1]):
                    raise SystemExit(f"allocation increased: {comparison} {group} {row[0]} {unit}")
        if comparison == "hc" and not improved:
            raise SystemExit(f"the target did not improve significantly: {group}")
PY
printf 'The paired target improved and the existing gate accepted the candidate.\n'
