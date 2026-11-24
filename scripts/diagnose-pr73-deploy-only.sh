#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
mode="${1:-run}"
[[ $# -le 1 && ( "$mode" == run || "$mode" == --plan-only ) ]]
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr73-deploy-only.XXXXXXXX")
evidence="$repo/.tmp/hosted-pr73-deploy-only"
if [[ "$mode" == --plan-only ]]; then evidence="$scratch/evidence"; fi
evidence_created=false

cleanup_deploy_probe() {
  local status=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr73-deploy-only.* ]] || exit 99
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$evidence_created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$evidence/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_deploy_probe EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
evidence_created=true

python3 - "$script_dir" <<'PY'
from hashlib import sha256
from pathlib import Path
import sys

root = Path(sys.argv[1])
for name, expected in {
    "diagnose-hosted-perf.sh": "9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072",
    "diagnose-pr73-exact-codegen.sh": "1a13d532840d2af053bcae6d0c02eaa62816d28d9454ed97a7c8dce5ee7e3acc",
}.items():
    if sha256((root / name).read_bytes()).hexdigest() != expected:
        raise SystemExit(f"changed diagnostic helper: {name}")
PY
source "$script_dir/diagnose-hosted-perf.sh"
source "$script_dir/diagnose-pr73-exact-codegen.sh"
base=a75a0acce23b30e3f99d44b0c47f746901b82bb8
deploy=d8b686b516ed5ab89e185093ca836756901fb56c
head=9474fabe422599733143763e5749729cc0656c28
base_tree=d119706aff9f4b3f0564f3725fa491953408a124
deploy_tree=73a3f238e978bb3efacb72eb67b5eb08401f0e78
head_tree=e4fcfeba45f502dbf492051e9541b4189db8c781
official=98f929615cdb9cde35fa3d8fd1ecc85a0da46997
package=pkg/codegen
filter='^BenchmarkAPIParameterMetadata$/^(Int|State)$'
store="$scratch/objects.git"

prepare_deploy_probe() {
  local role tree expected actual
  local -a paths=(pkg/codegen/deploy.go pkg/codegen/deploy_helm.go pkg/codegen/deploy_helm_test.go pkg/codegen/deploy_test.go)
  git init --bare --quiet "$store"
  git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$store/objects/info/alternates"
  test "$(git --git-dir="$store" rev-parse "$base^{tree}")" = "$base_tree"
  test "$(git --git-dir="$store" merge-tree --write-tree "$base" "$deploy")" = "$deploy_tree"
  test "$(git --git-dir="$store" merge-tree --write-tree "$base" "$head")" = "$head_tree"
  if git --git-dir="$store" cat-file -e "$official^{commit}" 2>/dev/null; then
    test "$(git --git-dir="$store" show -s --format='%T %P' "$official")" = "$head_tree $base $head"
  fi
  git --git-dir="$store" diff --no-ext-diff --no-textconv --full-index --binary "$deploy^" "$deploy" > "$evidence/deploy-only.patch"
  printf '%s  %s\n' 716719ae58859009d1306396bd6aac2baa131e5b8ddd3797ddf4f8248047a087 "$evidence/deploy-only.patch" | sha256sum -c -
  git --git-dir="$store" read-tree "$base"
  git --git-dir="$store" apply --cached --check --whitespace=error "$evidence/deploy-only.patch"
  git --git-dir="$store" apply --cached --whitespace=error "$evidence/deploy-only.patch"
  test "$(git --git-dir="$store" write-tree)" = "$deploy_tree"
  git --git-dir="$store" apply --cached --reverse --whitespace=error "$evidence/deploy-only.patch"
  test "$(git --git-dir="$store" write-tree)" = "$base_tree"
  git --git-dir="$store" diff --name-only "$base_tree" "$deploy_tree" > "$evidence/deploy-files.txt"
  printf '%s\n' "${paths[@]}" | diff - "$evidence/deploy-files.txt"
  git --git-dir="$store" diff --exit-code "$deploy_tree" "$head_tree" -- "${paths[@]}"
  git --git-dir="$store" diff --name-only "$base_tree" "$head_tree" > "$evidence/full-files.txt"
  printf 'role\trevision\tarchive_tree\nbase\t%s\t%s\ndeploy\t%s\t%s\nhead\t%s\t%s\n' \
    "$base" "$base_tree" "$deploy" "$deploy_tree" "$head" "$head_tree" > "$evidence/roles.tsv"
  printf 'official_merge=%s\noriginal_run=45315193455\nreproduction_run=45339360813\n' "$official" > "$evidence/official-reference.txt"
  for role in base deploy head; do
    case "$role" in
      base) tree="$base_tree";;
      deploy) tree="$deploy_tree";;
      head) tree="$head_tree";;
    esac
    mkdir -p "$scratch/source/$role"
    git --git-dir="$store" archive "$tree" | tar -xf - -C "$scratch/source/$role"
    expected=bca2f8c336051736ebb635ab0217f9c865e8551cd2126c1af5e706ffee2243d7
    actual=$(sha256sum "$scratch/source/$role/pkg/codegen/codegen_bench_test.go" | awk '{print $1}')
    test "$actual" = "$expected"
  done
  (cd "$scratch/source/deploy" && sha256sum "${paths[@]}") > "$evidence/deploy-files.sha256"
  (cd "$scratch/source/head" && sha256sum -c "$evidence/deploy-files.sha256")
}

