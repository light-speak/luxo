#!/usr/bin/env bash
set -euo pipefail

pr73_write_plan() {
  cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/codegen	default	BenchmarkRelationResolverEmission	^BenchmarkRelationResolverEmission$
02	pkg/codegen	default	BenchmarkNumericRangeValidation	^BenchmarkNumericRangeValidation$
CASES
  printf 'BenchmarkRelationResolverEmission/%s-2\n' nullable-single required-list leaf-list nullable-list > "$evidence/01.names"
  printf 'BenchmarkNumericRangeValidation/%s-2\n' none positive signed_int signed_float > "$evidence/02.names"
  python3 - "$evidence" <<'PYTHON'
import json
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
names = (evidence / "01.names").read_text() + (evidence / "02.names").read_text()
rows = [{"Package": "pkg/codegen", "Name": name} for name in names.splitlines()]
(evidence / "expected.json").write_text(json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n")
PYTHON
}

pr73_verify_process() {
  local raw="$1" case_id="$2"
  awk -v expected="$evidence/$case_id.names" -v os="$probe_goos" -v arch="$probe_goarch" '
    BEGIN {
      while (getline name < expected) names[++count] = name
      if (count != 4) exit 1
    }
    /^goos: / { os_count++; if ($2 != os) bad=1; next }
    /^goarch: / { arch_count++; if ($2 != arch) bad=1; next }
    /^pkg: / { pkg_count++; if ($2 != "github.com/light-speak/luxo/pkg/codegen") bad=1; next }
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

pr73_record_layout() {
  local revision="$1" binary="$scratch/bin/$1/pkg-codegen.test"
  local pattern='[.](generateRelationResolver|writeSingleRelationResult|writeRelationDepthCheck|writeNestedRelationResolve|generateNumericValidation|numericValidationBound|writeValidationCheck|compileNumericLiteral|generateQueryFacade|generatePreparedQueryUpdate|writeQueryFacadeParts|Benchmark(RelationResolverEmission|NumericRangeValidation)([.]func1)?)$'
  go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$revision.nm"
  for name in generateRelationResolver generateNumericValidation BenchmarkRelationResolverEmission.func1 BenchmarkNumericRangeValidation.func1; do
    grep -Fq ".$name" "$evidence/symbols-$revision.nm"
  done
  python3 - "$evidence/symbols-$revision.nm" <<'PYTHON' > "$evidence/symbols-$revision.tsv"
from pathlib import Path
import sys

print("symbol\taddress\tbytes\tmod64")
for row in Path(sys.argv[1]).read_text().splitlines():
    address, size, _, symbol = row.split()
    print(f"{symbol}\t{address}\t{size}\t{int(address, 16) % 64}")
PYTHON
  go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$revision.objdump"
  test -s "$evidence/symbols-$revision.objdump"
}

pr73_archive() {
  local revision="$1" commit="$2"
  local source="$scratch/source/$revision"
  mkdir -p "$source" "$scratch/bin/$revision"
  git -C "$repo" archive "$commit" | tar -xf - -C "$source"
  git -C "$repo" show -s --format="$revision %H %T" "$commit" >> "$evidence/revisions.txt"
  (
    cd "$source"
    sha256sum go.mod go.sum pkg/codegen/{dataloader_nullable_bench_test.go,relation_resolver.go,numeric_range_bench_test.go,handler.go,query_facade.go}
  ) > "$evidence/sources-$revision.sha256"
  sh "$source/scripts/setup-modules.sh" "$source"
  cp "$source/go.work" "$evidence/go-$revision.work.txt"
}

pr73_find_inventory() {
  local revision case_id package variant name filter output
  for revision in base head; do
    while IFS=$'\t' read -r case_id package variant name filter; do
      output="$evidence/inventory-$revision-$case_id.txt"
      "$scratch/bin/benchrun" run "$scratch/source/$revision" "$scratch/bin/$revision" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        > "$output"
      pr73_verify_process "$output" "$case_id"
    done < "$evidence/cases.tsv"
  done
  printf 'Exact 1x inventory passed for both families on both revisions.\n'
}

pr73_verify_samples() {
  local group sample comparison case_id side revision variant filename count=0
  while IFS=$'\t' read -r group sample comparison case_id side revision variant filename; do
    if [[ "$group" == group ]]; then continue; fi
    [[ "$case_id" == 01 || "$case_id" == 02 ]]
    pr73_verify_process "$evidence/$filename" "$case_id"
    count=$((count+1))
  done < "$evidence/order.tsv"
  [[ "$count" -eq 160 ]]
}

pr73_main() {
  local helper=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
  local launch revision inventory_only=false
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
  evidence="$launch/.tmp/hosted-pr73-codegen-layout"
  mkdir "$evidence"
  git -C "$repo" show "$helper:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
  printf '%s\n' "$helper" > "$evidence/sampler-revision.txt"
  sha256sum "$evidence/sampler.sh" > "$evidence/sampler.sha256"
  [[ "$(cut -d' ' -f1 "$evidence/sampler.sha256")" == 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 ]]
  source "$evidence/sampler.sh"
  head=a7525317461ff20a774c28d49df1dc1bc87efb28
  scratch=$(mktemp -d "$launch/.tmp/.hosted-pr73-codegen.XXXXXX")
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
Each process runs the four leaves in one family in the benchmark's original order.
A/A uses the base binary on both sides. A/B uses the base and head binaries.
Each comparison has two groups of 10 alternating pairs per family.
Go benchmark benchmem, 200ms, count=1, CPU=2, test timeout=90s, benchstat alpha=.01.
The original two-group time and allocation thresholds apply. A failed A/A control makes the result inconclusive.
The result reports compiler microbenchmarks. The PR retains its production gates.
PROTOCOL
  pr73_write_plan
  pr73_archive base "$base"
  pr73_archive head "$head"
  (
    cd "$scratch/source/base"
    for tool in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$tool" "./scripts/$tool"
    done
  ) > "$evidence/compile-tools.log" 2>&1
  for revision in base head; do
    "$scratch/bin/benchrun" compile "$scratch/source/$revision" "$scratch/bin/$revision" pkg/codegen \
      > "$evidence/compile-$revision.log" 2>&1
    record_binaries "$revision"
    pr73_record_layout "$revision"
  done
  pr73_find_inventory
  if "$inventory_only"; then return 0; fi

  GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
  go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr73_verify_samples
  analyze_groups
  if ! grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"; then
    printf 'A/A crossed the original threshold. The diagnostic is inconclusive.\n' >&2
    return 1
  fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  pr73_main "$@"
fi
