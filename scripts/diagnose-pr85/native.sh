#!/usr/bin/env bash
set -euo pipefail
umask 077

write_schedule() {
    local round offset index group left right order
    local groups=(AA BB AB1 AB2)
    printf 'pair\tgroup\tleft\tright\torder\n'
    for round in {1..10}; do
        for offset in {0..3}; do
            index=$(((round + offset - 1) % 4))
            group=${groups[$index]}
            left=A; right=B; order=LR
            case "$group" in AA) right=A ;; BB) left=B ;; esac
            if (( (round + index) % 2 == 0 )); then order=RL; fi
            printf '%s\t%s\t%s\t%s\t%s\n' "$round" "$group" "$left" "$right" "$order"
        done
    done
}

if [[ "${1:-}" == --print-schedule ]]; then
    write_schedule
    exit 0
fi
evidence=$(cd "${1:?missing evidence directory}" && pwd -P)
root=$(git rev-parse --show-toplevel)
cd "$root"
base=cbca8575fdd012835799a8f10a94f4c596d1e9d6
head=02c0e428f1e4abe27f049f2644f7b7fecd8dc90b
pkg=pkg/lux/api
scripts="$root/scripts/diagnose-pr85"
parse_fixture="$scripts/parse-log.go.txt"
shard_tool="$scripts/shards.py"
mode="${2:-route}"
[[ "$mode" == route || "$mode" == --shards ]]
cpu=$(lscpu | sed -n 's/^Model name:[[:space:]]*//p')
tool="$scripts/evidence.py"
hot='([.]BenchmarkLocalRouteExport|[.]localRouteSnapshot$|[.]BenchmarkDiagnosticWriteParseError|[.]ExportHandlers$|[.]writeParseError$|^runtime[.](gcWriteBarrier[0-9]*|writeBarrier)$)'

test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nv1\n1'
test "$GOMAXPROCS" = 2
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
test "$(git rev-parse "$base^{tree}")" = c64b011ae17958f80af20aa8b643c8b09ee1bb94
test "$(git rev-parse "$head^{tree}")" = 6cbf740716c176e0734b8f7fca513ee4f9007e6e
test "$(git diff --name-only "$base" "$head")" = "$(printf '%s\n' "$pkg/router.go" "$pkg/router_log_test.go")"
git diff --exit-code "$base" "$head" -- go.mod go.sum scripts/{benchrun,benchplan,benchgate,setup-modules.sh} \
    internal/benchlayout "$pkg/local_input_bench_test.go" "$pkg/http_abnormal_test.go"
if [[ "$mode" == route ]]; then
    test "$cpu" = 'Intel(R) Xeon(R) 6975P-C'
    printf '%s  %s\n' 647c12a482898f8d88427c41fcd70ec16b3934f8942552dd691b6f5ce555ea06 "$parse_fixture" | sha256sum -c -
    python3 -B "$tool" bind "$evidence/original.zip" "$evidence" > "$evidence/original-binding.json"
else
    python3 -B "$shard_tool" bind "$cpu" "$evidence" > "$evidence/lane.tsv"
    IFS=$'\t' read -r pkg shard_profile shard_filter hot < "$evidence/lane.tsv"
fi
{ go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; uname -a; lscpu; } > "$evidence/environment.txt"
git diff "$base" "$head" --stat > "$evidence/product-diff.txt"
for arm in "$base" "$head"; do
    for path in pkg/lux/api/{local_input_bench_test,http_abnormal_test,router,router_log_test}.go; do
        printf '%s %s %s\n' "$arm" "$(git rev-parse "$arm:$path")" "$path"
    done
done > "$evidence/source-blobs.txt"

