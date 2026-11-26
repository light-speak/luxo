#!/usr/bin/env python3
"""Run a matched native attribution subset without changing the release gate."""

from base64 import b64decode
from collections import Counter, defaultdict
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
RUN_CONFIG_SHA = '3a08f280cfacaa0698f0347e392dc21b9a197377a321ed5f5c7c13b64c69bfdc'
DIAGNOSTIC_PARENT = 'd586d4283c290501fb9f4e911996be13aa6945d7'
ORIGINAL_WITNESS = '5d63fd896bcc88aa35968e986985bb387e7de661'
DIAGNOSTIC_REF = 'refs/heads/diagnostics/pr158-intel-layout'
CONFIG = json.loads((INPUT / 'routes.json').read_bytes())
FAMILY_MANIFEST = INPUT / 'families.json'
FAMILIES = json.loads(FAMILY_MANIFEST.read_bytes())['packages']
FOLLOWUP_PATHS = (
    'M	.github/workflows/diagnose-pr158-attribution.yml',
    'A	scripts/diagnose-pr158/families.json',
    'A	scripts/diagnose-pr158/project.go',
    'M	scripts/diagnose-pr158/routes.json',
    'M	scripts/diagnose-pr158/run.py',
)
PAIRS = ('AA', 'BB', 'CC', 'AB', 'BC', 'AC')
METRICS = '.unit:(sec/op OR B/op OR allocs/op)'
HOT = {
    'pkg/lux/api': r'BenchmarkStaticValueParams|benchmarkStaticValue|ParamDateTimeArray|readParam(Value|Array)|findParam',
    'pkg/lux/rpc': r'BenchmarkLRPCConnectionFrameReuseCandidate|prepareReusedFrameCandidate|writePreparedFrame|retainReadFrame|appendCallBody|appendEnvelopePrefix',
    'pkg/lux/pg': r'BenchmarkDurationWriteReject|bindArguments?|validateDuration(List|Argument)|ValidateDuration',
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


def select_route(requested, cpu):
    routes = CONFIG['routes']
    if requested != 'auto':
        return requested if routes[requested]['cpu'] == cpu else None
    matches = [name for name, route in routes.items() if route['cpu'] == cpu]
    need(len(matches) <= 1, 'the CPU identifies more than one original route')
    return matches[0] if matches else None


def route_guard(requested):
    OUT.mkdir(parents=True, exist_ok=True)
    models = {line.split(':', 1)[1].strip() for line in Path('/proc/cpuinfo').read_text().splitlines()
              if line.startswith('model name')}
    need(len(models) == 1, 'expected one native CPU model')
    cpu = models.pop()
    (OUT / 'cpu.txt').write_text(cpu + '\n')
    need(os.environ.get('RUNNER_KIND') == 'github-hosted', 'expected a hosted runner')
    need(os.environ.get('GITHUB_EVENT_NAME') == 'push' and os.environ.get('GITHUB_REF') == DIAGNOSTIC_REF and
         os.environ.get('GITHUB_REPOSITORY') == 'light-speak/luxo', 'expected the diagnostic branch push')
    need(digest(INPUT / 'routes.json') == RUN_CONFIG_SHA, 'witness route manifest changed')
    selected = select_route(requested, cpu)
    if selected is None:
        expected = [route['cpu'] for route in CONFIG['routes'].values()] if requested == 'auto' else [CONFIG['routes'][requested]['cpu']]
        json_file(OUT / 'status.json', {'matched': False, 'requestedRoute': requested, 'expectedCPUs': expected,
                                       'cpu': cpu, 'processes': 0})
        raise SystemExit(3)
    need('VERSION_ID="24.04"' in Path('/etc/os-release').read_text() and call('getconf', '_NPROCESSORS_ONLN').strip() == b'4',
         'expected Ubuntu 24.04 on four CPU cores')
    verify_provenance()
    return selected, cpu


def verify_provenance():
    active = git('rev-parse', 'HEAD')
    need(active == os.environ.get('GITHUB_SHA') and git('show', '-s', '--format=%P', active) == DIAGNOSTIC_PARENT,
         'the diagnostic must append one commit to the published witness')
    need(git('show', '-s', '--format=%P', DIAGNOSTIC_PARENT) == ORIGINAL_WITNESS and
         git('show', '-s', '--format=%P', ORIGINAL_WITNESS) == CONFIG['candidateCommit'],
         'the published witness must descend from the historical candidate')
    need(git('show', '-s', '--format=%P', CONFIG['candidateCommit']) == CONFIG['baseCommit'], 'target ancestry changed')
    need(git('diff', '--name-status', DIAGNOSTIC_PARENT, active).splitlines() == list(FOLLOWUP_PATHS),
         'unexpected layout diagnostic revision paths')
    need(digest(FAMILY_MANIFEST) == CONFIG['familyManifestSHA256'], 'signed family inventory changed')
    need(digest(INPUT / 'project.go') == CONFIG['projectorSHA256'], 'verified fixture projector changed')
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


def compiler_env(go, source, work, evidence):
    return dict(os.environ, PATH=str(work / 'wrapper') + os.pathsep + os.environ['PATH'],
                LUXO_WITNESS_REAL_GO=go, LUXO_WITNESS_PROFILE_LOG=str(evidence), GOWORK=str(source / 'go.work'))


def compile_projected(tools, source, work, package, binary, name, stock, env):
    capture = json.loads((stock / (name + '.overlay.json')).read_bytes())
    need(all(not replacement for replacement in capture.values()), f'unexpected stock replacement for {package}')
    overlay = work / (binary.parent.name + '-' + name + '.json')
    json_file(overlay, {'Replace': {str(source / path): '' for path in capture}})
    projection = work / (binary.parent.name + '-' + name + '-overlay')
    projected = call(tools / 'project', source, package.rsplit('/', 1)[-1], overlay, projection).decode().strip()
    need(projected == str(projection / 'overlay.json'), 'unexpected projection path')
    call(work / 'wrapper' / 'go', 'test', '-trimpath', '-c', '-overlay=' + projected, './' + package, '-o', binary,
         cwd=source, env=env)
    roots = call(binary, '-test.list=.*', cwd=source / package).decode().splitlines()
    need(roots == [FAMILIES[package]['root']], f'{package} projection retained an unrelated benchmark or test')


def check_inventory(smoke, package):
    family = FAMILIES[package]
    output = smoke.decode()
    all_rows = [line for line in output.splitlines() if line.startswith('Benchmark')]
    rows = re.findall(r'^(Benchmark\S+-2)\s+1\s+\S+\s+ns/op\s+\S+\s+B/op\s+\S+\s+allocs/op$', output, re.M)
    need(output.rstrip().endswith('\nPASS') and len(all_rows) == len(rows) and Counter(rows) == Counter(family['names']),
         f"{package} fixture does not reproduce every signed {family['root']} leaf exactly once")


def build_profile(go, tools, mode, role, source, work, grouped):
    built = work / (mode + '-' + role + '-binaries')
    built.mkdir()
    evidence = OUT / mode / (role + '-build')
    evidence.mkdir()
    env = compiler_env(go, source, work, evidence)
    built_profiles = {}
    command = 'compile-performance' if role == 'C' else 'compile-performance-baseline'
    (evidence / 'modules.txt').write_bytes(call(go, 'list', '-m', 'all', cwd=source, env=env))
    for package, profile in grouped:
        name = package.replace('/', '-') + (f'-{profile}' if profile != 'default' else '')
        binary = built / (name + '.test')
        if mode == 'stock':
            call(tools / 'benchrun', command, source, built, package, profile, env=env)
        else:
            compile_projected(tools, source, work, package, binary, name, OUT / 'stock' / (role + '-build'), env)
        elf(go, binary, evidence / name, package, work)
        smoke = sample(binary, source / package, '^' + FAMILIES[package]['root'] + '$', '1x')
        check_inventory(smoke, package)
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


def measure(directory, arms, bins, grouped):
    process = 0
    with (directory / 'order.tsv').open('x') as order:
        for block, pair, code, visit in schedule():
            for slot in visit:
                role, direction = code[slot], ('left', 'right')[slot]
                output = directory / f'{code}-{block}.{direction}.raw'
                with output.open('ab') as stream:
                    for (package, profile), cases in grouped.items():
                        binary = bins[role][package, profile]
                        process += 1
                        order.write(f'{process}\t{block}\t{pair}\t{code}\t{role}\t{direction}\t{package}\t{profile}\n')
                        stream.write(sample(binary, arms[role] / package, filter_for(cases), '200ms'))
    need(process == 240 * len(grouped), 'wrong count of benchmark processes')
    return process


def gate(directory, tools, expected):
    approvals = ROOT / 'scripts/benchgate/approved-costs.json'
    statuses = {}
    with (directory / 'warnings.txt').open('x') as warnings:
        for code in PAIRS:
            csvs = []
            for block in (1, 2):
                prefix = directory / f'{code}-{block}'
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


def compare(go, tools, mode, work, arms, grouped, expected):
    folder = OUT / mode
    folder.mkdir()
    binaries = {role: build_profile(go, tools, mode, role, source, work, grouped) for role, source in arms.items()}
    processes = measure(folder, arms, binaries, grouped)
    return {'processes': processes, 'gates': gate(folder, tools, expected)}


def summarize(folder):
    statuses = sorted(folder.glob('*/status.json'))
    need(len(statuses) == 4, 'expected receipts from four independent runner slots')
    matched, failures = 0, []
    gate_names = {f'{pair}-1' for pair in PAIRS if pair[0] != pair[1]}
    gate_names.update(f'{pair}-{group}{suffix}' for pair in PAIRS if pair[0] == pair[1]
                      for group, suffix in product((1, 2), ('', '.reverse')))
    for status in statuses:
        result = json.loads(status.read_bytes())
        if not result['matched']:
            need(result['processes'] == 0 and result['cpu'] != CONFIG['routes']['intel']['cpu'],
                 'an unverified runner produced samples')
            continue
        matched += 1
        need(result['cpu'] == CONFIG['routes']['intel']['cpu'] and result['route'] == 'intel', 'wrong runner receipt')
        need(set(result['comparisons']) == {'stock', 'projected'}, 'both comparison receipts are required')
        for mode, comparison in result['comparisons'].items():
            need(comparison['processes'] == 720 and set(comparison['gates']) == gate_names,
                 f'incomplete {mode} receipt')
            if any(comparison['gates'].values()):
                failures.append((status.parent.name, mode))
    print(json.dumps({'matchedSlots': matched, 'failedComparisons': failures}, sort_keys=True))
    need(matched > 0, 'no original Intel 6975P-C measurement')
    need(not failures, 'a matched runner reported a strict gate failure')


def main():
    args = sys.argv[1:]
    if len(args) == 2 and args[0] == '--summarize':
        summarize(Path(args[1]))
        return
    probe = args[:1] == ['--probe']
    if probe:
        args = args[1:]
    need(args == ['intel'], 'expected --probe intel, intel, or --summarize PATH')
    selected, cpu = route_guard(args[0])
    if probe:
        print(selected)
        return
    route = CONFIG['routes'][selected]
    go = shutil.which('go')
    need(go, 'missing Go compiler')
    fields = ('GOVERSION', 'GOHOSTOS', 'GOHOSTARCH', 'GOOS', 'GOARCH', 'GOAMD64', 'CGO_ENABLED', 'GOFLAGS', 'GOTOOLCHAIN')
    version = call(go, 'env', *fields)
    need(version == b'go1.27.4\nlinux\namd64\nlinux\namd64\nv1\n1\n\nlocal\n', 'wrong native toolchain or flags')
    json_file(OUT / 'binding.json', {'inputs': CONFIG, 'route': selected, 'cpu': cpu,
                                    'diagnosticCommit': os.environ['GITHUB_SHA'], 'diagnosticParent': DIAGNOSTIC_PARENT,
                                    'goEnvironment': version.decode().splitlines(), 'goSHA256': digest(Path(go)),
                                    'runner': call('uname', '-a').decode().strip()})
    grouped = group_cases(route['cases'])
    rows = sorted(({'Package': item['package'], 'Name': item['name']} for item in route['cases']),
                  key=lambda item: (item['Package'], item['Name']))
    expected = OUT / 'expected.json'
    json_file(expected, {'Base': rows, 'Head': rows})
    with tempfile.TemporaryDirectory(prefix='pr158-witness.', dir=OUT.parent) as temp:
        work = Path(temp)
        tools, wrapper, gotmp = work / 'bin', work / 'wrapper', work / 'go-tmp'
        for path in (tools, wrapper, gotmp):
            path.mkdir()
        os.environ.update(TMPDIR=str(gotmp), GOTMPDIR=str(gotmp))
        shutil.copyfile(INPUT / 'go-capture.py', wrapper / 'go')
        (wrapper / 'go').chmod(0o700)
        env = dict(os.environ, GOWORK='off')
        for name in ('benchrun', 'benchplan', 'benchgate'):
            call(go, 'build', '-p', '1', '-o', tools / name, './scripts/' + name, env=env)
        call(go, 'build', '-p', '1', '-o', tools / 'project', INPUT / 'project.go', env=env)
        call(go, 'install', 'golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70', env=dict(env, GOBIN=str(tools)))
        profile = call(tools / 'benchrun', 'performance-profile')
        need(sha256(profile).hexdigest() == CONFIG['nativeCompileProfileSHA256'], 'original benchmark compile profile changed')
        (OUT / 'compile-profile.txt').write_bytes(profile)
        arms = materialize(work)
        comparisons = {'stock': compare(go, tools, 'stock', work, arms, grouped, expected)}
        controls = [code for name, code in comparisons['stock']['gates'].items() if name[:2] in ('AA', 'BB', 'CC')]
        if not any(controls):
            comparisons['projected'] = compare(go, tools, 'projected', work, arms, grouped, expected)
    json_file(OUT / 'status.json', {'matched': True, 'route': selected, 'cpu': cpu, 'comparisons': comparisons})
    # A successful projection does not change a stock gate's result.
    sys.exit(int(any(code for phase in comparisons.values() for code in phase['gates'].values())))


if __name__ == '__main__':
    main()
