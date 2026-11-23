#!/usr/bin/env bash
set -euo pipefail

pr72_read_offset_write_scope() {
  local case_id
  (
    evidence="$scratch/original-scope"
    mkdir "$evidence"
    pr72_rpc_context_write_plan
  )
  awk -F '\t' '$1 == "04" || $1 == "13" || $1 == "14" || $1 == "15"' \
    "$scratch/original-scope/cases.tsv" > "$evidence/cases.tsv"
  for case_id in 04 13 14 15; do
    cp "$scratch/original-scope/$case_id.names" "$evidence/$case_id.names"
  done
  python3 - "$scratch/original-scope/expected.json" "$evidence" <<'PYTHON'
import json
from pathlib import Path
import sys

original = json.loads(Path(sys.argv[1]).read_text())
evidence = Path(sys.argv[2])
selected = set()
for row in (evidence / "cases.tsv").read_text().splitlines():
    case, package, _, _, _ = row.split("\t")
    selected.update((package, name) for name in (evidence / f"{case}.names").read_text().splitlines())
filtered = {role: [row for row in rows if (row["Package"], row["Name"]) in selected]
            for role, rows in original.items()}
if len(selected) != 18 or len(filtered["Base"]) != 18 or filtered["Base"] != filtered["Head"]:
    raise SystemExit("expected eighteen distinct leaves and identical expectations")
(evidence / "expected.json").write_text(json.dumps(filtered, indent=2) + "\n")
PYTHON
  (
    cd "$evidence"
    printf '%s  %s\n' \
      120386cff865dc374bd9199bdc0c483a2a19713fa5a29779d0dfc7d9b646d840 cases.tsv \
      393b303e020d6ebfe28d474f70d6702b5099325030c24bb7c729ea50f5c0645d expected.json > scope.sha256
    sha256sum -c scope.sha256
  )
}

pr72_read_offset_toggle() {
  local role="$1"
  shift
  (
    cd "$scratch/source/$role"
    export GIT_CEILING_DIRECTORIES="$scratch/source"
    git apply --whitespace=error --include=pkg/lux/codec/wire.go "$@" --check "$evidence/read-bytes-offset.patch"
    git apply --whitespace=error --include=pkg/lux/codec/wire.go "$@" "$evidence/read-bytes-offset.patch"
  )
}

pr72_read_offset_archive_roles() {
  local role commit tree source wire_digest
  local wire=pkg/lux/codec/wire.go
  mkdir -p "$scratch/source/a" "$scratch/source/d"
  git -C "$repo" archive "$base" | tar -xf - -C "$scratch/source/a"
  git -C "$repo" archive "$head" | tar -xf - -C "$scratch/source/d"
  pr72_rpc_context_apply_patches "$scratch/source/a" "$evidence/bundle.json" base
  pr72_rpc_context_add_base_planner_fixture "$scratch/source/a"
  pr72_rpc_context_apply_patches "$scratch/source/d" "$evidence/bundle.json" head
  cp -R "$scratch/source/a" "$scratch/source/b"
  cp -R "$scratch/source/d" "$scratch/source/c"
  pr72_read_offset_toggle b
  pr72_read_offset_toggle c --reverse

  printf 'role\tarchive_commit\tarchive_tree\twire_sha256\n' > "$evidence/roles.tsv"
  while read -r role commit tree wire_digest; do
    source="$scratch/source/$role"
    [[ "$(git -C "$repo" show -s --format=%T "$commit")" == "$tree" ]]
    (
      cd "$source"
      printf '%s  %s\n' "$wire_digest" "$wire" | sha256sum -c -
      sha256sum go.mod go.sum \
        pkg/lux/codec/{array_write_test.go,wire.go,bytes_test.go,json_bench_test.go,decoder.go,json.go} \
        pkg/lux/api/{binary.go,binary_payload_bench_test.go,binary_payload_reference_test.go} \
        pkg/lux/jsonutil/jsonutil.go pkg/lux/luvia/planner_test.go \
        pkg/lux/rpc/{server.go,rpc_test.go} scripts/{setup-modules.sh,benchgate/approved-costs.json} \
        > "$evidence/sources-$role.sha256"
      sha256sum pkg/lux/codec/{bytes_test.go,json_bench_test.go} \
        pkg/lux/api/{binary_payload_bench_test.go,binary_payload_reference_test.go} \
        > "$evidence/fixtures-$role.sha256"
    )
    printf '%s\t%s\t%s\t%s\n' "$role" "$commit" "$tree" "$wire_digest" >> "$evidence/roles.tsv"
  done <<'ROLES'
a 65f1892e175487503aa36caa86d1eb8a0805f073 7a051ccbdd2ddbf9c341fd7906cd3c5f3e88dc44 cac2dd90bdf4e61ebd4c83cbe5df5bfc31e10df9e0a5f7a2b8e9d027cff8562c
b 65f1892e175487503aa36caa86d1eb8a0805f073 7a051ccbdd2ddbf9c341fd7906cd3c5f3e88dc44 ece4902ab6443aac69c3ae33d6fb36e3fb02f2b576a84580e6011417ba707e40
c f86453bc03d6ef7d0210aeb414c5e9a7dd540ed0 6daf4f4a3b81a507be32b895731f6ba96776524a c8cab050daf5809e968b196888fed1f459fb9575392b44e08faa430ed88c9c54
d f86453bc03d6ef7d0210aeb414c5e9a7dd540ed0 6daf4f4a3b81a507be32b895731f6ba96776524a 46753adeddd4d056128d4f26ddbfdb3b32a558bf4e36967aa7898cd1834567d1
ROLES
  for role in b c d; do
    cmp "$evidence/fixtures-a.sha256" "$evidence/fixtures-$role.sha256"
  done
  (
    cd "$scratch/source/a"
    printf '%s  %s\n' \
      0228ad8f89907ac82d204724e61692047ebb35bb32743bf17db4935b37905d65 \
      pkg/lux/luvia/planner_selection_diagnostic_test.go | sha256sum -c -
  )
}

