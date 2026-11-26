#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C GOTOOLCHAIN=local
mode=stock
candidate=head
comparison_forward=ab
comparison_control=bb
if [[ "${1:-}" == --project-test ]]; then
    mode=projected
    candidate=projected
    comparison_forward=ap
    comparison_control=pp
    shift
elif [[ "${1:-}" == --linked-profile ]]; then
    mode=profile
    candidate=profiled
    shift
fi

schedule() {
    local group round order offset index name left right
    local comparisons=(aa:base:base "$comparison_forward:base:$candidate" "$comparison_control:$candidate:$candidate")
    printf 'comparison\tgroup\tpair\torder\tleft\tright\n'
    for group in 1 2; do
        for round in {1..10}; do
            order=left,right
            if (( (round+group)%2 )); then order=right,left; fi
            for offset in 0 1 2; do
                index=$(( (round+(group-1)*10+offset)%3 ))
                IFS=: read -r name left right <<< "${comparisons[index]}"
                printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$name" "$group" "$round" "$order" "$left" "$right"
            done
        done
    done
}

if [[ "${1:-}" == --schedule ]]; then
    schedule
    exit
fi
test "$#" = 0

base=dd70430a0d3f32ba400ed25922d431d206c48ade
head=cb0e8dca57971c1da049fc6033e62f91e4f57cac
cpu='Intel(R) Xeon(R) 6975P-C'
package=pkg/lux/api
symbols='BenchmarkRequestLog(Boundary|Delivered)|\.logRequest(Timing)?$|\.shouldLogRequest$|\.logAcceptedRequestTiming$|asyncRequestLogs\)\.(run|finishWorker|stopAdmission|submit|close)'
root=$(cd "${GITHUB_WORKSPACE:?}" && pwd -P)
work="$root/.tmp"
control="$work/pr83-control"
base_checkout="$work/pr83-base"
head_git="$work/pr83-head"
evidence="$work/pr83-log-boundary-evidence"
control_parent=$head
if [[ "$mode" == projected ]]; then
    evidence="$work/pr83-log-projected-evidence"
    control_parent=3a82a85f4c3a35512fcec0e6a220772ea515a80a
elif [[ "$mode" == profile ]]; then
    evidence="$work/pr83-log-profile-evidence"
    control_parent=46a7dee10d32fba4347317e2824ced18ea3df915
fi
verify="$control/scripts/diagnose-pr83-log-boundary.py"
g() { git --no-replace-objects -C "$@"; }

test "$(uname -sm)" = 'Linux x86_64'
test "$(sed -n 's/^actual=//p' "$evidence/runner-route.txt")" = "$cpu"
test "$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')" = "$cpu"
test "$(go env GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
test "$(g "$control" rev-list --parents -n 1 HEAD)" = "$GITHUB_SHA $control_parent"
if [[ "$mode" == projected ]]; then
    test "$(g "$control" rev-parse "$control_parent^{tree}")" = 698e4cacc33b095f7217d66a85070012bdadef3f
    test "$(g "$control" show -s --format=%P "$control_parent")" = "$head"
    diff -u <(printf 'A\t%s\nM\t%s\nM\t%s\n' .github/workflows/diagnose-pr83-log-boundary-project-test.yml \
        scripts/diagnose-pr83-log-boundary.py scripts/diagnose-pr83-log-boundary.sh) \
        <(g "$control" diff --name-status "$control_parent" HEAD)
elif [[ "$mode" == profile ]]; then
    test "$(g "$control" show -s --format=%P "$control_parent")" = 3a82a85f4c3a35512fcec0e6a220772ea515a80a
    diff -u <(printf 'A\t%s\nA\t%s\nM\t%s\nM\t%s\n' .github/workflows/diagnose-pr83-log-boundary-profile.yml \
        scripts/diagnose-pr83-log-boundary-tools.patch.gz scripts/diagnose-pr83-log-boundary.py scripts/diagnose-pr83-log-boundary.sh) \
        <(g "$control" diff --name-status "$control_parent" HEAD)
else
    diff -u <(printf 'A\t%s\n' .github/workflows/diagnose-pr83-log-boundary.yml scripts/diagnose-pr83-log-boundary.py scripts/diagnose-pr83-log-boundary.sh) \
        <(g "$control" diff --name-status "$head" HEAD)
