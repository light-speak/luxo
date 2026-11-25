#!/usr/bin/env bash
set -euo pipefail
umask 077
evidence=$(cd "${1:?missing evidence directory}" && pwd -P)
mode="${2:-}"
[[ "$mode" == '' || "$mode" == --also-isolate ]]
root=$(git rev-parse --show-toplevel)
cd "$root"
base=e101cf7699acb358305fd373934e8813a98087bd
head=389c14b8a01891faee926b798039d02f5404f4aa
package=pkg/lux/luvia
profile=default
filter='^BenchmarkValidateResolvedFields$/^list-small-128x16$/^per-value$'
hot='([.]BenchmarkValidateResolvedFields|[.]validateResolvedPerValue|[.]skipModelResponsePerValue|[.]ValidateResolvedFields|[.]skipResponseField|[.]nestedResponseFields|[.]responseFieldByID|[.]NextField$)'
fixture=(grouped_response_bench_test.go grouped_response_test.go merger_bench_test.go primary_keys_bench_test.go primary_keys_baseline_test.go)
tool=scripts/diagnose-pr101-resolved-fields.py

test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
test "$(git rev-parse "$base^{tree}")" = 377fdb5bbe6815a45930c53e10797f08cf07c917
test "$(git rev-parse "$head^{tree}")" = 21fe7601aa4a57f290d4e5fb4f0b0f67efc8bf89
diff -u <(printf '%s\n' pkg/lux/h2transport/request_headers.go pkg/lux/h2transport/request_headers_test.go) \
    <(git diff --name-only "$base" "$head")
git diff --exit-code "$base" "$head" -- "$package" scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout
git diff --exit-code "$head" HEAD -- go.mod go.sum scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout
python3 -B "$tool" bind "$evidence/original.zip" "$evidence" > "$evidence/original-binding.json"
{ go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; uname -a; lscpu; } > "$evidence/environment.txt"
git diff --stat "$base" "$head" > "$evidence/product-diff.txt"
for file in merger.go response_skip.go "${fixture[@]}"; do
    git show "$head:$package/$file" > "$evidence/$file"
    printf '%s  %s\n' "$(git rev-parse "$head:$package/$file")" "$package/$file"
done > "$evidence/workload-blobs.txt"
cat > "$evidence/workload-stages.txt" <<'TEXT'
Setup outside b.Loop: resolvedValidationFixture builds the 128-row, 16-field list and registry. The existing benchmark checks validation succeeds before entering the loop.
The timed leaf calls the frozen validateResolvedPerValue traversal, resolves field metadata, then validates known-good payloads through the codec decoder and field checks. No HTTP/2 request frames or header parsing are timed.
A/B source and fixture bytes are checked. The optional test-file isolation is a separate linkage experiment and retains these same original fixture bytes and all production code.
TEXT

mkdir -p .tmp
work=$(mktemp -d "$root/.tmp/pr101-resolved-fields-work.XXXXXXXX")
cleanup() {
    [[ "$work" == "$root"/.tmp/pr101-resolved-fields-work.* && -d "$work" && ! -L "$work" ]] || return 99
    rm -rf -- "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$work/tools" "$work/test-tmp"
export TMPDIR="$work/test-tmp" GOWORK=off
for binary in benchrun benchplan benchgate; do
    go build -o "$work/tools/$binary" "./scripts/$binary"
done
export GOBIN="$work/tools"
go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
go version -m "$work/tools/benchstat" > "$evidence/benchstat-version.txt"
benchrun="$work/tools/benchrun"
benchplan="$work/tools/benchplan"
benchgate="$work/tools/benchgate"
benchstat="$work/tools/benchstat"
expected="$evidence/expected.json"
"$benchrun" performance-profile > "$evidence/profile.txt"
cmp "$evidence/profile.txt" "$evidence/original-profile.txt"
"$benchplan" --verify-profile-batch confirmation "$expected" "$evidence/confirmation-batch.tsv"

isolate() {
    local source=$1 file name
    if grep -nE '^func (init|TestMain)\(' "$source/$package/"*_test.go; then return 1; fi
    for file in "$source/$package/"*_test.go; do
        name=${file##*/}
        case "$name" in
            grouped_response_bench_test.go|grouped_response_test.go|merger_bench_test.go|primary_keys_bench_test.go|primary_keys_baseline_test.go) ;;
            *) rm -- "$file"; printf '%s\n' "$name" ;;
        esac
    done | LC_ALL=C sort
}

