#!/usr/bin/env bash
set -euo pipefail

repo=$(git rev-parse --show-toplevel)
mode="${1:-run}"
case "$mode" in run|--plan-only|--inventory-only) test "$#" -le 1;; *) exit 2;; esac
assets="$repo/.github/qualification"
sampler_commit=61521c06948d6f4cdf04cd94abb6a19fdd2c5ac7
head=32b67c84d53e3a5ff4c93e66ce96fa1a5ffa96c2
head_tree=c8333e7682bdac15b4faf0cc0f9ead198a7f6ccb
proposal=8d0b82cfef93757ca1e87b3027aa8bdf66c26fe4
target=pkg/codegen/query_facade.go

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr91-query-facade.XXXXXXXX")
evidence="$repo/.tmp/pr91-query-facade-evidence"
if [[ "$mode" != run ]]; then evidence="$scratch/evidence"; fi
evidence_created=false
cleanup_facade() {
  local status=$?
  trap - EXIT
  [[ "$scratch" == "$repo"/.tmp/pr91-query-facade.* && -d "$scratch" && ! -L "$scratch" ]] || exit 99
  if (( status != 0 )) && [[ "$mode" != run && -f "$evidence/compile.log" ]]; then tail -n 80 "$evidence/compile.log" >&2; fi
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$evidence_created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$status" > "$evidence/cleanup.txt"
  fi
  exit "$status"
}
trap cleanup_facade EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$evidence"
evidence_created=true
mkdir "$scratch/tmp" "$scratch/bin" "$scratch/source"
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local LC_ALL=C

git -C "$repo" show "$sampler_commit:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
printf '%s  %s\n' 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 "$evidence/sampler.sh" | sha256sum -c -
source "$evidence/sampler.sh"
base=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
head=32b67c84d53e3a5ff4c93e66ce96fa1a5ffa96c2
test "$(git -C "$repo" rev-parse "$head^{tree}")" = "$head_tree"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = ba252c7cace8908cdfcd7c203bc9dc11bb2ba746
git -C "$repo" merge-base --is-ancestor "$base" "$head"
git -C "$repo" diff --exit-code "$base" "$head" -- "$target" pkg/codegen/codegen_bench_test.go
base64 --decode < "$assets/pr91-query-facade.patch.b64" > "$evidence/product.patch"
cp "$assets/pr91-query-facade-long.go.txt" "$evidence/long.go.txt"
printf '%s  %s\n' \
  034ebdf51119afd1436295be108466b42ab6cfb08e1259e7b1d4810e17cdb092 "$evidence/product.patch" \
  d557a0b439f2364a70b9c7ea9fe33e5573132d345aefc191cc69c3d0ab9e7333 "$evidence/long.go.txt" | sha256sum -c -

store="$scratch/git"
git init --bare --quiet "$store"
printf '%s/objects\n' "$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir)" > "$store/objects/info/alternates"
replay() { git --git-dir="$store" "$@"; }
replay read-tree "$head"
replay apply --cached --check --whitespace=error "$evidence/product.patch"
replay apply --cached --whitespace=error "$evidence/product.patch"
test "$(replay write-tree)" = "$proposal"
test "$(replay diff --cached --name-only "$head")" = "$target"
replay apply --cached --reverse "$evidence/product.patch"
test "$(replay write-tree)" = "$head_tree"
printf 'base=%s\nPR_head=%s\nPR_tree=%s\nproposal_tree=%s\n' "$base" "$head" "$head_tree" "$proposal" > "$evidence/revisions.txt"
for role in "$base" "$head" "$proposal"; do
  replay ls-tree -r "$role" > "$evidence/source-$role.git-tree"
done
cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/codegen	default	^BenchmarkQueryFacadeSource$
02	pkg/codegen	long	^BenchmarkQueryFacadeLongIdentifier$
CASES

python3 - "$evidence" <<'PY'
from csv import writer
from pathlib import Path
import json
import sys

root = Path(sys.argv[1])
names = {
    "01": [f"BenchmarkQueryFacadeSource/{name}/{count}-2" for name in ("R", "User") for count in (1, 128)],
    "02": [f"BenchmarkQueryFacadeLongIdentifier/{shape}-2"
           for shape in ("RegularReadOnly", "RegularWritable", "DecimalReadOnly", "DecimalWritable")],
}
(root / "expected-by-case.json").write_text(json.dumps(names, indent=2)+"\n")
keys = [{"Package":"pkg/codegen","Name":name} for scope in names.values() for name in scope]
(root / "expected.json").write_text(json.dumps({"Base":keys,"Head":keys}, indent=2)+"\n")
comparisons = (("aa","base"),("ah","head"),("ap","proposal"))
with (root/"schedule.tsv").open("w") as stream:
    w = writer(stream, delimiter="\t", lineterminator="\n")
    for shift, group in enumerate(("primary","confirmation")):
        for pair in range(1, 11):
            sample = f"{pair:02}"
            sides = ("left","right") if (pair + shift) % 2 else ("right","left")
            for case, _, variant, filter in [row.split("\t") for row in (root/"cases.tsv").read_text().splitlines()]:
                start = (pair + int(case) + shift) % 3
                for comparison, right in comparisons[start:]+comparisons[:start]:
                    for side in sides:
                        role = "base" if side == "left" else right
                        raw = f"raw/{group}.{sample}.{comparison}.{case}.{side}.txt"
                        w.writerow((group,sample,comparison,case,side,role,variant,raw,filter))
