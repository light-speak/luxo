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
CASES
  printf '%s\n' BenchmarkDateTimePair/epoch/pair_read-2 > "$evidence/01.names"
  printf 'BenchmarkDateTimePair/current/%s-2\n' pair_read pair_write > "$evidence/02.names"
  printf '%s\n' BenchmarkDateTimePair/year_9999/pair_write-2 > "$evidence/03.names"
  printf 'BenchmarkRawJSONBoundary/%s/read/strict=false-2\n' small precision ascii72KiB unicode72KiB surrogate128 > "$evidence/04.names"
  printf '%s\n' BenchmarkRPCNilContextGuard/NoToken/After-2 > "$evidence/05.names"
  printf 'BenchmarkRPCNilContextGuard/%s/After-2\n' Verified Rejected Unconfigured > "$evidence/06.names"
  printf 'BenchmarkSelectionMaskLimits/%s/after-2\n' empty small 64-children > "$evidence/07.names"
  printf 'BenchmarkPlanSelection/%s-2\n' all nested > "$evidence/08.names"
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
if len(rows) != 18 or len({(r["Package"], r["Name"]) for r in rows}) != 18:
    raise SystemExit("expected exactly eighteen distinct leaves")
(evidence / "expected.json").write_text(json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n")
PYTHON
}

pr72_rpc_context_verify_process() {
  local raw="$1" case_id="$2" package
  case "$case_id" in 01|02|03|04|07) package=pkg/lux/codec ;; 05|06) package=pkg/lux/rpc ;; 08) package=pkg/lux/luvia ;; *) return 1 ;; esac
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
  local pattern='[.](ReadDateTime|AppendDateTime|ReadSvarint|AppendSvarint|ReadBytes|Finish|requestContext|verifyRequestContext|SplitSelectionMask|Plan|benchmarkJSONRead|Benchmark(DateTimePair|RawJSONBoundary|RPCNilContextGuard|SelectionMaskLimits|PlanSelection).*)$'
  for package in codec rpc luvia; do
    binary="$scratch/bin/$revision/pkg-lux-$package.test"
    go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$revision-$package.nm"
    test -s "$evidence/symbols-$revision-$package.nm"
    go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$revision-$package.objdump"
    test -s "$evidence/symbols-$revision-$package.objdump"
  done
}

pr72_rpc_context_apply_patch() {
  local source="$1" bundle="$2"
  local patch="$evidence/rpc-context-inline.patch"
  python3 - "$bundle" "$patch" <<'PYTHON'
from hashlib import sha256
import json
from pathlib import Path
import re
import sys

bundle, output = map(Path, sys.argv[1:])
patch = json.loads(bundle.read_text())["patch"].encode()
if sha256(patch).hexdigest() != "efed5ccafe419eb80b02df999e95afb105b7309ce3935b4c8c687ae50b9b1f02":
    raise SystemExit("frozen RPC context overlay SHA-256 mismatch")
paths = re.findall(rb"^diff --git a/(\S+) b/(\S+)$", patch, re.M)
if paths != [(b"pkg/lux/rpc/server.go", b"pkg/lux/rpc/server.go")]:
    raise SystemExit("RPC context overlay scope mismatch")
output.write_bytes(patch)
PYTHON
  (
    cd "$source"
    export GIT_CEILING_DIRECTORIES="${source%/*}"
    printf '%s  %s\n' 313870a1f66bbde4e83bfcd88841ccaf70135855ddd5f8d80d308b968736da0a pkg/lux/rpc/server.go |
      sha256sum -c -
    sha256sum pkg/lux/rpc/server.go > "$evidence/rpc-source-before.sha256"
    git apply --check --whitespace=error "$patch"
    git apply --whitespace=error "$patch"
    git apply -R --check "$patch"
    printf '%s  %s\n' 6074fa78bf3ab9e31615a73b02d007608fe472fd6b99fd80d764b424ec460747 pkg/lux/rpc/server.go |
      sha256sum -c -
    sha256sum pkg/lux/rpc/server.go > "$evidence/rpc-source-after.sha256"
  )
  sha256sum "$patch" > "$evidence/rpc-overlay.sha256"
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
  if [[ "$revision" == head ]]; then
    pr72_rpc_context_apply_patch "$source" "$overlay_bundle"
  else
    pr72_rpc_context_add_base_planner_fixture "$source"
  fi
  git -C "$repo" show -s --format="$revision %H %T" "$commit" >> "$evidence/revisions.txt"
  (
    cd "$source"
    sha256sum go.mod go.sum pkg/lux/codec/{datetime_bench_test.go,datetime.go,json_bench_test.go,decoder.go,selectionmask.go,selectionmask_bench_test.go} \
      pkg/lux/rpc/{auth_context_bench_test.go,server.go} pkg/lux/luvia/{planner.go,planner_test.go}
  ) > "$evidence/sources-$revision.sha256"
  sh "$source/scripts/setup-modules.sh" "$source"
  cp "$source/go.work" "$evidence/go-$revision.work.txt"
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
  printf 'Exact 1x inventory passed for all eighteen leaves on both revisions.\n'
}

pr72_rpc_context_verify_samples() {
  local group sample comparison case_id side revision variant filename count=0
  while IFS=$'\t' read -r group sample comparison case_id side revision variant filename; do
    if [[ "$group" == group ]]; then continue; fi
    [[ "$case_id" =~ ^0[1-8]$ ]] || return 1
    pr72_rpc_context_verify_process "$evidence/$filename" "$case_id" || return 1
    count=$((count+1))
  done < "$evidence/order.tsv"
  [[ "$count" -eq 640 ]]
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
The base archive adds the identical head Planner benchmark function in a test-only source file.
Performance groups cover equivalent successful or error outcomes. Malformed selection fixtures are excluded.
A/A uses the same base binary on both sides. A/B uses base and the verified one-file head overlay.
Each comparison has two groups of ten alternating pairs per exact process scope.
Go benchmark benchmem, 200ms, count=1, CPU=2, test timeout=90s, benchstat alpha=.01.
A/A and A/B must both satisfy the original thresholds. The complete PR retains its independent gates.
PROTOCOL
  pr72_rpc_context_write_plan
  pr72_rpc_context_archive base "$base"
  pr72_rpc_context_archive head "$head"
  (
    cd "$scratch/source/base"
    for tool in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$tool" "./scripts/$tool"
    done
  ) > "$evidence/compile-tools.log" 2>&1
  for revision in base head; do
    (
      for package in pkg/lux/codec pkg/lux/rpc pkg/lux/luvia; do
        "$scratch/bin/benchrun" compile "$scratch/source/$revision" "$scratch/bin/$revision" "$package"
      done
    ) > "$evidence/compile-$revision.log" 2>&1
    record_binaries "$revision"
    pr72_rpc_context_record_layout "$revision"
  done
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
