#!/usr/bin/env bash
set -euo pipefail

pr73_exact_scope() {
  cat > "$evidence/cases.tsv" <<'CASES'
01	pkg/codegen	^BenchmarkAPIParameterMetadata$/^Int$|^BenchmarkAPIParameterMetadata$/^State$	2
02	pkg/codegen	^BenchmarkGenerateModelFile$|^BenchmarkPrimaryKeyLoaderMetadata$/^1$/^loaders$	2
03	pkg/codegen	^BenchmarkGlobalReceiverName$/^_Record$	1
06	pkg/codegen	^BenchmarkCompileDecimalIntegerLiterals$/^int_plain$|^BenchmarkDurationScannerGeneration$/^Duration\?$|^BenchmarkDurationScannerGeneration$/^\[Duration\]\?$|^BenchmarkRelationLoaderGeneration$/^1$/^metadata$	4
07	pkg/codegen	^BenchmarkRelationResolverEmission$	4
08	pkg/codegen	^BenchmarkNumericRangeValidation$	4
CASES
  printf 'BenchmarkAPIParameterMetadata/%s-2\n' Int State > "$evidence/01.names"
  printf '%s\n' BenchmarkGenerateModelFile-2 BenchmarkPrimaryKeyLoaderMetadata/1/loaders-2 > "$evidence/02.names"
  printf '%s\n' BenchmarkGlobalReceiverName/_Record-2 > "$evidence/03.names"
  printf '%s\n' BenchmarkCompileDecimalIntegerLiterals/int_plain-2 'BenchmarkDurationScannerGeneration/Duration?-2' \
    'BenchmarkDurationScannerGeneration/[Duration]?-2' BenchmarkRelationLoaderGeneration/1/metadata-2 > "$evidence/06.names"
  printf 'BenchmarkRelationResolverEmission/%s-2\n' nullable-single required-list leaf-list nullable-list > "$evidence/07.names"
  printf 'BenchmarkNumericRangeValidation/%s-2\n' none positive signed_int signed_float > "$evidence/08.names"
  python3 - "$evidence" <<'PYTHON'
import json
from pathlib import Path
import sys

evidence = Path(sys.argv[1])
leaves = []
for line in (evidence / "cases.tsv").read_text().splitlines():
    case, package, _, count = line.split("\t")
    names = (evidence / f"{case}.names").read_text().splitlines()
    if len(names) != int(count):
        raise SystemExit(f"Wrong fixture count for {case}")
    leaves.extend({"Package": package, "Name": name} for name in names)
if len(leaves) != 17 or len({(row["Package"], row["Name"]) for row in leaves}) != 17:
    raise SystemExit("Expected seventeen distinct leaves")
(evidence / "expected.json").write_text(json.dumps({"Base": leaves, "Head": leaves}, indent=2) + "\n")
PYTHON
  (
    cd "$evidence"
    printf '%s  %s\n' \
      3d6fb436344b3f1213385ef40b827fda17c82b659d2efc6578cac60561bc8365 cases.tsv \
      61364c760d88a1079bb903759a7b9b5c474422dc543566b51e7fd9a7629103a5 expected.json > scope.sha256
    sha256sum -c scope.sha256
  )
}

