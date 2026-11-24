#!/usr/bin/env bash
set -euo pipefail

repo=$(git rev-parse --show-toplevel)
mode="${1:-run}"
case "$mode" in
  run|--inventory-only) test "$#" -le 1 ;;
  --plan-only) test "$#" -eq 2 ;;
  *) printf 'usage: %s [--inventory-only | --plan-only archived-plan.zip]\n' "$0" >&2; exit 2 ;;
esac

fixture_revision=615f5beb44786a4c9484cfc732204ad059e99435
sampler_revision=61521c06948d6f4cdf04cd94abb6a19fdd2c5ac7
head_tree=74f2f384715be4780412fa34a60ca997158fe058
base_tree=ba252c7cace8908cdfcd7c203bc9dc11bb2ba746
rpc=pkg/lux/rpc
omitted="$rpc/public_boundary_test.go"

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr101-link-context.XXXXXXXX")
results="$repo/.tmp/pr101-link-context-evidence"
if [[ "$mode" != run ]]; then results="$scratch/evidence"; fi
results_created=false
cleanup_context() {
  local status=$?
  trap - EXIT
  [[ "$scratch" == "$repo"/.tmp/pr101-link-context.* && -d "$scratch" && ! -L "$scratch" ]] || exit 99
  if (( status != 0 )) && [[ "$mode" != run && -f "$results/build.log" ]]; then
    tail -n 80 "$results/build.log" >&2
  fi
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$results_created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$results/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_context EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$results"
results_created=true
mkdir "$scratch/bin" "$scratch/tmp" "$scratch/source"
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local LC_ALL=C

git -C "$repo" show "$sampler_revision:scripts/diagnose-hosted-perf.sh" > "$scratch/sampler.sh"
printf '%s  %s\n' 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 "$scratch/sampler.sh" | sha256sum -c -
source "$scratch/sampler.sh"
base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
head=8f6633bfc9f325c857e1217112a5e2039e2d85de
test "$(git -C "$repo" rev-parse "$head^")" = "$base"
test "$(git -C "$repo" rev-parse "$head^{tree}")" = "$head_tree"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = "$base_tree"
git -C "$repo" diff --exit-code "$fixture_revision" -- scripts/benchmark-fixtures.sh \
  scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout
git -C "$repo" diff --name-only "$base" "$head" > "$results/product-paths.txt"
printf '%s\n' pkg/lux/h2transport/request_headers.go pkg/lux/h2transport/request_headers_test.go | cmp "$results/product-paths.txt" -
printf '%s\n' "base $base $base_tree" "head $head $head_tree" \
  "fixtures $fixture_revision" "sampler $sampler_revision" > "$results/revisions.txt"
printf '%s\n' '45342199613 f98ac550f672fdc381ef3c43136d3fada91d381a75212e4e3f7b9e3b195ab05b native' \
  '45353057578 67b39cf42bb83638f20d8537c9417e59c943abe929944e334f83543a625ec353 frozen' > "$results/historical-receipts.txt"
printf '%s\n' pkg/lux/h2transport pkg/lux/luvia "$rpc" > "$scratch/benchmark-packages.txt"
cat > "$results/targets.tsv" <<'CASES'
pkg/lux/h2transport	BenchmarkRequestHead-2	direct
pkg/lux/h2transport	BenchmarkReceiveUnary4KiB-2	direct
pkg/lux/luvia	BenchmarkAuthMiddlewareIsolation/anonymous/after-2	recurring
pkg/lux/luvia	BenchmarkAuthMiddlewareIsolation/anonymous/before-2	context
pkg/lux/luvia	BenchmarkGatewayIdentity/anonymous/before-2	recurring
pkg/lux/luvia	BenchmarkGatewayIdentity/anonymous/after-2	context
pkg/lux/luvia	BenchmarkTraceEncodingComparison/true/reservation-candidate-2	recurring
pkg/lux/luvia	BenchmarkTraceEncodingComparison/true/previous-2	context
pkg/lux/luvia	BenchmarkTraceEncodingComparison/true/static-reference-2	context
pkg/lux/rpc	BenchmarkLRPCDirectRequest/0/traced-after-2	recurring
pkg/lux/rpc	BenchmarkLRPCDirectRequest/0/traced-before-2	context
pkg/lux/rpc	BenchmarkRPCDeadlineClassification/ExpiredEOF/After-2	recurring
pkg/lux/rpc	BenchmarkRPCDeadlineClassification/ExpiredEOF/Before-2	context
pkg/lux/rpc	BenchmarkRPCNilContextGuard/NoToken/After-2	recurring
pkg/lux/rpc	BenchmarkRPCNilContextGuard/NoToken/Before-2	context
pkg/lux/rpc	BenchmarkRPCNilContextGuard/NoToken/GuardOnly-2	context
CASES

make_plan() {
  local suite="$1" inventory="$2" scope="$3" prefix="$results/$1" package label
  local -a receipts=()
  mkdir "$prefix"
  if [[ "$suite" == isolated ]]; then
    awk -F '\t' -v pkg="$rpc" '$1 == pkg {print $1 "\t" $2}' "$results/targets.tsv" > "$prefix/targets.tsv"
  else
    cut -f1,2 "$results/targets.tsv" > "$prefix/targets.tsv"
  fi
  while IFS= read -r package; do
    label="${package//\//-}"
    receipts+=("$inventory/$label/inventory.json")
  done < "$scratch/benchmark-packages.txt"
  jq -es --arg scope "$scope" '{
    Base: map(.[$scope].Base) | add, Head: map(.[$scope].Head) | add
  }' "${receipts[@]}" > "$prefix/full-inventory.json"
  "$scratch/bin/benchplan" --confirmation-plan "$prefix/full-inventory.json" "$prefix/targets.tsv" \
    "$prefix/batch.tsv" "$prefix/expected.json"
  awk -F '\t' -v suite="$suite" '
    NR==FNR {if (!($1 in names)) names[$1]=$2; next}
    {printf "%02d\t%s\t%s\t%s\t%s\n", FNR, $1, suite, names[$1], $2}
  ' "$prefix/targets.tsv" "$prefix/batch.tsv" > "$prefix/cases.tsv"
  test "$(jq '.Base|length' "$prefix/expected.json")" -eq "$(wc -l < "$prefix/targets.tsv")"
}

