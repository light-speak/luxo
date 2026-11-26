#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

base=e9159f125064eccd6499ff5f9426f9c020f05dcc
head=79ca343d68b265be411d07166702497eba1cd008
root=$(pwd -P)
work="$root/.tmp"
evidence="$work/pr144-native-evidence"
helper="$work/control/scripts/diagnose-pr144-native.py"
tools="$work/bin"
mkdir -p "$evidence"
test "$root" = "$(cd "$GITHUB_WORKSPACE" && pwd -P)"
test "$RUNNER_KIND" = github-hosted
test "$(uname -sm)" = 'Linux x86_64'
test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
test "$(git rev-parse HEAD)" = "$head"
test "$(git rev-parse 'HEAD^{tree}')" = 358f53b09104da9eb88a5ae67860c949ae449c7f
test "$(git -C "$work/base" rev-parse HEAD)" = "$base"
test "$(git -C "$work/base" rev-parse 'HEAD^{tree}')" = a721621c0c30b6c57f4b404fb358295ec065293b
test "$(git -C "$work/control" rev-list --parents -n 1 HEAD)" = "$GITHUB_SHA $head"
diff -u <(printf 'A\t%s\n' .github/workflows/diagnose-pr144-amd-evidence.yml \
    scripts/diagnose-pr144-native.py scripts/diagnose-pr144-native.sh) \
    <(git -C "$work/control" diff --name-status HEAD^ HEAD)
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; } > "$evidence/environment.txt"
cpu=$(sed -n 's/^Model name:[[:space:]]*//p' "$evidence/environment.txt")
test -n "$cpu"
git ls-tree HEAD -- pkg cmd go.mod go.sum > "$evidence/head-product.txt"
git -C "$work/base" ls-tree HEAD -- pkg cmd go.mod go.sum > "$evidence/base-product.txt"
cmp "$evidence/base-product.txt" "$evidence/head-product.txt"
printf 'workflow=%s\nbase=%s\nhead=%s\nmode=targeted ten alternating pairs in each group\n' \
    "$GITHUB_SHA" "$base" "$head" > "$evidence/revisions.txt"

fetch_original() {
    local shard=$1 artifact=$2
    local path="$evidence/$shard"
    mkdir "$path"
    gh api "repos/light-speak/luxo/actions/artifacts/$artifact" > "$path/original-metadata.json"
    gh api "repos/light-speak/luxo/actions/artifacts/$artifact/zip" > "$path/original.zip"
    python3 -I -B "$helper" prepare "$shard" "$path" "$cpu"
}
fetch_original 09 15095340498
fetch_original 10 15094702482

for directory in "$work/head" "$tools" "$work/test-tmp"; do
    test ! -e "$directory" && test ! -L "$directory"
done
cleanup() {
    local status=$?
    trap - EXIT
    rm -rf -- "$work/head" "$tools" "$work/test-tmp" || status=1
    printf 'exit=%s\n' "$status" > "$evidence/cleanup.txt"
    exit "$status"
}
trap cleanup EXIT
mkdir -p "$work/head" "$tools/base" "$tools/head" "$work/test-tmp"
git archive "$head" | tar -x -C "$work/head"
mkdir -p "$work/base/.tmp" "$work/head/.tmp"
sh scripts/setup-modules.sh .tmp/base
sh scripts/setup-modules.sh .tmp/head
GOWORK=off GOBIN="$tools" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
GOWORK=off go build -o "$tools/" ./scripts/benchrun ./scripts/benchplan ./scripts/benchgate
"$tools/benchrun" performance-profile > "$evidence/rebuilt-profile.txt"
cmp "$evidence/09/reference-profile.txt" "$evidence/rebuilt-profile.txt"
cmp "$evidence/10/reference-profile.txt" "$evidence/rebuilt-profile.txt"
sha256sum "$tools/benchrun" "$tools/benchplan" "$tools/benchgate" "$tools/benchstat" > "$evidence/tools.sha256"
go version -m "$tools/benchstat" > "$evidence/benchstat-build.txt"

record_binary() {
    local binary=$1 output=$2
    python3 -I -B "$helper" elf "$binary" > "$output.elf.json"
    sha256sum "$binary" > "$output.sha256"
    go version -m "$binary" > "$output.build.txt"
    go tool buildid "$binary" > "$output.buildid.txt"
    readelf -WhlS "$binary" > "$output.readelf.txt"
}