pr73_exact_archive_head() {
  local destination="$1" root_entry
  local -a roots=()
  if git -C "$repo" cat-file -e "$merge_sha^{commit}" 2>/dev/null; then
    [[ "$(git -C "$repo" show -s --format=%T "$merge_sha")" == "$merge_tree" ]]
    [[ "$(git -C "$repo" show -s --format=%P "$merge_sha")" == "$pr_base $pr_head" ]]
    git -C "$repo" ls-tree "$merge_sha" > "$evidence/official-root.tsv"
    git -C "$repo" archive "$merge_sha" | tar -xf - -C "$destination"
    printf 'snapshot\t%s\t%s\tgit-merge-commit\n' "$merge_sha" "$merge_tree" >> "$evidence/revisions.tsv"
    return
  fi

  # The official merge changed only the root packages tree relative to PR head.
  # Verify the complete reconstructed Git tree before copying either archive.
  python3 - "$repo" "$pr_head" "$pr_base" "$merge_tree" "$evidence" <<'PYTHON'
from pathlib import Path
import subprocess
import sys

repo, head, parent, expected, evidence_value = sys.argv[1:]
evidence = Path(evidence_value)

def git(*args, input=None):
    return subprocess.check_output(["git", "-C", repo, *args], input=input)

def entries(*args):
    for entry in git("ls-tree", "-z", *args).split(b"\0"):
        if entry:
            meta, name = entry.split(b"\t")
            yield name, meta.split(b" ")

head_tree = dict(entries(head))
parent_packages = dict(entries(parent, "packages"))
if (head_tree[b"packages"] != [b"040000", b"tree", b"f4ee85ad055b592d8d4cd9eb8593de921d2a779e"] or
        parent_packages != {b"packages": [b"040000", b"tree", b"4596e3b9d5ec7a0b164dfca9ff17fc418d91ee4a"]}):
    raise SystemExit("Pinned packages tree changed")
head_tree.update(parent_packages)
raw = b"".join(mode.lstrip(b"0") + b" " + name + b"\0" + bytes.fromhex(sha.decode())
               for name, (mode, _, sha) in head_tree.items())
observed = git("hash-object", "-t", "tree", "--stdin", input=raw).decode().strip()
if observed != expected:
    raise SystemExit(f"Reconstructed tree {observed} does not equal official {expected}")
(evidence / "official-root.tsv").write_bytes(b"".join(
    b" ".join(meta) + b"\t" + name + b"\n" for name, meta in head_tree.items()))
(evidence / "pr-head-archive-paths.txt").write_bytes(b"".join(
    name + b"\n" for name in head_tree if name != b"packages"))
PYTHON
  while IFS= read -r root_entry; do roots+=("$root_entry"); done < "$evidence/pr-head-archive-paths.txt"
  git -C "$repo" archive "$pr_head" -- "${roots[@]}" | tar -xf - -C "$destination"
  git -C "$repo" archive "$pr_base" -- packages | tar -xf - -C "$destination"
  printf 'snapshot\t%s+%s\t%s\treconstructed-official-tree\n' "$pr_head" "$pr_base" "$merge_tree" >> "$evidence/revisions.tsv"
}

pr73_exact_archive() {
  local role path source
  local -a product_tests fixtures
  product_tests=(pkg/codegen/{deploy_helm_test.go,deploy_test.go,numeric_range_test.go,relation_resolver_test.go})
  fixtures=(pkg/codegen/{codegen_bench_test.go,dataloader_local_pk_bench_test.go,dataloader_nullable_bench_test.go,global_value_bench_test.go,numeric_literal_bench_test.go,numeric_range_bench_test.go,scanner_test.go})
  [[ "$(git -C "$repo" show -s --format=%T "$base")" == 58674d178008ccf8974f2fdaaf538d03aaba2e79 ]]
  [[ "$(git -C "$repo" show -s --format=%T "$pr_head")" == 1da484ba237041b8725486da9fb364eae58a4d7b ]]
  [[ "$(git -C "$repo" show -s --format=%T "$pr_base")" == 9a110d16ea3bf6042de116a23f7e3ac4239fc38b ]]
  git -C "$repo" merge-base --is-ancestor "$base" "$pr_head"
  printf 'role\tcommit_or_parents\tarchive_base_tree\tarchive_method\n' > "$evidence/revisions.tsv"
  printf 'base\t%s\t%s\tgit-commit\n' "$base" 58674d178008ccf8974f2fdaaf538d03aaba2e79 >> "$evidence/revisions.tsv"
  printf 'official-head\t%s\t%s\tgithub-tested-merge\n' "$merge_sha" "$merge_tree" >> "$evidence/revisions.tsv"
  mkdir -p "$scratch/source/base" "$scratch/source/head"
  git -C "$repo" archive "$base" | tar -xf - -C "$scratch/source/base"
  pr73_exact_archive_head "$scratch/source/head"
  cp -R "$scratch/source/head" "$scratch/source/product"

  git -C "$repo" diff --no-ext-diff --no-textconv --color=never --binary --full-index \
    --diff-algorithm=myers --indent-heuristic --unified=3 --src-prefix=a/ --dst-prefix=b/ "$pr_head" "$base" \
    -- "${product_tests[@]}" > "$evidence/product-tests.patch"
  (
    cd "$evidence"
    printf '%s  %s\n' \
      2329fb8ed024b0e69e5c0e8e4533b1bde1ac7805559523c1e1b646043bf56bb0 product-tests.patch > patch.sha256
    sha256sum -c patch.sha256
  )
  (
    cd "$scratch/source/product"
    export GIT_CEILING_DIRECTORIES="$scratch/source"
    git apply --whitespace=error --check "$evidence/product-tests.patch"
    git apply --whitespace=error "$evidence/product-tests.patch"
    git apply --whitespace=error --reverse --check "$evidence/product-tests.patch"
    for path in "${product_tests[@]}"; do cmp "$path" "$scratch/source/base/$path"; done
  )
  printf 'product\thead\t%s\tfour-base-test-roots-restored\n' "$merge_tree" >> "$evidence/revisions.tsv"
  for role in base head product; do
    source="$scratch/source/$role"
    (
      cd "$source"
      sha256sum "${fixtures[@]}" > "$evidence/fixtures-$role.sha256"
      sha256sum go.mod go.sum "${fixtures[@]}" "${product_tests[@]}" \
        pkg/codegen/{handler.go,dataloader.go,scanner.go,identifiers.go,compile.go,deploy.go,deploy_helm.go,query_facade.go,relation_resolver.go} \
        scripts/{setup-modules.sh,benchgate/approved-costs.json} > "$evidence/sources-$role.sha256"
    )
    printf '%s  %s\n' 3ceefe00c18fff780928a3bb9928f04d68afe2cb512712857b7d84afa1d789fb \
      "$evidence/fixtures-$role.sha256" | sha256sum -c -
  done
  diff -u "$evidence/sources-head.sha256" "$evidence/sources-product.sha256" > "$evidence/product-delta.txt" || [[ $? == 1 ]]
  awk '$1 ~ /^[-+][a-f0-9]+$/ && length($1) == 65 { print $2 }' "$evidence/product-delta.txt" | sort -u > "$scratch/delta-paths.txt"
  printf '%s\n' "${product_tests[@]}" | sort > "$scratch/test-paths.txt"
  cmp "$scratch/delta-paths.txt" "$scratch/test-paths.txt"
}