write_deploy_plan() {
  printf '%s\n' BenchmarkAPIParameterMetadata/Int-2 BenchmarkAPIParameterMetadata/State-2 > "$evidence/01.names"
  python3 - "$evidence" "$package" <<'PY'
import csv
import json
from pathlib import Path
import sys

evidence, package = Path(sys.argv[1]), sys.argv[2]
leaves = [{"Package": package, "Name": name} for name in (evidence / "01.names").read_text().splitlines()]
(evidence / "expected.json").write_text(json.dumps({"Base": leaves, "Head": leaves}, indent=2) + "\n")
comparisons = ("aa", "ad", "ah")
right = dict(zip(comparisons, ("base", "deploy", "head")))
with (evidence / "planned-order.tsv").open("w", newline="") as stream:
    table = csv.writer(stream, delimiter="\t", lineterminator="\n")
    table.writerow(("group", "pair", "comparison", "case", "side", "revision", "variant", "raw"))
    for offset, group in enumerate(("primary", "confirmation")):
        for pair in range(1, 11):
            sides = ("left", "right") if (pair + offset) % 2 else ("right", "left")
            start = (pair + offset) % len(comparisons)
            for comparison in comparisons[start:] + comparisons[:start]:
                for side in sides:
                    role = "base" if side == "left" else right[comparison]
                    raw = f"raw/{group}.{pair:02}.{comparison}.01.{side}.txt"
                    table.writerow((group, f"{pair:02}", comparison, "01", side, role, "default", raw))
PY
}

snapshot_deploy_sources() {
  python3 - "$scratch/source" "$evidence/source-$1.json" <<'PY'
from hashlib import sha256
import json
from pathlib import Path
import sys

root, output = map(Path, sys.argv[1:])
sources = {}
for file in sorted(root.rglob("*")):
    name = file.relative_to(root)
    if file.is_file() and ".tmp" not in name.parts and file.name not in ("go.work", "go.work.sum"):
        sources[str(name)] = sha256(file.read_bytes()).hexdigest()
output.write_text(json.dumps(sources, indent=2) + "\n")
PY
}

compile_deploy_probe() {
  local role source binary tool
  local pattern='^github\.com/light-speak/luxo/pkg/codegen\..*(BenchmarkAPIParameterMetadata.*|(writeAPIRegistration|writeAPIParameterRegistration|writeRegisteredParam|writeRegistryRoute|registeredParamType))$|^fmt\.(Fprintf|\(\*pp\)\.doPrintf)$|^strings\.\(\*Builder\)\.(Write|WriteString)$'
  mkdir "$scratch/bin"
  for role in base deploy head; do
    source="$scratch/source/$role"
    sh "$source/scripts/setup-modules.sh" "$source" > "$evidence/workspace-$role.log" 2>&1
    cp "$source/go.work" "$evidence/go-$role.work.txt"
  done
  (
    cd "$scratch/source/base"
    for tool in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$tool" "./scripts/$tool"
    done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  ) > "$evidence/tools.log" 2>&1
  for role in base deploy head; do
    source="$scratch/source/$role"
    "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$role" "$package" > "$evidence/compile-$role.log" 2>&1
    record_binaries "$role"
    binary="$scratch/bin/$role/pkg-codegen.test"
    go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$role.nm"
    go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$role.objdump"
    grep -q 'BenchmarkAPIParameterMetadata.func1' "$evidence/symbols-$role.nm"
    grep -q '^TEXT .*writeAPIParameterRegistration(' "$evidence/symbols-$role.objdump"
    "$scratch/bin/benchrun" run "$source" "$scratch/bin/$role" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s \
      > "$evidence/inventory-$role.txt"
    pr73_exact_check_leg "$evidence/inventory-$role.txt" 01 1x
  done
}

