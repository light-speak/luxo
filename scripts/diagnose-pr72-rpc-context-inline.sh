#!/usr/bin/env bash
set -euo pipefail

pr72_rpc_context_write_plan() {
  cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/lux/codec	default	DateTimePair-epoch	^BenchmarkDateTimePair$/^epoch$/^pair_read$
02	pkg/lux/codec	default	DateTimePair-current	^BenchmarkDateTimePair$/^current$/^pair_(read|write)$
03	pkg/lux/codec	default	DateTimePair-year9999	^BenchmarkDateTimePair$/^year_9999$/^pair_write$
04	pkg/lux/codec	default	RawJSONBoundary	^BenchmarkRawJSONBoundary$/^(small|precision|ascii72KiB|unicode72KiB|surrogate128)$/^read$/^strict=false$
05	pkg/lux/rpc	default	RPCNilContextGuard	^BenchmarkRPCNilContextGuard$/^NoToken$/^After$
06	pkg/lux/rpc	default	RPCNilContextGuardControls	^BenchmarkRPCNilContextGuard$/^(Verified|Rejected|Unconfigured)$/^After$
07	pkg/lux/codec	default	SelectionMaskLimits	^BenchmarkSelectionMaskLimits$/^(empty|small|64-children)$/^after$
08	pkg/lux/luvia	default	PlanSelection	^BenchmarkPlanSelection$/^(all|nested)$
09	pkg/lux/codec	default	ArrayHeaderWrite	^BenchmarkArrayHeaderWrite$/^(0|128|1000000)$
10	pkg/lux/codec	default	InvalidArrayHeaderWrite	^BenchmarkArrayHeaderWrite$/^(1000001|-1)$
11	pkg/lux/codec	default	FiniteFloatArray	^BenchmarkFiniteFloatArray$/^checked$
12	pkg/lux/api	default	TraceArrayEncoding	^BenchmarkDebugTraceTextEncoding$/^spans=(1|16)$/^text=0$/^binary$/^before=false$
13	pkg/lux/codec	default	ReadBytesPrefix	^BenchmarkReadBytesPrefix$/^(0|1|16|127|128|1024|16384|73728)$
14	pkg/lux/codec	default	StrictJSONRead	^BenchmarkRawJSONBoundary$/^(small|precision|unicode72KiB)$/^read$/^strict=true$
15	pkg/lux/api	default	BinaryListRead	^BenchmarkBinaryListPayload$/^(Bytes|JSON)$/^count=128$/^version=1$
CASES
  printf '%s\n' BenchmarkDateTimePair/epoch/pair_read-2 > "$evidence/01.names"
  printf 'BenchmarkDateTimePair/current/%s-2\n' pair_read pair_write > "$evidence/02.names"
  printf '%s\n' BenchmarkDateTimePair/year_9999/pair_write-2 > "$evidence/03.names"
  printf 'BenchmarkRawJSONBoundary/%s/read/strict=false-2\n' small precision ascii72KiB unicode72KiB surrogate128 > "$evidence/04.names"
  printf '%s\n' BenchmarkRPCNilContextGuard/NoToken/After-2 > "$evidence/05.names"
  printf 'BenchmarkRPCNilContextGuard/%s/After-2\n' Verified Rejected Unconfigured > "$evidence/06.names"
  printf 'BenchmarkSelectionMaskLimits/%s/after-2\n' empty small 64-children > "$evidence/07.names"
  printf 'BenchmarkPlanSelection/%s-2\n' all nested > "$evidence/08.names"
  printf 'BenchmarkArrayHeaderWrite/%s-2\n' 0 128 1000000 > "$evidence/09.names"
  printf 'BenchmarkArrayHeaderWrite/%s-2\n' 1000001 -1 > "$evidence/10.names"
  printf '%s\n' BenchmarkFiniteFloatArray/checked-2 > "$evidence/11.names"
  printf 'BenchmarkDebugTraceTextEncoding/spans=%s/text=0/binary/before=false-2\n' 1 16 > "$evidence/12.names"
  printf 'BenchmarkReadBytesPrefix/%s-2\n' 0 1 16 127 128 1024 16384 73728 > "$evidence/13.names"
  printf 'BenchmarkRawJSONBoundary/%s/read/strict=true-2\n' small precision unicode72KiB > "$evidence/14.names"
  printf 'BenchmarkBinaryListPayload/%s/count=128/version=1-2\n' Bytes JSON > "$evidence/15.names"
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
if len(rows) != 39 or len({(r["Package"], r["Name"]) for r in rows}) != 39:
    raise SystemExit("expected exactly thirty-nine distinct leaves")
(evidence / "expected.json").write_text(json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n")
PYTHON
  (
    cd "$evidence"
    printf '%s  %s\n' \
      c97fbac73bec2fc17645a7330613cb2019df0521ae4e1615daa23650bcde3f0e cases.tsv \
      a74def50c44a6b53fab04a34eebe8c3b2d91c178c1296cb38728ca15604cb954 expected.json > scope.sha256
    sha256sum -c scope.sha256
  )
}

pr72_rpc_context_verify_process() {
  local raw="$1" case_id="$2" package
  case "$case_id" in
    01|02|03|04|07|09|10|11|13|14) package=pkg/lux/codec ;;
    05|06) package=pkg/lux/rpc ;;
    08) package=pkg/lux/luvia ;;
    12|15) package=pkg/lux/api ;;
    *) return 1 ;;
  esac
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

