#!/usr/bin/env python3
"""Attribute a shared exact Intel performance signal without changing release gates."""

from collections import Counter
from hashlib import sha256
from itertools import product
from pathlib import Path
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile


INPUT = Path(__file__).resolve().parent
ROOT = INPUT.parents[1]
OUT = ROOT / '.tmp/pr83-pr84-intel-evidence'
CHECKOUTS = ROOT / '.tmp/pr83-pr84-intel-checkouts'
REF = 'refs/heads/diagnostics/pr83-pr84-intel'
DIAGNOSTIC_PARENT = 'e1ee754133cda6a7b36b5b4c63cc596afec8c559'
MANIFEST_SHA = '3471b2d29d862dcce9bbdc0e27ec05440fe455c1f2b217a88e909daac1c27366'
MANIFEST = json.loads((INPUT / 'manifest.json').read_bytes())
PACKAGE = 'pkg/lux/api'
PROFILES = MANIFEST['profiles']
PAIRS = ('AA', 'HH', 'AH')
ROLES = ('A', 'H')
BLOCKS = (1, 2)
HOT = {
    'default': (r'BenchmarkIdentityConditionValidity', r'IdentityString|IdentityReader|Claims\.(Get|ID)|jwt\.'),
    'api-non-identity': (r'BenchmarkHandlerPanicFormatting', r'BenchmarkWSConnectionRegistrations',
                         r'benchmarkConnectionRegistrations|connectionSliceBaseline|callHandler'),
}
BENCH_RE = re.compile(rb'^(Benchmark\S+)\s+(\d+)\s+', re.M)


def need(ok, problem):
    if not ok:
        raise RuntimeError(problem)


def digest(path):
    return sha256(path.read_bytes()).hexdigest()


def call(*args, cwd=ROOT, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env)


def git(directory, *args):
    return call('git', '--no-replace-objects', '-C', directory, *args).decode().strip()


def save_json(path, data):
    with path.open('x') as stream:
        json.dump(data, stream, indent=2, sort_keys=True)
        stream.write('\n')


def selector(leaves):
    def anchor(piece):
        # Match Go's regexp.QuoteMeta byte for byte in the signed benchplan TSV.
        return '^' + re.sub(r'([\\.+*?()|\[\]{}^$])', r'\\\1', piece) + '$'

    return '|'.join('/'.join(anchor(piece) for piece in name.removesuffix('-2').split('/'))
                    for name in leaves)


def schedule():
    for block, index in product(BLOCKS, range(1, 11)):
        order = (0, 1) if index % 2 else (1, 0)
        revisions = ('83', '84') if index % 2 else ('84', '83')
        for pair, pr in product(PAIRS, revisions):
            for side in order:
                yield block, index, pr, pair, side


def manifest_check():
    need(digest(INPUT / 'manifest.json') == MANIFEST_SHA, 'qualification manifest changed')
    need(set(MANIFEST['revisions']) == {'83', '84'}, 'expected both independent release revisions')
    need({key: len(items) for key, items in PROFILES.items()} == {'default': 12, 'api-non-identity': 2},
         'expected 14 original matched leaves')
    leaves = sum(PROFILES.values(), [])
    need(len(set(leaves)) == 14 and set(leaves) == set(MANIFEST['originalShards']), 'leaf inventory mismatch')
    visits = list(schedule())
    counts = Counter((block, pr, pair, side) for block, _, pr, pair, side in visits)
    need(len(visits) == 240 and len(counts) == 24 and set(counts.values()) == {10}, 'incomplete paired schedule')
    for start in range(0, len(visits), 2):
        block, sample, pr, pair, side = visits[start]
        need(visits[start + 1] == (block, sample, pr, pair, 1 - side) and side == int(sample % 2 == 0),
             'arms must alternate and stay adjacent')
    return leaves


