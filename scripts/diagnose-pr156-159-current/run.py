#!/usr/bin/env python3
"""Measure the signed compiler leaves on their original EPYC models."""

from collections import Counter
from hashlib import sha256
from itertools import product
import json
import os
from pathlib import Path
import re
from shutil import which
import subprocess
import sys
from tempfile import TemporaryDirectory

INPUT = Path(__file__).resolve().parent
ROOT = INPUT.parents[1]
OUT = ROOT / '.tmp/pr156-159-current'
CONFIG = json.loads((INPUT / 'manifest.json').read_text())
PARENT = '3dc591f2bd0192f4613a641d18a41c8a1058916f'
ROLE_PAIRS = ('AA', 'HH', 'AH')
PROBE_ROWS = re.compile(r'^Benchmark\S+-2\s+1\s+\S+\s+ns/op\s+\S+\s+B/op\s+\S+\s+allocs/op$', re.M)
HOT = {
    'pkg/codegen': 'BenchmarkRelationLoaderGeneration|collectDataLoaderInputs|analyzeRelations|'
                   'BenchmarkFindRead|compileCall|BenchmarkNumericRangeValidation|generateNumericValidation',
    'pkg/semantic': 'BenchmarkCollectEventEmits|collectReachableEmits|'
                    'BenchmarkEventSnapshotContract|validateEventSnapshots',
}
FAMILIES = {
    'BenchmarkRelationLoaderGeneration': [
        f'BenchmarkRelationLoaderGeneration/{n}/{kind}-2' for n in (1,128) for kind in ('metadata','source')
    ],
    'BenchmarkCollectEventEmits': [
        f'BenchmarkCollectEventEmits/direct_{n}/recursive={recursive}-2'
        for n in (0,1,128) for recursive in ('true','false')
    ],
    'BenchmarkNumericRangeValidation': [
        f'BenchmarkNumericRangeValidation/{name}-2' for name in ('none','positive','signed_int','signed_float')
    ],
    'BenchmarkEventSnapshotContract': [
        f'BenchmarkEventSnapshotContract/nested_types_{n}-2' for n in (0,1,16,128)
    ],
    'BenchmarkFindRead': ['BenchmarkFindRead-2'],
}
PROJECT = ROOT / 'scripts/diagnose-relation-loader/project.go'


def require(ok, detail):
    if not ok:
        raise RuntimeError(detail)


def call(*args, cwd=ROOT, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env)


def git(root, *args, env=None):
    return call('git', '--no-replace-objects', '-C', root, *args, env=env)


def digest(path):
    return sha256(path.read_bytes()).hexdigest()


def dump(file, payload):
    file.write_text(json.dumps(payload, indent=2, sort_keys=True) + '\n')


def route_for(cpu):
    return next((kind for kind, route in CONFIG['routes'].items() if route['model'] == cpu), None)


def probe():
    OUT.mkdir(parents=True, exist_ok=True)
    models = {line.split(':', 1)[1].strip() for line in Path('/proc/cpuinfo').read_text().splitlines()
              if line.startswith('model name')}
    require(len(models) == 1, 'CPU description missing or heterogeneous')
    cpu = models.pop()
    route = route_for(cpu)
    dump(OUT / 'route.json', {'cpu': cpu, 'matched': route is not None, 'route': route})
    if route is None:
        dump(OUT / 'status.json', {'matched': False, 'timedProcesses': 0})
    else:
        with Path(os.environ['GITHUB_OUTPUT']).open('a') as output:
            output.write(f'cpu={route}\n')


def verify_run(route):
    bounds = json.loads((OUT / 'route.json').read_text())
    require(bounds == {'cpu': CONFIG['routes'][route]['model'], 'matched': True, 'route': route},
            'a different CPU was routed')
    require(os.environ['RUNNER_KIND'] == 'github-hosted' and os.cpu_count() == 4
            and 'VERSION_ID="24.04"' in Path('/etc/os-release').read_text().splitlines(), 'unexpected host')
    require((os.environ['GITHUB_REPOSITORY'], os.environ['GITHUB_REF'], os.environ['GITHUB_EVENT_NAME'])
            == ('light-speak/luxo', 'refs/heads/diagnostics/pr156-159-current-native', 'push'), 'unexpected route identity')
    require(git(ROOT, 'rev-parse', 'HEAD').decode().strip() == os.environ['GITHUB_SHA'], 'wrong workflow SHA')
    require(git(ROOT, 'show', '-s', '--format=%P', 'HEAD').decode().strip() == PARENT, 'wrong stage parent')
    require(git(ROOT, 'diff', '--name-status', PARENT, 'HEAD').decode().splitlines() == [
        'A\t.github/workflows/diagnose-pr156-159-current.yml',
        'A\tscripts/diagnose-pr156-159-current/manifest.json',
        'A\tscripts/diagnose-pr156-159-current/run.py',
    ], 'unexpected diagnostic paths')
    require(digest(PROJECT) == 'be83904923c90961a15ac69e374702a47a9347ace53fc88cd31a5f1b7312649f',
            'existing tested projector changed')
    fields = 'GOVERSION GOHOSTOS GOHOSTARCH GOOS GOARCH GOAMD64 CGO_ENABLED GOFLAGS GOTOOLCHAIN'.split()
    go_env = call('go', 'env', *fields).decode().splitlines()
    require(go_env == ['go1.27.1', 'linux', 'amd64', 'linux', 'amd64', 'v1', '1', '', 'local'],
            'Go environment differs from the original qualification')
    dump(OUT / 'binding.json', {'config': CONFIG, 'route': bounds, 'goEnvironment': go_env,
                                'diagnosticCommit': os.environ['GITHUB_SHA'], 'manifestSHA256': digest(INPUT/'manifest.json')})
    (OUT / 'environment.txt').write_bytes(call('lscpu'))