pr72_read_offset_record_symbols() {
  local role="$1" package="$2" binary="$3"
  local stem="$evidence/symbols-$role-$package"
  local product='^github\.com/light-speak/luxo/pkg/lux/(codec|api)\..*(ReadVarint|ReadBytes|ReadJSON|Finish|benchmarkJSONRead|readBinaryListParam|readBinaryBytesArray|readBinaryJSONArray|Benchmark(ReadBytesPrefix|RawJSONBoundary|BinaryListPayload).*)$'
  local validator='^github\.com/light-speak/luxo/pkg/lux/jsonutil\.(ValidUTF8JSON|WithinJSONDepth|jsonStringEnd)$|^encoding/json/(jsontext|internal/jsonwire)\.'
  local pattern="$product|$validator"
  go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$stem.nm"
  go tool objdump -s "$pattern" "$binary" > "$stem.objdump"
  grep -Eq ' github\.com/light-speak/luxo/pkg/lux/codec\.ReadBytes$' "$stem.nm"
  grep -Eq ' (encoding/json/jsontext\.|github\.com/light-speak/luxo/pkg/lux/jsonutil\.ValidUTF8JSON$)' "$stem.nm"
  test -s "$stem.objdump"
}

pr72_read_offset_compile() {
  local role source package case_id variant name filter output
  for role in a b c d; do
    source="$scratch/source/$role"
    sh "$source/scripts/setup-modules.sh" "$source"
    cp "$source/go.work" "$evidence/go-$role.work.txt"
  done
  (
    cd "$scratch/source/a"
    for package in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$package" "./scripts/$package"
    done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  ) > "$evidence/compile-tools.log" 2>&1
  for role in a b c d; do
    mkdir "$scratch/bin/$role"
    (
      for package in codec api; do
        "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" "pkg/lux/$package"
        pr72_read_offset_record_symbols "$role" "$package" "$scratch/bin/$role/pkg-lux-$package.test"
      done
    ) > "$evidence/compile-$role.log" 2>&1
    record_binaries "$role"
    while IFS=$'\t' read -r case_id package variant name filter; do
      output="$evidence/inventory-$role-$case_id.txt"
      "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        -test.timeout=90s > "$output"
      pr72_rpc_context_verify_process "$output" "$case_id"
    done < "$evidence/cases.tsv"
  done
}