plan_all() {
  local inventory="$1"
  make_plan native "$inventory" Native
  make_plan frozen "$inventory" Frozen
  make_plan isolated "$inventory" Native
  cmp "$results/native/expected.json" "$results/frozen/expected.json"
  test "$(wc -l < "$results/native/cases.tsv")" -eq 3
  test "$(wc -l < "$results/frozen/cases.tsv")" -eq 3
  test "$(wc -l < "$results/isolated/cases.tsv")" -eq 1
}

validate_inventory() {
  local directory="$1" role="$2" id="$3" package="$4"
  # The sampler normalizer has already validated measurement syntax and outcomes.
  python3 - "$directory" "$role" "$id" "$package" <<'PY'
from pathlib import Path
import json
import sys

root = Path(sys.argv[1])
role, case, package = sys.argv[2:]
path = root / f"inventory-{role}-{case}.txt"
rows = path.read_text().splitlines()
expected = json.loads((root / "expected.json").read_text())["Base"]
names = sorted(key["Name"] for key in expected if key["Package"] == package)
measurements = [line.split() for line in rows if line.startswith("Benchmark")]
metric = ["ns/op", "B/op", "allocs/op"]
if (rows[-1:] != ["PASS"]
    or [line for line in rows if line.startswith("pkg: ")] != [f"pkg: github.com/light-speak/luxo/{package}"]
    or len([line for line in rows if line.startswith("cpu: ")]) != 1
    or sorted(row[0] for row in measurements) != names
    or not all(len(row) == 8 and row[1] == "1" and row[3::2] == metric for row in measurements)):
    raise SystemExit(f"invalid selected 1x inventory: {path}")
PY
}

