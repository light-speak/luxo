#!/usr/bin/env python3
"""Replay the remaining signed #84 leaves without changing the release gate."""

from collections import Counter, defaultdict
from hashlib import sha256
from itertools import product
from math import isfinite
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from tempfile import TemporaryDirectory

INPUT = Path(__file__).resolve().parent
ROOT = INPUT.parents[1]
OUT = ROOT / '.tmp/pr84-native-evidence'
CHECKOUTS = ROOT / '.tmp/pr84-native-inputs'
MANIFEST = INPUT / 'remaining.json'
CONFIG = json.loads(MANIFEST.read_bytes())
MANIFEST_SHA = '590cd78408d600323a61e7df8d186c970c022a5d78cb8ac13acdb8c3903e6bd4'
PROJECTOR_SHA = '05d4710fbb50682462275be5597f9ab533779aa335df7e8187684233b7b3446a'
PARENT = '5d4dd6725ae111debcd5d768266ae46cdfc320fb'
REF = 'refs/heads/diagnostics/pr84-original-layout'
PACKAGE = 'pkg/lux/api'
ROLES = ('A', 'H')
PAIRS = ('AA', 'HH', 'AH')
CONTROLS = {f'{pair}-{block}.{direction}' for pair, block, direction in
            product(('AA', 'HH'), (1, 2), ('forward', 'reverse'))}
BENCH_ROW = re.compile(rb'^(Benchmark\S+-2)[ \t]+(\d+)[ \t]+([^\r\n]+)$', re.M)
HOT = {
    'handler-buffer': r'BenchmarkHandlerResponseBuffer|ResponseBuffer',
    'identity-subject': r'BenchmarkIdentitySubjectVisibility|identitySubject',
    'origin-run': r'BenchmarkResponseOriginRun|responseOrigin.*run',
    'http-completion': r'BenchmarkHTTPHandlerCompletion|benchmarkHTTPHandlerCompletion|executeHTTPHandler',
    'ws-auth-expired': r'BenchmarkWSAuthenticationExpiredParent|benchmarkWSExpiredParent|wsConn.*write',
    'request-values': r'BenchmarkStaticParamSlot|BenchmarkStaticValueParams|benchmarkStaticValue|LegacyParamJSON|findParam|SetParamSlot',
    'body-read': r'BenchmarkBodyRead|benchmarkBodyRead|[Rr]eadBody',
    'json-quote': r'BenchmarkJSONQuoteCandidate|appendStrict|[Aa]ppendJSONString',
}


def need(ok, problem):
    if not ok:
        raise RuntimeError(problem)


def digest(path):
    return sha256(path.read_bytes()).hexdigest()


def call(*args, cwd=ROOT, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env)


def git(directory, *args):
    return call('git', '--no-replace-objects', '-C', directory, *args).decode().strip()


def save(path, value):
    with path.open('x') as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write('\n')


def requested_shards(ref):
    known = {route['shard'] for route in CONFIG['routes']}
    if ref == REF:
        return known
    shard = ref.removeprefix(REF+'-')
    need(shard in known and ref == REF+'-'+shard, 'unexpected diagnostic route ref')
    return {shard}


def select_route(cpu, requested):
    found = [route for route in CONFIG['routes'] if route['cpu'] == cpu and route['shard'] in requested]
    need(len(found) <= 1, 'ambiguous CPU route')
    return found[0] if found else None


