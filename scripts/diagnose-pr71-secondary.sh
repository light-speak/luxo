#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" != 3 && ( "$#" != 4 || "$4" != --plan-only ) ]]; then
  printf 'usage: %s <git-worktree> <rpc-trace-local|gateway-identity> <formal-zip> [--plan-only]\n' "$0" >&2
  exit 2
fi

export LC_ALL=C
repo=$(git -C "$1" rev-parse --show-toplevel)
scope="$2"
formal="$(cd "$(dirname "$3")" && pwd -P)/$(basename "$3")"
mode="${4:-run}"
helper="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)/diagnose-pr71-secondary.py"
IFS=$'\t' read -r cpu package profile fixture fixture_sha artifact archive_sha benchmark < <(
  python3 "$helper" tuple "$scope"
)

baseline=1117eb9ffcc0beef977abceec656a351b0988389
head=4177d556d135cff1cd27e42798c0b082cf09f9b3
tested=3baa5b629d86749c6f5dc38dd9e540e0d11c39e9
tools=dd6405a02c653c8f2da90ff8ea81795a6781033b
sampler=3a9838348d630bcd0f806fbf38c67ee3e327924b
policy_sha=c4b166e36ca5705e8a93f215738d6bcc813df073cd0749eaef9f3f36292f8deb
evidence="$repo/.tmp/hosted-pr71-$scope"

sha() {
  python3 -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())'
}

