#!/usr/bin/env bash
set -euo pipefail
umask 077
export GOTOOLCHAIN=local

cd "${GITHUB_WORKSPACE:?}"
repo=$(pwd -P)
base=5017b587f4cf71d30ebba8b542267aed597bb573
head=8261decdeb6fb66f6dffa772a6889cf0a82da644
out="$repo/.tmp/pr160-stream-float"
input="$repo/scripts/diagnose-pr160-stream-float"
base_src="$repo/.tmp/base"
head_src="$repo/.tmp/head"

test "${RUNNER_KIND:?}" = github-hosted
test "${GITHUB_REPOSITORY:?}" = light-speak/luxo
test "${GITHUB_EVENT_NAME:?}" = push
test "${GITHUB_REF:?}" = refs/heads/diagnostics/pr160-api-float-7763
grep -Fxq 'VERSION_ID="24.04"' /etc/os-release
test "$(getconf _NPROCESSORS_ONLN)" = 4
test "$(cat "$out/cpu.txt")" = 'AMD EPYC 7763 64-Core Processor'
test "$(git rev-parse HEAD)" = "${GITHUB_SHA:?}"
test "$(git show -s --format=%P HEAD)" = "$head"
test "$(git show -s --format=%P "$head")" = "$base"
test "$(git -C "$base_src" rev-parse HEAD)" = "$base"
test "$(git rev-parse "$base^{tree}" "$head^{tree}")" = $'f4b13c19316c9fd4cbb0be56d9ba31eab3f98e7f\naa8223878be0583edbbb0ae534a5f122054d75e8'
git diff --name-status "$head" HEAD | diff -u - <(cat <<'PATHS'
A	.github/workflows/diagnose-pr160-stream-float.yml
A	scripts/diagnose-pr160-stream-float/batch.tsv
A	scripts/diagnose-pr160-stream-float/expected.json
A	scripts/diagnose-pr160-stream-float/run.sh
PATHS
)
git diff --exit-code "$base" "$head" -- pkg go.mod go.sum VERSION scripts/setup-modules.sh scripts/benchrun/linked_fixtures.go
test "$(git rev-parse "$base:pkg/lux/api")" = bcf53ef7b5469fdd2ea7298f023d467d99dd0d6d
test "$(git rev-parse "$head:pkg/lux/api")" = bcf53ef7b5469fdd2ea7298f023d467d99dd0d6d
test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS GOTOOLCHAIN)" = $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1\n\nlocal'
(
    cd "$input"
    printf '%s\n' \
        'b3026238aa6609faf137cedd19c19119378893ddd00a6184794dc99b342f9f5e  expected.json' \
        'be224d09e1d1bd582662c8a73f80bc3ca54a80f4a4cb36a76576a7a32eeed736  batch.tsv' |
        sha256sum --check
)
printf 'base=%s\nhead=%s\nrun=45880400669\nattempt=1\nshard=10\njob=140121679831\nartifact=15150365373\nartifactSHA256=79c769c5ef479e80be1478255aa02f1afb628c3bc6b72e9d23dcbec6a7cfa399\n' "$base" "$head" > "$out/binding.txt"
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED; } > "$out/environment.txt"
git diff --name-status "$base" "$head" > "$out/compared-paths.txt"

work=$(mktemp -d "$repo/.tmp/pr160-stream-float-work.XXXXXXXX")
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
"$work/bin/benchrun" performance-profile > "$out/compile-profile.txt"
printf '%s  %s\n' 66f667fe32c2e5cb6d4379d7efb160494d221d5fe0e39ca2a39ebcbc70f3a857 "$out/compile-profile.txt" | sha256sum --check
"$work/bin/benchplan" --verify-profile-batch confirmation "$input/expected.json" "$input/batch.tsv"

cat > "$work/wrapper/go" <<'PY'
#!/usr/bin/env python3
import json
import os
from pathlib import Path
import subprocess
import sys

args = sys.argv[1:]
overlays = [flag.removeprefix("-overlay=") for flag in args if flag.startswith("-overlay=")]
assert args[:3] == ["test", "-trimpath", "-c"], args
assert args[-3:-1] == ["./pkg/lux/api", "-o"], args
assert len(overlays) == 1 and len(args) == 7, args
overlay = json.loads(Path(overlays[0]).read_text())["Replace"]
reported = {str(Path(path).relative_to(Path.cwd())): value for path, value in overlay.items()}
expected = {f"pkg/lux/api/{fixture}": "" for fixture in (
    "identity_reader_bench_test.go", "identity_reader_fuzz_test.go",
    "identity_reader_test.go", "identity_value_test.go",
)}
assert reported == expected
out = Path(os.environ["LUXO_DIAG_OUTPUT"])
out.with_suffix(".overlay.json").write_text(json.dumps(reported, sort_keys=True) + "\n")
go = os.environ["LUXO_DIAG_REAL_GO"]
subprocess.run([go, *args], check=True)
template = "{{.ImportPath}}\t{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}{{end}}\t{{join .GoFiles \",\"}}"
with out.with_suffix(".dependencies.tsv").open("wb") as stream:
    subprocess.run([go, "list", "-deps", "-test", "-f", template, "-overlay=" + overlays[0], args[-3]],
                   stdout=stream, check=True)