pr73_exact_check_leg() {
  local filename="$1" case_id="$2" iterations="${3:-sampled}"
  awk -v expected="$evidence/$case_id.names" -v iterations="$iterations" '
    BEGIN {
      while ((getline name < expected) > 0) { names[name]=1; remaining++ }
      if (!remaining) exit 1
    }
    /^goos: linux$/ { systems++; next }
    /^goarch: amd64$/ { architectures++; next }
    /^pkg: github.com\/light-speak\/luxo\/pkg\/codegen$/ { packages++; next }
    /^cpu: .+/ { cpus++; next }
    /^PASS$/ { passes++; next }
    /^Benchmark/ {
      if (!names[$1] || NF != 8 || $2 !~ /^[1-9][0-9]*$/ ||
          (iterations == "1x" && $2 != 1) || $3 !~ /^[0-9]+([.][0-9]+)?$/ ||
          $4 != "ns/op" || $5 !~ /^[0-9]+([.][0-9]+)?$/ ||
          $6 != "B/op" || $7 !~ /^[0-9]+([.][0-9]+)?$/ || $8 != "allocs/op") bad=1
      delete names[$1]; remaining--; next
    }
    /^[[:space:]]*$/ { next }
    { bad=1 }
    END {
      if (bad || remaining || systems != 1 || architectures != 1 || packages != 1 || cpus != 1 || passes != 1) {
        print "Unexpected benchmark process: " FILENAME > "/dev/stderr"
        exit 1
      }
    }
  ' "$filename"
}

