#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C GOTOOLCHAIN=local
base=f83f9c78f9ee03a5d73b7d0a41bae5280a250bfd
head=c7dc905ecbd2b6ed9e307f4b5b52426fe65e1bee
cpu='Intel(R) Xeon(R) 6975P-C'
package=pkg/lux/api
symbols='BenchmarkBinaryParamLookup|findBinaryParamLinear|newBinaryParamLookup|binaryParamLookup\)\.find'
root=$(cd "${GITHUB_WORKSPACE:?}" && pwd -P)
work="$root/.tmp"
control="$work/pr84-control"
base_checkout="$work/pr84-base"
head_git="$work/pr84-head"
evidence="$work/pr84-binary-lookup-evidence"
g() { git --no-replace-objects -C "$@"; }

test "$(uname -sm)" = 'Linux x86_64'
test "$(sed -n 's/^actual=//p' "$evidence/runner-route.txt")" = "$cpu"
test "$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')" = "$cpu"
test "$(go env GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
test "$(g "$control" rev-list --parents -n 1 HEAD)" = "$GITHUB_SHA $head"
diff -u <(printf 'A\t%s\n' .github/workflows/diagnose-pr84-binary-lookup.yml scripts/diagnose-pr84-binary-lookup.sh) \
    <(g "$control" diff --name-status "$head" HEAD)
test "$(g "$base_checkout" rev-parse HEAD 'HEAD^{tree}')" = "$(printf '%s\n%s' "$base" 0f97d642325769e2882fd0235c1575af71e3fc3c)"
test "$(g "$head_git" rev-parse HEAD 'HEAD^{tree}')" = "$(printf '%s\n%s' "$head" 4516c91b6582e22f3cd51742b82e48fc83f31130)"
test "$(g "$head_git" cat-file commit "$head" | sed -n 's/^parent //p')" = \
    "$(printf '%s\n%s' 54ca8db0e1a324947f07ec61dfdc340b143bb441 "$base")"
for checkout in "$base_checkout" "$head_git"; do
    g "$checkout" diff --quiet
    test -z "$(g "$checkout" ls-tree -r --name-only HEAD | awk '/(^|\/)\.gitattributes$/')"
done
build_inputs=(go.mod go.sum scripts/setup-modules.sh scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout)
g "$base_checkout" ls-tree HEAD -- "${build_inputs[@]}" > "$evidence/base-tool-inputs.txt"
g "$head_git" ls-tree HEAD -- "${build_inputs[@]}" > "$evidence/head-tool-inputs.txt"
cmp "$evidence/base-tool-inputs.txt" "$evidence/head-tool-inputs.txt"
(cd "$base_checkout" && sha256sum -c -) > "$evidence/fixture-check.log" <<'HASHES'
f87c71b900ba020bf19dbe737d9be39c0982a27e3734a018e6f101c5ca03a9c7  pkg/lux/api/binary_param_lookup_test.go
61644455ad19b8c4fe2d90c0d02b49909d7c3393016adef5df099764d2e16e8a  pkg/lux/api/binary_plan.go
6f52f1f9c662a9832a58a174f5212698af4ef0572e4e41ae0759e2d4988c9cbe  pkg/lux/api/binary.go
HASHES
for filename in binary_param_lookup_test.go binary_plan.go binary.go; do
    cmp "$base_checkout/$package/$filename" "$head_git/$package/$filename"
done
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; } > "$evidence/environment.txt"

for filename in go.work go.work.sum .tmp; do
    [[ ! -e "$base_checkout/$filename" && ! -L "$base_checkout/$filename" ]]
