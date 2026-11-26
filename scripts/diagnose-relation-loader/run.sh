#!/usr/bin/env bash
set -euo pipefail
umask 077
export GOTOOLCHAIN=local
cd "${GITHUB_WORKSPACE:?}"
repo=$(pwd -P)
out="$repo/.tmp/relation-native"
input="$repo/scripts/diagnose-relation-loader"
base=59a4167f129f0bff0e8425125fa02eae87fd347b
await=0f3e233220962fbdc96a6ecc4f55750074f03861
sealed=8c1b0fc2a868cd8f7e176865982df311340dce61
mkdir -p "$out"
cpu=$(awk -F ': *' '/^model name[[:space:]]*:/ {sub(/[[:space:]]+$/, "", $2); print $2}' /proc/cpuinfo | sort -u)
test -n "$cpu"
printf '%s\n' "$cpu" > "$out/cpu.txt"
test "${RUNNER_KIND:?}" = github-hosted
test "${GITHUB_REPOSITORY:?}" = light-speak/luxo
test "${GITHUB_EVENT_NAME:?}" = push
test "${GITHUB_REF:?}" = refs/heads/diagnostics/pr159-relation-native
case "$cpu" in
    'AMD EPYC 7763 64-Core Processor') original=AC ;;
    'AMD EPYC 9V74 80-Core Processor') original=AD ;;
    *) printf 'matched=false\nbenchmark_processes=0\n' > "$out/status.txt"; exit 3 ;;
esac
grep -Fxq 'VERSION_ID="24.04"' /etc/os-release
test "$(getconf _NPROCESSORS_ONLN)" = 4
test "$(git --no-replace-objects rev-parse HEAD)" = "${GITHUB_SHA:?}"
test "$(git --no-replace-objects show -s --format=%P HEAD)" = "$sealed"
test "$(git --no-replace-objects show -s --format=%P "$sealed")" = "$base"
test "$(git --no-replace-objects rev-parse "$base^{tree}")" = c96b8db43227f6ed447d10b0b32deb39284c7f28
test "$(git --no-replace-objects rev-parse "$sealed^{tree}")" = 63dd6d47bcf97d8246fab77a3b845105e2bd6dae
git --no-replace-objects diff --name-status "$sealed" HEAD > "$out/diagnostic-paths.tsv"
diff -u "$out/diagnostic-paths.tsv" - <<'EOF'
A	.github/workflows/diagnose-pr159-relation-native.yml
A	scripts/diagnose-relation-loader/origin.txt
A	scripts/diagnose-relation-loader/project.go
A	scripts/diagnose-relation-loader/run.sh
A	scripts/diagnose-relation-loader/verify.py
EOF
if [[ "${1:-}" == --probe ]]; then
    printf 'Selected exact original CPU pair %s\n' "$original"
    exit 0
fi
test "$#" = 0
test "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS GOTOOLCHAIN)" = $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1\n\nlocal'

declare -A roots=([A]="$repo/.tmp/relation-A" [C]="$repo/.tmp/relation-C")
verify_checkout() {
    local root=$1 target=$2 tree=$3
    test "$(git --no-replace-objects -C "$root" rev-parse --show-toplevel)" = "$root"
    test "$(git --no-replace-objects -C "$root" rev-parse HEAD 'HEAD^{tree}')" = "$target"$'\n'"$tree"
    test -z "$(git --no-replace-objects -C "$root" status --porcelain)"
}
verify_checkout "${roots[A]}" "$base" c96b8db43227f6ed447d10b0b32deb39284c7f28
verify_checkout "${roots[C]}" "$await" 5d9fa584a66f7b5a4f284a1aaf7e19a1f550b520
parent=$(git --no-replace-objects -C "${roots[C]}" show -s --format=%P "$await")
test "$parent" = f5f0aaf83bc2637e91e1dd61dfe134399adaf2c0
test "$(git --no-replace-objects -C "${roots[C]}" show -s --format=%P "$parent")" = "$base"
printf '%s  %s\n' be83904923c90961a15ac69e374702a47a9347ace53fc88cd31a5f1b7312649f "$input/project.go" | sha256sum -c
printf 'A=%s\nC=%s\nD=%s\noriginal_cpu_pair=%s\n' "$base" "$await" "$sealed" "$original" > "$out/binding.txt"
cat "$input/origin.txt" >> "$out/binding.txt"
{ uname -a; lscpu; go env -json GOVERSION GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS; } > "$out/environment.txt"