sample() {
    local arm=$1 output=$2
    TMPDIR="$work/test-tmp" "$tools/benchrun" run-performance "$work/$arm" "$tools/$arm" "$package" "$profile" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=200ms -test.count=1 -test.cpu=2 >> "$output"
}

run_group() {
    local group=$1 right=head left_out right_out i
    if [[ "$group" == control ]]; then right=base; fi
    left_out="$path/$group.raw-base.txt"
    right_out="$path/$group.raw-head.txt"
    : > "$left_out"
    : > "$right_out"
    for i in {1..10}; do
        if (( i % 2 == 1 )); then
            printf '%s\t%d\tleft,right\tbase\t%s\n' "$group" "$i" "$right" >> "$path/order.tsv"
            sample base "$left_out"
            sample "$right" "$right_out"
        else
            printf '%s\t%d\tright,left\tbase\t%s\n' "$group" "$i" "$right" >> "$path/order.tsv"
            sample "$right" "$right_out"
            sample base "$left_out"
        fi
    done
    "$tools/benchplan" --normalize "$left_out" "$path/$group.base.txt"
    "$tools/benchplan" --normalize "$right_out" "$path/$group.head.txt"
    "$tools/benchplan" --verify "$path/expected.json" "$path/$group.base.txt" "$path/$group.head.txt"
    "$tools/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        "base=$path/$group.base.txt" "head=$path/$group.head.txt" > "$path/$group.csv" 2> "$path/$group.warnings.txt"
}

overall=0
for shard in 09 10; do
    path="$evidence/$shard"
    IFS=$'\t' read -r package profile filter < "$path/batch.tsv"
    "$tools/benchplan" --verify-profile-batch confirmation "$path/expected.json" "$path/batch.tsv"
    "$tools/benchrun" compile-performance-baseline .tmp/base .tmp/bin/base "$package" "$profile" > "$path/base.compile.log" 2>&1
    "$tools/benchrun" compile-performance .tmp/head .tmp/bin/head "$package" "$profile" > "$path/head.compile.log" 2>&1
    binary="${package//\//-}"
    if [[ "$profile" != default ]]; then binary="$binary-$profile"; fi
    for arm in base head; do record_binary "$tools/$arm/$binary.test" "$path/$arm"; done
    jq -s '{sameELF: (.[0].sha256 == .[1].sha256), sameTextBytes: (.[0].text.sha256 == .[1].text.sha256),
        sameTextRecord: (.[0].text == .[1].text),
        sameExecutableLoadSegments: (.[0].executableLoadSegments == .[1].executableLoadSegments)}' \
        "$path/base.elf.json" "$path/head.elf.json" > "$path/elf-comparison.json"
    if ! cmp -s "$path/base.elf.json" "$path/head.elf.json"; then
        symbols='BenchmarkInternalRouteExport|ExportHandlers'
        if [[ "$shard" == 10 ]]; then symbols='BenchmarkSoftDeleteIdentifiers|validateFieldIdentifiers'; fi
        for arm in base head; do
            go tool nm -size -sort address "$tools/$arm/$binary.test" |
                awk -v pattern="$symbols" '$0 ~ pattern' > "$path/$arm.symbols.txt"
            go tool objdump -s "$symbols" "$tools/$arm/$binary.test" > "$path/$arm.objdump.txt"
        done
        diff -u "$path/base.readelf.txt" "$path/head.readelf.txt" > "$path/elf-layout.diff" || test "$?" = 1
    fi

    printf 'group\tpair\torder\tleft_subject\tright_subject\n' > "$path/order.tsv"
    run_group primary
    run_group confirmation
    run_group control
    "$tools/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        "base=$path/control.head.txt" "head=$path/control.base.txt" > "$path/control-reversed.csv" 2> "$path/control-reversed.warnings.txt"
    for label in control control-reversed; do
        status=0
        "$tools/benchgate" "$path/$label.csv" > "$path/$label.gate.txt" 2>&1 || status=$?
        printf '%s\t%d\n' "$label" "$status" >> "$path/gates.tsv"
        if (( status != 0 )); then overall=1; fi
    done
    status=0
    "$tools/benchgate" --approved-costs scripts/benchgate/approved-costs.json --base-sha "$base" \
        "$path/primary.csv" "$path/confirmation.csv" > "$path/ab.gate.txt" 2>&1 || status=$?
    printf 'ab\t%d\n' "$status" >> "$path/gates.tsv"
    if (( status != 0 )); then overall=1; fi
    sha256sum -c "$path/base.sha256" "$path/head.sha256"
done
exit "$overall"