mkdir -p .tmp
work=$(mktemp -d "$root/.tmp/pr85-local-route-work.XXXXXXXX")
cleanup() {
    [[ "$work" == "$root"/.tmp/pr85-local-route-work.* && -d "$work" && ! -L "$work" ]] || return 99
    rm -rf -- "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$work/tools" "$work/tool-source" "$work/test-tmp"
export TMPDIR="$work/test-tmp" GOWORK=off GOBIN="$work/tools"
git archive "$head" go.mod go.sum internal/benchlayout scripts/{benchrun,benchplan,benchgate} | tar -xf - -C "$work/tool-source"
(
    cd "$work/tool-source"
    for name in benchrun benchplan benchgate; do go build -o "$work/tools/$name" "./scripts/$name"; done
    go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
) > "$evidence/tools.log" 2>&1
benchrun="$work/tools/benchrun"
benchplan="$work/tools/benchplan"
benchgate="$work/tools/benchgate"
benchstat="$work/tools/benchstat"
go version -m "$benchstat" > "$evidence/benchstat-version.txt"
"$benchrun" performance-profile > "$evidence/profile.txt"
cmp "$evidence/profile.txt" "$evidence/original-profile.txt"
if [[ "$mode" == route ]]; then
    "$benchplan" --verify-profile-batch confirmation "$evidence/original-expected.json" "$evidence/original-batch.tsv"
else
    for selected in "$evidence"/shard-*.batch.tsv; do
        "$benchplan" --verify-profile-batch confirmation "${selected%.batch.tsv}.expected.json" "$selected"
    done
    "$benchplan" --verify-profile-batch confirmation "$evidence/shards.expected.json" "$evidence/shards.batch.tsv"
fi

isolate_route_tests() {
    local directory=$1 file
    if grep -nE '^func (init|TestMain)\(' "$directory/"*_test.go; then return 1; fi
    for file in "$directory/"*_test.go; do
        [[ "${file##*/}" == local_input_bench_test.go ]] || { rm -- "$file"; printf '%s\n' "${file##*/}"; }
    done | LC_ALL=C sort
}

build_arm() {
    local variant=$1 arm=$2 revision=$3 compile=$4 profile=$5 out=$6
    local source="$work/$variant-source/$arm" bins="$work/$variant-bin/$arm"
    local stem=${pkg//\//-} binary
    binary="$bins/$stem.test"
    if [[ "$profile" != default ]]; then binary="$bins/$stem-$profile.test"; fi
    mkdir -p "$source/.tmp" "$bins"
    git archive "$revision" | tar -xf - -C "$source"
    if [[ "$variant" != shards ]]; then
        git show "$revision:$pkg/local_input_bench_test.go" | cmp - "$source/$pkg/local_input_bench_test.go"
    fi
    case "$variant" in
        parse)
            test ! -e "$source/$pkg/diagnostic_write_parse_error_test.go"
            cp "$parse_fixture" "$source/$pkg/diagnostic_write_parse_error_test.go"
            ;;
        isolated)
            isolate_route_tests "$source/$pkg" > "$out/$arm.removed-tests.txt"
            ;;
    esac
    sh "$source/scripts/setup-modules.sh" "$source" > "$out/$arm.modules.log" 2>&1
    "$benchrun" "$compile" "$source" "$bins" "$pkg" "$profile" > "$out/$arm.compile.log" 2>&1
    sha256sum "$binary" > "$out/$arm.elf.sha256"
    go version -m "$binary" > "$out/$arm.build.txt"
    go tool buildid "$binary" > "$out/$arm.buildid.txt"
    readelf -WhSl "$binary" > "$out/$arm.elf-layout.txt"
    go tool nm -size "$binary" | grep -E "$hot" > "$out/$arm.hot.nm.txt"
    go tool objdump -s "$hot" "$binary" > "$out/$arm.hot.objdump.txt"
}

sample() {
    local variant=$1 arm=$2 profile=$3 filter=$4 span=$5
    "$benchrun" run-performance "$work/$variant-source/$arm" "$work/$variant-bin/$arm" "$pkg" "$profile" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime="$span" -test.count=1 -test.cpu=2 -test.timeout=90s
}

stat_pair() {
    "$benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        "base=$2" "head=$3" > "$1.csv" 2> "$1.warnings.txt"
}

measure() {
    local variant=$1 profile=$2 filter=$3 out=$4 round group left right order protocol side raw
    printf 'pair\tgroup\tleft\tright\torder\tUTC\n' > "$out/order.tsv"
    while IFS=$'\t' read -r round group left right order; do
        printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$round" "$group" "$left" "$right" "$order" "$(date -u +%FT%TZ)" >> "$out/order.tsv"
        if [[ "$order" == LR ]]; then
            sample "$variant" "$left" "$profile" "$filter" 200ms >> "$out/$group.left.raw.txt"
            sample "$variant" "$right" "$profile" "$filter" 200ms >> "$out/$group.right.raw.txt"
        else
            sample "$variant" "$right" "$profile" "$filter" 200ms >> "$out/$group.right.raw.txt"
            sample "$variant" "$left" "$profile" "$filter" 200ms >> "$out/$group.left.raw.txt"
        fi
    done < <(write_schedule | tail -n +2)
    for group in AA BB AB1 AB2; do
        protocol="$out/$group"
        for side in left right; do
            raw="$protocol.$side.raw.txt"
            test "$(grep -c '^cpu: ' "$raw")" -eq 10
            test "$(grep -Fxc "cpu: $cpu" "$raw")" -eq 10
            test "$(grep -Fxc PASS "$raw")" -eq 10
            "$benchplan" --normalize "$raw" "$protocol.$side.txt"
        done
        "$benchplan" --verify "$out/expected.json" "$protocol.left.txt" "$protocol.right.txt"
        stat_pair "$protocol" "$protocol.left.txt" "$protocol.right.txt"
    done
}