check_host() {
  local model
  [[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
  model=$(lscpu | sed -n 's/^Model name:[[:space:]]*//p' | sed 's/[[:space:]]*$//')
  [[ "$model" == "$cpu" ]] || { printf 'expected %s, assigned %s\n' "$cpu" "$model" >&2; return 1; }
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nlinux\namd64\nv1\n1' ]]
  [[ -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')" ]]
}

verify_git() {
  local rev actual
  [[ "$(git -C "$repo" rev-parse "$baseline^{tree}")" == 97e7de78aafc023063c8ad6e44ecfaa3dab04d35 ]]
  [[ "$(git -C "$repo" rev-parse "$head^{tree}")" == 175294b28abcbb21b68ba40fd6288ce63e2e1547 ]]
  [[ "$(git -C "$repo" rev-parse "$tested^{tree}")" == 175294b28abcbb21b68ba40fd6288ce63e2e1547 ]]
  [[ "$(git -C "$repo" rev-parse "$tools^{tree}")" == 5cb2421d34fc1addd094a0a68ad6671414230c13 ]]
  [[ "$(git -C "$repo" cat-file -p "$tested" | sed -n '/^$/q; s/^parent //p')" == "$baseline"$'\n'"$head" ]]
  git -C "$repo" diff --name-only "$baseline" "$tested" > "$evidence/product-diff.txt"
  cmp "$evidence/product-diff.txt" <(printf '%s\n' \
    pkg/lux/auth/claims_test.go pkg/lux/auth/jwt.go pkg/lux/auth/jwt_duration_test.go \
    pkg/lux/auth/purpose_test.go pkg/lux/auth/static_bench_test.go pkg/lux/auth/static_test.go)
  git -C "$repo" diff --exit-code "$baseline" "$tools" -- go.mod go.sum pkg/lux scripts/setup-modules.sh
  for rev in "$baseline" "$tested"; do
    actual=$(git -C "$repo" show "$rev:$package/$fixture" | sha)
    [[ "$actual" == "$fixture_sha" ]]
    printf '%s %s %s\n' "$rev" "$actual" "$package/$fixture"
  done > "$evidence/fixtures.sha256"
  actual=$(git -C "$repo" show "$tools:scripts/benchgate/approved-costs.json" | sha)
  [[ "$actual" == "$policy_sha" ]]
  printf 'base=%s\nhead=%s\ntested=%s\ntools=%s\nsampler=%s\nartifact=%s\narchive_sha256=%s\nprofile=%s\n' \
    "$baseline" "$head" "$tested" "$tools" "$sampler" "$artifact" "$archive_sha" "$profile" > "$evidence/revisions.txt"
}

compile_role() {
  local role="$1" command="$2" dir="$scratch/bin/$1" ending=
  [[ "$profile" == default ]] || ending="-$profile"
  local binary="$dir/${package//\//-}$ending.test"
  "$scratch/bin/benchrun" "$command" "$scratch/source/$role" "$dir" "$package" "$profile" > "$evidence/compile-$role.log" 2>&1
  record_binaries "$role"
  go tool nm -size "$binary" | awk -v root="$benchmark" '$NF ~ ("[.]"root) || $NF ~ /[.](callRequest|CallWithMaskTargetContext|Identity|signWithSubject|Claims.Get)$/' \
    > "$evidence/$role.nm"
  grep -q "[.]$benchmark$" "$evidence/$role.nm"
  go tool objdump -s "[.]($benchmark|callRequest|CallWithMaskTargetContext|Identity|signWithSubject)" "$binary" > "$evidence/$role.objdump"
  grep -q '^TEXT ' "$evidence/$role.objdump"
  local -a run=("$scratch/bin/benchrun" run-performance "$scratch/source/$role" "$dir" "$package" "$profile")
  "${run[@]}" -test.list=. > "$evidence/registry-$role.txt"
  if [[ "$scope" == gateway-identity ]]; then
    [[ "$(cat "$evidence/registry-$role.txt")" == "$benchmark" ]]
  else
    "${run[@]}" -test.run='^TestTraceLocalClientResponseAndReuse$' -test.count=1 -test.timeout=90s -test.v > "$evidence/correctness-$role.txt"
    grep -Fq -- '--- PASS: TestTraceLocalClientResponseAndReuse ' "$evidence/correctness-$role.txt"
  fi
  "${run[@]}" -test.run='^$' -test.bench="^($benchmark)$" -test.benchmem -test.benchtime=1x \
    -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/inventory-$role.txt"
}

gate_comparison() {
  local comparison="$1" stem="$evidence/$1" status=0
  local -a args=() policy=()
  normalize_group "$comparison" primary
  normalize_group "$comparison" confirmation
  "$scratch/bin/benchgate" --confirmation-targets "$stem.primary.csv" > "$stem.targets.tsv"
  if [[ -s "$stem.targets.tsv" ]]; then
    "$scratch/bin/benchplan" --confirmation-plan "$evidence/expected.json" "$stem.targets.tsv" "$stem.targeted.tsv" "$stem.targeted.json"
    "$scratch/bin/benchplan" --verify-profile-batch confirmation "$stem.targeted.json" "$stem.targeted.tsv"
    python3 "$helper" filter "$scope" "$evidence" "$comparison"
    "$scratch/bin/benchplan" --verify "$stem.targeted.json" "$stem.targeted.left.txt" "$stem.targeted.right.txt"
    "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
      base="$stem.targeted.left.txt" head="$stem.targeted.right.txt" > "$stem.targeted.csv" 2> "$stem.targeted.warnings.txt"
    args=(--targeted-confirmation "$stem.primary.csv" "$stem.targeted.csv")
    if [[ "$comparison" == ab ]]; then
      policy=(--approved-costs "$scratch/source/tools/scripts/benchgate/approved-costs.json" --base-sha "$baseline")
    fi
  else
    args=("$stem.primary.csv")
  fi
  "$scratch/bin/benchgate" "${policy[@]}" "${args[@]}" > "$stem.benchgate.log" 2>&1 || status=$?
  printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
  if (( status != 0 )); then
    grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$stem.benchgate.log"
  fi
}

export GOWORK=off GOTOOLCHAIN=local
if [[ "$mode" != --plan-only ]]; then check_host; fi
[[ ! -L "$repo/.tmp" ]]
mkdir -p "$repo/.tmp"
mkdir "$evidence"
scratch=$(mktemp -d "$repo/.tmp/pr71-secondary.XXXXXXXX")
trap '[[ "$scratch" == "$repo"/.tmp/pr71-secondary.* && ! -L "$scratch" ]] && rm -rf -- "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

verify_git
python3 "$helper" prepare "$scope" "$formal" "$evidence" "$baseline" "$tested"
git -C "$repo" show "$sampler:scripts/diagnose-hosted-perf.sh" > "$scratch/shared-sampler.sh"
[[ "$(sha < "$scratch/shared-sampler.sh")" == 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 ]]
python3 "$helper" adapt "$scratch/shared-sampler.sh" "$evidence/profiled-sampler.sh"
bash -n "$evidence/profiled-sampler.sh"
printf '%s\n' "$evidence"
[[ "$mode" != --plan-only ]] || exit 0

source "$evidence/profiled-sampler.sh"
export TMPDIR="$scratch/tmp"
mkdir -p "$TMPDIR" "$scratch/bin" "$scratch/source/"{base,head,tools}
collect_host > "$evidence/environment.txt"
git -C "$repo" archive "$tools" -- go.mod go.sum internal/benchlayout scripts/benchrun scripts/benchplan scripts/benchgate scripts/setup-modules.sh |
  tar -xf - -C "$scratch/source/tools"
git -C "$repo" archive "$baseline" | tar -xf - -C "$scratch/source/base"
git -C "$repo" archive "$tested" | tar -xf - -C "$scratch/source/head"
for role in base head; do
  mkdir "$scratch/source/$role/.tmp"
  sh "$scratch/source/tools/scripts/setup-modules.sh" "$scratch/source/$role"
  cp "$scratch/source/$role/go.work" "$evidence/$role.go.work"
done
(
  cd "$scratch/source/tools"
  for tool in benchrun benchplan benchgate; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/"{benchrun,benchplan,benchgate,benchstat}
) > "$evidence/tools.log" 2>&1
"$scratch/bin/benchrun" performance-profile > "$evidence/compile-profile.txt"
grep -Fxq 'rpc-boundary-overlay-v2 99658ed681f4110df0db3fc688931fa61cd656f3c62477c04a1ffd4bb567ca8e' "$evidence/compile-profile.txt"
grep -Fxq 'luvia-gateway-identity-overlay-v1 ae790127f4acd6df4fe2388d239b2657c999add2e742584284c411bef514e365' "$evidence/compile-profile.txt"
"$scratch/bin/benchplan" --verify-profile-batch primary "$evidence/expected.json" "$evidence/batch.tsv"
compile_role base compile-performance-baseline
compile_role head compile-performance
sample_groups
python3 "$helper" verify-samples "$scope" "$evidence"
for role in base head; do
  cp "$evidence/binaries-$role.txt" "$scratch/before-$role.txt"
  record_binaries "$role"
  cmp "$scratch/before-$role.txt" "$evidence/binaries-$role.txt"
  [[ "$(sha < "$scratch/source/$role/$package/$fixture")" == "$fixture_sha" ]]
done

printf 'comparison\tgate_exit\n' > "$evidence/verdicts.tsv"
gate_comparison aa
gate_comparison ab
cat "$evidence/verdicts.tsv"
grep -Fxq $'aa\t0' "$evidence/verdicts.tsv" || {
  printf 'Same-binary control rejected; timing attribution is inconclusive.\n' >&2
  exit 1
}
grep -Fxq $'ab\t0' "$evidence/verdicts.tsv"
