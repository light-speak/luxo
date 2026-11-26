#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C GOTOOLCHAIN=local

base=36681c63627f4a3a4805bc6d19e908325614d3e9
head=27fddbeee9a994e1ea1330df9b53b36d501c8652
parent=d6690106e85d0608df32ba1a155acef76356dacf
root=$(cd "$GITHUB_WORKSPACE" && pwd -P)
work="$root/.tmp"
control="$work/control"
base_checkout="$work/base"
head_git="$work/head-git"
evidence="$work/pr143-strict-evidence"
helper="$control/scripts/diagnose-pr143-strict-native.py"
g() { git --no-replace-objects -C "$@"; }

cpu='AMD EPYC 9V45 96-Core Processor'
symbols='BenchmarkAuthMiddlewareIsolation|authMiddlewareBeforeIsolation|AuthMiddleware|extractBearerToken|auth\.VerifyCachedWithExpiry|luvia\.Identity'
test "$RUNNER_KIND" = github-hosted
test "$(uname -sm)" = 'Linux x86_64'
actual=$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')
test "$actual" = "$cpu"
test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
test "$(g "$control" rev-list --parents -n 1 HEAD)" = "$GITHUB_SHA $parent"
diff -u <(printf 'M\t%s\n' .github/workflows/diagnose-pr143-strict-layout.yml \
    scripts/diagnose-pr143-strict-native.py scripts/diagnose-pr143-strict-native.sh) \
    <(g "$control" diff --name-status "$parent" HEAD)
test "$(g "$base_checkout" rev-parse HEAD 'HEAD^{tree}')" = "$(printf '%s\n%s' "$base" d94bb8bc8d797a4cd2c0def3ff30fe59188dc772)"
test "$(g "$head_git" rev-parse HEAD 'HEAD^{tree}')" = "$(printf '%s\n%s' "$head" 648af87da509ec22c879161c27799c857ceadbdf)"
test "$(g "$head_git" cat-file commit "$head" | sed -n 's/^parent //p')" = \
    "$(printf '%s\n%s' 1b6c73bd67a781beb9e8b783cc7f8f631bf1276e "$base")"
g "$base_checkout" diff --quiet
g "$head_git" diff --quiet
for checkout in "$base_checkout" "$head_git"; do
    test -z "$(g "$checkout" ls-tree -r --name-only HEAD | awk '/(^|\/)\.gitattributes$/')"
done
printf 'A checkout=%s\nB archive=%s\nattributes files=none in either tree\n' "$base" "$head" > "$evidence/source-mode.txt"
build_inputs=(go.mod go.sum scripts/setup-modules.sh scripts/benchrun scripts/benchplan scripts/benchgate internal/benchlayout)
g "$base_checkout" ls-tree HEAD -- "${build_inputs[@]}" > "$evidence/base-tool-inputs.txt"
g "$head_git" ls-tree HEAD -- "${build_inputs[@]}" > "$evidence/head-tool-inputs.txt"
cmp "$evidence/base-tool-inputs.txt" "$evidence/head-tool-inputs.txt"
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; } > "$evidence/environment.txt"

for path in go.work go.work.sum .tmp; do
    [[ ! -e "$base_checkout/$path" && ! -L "$base_checkout/$path" ]]
done
scratch=$(mktemp -d "$work/pr143-strict-scratch.XXXXXXXX")
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
head_archive="$scratch/head"
tools="$scratch/bin"
mkdir -p "$input" "$head_archive/.tmp" "$tools"/{base,head,base64,head64} "$scratch/tmp" "$base_checkout/.tmp"
export TMPDIR="$scratch/tmp" GOTMPDIR="$scratch/tmp"
gh api repos/light-speak/luxo/actions/runs/45783705322/attempts/1 > "$input/original-run.json"
gh api "repos/light-speak/luxo/actions/artifacts/15101670856" > "$input/original-metadata.json"
gh api "repos/light-speak/luxo/actions/artifacts/15101670856/zip" > "$input/original.zip"
python3 -I -B "$helper" prepare 06 "$input" "$cpu"
cp "$input"/{original-run.json,original-metadata.json,original-gate.txt,reference-profile.txt,batch.tsv,expected.json,routing.json} "$evidence/"
gh api repos/light-speak/luxo/actions/runs/45804304446/attempts/2 > "$input/replay-run.json"
gh api repos/light-speak/luxo/actions/artifacts/15109146480 > "$input/replay-metadata.json"
gh api repos/light-speak/luxo/actions/artifacts/15109146480/zip > "$input/replay.zip"
cp "$input"/{replay-run.json,replay-metadata.json} "$evidence/"
python3 -I -B "$helper" reference "$input" "$evidence"
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
sha256sum "$tools/benchrun" "$tools/benchplan" "$tools/benchgate" "$tools/benchstat" > "$evidence/tools.sha256"
go version -m "$tools/benchstat" > "$evidence/benchstat-build.txt"