pr72_rpc_context_record_layout() {
  local revision="$1" package binary
  local pattern='[.](ReadDateTime|AppendDateTime|ReadVarint|ReadSvarint|AppendSvarint|ReadBytes|ReadJSON|Finish|requestContext|verifyRequestContext|WriteArrayHeader|SplitSelectionMask|Plan|benchmarkJSONRead|readBinaryListParam|readBinaryBytesArray|readBinaryJSONArray|Benchmark(DateTimePair|RawJSONBoundary|RPCNilContextGuard|SelectionMaskLimits|PlanSelection|ArrayHeaderWrite|FiniteFloatArray|DebugTraceTextEncoding|ReadBytesPrefix|BinaryListPayload).*)$'
  for package in codec rpc luvia api; do
    binary="$scratch/bin/$revision/pkg-lux-$package.test"
    go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$revision-$package.nm"
    test -s "$evidence/symbols-$revision-$package.nm"
    go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$revision-$package.objdump"
    test -s "$evidence/symbols-$revision-$package.objdump"
  done
}

pr72_rpc_context_apply_patches() {
  local source="$1" bundle="$2" revision="$3"
  python3 - "$bundle" "$evidence" "$source" "$revision" <<'PYTHON'
from hashlib import sha256
import json
import os
from pathlib import Path
import re
import subprocess
import sys

bundle, evidence, source = map(Path, sys.argv[1:4])
revision = sys.argv[4]
if revision not in ("base", "head"):
    raise SystemExit(f"unexpected overlay role: {revision}")
payload = json.loads(bundle.read_text())
wire = "pkg/lux/codec/wire.go"
array_test = "pkg/lux/codec/array_write_test.go"
bytes_test = "pkg/lux/codec/bytes_test.go"
server = "pkg/lux/rpc/server.go"
rpc_test = "pkg/lux/rpc/rpc_test.go"
original = {
    wire: "cac2dd90bdf4e61ebd4c83cbe5df5bfc31e10df9e0a5f7a2b8e9d027cff8562c",
    array_test: "63c1e136a50371db768100ba8965612de9921081d087179c4ab253a091ab25b8",
    rpc_test: "03720eec126d79f07dc0410a6fc4e5a399a270dd3900e9016f80f5f90909a86d",
    server: "313870a1f66bbde4e83bfcd88841ccaf70135855ddd5f8d80d308b968736da0a",
}
previous = {
    wire: "c8cab050daf5809e968b196888fed1f459fb9575392b44e08faa430ed88c9c54",
    array_test: "cf469513e09fff2ab49211c17476d4a6ee5e6d960b9184ce8403d2335b939e36",
    rpc_test: "0073d259ce97a9b1131d602d4ca53a330533f8109dd54d56fd1b78f27999445c",
    server: "c6588964660d9807e7da103fe3b66646d1849ae0416d5ab7ae702ce91f165a50",
}
read_before = {
    wire: previous[wire],
    bytes_test: "cee978ab5bc5b16d81a8d53d99b6a27653b94a6bf765cc5c62e26da69a173443",
}
read_after = {
    wire: "46753adeddd4d056128d4f26ddbfdb3b32a558bf4e36967aa7898cd1834567d1",
    bytes_test: "f6fb8e29aa3648581ffec29ba5a281a197b33de3f94c920aaeb53f30d4d35065",
}
rpc_before = {server: previous[server], rpc_test: previous[rpc_test]}
rpc_after = {
    server: "1e2ceec69c3bd1869d217690a46d023795167d9644699312b3a31c7e4782f98e",
    rpc_test: "9d29e5d1b6c90b18d6d8b4091890b6dfb9cf316ed93f9f970108fc77b057a438",
}
baseline_rpc_test = "ad2333bd90f0cd6f2336a2c1fa75e15f64a7df47cefadeacd93088f8fc2d7182"


def verify_sources(name, expected):
    for path, digest in expected.items():
        if sha256((source / path).read_bytes()).hexdigest() != digest:
            raise SystemExit(f"{name}: source SHA-256 mismatch for {path}")
    manifest = "".join(f"{digest}  {path}\n" for path, digest in expected.items())
    (evidence / f"{name}.sha256").write_text(manifest)


def checked_patch(key, name, digest, paths):
    patch = payload[key].encode()
    expected = [(path.encode(), path.encode()) for path in paths]
    if sha256(patch).hexdigest() != digest or re.findall(rb"^diff --git a/(\S+) b/(\S+)$", patch, re.M) != expected:
        raise SystemExit(f"{name}: patch SHA-256 or file scope mismatch")
    path = evidence / f"{name}.patch"
    path.write_bytes(patch)
    return path


def apply_patch(patch, manifest, before, after, include=None):
    verify_sources(f"{manifest}-before", before)
    env = dict(os.environ, GIT_CEILING_DIRECTORIES=str(source.parent))
    command = ["git", "apply", "--whitespace=error"]
    if include is not None:
        command.append(f"--include={include}")
    for flags in (["--check"], [], ["--reverse", "--check"]):
        subprocess.run([*command, *flags, str(patch)], cwd=source, env=env, check=True)
    verify_sources(f"{manifest}-after", after)


if set(payload) != {"patch", "readBytesOffset", "verifierLoad"}:
    raise SystemExit("unexpected overlay bundle keys")
first = checked_patch("patch", "rpc-context-inline",
                      "2c1c6f47e3e62118e847177c12b76f33eafbe7252ce31cc5b78c521c26ad2039", original)
read = checked_patch("readBytesOffset", "read-bytes-offset",
                     "993010c23a8d5011c3cef7c9f76c56f85c1fd886bf3c7ff5f745052dae327369", read_before)
rpc = checked_patch("verifierLoad", "verifier-load",
                    "92c6f69ef1fb16d0f304ad52255a1b66be5fd3240c15b19aa76c4a36abf1373e", rpc_before)

if revision == "head":
    apply_patch(first, "overlay-source", original, previous)
    apply_patch(read, "overlay-read-bytes-offset", read_before, read_after)
    apply_patch(rpc, "overlay-verifier-load", rpc_before, rpc_after)
    verify_sources("overlay-candidate-source", previous | read_after | rpc_after)
else:
    apply_patch(read, "baseline-read-bytes-tests", {bytes_test: read_before[bytes_test]},
                {bytes_test: read_after[bytes_test]}, include=bytes_test)
    apply_patch(rpc, "baseline-verifier-tests", {rpc_test: original[rpc_test]},
                {rpc_test: baseline_rpc_test}, include=rpc_test)
PYTHON
  sha256sum "$evidence/rpc-context-inline.patch" > "$evidence/rpc-overlay.sha256"
  (cd "$evidence" && sha256sum rpc-context-inline.patch read-bytes-offset.patch verifier-load.patch > patch-sequence.sha256)
}

