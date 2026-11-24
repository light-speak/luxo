#!/usr/bin/env bash
set -euo pipefail
umask 077

main_commit=ca818da9bf89e82dc6f5bd2f539e4dfdd576a801
base_tree=ba252c7cace8908cdfcd7c203bc9dc11bb2ba746
assets_revision=61521c06948d6f4cdf04cd94abb6a19fdd2c5ac7
patch_sha=a8fd2d7c1fc8636376bd40a1683bbe420b3840629b3a029b5eb60038ad0e14ba
middleware=pkg/lux/luvia/middleware.go
middleware_test=pkg/lux/luvia/middleware_test.go

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

asset() {
  git -C "$repo" show "$assets_revision:$1" > "$evidence/$2"
  printf '%s  %s\n' "$3" "$evidence/$2" | sha256sum -c - || fail "Diagnostic asset changed: $1"
}

stage_sources() {
  store="$scratch/objects.git"
  git init --quiet --bare "$store"
  git -C "$repo" rev-parse --path-format=absolute --git-path objects > "$store/objects/info/alternates"
  git_tree() { git --git-dir="$store" "$@"; }
  [[ "$(git_tree rev-parse "$main_commit^{tree}")" == "$base_tree" ]] || fail 'Wrong baseline tree'
  [[ "$(git_tree merge-tree --write-tree "$main_commit" "$head")" == "$head_tree" ]] || fail 'Wrong original projection'
  git_tree read-tree "$head_tree"
  git_tree apply --cached --check --whitespace=error "$evidence/candidate.patch"
  git_tree apply --cached --whitespace=error "$evidence/candidate.patch"
  git_tree diff --cached --check "$head_tree"
  [[ "$(git_tree write-tree)" == "$patched_tree" ]] || fail 'Wrong final projection'
  diff -u <(printf '%s\n' "$middleware" "$middleware_test") \
    <(git_tree diff --cached --name-only "$head_tree")
  git_tree apply --cached --reverse --whitespace=error "$evidence/candidate.patch"
  [[ "$(git_tree write-tree)" == "$head_tree" ]] || fail 'Full patch did not reverse'
  git_tree apply --cached --include="$middleware_test" --whitespace=error "$evidence/candidate.patch"
  guard_tree=$(git_tree write-tree)
  [[ "$guard_tree" == "$expected_guard_tree" ]] || fail 'Wrong symmetric test projection'
  [[ "$(git_tree diff --cached --name-only "$head_tree")" == "$middleware_test" ]] || fail 'Wrong test projection paths'
  [[ "$(git_tree rev-parse "$guard_tree:$middleware")" == 27d35d276482a08e78e32cb4fd6551c4b3ec6e91 ]] ||
    fail 'Guard baseline production bytes changed'
  [[ "$(git_tree rev-parse "$guard_tree:$middleware_test")" == "$(git_tree rev-parse "$patched_tree:$middleware_test")" ]] ||
    fail 'Guard test files differ'
  git_tree apply --cached --reverse --include="$middleware_test" --whitespace=error "$evidence/candidate.patch"
  [[ "$(git_tree write-tree)" == "$head_tree" ]] || fail 'Test overlay did not reverse'
  local role tree
  printf 'role\ttree\nbase\t%s\nhead\t%s\npatched\t%s\nguard-head\t%s\n' \
    "$base_tree" "$head_tree" "$patched_tree" "$guard_tree" > "$evidence/source-trees.tsv"
  printf 'main=%s\nhead=%s\nscope=%s\npatch_sha256=%s\n' "$main_commit" "$head" "$suite" "$patch_sha" > "$evidence/revisions.txt"
  for tree in "$base_tree" "$head_tree" "$patched_tree" "$guard_tree"; do
    git_tree show "$tree:pkg/lux/luvia/middleware_bench_test.go" | sha256sum
  done > "$evidence/original-auth-fixture-sha256.txt"
  [[ "$(sort -u "$evidence/original-auth-fixture-sha256.txt" | wc -l)" -eq 1 ]] || fail 'Original benchmark source differs'
  for role in base head patched guard-head; do
    case "$role" in
      base) tree="$base_tree" ;;
      head) tree="$head_tree" ;;
      patched) tree="$patched_tree" ;;
      guard-head) tree="$guard_tree" ;;
    esac
    [[ "$mode" != --plan-only ]] || continue
    mkdir -p "$scratch/source/$role"
    git_tree archive "$tree" | tar -xf - -C "$scratch/source/$role"
    sh "$scratch/source/$role/scripts/setup-modules.sh" "$scratch/source/$role"
    cp "$scratch/source/$role/go.work" "$evidence/go-$role.work.txt"
    (cd "$scratch/source/$role" && sha256sum "$middleware" "$middleware_test") > "$evidence/source-$role.sha256"
  done
}

