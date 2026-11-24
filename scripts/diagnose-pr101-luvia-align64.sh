#!/usr/bin/env bash
set -euo pipefail
umask 077

main_commit=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
head_commit=8f6633bfc9f325c857e1217112a5e2039e2d85de
diagnostics=61521c06948d6f4cdf04cd94abb6a19fdd2c5ac7
package=pkg/lux/luvia
repo=$(git rev-parse --show-toplevel)
mode="${1:---measure}"
[[ $# -le 1 && ( "$mode" == --measure || "$mode" == --plan-only ) ]] || exit 2
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr101-luvia-align.XXXXXXXX")
cleanup_owned() {
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr101-luvia-align.* ]] || exit 99
  rm -rf -- "$scratch"
}
trap cleanup_owned EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
evidence="$scratch/plan"
if [[ "$mode" == --measure ]]; then evidence="$repo/.tmp/hosted-pr101-luvia-align64"; fi
mkdir "$evidence" "$scratch/bin" "$scratch/tmp"
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local

asset() {
  git -C "$repo" show "$diagnostics:$1" > "$evidence/$2"
  printf '%s  %s\n' "$3" "$evidence/$2" | sha256sum -c - || exit 1
}
asset scripts/diagnose-hosted-perf.sh sampler.sh 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
asset scripts/diagnose-pr78-pr85-attribution.py schedule-helper.py 4eb13c3ace4612e295e9d490d48a8368dc422572e3557bf5f0fc66b846fc1dfb
source "$evidence/sampler.sh"

[[ "$(git -C "$repo" rev-parse "$main_commit^{tree}")" == ba252c7cace8908cdfcd7c203bc9dc11bb2ba746 ]] || exit 1
[[ "$(git -C "$repo" rev-parse "$head_commit^")" == "$main_commit" ]] || exit 1
[[ "$(git -C "$repo" rev-parse "$head_commit^{tree}")" == 74f2f384715be4780412fa34a60ca997158fe058 ]] || exit 1
git -C "$repo" diff --exit-code "$main_commit" "$head_commit" -- "$package"
git -C "$repo" diff --name-only "$main_commit" "$head_commit" > "$evidence/changed-files.txt"
diff -u <(printf '%s\n' pkg/lux/h2transport/request_headers.go pkg/lux/h2transport/request_headers_test.go) "$evidence/changed-files.txt"
printf 'prior_run=45359603386\nprior_artifact=14905400185\nprior_sha256=37daa6c7e7fb4b9c89e4a8a005b88963738787dcb7dd03d3e5ad537ab97f8cd4\n' > "$evidence/provenance.txt"
printf 'base=%s\nhead=%s\ntested_merge=3cfae197567b8b6e3ba277aa3cc7dd35675e455b\n' "$main_commit" "$head_commit" >> "$evidence/provenance.txt"

verify_plan() {
  python3 - "$evidence" "$1" <<'PY'
from pathlib import Path
import runpy
import sys

dest, mode = Path(sys.argv[1]), sys.argv[2]
helper = runpy.run_path(str(dest / "schedule-helper.py"))
source = helper["schedule"].__globals__
leaves = [
    "BenchmarkTraceEncodingComparison/true/previous",
    "BenchmarkTraceEncodingComparison/true/static-reference",
    "BenchmarkTraceEncodingComparison/true/reservation-candidate",
    "BenchmarkAuthMiddlewareIsolation/anonymous/before",
    "BenchmarkAuthMiddlewareIsolation/anonymous/after",
    "BenchmarkGatewayIdentity/anonymous/before",
    "BenchmarkGatewayIdentity/anonymous/after",
]
filter_string = "|".join("/".join("^" + part + "$" for part in name.split("/")) for name in leaves)
package = "pkg/lux/luvia"
source["CASES"] = {i: (package, variant, filter_string, leaves) for i, variant in
                   (("01", "default"), ("02", "align64"))}
source["COMPARISONS"] = [
    ("default-aa", "base", "base", ("01",)),
    ("default-ab", "base", "head", ("01",)),
    ("align64-aa", "base", "base", ("02",)),
    ("align64-ab", "base", "head", ("02",)),
]
if mode in ("inventory", "samples"):
    source["verify"](dest, mode == "samples")
    raise SystemExit(0)
assert mode == "plan"
rows = list(source["schedule"]())
assert len(rows) == len({row["raw"] for row in rows}) == 160
source["write_tsv"](dest / "schedule.tsv", ([row[key] for key in source["SCHEDULE_KEYS"]] for row in rows))
source["write_tsv"](dest / "inventory.tsv", ([role, case, *source["CASES"][case][:3]]
                                              for role, case in source["inventory"]()))
source["write_tsv"](dest / "comparisons.tsv", ((name,) for name, *_ in source["COMPARISONS"]))
keys = [{"Package": package, "Name": name + "-2"} for name in leaves]
source["write_json"](dest / "expected.json", {"Base": keys, "Head": keys})
print("4 inventory processes, 160 measurement arms, 1120 observations, all seven original Luvia leaves.")
PY
}

verify_plan plan
if [[ "$mode" == --plan-only ]]; then exit 0; fi
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]] || exit 1
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]] || exit 1
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]] || exit 1
collect_host > "$evidence/environment.txt"