work=$(mktemp -d "$repo/.tmp/relation-native-work.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
roots[D]="$work/D"
mkdir "${roots[D]}" "$work/bin" "$work/tmp"
git --no-replace-objects archive "$sealed" | tar -xf - -C "${roots[D]}"
export TMPDIR="$work/tmp" GOTMPDIR="$work/tmp"
for role in A C D; do
    mkdir "${roots[$role]}/.tmp"
    sh scripts/setup-modules.sh "${roots[$role]}"
    cmp "${roots[A]}/go.work" "${roots[$role]}/go.work"
    cmp "${roots[A]}/pkg/codegen/dataloader_nullable_bench_test.go" "${roots[$role]}/pkg/codegen/dataloader_nullable_bench_test.go"
    cmp "${roots[A]}/pkg/codegen/dataloader.go" "${roots[$role]}/pkg/codegen/dataloader.go"
    cmp "${roots[A]}/internal/inferred/relations.go" "${roots[$role]}/internal/inferred/relations.go"
done
test "$(git --no-replace-objects -C "${roots[C]}" diff --name-only "$base" "$await" -- go.mod go.sum scripts)" = ''
test "$(git --no-replace-objects diff --name-only "$base" "$sealed" -- go.mod go.sum scripts)" = ''
export GOWORK=off
for tool in benchrun benchplan benchgate; do
    go build -p 1 -o "$work/bin/$tool" "./scripts/$tool"
done
GOBIN="$work/bin" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70
go build -p 1 -o "$work/bin/project" "$input/project.go"
"$work/bin/benchrun" performance-profile > "$out/compile-profile.txt"
printf '%s  %s\n' ca6d61fffd64abebbfceb442bb80e0e4247d7ddbbd51a33ee2b35fe2c0ce8126 "$out/compile-profile.txt" | sha256sum -c
verify() { python3 -I -B "$input/verify.py" "$@"; }
verify expected > "$out/expected.json"

collect_elf() {
    local binary=$1 evidence=$2
    local symbols='BenchmarkRelationLoaderGeneration|collectDataLoaderInputs|analyzeRelations|relationRemoteKeyNullable|ImplicitRelationKeys'
    sha256sum "$binary" > "$evidence/elf.sha256"
    go tool buildid "$binary" > "$evidence/buildid.txt"
    go version -m "$binary" > "$evidence/buildinfo.txt"
    readelf -WlS "$binary" > "$evidence/sections.txt"
    readelf -Wh "$binary" > "$evidence/header.txt"
    grep -Eq 'Machine: +Advanced Micro Devices X86-64' "$evidence/header.txt"
    objcopy --dump-section ".text=$work/text" "$binary" "$work/copy.elf"
    sha256sum -c "$evidence/elf.sha256"
    go tool nm -size "$binary" | awk -v pat="$symbols" '$3 ~ /^[tT]$/ && $4 ~ pat' > "$evidence/symbols.txt"
    go tool objdump -s "$symbols" "$binary" > "$evidence/hot.txt"
    verify elf "$binary" "$work/text" "$evidence" > "$evidence/addresses.json"
    rm -f -- "$work/text" "$work/copy.elf"
}

filter='^BenchmarkRelationLoaderGeneration$'
sample() (
    cd "${roots[$1]}/pkg/codegen"
    "$work/$2-$1/codegen.test" -test.run='^$' -test.bench="$filter" -test.benchmem \
        -test.benchtime="$3" -test.count=1 -test.cpu=2
)
build() {
    local role=$1 source=${roots[$1]} ordinary evidence overlay expected mode
    ordinary=compile-performance
    if [[ "$role" == A ]]; then ordinary=compile-performance-baseline; fi
    mkdir "$work/stock-$role" "$work/projected-$role" "$out/stock-$role" "$out/projected-$role"
    "$work/bin/benchrun" "$ordinary" "$source" "$work/stock-$role" pkg/codegen default
    mv "$work/stock-$role/pkg-codegen.test" "$work/stock-$role/codegen.test"
    overlay="$work/overlay-$role"
    "$work/bin/project" "$source" "$overlay" codegen > "$overlay.projection.json"
    verify projection "$source" "$overlay"
    cp "$overlay.projection.json" "$out/projected-$role/"
    (cd "$source"; GOWORK="$source/go.work" go test -trimpath -c -overlay="$overlay/overlay.json" ./pkg/codegen -o "$work/projected-$role/codegen.test")
    for mode in stock projected; do
        evidence="$out/$mode-$role"
        collect_elf "$work/$mode-$role/codegen.test" "$evidence"
        sample "$role" "$mode" 1x > "$evidence/inventory.txt"
        verify inventory "$evidence/inventory.txt"
    done
    case "$role" in
        A) expected=0ada15a36b58d6154b0e6efcd949e1d489ef99b61bc5f922a04af010fcf4a6a8 ;;
        C) expected=c9f87b4479e715aaf116894d27967ddbdc0820efb3a0314dabe4915853cd4629 ;;
        D) return ;;
    esac
    # Full ELF SHA is recorded. Require the earlier stock executable code bytes before timing.
    grep -Fq "\"textSHA256\": \"$expected\"" "$out/stock-$role/addresses.json"
}
for arm in A C D; do build "$arm"; done