def exact_tree(repo, rev, tree):
    require(git(repo, 'rev-parse', rev + '^{tree}').decode().strip() == tree, f'unexpected tree for {rev}')


def materialize(work, anchor, old, role):
    head = CONFIG[role + 'Head']
    exact_tree(old, head, CONFIG[role+'Tree'])
    patch = git(old, 'diff', '--no-renames', '--binary', '--full-index', CONFIG['oldBaseline'], head)
    (OUT / (role+'.patch.sha256')).write_text(sha256(patch).hexdigest() + '\n')
    objects = work / (role+'-objects')
    objects.mkdir()
    git_dir = Path(git(anchor, 'rev-parse', '--absolute-git-dir').decode().strip())
    environment = dict(os.environ, GIT_INDEX_FILE=str(work/(role+'.index')), GIT_OBJECT_DIRECTORY=str(objects),
                       GIT_ALTERNATE_OBJECT_DIRECTORIES=str(git_dir/'objects'), GIT_NO_LAZY_FETCH='1')
    git(anchor, 'read-tree', CONFIG['baseline'], env=environment)
    subprocess.run(['git','--no-replace-objects','-C',anchor,'apply','--cached','--binary','-'],
                   env=environment, input=patch, check=True)
    tree = git(anchor, 'write-tree', env=environment).decode().strip()
    require(tree == CONFIG[role+'Projection'], f'{role} did not reproduce projected tree')
    source = work / role
    source.mkdir()
    with subprocess.Popen(['git','--no-replace-objects','-C',anchor,'archive',tree], env=environment,
                          stdout=subprocess.PIPE) as archive:
        subprocess.run(['tar','-xf','-','-C',source], stdin=archive.stdout, check=True)
        require(archive.wait() == 0, f'{role} git archive failed')
    return source


def build_tools(anchor, work):
    tools = work / 'bin'
    tools.mkdir()
    env = dict(os.environ, GOWORK='off')
    for name in ('benchrun','benchplan','benchgate'):
        call('go','build','-p','1','-o',tools/name,'./scripts/'+name,cwd=anchor,env=env)
    call('go','build','-p','1','-o',tools/'project',PROJECT,cwd=anchor,env=env)
    call('go','install','golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70',cwd=anchor,
         env=dict(env,GOBIN=str(tools)))
    profile = OUT / 'compile-profile.txt'
    profile.write_bytes(call(tools/'benchrun','performance-profile'))
    require(digest(profile) == CONFIG['compileProfileSHA256'], 'compile profile differs from actual A')
    return tools


def plan(tools, directory, cases):
    directory.mkdir()
    rows = [{'Package': package, 'Name': name} for package,name in cases]
    dump(directory/'signed-primary.json', {'Base': rows, 'Head': rows})
    (directory/'targets.tsv').write_text(''.join(f'{package}\t{name}\n' for package,name in cases))
    expected, batch = directory/'expected.json', directory/'batch.tsv'
    call(tools/'benchplan','--confirmation-plan',directory/'signed-primary.json',directory/'targets.tsv',batch,expected)
    call(tools/'benchplan','--verify-profile-batch','confirmation',expected,batch)
    batches = [tuple(line.split('\t')) for line in batch.read_text().splitlines()]
    require(batches and all(profile == 'default' and pkg in HOT for pkg,profile,_ in batches),
            'selector routed outside the original compiler profiles')
    return expected, batches


