#!/usr/bin/env python3
"""Run a matched native attribution subset without changing the release gate."""

from base64 import b64decode
from collections import defaultdict
from hashlib import sha256
from itertools import product
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


INPUT = Path(__file__).resolve().parent
ROOT = INPUT.parents[1]
OUT = ROOT / '.tmp/pr158-attribution'
RUN_CONFIG_SHA = 'bf3be34db7cca43489c678e725e5baa66ca03e323c4a282df9e8650db93cdc81'
CONFIG = json.loads((INPUT / 'routes.json').read_bytes())
PATHS = ('.github/workflows/diagnose-pr158-attribution.yml',
         *('scripts/diagnose-pr158/' + name for name in ('arm-b.patch.b64', 'go-capture.py', 'routes.json', 'run.py')))
PAIRS = ('AA', 'BB', 'CC', 'AB', 'BC', 'AC')
METRICS = '.unit:(sec/op OR B/op OR allocs/op)'
HOT = {
    'pkg/lux/api': r'BenchmarkStaticValueParams|BinaryArray|ReadDateTime',
    'pkg/lux/rpc': r'BenchmarkLRPCConnectionFrameReuseCandidate|prepareReusedFrameCandidate|writePreparedFrame',
    'pkg/lux/pg': r'BenchmarkDurationWriteReject|[Dd]uration',
    'pkg/lux/luvia': r'BenchmarkAuthUpgradeHeaders|AuthMiddleware|isWebSocketUpgrade',
    'pkg/codegen': r'BenchmarkCollectionLengthCompiler|compileCollectionLength|compileString',
    'pkg/lux/h2transport': r'BenchmarkPriorKnowledgeConnectionLifecycle|runPriorKnowledgeConnection|ClientConn.*RoundTrip|\.newClientConn|\.ReadFrame|\.processWindowUpdate',
}


def need(value, error):
    if not value:
        raise RuntimeError(error)


def digest(file):
    return sha256(file.read_bytes()).hexdigest()


def call(*args, cwd=ROOT, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env)


def git(*args, env=None):
    return call('git', '--no-replace-objects', *args, env=env).decode().strip()


def json_file(file, value):
    with file.open('x') as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write('\n')


def route_guard(route):
    OUT.mkdir(parents=True)
    cpu = next(line.split(':', 1)[1].strip() for line in Path('/proc/cpuinfo').read_text().splitlines()
               if line.startswith('model name'))
    (OUT / 'cpu.txt').write_text(cpu + '\n')
    need(os.environ.get('RUNNER_KIND') == 'github-hosted', 'expected a hosted runner')
    need(digest(INPUT / 'routes.json') == RUN_CONFIG_SHA, 'witness route manifest changed')
    if cpu != route['cpu']:
        json_file(OUT / 'status.json', {'matched': False, 'expectedCPU': route['cpu'], 'cpu': cpu, 'processes': 0})
        raise SystemExit(3)
    need('VERSION_ID="24.04"' in Path('/etc/os-release').read_text() and call('getconf', '_NPROCESSORS_ONLN').strip() == b'4',
         'expected Ubuntu 24.04 on four CPU cores')
    active = git('rev-parse', 'HEAD')
    need(active == os.environ['GITHUB_SHA'] and git('show', '-s', '--format=%P', active) == CONFIG['candidateCommit'],
         'the diagnostic must be one commit on the published historical candidate')
    need(git('show', '-s', '--format=%P', CONFIG['candidateCommit']) == CONFIG['baseCommit'], 'target ancestry changed')
    need(git('diff', '--name-only', CONFIG['candidateCommit'], active).splitlines() == list(PATHS), 'unexpected diagnostic paths')
    for role, commit in (('A', CONFIG['baseCommit']), ('C', CONFIG['candidateCommit'])):
        need(git('rev-parse', f'{commit}^{{tree}}') == CONFIG['trees'][role], f'{role} source identity changed')