fi
test "$(g "$base_checkout" rev-parse HEAD 'HEAD^{tree}')" = "$(printf '%s\n%s' "$base" 91dffe0aa1df765fa6c06da63dee7aff3b29aa79)"
test "$(g "$head_git" rev-parse HEAD 'HEAD^{tree}')" = "$(printf '%s\n%s' "$head" 094eba692d89593c05fb066302a3b7415aebd6ef)"
test "$(g "$head_git" cat-file commit "$head" | sed -n 's/^parent //p')" = \
    "$(printf '%s\n%s' f841a4770b83b368730db8c559df4a4da226b736 "$base")"
for checkout in "$base_checkout" "$head_git"; do
    g "$checkout" diff --quiet
    test -z "$(g "$checkout" ls-tree -r --name-only HEAD | awk '/(^|\/)\.gitattributes$/')"
done
build_inputs=(go.mod go.sum scripts/setup-modules.sh scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout)
g "$base_checkout" ls-tree HEAD -- "${build_inputs[@]}" > "$evidence/base-tool-inputs.txt"
g "$head_git" ls-tree HEAD -- "${build_inputs[@]}" > "$evidence/head-tool-inputs.txt"
cmp "$evidence/base-tool-inputs.txt" "$evidence/head-tool-inputs.txt"
cmp "$base_checkout/$package/router.go" "$head_git/$package/router.go"
for checkout in "$base_checkout" "$head_git"; do
    (cd "$checkout" && sha256sum "$package"/{router.go,request_log_async.go,request_log_async_test.go})
done > "$evidence/log-source.sha256"
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; } > "$evidence/environment.txt"

for name in go.work go.work.sum .tmp; do
    [[ ! -e "$base_checkout/$name" && ! -L "$base_checkout/$name" ]]
