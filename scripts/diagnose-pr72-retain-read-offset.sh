#!/usr/bin/env bash
set -euo pipefail

pr72_retained_source_manifests() {
  local role="$1" source="$scratch/source/$1"
  (
    cd "$source"
    sha256sum go.mod go.sum pkg/lux/codec/{datetime_bench_test.go,datetime.go,json_bench_test.go,decoder.go,selectionmask.go,selectionmask_bench_test.go,wire.go,encoder.go,array_write_test.go,array_write_bench_test.go,finite_float_bench_test.go,bytes_test.go,json.go} \
      pkg/lux/api/{trace_encoding_bench_test.go,binary_payload_bench_test.go,binary_payload_reference_test.go,binary.go} \
      pkg/lux/rpc/{auth_context_bench_test.go,rpc_test.go,server.go} \
      pkg/lux/jsonutil/jsonutil.go pkg/lux/luvia/{planner.go,planner_test.go}
  ) > "$evidence/sources-$role.sha256"
}

pr72_retained_archive() {
  local role file
  mkdir -p "$scratch/source/base" "$scratch/source/head"
  [[ "$(git -C "$repo" show -s --format=%T "$base")" == 7a051ccbdd2ddbf9c341fd7906cd3c5f3e88dc44 ]]
  [[ "$(git -C "$repo" show -s --format=%T "$head")" == 6daf4f4a3b81a507be32b895731f6ba96776524a ]]
  git -C "$repo" merge-base --is-ancestor "$base" "$head"
  git -C "$repo" archive "$base" | tar -xf - -C "$scratch/source/base"
  git -C "$repo" archive "$head" | tar -xf - -C "$scratch/source/head"
  (
    cd "$scratch/source/head"
    sha256sum pkg/lux/codec/{array_write_test.go,bytes_test.go,wire.go} pkg/lux/rpc/{rpc_test.go,server.go}
  ) > "$evidence/candidate-before.sha256"
  pr72_rpc_context_apply_patches "$scratch/source/base" "$evidence/bundle.json" base
  pr72_rpc_context_add_base_planner_fixture "$scratch/source/base"

  # Keep the original reader. Import only the symmetric test hunk from the offset patch.
  (
    cd "$scratch/source/head"
    export GIT_CEILING_DIRECTORIES="$scratch/source"
    git apply --whitespace=error "$evidence/rpc-context-inline.patch"
    git apply --whitespace=error --include=pkg/lux/codec/bytes_test.go "$evidence/read-bytes-offset.patch"
    git apply --whitespace=error "$evidence/verifier-load.patch"
    cat > "$evidence/candidate-after.sha256" <<'HASHES'
cf469513e09fff2ab49211c17476d4a6ee5e6d960b9184ce8403d2335b939e36  pkg/lux/codec/array_write_test.go
f6fb8e29aa3648581ffec29ba5a281a197b33de3f94c920aaeb53f30d4d35065  pkg/lux/codec/bytes_test.go
c8cab050daf5809e968b196888fed1f459fb9575392b44e08faa430ed88c9c54  pkg/lux/codec/wire.go
9d29e5d1b6c90b18d6d8b4091890b6dfb9cf316ed93f9f970108fc77b057a438  pkg/lux/rpc/rpc_test.go
1e2ceec69c3bd1869d217690a46d023795167d9644699312b3a31c7e4782f98e  pkg/lux/rpc/server.go
HASHES
    sha256sum -c "$evidence/candidate-after.sha256"
  )
  git -C "$repo" show -s --format="base %H %T" "$base" > "$evidence/revisions.txt"
  git -C "$repo" show -s --format="head %H %T" "$head" >> "$evidence/revisions.txt"
  pr72_rpc_context_verify_benchmark_sources
  (
    cd "$evidence"
    printf '%s  %s\n' "$fixture_digest" benchmark-fixtures-base.sha256 | sha256sum -c -
  )
  for role in base head; do pr72_retained_source_manifests "$role"; done
  (
    cd "$scratch/source/base"
    printf '%s  %s\n' 0228ad8f89907ac82d204724e61692047ebb35bb32743bf17db4935b37905d65 \
      pkg/lux/luvia/planner_selection_diagnostic_test.go | sha256sum -c -
  )
}

pr72_retained_replay() {
  local replay="$scratch/source/patch-proof" patch="$1" fingerprint file
  printf '%s  %s\n' 904dce2d2991fa244907eb82d6b9508c98d425142952999f06be15ce18383cdc \
    "$patch" | sha256sum -c -
  mkdir "$replay"
  while read -r fingerprint file; do
    mkdir -p "$replay/$(dirname "$file")"
    git -C "$repo" show "$head:$file" > "$replay/$file"
  done < "$evidence/candidate-before.sha256"
  (
    cd "$replay"
    export GIT_CEILING_DIRECTORIES="$scratch/source"
    sha256sum -c "$evidence/candidate-before.sha256"
    git apply --whitespace=error --check "$patch"
    git apply --whitespace=error "$patch"
    sha256sum -c "$evidence/candidate-after.sha256"
    git apply --whitespace=error --reverse "$patch"
    sha256sum -c "$evidence/candidate-before.sha256"
  )
}