IFS=$'\t' read -r package profile filter < "$evidence/batch.tsv"
"$tools/benchplan" --verify-profile-batch confirmation "$evidence/expected.json" "$evidence/batch.tsv"
source_for() {
    case "$1" in
        base|base64) printf '%s\n' "$base_checkout" ;;
        head|head64) printf '%s\n' "$head_archive" ;;
        *) return 2 ;;
    esac
}
test "$(sha256sum "$base_checkout/pkg/lux/luvia/middleware_bench_test.go" | cut -d' ' -f1)" = a5a5cd5191827423e07bf382d572614207c3e7fb5b7df27452963c3136bb2d8e
cmp "$base_checkout/pkg/lux/luvia/middleware_bench_test.go" "$head_archive/pkg/lux/luvia/middleware_bench_test.go"
compile_arm() {
    local arm=$1 flags= command=compile-performance
    if [[ "$arm" == base* ]]; then command=compile-performance-baseline; fi
    if [[ "$arm" == *64 ]]; then flags=-ldflags=-funcalign=64; fi
    printf '%s\t%s\t%s\n' "$arm" "$command" "$flags" >> "$evidence/link-policy.tsv"
    GOFLAGS="$flags" "$tools/benchrun" "$command" "$(source_for "$arm")" "$tools/$arm" "$package" "$profile" \
        > "$evidence/$arm.compile.log" 2>&1
}
binary="${package//\//-}"
if [[ "$profile" != default ]]; then binary="$binary-$profile"; fi
binary="$binary.test"
record_binary() {
    local path=$1 output=$2
    python3 -I -B "$helper" elf "$path" > "$output.elf.json"
    sha256sum "$path" > "$output.sha256"
    go version -m "$path" > "$output.build.txt"
    go tool buildid "$path" > "$output.buildid.txt"
    readelf -WhlS "$path" > "$output.readelf.txt"
    go tool nm -size -sort address "$path" | awk '$3 ~ /^[Tt]$/' > "$scratch/text-addresses.txt"
    grep -E "$symbols" "$scratch/text-addresses.txt" > "$output.symbols.txt"
    go tool objdump -s "$symbols" "$path" > "$output.objdump.txt"
    python3 -I -B "$helper" symbols "$path" "$output.symbols.txt" "$output.elf.json" > "$output.symbols.json"
    python3 -I -B "$helper" dispatch "$path" "$output.objdump.txt" > "$output.dispatch.json"
}
for arm in base head base64 head64; do
    compile_arm "$arm"
    record_binary "$tools/$arm/$binary" "$evidence/$arm"
    "$tools/benchrun" run-performance "$(source_for "$arm")" "$tools/$arm" "$package" "$profile" \
        -test.run='^$' -test.bench='^BenchmarkAuthMiddlewareIsolation$' -test.benchmem \
        -test.benchtime=1x -test.count=1 -test.cpu=2 > "$evidence/$arm.inventory.raw"
    python3 -I -B "$helper" inventory "$evidence/$arm.inventory.raw"
done
python3 -I -B "$helper" verify-alignment "$evidence"
jq -s '{sameELF: (.[0].sha256 == .[1].sha256), sameTextBytes: (.[0].text.sha256 == .[1].text.sha256),
    sameTextRecord: (.[0].text == .[1].text),
    sameExecutableLoadSegments: (.[0].executableLoadSegments == .[1].executableLoadSegments)}' \
    "$evidence/base.elf.json" "$evidence/head.elf.json" > "$evidence/elf-comparison.json"

sample() {
    local arm=$1 dest=$2
    "$tools/benchrun" run-performance "$(source_for "$arm")" "$tools/$arm" "$package" "$profile" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=200ms -test.count=1 -test.cpu=2 >> "$dest"
}

compare() {
    "$tools/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        "base=$1" "head=$2" > "$3.csv" 2> "$3.warnings.txt"
}