PY
test "$(wc -l < "$evidence/schedule.tsv")" -eq 240
test "$(cut -f8 "$evidence/schedule.tsv" | sort -u | wc -l)" -eq 240

if [[ "$mode" == --plan-only ]]; then
  cat "$evidence/revisions.txt" "$evidence/expected-by-case.json"
  printf '%s\n' '240 independent process legs, 8 leaves per side per group in A/A, A/H, and A/P.'
  exit 0
fi

if [[ "$mode" == run ]]; then
  test "$(go version)" = 'go version go1.27.1 linux/amd64'
  test "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'linux\namd64\nv1\n1'
  collect_host > "$evidence/environment.txt"
fi
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"

compile_roles() {
  local role package="$1" source overlay
  for role in base head proposal; do
    source="$scratch/source/$role"
    mkdir -p "$source/.tmp"
    replay archive "${!role}" | tar -xf - -C "$source"
    sh "$source/scripts/setup-modules.sh" "$source"
    cp "$source/go.work" "$evidence/workspace-$role.txt"
  done
  for tool in benchrun benchplan benchgate; do
    (cd "$scratch/source/base" && go build -o "$scratch/bin/$tool" "./scripts/$tool")
  done
  for role in base head proposal; do
    source="$scratch/source/$role"
    (
      cd "$source"
      sha256sum "$target" "$package/codegen_bench_test.go"
    ) >> "$evidence/timed-source-$role.sha256"
    "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$role" "$package"
    overlay="$evidence/long-$role.overlay.json"
    test ! -e "$source/$package/query_facade_long_diagnostic_test.go"
    jq -n --arg origin "$source/$package/query_facade_long_diagnostic_test.go" --arg fixture "$evidence/long.go.txt" \
      '{Replace:{($origin):$fixture}}' > "$overlay"
    GOFLAGS="-overlay=$overlay" "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$role-long" "$package"
    record_binaries "$role"
    record_binaries "$role-long"
  done
}

compile_roles pkg/codegen > "$evidence/compile.log" 2>&1
for role in base head proposal; do
  while IFS=$'\t' read -r case package variant filter; do
    binary="$scratch/bin/$role"
    if [[ "$variant" != default ]]; then binary+="-$variant"; fi
    raw="$evidence/inventory-$role-$case.raw"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$binary" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x \
      -test.count=1 -test.cpu=2 -test.timeout=90s > "$raw"
    "$scratch/bin/benchplan" --normalize "$raw" "$evidence/inventory-$role-$case.txt"
  done < "$evidence/cases.tsv"
done
python3 - "$evidence" <<'PY'
import json
from pathlib import Path
import sys
root=Path(sys.argv[1])
for case, names in json.loads((root/"expected-by-case.json").read_text()).items():
    for role in ("base","head","proposal"):
        where=root/f"inventory-{role}-{case}.txt"
        lines=where.read_text().splitlines()
        rows=[line.split() for line in lines if line.startswith("Benchmark")]
        if (lines[-1:] != ["PASS"] or sorted(row[0] for row in rows) != sorted(names)
            or not all(len(row) == 8 and row[1] == "1" and row[3::2] == ["ns/op","B/op","allocs/op"] for row in rows)):
            raise SystemExit(f"unexpected selected inventory: {where}")
print("All 3 roles passed the 4 original and 4 long-name leaves at 1x.")
PY
if [[ "$mode" == --inventory-only ]]; then exit 0; fi

GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
for role in base head proposal; do
  for variant in "$role" "$role-long"; do
    binary="$scratch/bin/$variant/pkg-codegen.test"
    regex='codegen\..*(BenchmarkQueryFacade(Source|LongIdentifier)|generateQueryFacade|write(Long)?QueryFacadeParts|generateDecimalQueryMethods|generatePreparedQueryUpdate)'
    go tool nm -size -sort address "$binary" | awk -v re="$regex" '$NF ~ re' > "$evidence/nm-$variant.txt"
    go tool objdump -s "$regex" "$binary" > "$evidence/objdump-$variant.txt"
    test -s "$evidence/nm-$variant.txt"
    test -s "$evidence/objdump-$variant.txt"
  done
done
mkdir "$evidence/raw"
printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
while IFS=$'\t' read -r group sample comparison case side role variant raw filter; do
  run_leg "$group" "$sample" "$comparison" "$side" "$role" "$case" pkg/codegen "$variant" "$filter"
done < "$evidence/schedule.tsv"
tail -n +2 "$evidence/order.tsv" | cmp - <(cut -f1-8 "$evidence/schedule.tsv")
grep -h '^cpu: ' "$evidence"/raw/*.txt "$evidence"/inventory-*.txt | sort -u > "$evidence/cpu.txt"
test "$(wc -l < "$evidence/cpu.txt")" -eq 1

printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
for comparison in aa ah ap; do
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  options=()
  if [[ "$comparison" != aa ]]; then
    policy_role=head
    if [[ "$comparison" == ap ]]; then policy_role=proposal; fi
    options=(--approved-costs "$scratch/source/$policy_role/scripts/benchgate/approved-costs.json" --base-sha "$base")
  fi
  status=0
  "$scratch/bin/benchgate" "${options[@]}" "$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv" \
    > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
  if (( status != 0 )); then grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log"; fi
  printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
done
cat "$evidence/verdicts.tsv"
# A/H records the existing PR control. A/A or A/P rejections block this qualification.
awk -F '\t' '($1=="aa" || $1=="ap") && $2!=0 {failed=1} END {exit failed}' "$evidence/verdicts.tsv"