def check_runner():
    OUT.mkdir(parents=True, exist_ok=True)
    need(digest(MANIFEST) == MANIFEST_SHA and digest(INPUT/'project.go') == PROJECTOR_SHA, 'witness inputs changed')
    active = git(ROOT, 'rev-parse', 'HEAD')
    need(os.environ.get('RUNNER_KIND') == 'github-hosted' and os.environ.get('GITHUB_REPOSITORY') == 'light-speak/luxo'
         and os.environ.get('GITHUB_EVENT_NAME') == 'push'
         and os.environ.get('GITHUB_SHA') == active, 'unexpected diagnostic event')
    requested = requested_shards(os.environ['GITHUB_REF'])
    need(git(ROOT, 'show', '-s', '--format=%P', active) == PARENT, 'unexpected diagnostic parent')
    need(git(ROOT, 'diff', '--name-status', PARENT, active).splitlines() == [
        'M\t.github/workflows/diagnose-pr83-pr84-intel.yml',
        'A\tscripts/diagnose-pr83-pr84-intel/project.go',
        'A\tscripts/diagnose-pr83-pr84-intel/remaining.json',
        'M\tscripts/diagnose-pr83-pr84-intel/run.py',
    ], 'unexpected diagnostic diff')
    cpus = {line.split(':', 1)[1].strip() for line in Path('/proc/cpuinfo').read_text().splitlines()
            if line.startswith('model name')}
    need(len(cpus) == 1, 'CPU descriptions missing or inconsistent')
    cpu = cpus.pop()
    route = select_route(cpu, requested)
    if route is not None:
        need(call('uname', '-sm').decode().strip() == 'Linux x86_64' and
             'VERSION_ID="24.04"' in Path('/etc/os-release').read_text().splitlines() and
             call('getconf', '_NPROCESSORS_ONLN').strip() == b'4', 'unexpected native runner image')
    receipt = {'matched': route is not None, 'cpu': cpu, 'shard': route['shard'] if route else None, 'diagnostic': active}
    if not (OUT/'route.json').is_file():
        save(OUT/'route.json', receipt)
    else:
        need(json.loads((OUT/'route.json').read_bytes()) == receipt, 'runner scope changed after probe')
    return route


def bind_sources(go):
    arms = {role: CHECKOUTS/role for role in ROLES}
    for role, source in arms.items():
        commit = CONFIG['base' if role == 'A' else 'head']
        need(git(source, 'rev-parse', 'HEAD') == commit and git(source, 'rev-parse', 'HEAD^{tree}') == CONFIG[role]
             and git(source, 'rev-parse', '--show-toplevel') == str(source)
             and not git(source, 'status', '--porcelain'), f'wrong {role} source checkout')
    head = arms['H']
    need(git(head, 'show', '-s', '--format=%P', 'HEAD').split()[-1] == CONFIG['base'], 'historic merge base changed')
    need(git(head, 'diff', '--name-only', CONFIG['base'], 'HEAD').splitlines() ==
         [PACKAGE+'/trace.go', PACKAGE+'/trace_encoding_test.go'], 'wrong original product diff')
    fields = ('GOVERSION','GOHOSTOS','GOHOSTARCH','GOOS','GOARCH','GOAMD64','CGO_ENABLED','GOFLAGS','GOTOOLCHAIN')
    environment = call(go, 'env', *fields)
    need(environment == b'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1\n\nlocal\n', 'wrong original Go environment')
    save(OUT/'binding.json', {'original': CONFIG, 'goEnvironment': environment.decode().splitlines(),
                              'goSHA256': digest(Path(go)), 'runner': json.loads((OUT/'route.json').read_bytes())})
    for arm in arms.values():
        (arm/'.tmp').mkdir()
        call('sh', head/'scripts/setup-modules.sh', arm, cwd=head)
    need((arms['A']/'go.work').read_bytes() == (head/'go.work').read_bytes(), 'workspace differs between arms')
    return arms


def build_tools(go, head, work):
    tools, wrapper, temp = work/'bin', work/'wrapper', work/'go-tmp'
    for path in (tools, wrapper, temp):
        path.mkdir()
    os.environ.update(TMPDIR=str(temp), GOTMPDIR=str(temp))
    env = dict(os.environ, GOWORK='off')
    for name in ('benchrun','benchplan','benchgate'):
        call(go, 'build', '-p', '1', '-o', tools/name, './scripts/'+name, cwd=head, env=env)
    call(go, 'build', '-p', '1', '-o', tools/'project', INPUT/'project.go', cwd=head, env=env)
    call(go, 'install', 'golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70',
         cwd=head, env=dict(env, GOBIN=str(tools)))
    original = call(tools/'benchrun', 'performance-profile')
    need(sha256(original).hexdigest() == CONFIG['compileProfileSHA256'], 'official compile profile changed')
    (OUT/'compile-profile.txt').write_bytes(original)
    shutil.copyfile(INPUT/'go-capture.py', wrapper/'go')
    (wrapper/'go').chmod(0o700)
    return tools


