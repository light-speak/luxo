#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C GOWORK=off GOTOOLCHAIN=local

lane=${1:?missing lane}
repo=$(cd "${2:?missing source repository}" && pwd -P)
inputs=$(cd "${3:?missing original artifacts}" && pwd -P)
mkdir -p "${4:?missing evidence directory}"
evidence=$(cd "$4" && pwd -P)
mode=${5:-native}
[[ "$mode" == native || "$mode" == --plan-only ]]
driver=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
model=$(python3 -B "$driver/verify.py" model "$lane")
if [[ "$mode" == native ]]; then
  actual=$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')
  [[ "${RUNNER_KIND:-}" == github-hosted && "$(uname -sm)" == 'Linux x86_64' && "$actual" == "$model" ]] || exit 3
  [[ "$(go env GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'go1.27.1\nlinux\namd64\nv1\n1' ]]
  [[ -z "$(go env GOFLAGS)" ]]
fi

base_sha=0a6b143ad667c84f0e517b9ef0a9e9d5bd6ec367
base_tree=c12a0bf33f9bd39da85ba6a199ac9d76e16fa00b
head_sha=1f5ce302d3bcf1af0b21e55cb3070c424e96cbc4
head_tree=a74d2e3afd39afd6d3c8fe8070e1c241a2b93d93
patch_tree=c32bdfcf5339070da78d47b912dab52830f76587
patch_sum=28ba3f8612756e1064cda07cf2d290c40bf5ab23b1f7286f1921cfa4bc6aaeeb
sampler_sha=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
sampler_sum=9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
package=pkg/lux/rpc
symbols='(BenchmarkRPC(ErrorDataMigration|MalformedErrorDecode|ErrorDecode)|rpc[.](decodeError|readRPCErrorFields|decodeTracedResponse|decodeFormerRPCError|decodePriorStrictRPCError|decodeErrorWithoutData|[(*]*rpcErrorEnvelope[)*]*[.](validate|readField)))'
g() { git --no-replace-objects -C "$repo" "$@"; }
[[ "$(g rev-parse "$base_sha^{tree}")" == "$base_tree" ]]
[[ "$(g rev-parse "$head_sha^{tree}")" == "$head_tree" ]]
mapfile -t parents < <(g cat-file commit "$head_sha" | sed -n 's/^parent //p')
[[ "${#parents[@]}" == 2 && "${parents[0]}" == 12e7743153a02a5c1cfc997e84ff5b6993a267bd && "${parents[1]}" == "$base_sha" ]]

scratch=$(mktemp -d "$evidence/.native.XXXXXXXX")
trap 'rm -rf -- "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$scratch/tmp"
export TMPDIR="$scratch/tmp" GOTMPDIR="$scratch/tmp"
python3 -B "$driver/verify.py" plan "$lane" "$inputs" "$evidence"
printf 'base=%s\nbase_tree=%s\nhead=%s\nhead_tree=%s\npatch_tree=%s\npatch_sha256=%s\n' \
  "$base_sha" "$base_tree" "$head_sha" "$head_tree" "$patch_tree" "$patch_sum" > "$evidence/revisions.txt"

python3 -B - "$driver/product.patch.b64" "$scratch/product.patch" <<'PY'
import base64
import sys
from pathlib import Path
source, target = map(Path, sys.argv[1:])
target.write_bytes(base64.b64decode(b"".join(source.read_bytes().splitlines()), validate=True))
PY
[[ "$(shasum -a 256 "$scratch/product.patch" | cut -d ' ' -f 1)" == "$patch_sum" ]]
git init --quiet --bare "$scratch/objects.git"
g rev-parse --path-format=absolute --git-path objects > "$scratch/objects.git/objects/info/alternates"
git_store=(git --git-dir="$scratch/objects.git")
"${git_store[@]}" read-tree "$head_sha"
"${git_store[@]}" apply --cached --whitespace=error-all "$scratch/product.patch"
[[ "$("${git_store[@]}" write-tree)" == "$patch_tree" ]]
"${git_store[@]}" diff --name-only "$head_tree" "$patch_tree" > "$evidence/product-paths.txt"
diff -u <(printf '%s\n' "$package/client.go" "$package/error.go" "$package/error_test.go" "$package/handshake_test.go") "$evidence/product-paths.txt"
"${git_store[@]}" diff --check "$head_tree" "$patch_tree"
"${git_store[@]}" apply --cached --reverse "$scratch/product.patch"
[[ "$("${git_store[@]}" write-tree)" == "$head_tree" ]]
for file in error_bench_test.go error_data_bench_test.go error_data_reuse_reference_test.go request_reuse_test.go; do
  blob=$(g rev-parse "$base_tree:$package/$file")
  [[ "$blob" == "$(g rev-parse "$head_tree:$package/$file")" ]]
  [[ "$blob" == "$("${git_store[@]}" rev-parse "$patch_tree:$package/$file")" ]]
  printf '%s %s/%s\n' "$blob" "$package" "$file"
done > "$evidence/fixture-blobs.txt"
[[ "$mode" == native ]] || exit 0

g show "$sampler_sha:scripts/diagnose-hosted-perf.sh" > "$scratch/sampler.sh"
[[ "$(shasum -a 256 "$scratch/sampler.sh" | cut -d ' ' -f 1)" == "$sampler_sum" ]]
source "$scratch/sampler.sh"
[[ "$benchtime" == 200ms ]]
collect_host > "$evidence/environment.txt"
mkdir "$scratch/source" "$scratch/bin"
for arm in base head patch; do
  case "$arm" in
    base) revision="$base_tree" ;;
    head) revision="$head_tree" ;;
    patch) revision="$patch_tree" ;;
  esac
  archive="$scratch/source/$arm"
  mkdir -p "$archive/.tmp"
  "${git_store[@]}" archive "$revision" | tar -xf - -C "$archive"
  sh "$archive/scripts/setup-modules.sh" "$archive"