done
scratch=$(mktemp -d "$work/pr83-log-qualification.XXXXXXXX")
cleanup() {
    local result=$?
    trap - EXIT
    rm -rf -- "$scratch" "$base_checkout/.tmp" || result=1
    rm -f -- "$base_checkout/go.work" "$base_checkout/go.work.sum" || result=1
    printf 'exit=%s\n' "$result" > "$evidence/cleanup.txt"
    exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
tools="$scratch/bin"
head_archive="$scratch/head"
mkdir -p "$tools" "$head_archive/.tmp" "$base_checkout/.tmp" "$scratch/tmp"
export TMPDIR="$scratch/tmp" GOTMPDIR="$scratch/tmp" GOCACHE="$scratch/go-cache"

gh api repos/light-speak/luxo/actions/runs/45821350412/attempts/1 > "$evidence/original-run.json"
gh api repos/light-speak/luxo/actions/artifacts/15120416499 > "$evidence/original-metadata.json"
gh api repos/light-speak/luxo/actions/artifacts/15120416499/zip > "$scratch/original.zip"
python3 -I -B "$verify" plan "$scratch/original.zip" "$evidence/original-metadata.json" "$evidence/original-run.json" "$evidence"
if [[ "$mode" == projected ]]; then
    prior="$scratch/stock"
    mkdir "$prior"
    gh api repos/light-speak/luxo/actions/runs/45829013113/attempts/1 > "$prior/run.json"
    for slot in 1 2; do
        artifact=15121816705
        if [[ "$slot" == 2 ]]; then artifact=15122226439; fi
        gh api "repos/light-speak/luxo/actions/artifacts/$artifact" > "$prior/$slot.json"
        gh api "repos/light-speak/luxo/actions/artifacts/$artifact/zip" > "$prior/$slot.zip"
    done
    python3 -I -B "$verify" verify-stock "$prior" "$evidence"
elif [[ "$mode" == profile ]]; then
    prior="$scratch/projected"
    mkdir "$prior"
    gh api repos/light-speak/luxo/actions/runs/45831608901/attempts/1 > "$prior/run.json"
    for slot in 1 2; do
        artifact=15122089356
        if [[ "$slot" == 2 ]]; then artifact=15120989921; fi
        gh api "repos/light-speak/luxo/actions/artifacts/$artifact" > "$prior/$slot.json"
        gh api "repos/light-speak/luxo/actions/artifacts/$artifact/zip" > "$prior/$slot.zip"
    done
    python3 -I -B "$verify" verify-projected "$prior" "$evidence"
fi
unset GH_TOKEN
g "$head_git" archive "$head" | tar -xf - -C "$head_archive"
sh "$head_archive/scripts/setup-modules.sh" "$base_checkout"
sh "$head_archive/scripts/setup-modules.sh" "$head_archive"
tool_source="$head_git"
if [[ "$mode" == profile ]]; then
    tool_source="$scratch/tool-source"
    mkdir "$tool_source"
    g "$head_git" archive "$head" "${build_inputs[@]}" | tar -xf - -C "$tool_source"
    payload="$scratch/diagnose-pr83-log-boundary-tools.patch"
    gzip -dc "$control/scripts/diagnose-pr83-log-boundary-tools.patch.gz" > "$payload"
    printf '%s  %s\n' dfa94e89f35e11e4145c82b01f9ca7dc63870c42140c07de737bc99670d7dba1 "$payload" > "$evidence/tool-patch.sha256"
    sha256sum -c "$evidence/tool-patch.sha256"
    git -C "$tool_source" init --quiet
    g "$tool_source" add -- internal/benchlayout scripts/benchrun scripts/benchplan
    g "$tool_source" apply --index "$payload"
    g "$tool_source" ls-files --stage -- internal/benchlayout scripts/benchrun scripts/benchplan > "$evidence/tool-index.txt"
    printf '%s  %s\n' 6e6bd7003e5deef04b519e09ee291e544e6dbc7f470282617b0f467d833f2379 "$evidence/tool-index.txt" > "$evidence/tool-index.sha256"
    sha256sum -c "$evidence/tool-index.sha256"
fi
(
    cd "$tool_source"
    GOWORK=off go build -buildvcs=false -o "$tools/" ./scripts/benchrun ./scripts/benchplan ./scripts/benchgate
)
GOWORK=off GOBIN="$tools" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
"$tools/benchrun" performance-profile > "$evidence/rebuilt-profile.txt"
IFS=$'\t' read -r pkg profile filter < "$evidence/batch.tsv"
test "$pkg" = "$package" && test "$profile" = api-non-identity
if [[ "$mode" == profile ]]; then
    profile=api-request-log
    mv "$evidence/batch.tsv" "$evidence/witness-batch.tsv"
    printf '%s\t%s\t%s\n' "$pkg" "$profile" "$filter" > "$evidence/batch.tsv"
    grep -Fx 'api-request-log-overlay-v1 request_log_async_test.go=ce47aebb7d132885536fe9eb2fb4a12b1f08d7b0d68ecc3b5f24b10b4dd720dc 18953d4af73fe0cdc9d8cbced337215e73f2eade18d892a31322534ca3a9c022' "$evidence/rebuilt-profile.txt"
else
    cmp "$evidence/original-compile-profile.txt" "$evidence/rebuilt-profile.txt"
fi
"$tools/benchplan" --verify-profile-batch primary "$evidence/expected.json" "$evidence/batch.tsv"

source_for() {
    case "$1" in
        base) printf '%s\n' "$base_checkout" ;;
        head|projected|profiled) printf '%s\n' "$head_archive" ;;
        *) return 2 ;;
    esac
}

record_binary() {
    local arm=$1 binary="$tools/$1/pkg-lux-api-$profile.test"
    sha256sum "$binary" > "$evidence/$arm.sha256"
    go version -m "$binary" > "$evidence/$arm.build.txt"
    go tool buildid "$binary" > "$evidence/$arm.buildid.txt"
    readelf -WhlS "$binary" > "$evidence/$arm.readelf.txt"
    go tool nm -size -sort address "$binary" > "$scratch/$arm.nm"
    grep -E "$symbols" "$scratch/$arm.nm" > "$evidence/$arm.symbols.txt"
    if [[ "$mode" == projected || "$mode" == profile ]]; then
        verify_projected_symbols "$arm"
    fi
    go tool objdump -s "$symbols" "$binary" > "$evidence/$arm.objdump.txt"
    grep -q 'BenchmarkRequestLogBoundary' "$evidence/$arm.objdump.txt"
    grep -q 'BenchmarkRequestLogDelivered' "$evidence/$arm.objdump.txt"
    objcopy --dump-section ".text=$scratch/$arm.text" "$binary" "$scratch/$arm.objcopy"
    sha256sum "$scratch/$arm.text" > "$evidence/$arm.text.sha256"
    rm -- "$scratch/$arm.text" "$scratch/$arm.objcopy" "$scratch/$arm.nm"
}