prepare_source() {
  local role="$1" revision="$2" source="$scratch/source/$1"
  mkdir -p "$source"
  git -C "$repo" archive "$revision" | tar -xf - -C "$source"
  sh "$source/scripts/setup-modules.sh" "$source"
  cp "$source/go.work" "$evidence/go-$role.work.txt"
  (cd "$source" && sha256sum pkg/lux/h2transport/request_headers{,_test}.go "$package"/*_test.go) \
    > "$evidence/source-$role.sha256"
}
prepare_source base "$main_commit"
prepare_source head "$head_commit"
(
  cd "$scratch/source/base"
  for tool in benchrun benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat"
) > "$evidence/tools.log" 2>&1

layout() {
  local role="$1" binary="$scratch/bin/$1/pkg-lux-luvia.test"
  local pattern='luvia[.](Identity$|appendTraceRecord$|AuthMiddleware|Benchmark(GatewayIdentity|AuthMiddlewareIsolation|TraceEncodingComparison))|h2transport[.]parseRequestHead$'
  go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern && NF == 4' > "$evidence/$role.nm.txt"
  grep -Eq 'h2transport[.]parseRequestHead$' "$evidence/$role.nm.txt"
  go tool objdump -s "$pattern" "$binary" > "$evidence/$role.objdump.txt"
  python3 - "$evidence/$role.nm.txt" "$role" <<'PY'
from pathlib import Path
import sys

names = ("Identity", "appendTraceRecord", "BenchmarkGatewayIdentity.func1.1",
         "BenchmarkTraceEncodingComparison.func3")
symbols = {line.split()[3]: int(line.split()[0], 16) for line in Path(sys.argv[1]).read_text().splitlines()}
for name in names:
    address = symbols["github.com/light-speak/luxo/pkg/lux/luvia." + name]
    if sys.argv[2].endswith("-align64") and address % 64:
        raise SystemExit(f"aligned link left {name} unaligned at {address:#x}")
    print(f"{name}\t{address:#x}\t{address % 64}")
PY
}

printf '%s\n' \
  "fd57751012f30efbfc78cbda058030f6f6cd5fc15636227900245077b9038f7f  $scratch/bin/base/pkg-lux-luvia.test" \
  "359613f7e72a82f3b2c27b7822d0ab5dba8a8daec5d2b8edd58cc798c7216824  $scratch/bin/head/pkg-lux-luvia.test" \
  > "$evidence/default-ELF-expected.txt"
for role in base head; do
  "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" "$package" > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
done
sha256sum -c "$evidence/default-ELF-expected.txt" || exit 1
for role in base head; do
  GOFLAGS=-ldflags=-funcalign=64 "$scratch/bin/benchrun" compile \
    "$scratch/source/$role" "$scratch/bin/$role-align64" "$package" > "$evidence/compile-$role-align64.log" 2>&1
  record_binaries "$role-align64"
  for suffix in '' -align64; do
    layout "$role$suffix" > "$evidence/$role$suffix-alignment.tsv"
  done
done

while IFS=$'\t' read -r role case_id pkg variant filter; do
  bin="$scratch/bin/$role"
  [[ "$variant" == default ]] || bin+="-$variant"
  "$scratch/bin/benchrun" run "$scratch/source/$role" "$bin" "$pkg" \
    -test.run='^$' -test.bench="$filter" -test.benchtime=1x -test.benchmem -test.cpu=2 -test.timeout=90s \
    > "$evidence/inventory-$role-$case_id.txt" 2>> "$evidence/benchmark-stderr.txt"
done < "$evidence/inventory.tsv"
verify_plan inventory
mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
while IFS=$'\t' read -r group pair comparison case_id side role variant raw pkg filter; do
  run_leg "$group" "$pair" "$comparison" "$side" "$role" "$case_id" "$pkg" "$variant" "$filter"
done < "$evidence/schedule.tsv"
verify_plan samples

validate_outcome() {
  local comparison leaf prefix invalid=0
  for comparison in default-aa align64-aa; do
    if ! grep -Fxq "$comparison"$'\t0' "$evidence/verdicts.tsv"; then
      printf 'inconclusive: %s control did not pass\n' "$comparison" >&2
      invalid=1
    fi
  done
  if ! grep -Fxq $'default-ab\t1' "$evidence/verdicts.tsv"; then
    printf 'inconclusive: default A/B did not reject\n' >&2
    invalid=1
  fi
  for leaf in AuthMiddlewareIsolation/anonymous/after-2 GatewayIdentity/anonymous/before-2 \
    TraceEncodingComparison/true/reservation-candidate-2; do
    prefix="confirmed performance regression: github.com/light-speak/luxo/$package/$leaf sec/op "
    if ! awk -v prefix="$prefix" 'index($0, prefix) == 1 { found=1 } END { exit !found }' \
      "$evidence/default-ab.benchgate.log"; then
      printf 'inconclusive: default A/B did not reproduce %s\n' "$leaf" >&2
      invalid=1
    fi
  done
  return "$invalid"
}

printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
while IFS= read -r comparison; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  status=0
  "$scratch/bin/benchgate" "$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv" \
    > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
  printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
  if (( status != 0 )); then
    [[ "$status" == 1 ]] || exit "$status"
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log" || exit 1
  fi
done < "$evidence/comparisons.tsv"
cat "$evidence/verdicts.tsv"
validate_outcome
