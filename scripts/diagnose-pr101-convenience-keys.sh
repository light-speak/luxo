#!/usr/bin/env bash
set -euo pipefail
umask 077
evidence=$(cd "${1:?missing evidence directory}" && pwd -P)
mode="${2:-}"
[[ "$mode" == '' || "$mode" == --also-isolate || "$mode" == --plan-only ]]
root=$(git rev-parse --show-toplevel)
cd "$root"
base=f9ce659fd7f17bf57cfe0aee7de7ecb8d9b8f2e4
head=39d329d07d7729b4b4ddb5915e08bf9d5d07ea9d
package=pkg/lux/luvia
profile=default
filter='^BenchmarkConvenienceKeyParentValidation$/^column-small$/^keyLast=false$/^current$|^BenchmarkConvenienceKeyParentValidation$/^column-small$/^keyLast=true$/^current$'
hot='([.]BenchmarkConvenienceKeyParentValidation|[.]benchmarkConvenienceKeys|[.]ExtractIDColumn|[.]extractIDColumnBeforeParentValidation|[.]walkColumnar|^runtime[.](memmove|growslice|memclrNoHeapPointers)$|^testing[.].*[.]Loop$)'
fixture=(convenience_keys_bench_test.go primary_keys_bench_test.go primary_keys_baseline_test.go)
tool=scripts/diagnose-pr101-convenience-keys.py

if [[ "$mode" != --plan-only ]]; then
  test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nv1\n1'
  test -z "$(go env GOFLAGS)"
fi
test "$(git rev-parse "$base^{tree}")" = 68591bb02805a9c5a95e47864c3be1abb6e256cb
test "$(git rev-parse "$head^{tree}")" = 66580ef843d8943da4546a0ef8abbeba676735a0
diff -u <(printf '%s\n' pkg/lux/h2transport/request_headers.go pkg/lux/h2transport/request_headers_test.go) \
    <(git diff --name-only "$base" "$head")
git diff --exit-code "$base" "$head" -- "$package" scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout
git diff --exit-code "$head" HEAD -- go.mod go.sum scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout
python3 -B "$tool" bind "$evidence/original.zip" "$evidence" > "$evidence/original-binding.json"
if [[ "$mode" != --plan-only ]]; then
    { go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; uname -a; lscpu; } > "$evidence/environment.txt"
fi
git diff --stat "$base" "$head" > "$evidence/product-diff.txt"
for file in merger.go "${fixture[@]}"; do
    git show "$head:$package/$file" > "$evidence/$file"
    printf '%s  %s\n' "$(git rev-parse "$head:$package/$file")" "$package/$file"
done > "$evidence/workload-blobs.txt"
cat > "$evidence/workload-stages.txt" <<'TEXT'
The original benchmark uses primaryKeysBenchmarkFixture(1, 2, true, keyLast), with both key positions.
It checks that the legacy extractor and ExtractIDColumn agree before entering b.Loop.
The measured loop calls ExtractIDColumn(data, 1, model) on pinned column bytes. It does not call HTTP/2 request parsing.
The isolated diagnostic removes unrelated Luvia test declarations in both arms, while retaining all production files and the exact original benchmark fixture.
TEXT
if [[ "$mode" == --plan-only ]]; then exit 0; fi

mkdir -p .tmp
work=$(mktemp -d "$root/.tmp/pr101-convenience-keys-work.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$work/tools" "$work/test-tmp"
export TMPDIR="$work/test-tmp" GOTMPDIR="$work/test-tmp" GOWORK=off
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
    for file in "$source/$package/"*_test.go; do
        name=${file##*/}
        case "$name" in
            convenience_keys_bench_test.go|primary_keys_bench_test.go|primary_keys_baseline_test.go) ;;
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
    for file in merger.go "${fixture[@]}"; do
        git show "$revision:$package/$file" | cmp - "$source/$package/$file"
    done
    sh "$source/scripts/setup-modules.sh" "$source" > "$out/$arm.modules.log" 2>&1
    "$benchrun" "$compile" "$source" "$bins" "$package" "$profile" > "$out/$arm.compile.log" 2>&1
    binary="$bins/pkg-lux-luvia.test"
    sha256sum "$binary" > "$out/$arm.elf.sha256"
    go version -m "$binary" > "$out/$arm.build.txt"
    go tool buildid "$binary" > "$out/$arm.buildid.txt"
    readelf -WhSl "$binary" > "$out/$arm.elf-layout.txt"
    go tool nm -size "$binary" > "$out/$arm.nm.txt"
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
    python3 -B "$tool" samples "$out"
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
if [[ "$mode" == --also-isolate ]]; then
    run_variant isolated
    isolated_status=0
    evaluate "$evidence/isolated" || isolated_status=$?
    printf 'isolated_status=%s\n' "$isolated_status" > "$evidence/isolated/status.txt"
fi
exit "$default_status"