pr72_read_offset_sample() {
  local group group_offset pair case_id package variant name filter comparison side index left right
  local -a comparisons=(aa ac cd ab) sides
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    group_offset=0
    if [[ "$group" == confirmation ]]; then group_offset=1; fi
    for pair in {1..10}; do
      printf -v pair '%02d' "$pair"
      sides=(right left)
      if (( (10#$pair + group_offset) % 2 == 1 )); then sides=(left right); fi
      while IFS=$'\t' read -r case_id package variant name filter; do
        for index in {0..3}; do
          comparison="${comparisons[(index + 10#$pair + 10#$case_id + group_offset) % 4]}"
          left="${comparison:0:1}"
          right="${comparison:1:1}"
          for side in "${sides[@]}"; do
            if [[ "$side" == left ]]; then
              run_leg "$group" "$pair" "$comparison" "$side" "$left" "$case_id" "$package" "$variant" "$filter"
            else
              run_leg "$group" "$pair" "$comparison" "$side" "$right" "$case_id" "$package" "$variant" "$filter"
            fi
          done
        done
      done < "$evidence/cases.tsv"
    done
  done
}

pr72_read_offset_verify_samples() {
  local group pair comparison case_id side revision variant filename
  python3 - "$evidence/order.tsv" <<'PYTHON' || return
from collections import Counter
import csv
from pathlib import Path
import sys

groups = ("primary", "confirmation")
comparisons = ("aa", "ac", "cd", "ab")
cases = ("04", "13", "14", "15")
with Path(sys.argv[1]).open(newline="") as stream:
    rows = list(csv.DictReader(stream, delimiter="\t"))
expected = []
for offset, group in enumerate(groups):
    for sample in range(1, 11):
        pair = f"{sample:02}"
        sides = ("left", "right") if (sample + offset) % 2 else ("right", "left")
        for case in cases:
            start = (sample + int(case) + offset) % len(comparisons)
            for comparison in comparisons[start:] + comparisons[:start]:
                for side in sides:
                    role = comparison[0 if side == "left" else 1]
                    expected.append(dict(group=group, pair=pair, comparison=comparison, case=case,
                                         side=side, revision=role, variant="default",
                                         raw=f"raw/{group}.{pair}.{comparison}.{case}.{side}.txt"))
if rows != expected:
    counts = Counter((row["group"], row["comparison"], row["side"]) for row in rows)
    mismatch = next(((actual, wanted) for actual, wanted in zip(rows, expected) if actual != wanted), ())
    raise SystemExit(f"wrong sample identity/order: got {len(rows)} processes, want {len(expected)}: {counts}; first mismatch {mismatch}")
print(f"Verified exactly {len(expected)} scheduled processes.")
PYTHON
  while IFS=$'\t' read -r group pair comparison case_id side revision variant filename; do
    if [[ "$group" == group ]]; then continue; fi
    pr72_rpc_context_verify_process "$evidence/$filename" "$case_id" || return 1
  done < "$evidence/order.tsv"
}

pr72_read_offset_analyze() {
  local comparison status exit_status=0
  local -a gate_args
  printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for comparison in aa ac cd ab; do
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    gate_args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
    if [[ "$comparison" == ac ]]; then
      gate_args=(--approved-costs "$scratch/source/c/scripts/benchgate/approved-costs.json" --base-sha "$base" "${gate_args[@]}")
    fi
    status=0
    "$scratch/bin/benchgate" "${gate_args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
    printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
    if (( status != 0 )); then
      grep -Eq '^performance gate rejected [0-9]+ regression\(s\)$' "$evidence/$comparison.benchgate.log" ||
        return "$status"
      exit_status=1
    fi
  done
  cat "$evidence/verdicts.tsv"
  if ! grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"; then
    printf 'A/A failed the original gate. Attribution is inconclusive.\n' >&2
    return 1
  fi
  return "$exit_status"
}

pr72_read_offset_main() {
  local loader=abb5738b19aeb8e24e733b897941826311fec55d
  local sampler=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
  local launch dry_run=false
  if [[ $# == 1 && "$1" == --dry-run ]]; then
    dry_run=true
  elif [[ $# != 0 ]]; then
    printf 'Usage: %s [--dry-run]\n' "$0" >&2
    return 2
  fi

  repo=$(git rev-parse --show-toplevel)
  launch=$(pwd -P)
  mkdir -p "$launch/.tmp"
  test ! -L "$launch/.tmp"
  evidence="$launch/.tmp/hosted-pr72-read-offset-attribution"
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
  git -C "$repo" merge-base --is-ancestor "$base" "$head"
  scratch=$(mktemp -d "$launch/.tmp/.hosted-pr72-read-offset-attribution.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
One runner, Go 1.27.1 linux/amd64 v1, default link, CGO enabled as in run 45258221740.
A is the exact base with the three symmetric test additions. B differs from A by the offset guard removal.
D is the exact latest stacked head. C differs from D by restoring only the ReadBytes offset guard.
A/A uses the same A binary on both sides and must pass for attribution.
A/C compares both revisions with the ReadBytes guard present. C/D and A/B isolate the removal in each context.
The four original process filters cover eighteen leaves. Each comparison has two groups of ten alternating pairs.
All four groups of comparisons retain raw legs and use the pinned original benchgate without changed thresholds.
A/C keeps the original approved-cost policy for the exact main revision. A/B and C/D isolate the guard with no added approvals.
A threshold failure fails the diagnostic after archiving all four verdicts. It does not replace the full PR gate.
PROTOCOL
  pr72_read_offset_write_scope
  pr72_read_offset_archive_roles
  if "$dry_run"; then
    printf 'Scope, archive trees, exact test sources and all four wire roles verified.\n'
    return
  fi

  export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
  mkdir -p "$TMPDIR" "$scratch/bin"
  probe_goos=$(go env GOOS)
  probe_goarch=$(go env GOARCH)
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$probe_goos:$probe_goarch:$(go env GOAMD64):$(go env CGO_ENABLED)" == linux:amd64:v1:1 ]]
  [[ -z "$(go env GOFLAGS)" && -z "$(go env GOEXPERIMENT)" ]]
  collect_host > "$evidence/environment.txt"
  pr72_read_offset_compile
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr72_read_offset_sample
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr72_read_offset_verify_samples
  pr72_read_offset_analyze
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  pr72_read_offset_main "$@"
fi
