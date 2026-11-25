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
            left=A
            right=B
            order=LR
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
base=feddadefe3ee05611c075cfa0319a851865584b5
base_tree=f30e4c89335c8b14d4269e0df1a5d51e7d471506
published=5387d161dc3950af5f72e4c0369b972a65c602db
published_tree=7d95c9e6835ac33651608eee8227484bf08bfb3b
candidate_tree=cc36733d53baede2ab56ad27e16cedb0a092ef13
cpu='Intel(R) Xeon(R) 6975P-C'
package=pkg/lux/api
profile=api-non-identity
filter='^BenchmarkAuditBufferedBody$'
fixture="$root/scripts/diagnose-pr86-buffered-body/fixture.go.txt"
paths=("$package/buffered_body.go" "$package/buffered_body_test.go")
a_blobs=(905ff2eb9d9f32abd61cd09f916ffe0098d42129 5f731e3aa188d6643aa5409c3f4096c229062f9a)
b_blobs=(41e09504f6f95ae22d361e991b768b2bef6324eb a78974a1f3d39901da8d50980202e1cf1b69bb0b)

actual_cpu=$(lscpu | sed -n '/^Model name:/ { s/^Model name:[[:blank:]]*//; s/[[:blank:]]*$//; p; }')
if [[ "$actual_cpu" != "$cpu" || "$(cat "$evidence/selection.txt")" != "$cpu" ]]; then
    printf 'wrong-cpu: %s\n' "$actual_cpu" >&2
    exit 3
fi
test "$(go env GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nv1\n1'
test "$GOMAXPROCS" = 2
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
test "$(git rev-parse "$base^{tree}")" = "$base_tree"
test "$(git rev-parse "$published^{tree}")" = "$published_tree"
git diff --exit-code "$base" HEAD -- go.mod go.sum scripts/{benchrun,benchplan,benchgate} internal/benchlayout
printf '%s  %s\n' ae4e78e5e60d98d946dbd296b6602fde7a6401ab7d32c4f1147a0eb3d9067634 "$fixture" \
    | sha256sum -c - > "$evidence/fixture-binding.txt"