def plan_subset(benchplan, expected, names, destination):
    destination.mkdir()
    targets, batch, paired = (destination/name for name in ('targets.tsv','batch.tsv','expected.json'))
    targets.write_text(''.join(f'{PACKAGE}\t{name}\n' for name in sorted(names)))
    call(benchplan, '--confirmation-plan', expected, targets, batch, paired)
    call(benchplan, '--verify-profile-batch', 'confirmation', paired, batch)
    rows = [line.split('\t') for line in batch.read_text().splitlines()]
    need(all(package == PACKAGE for package, _, _ in rows), 'original batch was not only API')
    return {profile: selector for _, profile, selector in rows}


def plan_route(tools, route):
    names = [item['name'] for item in route['confirmed']]
    entries = [{'Package': PACKAGE,'Name': name} for name in sorted(names)]
    expected = OUT/'expected.json'
    save(expected, {'Base': entries,'Head': entries})
    plan = OUT/'plan'
    plan.mkdir()
    by_group, by_profile = defaultdict(list), defaultdict(list)
    for item in route['confirmed']:
        group = item['projection']
        profile = CONFIG['projections'][group]['stock'][item['root']]
        by_group[group].append(item['name'])
        by_profile[profile].append(item['name'])
    stock = plan_subset(tools/'benchplan', expected, names, plan/'stock')
    need(set(stock) == set(by_profile), 'original stock family profile differs')
    for profile, selector in stock.items():
        selected = plan_subset(tools/'benchplan', expected, by_profile[profile], plan/('stock-'+profile))
        need(selected == {profile: selector}, 'original stock selector differs')
    projected = {}
    for group, group_names in by_group.items():
        selected = plan_subset(tools/'benchplan', expected, group_names, plan/group)
        need(len(selected) == 1, 'fixture mixes distinct stock roles')
        projected[group] = next(iter(selected.values()))
    return expected, stock, projected


def verify_metrics(name, sample):
    fields = sample.split()
    need(len(fields) % 2 == 0, 'incomplete benchmark metric')
    data = {unit: float(value) for value,unit in zip(fields[::2],fields[1::2])}
    needed = {b'ns/op',b'B/op',b'allocs/op'}
    need(needed <= data.keys() and len(data)*2==len(fields) and all(isfinite(v) and v>=0 for v in data.values()),
         'missing or invalid benchmark metrics')
    if name.startswith(b'BenchmarkWSAuthenticationExpiredParent/'):
        need(data.get(b'attempts/op') == 64 and data.get(b'writes/op') == 0,
             'expired parent benchmark ran a different number of attempted or accepted writes')


def inventory(raw, names, cpu, repeats=1, one_iteration=False):
    rows = BENCH_ROW.findall(raw)
    lines = raw.splitlines()
    need(len(rows)==sum(line.startswith(b'Benchmark') for line in lines) and
         Counter(name.decode() for name, _, _ in rows) == Counter({name: repeats for name in names}),
         'missing, repeated or invalid original benchmark leaves')
    need(all(int(iterations) == 1 if one_iteration else int(iterations) > 0 for _, iterations, _ in rows),
         'wrong use of 1x discovery or timed sampling')
    need(b'PASS' in lines and b'FAIL' not in lines, 'benchmark did not finish')
    need({line[5:].strip() for line in lines if line.startswith(b'cpu: ')} == {cpu.encode()}, 'sample CPU mismatch')
    for name, _, metrics in rows:
        verify_metrics(name,metrics)


def sample(binary, source, selector, time):
    return call(binary, '-test.run=^$', '-test.bench='+selector, '-test.benchmem', '-test.benchtime='+time,
                '-test.count=1', '-test.cpu=2', '-test.timeout=180s', cwd=source/PACKAGE)


def inspect_binary(go, binary, scope, pattern, work):
    data = {'fileSHA256': digest(binary)}
    commands = {'buildinfo':(go,'version','-m'), 'buildid':(go,'tool','buildid'),
                'sections':('readelf','-WlS'), 'hot':(go,'tool','objdump','-s',pattern)}
    for name, command in commands.items():
        (scope/(name+'.txt')).write_bytes(call(*command, binary))
    need((scope/'hot.txt').stat().st_size > 0, 'no hot instructions captured')
    symbols = call(go, 'tool','nm','-size',binary).decode().splitlines()
    (scope/'symbols.txt').write_text('\n'.join(row for row in symbols if re.search(pattern,row))+'\n')
    paths = [work/name for name in ('text','rodata')]
    copied = work/'discard.elf'
    call('objcopy', *(f'--dump-section=.{path.name}={path}' for path in paths), binary, copied)
    for path in paths:
        data[path.name] = digest(path)
        path.unlink()
    copied.unlink()
    save(scope/'elf.json', data)