processes=0
record() {
    local mode=$1 side=$2 role=$3 prefix="$out/$1-$block-$pair_kind"
    processes=$((processes + 1))
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$processes" "$mode" "$block" "$pair" "$pair_kind" "$role" "$side" >> "$out/order.tsv"
    sample "$role" "$mode" 200ms >> "$prefix.$side.raw"
}
for block in 1 2; do
    for pair in {1..10}; do
        for pair_kind in AA CC DD AC AD CD; do
            if (( (block + pair) % 2 == 0 )); then
                for mode in stock projected; do
                    record "$mode" left "${pair_kind:0:1}"; record "$mode" right "${pair_kind:1:1}"
                done
            else
                for mode in projected stock; do
                    record "$mode" right "${pair_kind:1:1}"; record "$mode" left "${pair_kind:0:1}"
                done
            fi
        done
    done
done
test "$processes" = 480

statistics() {
    local prefix=$1 left=$2 right=$3
    "$work/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
        base="$left" head="$right" > "$prefix.csv" 2> "$prefix.warnings"
}
failed=0
gate() {
    local prefix=$1
    shift
    if ! "$work/bin/benchgate" "$@" > "$prefix.gate.txt" 2>&1; then failed=1; fi
}
for mode in stock projected; do
    for pair_kind in AA CC DD AC AD CD; do
        for block in 1 2; do
            prefix="$out/$mode-$block-$pair_kind"
            for side in left right; do
                "$work/bin/benchplan" --normalize "$prefix.$side.raw" "$prefix.$side.txt"
            done
            "$work/bin/benchplan" --verify "$out/expected.json" "$prefix.left.txt" "$prefix.right.txt"
            statistics "$prefix" "$prefix.left.txt" "$prefix.right.txt"
            if [[ "${pair_kind:0:1}" == "${pair_kind:1:1}" ]]; then
                statistics "$prefix.reverse" "$prefix.right.txt" "$prefix.left.txt"
                gate "$prefix" "$prefix.csv"
                gate "$prefix.reverse" "$prefix.reverse.csv"
            fi
        done
        if [[ "${pair_kind:0:1}" != "${pair_kind:1:1}" ]]; then
            approval=()
            if [[ "${pair_kind:0:1}" == A ]]; then
                approval=(--approved-costs scripts/benchgate/approved-costs.json --base-sha "$base")
            fi
            gate "$out/$mode-$pair_kind" "${approval[@]}" "$out/$mode-1-$pair_kind.csv" "$out/$mode-2-$pair_kind.csv"
        fi
    done
done
printf 'matched=true\noriginal_cpu_pair=%s\nbenchmark_processes=%s\nstrict_exit=%s\n' "$original" "$processes" "$failed" > "$out/status.txt"
exit "$failed"