mkdir -p .tmp
work=$(mktemp -d "$root/.tmp/pr86-buffered-body-work.XXXXXXXX")
cleanup() {
    [[ "$work" == "$root/.tmp/pr86-buffered-body-work."* && -d "$work" && ! -L "$work" ]] || return 99
    rm -rf -- "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

candidate_index="$work/candidate.index"
GIT_INDEX_FILE="$candidate_index" git read-tree "$base"
for i in "${!paths[@]}"; do
    path=${paths[$i]}
    test "$(git rev-parse "$base:$path")" = "${a_blobs[$i]}"
    test "$(git rev-parse "$published:$path")" = "${b_blobs[$i]}"
    GIT_INDEX_FILE="$candidate_index" git update-index --cacheinfo "100644,${b_blobs[$i]},$path"
    printf '%s\t%s\t%s\n' "$path" "${a_blobs[$i]}" "${b_blobs[$i]}"
done > "$evidence/product-blobs.tsv"
test "$(GIT_INDEX_FILE="$candidate_index" git write-tree)" = "$candidate_tree"
diff -u <(printf '%s\n' "${paths[@]}") <(git diff --name-only "$base_tree" "$candidate_tree")
printf 'base=%s\nbase_tree=%s\nsource_pr_head=%s\nsource_pr_tree=%s\ncandidate_tree=%s\n' \
    "$base" "$base_tree" "$published" "$published_tree" "$candidate_tree" > "$evidence/revisions.txt"
git diff --stat "$base_tree" "$candidate_tree" > "$evidence/product-diff.txt"
go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS > "$evidence/go-env.json"

mkdir "$work/tools" "$work/test-tmp"
export TMPDIR="$work/test-tmp" GOWORK=off GOBIN="$work/tools"
{
    for tool in benchrun benchplan benchgate; do go build -o "$work/tools/$tool" "./scripts/$tool"; done
    go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
} > "$evidence/tools.log" 2>&1
benchrun="$work/tools/benchrun"
benchplan="$work/tools/benchplan"
benchgate="$work/tools/benchgate"
benchstat="$work/tools/benchstat"
go version -m "$benchstat" > "$evidence/benchstat-version.txt"
"$benchrun" performance-profile > "$evidence/profile.txt"
printf '%s\n' BenchmarkAuditBufferedBody/{borrow-4096,close-read,read-32,read-4096}-2 \
    | sort > "$evidence/expected-names.txt"
jq -Rn --arg pkg "$package" \
    '[inputs | {Package:$pkg, Name:.}] | {Base:., Head:.}' < "$evidence/expected-names.txt" > "$evidence/expected.json"
awk -v pkg="$package" '{print pkg "\t" $0}' "$evidence/expected-names.txt" > "$evidence/targets.tsv"
"$benchplan" --confirmation-plan "$evidence/expected.json" "$evidence/targets.tsv" \
    "$evidence/confirmation-batch.tsv" "$evidence/confirmation-expected.json"
"$benchplan" --verify-profile-batch confirmation "$evidence/confirmation-expected.json" "$evidence/confirmation-batch.tsv"
test "$(wc -l < "$evidence/confirmation-batch.tsv")" -eq 1
IFS=$'\t' read -r parsed_package parsed_profile _ < "$evidence/confirmation-batch.tsv"
test "$parsed_package" = "$package"
test "$parsed_profile" = "$profile"

build_arm() {
    local arm=$1 tree=$2 command=$3
    local source="$work/$arm/source" binaries="$work/$arm/bin" binary
    mkdir -p "$source/.tmp" "$binaries"
    git archive "$tree" | tar -xf - -C "$source"
    test ! -e "$source/$package/diagnostic_buffered_body_bench_test.go"
    cp "$fixture" "$source/$package/diagnostic_buffered_body_bench_test.go"
    sh "$source/scripts/setup-modules.sh" "$source" > "$evidence/$arm.modules.log" 2>&1
    "$benchrun" "$command" "$source" "$binaries" "$package" "$profile" > "$evidence/$arm.compile.log" 2>&1
    binary="$binaries/pkg-lux-api-$profile.test"
    sha256sum "$binary" > "$evidence/$arm.elf.sha256"
    go version -m "$binary" > "$evidence/$arm.build.txt"
    go tool buildid "$binary" > "$evidence/$arm.buildid.txt"
    local hot='(BenchmarkAuditBufferedBody|[.]\(\*BufferedBody\)[.](Read|Close|borrow)$)'
    go tool nm -size "$binary" | grep -E "$hot" > "$evidence/$arm.hot.nm.txt"
    go tool objdump -s "$hot" "$binary" > "$evidence/$arm.hot.objdump.txt"
}

sample() {
    local arm=$1 duration=$2 output=$3 raw="$work/sample.txt"
    "$benchrun" run-performance "$work/$arm/source" "$work/$arm/bin" "$package" "$profile" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime="$duration" \
        -test.count=1 -test.cpu=2 -test.timeout=90s > "$raw"
    test "$(sed -n '/^cpu: / { s/[[:blank:]]*$//; p; }' "$raw")" = "cpu: $cpu"
    test "$(grep -Fxc PASS "$raw")" -eq 1
    diff -u "$evidence/expected-names.txt" <(awk '/^Benchmark/ {print $1}' "$raw" | sort)
    if [[ "$duration" == 1x ]]; then
        diff -u "$evidence/expected-names.txt" <(awk '/^Benchmark/ && $2 == 1 {print $1}' "$raw" | sort)
        grep -Fx 'goos: linux' "$raw" > /dev/null
        grep -Fx 'goarch: amd64' "$raw" > /dev/null
    fi
    cat "$raw" >> "$output"
}

statistics() {
    local target=$1 left=$2 right=$3
    "$benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        "base=$left" "head=$right" > "$target.csv" 2> "$target.warnings.txt"
}

build_arm A "$base_tree" compile-performance-baseline
build_arm B "$candidate_tree" compile-performance
sample A 1x "$evidence/A.fixture-1x.txt"
sample B 1x "$evidence/B.fixture-1x.txt"
"$benchplan" --verify-equal-inventories "$evidence/A.fixture-1x.txt" "$evidence/B.fixture-1x.txt"

printf 'pair\tgroup\tleft\tright\torder\tUTC\n' > "$evidence/order.tsv"
while IFS=$'\t' read -r round group left right order; do
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$round" "$group" "$left" "$right" "$order" "$(date -u +%FT%TZ)" >> "$evidence/order.tsv"
    if [[ "$order" == LR ]]; then
        sample "$left" 200ms "$evidence/$group.left.raw.txt"
        sample "$right" 200ms "$evidence/$group.right.raw.txt"
    else
        sample "$right" 200ms "$evidence/$group.right.raw.txt"
        sample "$left" 200ms "$evidence/$group.left.raw.txt"
    fi
done < <(write_schedule | tail -n +2)

for group in AA BB AB1 AB2; do
    for side in left right; do
        "$benchplan" --normalize "$evidence/$group.$side.raw.txt" "$evidence/$group.$side.txt"
    done
    "$benchplan" --verify "$evidence/expected.json" "$evidence/$group.left.txt" "$evidence/$group.right.txt"
    statistics "$evidence/$group" "$evidence/$group.left.txt" "$evidence/$group.right.txt"
done

control_failed=false
for group in AA BB; do
    if ! "$benchgate" "$evidence/$group.csv" > "$evidence/$group.gate.txt" 2>&1; then control_failed=true; fi
    statistics "$evidence/$group.reverse" "$evidence/$group.right.txt" "$evidence/$group.left.txt"
    if ! "$benchgate" "$evidence/$group.reverse.csv" > "$evidence/$group.reverse.gate.txt" 2>&1; then control_failed=true; fi
done
comparison=0
"$benchgate" --approved-costs "$root/scripts/benchgate/approved-costs.json" --base-sha "$base" \
    "$evidence/AB1.csv" "$evidence/AB2.csv" > "$evidence/AB.gate.txt" 2>&1 || comparison=$?
printf 'control_failed=%s\ncomparison_exit=%s\n' "$control_failed" "$comparison" > "$evidence/verdict.txt"
cat "$evidence/AB.gate.txt"
[[ "$control_failed" == false ]] || exit 4
exit "$comparison"