def check_inventory(data, expected):
    text = data.decode()
    all_rows = [line for line in text.splitlines() if line.startswith('Benchmark')]
    require(text.rstrip().endswith('\nPASS') and len(all_rows)==len(PROBE_ROWS.findall(text))
            and Counter(row.split()[0] for row in all_rows) == Counter(expected), 'incomplete or invalid 1x inventory')


def elf(binary, destination, pkg, work):
    stats = {'fileSHA256': digest(binary), 'buildID': call('go','tool','buildid',binary).decode().strip()}
    for section in ('.text','.rodata'):
        materialized = work / (binary.parent.name + binary.name + section)
        copy = materialized.with_suffix('.elf-copy')
        call('objcopy','--dump-section',f'{section}={materialized}',binary,copy)
        stats[section+'SHA256'] = digest(materialized)
        materialized.unlink()
        copy.unlink()
    dump(destination/'elf.json',stats)
    for name, args in [('buildinfo',('go','version','-m')),('sections',('readelf','-WlS')),
                       ('hot',('go','tool','objdump','-s',HOT[pkg]))]:
        (destination/(name+'.txt')).write_bytes(call(*args,binary))
    require('TEXT ' in (destination/'hot.txt').read_text(), 'hot symbol capture empty')


def sample(binary, root, pkg, selector, budget):
    return call(binary,'-test.run=^$','-test.bench='+selector,'-test.benchmem',
                '-test.benchtime='+budget,'-test.count=1','-test.cpu=2',cwd=root/pkg)


def compile_bins(tools, scope, roots, batches, cases, work, projected):
    built = {}
    for role, source in roots.items():
        binaries = work/(scope.parent.name+'-'+scope.name+'-'+role)
        binaries.mkdir()
        for pkg,profile,_ in batches:
            family = pkg.rsplit('/',1)[-1]
            destination = scope/(role+'-'+family)
            destination.mkdir()
            output = binaries/(pkg.replace('/','-')+'.test')
            if projected:
                overlay = binaries/(family+'-overlay')
                (destination/'projection.json').write_bytes(call(tools/'project',source,overlay,family))
                call('go','test','-trimpath','-c','-overlay='+str(overlay/'overlay.json'),'./'+pkg,'-o',output,cwd=source,
                     env=dict(os.environ,GOWORK=str(source/'go.work')))
            else:
                command = 'compile-performance-baseline' if role == 'A' else 'compile-performance'
                call(tools/'benchrun',command,source,binaries,pkg,profile)
            elf(output,destination,pkg,work)
            family_roots = sorted({name.split('/')[0].removesuffix('-2') for package,name in cases if package==pkg})
            for root in family_roots:
                data = sample(output,source,pkg,'^'+root+'$','1x')
                check_inventory(data,FAMILIES[root])
                (destination/(root+'.raw')).write_bytes(data)
            built[role,pkg] = output
    return built


def schedule(batches):
    for block, pair, arms in product((1,2),range(1,11),ROLE_PAIRS):
        indexes = (0,1) if (block+pair)%2==0 else (1,0)
        for side in indexes:
            for package, _, selector in batches:
                yield block,pair,arms,('left','right')[side],arms[side],package,selector


def measure(scope, roots, bins, batches):
    process = 0
    with (scope/'order.tsv').open('x') as order:
        for block,pair,arms,side,role,pkg,selector in schedule(batches):
            process += 1
            order.write(f'{process}\t{block}\t{pair}\t{arms}\t{side}\t{role}\t{pkg}\n')
            with (scope/f'{arms}-{block}.{side}.raw').open('ab') as stream:
                stream.write(sample(bins[role,pkg],roots[role],pkg,selector,'200ms'))
    require(process==120*len(batches),'wrong number of timed processes')
    return process


def gate(tools, scope, expected, approvals):
    results = {}
    for block,arms in product((1,2),ROLE_PAIRS):
        name = f'{arms}-{block}'
        files = [scope/f'{name}.{part}.txt' for part in ('left','right')]
        for item in files:
            call(tools/'benchplan','--normalize',item.with_suffix('.raw'),item)
        call(tools/'benchplan','--verify',expected,*files)
        for reverse in ((False,True) if arms[0]==arms[1] else (False,)):
            pair = files[::-1] if reverse else files
            stem = name + ('.reverse' if reverse else '')
            with (scope/(stem+'.csv')).open('wb') as csv, (scope/(stem+'.warnings')).open('wb') as warnings:
                subprocess.run([tools/'benchstat','-alpha','0.01','-filter','.unit:(sec/op OR B/op OR allocs/op)',
                                '-format','csv','base='+str(pair[0]),'head='+str(pair[1])],
                               stdout=csv,stderr=warnings,check=True)
            if arms[0]==arms[1]:
                results[stem] = enforce(tools,scope,stem,[],scope/(stem+'.csv'))
    options = ['--approved-costs',approvals,'--base-sha',CONFIG['baseline']]
    results['AH'] = enforce(tools,scope,'AH',options,scope/'AH-1.csv',scope/'AH-2.csv')
    return results