pr72_rpc_context_add_base_planner_fixture() {
  local source="$1" fixture="$evidence/base-planner-benchmark.go"
  git -C "$repo" show "$head:pkg/lux/luvia/planner_test.go" |
    python3 -c '
from hashlib import sha256
import re
import sys

data = sys.stdin.buffer.read()
pattern = rb"^func BenchmarkPlanSelection\(b \*testing\.B\) \{\n.*?^}\n"
matches = re.findall(pattern, data, re.M | re.S)
if len(matches) != 1 or sha256(matches[0]).hexdigest() != "cc5a523bba9445aa7f999ea520719798c0d713c2f5de4f9d789ea46b08737d8e":
    raise SystemExit("pinned planner benchmark fixture mismatch")
sys.stdout.buffer.write(b"""package luvia

import (
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

""" + matches[0])
' > "$fixture"
  local destination="$source/pkg/lux/luvia/planner_selection_diagnostic_test.go"
  test ! -e "$destination"
  cp "$fixture" "$destination"
  sha256sum "$fixture" > "$evidence/base-planner-benchmark.sha256"
}

pr72_rpc_context_archive() {
  local revision="$1" commit="$2"
  local source="$scratch/source/$revision"
  mkdir -p "$source" "$scratch/bin/$revision"
  git -C "$repo" archive "$commit" | tar -xf - -C "$source"
  pr72_rpc_context_apply_patches "$source" "$overlay_bundle" "$revision"
  if [[ "$revision" == base ]]; then
    pr72_rpc_context_add_base_planner_fixture "$source"
  fi
  git -C "$repo" show -s --format="$revision %H %T" "$commit" >> "$evidence/revisions.txt"
  (
    cd "$source"
    sha256sum go.mod go.sum pkg/lux/codec/{datetime_bench_test.go,datetime.go,json_bench_test.go,decoder.go,selectionmask.go,selectionmask_bench_test.go,wire.go,encoder.go,array_write_test.go,array_write_bench_test.go,finite_float_bench_test.go,bytes_test.go,json.go} \
      pkg/lux/api/{trace_encoding_bench_test.go,binary_payload_bench_test.go,binary_payload_reference_test.go,binary.go} \
      pkg/lux/rpc/{auth_context_bench_test.go,rpc_test.go,server.go} pkg/lux/luvia/{planner.go,planner_test.go}
  ) > "$evidence/sources-$revision.sha256"
  sh "$source/scripts/setup-modules.sh" "$source"
  cp "$source/go.work" "$evidence/go-$revision.work.txt"
}