inventory_suite() {
  local suite="$1" role id package variant name filter directory="$results/$1"
  for role in base head; do
    while IFS=$'\t' read -r id package variant name filter; do
      "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role-$variant" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x \
        -test.count=1 -test.cpu=2 -test.timeout=90s > "$directory/inventory-$role-$id.raw"
      "$scratch/bin/benchplan" --normalize "$directory/inventory-$role-$id.raw" "$directory/inventory-$role-$id.txt"
      validate_inventory "$directory" "$role" "$id" "$package"
    done < "$directory/cases.tsv"
  done
}

list_dependencies() {
  local role="$1" package="$2" variant="$3" label="${2//\//-}"
  shift 3
  (
    cd "$scratch/$role"
    GOWORK="$PWD/go.work" go list "$@" -deps -f '{{.ImportPath}}' "./$package" | sort -u
  ) > "$results/deps-$role-$label-$variant.txt"
}

isolate_rpc() {
  local role="$1" source="$scratch/$1" overlay="$results/overlay-$1.json" test_sha
  test -f "$source/$omitted"
  test_sha=$(sha256sum "$source/$omitted")
  jq -n --arg name "$source/$omitted" '{Replace: {($name): ""}}' > "$overlay"
  GOFLAGS="-overlay=$overlay" "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$role-isolated" "$rpc"
  GOFLAGS="-overlay=$overlay" list_dependencies "$role" "$rpc" isolated-production
  GOFLAGS="-overlay=$overlay" list_dependencies "$role" "$rpc" isolated-tests -test
  test "$(sha256sum "$source/$omitted")" = "$test_sha"
  printf '%s\n' "$test_sha" >> "$results/isolated-original-test.sha256"
  local deps="$results/deps-$role-pkg-lux-rpc"
  cmp "$deps-production.txt" "$deps-isolated-production.txt"
  grep -Fxq github.com/light-speak/luxo/pkg/lux/h2transport "$deps-tests.txt"
  if grep -Fqx github.com/light-speak/luxo/pkg/lux/h2transport "$deps-production.txt" "$deps-isolated-tests.txt"; then
    printf 'RPC isolation did not remove its test-only H2 import\n' >&2
    return 1
  fi
}

record_layout() {
  local role="$1" suite="$2" package="$3" label="${3//\//-}" binary stem regex
  binary="$scratch/bin/$role-$suite/$label.test"
  stem="$results/layout/$role-$suite-$label"
  case "$package" in
    "$rpc")
      regex='rpc\..*(Benchmark(LRPCDirectRequest|RPCDeadlineClassification|RPCNilContextGuard)|prepareTracedCallFrame|contextError(AfterIO|BeforeDeadlineFix)|requestContext|beforeNilContextGuard)'
      ;;
    pkg/lux/luvia)
      regex='luvia\..*(Benchmark(AuthMiddlewareIsolation|GatewayIdentity|TraceEncodingComparison)|AuthMiddleware|extractBearerToken|Identity|encodeTraces|traceJSONCapacity|appendTraceRecord)'
      ;;
    pkg/lux/h2transport)
      regex='h2transport\..*(Benchmark(RequestHead|ReceiveUnary4KiB)|parseRequestHead|requestReceiver.*(open|data|release))'
      ;;
  esac
  {
    sha256sum "$binary"
    go tool buildid "$binary"
    go version -m "$binary"
  } > "$stem.binary.txt"
  go tool nm -size -sort address "$binary" | awk -v re="$regex" '$NF ~ re' > "$stem.nm.txt"
  test -s "$stem.nm.txt"
  go tool objdump -s "$regex" "$binary" > "$stem.disassembly.txt"
  test -s "$stem.disassembly.txt"
  readelf -h -lW "$binary" > "$stem.elf.txt"
  readelf -sW "$binary" | awk -v re="$regex" 'NR <= 3 || $NF ~ re' > "$stem.elf-symbols.txt"
  python3 - "$stem.nm.txt" <<'PY' > "$stem.alignment.tsv"