build_arm() {
    local variant=$1 arm=$2 revision=$3 compile=$4 out=$5
    local source="$work/$variant-source/$arm" bins="$work/$variant-bin/$arm" binary
    mkdir -p "$source" "$bins"
    git archive "$revision" | tar -xf - -C "$source"
    if [[ "$variant" == isolated ]]; then isolate "$source" > "$out/$arm.removed-tests.txt"; fi
    for file in merger.go response_skip.go "${fixture[@]}"; do
        git show "$revision:$package/$file" | cmp - "$source/$package/$file"
    done
    sh "$source/scripts/setup-modules.sh" "$source" > "$out/$arm.modules.log" 2>&1
    "$benchrun" "$compile" "$source" "$bins" "$package" "$profile" > "$out/$arm.compile.log" 2>&1
    binary="$bins/pkg-lux-luvia.test"
    sha256sum "$binary" > "$out/$arm.elf.sha256"
    go version -m "$binary" > "$out/$arm.build.txt"
    go tool buildid "$binary" > "$out/$arm.buildid.txt"
    readelf -WhSl "$binary" > "$out/$arm.elf-layout.txt"
    go tool nm -size "$binary" | grep -E "$hot" > "$out/$arm.nm.txt"
    go tool objdump -s "$hot" "$binary" > "$out/$arm.hot.objdump.txt"
    sample "$variant" "$arm" 1x > "$out/$arm.fixture-1x.txt"
}

sample() {
    local variant=$1 arm=$2 span=$3
    "$benchrun" run-performance "$work/$variant-source/$arm" "$work/$variant-bin/$arm" "$package" "$profile" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime="$span" -test.count=1 -test.cpu=2
}

statistics() {
    local out=$1 group=$2
    for side in left right; do
        "$benchplan" --normalize "$out/$group.$side.raw.txt" "$out/$group.$side.txt"
    done
    "$benchplan" --verify "$expected" "$out/$group.left.txt" "$out/$group.right.txt"
    "$benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        "base=$out/$group.left.txt" "head=$out/$group.right.txt" > "$out/$group.csv" 2> "$out/$group.warnings.txt"
}

measure() {
    local variant=$1 out=$2 round offset group_index group left right order stamp
    local groups=(AA BB AB1 AB2)
    for round in {1..10}; do
        for offset in {0..3}; do
            group_index=$(((round + offset - 1) % 4))
            group=${groups[$group_index]}
            left=A; right=B
            case "$group" in AA) right=A ;; BB) left=B ;; esac
            order=LR
            if (( (round + group_index) % 2 == 0 )); then order=RL; fi
            stamp=$(date -u +%FT%TZ)
            printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$round" "$group" "$left" "$right" "$order" "$stamp" >> "$out/order.tsv"
            if [[ "$order" == LR ]]; then
                sample "$variant" "$left" 200ms >> "$out/$group.left.raw.txt"
                sample "$variant" "$right" 200ms >> "$out/$group.right.raw.txt"
            else
                sample "$variant" "$right" 200ms >> "$out/$group.right.raw.txt"
                sample "$variant" "$left" 200ms >> "$out/$group.left.raw.txt"
            fi
        done
    done
    for group in "${groups[@]}"; do statistics "$out" "$group"; done
}

evaluate() {
    local out=$1 group code control_failed=false
    for group in AA BB; do
        if ! "$benchgate" "$out/$group.csv" > "$out/$group.gate.txt" 2>&1; then control_failed=true; fi
        "$benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
            "base=$out/$group.right.txt" "head=$out/$group.left.txt" > "$out/$group.reverse.csv" 2> "$out/$group.reverse.warnings.txt"
        if ! "$benchgate" "$out/$group.reverse.csv" > "$out/$group.reverse.gate.txt" 2>&1; then control_failed=true; fi
    done
    code=0
    "$benchgate" "$out/AB1.csv" "$out/AB2.csv" > "$out/AB.gate.txt" 2>&1 || code=$?
    printf 'control_failed=%s\ncomparison_exit=%s\n' "$control_failed" "$code" > "$out/verdict.txt"
    [[ "$control_failed" == false ]] || return 4
    return "$code"
}

run_variant() {
    local variant=$1 out="$evidence/$1"
    mkdir "$out"
    build_arm "$variant" A "$base" compile-performance-baseline "$out"
    build_arm "$variant" B "$head" compile-performance "$out"
    if [[ "$variant" == isolated ]]; then cmp "$out/A.removed-tests.txt" "$out/B.removed-tests.txt"; fi
    "$benchplan" --verify-equal-inventories "$out/A.fixture-1x.txt" "$out/B.fixture-1x.txt"
    for arm in A B; do
        python3 -B "$tool" smoke "$out/$arm.fixture-1x.txt"
    done
    python3 -B "$tool" compare "$out" > "$out/linkage.json"
    measure "$variant" "$out"
}

run_variant default
default_status=0
evaluate "$evidence/default" || default_status=$?
if [[ "$mode" == --also-isolate && "$default_status" == 1 ]] &&
   grep -Fq "ValidateResolvedFields/list-small-128x16/per-value-2 sec/op" "$evidence/default/AB.gate.txt"; then
    run_variant isolated
    isolated_status=0
    evaluate "$evidence/isolated" || isolated_status=$?
    printf 'isolated_status=%s\n' "$isolated_status" > "$evidence/isolated/status.txt"
fi
exit "$default_status"