pr72_rpc_context_verify_benchmark_sources() {
  local revision
  for revision in base head; do
    (
      cd "$scratch/source/$revision"
      sha256sum pkg/lux/codec/{datetime_bench_test.go,json_bench_test.go,selectionmask_bench_test.go,array_write_bench_test.go,finite_float_bench_test.go,bytes_test.go} \
        pkg/lux/rpc/auth_context_bench_test.go \
        pkg/lux/api/{trace_encoding_bench_test.go,binary_payload_bench_test.go,binary_payload_reference_test.go}
    ) > "$evidence/benchmark-fixtures-$revision.sha256"
  done
  cmp "$evidence/benchmark-fixtures-base.sha256" "$evidence/benchmark-fixtures-head.sha256"
}

pr72_rpc_context_run_boundary_tests() {
  local revision test_name
  local read_tests='^TestReadBytes(Boundaries|InvalidOffsets|LongPrefix)$'
  local rpc_test='TestRPCRequestContextKeepsPublishedVerifierResult'
  for revision in base head; do
    "$scratch/bin/benchrun" run "$scratch/source/$revision" "$scratch/bin/$revision" pkg/lux/codec \
      -test.run="$read_tests" -test.v -test.timeout=90s > "$evidence/read-bytes-test-$revision.log"
    for test_name in TestReadBytesBoundaries TestReadBytesInvalidOffsets TestReadBytesLongPrefix; do
      grep -q "^--- PASS: $test_name " "$evidence/read-bytes-test-$revision.log"
    done
    "$scratch/bin/benchrun" run "$scratch/source/$revision" "$scratch/bin/$revision" pkg/lux/rpc \
      -test.run="^$rpc_test$" -test.v -test.timeout=90s > "$evidence/verifier-test-$revision.log"
    grep -q "^--- PASS: $rpc_test " "$evidence/verifier-test-$revision.log"
  done
}

pr72_rpc_context_find_inventory() {
  local revision case_id package variant name filter output
  for revision in base head; do
    while IFS=$'\t' read -r case_id package variant name filter; do
      output="$evidence/inventory-$revision-$case_id.txt"
      "$scratch/bin/benchrun" run "$scratch/source/$revision" "$scratch/bin/$revision" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        > "$output"
      pr72_rpc_context_verify_process "$output" "$case_id"
    done < "$evidence/cases.tsv"
  done
  printf 'Exact 1x inventory passed for all thirty-nine leaves on both revisions.\n'
}

pr72_rpc_context_verify_samples() {
  local group sample comparison case_id side revision variant filename count=0
  while IFS=$'\t' read -r group sample comparison case_id side revision variant filename; do
    if [[ "$group" == group ]]; then continue; fi
    [[ "$case_id" =~ ^(0[1-9]|1[0-5])$ ]] || return 1
    pr72_rpc_context_verify_process "$evidence/$filename" "$case_id" || return 1
    count=$((count+1))
  done < "$evidence/order.tsv"
  [[ "$count" -eq 1200 ]]
}

pr72_rpc_context_require_gates() {
  if ! grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"; then
    printf 'A/A crossed the original threshold. The diagnostic is inconclusive.\n' >&2
    return 1
  fi
  if ! grep -Fxq $'ab\t0' "$evidence/verdicts.tsv"; then
    printf 'A/B crossed the original threshold. The performance comparison failed.\n' >&2
    return 1
  fi
}