from pathlib import Path
import sys

print("symbol\taddress\tsize\tmod64\tmod4096")
for line in Path(sys.argv[1]).read_text().splitlines():
    address, size, _, name = line.split(maxsplit=3)
    value = int(address, 16)
    print(f"{name}\t{address}\t{size}\t{value % 64}\t{value % 4096}")
PY
}

compile_roles() {
  local role package label both
  for role in base head; do
    both="$scratch/$role"
    mkdir -p "$both/.tmp"
    git -C "$repo" archive "${!role}" | tar -xf - -C "$both"
    sh "$repo/scripts/setup-modules.sh" "$both"
    cp "$both/go.work" "$results/go-$role.work.txt"
    ln -s "$both" "$scratch/source/$role"
    (
      cd "$both"
      sha256sum "$omitted" pkg/lux/h2transport/request_headers{,_test}.go
    ) > "$results/source-$role.sha256"
  done
  bash "$repo/scripts/benchmark-fixtures.sh" plan "$scratch" > "$results/discovery-matrix.json"
  cp -R "$scratch/fixture-discovery" "$scratch/benchmark-inventory" "$results/"
  plan_all "$scratch/fixture-discovery"
  for role in base head; do
    mkdir -p "$scratch/bin/$role-frozen"
    ln -s "$scratch/bin/native-$role" "$scratch/bin/$role-native"
    while IFS= read -r package; do
      label="${package//\//-}"
      ln -s "$scratch/pairs/$label/$role/$label.test" "$scratch/bin/$role-frozen/$label.test"
      list_dependencies "$role" "$package" production
      list_dependencies "$role" "$package" tests -test
    done < "$scratch/benchmark-packages.txt"
    isolate_rpc "$role"
  done
}

record_rpc_inventory() {
  local suite role out
  mkdir "$results/layout"
  for suite in native frozen isolated; do
    for role in base head; do
      out="$results/layout/rpc-$role-$suite.symbols"
      go tool nm "$scratch/bin/$role-$suite/pkg-lux-rpc.test" |
        awk '$NF ~ /\.Benchmark/ && $NF ~ /\/rpc(_test)?\./ {print $NF}' | sort -u > "$out"
      test -s "$out"
      cmp "$results/layout/rpc-base-native.symbols" "$out"
    done
  done
}

verify_scheduled_roles() {
  python3 - "$1" "$2" <<'PY'
from pathlib import Path
import csv
import sys

d = Path(sys.argv[1])
variant = sys.argv[2]
cases = [line.split("\t")[0] for line in (d / "cases.tsv").read_text().splitlines()]
expected = []
for group_index, group in enumerate(("primary", "confirmation")):
    for index in range(1, 11):
        for case in cases:
            sides = ["left", "right"] if (index + group_index) % 2 else ["right", "left"]
            comparisons = ["aa", "ab"] if (index + int(case) + group_index) % 2 else ["ab", "aa"]
            for comparison in comparisons:
                for side in sides:
                    role = "head" if comparison == "ab" and side == "right" else "base"
                    sample = f"{index:02}"
                    raw = f"raw/{group}.{sample}.{comparison}.{case}.{side}.txt"
                    expected.append([group, sample, comparison, case, side, role, variant, raw])
with (d / "order.tsv").open() as stream:
    rows = list(csv.reader(stream, delimiter="\t"))
if rows[1:] != expected or len({row[-1] for row in expected}) != len(expected):
    raise SystemExit(f"unexpected execution order or reused arm: {d}")
if (d / "raw").exists() and set(path.relative_to(d).as_posix() for path in (d / "raw").iterdir()) != {row[-1] for row in expected}:
    raise SystemExit(f"missing or additional raw arms: {d}")
print(f"{variant}: {len(expected)} independently ordered benchmark processes")
PY
}