done
bin="$scratch/bin"
for tool in benchrun benchplan benchgate; do
  (cd "$scratch/source/base" && go build -o "$bin/$tool" "./scripts/$tool")
done
GOBIN="$bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
"$bin/benchrun" performance-profile > "$evidence/current-profile.txt"
cmp "$evidence/original-profile.txt" "$evidence/current-profile.txt"
for arm in base head patch; do
  archive="$scratch/source/$arm"
  compiler=compile-performance
  [[ "$arm" != base ]] || compiler=compile-performance-baseline
  "$bin/benchrun" "$compiler" "$archive" "$bin/$arm" "$package" default
  record_binaries "$arm"
  binary="$bin/$arm/${package//\//-}.test"
  go tool nm -size "$binary" > "$scratch/nm"
  awk -v pattern="$symbols" '$0 ~ pattern' "$scratch/nm" > "$evidence/$arm.nm.txt"
  go tool objdump -s "$symbols" "$binary" > "$evidence/$arm.objdump.txt"
  test -s "$evidence/$arm.nm.txt"
  test -s "$evidence/$arm.objdump.txt"
  readelf -SW "$binary" > "$evidence/$arm.sections.txt"
  objcopy --dump-section ".text=$scratch/text" --dump-section ".rodata=$scratch/rodata" "$binary" "$scratch/link.tmp"
  (cd "$scratch" && sha256sum text rodata) > "$evidence/$arm.section-sha256"
  rm -- "$scratch/text" "$scratch/rodata" "$scratch/link.tmp"

  : > "$evidence/inventory-$arm.txt"
  while IFS=$'\t' read -r _ case_pkg profile filter; do
    "$bin/benchrun" run-performance "$archive" "$bin/$arm" "$case_pkg" "$profile" \
      '-test.run=^$' "-test.bench=$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      >> "$evidence/inventory-$arm.txt"
  done < "$evidence/cases.tsv"
  python3 -B "$driver/verify.py" inventory "$lane" "$evidence/expected.json" "$evidence/inventory-$arm.txt"
done