evaluate() {
    local out=$1 group code=0 control_failed=false
    for group in AA BB; do
        "$benchgate" "$out/$group.csv" > "$out/$group.gate.txt" 2>&1 || control_failed=true
        stat_pair "$out/$group.reverse" "$out/$group.right.txt" "$out/$group.left.txt"
        "$benchgate" "$out/$group.reverse.csv" > "$out/$group.reverse.gate.txt" 2>&1 || control_failed=true
    done
    "$benchgate" --approved-costs "$work/tool-source/scripts/benchgate/approved-costs.json" --base-sha "$base" \
        "$out/AB1.csv" "$out/AB2.csv" > "$out/AB.gate.txt" 2>&1 || code=$?
    printf 'control_failed=%s\ncomparison_exit=%s\n' "$control_failed" "$code" > "$out/verdict.txt"
    [[ "$control_failed" == false ]] || return 4
    return "$code"
}

compare_wire() {
    local profile=$1 out=$2 arm policy
    for arm in A B; do
        policy=original
        if [[ "$arm" == B ]]; then policy=category; fi
        LUXO_DIAGNOSTIC_PARSE_LOG="$policy" "$benchrun" run-performance "$work/parse-source/$arm" "$work/parse-bin/$arm" \
            "$pkg" "$profile" -test.run='^TestDiagnosticWriteParseErrorWire$' -test.v -test.count=1 -test.cpu=2 \
            > "$out/$arm.wire.log"
        sed -n 's/^.*parse-wire /parse-wire /p' "$out/$arm.wire.log" > "$out/$arm.wire.txt"
        test "$(wc -l < "$out/$arm.wire.txt")" -eq 6
    done
    cmp "$out/A.wire.txt" "$out/B.wire.txt"
}

run_variant() {
    local variant=$1 profile=$2 filter=$3 out="$evidence/$1" arm
    mkdir "$out"
    python3 -B "$tool" plan "$variant" > "$out/expected.json"
    build_arm "$variant" A "$base" compile-performance-baseline "$profile" "$out"
    build_arm "$variant" B "$head" compile-performance "$profile" "$out"
    if [[ "$variant" == isolated ]]; then cmp "$out/A.removed-tests.txt" "$out/B.removed-tests.txt"; fi
    if [[ "$variant" == parse ]]; then compare_wire "$profile" "$out"; fi
    for arm in A B; do
        sample "$variant" "$arm" "$profile" "$filter" 1x > "$out/$arm.fixture-1x.txt"
        python3 -B "$tool" smoke "$variant" "$out/$arm.fixture-1x.txt"
    done
    "$benchplan" --verify-equal-inventories "$out/A.fixture-1x.txt" "$out/B.fixture-1x.txt"
    python3 -B "$tool" compare "$variant" "$out" > "$out/linkage.json"
    measure "$variant" "$profile" "$filter" "$out"
}

if [[ "$mode" == --shards ]]; then
    out="$evidence/shards"
    mkdir "$out"
    cp "$evidence/shards.expected.json" "$out/expected.json"
    build_arm shards A "$base" compile-performance-baseline "$shard_profile" "$out"
    build_arm shards B "$head" compile-performance "$shard_profile" "$out"
    for arm in A B; do
        sample shards "$arm" "$shard_profile" "$shard_filter" 1x > "$out/$arm.fixture-1x.txt"
        python3 -B "$shard_tool" smoke "$cpu" "$evidence" "$out/$arm.fixture-1x.txt"
    done
    "$benchplan" --verify-equal-inventories "$out/A.fixture-1x.txt" "$out/B.fixture-1x.txt"
    python3 -B "$shard_tool" linkage "$evidence" "$out" > "$out/linkage.json"
    measure shards "$shard_profile" "$shard_filter" "$out"
    evaluate "$out"
    exit 0
fi

run_variant linked api-non-identity '^BenchmarkLocalRouteExport$/^routes=128$/^(previous|current)$'
linked_status=0
evaluate "$evidence/linked" || linked_status=$?
run_variant parse api-non-identity '^BenchmarkDiagnosticWriteParseError$'
parse_status=0
evaluate "$evidence/parse" || parse_status=$?
if [[ "$linked_status" == 1 ]] && grep -q 'confirmed performance regression: .*LocalRouteExport/routes=128/previous-2 ' "$evidence/linked/AB.gate.txt"; then
    run_variant isolated default '^BenchmarkLocalRouteExport$'
    isolation_status=0
    evaluate "$evidence/isolated" || isolation_status=$?
    printf 'isolation_status=%s\n' "$isolation_status" > "$evidence/isolated/status.txt"
fi
printf 'linked_status=%s\nparse_status=%s\n' "$linked_status" "$parse_status" > "$evidence/status.txt"
if [[ "$linked_status" != 0 ]]; then exit "$linked_status"; fi
exit "$parse_status"
