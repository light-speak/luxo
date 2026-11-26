#!/usr/bin/env bash
set -euo pipefail
umask 077
export GOTOOLCHAIN=local

cd "${GITHUB_WORKSPACE:?}"
repo=$(pwd -P)
base=59a4167f129f0bff0e8425125fa02eae87fd347b
head=0f3e233220962fbdc96a6ecc4f55750074f03861
previous=f5f0aaf83bc2637e91e1dd61dfe134399adaf2c0
out="$repo/.tmp/pr156-7763"
input="$repo/scripts/diagnose-pr156-7763"
base_src="$repo/.tmp/base"
head_src="$repo/.tmp/head"

test "${RUNNER_KIND:?}" = github-hosted
test -s "$out/cpu.txt"
grep -Fxq 'VERSION_ID="24.04"' /etc/os-release
test "$(getconf _NPROCESSORS_ONLN)" = 4
test "$(git rev-parse HEAD)" = "${GITHUB_SHA:?}"
test "$(git show -s --format=%P HEAD)" = "$head"
test "$(git show -s --format=%P "$head")" = "$previous"
test "$(git show -s --format=%P "$previous")" = "$base"
test "$(git -C "$base_src" rev-parse HEAD)" = "$base"
test "$(git rev-parse "$base^{tree}")" = c96b8db43227f6ed447d10b0b32deb39284c7f28
test "$(git rev-parse "$head^{tree}")" = 5d9fa584a66f7b5a4f284a1aaf7e19a1f550b520
git diff --exit-code "$base" "$head" -- scripts/benchrun scripts/setup-modules.sh go.mod go.sum \
    pkg/semantic pkg/codegen/dataloader.go pkg/codegen/dataloader_nullable_bench_test.go
test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED)" = $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1'
(
    cd "$input"
    sha256sum --check <<'EOF'
6a455609704a6b22c556c78405c53b7bdd3a5ba95968aa432a0e38541b88fce6  batch.tsv
afab7e99308611b58d1e5043de785f55aba1eb5fd175a0b5804b2a1f43038dee  expected.json
c5f6345a3e761ef28b70f0043e144968f34826e4eaa15be5dcac7aea5164898d  origin.txt
EOF
)
printf 'base=%s\nhead=%s\n' "$base" "$head" > "$out/binding.txt"
cat "$input/origin.txt" >> "$out/binding.txt"
cp "$input/"{batch.tsv,expected.json} "$out/"
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED; } > "$out/environment.txt"
git diff --name-status "$base" "$head" > "$out/compared-paths.txt"

