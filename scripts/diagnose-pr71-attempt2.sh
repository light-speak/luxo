#!/usr/bin/env bash
set -euo pipefail

pr71_attempt2_write_plan() {
  cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/lux/api	default	NullableRootJSON	^BenchmarkNullableRootJSON$/^(int|zero)$/^(plain|present)$
02	pkg/lux/api	default	RawJSONArrayUnicode	^BenchmarkRawJSONArrayUnicode$/^empty$/^strict$
03	pkg/lux/api	default	IdentityEnumArray	^BenchmarkIdentityEnumArray$/^empty$
04	pkg/lux/rpc	default	RPCPoolCloseSafety	^BenchmarkRPCPoolCloseSafety$/^serial$/^(before|after)$
CASES
  {
    printf 'BenchmarkNullableRootJSON/int/%s-2\n' plain present
    printf 'BenchmarkNullableRootJSON/zero/%s-2\n' plain present
  } > "$evidence/01.names"
  printf '%s\n' BenchmarkRawJSONArrayUnicode/empty/strict-2 > "$evidence/02.names"
  printf '%s\n' BenchmarkIdentityEnumArray/empty-2 > "$evidence/03.names"
  printf 'BenchmarkRPCPoolCloseSafety/serial/%s-2\n' before after > "$evidence/04.names"
  cat > "$evidence/attempt-membership.tsv" <<'MEMBERSHIP'
attempt1_confirmed	package	leaf
no	pkg/lux/api	BenchmarkNullableRootJSON/int/plain-2
no	pkg/lux/api	BenchmarkNullableRootJSON/int/present-2
no	pkg/lux/api	BenchmarkNullableRootJSON/zero/plain-2
no	pkg/lux/api	BenchmarkNullableRootJSON/zero/present-2
no	pkg/lux/api	BenchmarkRawJSONArrayUnicode/empty/strict-2
yes	pkg/lux/api	BenchmarkIdentityEnumArray/empty-2
yes	pkg/lux/rpc	BenchmarkRPCPoolCloseSafety/serial/before-2
no	pkg/lux/rpc	BenchmarkRPCPoolCloseSafety/serial/after-2
MEMBERSHIP
  python3 - "$evidence" <<'PYTHON'
import json
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
rows = []
for line in (evidence / "cases.tsv").read_text().splitlines():
    case, package, _, _, _ = line.split("\t")
    for name in (evidence / f"{case}.names").read_text().splitlines():
        rows.append({"Package": package, "Name": name})
leaves = {(r["Package"], r["Name"]) for r in rows}
membership = [line.split("\t") for line in (evidence / "attempt-membership.tsv").read_text().splitlines()[1:]]
if len(rows) != 8 or len(leaves) != 8 or leaves != {(package, name) for _, package, name in membership}:
    raise SystemExit("expected exactly eight distinct leaves with attempt membership")
if len(membership) != 8 or sum(status == "yes" for status, _, _ in membership) != 2:
    raise SystemExit("expected exactly two confirmed repeat leaves")
(evidence / "expected.json").write_text(json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n")
PYTHON
}

pr71_attempt2_verify_process() {
  local raw="$1" case_id="$2" package
  case "$case_id" in 01|02|03) package=pkg/lux/api ;; 04) package=pkg/lux/rpc ;; *) return 1 ;; esac
  awk -v expected="$evidence/$case_id.names" -v os="$probe_goos" -v arch="$probe_goarch" -v pkg="github.com/light-speak/luxo/$package" '
    BEGIN {
      while (getline name < expected) names[++count] = name
      if (count < 1) exit 1
    }
    /^goos: / { os_count++; if ($2 != os) bad=1; next }
    /^goarch: / { arch_count++; if ($2 != arch) bad=1; next }
    /^pkg: / { pkg_count++; if ($2 != pkg) bad=1; next }
    /^cpu: .+/ { cpu_count++; next }
    /^PASS$/ { passes++; next }
    /^Benchmark/ {
      if ($1 != names[++seen] || NF != 8 || $2 !~ /^[1-9][0-9]*$/ ||
          $3 !~ /^[0-9]+([.][0-9]+)?$/ || $4 != "ns/op" ||
          $5 !~ /^[0-9]+([.][0-9]+)?$/ || $6 != "B/op" ||
          $7 !~ /^[0-9]+([.][0-9]+)?$/ || $8 != "allocs/op") bad=1
      next
    }
    /^[[:space:]]*$/ { next }
    { bad=1 }
    END {
      if (bad || seen != count || os_count != 1 || arch_count != 1 ||
          pkg_count != 1 || cpu_count != 1 || passes != 1) {
        print "Unexpected benchmark process input: " FILENAME > "/dev/stderr"
        exit 1
      }
    }
  ' "$raw"
}