def enforce(tools, scope, stem, options, *csv):
    with (scope/(stem+'.gate.txt')).open('wb') as evidence:
        return subprocess.run([tools/'benchgate',*options,*csv],stdout=evidence,stderr=subprocess.STDOUT).returncode


def work_pair(tools, folder, anchor, tested, cases, work, projected=False):
    expected, batches = plan(tools,folder,cases)
    roots = {'A':anchor,'H':tested}
    bins = compile_bins(tools,folder,roots,batches,cases,work,projected)
    n = measure(folder,roots,bins,batches)
    outcome = gate(tools,folder,expected,anchor/'scripts/benchgate/approved-costs.json')
    report = {'timedProcesses':n,'gates':outcome}
    dump(folder/'status.json',report)
    return report


def main(route):
    os.environ['GOTOOLCHAIN'] = 'local'
    verify_run(route)
    anchor = ROOT/'.tmp/current-a'
    require(git(anchor,'rev-parse','HEAD').decode().strip()==CONFIG['baseline'],'wrong baseline checkout')
    exact_tree(anchor,'HEAD',CONFIG['baselineTree'])
    results = {}
    with TemporaryDirectory(prefix='pr156-159-witness.',dir=OUT.parent) as folder:
        work = Path(folder)
        (work/'temp').mkdir()
        os.environ.update(TMPDIR=str(work/'temp'),GOTMPDIR=str(work/'temp'))
        tools = build_tools(anchor,work)
        call('sh',anchor/'scripts/setup-modules.sh',anchor,cwd=anchor)
        for role, cases in CONFIG['routes'][route].items():
            if role == 'model':
                continue
            old = ROOT/'.tmp/await-old' if role=='await' else ROOT
            tested = materialize(work,anchor,old,role)
            call('sh',anchor/'scripts/setup-modules.sh',tested,cwd=anchor)
            require((anchor/'go.work').read_bytes()==(tested/'go.work').read_bytes(),'workspace inputs differ')
            target = OUT/role
            target.mkdir()
            stock = work_pair(tools,target/'stock',anchor,tested,cases,work)
            results[role] = {'stock':stock}
            controls = {name:code for name,code in stock['gates'].items() if name!='AH'}
            # Only the two existing AST projections have a qualified diagnostic fixture.
            if stock['gates']['AH'] and not any(controls.values()) and (role=='await' or route=='9v74'):
                results[role]['projected'] = work_pair(tools,target/'projected',anchor,tested,cases,work,True)
    dump(OUT/'status.json',{'matched':True,'route':route,'comparisons':results})
    # Stock failures remain visible even when the projected experiment is green.
    return int(any(code for row in results.values() for mode in row.values() for code in mode['gates'].values()))


def summarize(directory):
    statuses = sorted(directory.rglob('status.json'))
    statuses = [path for path in statuses if (path.parent/'route.json').is_file()]
    require(len(statuses)==4, 'expected receipts from all four independent runner slots')
    seen, failures = set(), []
    for path in statuses:
        status = json.loads(path.read_text())
        if not status['matched']:
            require(status['timedProcesses']==0, 'unmatched CPU produced samples')
            continue
        route = status['route']
        binding = json.loads((path.parent/'route.json').read_text())
        require(route_for(binding['cpu'])==route, 'receipt came from an unexpected CPU')
        roles = {'await','sealed'} if route=='7763' else {'sealed'}
        require(set(status['comparisons'])==roles, 'incomplete CPU receipt')
        seen.add(route)
        for role, modes in status['comparisons'].items():
            stock = modes['stock']
            require(stock['timedProcesses']==(240 if route=='7763' else 120)
                    and len(stock['gates'])==9 and 'AH' in stock['gates'], 'incomplete stock results')
            for mode, tested in modes.items():
                if any(tested['gates'].values()):
                    failures.append((route,role,mode))
    print(json.dumps({'measuredRoutes':sorted(seen),'failedComparisons':failures},sort_keys=True))
    require(seen==set(CONFIG['routes']), 'no valid evidence for one or both original CPUs')
    require(not failures, 'a matched runner reported a real gate failure')


if __name__ == '__main__':
    if sys.argv[1:] == ['--probe']:
        probe()
    elif len(sys.argv) == 3 and sys.argv[1] == '--summarize':
        summarize(Path(sys.argv[2]))
    elif len(sys.argv) == 2 and sys.argv[1] in CONFIG['routes']:
        sys.exit(main(sys.argv[1]))
    else:
        sys.exit('expected --probe or the verified runner route')