done
scratch=$(mktemp -d "$work/pr84-binary-lookup.XXXXXXXX")
cleanup() {
    local status=$?
    trap - EXIT
    rm -rf -- "$scratch" "$base_checkout/.tmp" || status=1
    rm -f -- "$base_checkout/go.work" "$base_checkout/go.work.sum" || status=1
    printf 'exit=%s\n' "$status" > "$evidence/cleanup.txt"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
input="$scratch/input"
tools="$scratch/bin"
head_archive="$scratch/head"
mkdir -p "$input" "$tools" "$head_archive/.tmp" "$base_checkout/.tmp" "$scratch/tmp"
export TMPDIR="$scratch/tmp" GOTMPDIR="$scratch/tmp" GOCACHE="$scratch/go-cache"
gh api repos/light-speak/luxo/actions/runs/45810683151/attempts/1 > "$evidence/original-run.json"
gh api repos/light-speak/luxo/actions/artifacts/15114248625 > "$evidence/original-metadata.json"
gh api repos/light-speak/luxo/actions/artifacts/15114248625/zip > "$input/original.zip"
python3 -I -B - "$input/original.zip" "$evidence" "$base" "$head" "$cpu" <<'PY'
from hashlib import sha256
import json
from pathlib import Path
import re
import sys
from zipfile import ZipFile

artifact, out = map(Path, sys.argv[1:3])
base, head, cpu = sys.argv[3:]
digest = "1b79f911e226278e34a65b900ee77222a42928fbb0e240c69c65079b7478c996"
assert sha256(artifact.read_bytes()).hexdigest() == digest
meta = json.loads((out / "original-metadata.json").read_text())
assert meta["id"] == 15114248625 and meta["digest"] == f"sha256:{digest}"
run = json.loads((out / "original-run.json").read_text())
assert (run["id"],run["run_attempt"],run["head_sha"],run["event"],run["conclusion"]) == (
    45810683151,1,head,"workflow_dispatch","failure")
assert meta["workflow_run"]["id"] == run["id"] and meta["workflow_run"]["head_sha"] == head
with ZipFile(artifact) as z:
    assert z.testzip() is None
    assert z.read("benchmark-revisions.txt").decode().splitlines() == [f"base_sha={base}",f"tested_sha={head}"]
    detected = re.findall(r"^Model name:\s*(.*)$", z.read("benchmark-environment.txt").decode(), re.M)
    assert [s.strip() for s in detected] == [cpu]
    benchmark = "BenchmarkBinaryParamLookup"
    def family(rows):
        return [item for item in rows if item["Name"].startswith(benchmark + "/")]
    expected = json.loads(z.read("benchmark-expected.json"))
    leaves = [{"Package":"pkg/lux/api","Name":f"{benchmark}/n={n}/stride={stride}/direct={direct}-2"}
              for n in (1,16) for stride in (1,1001,65537) for direct in ("false","true")]
    assert family(expected["Base"]) == family(expected["Head"]) == leaves
    confirmation = json.loads(z.read("benchmark-confirmation-expected.json"))
    confirmed = [item for item in leaves if "/n=16/" in item["Name"] and item["Name"].endswith("direct=false-2")]
    assert family(confirmation["Base"]) == family(confirmation["Head"]) == confirmed
    gate = z.read("benchmark-gate.txt").decode()
    failures = re.findall(r"^confirmed performance regression: .*?/BinaryParamLookup/(\S+) sec/op ",gate,re.M)
    assert set(failures) == {item["Name"].split("/",1)[1] for item in confirmed}
    stock = z.read("benchmark-batch.tsv").decode().splitlines()
    assert len([line for line in stock if line.startswith("pkg/lux/api\tapi-non-identity\t")
                and benchmark in line]) == 1
    (out / "expected.json").write_text(json.dumps({"Base":leaves,"Head":leaves},indent=2)+"\n")
    (out / "batch.tsv").write_text("pkg/lux/api\tapi-non-identity\t^(BenchmarkBinaryParamLookup)$\n")
    for upstream,target in {
        "benchmark-gate.txt":"original-gate.txt",
        "benchmark-revisions.txt":"benchmark-revisions.txt",
        "benchmark-compile-profile.txt":"reference-profile.txt",
        "benchmark-batch.tsv":"original-primary-batch.tsv",
        "benchmark-confirmation-batch.tsv":"original-confirmation-batch.tsv",
    }.items():
        (out / target).write_bytes(z.read(upstream))
PY

g "$head_git" archive "$head" | tar -xf - -C "$head_archive"
sh "$head_archive/scripts/setup-modules.sh" "$base_checkout"
sh "$head_archive/scripts/setup-modules.sh" "$head_archive"
(
    cd "$head_git"
    GOWORK=off go build -o "$tools/" ./scripts/benchrun ./scripts/benchplan ./scripts/benchgate
)
GOWORK=off GOBIN="$tools" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
"$tools/benchrun" performance-profile > "$evidence/rebuilt-profile.txt"
cmp "$evidence/reference-profile.txt" "$evidence/rebuilt-profile.txt"
IFS=$'\t' read -r pkg profile filter < "$evidence/batch.tsv"
test "$pkg" = "$package" && test "$profile" = api-non-identity
"$tools/benchplan" --verify-profile-batch primary "$evidence/expected.json" "$evidence/batch.tsv"

source_for() {
    case "$1" in
        base) printf '%s\n' "$base_checkout" ;;
        head) printf '%s\n' "$head_archive" ;;
        *) return 2 ;;
    esac
}

