#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C GOTOOLCHAIN=local

schedule() {
    local group round order offset index name left right
    local comparisons=(aa:base:base ab:base:head bb:head:head)
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
verify="$control/scripts/diagnose-pr83-log-boundary.py"
g() { git --no-replace-objects -C "$@"; }

test "$(uname -sm)" = 'Linux x86_64'
test "$(sed -n 's/^actual=//p' "$evidence/runner-route.txt")" = "$cpu"
test "$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')" = "$cpu"
test "$(go env GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
test "$(g "$control" rev-list --parents -n 1 HEAD)" = "$GITHUB_SHA $head"
diff -u <(printf 'A\t%s\n' .github/workflows/diagnose-pr83-log-boundary.yml scripts/diagnose-pr83-log-boundary.py scripts/diagnose-pr83-log-boundary.sh) \
    <(g "$control" diff --name-status "$head" HEAD)
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
unset GH_TOKEN
python3 -I -B "$verify" plan "$scratch/original.zip" "$evidence/original-metadata.json" "$evidence/original-run.json" "$evidence"
g "$head_git" archive "$head" | tar -xf - -C "$head_archive"
sh "$head_archive/scripts/setup-modules.sh" "$base_checkout"
sh "$head_archive/scripts/setup-modules.sh" "$head_archive"
(
    cd "$head_git"
    GOWORK=off go build -o "$tools/" ./scripts/benchrun ./scripts/benchplan ./scripts/benchgate
)
GOWORK=off GOBIN="$tools" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
"$tools/benchrun" performance-profile > "$evidence/rebuilt-profile.txt"
cmp "$evidence/original-compile-profile.txt" "$evidence/rebuilt-profile.txt"
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

record_binary() {
    local arm=$1 binary="$tools/$1/pkg-lux-api-api-non-identity.test"
    sha256sum "$binary" > "$evidence/$arm.sha256"
    go version -m "$binary" > "$evidence/$arm.build.txt"
    go tool buildid "$binary" > "$evidence/$arm.buildid.txt"
    readelf -WhlS "$binary" > "$evidence/$arm.readelf.txt"
    go tool nm -size -sort address "$binary" > "$scratch/$arm.nm"
    grep -E "$symbols" "$scratch/$arm.nm" > "$evidence/$arm.symbols.txt"
    go tool objdump -s "$symbols" "$binary" > "$evidence/$arm.objdump.txt"
    grep -q 'BenchmarkRequestLogBoundary' "$evidence/$arm.objdump.txt"
    grep -q 'BenchmarkRequestLogDelivered' "$evidence/$arm.objdump.txt"
    objcopy --dump-section ".text=$scratch/$arm.text" "$binary" "$scratch/$arm.objcopy"
    sha256sum "$scratch/$arm.text" > "$evidence/$arm.text.sha256"
    rm -- "$scratch/$arm.text" "$scratch/$arm.objcopy" "$scratch/$arm.nm"
}

sample() {
    "$tools/benchrun" run-performance "$(source_for "$1")" "$tools/$1" "$package" "$profile" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime="$2" -test.count=1 -test.cpu=2 >> "$3"
}

for arm in base head; do
    mkdir "$tools/$arm"
    compile=compile-performance
    if [[ "$arm" == base ]]; then compile=compile-performance-baseline; fi
    "$tools/benchrun" "$compile" "$(source_for "$arm")" "$tools/$arm" "$package" "$profile" > "$evidence/$arm.compile.log" 2>&1
    record_binary "$arm"
    sample "$arm" 1x "$evidence/$arm.inventory.raw"
    python3 -I -B "$verify" verify-samples 1 "$evidence/$arm.inventory.raw"
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
    for comparison in aa ab bb; do
        prefix="$evidence/$comparison.$group"
        for side in left right; do
            python3 -I -B "$verify" verify-samples 10 "$prefix.$side.raw"
            "$tools/benchplan" --normalize "$prefix.$side.raw" "$prefix.$side.txt"
        done
        "$tools/benchplan" --verify "$evidence/expected.json" "$prefix.left.txt" "$prefix.right.txt"
        compare "$prefix.left.txt" "$prefix.right.txt" "$prefix"
        if [[ "$comparison" == ab ]]; then continue; fi
        compare "$prefix.right.txt" "$prefix.left.txt" "$prefix.reverse"
        gate "$comparison.$group" "$prefix.csv" || unstable=1
        gate "$comparison.$group.reverse" "$prefix.reverse.csv" || unstable=1
    done
done
sha256sum -c "$evidence"/{base,head}.sha256
result=0
gate ab-combined --approved-costs "$head_git/scripts/benchgate/approved-costs.json" --base-sha "$base" \
    "$evidence/ab.1.csv" "$evidence/ab.2.csv" || result=$?

if (( unstable )); then
    printf 'unstable-control\n' > "$evidence/outcome.txt"
    exit 4
fi
if (( result == 0 )); then
    printf 'no-confirmed-regression\n' > "$evidence/outcome.txt"
elif grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/ab-combined.gate.txt"; then
    printf 'strict-performance-rejection\n' > "$evidence/outcome.txt"
else
    printf 'diagnostic-gate-failure\n' > "$evidence/outcome.txt"
fi
exit "$result"