pr72_rpc_context_main() {
  local helper=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
  local launch revision package overlay_bundle inventory_only=false
  if [[ $# -eq 1 && "$1" == --inventory-only ]]; then
    inventory_only=true
  elif [[ $# -ne 0 ]]; then
    printf 'Usage: %s [--inventory-only]\n' "$0" >&2
    return 2
  fi
  repo=$(git rev-parse --show-toplevel)
  launch=$(pwd -P)
  overlay_bundle="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)/diagnose-pr72-rpc-context-inline.json"
  mkdir -p "$launch/.tmp"
  test ! -L "$launch/.tmp"
  evidence="$launch/.tmp/hosted-pr72-rpc-context-inline"
  mkdir "$evidence"
  git -C "$repo" show "$helper:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
  printf '%s\n' "$helper" > "$evidence/sampler-revision.txt"
  sha256sum "$evidence/sampler.sh" > "$evidence/sampler.sha256"
  [[ "$(cut -d' ' -f1 "$evidence/sampler.sha256")" == 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 ]]
  source "$evidence/sampler.sh"
  head=f86453bc03d6ef7d0210aeb414c5e9a7dd540ed0
  scratch=$(mktemp -d "$launch/.tmp/.hosted-pr72-rpc-context-inline.XXXXXX")
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
One runner, original default-link compiler flags, exact 65f1892 and f86453b archives.
The original five process contexts from run 45230006287 retain all ten leaves.
A separate process compares the three token-present After paths, preserving no-token process context.
Three valid SplitSelectionMask leaves and two valid Planner leaves cover direct product scope.
Three ArrayHeaderWrite leaves retain late failures observed on multiple runner CPU models.
Five additional controls cover invalid counts, a shared array writer and two real trace encoders.
Three additional process contexts add eight length prefixes, three strict JSON reads and two real API list reads.
The base archive adds the identical head Planner benchmark function in a test-only source file.
The two new boundary-test deltas apply to both archived roles. The existing array and static authentication overlay is unchanged.
Performance groups cover equivalent successful or error outcomes. Malformed selection fixtures are excluded.
A/A uses the same base binary on both sides. A/B uses base and the three verified head patch layers in order.
Each comparison has two groups of ten alternating pairs per exact process scope.
Go benchmark benchmem, 200ms, count=1, CPU=2, test timeout=90s, benchstat alpha=.01.
A/A and A/B must both satisfy the original thresholds. The complete PR retains its independent gates.
PROTOCOL
  pr72_rpc_context_write_plan
  pr72_rpc_context_archive base "$base"
  pr72_rpc_context_archive head "$head"
  pr72_rpc_context_verify_benchmark_sources
  (
    cd "$scratch/source/base"
    for tool in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$tool" "./scripts/$tool"
    done
  ) > "$evidence/compile-tools.log" 2>&1
  for revision in base head; do
    (
      for package in pkg/lux/codec pkg/lux/rpc pkg/lux/luvia pkg/lux/api; do
        "$scratch/bin/benchrun" compile "$scratch/source/$revision" "$scratch/bin/$revision" "$package"
      done
    ) > "$evidence/compile-$revision.log" 2>&1
    record_binaries "$revision"
    pr72_rpc_context_record_layout "$revision"
  done
  "$scratch/bin/benchrun" run "$scratch/source/head" "$scratch/bin/head" pkg/lux/rpc \
    -test.run='^TestRPCReusesStaticAuthenticationErrors$' -test.v -test.timeout=90s \
    > "$evidence/rpc-static-authentication-errors.log"
  grep -q '^--- PASS: TestRPCReusesStaticAuthenticationErrors ' "$evidence/rpc-static-authentication-errors.log"
  "$scratch/bin/benchrun" run "$scratch/source/head" "$scratch/bin/head" pkg/lux/codec \
    -test.run='^Test(ArrayHeaderWriteBounds|ArrayHeaderWriteCapacity|FiniteFloatBenchmarkParity)$' \
    -test.v -test.timeout=90s > "$evidence/array-header-test.log"
  grep -q '^--- PASS: TestArrayHeaderWriteCapacity ' "$evidence/array-header-test.log"
  pr72_rpc_context_run_boundary_tests
  pr72_rpc_context_find_inventory
  if "$inventory_only"; then return 0; fi

  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr72_rpc_context_verify_samples
  analyze_groups
  pr72_rpc_context_require_gates
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  pr72_rpc_context_main "$@"
fi