run_suite() {
  local suite="$1"
  evidence="$results/$suite"
  sample_groups
  verify_scheduled_roles "$evidence" "$suite"
  grep -h '^cpu: ' "$evidence"/inventory-*.txt "$evidence"/raw/*.txt |
    sed 's/[[:blank:]]*$//' | sort -u > "$evidence/cpu.txt"
  test "$(wc -l < "$evidence/cpu.txt")" -eq 1
  analyze_groups
}

# Plan-only consumes the verified old four-role inventory rather than timing.
if [[ "$mode" == --plan-only ]]; then
  old_artifact="$2"
  mkdir "$scratch/old"
  while IFS= read -r package; do
    label="${package//\//-}"
    mkdir "$scratch/old/$label"
    unzip -p "$old_artifact" "fixture-discovery/$label/inventory.json" > "$scratch/old/$label/inventory.json"
  done < "$scratch/benchmark-packages.txt"
  go build -o "$scratch/bin/benchplan" "$repo/scripts/benchplan"
  plan_all "$scratch/old"
  for suite in native frozen isolated; do
    evidence="$results/$suite"
    # Exercise the pinned scheduler; only its process-launch primitive is replaced.
    (
      run_leg() {
        printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\traw/%s.%s.%s.%s.%s.txt\n' \
          "$1" "$2" "$3" "$6" "$4" "$5" "$8" "$1" "$2" "$3" "$6" "$4" >> "$evidence/order.tsv"
        : > "$evidence/raw/$1.$2.$3.$6.$4.txt"
      }
      sample_groups
    )
    verify_scheduled_roles "$evidence" "$suite"
    cat "$evidence/cases.tsv"
  done
  exit 0
fi

if [[ "$mode" == run ]]; then
  test "$(go version)" = 'go version go1.27.1 linux/amd64'
  test "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'linux\namd64\nv1\n1'
  collect_host > "$results/environment.txt"
fi
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
for tool in benchrun benchplan; do go build -o "$scratch/bin/$tool" "$repo/scripts/$tool"; done
compile_roles > "$results/build.log" 2>&1
for suite in native frozen isolated; do inventory_suite "$suite"; done
record_rpc_inventory
if [[ "$mode" == --inventory-only ]]; then
  for suite in native frozen isolated; do
    printf '%s selected leaves per side: %s\n' "$suite" "$(jq '.Base|length' "$results/$suite/expected.json")"
  done
  printf 'common RPC benchmark symbols: %s\n' "$(wc -l < "$results/layout/rpc-base-native.symbols" | tr -d ' ')"
  sha256sum "$scratch"/bin/{base,head}-isolated/pkg-lux-rpc.test
  printf '%s\n' 'Selected 1x inventories, RPC dependency/benchmark inventory and original source checks passed.'
  exit 0
fi
go build -o "$scratch/bin/benchgate" "$repo/scripts/benchgate"
GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
go version -m "$scratch/bin/benchstat" > "$results/benchstat-build.txt"
for suite in native frozen isolated; do
  for role in base head; do
    while IFS=$'\t' read -r id package _; do record_layout "$role" "$suite" "$package"; done < "$results/$suite/cases.tsv"
  done
done
date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$results/environment.txt"
for suite in native frozen isolated; do run_suite "$suite"; done
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$results/environment.txt"
# All roles must use the same machine. Only A/A or invalid evidence can fail the diagnostic.
cat "$results"/{native,frozen,isolated}/cpu.txt | sort -u > "$results/cpu.txt"
test "$(wc -l < "$results/cpu.txt")" -eq 1
for suite in native frozen isolated; do
  awk -F '\t' 'NR==2 {if ($1!="aa" || $2!=0) exit 1} NR==3 {if ($1!="ab" || $2 !~ /^[01]$/) exit 1} END {if (NR != 3) exit 1}' \
    "$results/$suite/verdicts.tsv"
done