sample_deploy_probe() {
  local group pair comparison case_id side role variant raw
  mkdir "$evidence/raw"
  head -n 1 "$evidence/planned-order.tsv" > "$evidence/order.tsv"
  while IFS=$'\t' read -r group pair comparison case_id side role variant raw; do
    run_leg "$group" "$pair" "$comparison" "$side" "$role" "$case_id" "$package" "$variant" "$filter"
    if [[ "$mode" == run ]]; then pr73_exact_check_leg "$evidence/$raw" "$case_id"; fi
  done < <(tail -n +2 "$evidence/planned-order.tsv")
  cmp "$evidence/planned-order.tsv" "$evidence/order.tsv"
}

analyze_deploy_probe() {
  local comparison role status failed=0
  local -a gate_args
  printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for comparison in aa ad ah; do
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    gate_args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
    case "$comparison" in
      aa) role=base;;
      ad) role=deploy;;
      ah) role=head;;
    esac
    if [[ "$comparison" != aa ]]; then
      gate_args=(--approved-costs "$scratch/source/$role/scripts/benchgate/approved-costs.json" --base-sha "$base" "${gate_args[@]}")
    fi
    status=0
    "$scratch/bin/benchgate" "${gate_args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
    printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
    if (( status != 0 )); then
      grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log" || return "$status"
      failed=1
    fi
  done
  cat "$evidence/verdicts.tsv"
  if ! grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"; then
    printf 'A/A rejected. Attribution is inconclusive.\n' >&2
  fi
  return "$failed"
}

prepare_deploy_probe
write_deploy_plan
if [[ "$mode" == --plan-only ]]; then
  run_leg() {
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\traw/%s.%s.%s.%s.%s.txt\n' \
      "$1" "$2" "$3" "$6" "$4" "$5" "$8" "$1" "$2" "$3" "$6" "$4" >> "$evidence/order.tsv"
  }
  sample_deploy_probe
  cat "$evidence/roles.tsv" "$evidence/deploy-files.sha256" "$evidence/01.names"
  printf 'Verified %s planned processes.\n' "$(( $(wc -l < "$evidence/order.tsv") - 1 ))"
  exit 0
fi

export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
mkdir "$TMPDIR"
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
[[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
[[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
[[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
collect_host > "$evidence/environment.txt"
cp "${BASH_SOURCE[0]}" "$script_dir/diagnose-hosted-perf.sh" "$script_dir/diagnose-pr73-exact-codegen.sh" "$evidence/"
cat > "$evidence/protocol.txt" <<'PROTOCOL'
A is exact a75. D applies the four-file original deployment fix to A. H is the original exact full tested merge tree.
All three retain their normal linked source and tests. No product logic is switched at runtime or restored in H.
A/A reuses the same binary. A/D decides the measured deployment-only comparison. A/H retains the historical control.
Int and State run in the same process on the actual runner model recorded in environment.txt.
Two groups of ten alternating pairs, 200ms, CPU2, benchmem. Go 1.27.1 linux/amd64 v1 and default linkage.
Original benchstat alpha .01 and exact-bound repo benchgate for significance, limits and both groups.
A/H can remain red even when A/D passes. The workflow retains all three verdicts and exits nonzero if any fails.
A/A rejection leaves this experiment inconclusive. This selected diagnostic does not replace the normal PR performance gate.
PROTOCOL
snapshot_deploy_sources before
compile_deploy_probe
date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
sample_deploy_probe
date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
grep -h '^cpu: ' "$evidence"/inventory-*.txt "$evidence"/raw/*.txt | sort -u > "$evidence/cpu-consistency.txt"
test "$(wc -l < "$evidence/cpu-consistency.txt" | tr -d ' ')" = 1
snapshot_deploy_sources after
cmp "$evidence/source-before.json" "$evidence/source-after.json"
analyze_deploy_probe
