#!/usr/bin/env bash
set -euo pipefail

base=65f1892e175487503aa36caa86d1eb8a0805f073
pr73=d8b686b516ed5ab89e185093ca836756901fb56c
benchstat_version=v0.0.0-20260825160852-19be9d8e6c70
benchtime=200ms

write_cases() {
  cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/codegen	BenchmarkQueryFacadeSource/R/1-2	^BenchmarkQueryFacadeSource$/^R$/^1$
02	pkg/codegen	BenchmarkQueryFacadeSource/User/1-2	^BenchmarkQueryFacadeSource$/^User$/^1$
CASES
  cat > "$evidence/comparisons.tsv" <<'COMPARISONS'
default-aa	base	base
default-ab	base	pr73
align64-aa	base-align64	base-align64
align64-ab	base-align64	pr73-align64
COMPARISONS
  python3 - "$evidence" <<'PYTHON'
import json
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
rows = []
for line in (evidence / "cases.tsv").read_text().splitlines():
    _, package, name, _ = line.split("\t")
    rows.append({"Package": package, "Name": name})
(evidence / "expected.json").write_text(json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n")
PYTHON
}

compile_revision() {
  local revision="$1" commit="$2"
  mkdir -p "$scratch/source/$revision" "$scratch/bin/$revision"
  git -C "$repo" archive "$commit" | tar -xf - -C "$scratch/source/$revision"
  git -C "$repo" show -s --format="$revision %H %T" "$commit" >> "$evidence/revisions.txt"
  (
    set -x
    sh "$scratch/source/base/scripts/setup-modules.sh" "$scratch/source/$revision"
    if [[ "$revision" == base ]]; then
      cd "$scratch/source/base"
      go build -o "$scratch/bin/benchrun" ./scripts/benchrun
      go build -o "$scratch/bin/benchplan" ./scripts/benchplan
      go build -o "$scratch/bin/benchgate" ./scripts/benchgate
      GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
      go version -m "$scratch/bin/benchstat"
    fi
    "$scratch/bin/benchrun" compile "$scratch/source/$revision" "$scratch/bin/$revision" pkg/codegen
  ) >> "$evidence/compile-$revision.log" 2>&1
  cp "$scratch/source/$revision/go.work" "$evidence/go-$revision.work.txt"
  record_binary "$revision"
}

compile_aligned() {
  local revision="$1"
  (
    set -x
    GOFLAGS=-ldflags=-funcalign=64 "$scratch/bin/benchrun" compile \
      "$scratch/source/$revision" "$scratch/bin/$revision-align64" pkg/codegen
  ) > "$evidence/compile-$revision-align64.log" 2>&1
  record_binary "$revision-align64"
}

record_binary() {
  local revision="$1" binary pattern
  binary="$scratch/bin/$revision/pkg-codegen.test"
  {
    sha256sum "$binary"
    go version -m "$binary"
    go tool buildid "$binary"
  } > "$evidence/binaries-$revision.txt"
  pattern='[.](generateQueryFacade|generatePreparedQueryUpdate|BenchmarkQueryFacadeSource([.]func1)?)$'
  go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$revision.nm"
  go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$revision.objdump"
  python3 - "$evidence" "$revision" <<'PYTHON' > "$evidence/symbols-$revision.tsv"
from hashlib import sha256
from pathlib import Path
import sys

evidence, revision = Path(sys.argv[1]), sys.argv[2]
instructions = {}
for line in (evidence / f"symbols-{revision}.objdump").read_text().splitlines():
    fields = line.split()
    if not fields:
        continue
    if fields[0] == "TEXT":
        symbol = fields[1].removesuffix("(SB)")
        instructions[symbol] = bytearray()
    else:
        instructions[symbol].extend(bytes.fromhex(fields[2]))
required = {"generateQueryFacade", "generatePreparedQueryUpdate"}
print("symbol\taddress\tmod64\tsize\tobjdump_encoding_sha256")
for line in (evidence / f"symbols-{revision}.nm").read_text().splitlines():
    address, size, _, symbol = line.split()
    offset = int(address, 16) % 64
    name = symbol.rsplit(".", 1)[-1]
    encoding = instructions[symbol]
    if not encoding:
        raise SystemExit(f"{revision}: empty instruction encoding for {symbol}")
    if name in required and revision.endswith("-align64") and offset != 0:
        raise SystemExit(f"{revision}: {symbol} misaligned at 0x{address}")
    required.discard(name)
    print(f"{symbol}\t0x{address}\t{offset}\t{size}\t{sha256(encoding).hexdigest()}")
if required:
    raise SystemExit(f"{revision}: missing symbols {sorted(required)}")
PYTHON
}

run_leg() {
  local sample="$1" comparison="$2" side="$3" revision="$4"
  local case_id="$5" package="$6" filter="$7" filename
  filename="raw/$sample.$comparison.$case_id.$side.txt"
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$sample" "$comparison" "$case_id" "$side" "$revision" "$filename" >> "$evidence/order.tsv"
  "$scratch/bin/benchrun" run "$scratch/source/${revision%-align64}" "$scratch/bin/$revision" "$package" \
    -test.run='^$' -test.bench="$filter" -test.benchmem \
    -test.benchtime="$benchtime" -test.count=1 -test.cpu=2 -test.timeout=90s \
    > "$evidence/$filename" 2>> "$evidence/benchmark-stderr.txt"
}

sample_groups() {
  local sample case_id package name filter comparison left right entry offset index
  local -a comparisons
  comparisons=()
  while IFS= read -r entry; do
    comparisons+=("$entry")
  done < "$evidence/comparisons.tsv"
  mkdir "$evidence/raw"
  printf 'pair\tcomparison\tcase\tside\trevision\traw\n' > "$evidence/order.tsv"
  for sample in {1..10}; do
    printf -v sample '%02d' "$sample"
    while IFS=$'\t' read -r case_id package name filter; do
      for ((offset=0; offset<${#comparisons[@]}; offset++)); do
        index=$(( (10#$sample + 10#$case_id - 2 + offset) % ${#comparisons[@]} ))
        IFS=$'\t' read -r comparison left right <<< "${comparisons[index]}"
        if (( (10#$sample + 10#$case_id) % 2 == 0 )); then
          run_leg "$sample" "$comparison" left "$left" "$case_id" "$package" "$filter"
          run_leg "$sample" "$comparison" right "$right" "$case_id" "$package" "$filter"
        else
          run_leg "$sample" "$comparison" right "$right" "$case_id" "$package" "$filter"
          run_leg "$sample" "$comparison" left "$left" "$case_id" "$package" "$filter"
        fi
      done
    done < "$evidence/cases.tsv"
  done
}

normalize_group() {
  local comparison="$1" side
  local stem="$evidence/$comparison"
  for side in left right; do
    cat "$evidence"/raw/*."$comparison".*."$side".txt > "$scratch/combined.txt"
    "$scratch/bin/benchplan" --normalize "$scratch/combined.txt" "$stem.$side.txt"
  done
  "$scratch/bin/benchplan" --verify "$evidence/expected.json" "$stem.left.txt" "$stem.right.txt"
  "$scratch/bin/benchstat" -alpha 0.01 -filter '.unit:(sec/op OR B/op OR allocs/op)' -format csv \
    base="$stem.left.txt" head="$stem.right.txt" > "$stem.csv" 2> "$stem.warnings.txt"
  "$scratch/bin/benchgate" --requires-confirmation "$stem.csv" > "$stem.requires-confirmation.txt"
}

analyze_groups() {
  local comparison left right flagged
  printf 'comparison\twould_require_confirmation\n' > "$evidence/diagnostic-flags.tsv"
  while IFS=$'\t' read -r comparison left right; do
    normalize_group "$comparison"
    flagged=$(cat "$evidence/$comparison.requires-confirmation.txt")
    printf '%s\t%s\n' "$comparison" "$flagged" >> "$evidence/diagnostic-flags.tsv"
  done < "$evidence/comparisons.tsv"
  cat "$evidence/diagnostic-flags.tsv"
}

collect_host() {
  uname -a
  lscpu
  go version
  go env -json GOOS GOARCH GOAMD64 CGO_ENABLED GOEXPERIMENT GOFLAGS CC
  printf 'image=%s image_version=%s runner_arch=%s\n' "${ImageOS:-}" "${ImageVersion:-}" "${RUNNER_ARCH:-}"
  printf 'workflow_revision=%s run=%s attempt=%s\n' "${GITHUB_SHA:-}" "${GITHUB_RUN_ID:-}" "${GITHUB_RUN_ATTEMPT:-}"
  if [[ -r /sys/fs/cgroup/cpu.max ]]; then
    cat /sys/fs/cgroup/cpu.max
  fi
  date -u '+%Y-%m-%dT%H:%M:%SZ'
}

cleanup() {
  rm -rf -- "$scratch"
}

main() {
  local revision
  repo=$(git rev-parse --show-toplevel)
  evidence="$repo/.tmp/hosted-perf-evidence"
  mkdir -p "$repo/.tmp"
  mkdir "$evidence"
  scratch=$(mktemp -d "$repo/.tmp/hosted-perf.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  export TMPDIR="$scratch/tmp" GOWORK=off
  mkdir "$TMPDIR"
  [[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]]
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64)" == $'linux\namd64\nv1' ]]
  collect_host > "$evidence/environment.txt"
  [[ -z "$(go env GOFLAGS)" ]]
  for revision in "$base" "$pr73"; do
    [[ "$(git -C "$repo" cat-file -t "$revision")" == commit ]]
  done
  git -C "$repo" merge-base --is-ancestor "$base" "$pr73"
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
Same runner, default and align64 configurations each have one A/A and one A/B group of ten alternating pairs per exact case.
A/A uses the same baseline binary on both legs. A/B compares the pinned revisions with a matched link setting.
align64 applies -ldflags=-funcalign=64 to both revisions. This changes other function addresses as well.
benchmem, 200ms, count=1, cpu=2, test timeout=90s, benchstat alpha=0.01.
The diagnostic flags show whether one group would require confirmation at the original threshold. There is no confirmation or aggregate gate here.
This experiment does not replace the PR's default-binary performance checks. A missing case or invalid sample fails this workflow.
PROTOCOL
  write_cases
  compile_revision base "$base"
  compile_revision pr73 "$pr73"
  compile_aligned base
  compile_aligned pr73
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  analyze_groups
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