PY
chmod +x "$work/wrapper/go"
IFS=$'\t' read -r package profile filter < "$input/batch.tsv"
test "$package/$profile" = pkg/lux/api/api-non-identity
for side in base head; do
    command=compile-performance
    if [[ "$side" == base ]]; then command=compile-performance-baseline; fi
    PATH="$work/wrapper:$PATH" LUXO_DIAG_OUTPUT="$out/$side" \
        "$work/bin/benchrun" "$command" "$repo/.tmp/$side" "$work/$side" "$package" "$profile"
    binary="$work/$side/pkg-lux-api-api-non-identity.test"
    sha256sum "$binary" > "$out/$side.sha256"
    go tool buildid "$binary" > "$out/$side.buildid"
    go version -m "$binary" > "$out/$side.buildinfo"
    objcopy --dump-section ".text=$work/$side.text" --dump-section ".rodata=$work/$side.rodata" \
        "$binary" "$work/$side.elf-copy"
    sha256sum "$work/$side.text" "$work/$side.rodata" > "$out/$side.sections.sha256"
    go tool nm -size "$binary" | awk '$3 ~ /^[tT]$/ && $4 ~ /BenchmarkStreamParamsLookupFloat|StreamParams.*LookupFloat|IsFiniteFloat/' > "$out/$side.symbols.txt"
    test -s "$out/$side.symbols.txt"
    go tool objdump -s 'BenchmarkStreamParamsLookupFloat|StreamParams.*LookupFloat|IsFiniteFloat' "$binary" > "$out/$side.hot.txt"
    test -s "$out/$side.hot.txt"
done
cmp "$out/base.overlay.json" "$out/head.overlay.json"
cmp "$out/base.dependencies.tsv" "$out/head.dependencies.tsv"

for side in base head; do
    "$work/bin/benchrun" run-performance "$repo/.tmp/$side" "$work/$side" "$package" "$profile" \
        -test.run='^$' -test.bench='^BenchmarkStreamParamsLookupFloat$' -test.benchmem \
        -test.benchtime=1x -test.count=1 -test.cpu=2 > "$out/$side.inventory.raw"
    "$work/bin/benchplan" --normalize "$out/$side.inventory.raw" "$out/$side.inventory.txt"
done
"$work/bin/benchplan" --verify "$input/expected.json" "$out/base.inventory.txt" "$out/head.inventory.txt"
if cmp -s "$work/base/pkg-lux-api-api-non-identity.test" "$work/head/pkg-lux-api-api-non-identity.test"; then
    echo 'ELF identity: byte-identical' > "$out/elf-identity.txt"
elif cmp -s "$work/base.text" "$work/head.text"; then
    echo 'ELF identity: different file, byte-identical .text' > "$out/elf-identity.txt"
else
    echo 'ELF identity: different .text' > "$out/elf-identity.txt"
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
        for arms in AA BB AB; do
            left=base; right=head
            case "$arms" in AA) right=base ;; BB) left=head ;; esac
            path="$out/$arms-$block"
            if (( (block + pair) % 2 == 0 )); then
                sample "$left" "$path.left.raw"; sample "$right" "$path.right.raw"
            else
                sample "$right" "$path.right.raw"; sample "$left" "$path.left.raw"
            fi
        done
    done
    for arms in AA BB AB; do
        path="$out/$arms-$block"
        for side in left right; do
            "$work/bin/benchplan" --normalize "$path.$side.raw" "$path.$side.txt"
        done
        "$work/bin/benchplan" --verify "$input/expected.json" "$path.left.txt" "$path.right.txt"
        "$work/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
            base="$path.left.txt" head="$path.right.txt" > "$path.csv" 2> "$path.warnings"
        if [[ "$arms" != AB ]]; then
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
    "$out/AB-1.csv" "$out/AB-2.csv" 2>&1 | tee "$out/candidate.gate.txt"; then
    failed=1
fi
test "$processes" = 120
printf 'matched=true\nstrict_exit=%s\nbenchmark_processes=%s\n' "$failed" "$processes" > "$out/status.txt"
exit "$failed"