def runner():
    OUT.mkdir(parents=True, exist_ok=True)
    need(os.environ.get('RUNNER_KIND') == 'github-hosted' and
         os.environ.get('GITHUB_REPOSITORY') == 'light-speak/luxo' and
         os.environ.get('GITHUB_EVENT_NAME') == 'push' and os.environ.get('GITHUB_REF') == REF,
         'expected a hosted push of the diagnostic branch')
    active = git(ROOT, 'rev-parse', 'HEAD')
    need(active == os.environ.get('GITHUB_SHA') and
         git(ROOT, 'show', '-s', '--format=%P', active) == DIAGNOSTIC_PARENT and
         git(ROOT, 'diff', '--name-status', DIAGNOSTIC_PARENT, active) == 'M\tscripts/diagnose-pr83-pr84-intel/run.py',
         'expected the one-file selector correction on the published diagnostic parent')
    cpu = {line.split(':', 1)[1].strip() for line in Path('/proc/cpuinfo').read_text().splitlines()
           if line.startswith('model name')}
    spec = MANIFEST['runner']
    machine = call('uname', '-sm').decode().strip()
    ubuntu = f'VERSION_ID="{spec["ubuntu"]}"' in Path('/etc/os-release').read_text().splitlines()
    cores = int(call('getconf', '_NPROCESSORS_ONLN'))
    matched = cpu == {spec['model']} and machine == 'Linux x86_64' and ubuntu and cores == spec['cores']
    save_json(OUT / 'runner.json', {'matched': matched, 'cpu': sorted(cpu), 'cores': cores, 'platform': machine,
                                   'ubuntuMatched': ubuntu, 'diagnostic': active, 'scheduledSamples': 0})
    need(matched, 'CPU or Linux image differs from the original runner, no samples taken')


def bound_source(directory, commit, tree):
    need(directory.exists() and git(directory, 'rev-parse', '--show-toplevel') == str(directory) and
         git(directory, 'rev-parse', 'HEAD') == commit and git(directory, 'rev-parse', 'HEAD^{tree}') == tree and
         not git(directory, 'status', '--porcelain'), f'unexpected source checkout: {directory}')


def source_pair(pr, work, evidence):
    spec = MANIFEST['revisions'][pr]
    left, control = (CHECKOUTS / (pr + role) for role in ROLES)
    bound_source(left, spec['base'], spec['baseTree'])
    bound_source(control, spec['head'], spec['headTree'])
    need(git(control, 'show', '-s', '--format=%P', spec['head']).split()[-1] == spec['base'], 'original PR ancestry changed')
    need(git(control, 'diff', '--name-only', spec['base'], spec['head']).splitlines() == spec['productPaths'],
         'original product path list changed')
    right = work / 'head'
    right.mkdir()
    process = subprocess.Popen(['git', '--no-replace-objects', 'archive', spec['head']], cwd=control, stdout=subprocess.PIPE)
    with process.stdout:
        subprocess.run(['tar', '-xf', '-', '-C', right], stdin=process.stdout, check=True)
    need(process.wait() == 0, 'original head archive failed')
    for role, path, commit in (('A', left, spec['base']), ('H', right, spec['head'])):
        (path / '.tmp').mkdir()
        call('sh', control / 'scripts/setup-modules.sh', path, cwd=control)
        source_repo = left if role == 'A' else control
        tree = git(source_repo, 'ls-tree', '-r', commit, PACKAGE, 'go.mod', 'go.sum', 'VERSION')
        (evidence / (role + '-source-tree.txt')).write_text(tree + '\n')
    return left, right, control


def inspect_binary(go, binary, profile, evidence, work):
    evidence.mkdir()
    (evidence / 'elf.sha256').write_text(digest(binary) + '\n')
    pattern = '|'.join(HOT[profile])
    specs = {'buildinfo': (go, 'version', '-m', binary), 'buildid': (go, 'tool', 'buildid', binary),
             'sections': ('readelf', '-WlS', binary), 'hot': (go, 'tool', 'objdump', '-s', pattern, binary)}
    for name, args in specs.items():
        (evidence / (name + '.txt')).write_bytes(call(*args))
    hot = (evidence / 'hot.txt').read_text()
    need(all(marker in hot for marker in HOT[profile] if '|' not in marker), f'missing hot benchmark symbol: {profile}')
    symbols = call(go, 'tool', 'nm', '-size', binary).decode().splitlines()
    (evidence / 'symbols.txt').write_text('\n'.join(line for line in symbols if re.search(pattern, line)) + '\n')
    paths = [work / name for name in ('text', 'rodata')]
    copy = work / 'copy.elf'
    call('objcopy', *(f'--dump-section=.{path.name}={path}' for path in paths), binary, copy)
    save_json(evidence / 'segments.json', {path.name: digest(path) for path in paths})
    for path in (*paths, copy):
        path.unlink()


