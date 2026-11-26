#!/usr/bin/env bash
set -euo pipefail
umask 077
export GOTOOLCHAIN=local

cd "${GITHUB_WORKSPACE:?}"
repo=$(pwd -P)
base=59a4167f129f0bff0e8425125fa02eae87fd347b
head=7f71dca0b5c99a07f11c3307131d1f634b8abae2
witness_parent=099afa428cd0fa877722d378017359466e8aafdf
out="$repo/.tmp/pr157-json-list"
input="$repo/scripts/diagnose-pr157-json-list"
base_src="$repo/.tmp/base"
head_src="$repo/.tmp/head"

test "${RUNNER_KIND:?}" = github-hosted
cpu=$(cat "$out/cpu.txt")
test -n "$cpu"
grep -Fxq 'VERSION_ID="24.04"' /etc/os-release
test "$(getconf _NPROCESSORS_ONLN)" = 4
test "$(git rev-parse HEAD)" = "${GITHUB_SHA:?}"
test "$(git show -s --format=%P HEAD)" = "$witness_parent"
test "$(git show -s --format=%P "$witness_parent")" = "$head"
test "$(git show -s --format=%P "$head")" = "$base"
test "$(git -C "$base_src" rev-parse HEAD)" = "$base"
test "$(git rev-parse "$base^{tree}")" = c96b8db43227f6ed447d10b0b32deb39284c7f28
test "$(git rev-parse "$head^{tree}")" = ed7f8556581ba2455a3f47e1c07a9b8b1683b63a
test -z "$(git -C "$base_src" status --porcelain --untracked-files=no)"
git diff --exit-code "$base" "$head" -- pkg/lux go.mod go.sum scripts/setup-modules.sh
test "$(git rev-parse "$base:pkg/lux")" = 66f0d476c25b7cca61432d0ffe3bbdff21cdc2aa
test "$(git rev-parse "$head:pkg/lux")" = 66f0d476c25b7cca61432d0ffe3bbdff21cdc2aa
test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1'
(
    cd "$input"
    printf '%s\n'         'c475e91509f724c67b28942360d3f27b704c22130576b04279ea0c724a634a34  expected.json'         '161e0109204fbe973a67544db8d3ab9cc40964dc819fb767a0165eea0f3fe9ab  batch.tsv' |
        sha256sum --check
)
printf 'base=%s\nhead=%s\nrun=45859078382\njob=140046710247\nartifact=15139056901\nartifactSHA256=079bde7a1e5beabfdcb935315f834c813c25f18cad08035616054e5339711276\n' "$base" "$head" > "$out/binding.txt"
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED; } > "$out/environment.txt"
git diff --name-status "$base" "$head" > "$out/compared-paths.txt"

