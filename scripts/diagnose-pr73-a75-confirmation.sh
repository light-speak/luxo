#!/usr/bin/env bash
set -euo pipefail

# Reuse frozen samplers; this entry changes only revisions and the selected scope.
main() {
  local suite="${1:-}" dry_run=false helper path role package filter name case_id
  local helpers=d2533667e3ae147570d903cfcec6ed54d7c3141d
  case "$suite" in int|float) ;; *) printf 'Usage: %s int|float [--dry-run]\n' "$0" >&2; return 2 ;; esac
  if [[ $# == 2 && "$2" == --dry-run ]]; then dry_run=true
  elif [[ $# != 1 ]]; then return 2; fi
  case "${PR73_PRIMARY_REPLAY:-0}" in 0|1) ;; *) return 2 ;; esac
  repo=$(git rev-parse --show-toplevel)
  mkdir -p "$repo/.tmp"
  test ! -L "$repo/.tmp"
  evidence="$repo/.tmp/hosted-pr73-a75-confirmation-$suite"
  mkdir "$evidence"
  scratch=$(mktemp -d "$repo/.tmp/.hosted-pr73-a75-confirmation-$suite.XXXXXX")
  trap 'status=$?; trap - EXIT; case "$scratch" in "$repo"/.tmp/.hosted-pr73-a75-confirmation-*) rm -rf -- "$scratch" ;; *) exit 2 ;; esac; printf "%s\n" "$status" > "$evidence/exit"; exit "$status"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  cp "$repo/scripts/diagnose-pr73-a75-confirmation.json" "$evidence/inputs.json"
  cp "$repo/scripts/diagnose-pr73-a75-confirmation.sh" "$evidence/entry.sh"
  for path in diagnose-hosted-perf.sh diagnose-pr73-exact-codegen.sh diagnose-pr99-codegen.py; do
    git -C "$repo" show "$helpers:scripts/$path" > "$evidence/$path"
  done
  (
    cd "$evidence"
    printf '%s  %s\n' \
      9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 diagnose-hosted-perf.sh \
      1a13d532840d2af053bcae6d0c02eaa62816d28d9454ed97a7c8dce5ee7e3acc diagnose-pr73-exact-codegen.sh \
      1ce905093a9dfb152fceb0e4e2f1497c2a421f810d360865d0f9d2d1a541a5af diagnose-pr99-codegen.py > helpers.sha256
    sha256sum -c helpers.sha256
  )
  # The exact verifier retains its strict raw validation and schedule checks.
  python3 - "$evidence" "$suite" "$repo" "$scratch" <<'PYTHON'
from pathlib import Path
from hashlib import sha256
import json
import subprocess
import sys

e, suite, repo, scratch = Path(sys.argv[1]), sys.argv[2], sys.argv[3], Path(sys.argv[4])
d = json.loads((e / 'inputs.json').read_text())
assert d['helpers'] == 'd2533667e3ae147570d903cfcec6ed54d7c3141d'
source = (e / 'diagnose-pr73-exact-codegen.sh').read_text()
for old, new in (
    ('comparisons = ("aa", "ab", "ap")', 'comparisons = ("ab", "aa")'),
    ('right_role = {"aa": "base", "ab": "head", "ap": "product"}', 'right_role = {"aa": "base", "ab": "head"}'),
    ('cases = ("01", "02", "03", "06", "07", "08")', 'cases = (sys.argv[2],)'),
    ('python3 - "$evidence/order.tsv" <<\'PYTHON\'', 'python3 - "$evidence/order.tsv" "$pr73_case_id" <<\'PYTHON\''),
):
    assert source.count(old) == 1, old
    source = source.replace(old, new)
(e / 'scope-verifier.sh').write_text(source)
s = d['suites'][suite]
for path, text in s['scope'].items():
    (e / path).write_text(text)
confirm = json.loads(s['scope']['benchmark-confirmation-expected.json'])
primary = json.loads(s['scope']['benchmark-expected.json'])
assert confirm['Base'] == confirm['Head'] and len(confirm['Base']) == 1
assert primary['Base'] == primary['Head'] and len(primary['Base']) == 52
assert confirm['Base'][0] in primary['Base']
package, expression = s['scope']['benchmark-confirmation-batch.tsv'].strip().split('\t')
assert package == 'pkg/codegen'
name = confirm['Base'][0]['Name']
assert (expression, name) == {
    'int': ('^BenchmarkAPIParameterMetadata$/^Int$', 'BenchmarkAPIParameterMetadata/Int-2'),
    'float': ('^BenchmarkCompileFloatEmission$/^literal$', 'BenchmarkCompileFloatEmission/literal-2'),
}[suite]
case = {'int': '01', 'float': '03'}[suite]
(e / (case + '.names')).write_text(name + '\n')
(e / 'cases.tsv').write_text('\t'.join((case, package, 'default', name, expression)) + '\n')
(e / 'expected.json').write_text(s['scope']['benchmark-confirmation-expected.json'])
(e / 'case-id').write_text(case + '\n')
(e / 'packages.txt').write_text(''.join(p + '\n' for p in sorted({r['Package'] for r in primary['Base']})))
rows = ['group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw']
for offset, group in enumerate(('primary', 'confirmation')):
    for pair in range(1, 11):
        comparisons = ('aa', 'ab') if (pair + int(case) + offset) % 2 else ('ab', 'aa')
        sides = ('left', 'right') if (pair + offset) % 2 else ('right', 'left')
        for comparison in comparisons:
            for side in sides:
                role = 'head' if comparison == 'ab' and side == 'right' else 'base'
                raw = f'raw/{group}.{pair:02}.{comparison}.{case}.{side}.txt'
                rows.append('\t'.join((group, f'{pair:02}', comparison, case, side, role, 'default', raw)))
assert len(rows) == 81
(e / 'planned-order.tsv').write_text('\n'.join(rows) + '\n')

def git(*args):
    return subprocess.check_output(['git', '-C', repo, *args]).decode().strip()
assert git('rev-parse', d['base'] + '^{tree}') == d['baseTree']
merge_base = git('merge-base', d['base'], d['prHead'])
assert len(merge_base) == 40
tree = git('merge-tree', '--write-tree', d['base'], d['prHead'])
assert tree == d['testedTree'], (tree, d['testedTree'])
# The synthetic merge object is optional on the hosted runner.
if subprocess.run(['git', '-C', repo, 'cat-file', '-e', d['testedMerge'] + '^{commit}'], stderr=subprocess.DEVNULL).returncode == 0:
    assert git('show', '-s', '--format=%T', d['testedMerge']) == tree
    assert git('show', '-s', '--format=%P', d['testedMerge']) == d['base'] + ' ' + d['prHead']
for role, revision in (('base', d['base']), ('head', tree)):
    target = scratch / 'source' / role
    target.mkdir(parents=True)
    # git archive provides the exact tree without any working-tree files.
    archive = subprocess.Popen(['git', '-C', repo, 'archive', revision], stdout=subprocess.PIPE)
    result = subprocess.run(['tar', '-xf', '-', '-C', str(target)], stdin=archive.stdout)
    archive.stdout.close()
    assert archive.wait() == result.returncode == 0
    for path, digest in d['identicalFixtureSHA256'].items():
        assert sha256((target / path).read_bytes()).hexdigest() == digest, (role, path)
(e / 'revisions.json').write_text(json.dumps({
    'base': d['base'], 'baseTree': d['baseTree'], 'prHead': d['prHead'],
    'officialMerge': d['testedMerge'], 'testedTree': tree,
    'headArchiveMethod': 'git merge-tree --write-tree base prHead; git archive tree',
    'mergeBase': merge_base,
    'officialRun': d['officialRun'], 'officialAttempt': d['officialAttempt'],
    'officialCPU': s['officialCPU'], 'scope': suite, 'timedProcessesPlanned': 80,
    'primaryReplay': 'optional one pass per role, 52 leaves; not confirmation evidence',
}, indent=2) + '\n')
print(f'{suite}: exact merge tree {tree}, one official leaf, 80 planned timed arms verified')
PYTHON
  snapshot_sources archive
  source "$evidence/diagnose-hosted-perf.sh"
  source "$evidence/scope-verifier.sh"
  base=a75a0acce23b30e3f99d44b0c47f746901b82bb8
  pr73_case_id=$(cat "$evidence/case-id")
  cat > "$evidence/protocol.txt" <<'PROTOCOL'
PR 73 official run 45315193455 attempt 1: a75 base versus exact e4fc tested merge tree.
Each job uses its original official one-leaf confirmation filter in BOTH groups.
A/A and A/H run on the same Linux amd64 v1 runner, Go 1.27.1, CGO1, default link, trimpath.
Two groups of ten alternating pairs per comparison; 200ms, count=1, cpu=2, timeout=90s, benchmem.
Original benchstat alpha=.01 and benchgate apply: significant time >5%; bytes/allocations >0%; both groups.
A/A has no approvals; A/H uses only the exact H policy with the exact a75 baseline SHA.
All raw arms, source hashes, ELF binaries, symbol positions and selected disassembly are retained.
Actual CPU is recorded; ubuntu-latest does not promise the original AMD/Intel model.
Optional original primary replay retains the original 52 leaves once per role, after mandatory sampling.
That optional replay is not a two-group performance gate and cannot approve other leaves.
A/A rejection makes attribution inconclusive; A/H rejection remains a rejection. No padding or test restoration.
PROTOCOL
  if "$dry_run"; then
    # Exercise the real sampler's order generation without starting any processes.
    run_leg() { printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\traw/%s.%s.%s.%s.%s.txt\n' "$1" "$2" "$3" "$6" "$4" "$5" "$8" "$1" "$2" "$3" "$6" "$4" >> "$evidence/order.tsv"; }
    sample_groups
    cmp "$evidence/planned-order.tsv" "$evidence/order.tsv"
    printf 'Dry-run only: no Go build, benchmark, VM or container was started.\n'
    return
  fi
  export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local
  mkdir -p "$TMPDIR" "$scratch/bin"
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  [[ -z "$(go env GOFLAGS)" && -z "$(go env GOEXPERIMENT)" ]]
  collect_host > "$evidence/environment.txt"
  for role in base head; do
    sh "$scratch/source/$role/scripts/setup-modules.sh" "$scratch/source/$role"
    cp "$scratch/source/$role/go.work" "$evidence/go-$role.work.txt"
  done
  snapshot_sources before
  (
    cd "$scratch/source/base"
    for helper in benchrun benchplan benchgate; do go build -o "$scratch/bin/$helper" "./scripts/$helper"; done
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat" > "$evidence/benchstat-build.txt"
  ) > "$evidence/compile-tools.log" 2>&1
  local pattern='^github\.com/light-speak/luxo/pkg/codegen\..*(Benchmark(APIParameterMetadata|CompileFloatEmission).*|(writeAPIRegistration|writeAPIParameterRegistration|writeRegisteredParam|writeRegistryRoute|registeredParamType|compileExpr|compileLiteral|compileNumericLiteral))$|^fmt\.(Fprintf|\(\*pp\)\.doPrintf)$|^strings\.\(\*Builder\)\.(Write|WriteString)$'
  for role in base head; do
    mkdir "$scratch/bin/$role"
    "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" pkg/codegen > "$evidence/compile-$role.log" 2>&1
    if [[ "${PR73_PRIMARY_REPLAY:-0}" == 1 && "$suite" == int ]]; then
      "$scratch/bin/benchrun" compile "$scratch/source/$role" "$scratch/bin/$role" pkg/lsp >> "$evidence/compile-$role.log" 2>&1
    fi
    record_binaries "$role"
    mkdir "$evidence/elf-$role"
    cp "$scratch/bin/$role/"*.test "$evidence/elf-$role/"
    go tool nm -size "$scratch/bin/$role/pkg-codegen.test" > "$evidence/symbols-$role.nm"
    python3 "$evidence/diagnose-pr99-codegen.py" layout "$evidence/symbols-$role.nm" "$evidence/layout-$role.tsv"
    go tool objdump -s "$pattern" "$scratch/bin/$role/pkg-codegen.test" > "$evidence/symbols-$role.objdump"
    test -s "$evidence/symbols-$role.objdump"
    IFS=$'\t' read -r case_id package helper name filter < "$evidence/cases.tsv"
    "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
      -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/inventory-$role.txt"
    pr73_exact_check_leg "$evidence/inventory-$role.txt" "$case_id" 1x
  done
  date -u '+sampling_start=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  sample_groups
  date -u '+sampling_end=%Y-%m-%dT%H:%M:%SZ' >> "$evidence/environment.txt"
  cmp "$evidence/planned-order.tsv" "$evidence/order.tsv"
  pr73_exact_verify_samples
  if [[ "${PR73_PRIMARY_REPLAY:-0}" == 1 ]]; then
    for role in base head; do
      while IFS=$'\t' read -r package filter; do
        "$scratch/bin/benchrun" run "$scratch/source/$role" "$scratch/bin/$role" "$package" \
          -test.run='^$' -test.bench="$filter" -test.benchmem -test.benchtime="$benchtime" -test.count=1 -test.cpu=2 -test.timeout=90s \
          >> "$evidence/primary-replay-$role.txt" 2>> "$evidence/benchmark-stderr.txt"
      done < "$evidence/benchmark-batch.tsv"
      "$scratch/bin/benchplan" --normalize "$evidence/primary-replay-$role.txt" "$evidence/primary-replay-$role.normalized.txt"
    done
    "$scratch/bin/benchplan" --verify "$evidence/benchmark-expected.json" "$evidence/primary-replay-base.normalized.txt" "$evidence/primary-replay-head.normalized.txt"
  fi
  python3 - "$evidence" <<'PYTHON'
from pathlib import Path
import json
import sys
p = Path(sys.argv[1])
files = list((p / 'raw').glob('*.txt')) + list(p.glob('inventory-*.txt')) + list(p.glob('primary-replay-*.txt'))
files = [f for f in files if '.normalized.' not in f.name]
cpus = {line.strip() for f in files for line in f.read_text().splitlines() if line.startswith('cpu: ')}
assert len(cpus) == 1, cpus
(p / 'cpu-consistency.json').write_text(json.dumps({'CPU': sorted(cpus), 'processFiles': len(files)}, indent=2) + '\n')
PYTHON
  snapshot_sources after
  cmp "$evidence/source-before.json" "$evidence/source-after.json"
  analyze_groups
  awk 'NR>1 && $2 != 0 {failed=1} END {exit failed}' "$evidence/verdicts.tsv"
}

snapshot_sources() {
  python3 - "$scratch/source" "$evidence/source-$1.json" <<'PYTHON'
from hashlib import sha256
from pathlib import Path
import json
import sys
root, target = map(Path, sys.argv[1:])
files = {str(p.relative_to(root)): sha256(p.read_bytes()).hexdigest()
         for p in sorted(root.rglob('*')) if p.is_file() and '.tmp' not in p.relative_to(root).parts
         and p.name not in ('go.work', 'go.work.sum')}
target.write_text(json.dumps(files, indent=2, sort_keys=True) + '\n')
PYTHON
}

main "$@"
