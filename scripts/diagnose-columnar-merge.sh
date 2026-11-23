#!/usr/bin/env bash
set -euo pipefail

anchor=${1:-main}
mode=${2:-sample}
case "$mode" in sample|--inventory-only|--plan-only) ;; *) exit 2 ;; esac
[[ "$#" -le 2 ]]
case "$anchor" in
  main)
    commit=f853ea49f14a2eb3126f54a831a39d7ea05585db
    tree=d4a8ffd9f98ecd00c7eb7c2822f07accce1dab1b
    ;;
  pr71)
    commit=11a1b246b7cfb88926d84bb53424320fd3be3c48
    tree=e868c4d936859a1ffa2a5c465c3d36f8ba708a26
    ;;
  *) exit 2 ;;
esac
launch=$(pwd -P)
repo=$(git rev-parse --show-toplevel)
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
evidence="$launch/.tmp/hosted-columnar-merge-$anchor"
mkdir -p "$launch/.tmp"
test ! -L "$launch/.tmp"
mkdir "$evidence"

sampler_revision=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
validator_revision=1eede1c4002de6636575415eef5fff8e3cb572f0
git -C "$repo" show "$sampler_revision:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
git -C "$repo" show "$validator_revision:scripts/diagnose-pr83-pr84.sh" > "$evidence/validator.sh"
python3 -c 'import base64,sys; sys.stdout.buffer.write(base64.b64decode(sys.stdin.buffer.read()))' \
  < "$script_dir/diagnose-columnar-merge.patch.b64" > "$evidence/columnar-merge.patch"
cp "$script_dir/diagnose-columnar-merge.go.txt" "$evidence/selected-wire.go.txt"
(
  cd "$evidence"
  sha256sum -c - <<'PINS'
9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072  sampler.sh
8a998e03087873c17f7b5e0675f3880c2dc6e874e61ae579e3e371f9a5242bbc  validator.sh
2e248077b67ed819c1970c5297efb6e86ec4e3c7adb6fac917a2558d6415ae13  columnar-merge.patch
PINS
  sha256sum selected-wire.go.txt > fixture.sha256
)
source "$evidence/sampler.sh"
source "$evidence/validator.sh"
base="$commit"
head="$commit"
[[ "$(git -C "$repo" rev-parse "$commit^{tree}")" == "$tree" ]]
printf 'anchor %s\nbase %s %s\nhead %s %s plus the pinned merger.go patch\n' \
  "$anchor" "$base" "$tree" "$head" "$tree" > "$evidence/revisions.txt"
package=pkg/lux/luvia
packages=("$package")
benchmarks='BenchmarkMergeDestination|benchmarkMergeDestination|appendMergedColumnar|AppendMergedColumnar|AppendMergedSelected'
printf '01\t%s\tdefault\tBenchmarkMergeDestination/columnar=false/rows=1/direct=false-2\t^BenchmarkMergeDestination$\n' \
  "$package" > "$evidence/cases.tsv"
python3 - "$evidence" "$package" <<'PYTHON'
import json
from pathlib import Path
import sys

evidence, package = Path(sys.argv[1]), sys.argv[2]
names = [f"BenchmarkMergeDestination/columnar={columnar}/rows={rows}/direct={direct}-2"
         for columnar, rows in (("false", 1), ("true", 1), ("true", 128))
         for direct in ("false", "true")]
expected = [{"Package": package, "Name": name} for name in names]
(evidence / "scope.json").write_text(json.dumps([{"case": "01", "package": package, "leaves": names}], indent=2) + "\n")
(evidence / "expected.json").write_text(json.dumps({"Base": expected, "Head": expected}, indent=2) + "\n")
PYTHON
if [[ "$mode" == --plan-only ]]; then exit 0; fi