sample_pair() {
    local comparison=$1 group=$2 round=$3 left=$4 right=$5 order=$6
    local prefix="$evidence/$comparison.$group"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$comparison" "$group" "$round" "$order" "$left" "$right" >> "$evidence/order.tsv"
    if [[ "$order" == left,right ]]; then
        sample "$left" "$prefix.left.raw"
        sample "$right" "$prefix.right.raw"
    else
        sample "$right" "$prefix.right.raw"
        sample "$left" "$prefix.left.raw"
    fi
}

comparisons=(aa:base:base ab:base:head bb:head:head aa64:base64:base64 ab64:base64:head64 bb64:head64:head64
    a-align:base:base64 b-align:head:head64)
printf 'comparison\tgroup\tpair\torder\tleft\tright\n' > "$evidence/order.tsv"
for group in 1 2; do
    for round in {1..10}; do
        order=left,right
        if (( (round + group) % 2 )); then order=right,left; fi
        for (( offset=0; offset < ${#comparisons[@]}; offset++ )); do
            index=$(( (round + (group - 1) * 10 + offset) % ${#comparisons[@]} ))
            IFS=: read -r comparison left right <<< "${comparisons[index]}"
            sample_pair "$comparison" "$group" "$round" "$left" "$right" "$order"
        done
    done
done

for group in 1 2; do
    for scope in "${comparisons[@]}"; do
        IFS=: read -r comparison left right <<< "$scope"
        prefix="$evidence/$comparison.$group"
        for side in left right; do
            "$tools/benchplan" --normalize "$prefix.$side.raw" "$prefix.$side.txt"
            test "$(sed -n 's/^cpu: //p' "$prefix.$side.txt" | sort -u)" = "$cpu"
        done
        "$tools/benchplan" --verify "$evidence/expected.json" "$prefix.left.txt" "$prefix.right.txt"
        compare "$prefix.left.txt" "$prefix.right.txt" "$prefix"
        if [[ "$left" == "$right" ]]; then compare "$prefix.right.txt" "$prefix.left.txt" "$prefix.reverse"; fi
    done
done

gate() {
    local name=$1 status=0
    shift
    "$tools/benchgate" "$@" > "$evidence/$name.gate.txt" 2>&1 || status=$?
    printf '%s\t%d\n' "$name" "$status" >> "$evidence/gates.tsv"
    return "$status"
}
unstable=0
for side in aa bb aa64 bb64; do
    for group in 1 2; do
        for direction in '' .reverse; do
            name="$side.$group$direction"
            gate "$name" "$evidence/$name.csv" || unstable=1
        done
    done
done
# Both sample groups contain the complete comparison inventory.
strict_compare() {
    local label=$1
    gate "$label-combined" --approved-costs "$head_git/scripts/benchgate/approved-costs.json" --base-sha "$base" \
        "$evidence/$label.1.csv" "$evidence/$label.2.csv"
}
strict_status=0
strict_compare ab || strict_status=$?
aligned_status=0
strict_compare ab64 || aligned_status=$?
is_performance_rejection() {
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$1.gate.txt"
}
for label in a-align b-align; do
    cross_status=0
    gate "$label-combined" "$evidence/$label.1.csv" "$evidence/$label.2.csv" || cross_status=$?
    if (( cross_status != 0 )) && ! is_performance_rejection "$label-combined"; then
        printf 'invalid-cross-comparison\n' > "$evidence/outcome.txt"
        exit 4
    fi
done
sha256sum -c "$evidence/"{base,head,base64,head64}.sha256
if (( unstable )); then printf 'unstable-control\n' > "$evidence/outcome.txt"; exit 4; fi
if (( strict_status == 0 )); then
    printf 'default-targets-not-reproduced\n' > "$evidence/outcome.txt"
    exit 4
fi
if ! is_performance_rejection ab-combined; then
    printf 'invalid-default-comparison\n' > "$evidence/outcome.txt"
    exit "$strict_status"
fi
if ! python3 -I -B "$helper" confirmed-targets "$evidence/ab-combined.gate.txt"; then
    printf 'default-confirmed-targets-changed\n' > "$evidence/outcome.txt"
    exit "$strict_status"
fi
if (( aligned_status )); then
    if is_performance_rejection ab64-combined; then
        printf 'default-strict-failed-aligned-comparison-failed\n' > "$evidence/outcome.txt"
    else
        printf 'invalid-aligned-comparison\n' > "$evidence/outcome.txt"
    fi
    exit "$aligned_status"
fi
printf 'default-strict-failed-alignment-cleared-targets\n' > "$evidence/outcome.txt"