verify_projected_symbols() {
    local arm=$1 symbol expected
    local prefix=github.com/light-speak/luxo/pkg/lux/api.
    awk '{print $4}' "$scratch/$arm.nm" > "$scratch/$arm.names"
    for symbol in BenchmarkRequestLogBoundary BenchmarkRequestLogBoundary.func1 BenchmarkRequestLogDelivered BenchmarkRequestLogDelivered.func1 \
        '(*Router).logRequestTiming' '(*asyncRequestLogs).run' '(*asyncRequestLogs).close'; do
        test "$(grep -Fxc "$prefix$symbol" "$scratch/$arm.names" || true)" = 1
    done
    expected=0
    if [[ "$arm" != base ]]; then expected=1; fi
    test "$(grep -Fxc "$prefix(*asyncRequestLogs).finishWorker" "$scratch/$arm.names" || true)" = "$expected"
    if grep -E 'TestAsyncRequestLogsAbnormalWriter(DrainsAndStops|PreservesFirstError)' "$scratch/$arm.names"; then
        return 1
    fi
}

verify_projection_inputs() {
    local fixture="$package/request_log_async_test.go"
    local product="$package/request_log_async.go"
    cmp "$head_archive/$fixture" "$head_git/$fixture"
    cmp "$head_archive/$product" "$head_git/$product"
    printf '%s  %s\n' ce47aebb7d132885536fe9eb2fb4a12b1f08d7b0d68ecc3b5f24b10b4dd720dc "$base_checkout/$fixture" \
        18953d4af73fe0cdc9d8cbced337215e73f2eade18d892a31322534ca3a9c022 "$head_archive/$fixture" \
        6dc7ca72cbfcf29fd5e6f30b6010b7b44bbaea9f65ecff6d96f7ba46915a1c48 "$head_archive/$product" \
        > "$evidence/projection-inputs.sha256"
    sha256sum -c "$evidence/projection-inputs.sha256"
}

project_test_fixture() {
    local fixture="$package/request_log_async_test.go"
    local product="$package/request_log_async.go"
    verify_projection_inputs
    cp "$base_checkout/$fixture" "$head_archive/$fixture"
    cmp "$head_archive/$fixture" "$base_checkout/$fixture"
    sha256sum "$head_archive/$fixture" "$head_archive/$product" > "$evidence/projection-compile.sha256"
}

sample() {
    "$tools/benchrun" run-performance "$(source_for "$1")" "$tools/$1" "$package" "$profile" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime="$2" -test.count=1 -test.cpu=2 >> "$3"
}