work=$(mktemp -d "$repo/.tmp/pr157-json-list-work.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
mkdir "$head_src"
trap 'rm -rf -- "$work" "$head_src"' EXIT
git archive "$head" | tar -x -C "$head_src"
mkdir -p "$work/bin" "$work/base" "$work/head" "$work/wrapper" "$work/test-tmp" "$base_src/.tmp" "$head_src/.tmp"
sh scripts/setup-modules.sh "$base_src"
sh scripts/setup-modules.sh "$head_src"
cmp "$base_src/go.work" "$head_src/go.work"
export TMPDIR="$work/test-tmp"
export LUXO_DIAG_REAL_GO
LUXO_DIAG_REAL_GO=$(command -v go)
for tool in benchrun benchplan benchgate; do
    GOWORK=off go build -o "$work/bin/$tool" "./scripts/$tool"
done
GOWORK=off GOBIN="$work/bin" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
"$work/bin/benchrun" performance-profile > "$out/benchmark-compile-profile.txt"
printf '%s  %s\n' '2def07c8d8706eaa0de3bb81da4635c4b4264ad67fc98e2fcdf40a06a63d9e18' "$out/benchmark-compile-profile.txt" | sha256sum --check
"$work/bin/benchplan" --verify-profile-batch confirmation "$input/expected.json" "$input/batch.tsv"

cat > "$work/wrapper/go" <<'PY'
#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
overlays = [flag.removeprefix("-overlay=") for flag in args if flag.startswith("-overlay=")]
assert len(overlays) == 1, args
overlay = json.loads(Path(overlays[0]).read_text())["Replace"]
reported = {str(Path(path).relative_to(Path.cwd())): value for path, value in overlay.items()}
expected = {f"pkg/lux/api/{fixture}": "" for fixture in (
    "identity_reader_bench_test.go", "identity_reader_fuzz_test.go",
    "identity_reader_test.go", "identity_value_test.go",
)}
assert reported == expected
Path(os.environ["LUXO_DIAG_OVERLAY"]).write_text(json.dumps(reported, sort_keys=True) + "\n")
go = os.environ["LUXO_DIAG_REAL_GO"]
os.execv(go, [go, *args])
PY
chmod +x "$work/wrapper/go"
IFS=$'\t' read -r package profile filter < "$input/batch.tsv"
test "$package/$profile" = pkg/lux/api/api-non-identity
for side in base head; do
    command=compile-performance
    if [[ "$side" == base ]]; then command=compile-performance-baseline; fi
    PATH="$work/wrapper:$PATH" LUXO_DIAG_OVERLAY="$out/$side.overlay.json" \
        "$work/bin/benchrun" "$command" "$repo/.tmp/$side" "$work/$side" "$package" "$profile"
    binary="$work/$side/pkg-lux-api-api-non-identity.test"
    sha256sum "$binary" > "$out/$side.sha256"
    go tool buildid "$binary" > "$out/$side.buildid"
    go version -m "$binary" > "$out/$side.buildinfo"
    objcopy --dump-section ".text=$work/$side.text" "$binary" "$work/$side.elf-copy"
    sha256sum "$work/$side.text" > "$out/$side.text.sha256"
    go tool objdump -s 'BenchmarkJSONListMetadata|benchmarkListMetadata|parseListParams|ParseRequest|parseRequestLimitBudget|parseRawRequest|parseListObjects' "$binary" > "$out/$side.hot.txt"
    test -s "$out/$side.hot.txt"
done
cmp "$out/base.overlay.json" "$out/head.overlay.json"
if cmp -s "$work/base/pkg-lux-api-api-non-identity.test" "$work/head/pkg-lux-api-api-non-identity.test"; then
    echo 'ELF identity: byte-identical' > "$out/elf-identity.txt"
elif cmp -s "$work/base.text" "$work/head.text"; then
    echo 'ELF identity: different file, byte-identical .text' > "$out/elf-identity.txt"
else
    echo 'ELF identity: different .text' > "$out/elf-identity.txt"
fi
if [[ "$cpu" != 'AMD EPYC 9V45 96-Core Processor' ]]; then
    printf 'verification=elf-only\nbenchmark_processes=0\n' > "$out/status.txt"
    exit 0
fi

sample() {
    local side=$1 file=$2
    processes=$((processes + 1))
    printf '%s\t%s\t%s\t%s\t%s\n' "$processes" "$block" "$pair" "$arms" "$side" >> "$out/order.tsv"
    "$work/bin/benchrun" run-performance "$repo/.tmp/$side" "$work/$side" "$package" "$profile" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=200ms -test.count=1 -test.cpu=2 >> "$file"
}

failed=0
processes=0
for block in 1 2; do
    for pair in {1..10}; do
        for arms in AA HH AH; do
            left=base; right=head
            case "$arms" in AA) right=base ;; HH) left=head ;; esac
            path="$out/$arms-$block"
            if (( (block + pair) % 2 == 0 )); then
                sample "$left" "$path.left.raw"; sample "$right" "$path.right.raw"
            else
                sample "$right" "$path.right.raw"; sample "$left" "$path.left.raw"
            fi
        done
    done
    for arms in AA HH AH; do
        path="$out/$arms-$block"
        for side in left right; do
            "$work/bin/benchplan" --normalize "$path.$side.raw" "$path.$side.txt"
        done
        "$work/bin/benchplan" --verify "$input/expected.json" "$path.left.txt" "$path.right.txt"
        "$work/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
            base="$path.left.txt" head="$path.right.txt" > "$path.csv" 2> "$path.warnings"
        if [[ "$arms" != AH ]]; then
            "$work/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
                base="$path.right.txt" head="$path.left.txt" > "$path.reverse.csv" 2> "$path.reverse.warnings"
            for direction in "$path" "$path.reverse"; do
                if ! "$work/bin/benchgate" "$direction.csv" 2>&1 | tee "$direction.gate.txt"; then
                    failed=1
                fi
            done
        fi
    done
done
if ! "$work/bin/benchgate" --approved-costs scripts/benchgate/approved-costs.json --base-sha "$base" \
    "$out/AH-1.csv" "$out/AH-2.csv" 2>&1 | tee "$out/candidate.gate.txt"; then
    failed=1
fi
test "$processes" = 120
printf 'strict_exit=%s\nbenchmark_processes=%s\n' "$failed" "$processes" > "$out/status.txt"
exit "$failed"