def materialize(work):
    env = dict(os.environ, GIT_INDEX_FILE=str(work / 'B.index'))
    patch = work / 'arm-b.patch'
    encoded = b''.join((INPUT / 'arm-b.patch.b64').read_bytes().split())
    data = b64decode(encoded, validate=True)
    need(sha256(data).hexdigest() == CONFIG['pinOnlyPatchSHA256'], 'pin-only source patch changed')
    patch.write_bytes(data)
    git('read-tree', CONFIG['baseCommit'], env=env)
    git('apply', '--cached', '--check', patch, env=env)
    git('apply', '--cached', patch, env=env)
    need(git('write-tree', env=env) == CONFIG['trees']['B'], 'wrong pin-only tree')
    base = OUT.parent / 'pr158-base'
    actual_base = git('-C', base, 'rev-parse', 'HEAD')
    need(actual_base == CONFIG['baseCommit'] and git('-C', base, 'rev-parse', '--show-toplevel') == str(base) and
         not git('-C', base, 'status', '--porcelain'), 'baseline checkout differs from the exact original input')
    arms = {'A': base}
    for role in ('B', 'C'):
        tree = CONFIG['trees'][role]
        directory = work / role
        directory.mkdir()
        process = subprocess.Popen(['git', '--no-replace-objects', 'archive', tree], cwd=ROOT, stdout=subprocess.PIPE)
        with process.stdout:
            subprocess.run(['tar', '-xf', '-', '-C', directory], stdin=process.stdout, check=True)
        need(process.wait() == 0, f'{role} archive failed')
        arms[role] = directory
    for directory in arms.values():
        (directory / '.tmp').mkdir()
        call('sh', ROOT / 'scripts/setup-modules.sh', directory)
    return arms


def filter_for(cases):
    def part(text):
        return '^' + re.sub(r'([\\.+*?()|\[\]{}^$])', r'\\\1', text) + '$'

    return '|'.join('/'.join(part(piece) for piece in case['name'].removesuffix('-2').split('/')) for case in cases)


def group_cases(cases):
    grouped = defaultdict(list)
    for case in cases:
        grouped[case['package'], case['profile']].append(case)
    return grouped


def elf(go, binary, target, package, work):
    target.mkdir()
    (target / 'elf.sha256').write_text(digest(binary) + '\n')
    for name, args in (('buildinfo', (go, 'version', '-m', binary)), ('buildid', (go, 'tool', 'buildid', binary)),
                       ('sections', ('readelf', '-WlS', binary)), ('hot', (go, 'tool', 'objdump', '-s', HOT[package], binary))):
        (target / (name + '.txt')).write_bytes(call(*args))
    symbols = call(go, 'tool', 'nm', '-size', binary).decode().splitlines()
    (target / 'symbols.txt').write_text('\n'.join(line for line in symbols if re.search(HOT[package], line)) + '\n')
    need((target / 'hot.txt').stat().st_size, f'empty hot instructions for {package}')
    segment_paths = [work / section for section in ('text', 'rodata')]
    call('objcopy', *(f'--dump-section=.{part.name}={part}' for part in segment_paths), binary, work / 'copy.elf')
    json_file(target / 'segments.json', {part.name: digest(part) for part in segment_paths})
    for path in (*segment_paths, work / 'copy.elf'):
        path.unlink()


def build_profile(go, tools, role, source, work, grouped):
    built = work / (role + '-binaries')
    evidence = OUT / (role + '-build')
    evidence.mkdir()
    wrapper = work / 'wrapper'
    env = dict(os.environ, PATH=str(wrapper) + os.pathsep + os.environ['PATH'], LUXO_WITNESS_REAL_GO=go,
               LUXO_WITNESS_PROFILE_LOG=str(evidence), GOWORK=str(source / 'go.work'))
    built_profiles = {}
    mode = 'compile-performance' if role == 'C' else 'compile-performance-baseline'
    (evidence / 'modules.txt').write_bytes(call(go, 'list', '-m', 'all', cwd=source, env=env))
    for (package, profile), cases in grouped.items():
        call(tools / 'benchrun', mode, source, built, package, profile, env=env)
        name = package.replace('/', '-') + (f'-{profile}' if profile != 'default' else '')
        binary = built / (name + '.test')
        elf(go, binary, evidence / name, package, work)
        selector = filter_for(cases)
        smoke = sample(binary, source / package, selector, '1x')
        expected = [case['name'] for case in cases]
        observed = re.findall(r'^(Benchmark\S+-2)\s+1\s+', smoke.decode(), re.M)
        need(sorted(observed) == sorted(expected) and b'\nPASS\n' in smoke, f'{role} fixture differs for {name}')
        (evidence / (name + '.inventory.txt')).write_bytes(smoke)
        built_profiles[package, profile] = binary
    return built_profiles


def sample(binary, directory, selector, budget):
    return call(binary, '-test.run=^$', '-test.bench=' + selector, '-test.benchmem', '-test.benchtime=' + budget,
                '-test.count=1', '-test.cpu=2', cwd=directory)


def schedule():
    for block, pair in product((1, 2), range(1, 11)):
        for code in PAIRS:
            visit = (0, 1) if (block + pair) % 2 == 0 else (1, 0)
            yield block, pair, code, visit