compile_arms() {
    local mode=$1 results="$evidence/$1" arm tree command binary
    mkdir "$results"
    for arm in base head; do
        mkdir -p "$tools/$mode/$arm"
        tree=$(source_for "$arm")
        binary="$tools/$mode/$arm/pkg-lux-api-api-non-identity.test"
        if [[ "$mode" == stock ]]; then
            command=compile-performance
            if [[ "$arm" == base ]]; then command=compile-performance-baseline; fi
            "$tools/benchrun" "$command" "$tree" "$tools/$mode/$arm" "$package" "$profile" \
                > "$results/$arm.compile.log" 2>&1
        else
            python3 -I -B - "$tree/$package" "$scratch/$arm.overlay" "$results/$arm.overlay.json" <<'OVERLAY'
import json
from pathlib import Path
import sys
package, overlay, receipt = map(Path, sys.argv[1:])
files = sorted(package.glob("*_test.go"))
fixture = package / "binary_param_lookup_test.go"
assert fixture in files and len(files) > 1
assert all(p.is_file() and not p.is_symlink() for p in files)
assert not any("func TestMain(" in p.read_text() for p in files)
removed = [p for p in files if p != fixture]
overlay.write_text(json.dumps({"Replace": {str(p): "" for p in removed}}))
receipt.write_text(json.dumps({"kept":fixture.name,"removed":[p.name for p in removed]},indent=2)+"\n")
OVERLAY
            (
                cd "$tree"
                GOWORK="$tree/go.work" go test -trimpath -c -overlay="$scratch/$arm.overlay" \
                    "./$package" -o "$binary"
            ) > "$results/$arm.compile.log" 2>&1
        fi
        sha256sum "$binary" > "$results/$arm.sha256"
        go version -m "$binary" > "$results/$arm.build.txt"
        go tool buildid "$binary" > "$results/$arm.buildid.txt"
        readelf -WhlS "$binary" > "$results/$arm.readelf.txt"
        go tool nm -size -sort address "$binary" > "$scratch/$arm.nm"
        grep -E "$symbols" "$scratch/$arm.nm" > "$results/$arm.symbols.txt"
        grep -E 'DecodeDebug|decodeDebugTrace' "$scratch/$arm.nm" > "$results/$arm.trace-symbols.txt" || [[ "$?" == 1 ]]
        go tool objdump -s "$symbols" "$binary" > "$results/$arm.objdump.txt"
        grep -q 'BenchmarkBinaryParamLookup' "$results/$arm.objdump.txt"
        grep -q 'binary_param_lookup_test.go:' "$results/$arm.objdump.txt"
        objcopy --dump-section ".text=$scratch/$arm.text" "$binary" "$scratch/$arm.objcopy"
        sha256sum "$scratch/$arm.text" > "$results/$arm.text.sha256"
        rm -- "$scratch/$arm.text" "$scratch/$arm.objcopy" "$scratch/$arm.nm"
    done
}

sample() {
    "$tools/benchrun" run-performance "$(source_for "$2")" "$tools/$1/$2" "$package" "$profile" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=200ms -test.count=1 -test.cpu=2 >> "$3"
}

compare() {
    "$tools/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        "base=$1" "head=$2" > "$3.csv" 2> "$3.warnings.txt"
}

gate() {
    local output=$1 name=$2 status=0
    shift 2
    "$tools/benchgate" "$@" > "$output/$name.gate.txt" 2>&1 || status=$?
    printf '%s\t%d\n' "$name" "$status" >> "$output/gates.tsv"
    return "$status"
}