work=$(mktemp -d "$repo/.tmp/pr156-7763-work.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
mkdir "$head_src"
trap 'rm -rf -- "$work" "$head_src"' EXIT
git archive "$head" | tar -xf - -C "$head_src"
mkdir -p "$work/"{bin,base,head,wrapper,test-tmp} "$base_src/.tmp" "$head_src/.tmp"
sh scripts/setup-modules.sh "$base_src"
sh scripts/setup-modules.sh "$head_src"
cmp "$base_src/go.work" "$head_src/go.work"
export TMPDIR="$work/test-tmp"
export LUXO_DIAG_REAL_GO
LUXO_DIAG_REAL_GO=$(command -v go)
for tool in benchrun benchplan benchgate; do
    GOWORK=off go build -o "$work/bin/$tool" "./scripts/$tool"
done
"$work/bin/benchrun" performance-profile > "$out/compile-profile.txt"
printf '%s  %s\n' ca6d61fffd64abebbfceb442bb80e0e4247d7ddbbd51a33ee2b35fe2c0ce8126 "$out/compile-profile.txt" | sha256sum --check
"$work/bin/benchplan" --verify-profile-batch confirmation "$input/expected.json" "$input/batch.tsv"

cat > "$work/wrapper/go" <<'PY'
#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
package = os.environ["LUXO_DIAG_PACKAGE"]
assert args[:3] == ["test", "-trimpath", "-c"] and "./" + package in args
overlays = [flag.removeprefix("-overlay=") for flag in args if flag.startswith("-overlay=")]
assert package in {"pkg/codegen", "pkg/semantic"} and not overlays
Path(os.environ["LUXO_DIAG_OVERLAY"]).write_text(json.dumps({}) + "\n")
go = os.environ["LUXO_DIAG_REAL_GO"]
os.execv(go, [go, *args])
PY
chmod +x "$work/wrapper/go"

while IFS=$'\t' read -r package profile _; do
    name=${package##*/}
    case "$name" in
        codegen) symbols='BenchmarkRelationLoaderGeneration|collectDataLoaderInputs|analyzeRelations|relationRemoteKeyNullable|ImplicitRelationKeys' ;;
        semantic) symbols='BenchmarkCollectEventEmits|collectReachableEmits|eventEmitCollector|/ast\.Walk' ;;
        *) exit 1 ;;
    esac
    for side in base head; do
        command=compile-performance
        if [[ "$side" == base ]]; then command=compile-performance-baseline; fi
        key="$side-$name"
        PATH="$work/wrapper:$PATH" LUXO_DIAG_PACKAGE="$package" LUXO_DIAG_OVERLAY="$out/$key.overlay.json" \
            "$work/bin/benchrun" "$command" "$repo/.tmp/$side" "$work/$side" "$package" "$profile" \
            >> "$out/compiler.log" 2>&1
        binary="$work/$side/${package//\//-}.test"
        sha256sum "$binary" > "$out/$key.sha256"
        go tool buildid "$binary" > "$out/$key.buildid"
        go version -m "$binary" > "$out/$key.buildinfo"
        readelf -WS "$binary" > "$out/$key.sections.txt"
        objcopy --dump-section ".text=$work/$key.text" "$binary" "$work/$key.elf-copy"
        sha256sum --check "$out/$key.sha256"
        sha256sum "$work/$key.text" > "$out/$key.text.sha256"
        go tool nm -size "$binary" | awk -v re="$symbols" '$0 ~ re' > "$out/$key.symbols.txt"
        go tool objdump -s "$symbols" "$binary" > "$out/$key.hot.txt"
        test -s "$out/$key.hot.txt" && test -s "$out/$key.symbols.txt"
    done
    cmp "$out/base-$name.overlay.json" "$out/head-$name.overlay.json"
    base_sha=$(cut -d' ' -f1 "$out/base-$name.sha256")
    head_sha=$(cut -d' ' -f1 "$out/head-$name.sha256")
    if [[ "$base_sha" == "$head_sha" ]]; then
        echo "$name ELF byte-identical" >> "$out/elf-identity.txt"
    elif cmp -s "$work/base-$name.text" "$work/head-$name.text"; then
        echo "$name ELF differs; .text byte-identical" >> "$out/elf-identity.txt"
    else
        echo "$name .text differs" >> "$out/elf-identity.txt"
    fi
done < "$input/batch.tsv"

if [[ "$(cat "$out/cpu.txt")" != 'AMD EPYC 7763 64-Core Processor' ]]; then
    printf 'matched_7763=false\nbenchmark_processes=0\nelf_capture=complete\n' > "$out/status.txt"
    exit 0
fi
GOWORK=off GOBIN="$work/bin" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70

sample() {
    local side=$1 file=$2 package profile filter
    while IFS=$'\t' read -r package profile filter; do
        processes=$((processes + 1))
        printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$processes" "$block" "$pair" "$arms" "$side" "$package" >> "$out/order.tsv"
        "$work/bin/benchrun" run-performance "$repo/.tmp/$side" "$work/$side" "$package" "$profile" \
            -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=200ms -test.count=1 -test.cpu=2 >> "$file"
    done < "$input/batch.tsv"
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
                sample "$left" "$path.left.raw"
                sample "$right" "$path.right.raw"
            else
                sample "$right" "$path.right.raw"
                sample "$left" "$path.left.raw"
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
test "$processes" = 240
printf 'matched_7763=true\nstrict_exit=%s\nbenchmark_processes=%s\nelf_capture=complete\n' "$failed" "$processes" > "$out/status.txt"
exit "$failed"