def sample(benchrun, arm, profile, budget):
    source, binaries = arm
    args = (benchrun, 'run-performance', source, binaries, PACKAGE, profile,
            '-test.run=^$', '-test.bench=' + selector(PROFILES[profile]), '-test.benchmem',
            '-test.benchtime=' + budget, '-test.count=1', '-test.cpu=2')
    try:
        return call(*args)
    except subprocess.CalledProcessError as error:
        (OUT / 'sample-failure.txt').write_bytes(error.output)
        raise


def verify_rows(raw, leaves, count, one_iteration=False):
    parsed = BENCH_RE.findall(raw)
    expected = Counter({name.encode(): count for name in leaves})
    need(Counter(name for name, _ in parsed) == expected, 'missing, duplicated, or unplanned benchmark leaf')
    need(all(int(iters) == 1 if one_iteration else int(iters) > 0 for _, iters in parsed), 'invalid iteration count')
    need(b'FAIL' not in raw.splitlines() and b'PASS' in raw.splitlines(), 'missing successful benchmark completion')
    expected_cpu = MANIFEST['runner']['model'].encode()
    need({line[5:].strip() for line in raw.splitlines() if line.startswith(b'cpu: ')} == {expected_cpu}, 'sample CPU mismatch')


def build_pr(go, pr, work, evidence, wrapper, env):
    work.mkdir()
    evidence.mkdir()
    left, right, control = source_pair(pr, work, evidence)
    benchrun, benchgate, benchplan = (work / n for n in ('benchrun', 'benchgate', 'benchplan'))
    for tool in (benchrun, benchgate, benchplan):
        call(go, 'build', '-p', '1', '-o', tool, './scripts/' + tool.name, cwd=control, env=env)
    compile_profile = call(benchrun, 'performance-profile')
    need(sha256(compile_profile).hexdigest() == MANIFEST['revisions'][pr]['compileProfileSHA256'],
         'original compiler profile differs for PR ' + pr)
    (evidence / 'compile-profile.txt').write_bytes(compile_profile)
    batch = evidence / 'benchmark-batch.tsv'
    batch.write_text(''.join(f'{PACKAGE}\t{profile}\t{selector(items)}\n' for profile, items in PROFILES.items()))
    call(benchplan, '--verify-profile-batch', 'confirmation', OUT / 'expected.json', batch)
    arms = {}
    for role, src, command in (('A', left, 'compile-performance-baseline'), ('H', right, 'compile-performance')):
        binaries = work / (role + '-bin')
        build_log = evidence / (role + '-build')
        build_log.mkdir()
        compile_env = dict(env, PATH=str(wrapper) + os.pathsep + env['PATH'], LUXO_WITNESS_REAL_GO=go,
                           LUXO_WITNESS_PROFILE_LOG=str(build_log), GOWORK=str(src / 'go.work'))
        for profile in PROFILES:
            call(benchrun, command, src, binaries, PACKAGE, profile, env=compile_env)
            suffix = '-' + profile if profile != 'default' else ''
            binary = binaries / ('pkg-lux-api' + suffix + '.test')
            inspect_binary(go, binary, profile, build_log / profile, work)
            capture = sample(benchrun, (src, binaries), profile, '1x')
            verify_rows(capture, PROFILES[profile], 1, one_iteration=True)
            (build_log / (profile + '-inventory.txt')).write_bytes(capture)
        arms[role] = src, binaries
    return arms, benchrun, benchgate, benchplan, control


def measure(built):
    index = 0
    with (OUT / 'order.tsv').open('x') as order:
        order.write('record\tblock\tpair\tpr\tcomparison\tside\trole\tprofile\n')
        for block, pair, pr, comparison, side in schedule():
            arms, benchrun, *_ = built[pr]
            role = comparison[side]
            side_name = ('left', 'right')[side]
            output = OUT / pr / f'{comparison}-{block}.{side_name}.raw'
            with output.open('ab') as stream:
                for profile in PROFILES:
                    index += 1
                    order.write(f'{index}\t{block}\t{pair}\t{pr}\t{comparison}\t{side_name}\t{role}\t{profile}\n')
                    stream.write(sample(benchrun, arms[role], profile, '200ms'))
    need(index == 480, 'expected 480 benchmark process invocations')
    return index