compile_sources() {
  (
    cd "$scratch/source/base"
    for tool in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$tool" "./scripts/$tool"
    done
  ) >> "$evidence/build.log" 2>&1
  local role package
  for role in base head patched; do
    for package in pkg/lux/luvia pkg/lux/rpc pkg/lux/h2transport; do
      "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" "$package" >> "$evidence/build.log" 2>&1
    done
    record_binaries "$role"
    if [[ "$suite" == pr78 ]]; then
      compile_diagnostic_fixture "$role" shutdown pkg/lux/h2transport shutdown_opening_diagnostic_test.go shutdown.go.txt
    else
      compile_diagnostic_fixture "$role" tls pkg/lux/luvia gateway_tls_diagnostic_test.go tls.go.txt
    fi
  done
  for role in guard-head patched; do
    compile_diagnostic_fixture "$role" guard pkg/lux/luvia gateway_auth_header_diagnostic_test.go guard.go.txt
    go tool nm "$scratch/bin/$role-guard/pkg-lux-luvia.test" |
      awk '/AuthMiddleware|extractBearerToken|BenchmarkDiagnosticGatewayAuthHeaders/' > "$evidence/auth-$role.nm"
    go tool objdump -s 'luvia\..*(AuthMiddleware|extractBearerToken)' \
      "$scratch/bin/$role-guard/pkg-lux-luvia.test" > "$evidence/auth-$role.objdump"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role-guard" pkg/lux/luvia \
      -test.run='^TestAuthMiddlewareBearerHeaderSelection$' -test.v -test.count=1 -test.timeout=90s \
      > "$evidence/test-$role.log" 2>&1
    grep -q '^--- PASS: TestAuthMiddlewareBearerHeaderSelection ' "$evidence/test-$role.log"
  done
}

run_inventory() {
  local role case_id package variant filter binaries
  while IFS=$'\t' read -r role case_id package variant filter; do
    binaries="$scratch/bin/$role"
    [[ "$variant" == default ]] || binaries+="-$variant"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$binaries" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 \
      -test.cpu=2 -test.timeout=90s > "$evidence/inventory-$role-$case_id.txt" 2>> "$evidence/benchmark-stderr.txt"
  done < "$evidence/inventory.tsv"
  python3 "$driver" inventory "$evidence" "$suite" "$target_os" "$target_arch"
}

measure() {
  local group pair comparison case_id side role variant raw package filter left right status gate_status=0
  local -a gate_args
  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  while IFS=$'\t' read -r group pair comparison case_id side role variant raw package filter; do
    run_leg "$group" "$pair" "$comparison" "$side" "$role" "$case_id" "$package" "$variant" "$filter"
  done < "$evidence/schedule.tsv"
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  python3 "$driver" samples "$evidence" "$suite" "$target_os" "$target_arch"
  printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  while IFS=$'\t' read -r comparison left right; do
    cp "$evidence/$comparison.expected.json" "$evidence/expected.json"
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    gate_args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
    if [[ "$left" == base && "$left" != "$right" ]]; then
      gate_args=(--approved-costs "$scratch/source/$right/scripts/benchgate/approved-costs.json" --base-sha "$main_commit" "${gate_args[@]}")
    fi
    status=0
    "$scratch/bin/benchgate" "${gate_args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
    printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
    if (( status != 0 )); then
      grep -Eq '^performance gate rejected [1-9][0-9]* regression\(s\)$' "$evidence/$comparison.benchgate.log"
      [[ "$comparison" == release-ah ]] || gate_status=1
    fi
  done < "$evidence/comparisons.tsv"
  cat "$evidence/verdicts.tsv"
  return "$gate_status"
}

suite="${1:?usage: diagnose-pr78-pr79-empty-header.sh pr78|pr79 [--plan-only|--compile-only]}"
mode="${2:---measure}"
case "$suite" in
  pr78)
    head=397af9c8c6fc6dc807e1dbcbe2e05d8913f58944
    head_tree=51af7fd6ef4126cd7b7a99e1f762f0045a875f9a
    expected_guard_tree=73aad1c668048333a038b693ee926bccd3725734
    patched_tree=0de08cc1278f1d18f05b4cd07523d0ab599b8124 ;;
  pr79)
    head=e04c586e491b8b215308d10f7228636e38547f5b
    head_tree=3548f8c0df1ff05417a092cf1c4f66ccd9f71f4e
    expected_guard_tree=1f1c784874f823ffcd767ab3a0e57ed25e9b760b
    patched_tree=a16d42f26031c1b21f8fb22c0d3053f9ae7d5615 ;;
  *) exit 2 ;;