schedule_pairs() {
    local mode=$1 results="$evidence/$1" group round order offset index name left right prefix
    local comparisons=(aa:base:base ab:base:head bb:head:head)
    printf 'comparison\tgroup\tpair\torder\tleft\tright\n' > "$results/order.tsv"
    for group in 1 2; do
        for round in {1..10}; do
            order=left,right
            if (( (round+group)%2 )); then order=right,left; fi
            for offset in 0 1 2; do
                index=$(( (round+(group-1)*10+offset)%3 ))
                IFS=: read -r name left right <<< "${comparisons[index]}"
                prefix="$results/$name.$group"
                printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$name" "$group" "$round" "$order" "$left" "$right" >> "$results/order.tsv"
                if [[ "$order" == left,right ]]; then
                    sample "$mode" "$left" "$prefix.left.raw"
                    sample "$mode" "$right" "$prefix.right.raw"
                else
                    sample "$mode" "$right" "$prefix.right.raw"
                    sample "$mode" "$left" "$prefix.left.raw"
                fi
            done
        done
    done
}

run_comparisons() {
    local mode=$1 results="$evidence/$1" group name prefix side unstable=0 status=0
    schedule_pairs "$mode"
    for group in 1 2; do
        for name in aa ab bb; do
            prefix="$results/$name.$group"
            for side in left right; do
                "$tools/benchplan" --normalize "$prefix.$side.raw" "$prefix.$side.txt"
                test "$(sed -n 's/^cpu: //p' "$prefix.$side.txt" | sort -u)" = "$cpu"
            done
            "$tools/benchplan" --verify "$evidence/expected.json" "$prefix.left.txt" "$prefix.right.txt"
            compare "$prefix.left.txt" "$prefix.right.txt" "$prefix"
            if [[ "$name" == ab ]]; then continue; fi
            compare "$prefix.right.txt" "$prefix.left.txt" "$prefix.reverse"
            gate "$results" "$name.$group" "$prefix.csv" || unstable=1
            gate "$results" "$name.$group.reverse" "$prefix.reverse.csv" || unstable=1
        done
    done
    gate "$results" ab-combined --approved-costs "$head_git/scripts/benchgate/approved-costs.json" --base-sha "$base" \
        "$results/ab.1.csv" "$results/ab.2.csv" || status=$?
    sha256sum -c "$results"/{base,head}.sha256
    if (( unstable )); then
        printf 'unstable-control\n' > "$results/outcome.txt"
        return 4
    fi
    if (( status == 0 )); then
        printf 'no-confirmed-regression\n' > "$results/outcome.txt"
        return 0
    fi
    if grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$results/ab-combined.gate.txt"; then
        printf 'strict-performance-rejection\n' > "$results/outcome.txt"
    else
        printf 'diagnostic-gate-failure\n' > "$results/outcome.txt"
    fi
    return "$status"
}

# A child Bash keeps errexit active even while the parent records its exit.
export base cpu evidence package profile filter tools base_checkout head_archive head_git
export -f source_for sample compare gate schedule_pairs run_comparisons
compile_arms stock
stock_status=0
BASH_ENV=/dev/null bash -c 'set -euo pipefail; run_comparisons "$1"' _ stock || stock_status=$?
if (( stock_status == 1 )) && [[ -f "$evidence/stock/ab-combined.gate.txt" ]] && grep -Eq '^confirmed performance regression: .*?/BinaryParamLookup/n=16/stride=(1|1001|65537)/direct=false-2 sec/op ' \
    "$evidence/stock/ab-combined.gate.txt"; then
    # Keep the full original fixture and exact production files. Remove other
    # test linker roots only for this counterfactual, after reproducing a red.
    compile_arms isolated
    isolated_status=0
    BASH_ENV=/dev/null bash -c 'set -euo pipefail; run_comparisons "$1"' _ isolated || isolated_status=$?
    printf '%s\n' "$isolated_status" > "$evidence/isolated-exit.txt"
fi
printf 'stock=%s\noriginal-formal-gate=retained\n' "$stock_status" > "$evidence/outcome.txt"
exit "$stock_status"