take_sample() {
  local group="$1" pair="$2" choice="$3" id="$4" side="$5" arm="$6" target="$7" profile="$8" filter="$9" file
  file="raw/$group.$pair.$choice.$id.$side.txt"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$group" "$pair" "$choice" "$id" "$side" "$arm" "$profile" "$file" >> "$evidence/order.tsv"
  "$bin/benchrun" run-performance "$scratch/source/$arm" "$bin/$arm" "$target" "$profile" \
    '-test.run=^$' "-test.bench=$filter" -test.benchmem "-test.benchtime=$benchtime" \
    -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/$file" 2>> "$evidence/benchmark-stderr.txt"
}

mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tprofile\traw\n' > "$evidence/order.tsv"
comparisons=(aa ah ap pp)
for group in primary confirmation; do
  offset=0
  [[ "$group" != confirmation ]] || offset=1
  for i in {1..10}; do
    printf -v pair '%02d' "$i"
    while IFS=$'\t' read -r id target profile filter; do
      for slot in {0..3}; do
        choice=${comparisons[(slot+i+offset+10#${id}) % 4]}
        left=base
        case "$choice" in
          aa) right=base ;;
          ah) right=head ;;
          ap) right=patch ;;
          pp) left=patch; right=patch ;;
        esac
        if (( (i + offset) % 2 == 1 )); then
          take_sample "$group" "$pair" "$choice" "$id" left "$left" "$target" "$profile" "$filter"
          take_sample "$group" "$pair" "$choice" "$id" right "$right" "$target" "$profile" "$filter"
        else
          take_sample "$group" "$pair" "$choice" "$id" right "$right" "$target" "$profile" "$filter"
          take_sample "$group" "$pair" "$choice" "$id" left "$left" "$target" "$profile" "$filter"
        fi
      done
    done < "$evidence/cases.tsv"
  done
done

gate() {
  local name="$1" status=0
  shift
  "$bin/benchgate" "$@" > "$evidence/$name.gate.txt" 2>&1 || status=$?
  printf '%s\t%s\n' "$name" "$status" >> "$evidence/gate-status.tsv"
  if (( status != 0 )); then
    grep -Eq '^performance gate (rejected |requires confirmation for )' "$evidence/$name.gate.txt" || return "$status"
  fi
}
: > "$evidence/gate-status.tsv"
for choice in "${comparisons[@]}"; do
  for group in primary confirmation; do
    normalize_group "$choice" "$group"
    stem="$evidence/$choice.$group"
    for side in left right; do
      [[ "$(sed -n 's/^cpu: //p' "$stem.$side.txt" | sort -u)" == "$model" ]]
    done
    if [[ "$choice" == aa || "$choice" == pp ]]; then
      "$bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        "base=$stem.right.txt" "head=$stem.left.txt" > "$stem.reverse.csv" 2> "$stem.reverse.warnings.txt"
      gate "$choice-$group" "$stem.csv"
      gate "$choice-$group-reverse" "$stem.reverse.csv"
    fi
  done
  if [[ "$choice" == ah || "$choice" == ap ]]; then
    gate "$choice-combined" --approved-costs "$scratch/source/base/scripts/benchgate/approved-costs.json" \
      --base-sha "$base_sha" "$evidence/$choice.primary.csv" "$evidence/$choice.confirmation.csv"
  fi
done
if awk -F '\t' '$1 ~ /^(aa|pp)-/ && $2 != 0 { bad = 1 } END { exit !bad }' "$evidence/gate-status.tsv"; then
  printf 'unstable-control\n' | tee "$evidence/outcome.txt"
  exit 4
fi
if ! grep -qx $'ap-combined\t0' "$evidence/gate-status.tsv"; then
  printf 'candidate-strict-failure\n' | tee "$evidence/outcome.txt"
  exit 5
fi
awk '/^confirmed performance regression: .* sec\/op / { print $4 }' "$evidence/ah-combined.gate.txt" | sort -u > "$evidence/ah-confirmed-time.txt"
comm -23 "$evidence/original-direct.txt" "$evidence/ah-confirmed-time.txt" > "$evidence/not-reproduced.txt"
if [[ -s "$evidence/not-reproduced.txt" ]]; then
  printf 'candidate-green-original-incompletely-reproduced\n' | tee "$evidence/outcome.txt"
  exit 6
fi
printf 'original-direct-reproduced-candidate-green\n' | tee "$evidence/outcome.txt"