if [[ "$mode" == profile ]]; then verify_projection_inputs; fi
for arm in base "$candidate"; do
    mkdir "$tools/$arm"
    compile=compile-performance
    if [[ "$arm" == base ]]; then compile=compile-performance-baseline; fi
    if [[ "$arm" == projected ]]; then project_test_fixture; fi
    "$tools/benchrun" "$compile" "$(source_for "$arm")" "$tools/$arm" "$package" "$profile" > "$evidence/$arm.compile.log" 2>&1
    if [[ "$arm" == projected ]]; then
        fixture="$package/request_log_async_test.go"
        cp "$head_git/$fixture" "$head_archive/$fixture"
        sha256sum -c "$evidence/projection-inputs.sha256"
    fi
    record_binary "$arm"
    if [[ "$mode" == profile ]]; then
        sha256sum -c "$evidence/projection-inputs.sha256"
        sha256sum "$head_archive/$package/request_log_async_test.go" > "$evidence/$arm.original-head-fixture.sha256"
        awk '{print $2, $4}' "$evidence/$arm.symbols.txt" | sort > "$evidence/$arm.linked-symbol-sizes.txt"
        cmp "$evidence/$arm.linked-symbol-sizes.txt" "$evidence/witness-$arm.symbol-sizes.txt"
        ordinary_directory="$tools/$arm-stock"
        mkdir "$ordinary_directory"
        "$tools/benchrun" "$compile" "$(source_for "$arm")" "$ordinary_directory" "$package" api-non-identity \
            > "$evidence/$arm.stock-compile.log" 2>&1
        "$tools/benchrun" run-performance "$(source_for "$arm")" "$ordinary_directory" "$package" api-non-identity \
            -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
            > "$evidence/$arm.stock-inventory.raw"
        python3 -I -B "$verify" verify-samples 1 "$evidence/$arm.stock-inventory.raw"
        rm -- "$ordinary_directory/pkg-lux-api-api-non-identity.test"
        rmdir -- "$ordinary_directory"
    fi
    if [[ "$mode" == projected && "$arm" == base ]]; then
        cut -d ' ' -f 1 "$evidence/base.sha256" | cmp - "$evidence/stock-base.sha256"
        cut -d ' ' -f 1 "$evidence/base.text.sha256" | cmp - "$evidence/stock-base.text.sha256"
        cmp "$evidence/base.symbols.txt" "$evidence/stock-base.symbols.txt"
    fi
    sample "$arm" 1x "$evidence/$arm.inventory.raw"
    python3 -I -B "$verify" verify-samples 1 "$evidence/$arm.inventory.raw"
    if [[ "$mode" == profile ]]; then
        "$tools/benchplan" --verify-profile-inventory "$package" "$profile" \
            "$evidence/$arm.stock-inventory.raw" "$evidence/$arm.inventory.raw"
    fi
done

compare() {
    "$tools/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        "base=$1" "head=$2" > "$3.csv" 2> "$3.warnings.txt"
}

gate() {
    local name=$1 result=0
    shift
    "$tools/benchgate" "$@" > "$evidence/$name.gate.txt" 2>&1 || result=$?
    printf '%s\t%d\n' "$name" "$result" >> "$evidence/gates.tsv"
    return "$result"
}

schedule > "$evidence/order.tsv"
while IFS=$'\t' read -r comparison group pair order left right; do
    prefix="$evidence/$comparison.$group"
    if [[ "$order" == left,right ]]; then
        sample "$left" 200ms "$prefix.left.raw"
        sample "$right" 200ms "$prefix.right.raw"
    else
        sample "$right" 200ms "$prefix.right.raw"
        sample "$left" 200ms "$prefix.left.raw"
    fi
done < <(tail -n +2 "$evidence/order.tsv")

unstable=0
for group in 1 2; do
    for comparison in aa "$comparison_forward" "$comparison_control"; do
        prefix="$evidence/$comparison.$group"
        for side in left right; do
            python3 -I -B "$verify" verify-samples 10 "$prefix.$side.raw"
            "$tools/benchplan" --normalize "$prefix.$side.raw" "$prefix.$side.txt"
        done
        "$tools/benchplan" --verify "$evidence/expected.json" "$prefix.left.txt" "$prefix.right.txt"
        compare "$prefix.left.txt" "$prefix.right.txt" "$prefix"
        if [[ "$comparison" == "$comparison_forward" ]]; then continue; fi
        compare "$prefix.right.txt" "$prefix.left.txt" "$prefix.reverse"
        gate "$comparison.$group" "$prefix.csv" || unstable=1
        gate "$comparison.$group.reverse" "$prefix.reverse.csv" || unstable=1
    done
done
sha256sum -c "$evidence/base.sha256" "$evidence/$candidate.sha256"
if [[ "$mode" == profile ]]; then sha256sum -c "$evidence/projection-inputs.sha256"; fi
result=0
gate "$comparison_forward-combined" --approved-costs "$head_git/scripts/benchgate/approved-costs.json" --base-sha "$base" \
    "$evidence/$comparison_forward.1.csv" "$evidence/$comparison_forward.2.csv" || result=$?

if (( unstable )); then
    printf 'unstable-control\n' > "$evidence/outcome.txt"
    exit 4
fi
if (( result == 0 )); then
    printf 'no-confirmed-regression\n' > "$evidence/outcome.txt"
elif grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison_forward-combined.gate.txt"; then
    printf 'strict-performance-rejection\n' > "$evidence/outcome.txt"
else
    printf 'diagnostic-gate-failure\n' > "$evidence/outcome.txt"
fi
exit "$result"
