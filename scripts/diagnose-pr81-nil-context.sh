#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C GOTOOLCHAIN=local
base=f83f9c78f9ee03a5d73b7d0a41bae5280a250bfd
head=bb065672f26402b8f093ad2c49e00aa50da08f4e
diagnostic_parent=7665cba45e5f58bd7021d8e0399abc431f4ae901
cpu='Intel(R) Xeon(R) 6975P-C'
package=pkg/lux/rpc
symbols='BenchmarkRPCNilContextGuard|beforeNilContextGuard|beforeAuthenticationRecovery|Server\)\.(requestContext|verifyRequestContext)'
root=$(cd "${GITHUB_WORKSPACE:?}" && pwd -P)
work="$root/.tmp"
control="$work/pr81-control"
base_checkout="$work/pr81-base"
head_git="$work/pr81-head"
evidence="$work/pr81-nil-context-evidence"
source_after="$head_git"
g() { git --no-replace-objects -C "$@"; }

test "$(uname -sm)" = 'Linux x86_64'
test "$(sed -n 's/^actual=//p' "$evidence/runner-route.txt")" = "$cpu"
test "$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')" = "$cpu"
test "$(go env GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
test "$(g "$control" rev-list --parents -n 1 HEAD)" = "$GITHUB_SHA $diagnostic_parent"
test "$(g "$control" cat-file commit "$diagnostic_parent" | sed -n 's/^parent //p')" = 9cf385827185661b843e5d5cdcf179e998e3db66
diff -u <(printf 'M\t%s\n' scripts/diagnose-pr81-nil-context.sh) \
    <(g "$control" diff --name-status "$diagnostic_parent" HEAD)
test "$(g "$base_checkout" rev-parse HEAD 'HEAD^{tree}')" = "$(printf '%s\n%s' "$base" 0f97d642325769e2882fd0235c1575af71e3fc3c)"
test "$(g "$head_git" rev-parse HEAD 'HEAD^{tree}')" = "$(printf '%s\n%s' "$head" 8e978e8329322d891dc1f57c03bcc956de90f6b0)"
test "$(g "$head_git" cat-file commit "$head" | sed -n 's/^parent //p')" = \
    "$(printf '%s\n%s' ca2c9847d455c2d1aa76a5fc0476678ff055e0ac "$base")"
for checkout in "$base_checkout" "$head_git"; do
    g "$checkout" diff --quiet
    test -z "$(g "$checkout" ls-tree -r --name-only HEAD | awk '/(^|\/)\.gitattributes$/')"
done
build_inputs=(go.mod go.sum scripts/setup-modules.sh scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout)
g "$base_checkout" ls-tree HEAD -- "${build_inputs[@]}" > "$evidence/base-tool-inputs.txt"
g "$head_git" ls-tree HEAD -- "${build_inputs[@]}" > "$evidence/head-tool-inputs.txt"
cmp "$evidence/base-tool-inputs.txt" "$evidence/head-tool-inputs.txt"
(cd "$base_checkout" && sha256sum -c -) > "$evidence/fixture-check.log" <<'HASHES'
313870a1f66bbde4e83bfcd88841ccaf70135855ddd5f8d80d308b968736da0a  pkg/lux/rpc/server.go
2a4f649d14a84dc3fd6009f9252fdc161282962030ec961d47821efd9c899917  pkg/lux/rpc/auth_context_bench_test.go
HASHES
for filename in server.go auth_context_bench_test.go; do
    cmp "$base_checkout/$package/$filename" "$source_after/$package/$filename"
done
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; } > "$evidence/environment.txt"

for filename in go.work go.work.sum .tmp; do
    [[ ! -e "$base_checkout/$filename" && ! -L "$base_checkout/$filename" ]]
done
scratch=$(mktemp -d "$work/pr81-nil-context.XXXXXXXX")
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
mkdir -p "$input" "$tools"/{base,head} "$head_archive/.tmp" "$base_checkout/.tmp" "$scratch/tmp"
export TMPDIR="$scratch/tmp" GOTMPDIR="$scratch/tmp" GOCACHE="$scratch/go-cache"
gh api repos/light-speak/luxo/actions/runs/45811273426/attempts/1 > "$evidence/original-run.json"
gh api repos/light-speak/luxo/actions/artifacts/15114528645 > "$evidence/original-metadata.json"
gh api repos/light-speak/luxo/actions/artifacts/15114528645/zip > "$input/original.zip"
python3 -I -B - "$input/original.zip" "$evidence" "$base" "$head" "$cpu" <<'PY'
from hashlib import sha256
import json
from pathlib import Path
import re
import sys
from zipfile import ZipFile