pr73_exact_compile() {
  local role source binary case_id package filter count inventory
  local pattern='^github\.com/light-speak/luxo/pkg/codegen\..*(Benchmark(APIParameterMetadata|GenerateModelFile|PrimaryKeyLoaderMetadata|GlobalReceiverName|CompileDecimalIntegerLiterals|DurationScannerGeneration|RelationLoaderGeneration|RelationResolverEmission|NumericRangeValidation).*|(writeAPIRegistration|writeAPIParameterRegistration|writeRegisteredParam|writeRegistryRoute|registeredParamType|collectDataLoaderInputs|collectLoadCalls|analyzeRelations|relationRemoteKeyNullable|generateScanner|writeScannerLocals|writeScannerResult|durationScanFormat|receiverName|compileLiteral|compileNumericLiteral|decimalIntegerLiteral|generateRelationResolver|generateNumericValidation|writeValidationCheck|writeValidationCheckParts))$|^fmt\.(Fprintf|\(\*pp\)\.doPrintf)$|^strings\.(HasPrefix|TrimLeft|\(\*Builder\)\.(Write|WriteString))$|^runtime\.mapaccess[12]_faststr$'
  for role in base head product; do
    source="$scratch/source/$role"
    sh "$source/scripts/setup-modules.sh" "$source"
    cp "$source/go.work" "$evidence/go-$role.work.txt"
  done
  (
    cd "$scratch/source/base"
    for role in benchrun benchplan benchgate; do
      go build -o "$scratch/bin/$role" "./scripts/$role"
    done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  ) > "$evidence/compile-tools.log" 2>&1
  for role in base head product; do
    source="$scratch/source/$role"
    mkdir "$scratch/bin/$role"
    "$scratch/bin/benchrun" compile "$source" "$scratch/bin/$role" pkg/codegen > "$evidence/compile-$role.log" 2>&1
    record_binaries "$role"
    binary="$scratch/bin/$role/pkg-codegen.test"
    go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$role.nm"
    go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$role.objdump"
    test -s "$evidence/symbols-$role.nm"
    test -s "$evidence/symbols-$role.objdump"
    while IFS=$'\t' read -r case_id package filter count; do
      inventory="$evidence/inventory-$role-$case_id.txt"
      "$scratch/bin/benchrun" run "$source" "$scratch/bin/$role" "$package" \
        -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
        > "$inventory"
      pr73_exact_check_leg "$inventory" "$case_id" 1x
    done < "$evidence/cases.tsv"
  done
}