pr71_attempt2_record_layout() {
  local revision="$1" package binary
  local pattern='^github[.]com/light-speak/luxo/pkg/lux/(api[.](benchmarkNullableRootJSON|[(][*]Router[)][.]convertBinaryToJSON|encodeRawJSONArray|Benchmark(NullableRootJSON|RawJSONArrayUnicode|IdentityEnumArray)([.]func[0-9]+)?)|rpc[.](benchmarkPoolSerial(Before|After)|[(][*](Pool|poolBeforeConcurrentClose)[)][.](GetContext|Put|tryAcquire|signal))|codec[.]UnwrapNullableRoot|schema[.]BinaryScalarToJSON|jsonutil[.]ParseStringValuesLimit\[.*\])$'
  for package in api rpc; do
    binary="$scratch/bin/$revision/pkg-lux-$package.test"
    go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$revision-$package.nm"
    test -s "$evidence/symbols-$revision-$package.nm"
    go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$revision-$package.objdump"
    test -s "$evidence/symbols-$revision-$package.objdump"
  done
}

pr71_attempt2_archive() {
  local revision="$1" commit="$2"
  local source="$scratch/source/$revision"
  mkdir -p "$source" "$scratch/bin/$revision"
  git -C "$repo" archive "$commit" | tar -xf - -C "$source"
  git -C "$repo" show -s --format="$revision %H %T" "$commit" >> "$evidence/revisions.txt"
  (
    cd "$source"
    sha256sum go.mod go.sum scripts/{setup-modules.sh,benchrun/main.go} scripts/benchgate/approved-costs.json \
      pkg/lux/api/{protocol_bench_test.go,router.go,json_unicode_bench_test.go,json_params.go,identity_reader_bench_test.go,identity_value.go} \
      pkg/lux/codec/wire.go pkg/lux/schema/convert.go pkg/lux/jsonutil/scalar.go \
      pkg/lux/rpc/{pool_close_bench_test.go,pool_close_reference_test.go,pool.go,pool_limits.go} pkg/lux/auth/claims.go
  ) > "$evidence/sources-$revision.sha256"
  sh "$source/scripts/setup-modules.sh" "$source"
  cp "$source/go.work" "$evidence/go-$revision.work.txt"
}

pr71_attempt2_find_inventory() {
  local revision case_id package variant name filter output
  for revision in base head; do
    while IFS=$'\t' read -r case_id package variant name filter; do
      output="$evidence/inventory-$revision-$case_id.txt"
      "$scratch/bin/benchrun" run "$scratch/source/$revision" "$scratch/bin/$revision" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        > "$output"
      pr71_attempt2_verify_process "$output" "$case_id"
    done < "$evidence/cases.tsv"
  done
  printf 'Exact 1x inventory passed for all eight leaves on both revisions.\n'
}

pr71_attempt2_verify_samples() {
  local group sample comparison case_id side revision variant filename count=0
  while IFS=$'\t' read -r group sample comparison case_id side revision variant filename; do
    if [[ "$group" == group ]]; then continue; fi
    [[ "$case_id" =~ ^0[1-4]$ ]] || return 1
    pr71_attempt2_verify_process "$evidence/$filename" "$case_id" || return 1
    count=$((count+1))
  done < "$evidence/order.tsv"
  [[ "$count" -eq 320 ]]
}