scratch=$(mktemp -d "$launch/.tmp/.hosted-columnar-merge-$anchor.XXXXXX")
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export GOWORK=off GOTOOLCHAIN=local TMPDIR="$scratch/tmp"
mkdir -p "$TMPDIR" "$scratch/bin"
probe="$(go env GOOS):$(go env GOARCH)"
[[ "$(go env GOVERSION)" == go1.27.1 && "$(go env GOHOSTOS):$(go env GOHOSTARCH)" == "$probe" ]]
[[ "$probe:$(go env GOAMD64):$(go env CGO_ENABLED)" == linux:amd64:v1:1 && -z "$(go env GOFLAGS)" ]]
collect_host > "$evidence/environment.txt"
for role in base head; do
  checkout="$scratch/source/$role"
  mkdir -p "$checkout" "$scratch/bin/$role"
  git -C "$repo" archive "$commit" | tar -xf - -C "$checkout"
  (
    cd "$checkout"
    export GIT_CEILING_DIRECTORIES="$scratch/source"
    printf '%s  %s\n' \
      2c30da7b52cf1697b13d260ab7dbe74154b77a4d124817ca21b5f367fe6acf51 "$package/merger.go" \
      0c115ad4099c7ed845f35321f01bde38d53bb414bdabd3eeefe4a2e32051c34f "$package/merger_bench_test.go" \
      0cba44db8b444555efdb427368e2ef26145bbd53c66c229e70d2beced5ff6023 "$package/merger_test.go" |
      sha256sum -c -
    if [[ "$role" == head ]]; then
      git apply --check --whitespace=error "$evidence/columnar-merge.patch"
      git apply --whitespace=error "$evidence/columnar-merge.patch"
      git apply --reverse --check "$evidence/columnar-merge.patch"
      printf '%s  %s\n' 4f72470b71fa742893a0791c87ee39e3b2c719b34c30b642ebe8cc731a44a0fb "$package/merger.go" |
        sha256sum -c -
    fi
    sha256sum go.mod go.sum "$package/"*.go > "$evidence/sources-$role.sha256"
    sh scripts/setup-modules.sh "$checkout"
    if [[ "$role" == base ]]; then
      for tool in benchrun benchplan benchgate; do
        go build -o "$scratch/bin/$tool" "./scripts/$tool"
      done > "$evidence/compile-tools.log" 2>&1
    fi
  )
  "$scratch/bin/benchrun" compile "$checkout" "$scratch/bin/$role" "$package" > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  "$scratch/bin/benchrun" run "$checkout" "$scratch/bin/$role" "$package" -test.timeout=90s \
    -test.run='^Test(AppendMerged.*|MergeColumnar.*|Integration_(PlanSplitMerge|ColumnarFederation)|Resolved(NullRemainsPresent|ResponseUsesCanonicalFieldValues))$' \
    -test.count=1 -test.v > "$evidence/merge-$role.log"
  grep -Fq -- '--- PASS: TestAppendMergedSelectedKeyPositions ' "$evidence/merge-$role.log"
  compile_diagnostic_fixture "$role" selected "$package" columnar_merge_diagnostic_test.go selected-wire.go.txt
  "$scratch/bin/benchrun" run "$checkout" "$scratch/bin/$role-selected" "$package" -test.timeout=90s \
    -test.run='^TestDiagnosticMergeSelectedNullable$' -test.count=1 -test.v > "$evidence/selected-$role.log"
  grep -Fq -- '--- PASS: TestDiagnosticMergeSelectedNullable/row ' "$evidence/selected-$role.log"
  grep -Fq -- '--- PASS: TestDiagnosticMergeSelectedNullable/columnar-128 ' "$evidence/selected-$role.log"
done
diff -u "$evidence/sources-base.sha256" "$evidence/sources-head.sha256" > "$evidence/sources.diff" || [[ "$?" == 1 ]]
[[ "$(grep -Ec '^[-+][0-9a-f]{64}  .*/merger\.go$' "$evidence/sources.diff")" == 2 ]]
[[ "$(grep -Ec '^[-+][0-9a-f]{64}  ' "$evidence/sources.diff")" == 2 ]]
probe_inventory
if [[ "$mode" == --inventory-only ]]; then exit 0; fi
for role in base head; do record_layout "$role"; done
GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
sample_groups
verify_output samples
analyze_groups
grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"
grep -Fxq $'ab\t0' "$evidence/verdicts.tsv"