pr73_exact_sample() {
  local group group_offset pair case_id package filter count index comparison right_role side
  local -a comparisons=(aa ab ap) sides
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
  for group in primary confirmation; do
    group_offset=0
    if [[ "$group" == confirmation ]]; then group_offset=1; fi
    for pair in {1..10}; do
      printf -v pair '%02d' "$pair"
      sides=(right left)
      if (( (10#$pair + group_offset) % 2 == 1 )); then sides=(left right); fi
      while IFS=$'\t' read -r case_id package filter count; do
        for index in {0..2}; do
          comparison="${comparisons[(index + 10#$pair + 10#$case_id + group_offset) % 3]}"
          right_role=base
          case "$comparison" in
            ab) right_role=head ;;
            ap) right_role=product ;;
          esac
          for side in "${sides[@]}"; do
            if [[ "$side" == left ]]; then
              run_leg "$group" "$pair" "$comparison" left base "$case_id" "$package" default "$filter"
            else
              run_leg "$group" "$pair" "$comparison" right "$right_role" "$case_id" "$package" default "$filter"
            fi
          done
        done
      done < "$evidence/cases.tsv"
    done
  done
}

pr73_exact_verify_samples() {
  local group pair comparison case_id side revision variant filename
  python3 - "$evidence/order.tsv" <<'PYTHON' || return
import csv
from pathlib import Path
import sys

comparisons = ("aa", "ab", "ap")
right_role = {"aa": "base", "ab": "head", "ap": "product"}
cases = ("01", "02", "03", "06", "07", "08")
with Path(sys.argv[1]).open(newline="") as stream:
    rows = list(csv.DictReader(stream, delimiter="\t"))
expected = []
for offset, group in enumerate(("primary", "confirmation")):
    for sample in range(1, 11):
        pair = f"{sample:02}"
        sides = ("left", "right") if (sample + offset) % 2 else ("right", "left")
        for case in cases:
            start = (sample + int(case) + offset) % len(comparisons)
            for comparison in comparisons[start:] + comparisons[:start]:
                for side in sides:
                    revision = "base" if side == "left" else right_role[comparison]
                    expected.append(dict(group=group, pair=pair, comparison=comparison, case=case,
                                         side=side, revision=revision, variant="default",
                                         raw=f"raw/{group}.{pair}.{comparison}.{case}.{side}.txt"))
if rows != expected:
    mismatch = next(((got, want) for got, want in zip(rows, expected) if got != want), ())
    raise SystemExit(f"wrong sample identity/order: got {len(rows)} legs, want {len(expected)}; first mismatch {mismatch}")
print(f"Verified {len(expected)} scheduled benchmark processes.")
PYTHON
  while IFS=$'\t' read -r group pair comparison case_id side revision variant filename; do
    if [[ "$group" == group ]]; then continue; fi
    pr73_exact_check_leg "$evidence/$filename" "$case_id" || return
  done < "$evidence/order.tsv"
}

pr73_exact_analyze() {
  local comparison status failure=0
  local -a gate_args
  printf 'comparison\tbenchgate_exit\n' > "$evidence/verdicts.tsv"
  for comparison in aa ab ap; do
    normalize_group "$comparison" primary
    normalize_group "$comparison" confirmation
    gate_args=("$evidence/$comparison.primary.csv" "$evidence/$comparison.confirmation.csv")
    if [[ "$comparison" != aa ]]; then
      gate_args=(--approved-costs "$scratch/source/head/scripts/benchgate/approved-costs.json" --base-sha "$base" "${gate_args[@]}")
    fi
    status=0
    "$scratch/bin/benchgate" "${gate_args[@]}" > "$evidence/$comparison.benchgate.log" 2>&1 || status=$?
    printf '%s\t%s\n' "$comparison" "$status" >> "$evidence/verdicts.tsv"
    if (( status != 0 )); then
      grep -Eq '^performance gate rejected [0-9]+ regression\(s\)$' "$evidence/$comparison.benchgate.log" ||
        return "$status"
      failure=1
    fi
  done
  cat "$evidence/verdicts.tsv"
  if ! grep -Fxq $'aa\t0' "$evidence/verdicts.tsv"; then
    printf 'A/A failed the original gate. Attribution is inconclusive.\n' >&2
    return 1
  fi
  return "$failure"
}

pr73_exact_main() {
  local sampler=cbf4cfb383d944ceb38f700e19cf79c4c54d3c58
  local pr_base=01126509def82c0465e3e703f64268233fbec651
  local pr_head=51ef873c5f578614f70d76e0b06471d459a6fb23
  local merge_sha=7553e2ede28d690f8ef0622e7fa462d969429dac
  local merge_tree=aff8b0162442ed67962519636b9005b086a49329
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
  evidence="$launch/.tmp/hosted-pr73-exact-codegen"
  mkdir "$evidence"
  git -C "$repo" show "$sampler:scripts/diagnose-hosted-perf.sh" > "$evidence/sampler.sh"
  printf '%s\n' "$sampler" > "$evidence/sampler-revision.txt"
  (
    cd "$evidence"
    printf '%s  %s\n' 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 sampler.sh > sampler.sha256
    sha256sum -c sampler.sha256
  )
  source "$evidence/sampler.sh"
  base=ba94103efe6e623849ba98df20fe6cf14b2d3596
  scratch=$(mktemp -d "$launch/.tmp/.hosted-pr73-exact-codegen.XXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
Focused follow-up to PR 73 official Tests run 45254105462 attempt 1.
A is ba94103. H uses the exact 7553e2e source tree. P keeps H production code with four A test roots.
If the synthetic merge object is absent, both fixed parents reconstruct and verify its exact root tree before archiving.
The four official confirmation filters retain nine leaves; two separate changed-production controls add eight.
A/A, A/H and A/P use one Linux amd64 v1 runner, Go 1.27.1, CGO1, default link, trimpath.
Two groups of ten alternating pairs per comparison, 200ms, CPU2, benchmem, benchstat alpha=.01.
The original benchgate and A-relative approvals apply; A/A has no approvals.
Threshold failures retain all three raw results and fail the diagnostic. It is a separate scope from the official PR gate.
PROTOCOL
  pr73_exact_scope
  pr73_exact_archive
  if "$dry_run"; then
    printf 'Official source tree, product test delta, and seventeen fixture leaves verified.\n'
    return
  fi
  export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
  mkdir -p "$TMPDIR" "$scratch/bin"
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  [[ -z "$(go env GOFLAGS)" && -z "$(go env GOEXPERIMENT)" ]]
  collect_host > "$evidence/environment.txt"
  pr73_exact_compile
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr73_exact_sample
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  pr73_exact_verify_samples
  pr73_exact_analyze
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  pr73_exact_main "$@"
fi