pr71_attempt2_main() {
  local helper=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
  local launch revision package inventory_only=false
  if [[ $# -eq 1 && "$1" == --inventory-only ]]; then
    inventory_only=true
  elif [[ $# -ne 0 ]]; then
    printf 'Usage: %s [--inventory-only]\n' "$0" >&2
    return 2
  fi
  repo=$(git rev-parse --show-toplevel)
  launch=$(pwd -P)
  mkdir -p "$launch/.tmp"
  test ! -L "$launch/.tmp"
  evidence="$launch/.tmp/hosted-pr71-attempt2"
  mkdir "$evidence"
  git -C "$repo" show "$helper:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
  printf '%s\n' "$helper" > "$evidence/sampler-revision.txt"
  sha256sum "$evidence/sampler.sh" > "$evidence/sampler.sha256"
  [[ "$(cut -d' ' -f1 "$evidence/sampler.sha256")" == 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 ]]
  source "$evidence/sampler.sh"
  head=e79e2a2930213e317eb6e0f1a77e27f22c4502e7
  scratch=$(mktemp -d "$launch/.tmp/.hosted-pr71-attempt2.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
  mkdir -p "$TMPDIR" "$scratch/bin"
  probe_goos=$(go env GOOS)
  probe_goarch=$(go env GOARCH)
  [[ "$(go env GOVERSION)" == go1.27.1 ]]
  [[ "$(go env GOHOSTOS):$(go env GOHOSTARCH)" == "$probe_goos:$probe_goarch" ]]
  if "$inventory_only"; then
    (uname -a && go version && go env -json GOOS GOARCH GOAMD64 CGO_ENABLED GOEXPERIMENT GOFLAGS CC) > "$evidence/environment.txt"
  else
    [[ "$probe_goos:$probe_goarch:$(go env GOAMD64)" == linux:amd64:v1 ]]
    [[ -z "$(go env GOFLAGS)" ]]
    collect_host > "$evidence/environment.txt"
  fi
  git -C "$repo" merge-base --is-ancestor "$base" "$head"
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
One runner, original default-link compiler flags, exact base and head archives.
Four benchmark processes select the eight attempt-2 timing failures across shards 01 and 08.
Original evidence: run 45157472150 attempt 2, jobs 137849785997 and 137849785961.
Artifact IDs 14845805628 (01) and 14846860403 (08). Both used AMD EPYC 7763.
Attempt 1 on AMD 7763 confirmed IdentityEnumArray/empty and RPCPoolCloseSafety/serial/before.
The other six leaves are new confirmed failures. The original shard 08 ran on Intel.
The targeted processes do not replay the full original shard contexts.
A/A uses the same base binary on both sides. A/B uses base and head binaries.
Each comparison has two groups of ten alternating pairs per exact process scope.
Go benchmark benchmem, 200ms, count=1, CPU=2, test timeout=90s, benchstat alpha=.01.
The original time and allocation thresholds apply. An A/A failure makes the result inconclusive.
These microbenchmarks are diagnostic. The original PR gate still determines acceptance.
PROTOCOL
  pr71_attempt2_write_plan
  pr71_attempt2_archive base "$base"
  pr71_attempt2_archive head "$head"
  cmp "$evidence/sources-base.sha256" "$evidence/sources-head.sha256"
  (
    cd "$scratch/source/base"
    for tool in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$tool" "./scripts/$tool"
    done
  ) > "$evidence/compile-tools.log" 2>&1
  for revision in base head; do
    (
      for package in pkg/lux/api pkg/lux/rpc; do
        "$scratch/bin/benchrun" compile "$scratch/source/$revision" "$scratch/bin/$revision" "$package"
      done
    ) > "$evidence/compile-$revision.log" 2>&1
    record_binaries "$revision"
    pr71_attempt2_record_layout "$revision"
  done
  pr71_attempt2_find_inventory
  if "$inventory_only"; then return 0; fi

  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr71_attempt2_verify_samples
  analyze_groups
  if ! grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"; then
    printf 'A/A crossed the original threshold. The diagnostic is inconclusive.\n' >&2
    return 1
  fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  pr71_attempt2_main "$@"
fi