pr72_retained_compile() {
  local role package test_name binary
  local validation='^github\.com/light-speak/luxo/pkg/lux/jsonutil\.(ValidUTF8JSON|WithinJSONDepth|jsonStringEnd)$|^encoding/json/(jsontext|internal/jsonwire)\.'
  for role in base head; do
    sh "$scratch/source/$role/scripts/setup-modules.sh" "$scratch/source/$role"
    cp "$scratch/source/$role/go.work" "$evidence/go-$role.work.txt"
  done
  (
    cd "$scratch/source/base"
    for package in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$package" "./scripts/$package"
    done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  ) > "$evidence/compile-tools.log" 2>&1
  for role in base head; do
    mkdir "$scratch/bin/$role"
    (
      for package in pkg/lux/codec pkg/lux/rpc pkg/lux/luvia pkg/lux/api; do
        "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" "$package"
      done
    ) > "$evidence/compile-$role.log" 2>&1
    record_binaries "$role"
    pr72_rpc_context_record_layout "$role"
    for package in codec api; do
      binary="$scratch/bin/$role/pkg-lux-$package.test"
      go tool nm -size "$binary" | awk -v pattern="$validation" '$4 ~ pattern' > "$evidence/validator-$role-$package.nm"
      go tool objdump -s "$validation" "$binary" > "$evidence/validator-$role-$package.objdump"
      test -s "$evidence/validator-$role-$package.nm"
      test -s "$evidence/validator-$role-$package.objdump"
    done
  done
  "$scratch/bin/benchrun" run "$scratch/source/head" "$scratch/bin/head" pkg/lux/rpc \
    -test.run='^TestRPCReusesStaticAuthenticationErrors$' -test.v -test.timeout=90s > "$evidence/rpc-static-authentication-errors.log"
  grep -q '^--- PASS: TestRPCReusesStaticAuthenticationErrors ' "$evidence/rpc-static-authentication-errors.log"
  "$scratch/bin/benchrun" run "$scratch/source/head" "$scratch/bin/head" pkg/lux/codec \
    -test.run='^Test(ArrayHeaderWriteBounds|ArrayHeaderWriteCapacity|FiniteFloatBenchmarkParity)$' \
    -test.v -test.timeout=90s > "$evidence/array-header-test.log"
  for test_name in TestArrayHeaderWriteBounds TestArrayHeaderWriteCapacity TestFiniteFloatBenchmarkParity; do
    grep -q "^--- PASS: $test_name " "$evidence/array-header-test.log"
  done
  pr72_rpc_context_run_boundary_tests
  pr72_rpc_context_find_inventory
}

pr72_retained_main() {
  local loader=abb5738b19aeb8e24e733b897941826311fec55d
  local sampler=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
  local fixture_digest=11fae710ec5cd28db40633fb7fe845fa79e93f179ad3eebb8beace6566daf8f3
  local launch patch='' dry_run=false
  if [[ $# == 1 && "$1" == --dry-run ]]; then
    dry_run=true
    patch="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)/pr72-retain-read-offset-production.patch"
  elif [[ $# != 0 ]]; then
    printf 'Usage: %s [--dry-run]\n' "$0" >&2
    return 2
  fi

  repo=$(git rev-parse --show-toplevel)
  launch=$(pwd -P)
  mkdir -p "$launch/.tmp"
  test ! -L "$launch/.tmp"
  evidence="$launch/.tmp/hosted-pr72-retain-read-offset"
  mkdir "$evidence"
  git -C "$repo" show "$loader:scripts/diagnose-pr72-rpc-context-inline.sh" > "$evidence/loader.sh"
  git -C "$repo" show "$loader:scripts/diagnose-pr72-rpc-context-inline.json" > "$evidence/bundle.json"
  git -C "$repo" show "$sampler:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
  (
    cd "$evidence"
    printf '%s  %s\n' \
      2a3330be68e16ffa98958a48f4feb79197ed173350a2b07d36c0d004ce65dd86 loader.sh \
      0485364e289dc22f262e906c9359f6908a391d9ef9788494dcd35ef18f1e99e1 bundle.json \
      9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 sampler.sh > tools.sha256
    sha256sum -c tools.sha256
  )
  printf 'loader=%s\nsampler=%s\n' "$loader" "$sampler" > "$evidence/inputs.txt"
  source "$evidence/loader.sh"
  source "$evidence/sampler.sh"
  head=f86453bc03d6ef7d0210aeb414c5e9a7dd540ed0
  scratch=$(mktemp -d "$launch/.tmp/.hosted-pr72-retain-read-offset.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
This replays all 39 leaves and 15 process filters from run 45258221740 without the ReadBytes offset-removal hunk.
The head binary is exact role C from the four-role diagnostic run 45265357760.
The base is 65f1892 with the same Planner and symmetric codec/RPC test fixtures. Head f86453b retains the array and RPC production improvements.
The second patch is applied only to codec/bytes_test.go; the complete third patch still applies.
A/A uses one binary; A/B is A versus role C. Same native runner, Go 1.27.1 amd64 v1, CGO1, default link, trimpath.
Two groups of ten alternating pairs, 200ms, CPU=2, benchmem, alpha=.01, original rejection and approval rules.
This diagnostic retains full raw evidence and does not replace the independent exact PR gates.
PROTOCOL
  pr72_rpc_context_write_plan
  pr72_retained_archive
  if "$dry_run"; then
    pr72_retained_replay "$patch"
    printf 'All scope/source checks and standalone production patch replay passed.\n'
    return
  fi

  export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
  mkdir -p "$TMPDIR" "$scratch/bin"
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  [[ -z "$(go env GOFLAGS)" && -z "$(go env GOEXPERIMENT)" ]]
  probe_goos=linux
  probe_goarch=amd64
  collect_host > "$evidence/environment.txt"
  pr72_retained_compile
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr72_rpc_context_verify_samples
  analyze_groups
  pr72_rpc_context_require_gates
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  pr72_retained_main "$@"
fi
