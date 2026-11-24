#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

if [[ $# != 3 && ( $# != 4 || "$4" != --plan-only ) ]]; then
  printf 'usage: %s <git-worktree> <9v45|9v74|8573c> <formal-shard.zip> [--plan-only]\n       %s <git-worktree> elf-only -\n' "$0" "$0" >&2
  exit 2
fi
repo=$(git -C "$1" rev-parse --show-toplevel)
scope=$2
helper="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)/diagnose-pr74-auth-linkage.py"
artifact=none
digest=none
if [[ "$scope" == elf-only ]]; then
  [[ $# == 3 && "$3" == - ]]
else
  archive="$(cd "$(dirname "$3")" && pwd -P)/$(basename "$3")"
  IFS=$'\t' read -r cpu shard artifact digest < <(python3 "$helper" tuple "$scope")
fi
base=a2c411b7b83ca233881ace0c5bf3bec0b9646e1d
head=a1faf2f6d2aa4b733ee09959387ff433917d98b0
tested=c50446aaf51d439a105ce674bdddd93407270a5d
head_tree=75700cb51c366082faac11e110b6d1a842aa3c07
benchtime=200ms

if [[ "${4:-}" != --plan-only ]]; then
  [[ "$(uname -sm)" == 'Linux x86_64' ]]
  if [[ "$scope" != elf-only ]]; then
    [[ "$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')" == "$cpu" ]]
  fi
  export GOWORK=off GOTOOLCHAIN=local
  [[ "$(go env GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1' ]]
  [[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
fi
[[ ! -L "$repo/.tmp" ]]
mkdir -p "$repo/.tmp"
evidence=$(mktemp -d "$repo/.tmp/pr74-auth-linkage-evidence.XXXXXXXX")
scratch=$(mktemp -d "$repo/.tmp/pr74-auth-linkage-scratch.XXXXXXXX")
trap '[[ "$scratch" == "$repo"/.tmp/pr74-auth-linkage-scratch.* && ! -L "$scratch" ]] && rm -rf -- "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
[[ "$(git -C "$repo" rev-parse "$base^{tree}")" == 782d78fff9fe3346fa7d161960a12a452cf51ff0 ]]
[[ "$(git -C "$repo" rev-parse "$head^{tree}")" == "$head_tree" ]]
[[ "$(git -C "$repo" rev-parse "$tested^{tree}")" == "$head_tree" ]]
[[ "$(git -C "$repo" cat-file -p "$tested" | sed -n '/^$/q; s/^parent //p')" == "$base"$'\n'"$head" ]]
store="$scratch/git"
git init -q --bare "$store"
git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$store/objects/info/alternates"
graft=(git --git-dir="$store")
"${graft[@]}" read-tree "$tested"
for bearer_file in pkg/lux/luvia/{middleware.go,middleware_test.go}; do
  "${graft[@]}" update-index --cacheinfo "100644,$("${graft[@]}" rev-parse "$base:$bearer_file"),$bearer_file"
done
auth_tree=$("${graft[@]}" write-tree)
[[ "$auth_tree" == b518d1fe4270e372f55c0c8fef16f5c012192434 ]]
"${graft[@]}" diff --name-only "$tested" "$auth_tree" > "$evidence/auth-only-diff.txt"
cmp "$evidence/auth-only-diff.txt" <(printf '%s\n' pkg/lux/luvia/middleware.go pkg/lux/luvia/middleware_test.go)
printf 'base=%s\nhead=%s\ntested=%s\nhead_tree=%s\nauth_only_tree=%s\nscope=%s\nartifact=%s\nsha256=%s\n' \
  "$base" "$head" "$tested" "$head_tree" "$auth_tree" "$scope" "$artifact" "$digest" > "$evidence/revisions.txt"
if [[ "$scope" != elf-only ]]; then
  python3 "$helper" plan "$scope" "$archive" "$evidence"
fi
printf '%s\n' "$evidence"
[[ "${4:-}" != --plan-only ]] || exit 0

export TMPDIR="$scratch/tmp"
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source"
uname -a > "$evidence/host.txt"
lscpu > "$evidence/lscpu.txt"
go env -json GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS GOEXPERIMENT CC > "$evidence/go-env.json"
roles=(base head)
if [[ "$scope" != 9v74 && "$scope" != elf-only ]]; then roles+=(auth-only); fi
for role in "${roles[@]}"; do
  case "$role" in base) revision=$base ;; head) revision=$tested ;; auth-only) revision=$auth_tree ;; esac
  source="$scratch/source/$role"
  mkdir -p "$source/.tmp"
  "${graft[@]}" archive "$revision" | tar -xf - -C "$source"
  sh "$source/scripts/setup-modules.sh" "$source"
  cp "$source/go.work" "$evidence/$role.go.work"
done
(
  cd "$scratch/source/head"
  go build -o "$scratch/bin/benchrun" ./scripts/benchrun
  if [[ "$scope" != elf-only ]]; then
    for tool in benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
    (cd "$scratch" && go mod download -json golang.org/x/perf@v0.0.0-20260825160852-19be9d8e6c70) > "$evidence/benchstat-module.json"
    perf_dir=$(jq -er 'select(.Sum == "h1:M+kM03JKR23KkrutTue5KEQYDSbZTZTJ4FJ1v5zYrHM=") | .Dir' "$evidence/benchstat-module.json")
    (cd "$perf_dir" && go build -o "$scratch/bin/benchstat" ./cmd/benchstat)
  fi
  go version -m "$scratch/bin/"*
) > "$evidence/tools.log" 2>&1

runner="$scratch/bin/benchrun"
planner="$scratch/bin/benchplan"
gate="$scratch/bin/benchgate"
"$runner" performance-profile > "$evidence/compile-profile.txt"
if [[ "$scope" != elf-only ]]; then
  cmp "$evidence/compile-profile.txt" "$evidence/formal-benchmark-compile-profile.txt"
fi

compile_role() {
  local role=$1 pkg=$2 profile=$3 kind=$4 command=compile-performance mark= binary
  if [[ "$role" == base ]]; then command=compile-performance-baseline; fi
  if [[ "$profile" != default ]]; then mark="-$profile"; fi
  "$runner" "$command" "$scratch/source/$role" "$scratch/bin/$role" "$pkg" "$profile" > "$evidence/$kind-$role.compile.log" 2>&1
  binary="$scratch/bin/$role/${pkg//\//-}$mark.test"
  go tool nm -size "$binary" > "$evidence/$kind-$role.nm"
  go tool buildid "$binary" > "$evidence/$kind-$role.build-id"
  go version -m "$binary" > "$evidence/$kind-$role.build.txt"
  awk -F '\t' '
    NR == 1 && $NF ~ /: go1[.]27[.]1$/ { version = 1 }
    $2 == "build" { settings[$3] = 1 }
    END { exit !(version && settings["-trimpath=true"] && settings["CGO_ENABLED=1"] &&
                 settings["GOOS=linux"] && settings["GOARCH=amd64"] && settings["GOAMD64=v1"]) }
  ' "$evidence/$kind-$role.build.txt"
  sha256sum "$binary" > "$evidence/$kind-$role.sha256"
}

for role in base head; do
  compile_role "$role" pkg/lux/api api-non-identity api
  awk '$NF ~ /[/]auth[.]loadConfigFromEnv$|[/]luvia[.]TestServeRejectsScaffoldJWTSecret$/' \
    "$evidence/api-$role.nm" > "$evidence/api-$role.jwt-setup-symbols.txt"
done
python3 "$helper" elf "$scratch/bin/base/pkg-lux-api-api-non-identity.test" "$scratch/bin/head/pkg-lux-api-api-non-identity.test" \
  "$evidence/api-base.nm" "$evidence/api-head.nm" > "$evidence/api-elf.json"
if [[ "$scope" == elf-only ]]; then
  (cd "$evidence" && sha256sum -c -- ./*.sha256) > "$evidence/binary-recheck.txt"
  printf '%s\n' 'ELF comparison completed on a native Linux amd64 runner. No timing samples were collected.'
  exit 0
fi
if [[ "$scope" != 9v74 ]]; then
  for role in "${roles[@]}"; do
    compile_role "$role" pkg/lux/luvia default luvia
    go tool objdump -s '[/]luvia[.](encodeHeartbeat|BenchmarkHeartbeatEncodingComparison.*|BenchmarkMergeDestination.*|extractBearerToken)$' \
      "$scratch/bin/$role/pkg-lux-luvia.test" > "$evidence/luvia-$role.asm"
    grep -q '^TEXT ' "$evidence/luvia-$role.asm"
  done
  for pair in base:head base:auth-only head:auth-only; do
    left=${pair%:*}
    right=${pair#*:}
    python3 "$helper" elf "$scratch/bin/$left/pkg-lux-luvia.test" "$scratch/bin/$right/pkg-lux-luvia.test" \
      "$evidence/luvia-$left.nm" "$evidence/luvia-$right.nm" > "$evidence/luvia-$left-$right-elf.json"
  done
  for role in head auth-only; do
    "$runner" run-performance "$scratch/source/$role" "$scratch/bin/$role" pkg/lux/luvia default \
      -test.run='^TestServeRejectsScaffoldJWTSecret$' -test.count=1 -test.timeout=90s -test.v > "$evidence/luvia-$role.correctness.txt"
    grep -Fq -- '--- PASS: TestServeRejectsScaffoldJWTSecret ' "$evidence/luvia-$role.correctness.txt"
  done
fi

mkdir "$evidence/raw"
head -n 1 "$evidence/planned-order.tsv" > "$evidence/order.tsv"
while IFS=$'\t' read -r group sample index pkg profile comparison side role filter raw; do
  if [[ "$group" == group ]]; then continue; fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$group" "$sample" "$index" "$pkg" "$profile" "$comparison" "$side" "$role" "$filter" "$raw" >> "$evidence/order.tsv"
  "$runner" run-performance "$scratch/source/$role" "$scratch/bin/$role" "$pkg" "$profile" \
    -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime="$benchtime" -test.count=1 -test.cpu=2 -test.timeout=90s \
    > "$evidence/$raw" 2>> "$evidence/stderr.txt"
done < "$evidence/planned-order.tsv"
python3 "$helper" audit "$scope" "$evidence" > "$evidence/sample-audit.json"
(cd "$evidence" && sha256sum -c -- ./*.sha256) > "$evidence/binary-recheck.txt"

score() {
  local index=$1 comparison=$2 stem="$evidence/$1.$2" phase side status=0
  local -a args=() policy=("$gate")
  if [[ "$comparison" == ah ]]; then
    policy+=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base")
  fi
  for phase in primary confirmation; do
    for side in left right; do
      cat "$evidence"/raw/"$index.$comparison.$phase".*."$side".txt > "$scratch/combined.txt"
      "$planner" --normalize "$scratch/combined.txt" "$stem.$phase.$side.txt"
    done
    "$planner" --verify "$evidence/$index.expected.json" "$stem.$phase.left.txt" "$stem.$phase.right.txt"
  done
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.primary.left.txt" head="$stem.primary.right.txt" > "$stem.primary.csv" 2> "$stem.primary.warnings.txt"
  "$gate" --confirmation-targets "$stem.primary.csv" > "$stem.targets.tsv"
  if [[ -s "$stem.targets.tsv" ]]; then
    "$planner" --confirmation-plan "$evidence/$index.expected.json" "$stem.targets.tsv" "$stem.targeted.tsv" "$stem.targeted.json"
    "$planner" --verify-profile-batch confirmation "$stem.targeted.json" "$stem.targeted.tsv"
    for side in left right; do
      python3 "$helper" filter "$stem.confirmation.$side.txt" "$stem.targeted.json" "$stem.targeted.$side.txt"
    done
    "$planner" --verify "$stem.targeted.json" "$stem.targeted.left.txt" "$stem.targeted.right.txt"
    "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
      base="$stem.targeted.left.txt" head="$stem.targeted.right.txt" > "$stem.targeted.csv" 2> "$stem.targeted.warnings.txt"
    args=(--targeted-confirmation "$stem.primary.csv" "$stem.targeted.csv")
  else
    args=("$stem.primary.csv")
  fi
  "${policy[@]}" "${args[@]}" > "$stem.gate.log" 2>&1 || status=$?
  if (( status != 0 )); then grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$stem.gate.log"; fi
  printf '%s\t%s\t%s\n' "$index" "$comparison" "$status" >> "$evidence/verdicts.tsv"
}
printf 'case\tcomparison\tgate_exit\n' > "$evidence/verdicts.tsv"
while IFS=$'\t' read -r index pkg _profile _filter; do
  score "$index" aa
  score "$index" ah
  if [[ "$pkg" == pkg/lux/luvia ]]; then
    score "$index" aj
    score "$index" hj
  fi
done < "$evidence/cases.tsv"
cat "$evidence/verdicts.tsv"
if awk -F '\t' '$2 == "aa" && $3 != "0" {failed=1} END {exit !failed}' "$evidence/verdicts.tsv"; then
  printf 'Same-binary control rejected; attribution is inconclusive.\n' >&2
  exit 3
fi
printf 'Diagnostics completed. Consult ELF proof and individual gate verdicts.\n'
