#!/usr/bin/env bash
set -euo pipefail

pr72_shard11_write_plan() {
  cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/lux/codec	default	DateTimePair-epoch	^BenchmarkDateTimePair$/^epoch$/^pair_read$
02	pkg/lux/codec	default	DateTimePair-current	^BenchmarkDateTimePair$/^current$/^pair_(read|write)$
03	pkg/lux/codec	default	DateTimePair-year9999	^BenchmarkDateTimePair$/^year_9999$/^pair_write$
04	pkg/lux/codec	default	RawJSONBoundary	^BenchmarkRawJSONBoundary$/^(small|precision|ascii72KiB|unicode72KiB|surrogate128)$/^read$/^strict=false$
05	pkg/lux/rpc	default	RPCNilContextGuard	^BenchmarkRPCNilContextGuard$/^NoToken$/^After$
CASES
  printf '%s\n' BenchmarkDateTimePair/epoch/pair_read-2 > "$evidence/01.names"
  printf 'BenchmarkDateTimePair/current/%s-2\n' pair_read pair_write > "$evidence/02.names"
  printf '%s\n' BenchmarkDateTimePair/year_9999/pair_write-2 > "$evidence/03.names"
  printf 'BenchmarkRawJSONBoundary/%s/read/strict=false-2\n' small precision ascii72KiB unicode72KiB surrogate128 > "$evidence/04.names"
  printf '%s\n' BenchmarkRPCNilContextGuard/NoToken/After-2 > "$evidence/05.names"
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
if len(rows) != 10 or len({(r["Package"], r["Name"]) for r in rows}) != 10:
    raise SystemExit("expected exactly ten distinct leaves")
(evidence / "expected.json").write_text(json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n")
PYTHON
}

pr72_shard11_verify_process() {
  local raw="$1" case_id="$2" package
  case "$case_id" in 01|02|03|04) package=pkg/lux/codec ;; 05) package=pkg/lux/rpc ;; *) return 1 ;; esac
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

pr72_shard11_record_layout() {
  local revision="$1" package binary
  local pattern='[.](ReadDateTime|AppendDateTime|ReadSvarint|AppendSvarint|ReadBytes|Finish|requestContext|benchmarkJSONRead|Benchmark(DateTimePair|RawJSONBoundary|RPCNilContextGuard).*)$'
  for package in codec rpc; do
    binary="$scratch/bin/$revision/pkg-lux-$package.test"
    go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$revision-$package.nm"
    test -s "$evidence/symbols-$revision-$package.nm"
    go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$revision-$package.objdump"
    test -s "$evidence/symbols-$revision-$package.objdump"
  done
}

pr72_shard11_archive() {
  local revision="$1" commit="$2"
  local source="$scratch/source/$revision"
  mkdir -p "$source" "$scratch/bin/$revision"
  git -C "$repo" archive "$commit" | tar -xf - -C "$source"
  git -C "$repo" show -s --format="$revision %H %T" "$commit" >> "$evidence/revisions.txt"
  (
    cd "$source"
    sha256sum go.mod go.sum pkg/lux/codec/{datetime_bench_test.go,datetime.go,json_bench_test.go,decoder.go,selectionmask.go} pkg/lux/rpc/{auth_context_bench_test.go,server.go} pkg/lux/luvia/planner.go
  ) > "$evidence/sources-$revision.sha256"
  sh "$source/scripts/setup-modules.sh" "$source"
  cp "$source/go.work" "$evidence/go-$revision.work.txt"
}

pr72_shard11_find_inventory() {
  local revision case_id package variant name filter output
  for revision in base head; do
    while IFS=$'\t' read -r case_id package variant name filter; do
      output="$evidence/inventory-$revision-$case_id.txt"
      "$scratch/bin/benchrun" run "$scratch/source/$revision" "$scratch/bin/$revision" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        > "$output"
      pr72_shard11_verify_process "$output" "$case_id"
    done < "$evidence/cases.tsv"
  done
  printf 'Exact 1x inventory passed for all ten leaves on both revisions.\n'
}

pr72_shard11_verify_samples() {
  local group sample comparison case_id side revision variant filename count=0
  while IFS=$'\t' read -r group sample comparison case_id side revision variant filename; do
    if [[ "$group" == group ]]; then continue; fi
    [[ "$case_id" =~ ^0[1-5]$ ]]
    pr72_shard11_verify_process "$evidence/$filename" "$case_id"
    count=$((count+1))
  done < "$evidence/order.tsv"
  [[ "$count" -eq 400 ]]
}

pr72_shard11_main() {
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
  evidence="$launch/.tmp/hosted-pr72-shard11-attempt2"
  mkdir "$evidence"
  git -C "$repo" show "$helper:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
  printf '%s\n' "$helper" > "$evidence/sampler-revision.txt"
  sha256sum "$evidence/sampler.sh" > "$evidence/sampler.sha256"
  [[ "$(cut -d' ' -f1 "$evidence/sampler.sha256")" == 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 ]]
  source "$evidence/sampler.sh"
  head=f86453bc03d6ef7d0210aeb414c5e9a7dd540ed0
  scratch=$(mktemp -d "$launch/.tmp/.hosted-pr72-shard11.XXXXXX")
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
One runner, original default-link compiler flags, exact base and head archive.
Five exact processes select only the ten attempt-2 shard-11 failures, in original order.
Original evidence: run 45157471703 attempt 2, job 137849786979, artifact 14844951614.
This is targeted fresh-process sampling, not a replay of the full original shard context (236 base / 238 head expected leaves).
A/A uses the base binary on both sides. A/B uses the base and head binaries.
Each comparison has two groups of 10 alternating pairs per exact process scope.
Go benchmark benchmem, 200ms, count=1, CPU=2, test timeout=90s, benchstat alpha=.01.
The original two-group time and allocation thresholds apply. A failed A/A control makes the result inconclusive.
The result reports codec and RPC microbenchmarks. The PR retains its production gates.
PROTOCOL
  pr72_shard11_write_plan
  pr72_shard11_archive base "$base"
  pr72_shard11_archive head "$head"
  (
    cd "$scratch/source/base"
    for tool in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$tool" "./scripts/$tool"
    done
  ) > "$evidence/compile-tools.log" 2>&1
  for revision in base head; do
    (
      for package in pkg/lux/codec pkg/lux/rpc; do
        "$scratch/bin/benchrun" compile "$scratch/source/$revision" "$scratch/bin/$revision" "$package"
      done
    ) > "$evidence/compile-$revision.log" 2>&1
    record_binaries "$revision"
    pr72_shard11_record_layout "$revision"
  done
  pr72_shard11_find_inventory
  if "$inventory_only"; then return 0; fi

  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr72_shard11_verify_samples
  analyze_groups
  if ! grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"; then
    printf 'A/A crossed the original threshold. The diagnostic is inconclusive.\n' >&2
    return 1
  fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  pr72_shard11_main "$@"
fi