artifact, out = map(Path, sys.argv[1:3])
base, head, cpu = sys.argv[3:]
digest = "ae685b36a6b3fc0eeaa5355db5579fc0cf8bfb04a57a05adcf2c78fde5377325"
assert sha256(artifact.read_bytes()).hexdigest() == digest
meta = json.loads((out / "original-metadata.json").read_text())
assert meta["id"] == 15114528645 and meta["digest"] == f"sha256:{digest}"
run = json.loads((out / "original-run.json").read_text())
assert (run["id"],run["run_attempt"],run["head_sha"],run["event"],run["conclusion"]) == (
    45811273426,1,head,"workflow_dispatch","failure")
assert meta["workflow_run"]["id"] == run["id"] and meta["workflow_run"]["head_sha"] == head
with ZipFile(artifact) as z:
    assert z.testzip() is None
    assert z.read("benchmark-revisions.txt").decode().splitlines() == [f"base_sha={base}",f"tested_sha={head}"]
    detected = re.findall(r"^Model name:\s*(.*)$", z.read("benchmark-environment.txt").decode(), re.M)
    assert [s.strip() for s in detected] == [cpu]
    expected = json.loads(z.read("benchmark-confirmation-expected.json"))
    selectors = [(case, part) for case in ("NoToken","Rejected") for part in ("After","Before")]
    names = [f"BenchmarkRPCNilContextGuard/{case}/{part}-2" for case,part in selectors]
    leaves = [{"Package":"pkg/lux/rpc","Name":name} for name in names]
    assert expected["Base"] == expected["Head"] == leaves
    family = [{"Package":"pkg/lux/rpc","Name":f"BenchmarkRPCNilContextGuard/{case}/{part}-2"}
              for case in ("NoToken", "Verified", "Rejected", "Unconfigured")
              for part in ("Before", "GuardOnly", "After")]
    (out / "family-expected.json").write_text(json.dumps({"Base":family,"Head":family}, indent=2)+"\n")
    filter_ = "|".join(f"^BenchmarkRPCNilContextGuard$/^{case}$/^{part}$" for case,part in selectors)
    assert z.read("benchmark-confirmation-batch.tsv") == f"pkg/lux/rpc\tdefault\t{filter_}\n".encode()
    assert z.read("benchmark-confirmation-targets.tsv").decode().splitlines() == [f"pkg/lux/rpc\t{name}" for name in names]
    gate = z.read("benchmark-gate.txt").decode()
    failures = re.findall(r"^confirmed performance regression: .*?/RPCNilContextGuard/(\S+) sec/op ",gate,re.M)
    assert sorted(failures) == ["NoToken/After-2","Rejected/After-2"]
    for upstream,target in {
        "benchmark-gate.txt":"original-gate.txt",
        "benchmark-revisions.txt":"benchmark-revisions.txt",
        "benchmark-compile-profile.txt":"reference-profile.txt",
        "benchmark-confirmation-batch.tsv":"batch.tsv",
        "benchmark-confirmation-expected.json":"expected.json",
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
test "$pkg" = "$package" && test "$profile" = default
"$tools/benchplan" --verify-profile-batch confirmation "$evidence/expected.json" "$evidence/batch.tsv"

source_for() {
    case "$1" in
        base) printf '%s\n' "$base_checkout" ;;
        head) printf '%s\n' "$head_archive" ;;
        *) return 2 ;;
    esac
}
compile_arms() {
    local mode=$1 results="$evidence/$1" arm tree command binary section
    mkdir "$results"
    for arm in base head; do
        mkdir -p "$tools/$mode/$arm"
        tree=$(source_for "$arm")
        binary="$tools/$mode/$arm/pkg-lux-rpc.test"
        if [[ "$mode" == stock ]]; then
            command=compile-performance
            if [[ "$arm" == base ]]; then command=compile-performance-baseline; fi
            "$tools/benchrun" "$command" "$tree" "$tools/$mode/$arm" "$package" "$profile" \
                > "$results/$arm.compile.log" 2>&1
        else
            python3 -I -B - "$tree/$package" "$scratch/$arm.overlay" "$scratch/$arm.projected" "$results/$arm.overlay.json" <<'OVERLAY'
from hashlib import sha256
import json
from pathlib import Path
import sys

package, overlay, projected_dir, receipt = map(Path, sys.argv[1:])
files = sorted(package.glob("*_test.go"))
fixture = package / "auth_context_bench_test.go"
assert fixture in files and len(files) > 1
assert all(p.is_file() and not p.is_symlink() for p in files)
assert not any("func TestMain(" in p.read_text() for p in files)
source = fixture.read_bytes()
assert sha256(source).hexdigest() == "2a4f649d14a84dc3fd6009f9252fdc161282962030ec961d47821efd9c899917"
prefix, _ = source.split(b"\nvar authEncodedSink []byte\n")
# Keep original source lines and the entire timed benchmark intact.
projected_bytes = prefix.replace(b'\t"bytes"\n', b'\n', 1).replace(b'\t"strings"\n', b'\n', 1) + b"\n"
projected_digest = sha256(projected_bytes).hexdigest()
assert projected_digest == "cd1b4a5dd0d0622ce19cfc144057482a1059cd61245cf38cf9205f46aaece2c3"
projected_dir.mkdir()
projected = projected_dir / fixture.name
projected.write_bytes(projected_bytes)
removed = [p for p in files if p != fixture]
replacements = {str(p): "" for p in removed}
replacements[str(fixture)] = str(projected)
overlay.write_text(json.dumps({"Replace": replacements}))
receipt.write_text(json.dumps({"source":fixture.name,"sha256":projected_digest,
                               "removed":[p.name for p in removed]}, indent=2)+"\n")
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
        grep -E '[[:space:]]T[[:space:]].*\.Benchmark[A-Z][^.[:space:]]*$' "$scratch/$arm.nm" > "$results/$arm.benchmark-roots.txt"
        if [[ "$mode" == isolated ]]; then
            test "$(awk '{print $4}' "$results/$arm.benchmark-roots.txt" | grep '^github.com/light-speak/luxo/pkg/lux/rpc\.')" = \
                github.com/light-speak/luxo/pkg/lux/rpc.BenchmarkRPCNilContextGuard
        fi
        go tool objdump -s "$symbols" "$binary" > "$results/$arm.objdump.txt"
        grep -q 'BenchmarkRPCNilContextGuard' "$results/$arm.objdump.txt"
        grep -q 'auth_context_bench_test.go:' "$results/$arm.objdump.txt"
        for section in text rodata gopclntab; do
            objcopy --dump-section ".$section=$scratch/$arm.$section" "$binary" "$scratch/$arm.objcopy"
            sha256sum "$scratch/$arm.$section" > "$results/$arm.$section.sha256"
            rm -- "$scratch/$arm.$section" "$scratch/$arm.objcopy"
        done
        rm -- "$scratch/$arm.nm"
        "$tools/benchrun" run-performance "$tree" "$tools/$mode/$arm" "$package" "$profile" \
            -test.run='^$' -test.bench='^BenchmarkRPCNilContextGuard$' -test.benchmem \
            -test.benchtime=1x -test.count=1 -test.cpu=2 > "$results/$arm.family.raw"
        "$tools/benchplan" --normalize "$results/$arm.family.raw" "$results/$arm.family.txt"
        test "$(sed -n 's/^cpu: //p' "$results/$arm.family.txt" | sort -u)" = "$cpu"
    done
    "$tools/benchplan" --verify "$evidence/family-expected.json" "$results/base.family.txt" "$results/head.family.txt"
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
                test "$(grep -c '^PASS$' "$prefix.$side.raw")" = 10
                awk '/^BenchmarkRPCNilContextGuard\// {count[$1]++}
                     END {if (length(count) != 4) exit 1; for (key in count) if (count[key] != 10) exit 1}' "$prefix.$side.raw"
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

# Child Bash keeps errexit active while the parent stores each result.
export base cpu evidence package profile filter tools base_checkout head_archive head_git
export -f source_for sample compare gate schedule_pairs run_comparisons
stock_status=0
isolated_status=0
compile_arms stock
BASH_ENV=/dev/null bash -c 'set -euo pipefail; run_comparisons "$1"' _ stock || stock_status=$?
compile_arms isolated
cmp <(jq -r .sha256 "$evidence/isolated/base.overlay.json") <(jq -r .sha256 "$evidence/isolated/head.overlay.json")
BASH_ENV=/dev/null bash -c 'set -euo pipefail; run_comparisons "$1"' _ isolated || isolated_status=$?
printf 'stock=%s\nisolated=%s\noriginal-formal-gate=retained\n' "$stock_status" "$isolated_status" > "$evidence/outcome.txt"
if (( stock_status != 1 )) || [[ "$(cat "$evidence/stock/outcome.txt")" != strict-performance-rejection ]]; then
    printf 'verdict=stock-positive-control-unconfirmed\n' >> "$evidence/outcome.txt"
    exit 5
fi
if ! grep -Fxq 'performance gate rejected 2 regression(s)' "$evidence/stock/ab-combined.gate.txt" ||
   ! diff -u <(printf 'NoToken/After-2\nRejected/After-2\n') \
    <(sed -n 's@^confirmed performance regression: .*\/RPCNilContextGuard/\([^ ]*\) sec/op .*@\1@p' "$evidence/stock/ab-combined.gate.txt" | sort); then
    printf 'verdict=stock-positive-control-changed\n' >> "$evidence/outcome.txt"
    exit 6
fi
if (( isolated_status != 0 )); then
    printf 'verdict=isolated-comparison-failed\n' >> "$evidence/outcome.txt"
    exit "$isolated_status"
fi
printf 'verdict=isolated-passes-stock-rejects\n' >> "$evidence/outcome.txt"