def build_binary(go, tools, mode, role, key, source, work, evidence):
    wrapper = work/'wrapper/go'
    env = dict(os.environ, PATH=str(wrapper.parent)+os.pathsep+os.environ['PATH'], LUXO_WITNESS_REAL_GO=go,
               LUXO_WITNESS_PROFILE_LOG=str(evidence), GOWORK=str(source/'go.work'))
    built = work/(mode+'-'+role+'-'+key)
    built.mkdir()
    if mode == 'stock':
        command = 'compile-performance-baseline' if role == 'A' else 'compile-performance'
        call(tools/'benchrun', command, source, built, PACKAGE, key, env=env)
        suffix = '' if key == 'default' else '-'+key
        return built/('pkg-lux-api'+suffix+'.test')
    outcome = json.loads(call(tools/'project', source, MANIFEST, key, built/'overlay'))
    save(evidence/'projection.json', outcome)
    binary = built/'pkg-lux-api.test'
    call(wrapper, 'test','-trimpath','-c','-overlay='+outcome['Overlay'],'./'+PACKAGE,'-o',binary,cwd=source,env=env)
    return binary


def build_group(go, tools, work, mode, role, source, key, route, output):
    scope = output/(role+'-'+key)
    scope.mkdir()
    binary = build_binary(go, tools, mode, role, key, source, work, scope)
    projections = CONFIG['projections']
    if mode == 'stock':
        groups = sorted({item['projection'] for item in route['confirmed']
                         if projections[item['projection']]['stock'][item['root']] == key})
    else:
        groups = [key]
    inspect_binary(go, binary, scope, '|'.join(HOT[group] for group in groups), work)
    for group in groups:
        spec = projections[group]
        roots = [root for root in spec['roots'] if mode != 'stock' or spec['stock'][root] == key]
        names = [name for name in spec['names'] if name.split('/')[0].removesuffix('-2') in roots]
        raw = sample(binary,source,'^('+'|'.join(roots)+')$','1x')
        inventory(raw,names,route['cpu'],one_iteration=True)
        (scope/(group+'.inventory.raw')).write_bytes(raw)
    return binary


def schedule():
    for block, index in product((1,2),range(1,11)):
        order = (0,1) if (block+index)%2 == 0 else (1,0)
        for comparison in PAIRS:
            yield block,index,comparison,order


def measure(arms, binaries, selectors, output):
    measured = 0
    with (output/'order.tsv').open('x') as order:
        for block, index, comparison, matching in schedule():
            for side in matching:
                label, role = ('left','right')[side], comparison[side]
                with (output/f'{comparison}-{block}.{label}.raw').open('ab') as sink:
                    for key, selector in selectors.items():
                        measured += 1
                        order.write(f'{measured}\t{block}\t{index}\t{comparison}\t{label}\t{role}\t{key}\n')
                        sink.write(sample(binaries[role,key],arms[role],selector,'200ms'))
    need(measured==120*len(selectors),'incorrect native process count')
    return measured


def gate(tools, expected, head, route, output):
    names = [item['name'] for item in route['confirmed']]
    statuses = {}
    for comparison in PAIRS:
        files = []
        for block in (1,2):
            prefix = output/f'{comparison}-{block}'
            halves = [Path(str(prefix)+'.'+side+'.txt') for side in ('left','right')]
            for arm in halves:
                raw = arm.with_suffix('.raw')
                inventory(raw.read_bytes(), names, route['cpu'], repeats=10)
                call(tools/'benchplan','--normalize',raw,arm)
            call(tools/'benchplan','--verify',expected,*halves)
            for direction in (('forward','reverse') if comparison != 'AH' else ('forward',)):
                csv = Path(f'{prefix}.{direction}.csv')
                order = halves if direction=='forward' else halves[::-1]
                with csv.open('xb') as stats, csv.with_suffix('.warnings').open('xb') as warnings:
                    subprocess.run([tools/'benchstat','-alpha','0.01','-filter','.unit:(sec/op OR B/op OR allocs/op)',
                                    '-format','csv','base='+str(order[0]),'head='+str(order[1])],
                                   stdout=stats,stderr=warnings,check=True)
                files.append(csv)
        groups = [files] if comparison=='AH' else [[part] for part in files]
        flags = ['--approved-costs',head/'scripts/benchgate/approved-costs.json','--base-sha',CONFIG['base']] if comparison=='AH' else []
        for parts in groups:
            with parts[0].with_suffix('.gate.txt').open('xb') as raw:
                result = subprocess.run([tools/'benchgate',*flags,*parts],stdout=raw,stderr=subprocess.STDOUT)
            statuses[parts[0].stem] = result.returncode
    return statuses