def measure(arms, bins, grouped):
    process = 0
    with (OUT / 'order.tsv').open('x') as order:
        for block, pair, code, visit in schedule():
            for slot in visit:
                role, direction = code[slot], ('left', 'right')[slot]
                output = OUT / f'{code}-{block}.{direction}.raw'
                with output.open('ab') as stream:
                    for (package, profile), cases in grouped.items():
                        binary = bins[role][package, profile]
                        process += 1
                        order.write(f'{process}\t{block}\t{pair}\t{code}\t{role}\t{direction}\t{package}\t{profile}\n')
                        stream.write(sample(binary, arms[role] / package, filter_for(cases), '200ms'))
    need(process == 240 * len(grouped), 'wrong count of benchmark processes')
    return process


def gate(tools, expected):
    approvals = ROOT / 'scripts/benchgate/approved-costs.json'
    statuses = {}
    with (OUT / 'warnings.txt').open('x') as warnings:
        for code in PAIRS:
            csvs = []
            for block in (1, 2):
                prefix = OUT / f'{code}-{block}'
                files = [Path(str(prefix) + '.' + label + '.txt') for label in ('left', 'right')]
                for file in files:
                    call(tools / 'benchplan', '--normalize', file.with_suffix('.raw'), file)
                call(tools / 'benchplan', '--verify', expected, *files)
                for reverse in ((False, True) if code[0] == code[1] else (False,)):
                    order = files[::-1] if reverse else files
                    csv = Path(str(prefix) + ('.reverse.csv' if reverse else '.csv'))
                    with csv.open('wb') as stream:
                        subprocess.run([tools / 'benchstat', '-alpha', '0.01', '-filter', METRICS, '-format', 'csv',
                                        f'base={order[0]}', f'head={order[1]}'], stdout=stream, stderr=warnings, check=True)
                    csvs.append(csv)
            gate_groups = [(csv,) for csv in csvs] if code[0] == code[1] else [csvs]
            for files in gate_groups:
                opts = ['--approved-costs', approvals, '--base-sha', CONFIG['baseCommit']] if code == 'AC' else []
                with files[0].with_suffix('.gate.txt').open('wb') as stream:
                    process = subprocess.run([tools / 'benchgate', *opts, *files], stdout=stream, stderr=subprocess.STDOUT)
                statuses[files[0].stem] = process.returncode
    return statuses


def main():
    need(len(sys.argv) == 2 and sys.argv[1] in CONFIG['routes'], 'expected the Intel or EPYC route')
    route = CONFIG['routes'][sys.argv[1]]
    route_guard(route)
    go = shutil.which('go')
    need(go, 'missing Go compiler')
    fields = ('GOVERSION', 'GOHOSTOS', 'GOHOSTARCH', 'GOOS', 'GOARCH', 'GOAMD64', 'CGO_ENABLED', 'GOFLAGS', 'GOTOOLCHAIN')
    version = call(go, 'env', *fields)
    need(version == b'go1.27.4\nlinux\namd64\nlinux\namd64\nv1\n1\n\nlocal\n', 'wrong native toolchain or flags')
    json_file(OUT / 'binding.json', {'inputs': CONFIG, 'route': sys.argv[1], 'goEnvironment': version.decode().splitlines(),
                                    'goSHA256': digest(Path(go)), 'runner': call('uname', '-a').decode().strip()})
    grouped = group_cases(route['cases'])
    rows = sorted(({'Package': item['package'], 'Name': item['name']} for item in route['cases']),
                  key=lambda item: (item['Package'], item['Name']))
    expected = OUT / 'expected.json'
    json_file(expected, {'Base': rows, 'Head': rows})
    with tempfile.TemporaryDirectory(prefix='pr158-witness.', dir=OUT.parent) as temp:
        work = Path(temp)
        tools, wrapper = work / 'bin', work / 'wrapper'
        tools.mkdir()
        wrapper.mkdir()
        shutil.copyfile(INPUT / 'go-capture.py', wrapper / 'go')
        (wrapper / 'go').chmod(0o700)
        env = dict(os.environ, GOWORK='off')
        for name in ('benchrun', 'benchplan', 'benchgate'):
            call(go, 'build', '-p', '1', '-o', tools / name, './scripts/' + name, env=env)
        call(go, 'install', 'golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70', env=dict(env, GOBIN=str(tools)))
        profile = call(tools / 'benchrun', 'performance-profile')
        need(sha256(profile).hexdigest() == CONFIG['nativeCompileProfileSHA256'], 'original benchmark compile profile changed')
        (OUT / 'compile-profile.txt').write_bytes(profile)
        arms = materialize(work)
        binaries = {role: build_profile(go, tools, role, source, work, grouped) for role, source in arms.items()}
        processes = measure(arms, binaries, grouped)
        outcomes = gate(tools, expected)
    json_file(OUT / 'status.json', {'matched': True, 'processes': processes, 'gates': outcomes})
    sys.exit(int(any(outcomes.values())))


if __name__ == '__main__':
    main()