def gate(built, benchstat, leaves):
    expected = OUT / 'expected.json'
    statuses = {}
    for pr, (_, _, benchgate, benchplan, control) in built.items():
        dest = OUT / pr
        for comparison in PAIRS:
            csvs = []
            directions = ('forward', 'reverse') if comparison != 'AH' else ('forward',)
            for block in BLOCKS:
                prefix = dest / f'{comparison}-{block}'
                halves = [Path(str(prefix) + '.' + side) for side in ('left', 'right')]
                output = [path.with_suffix(path.suffix + '.txt') for path in halves]
                for raw, normalized in zip(halves, output):
                    raw = raw.with_suffix(raw.suffix + '.raw')
                    verify_rows(raw.read_bytes(), leaves, 10)
                    call(benchplan, '--normalize', raw, normalized)
                call(benchplan, '--verify', expected, *output)
                for direction in directions:
                    csv = Path(f'{prefix}.{direction}.csv')
                    warnings = Path(f'{prefix}.{direction}.warnings.txt')
                    arms = output if direction == 'forward' else output[::-1]
                    with csv.open('xb') as stream, warnings.open('xb') as errors:
                        subprocess.run([benchstat, '-alpha', '0.01', '-filter', '.unit:(sec/op OR B/op OR allocs/op)',
                                        '-format', 'csv', f'base={arms[0]}', f'head={arms[1]}'],
                                       stdout=stream, stderr=errors, check=True)
                    csvs.append(csv)
            groups = [csvs] if comparison == 'AH' else [[item] for item in csvs]
            opts = ['--approved-costs', control / 'scripts/benchgate/approved-costs.json',
                    '--base-sha', MANIFEST['revisions'][pr]['base']] if comparison == 'AH' else []
            for files in groups:
                with files[0].with_suffix('.gate.txt').open('xb') as log:
                    result = subprocess.run([benchgate, *opts, *files], stdout=log, stderr=subprocess.STDOUT)
                statuses[pr + '/' + files[0].stem] = result.returncode
    return statuses


def main():
    args = sys.argv[1:]
    leaves = manifest_check()
    if args == ['--check']:
        print('14 leaves, 2 independent revisions, 480 scheduled processes, 2 profiles')
        return
    if args == ['--probe']:
        runner()
        return
    need(not args, 'use --check, --probe, or no arguments')
    receipt = json.loads((OUT / 'runner.json').read_bytes())
    need(receipt['matched'] and receipt['diagnostic'] == git(ROOT, 'rev-parse', 'HEAD'), 'missing matching runner receipt')
    go = shutil.which('go')
    need(go, 'missing Go compiler')
    env = dict(os.environ, GOWORK='off')
    fields = ('GOVERSION', 'GOHOSTOS', 'GOHOSTARCH', 'GOOS', 'GOARCH', 'GOAMD64', 'CGO_ENABLED', 'GOFLAGS', 'GOTOOLCHAIN')
    version = call(go, 'env', *fields, env=env)
    spec = MANIFEST['runner']
    wanted = f'go{spec["go"]}\nlinux\namd64\nlinux\namd64\n{spec["goamd64"]}\n{spec["cgo"]}\n\nlocal\n'.encode()
    need(version == wanted, 'Go build settings differ from the original')
    save_json(OUT / 'binding.json', {'manifest': MANIFEST, 'goEnvironment': version.decode().splitlines(),
                                    'goSHA256': digest(Path(go)), 'kernel': call('uname', '-a').decode().strip()})
    rows = [{'Package': PACKAGE, 'Name': leaf} for leaf in sorted(leaves)]
    save_json(OUT / 'expected.json', {'Base': rows, 'Head': rows})
    with tempfile.TemporaryDirectory(prefix='intel-work.', dir=OUT.parent) as scratch:
        work = Path(scratch)
        wrapper = work / 'wrapper'
        wrapper.mkdir()
        shutil.copyfile(INPUT / 'go-capture.py', wrapper / 'go')
        (wrapper / 'go').chmod(0o700)
        environment = dict(env, TMPDIR=str(work), GOBIN=str(work))
        call(go, 'install', 'golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70', env=environment)
        built = {pr: build_pr(go, pr, work / pr, OUT / pr, wrapper, environment) for pr in ('83', '84')}
        count = measure(built)
        statuses = gate(built, work / 'benchstat', leaves)
    save_json(OUT / 'status.json', {'processes': count, 'gates': statuses})
    sys.exit(int(any(statuses.values())))


if __name__ == '__main__':
    main()