def compare(mode, go, tools, arms, route, selectors, expected, work):
    output = OUT/mode
    output.mkdir()
    binaries = {(role,key): build_group(go,tools,work,mode,role,arms[role],key,route,output)
                for role,key in product(ROLES,selectors)}
    if mode=='projected':
        for key in selectors:
            sources = [json.loads((output/(role+'-'+key)/'projection.json').read_bytes())['Fixtures'] for role in ROLES]
            need(sources[0]==sources[1],'different retained fixture bytes between arms')
    return {'processes': measure(arms,binaries,selectors,output), 'binaries': len(selectors),
            'gates': gate(tools,expected,arms['H'],route,output)}


def summarize(folder):
    status_paths = list(folder.glob('*/status.json'))
    need(len(status_paths)==6,'missing slot receipt')
    observed, failed = set(), []
    requested = requested_shards(os.environ.get('GITHUB_REF', REF))
    for path in sorted(status_paths):
        result = json.loads(path.read_bytes())
        route = select_route(result['cpu'], requested)
        if not result['matched']:
            need(route is None and result['processes']==0,'misrouted benchmark receipt')
            continue
        need(route and route['shard']==result['shard'], 'wrong CPU for original shard')
        observed.add(route['shard'])
        needed = {'stock', 'projected'}
        available = set(result['modes'])
        if available != needed:
            failed.append((route['shard'],'missing comparison'))
        for mode, comparison in result['modes'].items():
            count = len({(item['projection'] if mode=='projected' else
                         CONFIG['projections'][item['projection']]['stock'][item['root']]) for item in route['confirmed']})
            need(comparison['binaries']==count and comparison['processes']==120*count
                 and set(comparison['gates']) == CONTROLS|{'AH-1.forward'}, 'invalid slot schedule or gate receipt')
            if any(comparison['gates'].values()):
                failed.append((route['shard'],mode))
    missing = sorted(requested-observed)
    print(json.dumps({'matchedShards':sorted(observed),'missingShards':missing,'strictFailures':failed},sort_keys=True))
    need(not missing and not failed,'at least one original model remains unqualified')


def main():
    args = sys.argv[1:]
    if len(args)==2 and args[0]=='--summarize':
        summarize(Path(args[1]))
        return
    route = check_runner()
    if args==['--probe']:
        if route:
            with Path(os.environ['GITHUB_OUTPUT']).open('a') as output:
                output.write(f"shard={route['shard']}\n")
        else:
            recorded = json.loads((OUT/'route.json').read_bytes())
            save(OUT/'status.json', {'matched':False,'cpu':recorded['cpu'],'processes':0})
        return
    need(route and args==[route['shard']],'requested shard differs from this native model')
    go = shutil.which('go')
    need(go,'Go compiler not found')
    arms = bind_sources(go)
    with TemporaryDirectory(prefix='pr84-native.', dir=OUT.parent) as parent:
        work = Path(parent)
        tools = build_tools(go,arms['H'],work)
        expected, stock, projected = plan_route(tools,route)
        modes = {'stock': compare('stock',go,tools,arms,route,stock,expected,work)}
        if all(modes['stock']['gates'][name]==0 for name in CONTROLS):
            modes['projected'] = compare('projected',go,tools,arms,route,projected,expected,work)
    save(OUT/'status.json', {'matched':True,'cpu':route['cpu'],'shard':route['shard'],'modes':modes})
    sys.exit(int(any(value for mode in modes.values() for value in mode['gates'].values())))


if __name__ == '__main__':
    main()
