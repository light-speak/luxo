#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C GOTOOLCHAIN=local

base=36681c63627f4a3a4805bc6d19e908325614d3e9
head=27fddbeee9a994e1ea1330df9b53b36d501c8652
driver_parent=99926f9dac1ebcca449ada8f6c9d33e231bd6a70
root=$(cd "$GITHUB_WORKSPACE" && pwd -P)
work="$root/.tmp"
control="$work/control"
base_checkout="$work/base"
head_git="$work/head-git"
evidence="$work/pr143-strict-evidence"
helper="$control/scripts/diagnose-pr143-strict-native.py"
g() { git --no-replace-objects -C "$@"; }

case "$TARGET_SHARD" in
    06) original=15101670856; cpu='AMD EPYC 9V45 96-Core Processor';
        symbols='BenchmarkAuthMiddlewareIsolation|authMiddlewareBeforeIsolation|AuthMiddleware|extractBearerToken|auth\.VerifyCachedWithExpiry|luvia\.Identity' ;;
    07) original=15101276107; cpu='INTEL(R) XEON(R) PLATINUM 8573C';
        symbols='BenchmarkRPCDeadlineClassification|contextErrorAfterIO|contextErrorBeforeDeadlineFix|pendingDeadlineContext' ;;
    *) exit 2 ;;
esac
test "$RUNNER_KIND" = github-hosted
test "$(uname -sm)" = 'Linux x86_64'
actual=$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')
test "$actual" = "$cpu"
test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1'
test -z "$(go env GOFLAGS)"
test "$(g "$control" rev-list --parents -n 1 HEAD)" = "$GITHUB_SHA $driver_parent"
test "$(g "$control" rev-list --parents -n 1 "$driver_parent")" = "$driver_parent $base"
diff -u <(printf 'M\t%s\n' .github/workflows/diagnose-pr143-strict-layout.yml scripts/diagnose-pr143-strict-native.sh) \
    <(g "$control" diff --name-status "$driver_parent" HEAD)
diff -u <(printf 'A\t%s\n' .github/workflows/diagnose-pr143-strict-layout.yml \
    scripts/diagnose-pr143-strict-native.py scripts/diagnose-pr143-strict-native.sh) \
    <(g "$control" diff --name-status "$base" HEAD)
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
mkdir -p "$input" "$head_archive/.tmp" "$tools/base" "$tools/head" "$scratch/tmp" "$base_checkout/.tmp"
export TMPDIR="$scratch/tmp" GOTMPDIR="$scratch/tmp"
gh api repos/light-speak/luxo/actions/runs/45783705322/attempts/1 > "$input/original-run.json"
gh api "repos/light-speak/luxo/actions/artifacts/$original" > "$input/original-metadata.json"
gh api "repos/light-speak/luxo/actions/artifacts/$original/zip" > "$input/original.zip"
python3 -I -B "$helper" prepare "$TARGET_SHARD" "$input" "$cpu"
cp "$input"/{original-run.json,original-metadata.json,original-gate.txt,reference-profile.txt,batch.tsv,expected.json,routing.json} "$evidence/"
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
"$tools/benchrun" compile-performance-baseline "$base_checkout" "$tools/base" "$package" "$profile" > "$evidence/base.compile.log" 2>&1
"$tools/benchrun" compile-performance "$head_archive" "$tools/head" "$package" "$profile" > "$evidence/head.compile.log" 2>&1
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
    go tool nm -size -sort address "$path" | awk -v pattern="$symbols" '$3 ~ /^[Tt]$/ && $0 ~ pattern' > "$output.symbols.txt"
    go tool objdump -s "$symbols" "$path" > "$output.objdump.txt"
    python3 -I -B "$helper" symbols "$path" "$output.symbols.txt" "$output.elf.json" > "$output.symbols.json"
}
for arm in base head; do record_binary "$tools/$arm/$binary" "$evidence/$arm"; done
jq -s '{sameELF: (.[0].sha256 == .[1].sha256), sameTextBytes: (.[0].text.sha256 == .[1].text.sha256),
    sameTextRecord: (.[0].text == .[1].text),
    sameExecutableLoadSegments: (.[0].executableLoadSegments == .[1].executableLoadSegments)}' \
    "$evidence/base.elf.json" "$evidence/head.elf.json" > "$evidence/elf-comparison.json"

sample() {
    local arm=$1 dest=$2 checkout="$head_archive"
    if [[ "$arm" == base ]]; then checkout="$base_checkout"; fi
    "$tools/benchrun" run-performance "$checkout" "$tools/$arm" "$package" "$profile" \
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

printf 'comparison\tgroup\tpair\torder\tleft\tright\n' > "$evidence/order.tsv"
for group in 1 2; do
    for round in {1..10}; do
        if (( (round + group) % 2 == 0 )); then
            sample_pair aa "$group" "$round" base base left,right
            sample_pair ab "$group" "$round" base head left,right
            sample_pair bb "$group" "$round" head head left,right
        else
            sample_pair bb "$group" "$round" head head right,left
            sample_pair ab "$group" "$round" base head right,left
            sample_pair aa "$group" "$round" base base right,left
        fi
    done
done

for group in 1 2; do
    for comparison in aa ab bb; do
        prefix="$evidence/$comparison.$group"
        for side in left right; do
            "$tools/benchplan" --normalize "$prefix.$side.raw" "$prefix.$side.txt"
            test "$(sed -n 's/^cpu: //p' "$prefix.$side.txt" | sort -u)" = "$cpu"
        done
        "$tools/benchplan" --verify "$evidence/expected.json" "$prefix.left.txt" "$prefix.right.txt"
        compare "$prefix.left.txt" "$prefix.right.txt" "$prefix"
        if [[ "$comparison" != ab ]]; then compare "$prefix.right.txt" "$prefix.left.txt" "$prefix.reverse"; fi
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
for side in aa bb; do
    for group in 1 2; do
        for direction in '' .reverse; do
            name="$side.$group$direction"
            gate "$name" "$evidence/$name.csv" || unstable=1
        done
    done
done
status=0
gate ab-combined --approved-costs "$head_git/scripts/benchgate/approved-costs.json" --base-sha "$base" \
    "$evidence/ab.1.csv" "$evidence/ab.2.csv" || status=$?
sha256sum -c "$evidence/base.sha256" "$evidence/head.sha256"
if (( unstable )); then printf 'unstable-control\n' > "$evidence/outcome.txt"; exit 4; fi
if (( status )); then printf 'strict-comparison-failed\n' > "$evidence/outcome.txt"; exit "$status"; fi
printf 'strict-targets-clear\n' > "$evidence/outcome.txt"