esac
[[ "$#" -le 2 && ( "$mode" == --measure || "$mode" == --plan-only || "$mode" == --compile-only ) ]]
repo=$(git rev-parse --show-toplevel)
directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
driver="$directory/diagnose-pr78-pr79-empty-header.py"
mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/empty-header-transport.XXXXXXXX")
cleanup_owned() {
  local result=$? report
  if (( result != 0 )) && [[ "$mode" == --compile-only ]]; then
    for report in "$evidence"/build.log "$evidence"/compile-*.log "$evidence"/benchmark-stderr.txt; do
      [[ ! -s "$report" ]] || tail -n 20 "$report" >&2
    done
  fi
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/empty-header-transport.* ]] || exit 99
  rm -rf -- "$scratch"
}
trap cleanup_owned EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
evidence="$scratch/evidence"
if [[ "$mode" == --measure ]]; then evidence="$repo/.tmp/hosted-empty-header-$suite"; fi
mkdir "$evidence" "$scratch/bin" "$scratch/tmp"
export TMPDIR="$scratch/tmp" GOTOOLCHAIN=local GOWORK=off
asset scripts/diagnose-hosted-perf.sh sampler.sh 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072
asset scripts/diagnose-pr78-pr85-attribution.py schedule-helper.py 4eb13c3ace4612e295e9d490d48a8368dc422572e3557bf5f0fc66b846fc1dfb
asset scripts/diagnose-pr79-pr81-scope.json original-pr79-scope.json deef5a560b4e886001b0889d6cbf6c6af0a61803728513734b80faf02df4b851
asset scripts/diagnose-pr79-pr81-tls.go.txt tls.go.txt b04a8c9907db0afd69912a20c10c4abd0b5f9e9a73f301bd830d602ffcfba954
asset scripts/diagnose-h2-shutdown-opening.go.txt shutdown.go.txt ea6127592cb1c6ae965007266ffead6e01eed7026e128e9346e722af64b397bb
source "$evidence/sampler.sh"
base64 -d < "$directory/../.github/qualification/pr78-pr79-empty-header.patch.b64" > "$evidence/candidate.patch"
printf '%s  %s\n' "$patch_sha" "$evidence/candidate.patch" | sha256sum -c - || fail 'Candidate patch changed'
cp "$directory/diagnose-pr78-pr79-empty-header.go.txt" "$evidence/guard.go.txt"
printf '%s  %s\n' '05b03d06b1d62f77231bcbc44e35bbb06038b76b6363f01b867f8f49b6bd69a2' \
  "$evidence/guard.go.txt" > "$evidence/guard-fixture.sha256"
sha256sum -c "$evidence/guard-fixture.sha256" || fail 'Guard fixture changed'
stage_sources
python3 "$driver" plan "$evidence" "$suite"
cat "$evidence/source-trees.tsv"
if [[ "$mode" == --plan-only ]]; then exit 0; fi
target_os=$(go env GOOS)
target_arch=$(go env GOARCH)
[[ "$(go env GOVERSION GOHOSTOS GOHOSTARCH)" == "$(printf 'go1.27.1\n%s\n%s' "$target_os" "$target_arch")" ]] ||
  fail 'Expected native Go 1.27.1'
go_flags=$(go env GOFLAGS)
[[ -z "$(go env GOEXPERIMENT)" ]] || fail 'Expected the default Go experiments'
if [[ "$mode" == --measure ]]; then
  [[ -z "$go_flags" ]] || fail 'Expected the default build flags for measurement'
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]] || fail 'Expected Linux amd64/v1 CGO=1'
  collect_host > "$evidence/environment.txt"
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
One native Linux amd64 v1 runner per PR. Go 1.27.1, CGO=1, default link, 200ms, CPU2.
Two groups of ten alternating pairs, benchmem, alpha .01, time 5% and allocation +0 thresholds.
Release comparisons use exact main, original projection and candidate. Guard comparisons use
the same candidate test source on both sides plus the same supplemental test-only fixture.
Release A/H is diagnostic. A/A, release A/P and guard H/P must pass for qualification.
PROTOCOL
else
  [[ -z "$go_flags" || "$go_flags" == -trimpath ]] || fail 'Unexpected local compile-only build flags'
fi
compile_sources
run_inventory
if [[ "$mode" == --measure ]]; then measure; fi
