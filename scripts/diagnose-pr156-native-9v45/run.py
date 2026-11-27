#!/usr/bin/env python3
"""Compare the signed PR 156 compiler leaves on AMD EPYC 9V45."""

from collections import Counter
from hashlib import sha256
from itertools import product
import json
from math import isfinite
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from tempfile import TemporaryDirectory


INPUT = Path(__file__).resolve().parent
ROOT = INPUT.parents[1]
OUT = ROOT/'.tmp/pr156-native-9v45-evidence'
CHECKOUT = ROOT/'.tmp/pr156-native-9v45-inputs'
MANIFEST = INPUT/'manifest.json'
MANIFEST_SHA = '1b0d885dafc62ac8245781bbc8911c7f3bb90476003a7d63782fcfe601a96fe1'
PARENT = 'f8924ac56afa77c460d7fc62f81df10e2673c202'
REF = 'refs/heads/diagnostics/pr156-native-9v45'
CONFIG = json.loads(MANIFEST.read_bytes())
ROLES = ('A','H')
PAIRS = ('AA','HH','AH')
CONTROLS = {f'{pair}-{block}.{direction}' for pair, block, direction in
            product(('AA','HH'),(1,2),('forward','reverse'))}
BENCH_ROW = re.compile(rb'^(Benchmark\S+-2)[ \t]+(\d+)[ \t]+([^\r\n]+)$',re.M)


def need(ok, problem):
    if not ok:
        raise RuntimeError(problem)


def digest(path):
    h=sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024*1024),b''):
            h.update(chunk)
    return h.hexdigest()


def call(*args, cwd=ROOT, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env)


def git(directory, *args):
    return call('git', '--no-replace-objects', '-C', directory, *args).decode().strip()


def save(path, value):
    with path.open('x') as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write('\n')


def runner():
    OUT.mkdir(parents=True, exist_ok=True)
    need(digest(MANIFEST)==MANIFEST_SHA,'signed evidence manifest changed')
    active = git(ROOT,'rev-parse','HEAD')
    need(os.environ.get('RUNNER_KIND') == 'github-hosted' and
         os.environ.get('GITHUB_REPOSITORY') == 'light-speak/luxo' and
         os.environ.get('GITHUB_EVENT_NAME') == 'push' and os.environ.get('GITHUB_REF') == REF and
         os.environ.get('GITHUB_SHA') == active, 'unexpected diagnostic event')
    need(git(ROOT,'show','-s','--format=%P',active)==PARENT and
         git(ROOT,'diff','--name-status',PARENT,active).splitlines()==[
             'A\t.github/workflows/diagnose-pr156-native-9v45.yml',
             'A\tscripts/diagnose-pr156-native-9v45/go-capture.py',
             'A\tscripts/diagnose-pr156-native-9v45/manifest.json',
             'A\tscripts/diagnose-pr156-native-9v45/project.go',
             'A\tscripts/diagnose-pr156-native-9v45/run.py',
         ], 'unexpected diagnostic inputs')
    models={line.split(':',1)[1].strip(' \t') for line in Path('/proc/cpuinfo').read_text().splitlines()
            if line.startswith('model name')}
    need(len(models)==1,'unknown or mixed CPU model')
    cpu=models.pop()
    matched=cpu==CONFIG['cpu']
    if matched:
        need(call('uname','-sm').decode().strip()=='Linux x86_64' and
             'VERSION_ID="24.04"' in Path('/etc/os-release').read_text().splitlines() and
             call('getconf','_NPROCESSORS_ONLN').strip()==b'4', 'unexpected hosted environment')
    value={'matched':matched,'cpu':cpu,'diagnostic':active}
    route=OUT/'route.json'
    if not route.is_file():
        save(route,value)
    else:
        need(json.loads(route.read_bytes())==value,'CPU changed after routing')
    return value


def arm_sources(go):
    arms={role: CHECKOUT/role for role in ROLES}
    for role,path in arms.items():
        values=CONFIG[role]
        need(git(path,'rev-parse','HEAD')==values['commit'] and git(path,'rev-parse','HEAD^{tree}')==values['tree'] and
             git(path,'rev-parse','--show-toplevel')==str(path) and not git(path,'status','--porcelain'),
             f'unexpected {role} source')
    a,h=arms['A'],arms['H']
    need(git(h,'show','-s','--format=%P','HEAD').split()==CONFIG['H']['parents'],
         'unexpected merge provenance')
    need(sha256(call('git','--no-replace-objects','-C',h,'diff','--name-status',
                     CONFIG['A']['commit'],'HEAD')).hexdigest()==CONFIG['productPathSHA256'],
         'unexpected original product delta')
    fields=('GOVERSION','GOHOSTOS','GOHOSTARCH','GOOS','GOARCH','GOAMD64','CGO_ENABLED','GOFLAGS','GOTOOLCHAIN')
    env=call(go,'env',*fields)
    need(env==b'go1.27.1\nlinux\namd64\nlinux\namd64\nv1\n1\n\nlocal\n','wrong Go toolchain or flags')
    for path in arms.values():
        (path/'.tmp').mkdir()
        call('sh',h/'scripts/setup-modules.sh',path,cwd=h)
    need((a/'go.work').read_bytes()==(h/'go.work').read_bytes(),'different Go workspace sources')
    save(OUT/'binding.json',{'inputs':CONFIG,'goEnvironment':dict(zip(fields,env.decode().splitlines())),
                             'goSHA256':digest(Path(go)),'host':call('uname','-a').decode().strip(),
                             'bootID':Path('/proc/sys/kernel/random/boot_id').read_text().strip()})
    (OUT/'lscpu.json').write_bytes(call('lscpu','--json'))
    return arms


def build_tools(go, head, work):
    tools,wrapper,temp=work/'bin',work/'wrapper',work/'go-tmp'
    for path in (tools,wrapper,temp):
        path.mkdir()
    env=dict(os.environ,GOWORK='off',TMPDIR=str(temp),GOTMPDIR=str(temp))
    os.environ.update(TMPDIR=str(temp),GOTMPDIR=str(temp))
    for name in ('benchplan','benchrun','benchgate'):
        call(go,'build','-p','1','-o',tools/name,'./scripts/'+name,cwd=head,env=env)
    call(go,'build','-p','1','-o',tools/'project',INPUT/'project.go',cwd=head,env=env)
    call(go,'install','golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70',
         cwd=head,env=dict(env,GOBIN=str(tools)))
    profile=call(tools/'benchrun','performance-profile')
    need(sha256(profile).hexdigest()==CONFIG['compileProfileSHA256'],'original compiler profile changed')
    (OUT/'compile-profile.txt').write_bytes(profile)
    save(OUT/'tools.json',{tool.name:digest(tool) for tool in sorted(tools.iterdir())})
    shutil.copyfile(INPUT/'go-capture.py',wrapper/'go')
    (wrapper/'go').chmod(0o700)
    return tools


def full_names(config, key):
    return ["/".join(filter(None,(config["root"],item)))+"-2" for item in config[key]]


def plan_route(tools, directory, package, config):
    names=full_names(config,'timed')
    leaves=[{'Package':package,'Name':name} for name in names]
    expected=directory/'expected.json'
    save(expected,{'Base':leaves,'Head':leaves})
    targets,batch,paired=(directory/name for name in ('targets.tsv','batch.tsv','confirmation.json'))
    targets.write_text(''.join(f'{package}\t{name}\n' for name in names))
    call(tools/'benchplan','--confirmation-plan',expected,targets,batch,paired)
    call(tools/'benchplan','--verify-profile-batch','confirmation',paired,batch)
    wanted=(package,'default','|'.join('/'.join(f'^{part}$' for part in name[:-2].split('/'))
                                      for name in sorted(names)))
    need(batch.read_text().splitlines()==['\t'.join(wanted)],'original compile path or selector changed')
    return expected,wanted[2],names



def verify_metrics(sample):
    fields=sample.split()
    need(len(fields)%2==0,'incomplete benchmark metric')
    data={unit:float(value) for value,unit in zip(fields[::2],fields[1::2])}
    need({b'ns/op',b'B/op',b'allocs/op'} <= data.keys() and len(data)*2==len(fields)
         and all(isfinite(v) and v>=0 for v in data.values()),'missing or invalid benchmark metrics')


def inventory(raw, names, cpu, package, platform=(b'linux', b'amd64'), repeats=1, one_iteration=False):
    rows = BENCH_ROW.findall(raw)
    lines = raw.splitlines()
    need(len(rows)==sum(line.startswith(b'Benchmark') for line in lines) and
         Counter(name.decode() for name, _, _ in rows) == Counter({name: repeats for name in names}),
         'missing, repeated or invalid original benchmark leaves')
    need(all(int(iterations) == 1 if one_iteration else int(iterations) > 0 for _, iterations, _ in rows),
         'wrong use of 1x discovery or timed sampling')
    need(lines.count(b'PASS')==repeats and b'FAIL' not in lines,'benchmark did not finish')
    headers={b'goos: ':platform[0],b'goarch: ':platform[1],
             b'pkg: ':('github.com/light-speak/luxo/'+package).encode(),b'cpu: ':cpu.encode()}
    for prefix, value in headers.items():
        actual=[line[len(prefix):] for line in lines if line.startswith(prefix)]
        if prefix==b'cpu: ':
            actual=[row.rstrip(b' \t') for row in actual]
        need(actual==[value]*repeats,'unexpected benchmark header '+prefix.decode())
    for name, _, metrics in rows:
        verify_metrics(metrics)


def sample(binary, source, package, selector, time):
    return call(binary, '-test.run=^$', '-test.bench='+selector, '-test.benchmem', '-test.benchtime='+time,
                '-test.count=1', '-test.cpu=2', '-test.timeout=180s', cwd=source/package)


def inspect_binary(go, binary, scope, pattern, work):
    data = {'fileSHA256': digest(binary)}
    commands = {'buildinfo':(go,'version','-m'), 'buildid':(go,'tool','buildid'),
                'sections':('readelf','-WlS'), 'hot':(go,'tool','objdump','-s',pattern)}
    for name, command in commands.items():
        (scope/(name+'.txt')).write_bytes(call(*command, binary))
    need((scope/'hot.txt').stat().st_size > 0, 'no hot instructions captured')
    symbols = call(go, 'tool','nm','-size',binary).decode().splitlines()
    selected=[row for row in symbols if re.search(pattern,row)]
    need(selected,'no selected linked symbols')
    (scope/'symbols.txt').write_text('\n'.join(selected)+'\n')
    paths = [work/name for name in ('text','rodata')]
    copied = work/'discard.elf'
    call('objcopy', *(f'--dump-section=.{path.name}={path}' for path in paths), binary, copied)
    for path in paths:
        data[path.name] = digest(path)
        path.unlink()
    copied.unlink()
    save(scope/'elf.json', data)


def build_binary(go, tools, mode, role, source, package, config, work, evidence):
    evidence.mkdir()
    built=work/(package.split('/')[-1]+'-'+mode+'-'+role)
    built.mkdir()
    name=package.replace('/','-')
    binary=built/(name+'.test')
    wrapper=work/'wrapper/go'
    env=dict(os.environ,PATH=str(wrapper.parent)+os.pathsep+os.environ['PATH'],LUXO_WITNESS_REAL_GO=go,
             LUXO_WITNESS_PROFILE_LOG=str(evidence),GOWORK=str(source/'go.work'))
    if mode=='stock':
        command='compile-performance-baseline' if role=='A' else 'compile-performance'
        call(tools/'benchrun',command,source,built,package,'default',env=env)
        assert_overlay(evidence,name,config['stockOverlay'])
    else:
        overlay_dir=built/'projection'
        role_name={'A':'base','H':'head'}[role]
        call(tools/'project',MANIFEST,source,package,role_name,overlay_dir)
        overlay=overlay_dir/'overlay.json'
        result=json.loads(overlay.read_bytes())['Replace']
        retained={Path(old).name:digest(Path(new)) for old,new in result.items() if new}
        need(retained=={name:spec['projectionSHA256'] for name,spec in config['files'].items()},
             'reviewed fixture projection changed')
        call(wrapper,'test','-trimpath','-c','-overlay='+str(overlay),'./'+package,'-o',binary,cwd=source,env=env)
        roots=call(binary,'-test.list=.*',cwd=source/package).decode().splitlines()
        need(roots==[config['root']],'projected binary acquired another benchmark or test')
        save(evidence/'projection.json',{'retained':retained,'overlayFileCount':len(result)})
        actual=json.loads((evidence/(name+'.overlay.json')).read_bytes())
        need(len(actual)==len(result),'wrong projection overlay size')
        for relative, digest_value in actual.items():
            need(relative.startswith(package+'/'),'projection includes a foreign package')
            current=retained.get(Path(relative).name)
            need(digest_value==(current or ''),'projection changed an unrecognized fixture')
    inspect_binary(go,binary,evidence,config['hot'],work)
    raw=sample(binary,source,package,'^'+config['root']+'$','1x')
    (evidence/'full-inventory.raw').write_bytes(raw)
    inventory(raw,full_names(config,'leaves'),CONFIG['cpu'],package,one_iteration=True)
    return binary


def assert_overlay(evidence, name, expected):
    need(json.loads((evidence/(name+'.overlay.json')).read_bytes())==expected,
         'original stock compiler overlay changed')


def schedule():
    for block, index in product((1,2),range(1,11)):
        order=(0,1) if (block+index)%2==0 else (1,0)
        for comparison in PAIRS:
            yield block,index,comparison,order


def measure(arms, binaries, package, selector, names, output):
    measured=0
    with (output/'order.tsv').open('x',buffering=1) as order:
        order.write('process\tblock\tpair\tcomparison\tside\tarm\n')
        for block, index, comparison, matching in schedule():
            for side in matching:
                label,role=('left','right')[side],comparison[side]
                order.write(f'{measured+1}\t{block}\t{index}\t{comparison}\t{label}\t{role}\n')
                with (output/f'{comparison}-{block}.{label}.raw').open('ab') as sink:
                    raw=sample(binaries[role],arms[role],package,selector,'200ms')
                    sink.write(raw)
                    inventory(raw,names,CONFIG['cpu'],package)
                measured+=1
    need(measured==120,'incorrect native process count')
    return measured



def rejected_gate(report, comparison):
    lines=report.splitlines()
    if comparison=='AH':
        return (any(line.startswith('confirmed performance regression: ') for line in lines)
                and bool(lines) and re.fullmatch(r'performance gate rejected [1-9][0-9]* regression\(s\)',lines[-1]))
    return (bool(lines) and re.fullmatch(
        r'performance gate requires confirmation for [1-9][0-9]* regression candidate\(s\)',lines[-1]))


def gate(tools, expected, head, output, package, names):
    statuses = {}
    for comparison in PAIRS:
        files = []
        for block in (1,2):
            prefix = output/f'{comparison}-{block}'
            halves = [Path(str(prefix)+'.'+side+'.txt') for side in ('left','right')]
            for arm in halves:
                raw = arm.with_suffix('.raw')
                inventory(raw.read_bytes(), names, CONFIG['cpu'], package, repeats=10)
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
        flags = ['--approved-costs',head/'scripts/benchgate/approved-costs.json','--base-sha',CONFIG['A']['commit']] if comparison=='AH' else []
        for parts in groups:
            transcript=parts[0].with_suffix('.gate.txt')
            with transcript.open('xb') as raw:
                result = subprocess.run([tools/'benchgate',*flags,*parts],stdout=raw,stderr=subprocess.STDOUT)
            report=transcript.read_text()
            need(result.returncode==0 and 'performance gate passed:' in report or
                 result.returncode==1 and rejected_gate(report,comparison),
                 'benchgate input invalid or evaluator failed: '+str(transcript))
            statuses[parts[0].stem] = result.returncode
    return statuses


def run_package(go, tools, arms, package, config, work):
    directory=OUT/package.split('/')[-1]
    directory.mkdir()
    expected,selector,names=plan_route(tools,directory,package,config)
    results={}
    for mode in ('stock','projected'):
        mode_path=directory/mode
        mode_path.mkdir()
        binaries={role:build_binary(go,tools,mode,role,arm,package,config,work,mode_path/role)
                  for role,arm in arms.items()}
        count=measure(arms,binaries,package,selector,names,mode_path)
        gates=gate(tools,expected,arms['H'],mode_path,package,names)
        transcript=(mode_path/'AH-1.forward.gate.txt').read_text()
        confirmed=re.findall(r'^confirmed performance regression: (\S+) (\S+) ',transcript,re.M)
        signed={f'github.com/light-speak/luxo/{package}/{name.removeprefix("Benchmark")}' for name in names}
        need(all(name in signed and unit in ('sec/op','B/op','allocs/op') for name,unit in confirmed)
             and len(confirmed)==len(set(confirmed)), 'unknown or duplicate strict confirmation')
        results[mode]={'processes':count,'gates':gates,'confirmed':confirmed}
    save(directory/'status.json',results)
    return results


def classify(results):
    need(results.keys()=={Path(package).name for package in CONFIG['packages']},'missing package result')
    outcomes={}
    for package, modes in results.items():
        need(modes.keys()=={'stock','projected'},'missing compiler arm')
        for result in modes.values():
            need(result['processes']==120 and set(result['gates'])==CONTROLS|{'AH-1.forward'}
                 and all(type(code) is int and code in (0,1) for code in result['gates'].values())
                 and bool(result['confirmed'])==bool(result['gates']['AH-1.forward']),
                 'invalid strict gate receipt')
        controls_green=all(result['gates'][name]==0 for result in modes.values() for name in CONTROLS)
        stock=modes['stock']['gates']['AH-1.forward']
        projected=modes['projected']['gates']['AH-1.forward']
        reproduced=sorted(name for name,unit in modes['stock']['confirmed'] if unit=='sec/op')
        outcomes[package]={'controlsGreen':controls_green,'stockExit':stock,'projectedExit':projected,
                           'stockConfirmedTiming':reproduced}
    return outcomes


def summarize(directory):
    replies=sorted(directory.glob('*/status.json'))
    need(len(replies)==6,'missing slot receipt')
    outcomes={}
    for path in replies:
        status=json.loads(path.read_bytes())
        correct=status['cpu']==CONFIG['cpu']
        need(correct==status['matched'],'wrong CPU route')
        if not correct:
            need(status['processes']==0,'an unqualified runner sampled')
            continue
        result=classify(status['comparisons'])
        need(status['processes']==480 and result==status['outcomes'],'incomplete native result')
        outcomes[path.parent.name]=result
    print(json.dumps({'matchedSlots':len(outcomes),'results':outcomes},sort_keys=True))
    need(outcomes,'no original CPU in this attempt')
    need(all(item['controlsGreen'] and item['projectedExit']==0
             for result in outcomes.values() for item in result.values()),'controls or projection failed')


def main():
    args=sys.argv[1:]
    if len(args)==2 and args[0]=='--summarize':
        summarize(Path(args[1]))
        return
    route=runner()
    if args==['--probe']:
        if route['matched']:
            with Path(os.environ['GITHUB_OUTPUT']).open('a') as output:
                output.write('matched=true\n')
        else:
            save(OUT/'status.json',{'matched':False,'cpu':route['cpu'],'processes':0})
        return
    need(not args and route['matched'],'requested host is not the original CPU')
    go=shutil.which('go')
    need(go,'Go not found')
    arms=arm_sources(go)
    with TemporaryDirectory(prefix='pr156-native-9v45.',dir=OUT.parent) as folder:
        work=Path(folder)
        tools=build_tools(go,arms['H'],work)
        results={Path(package).name:run_package(go,tools,arms,package,config,work)
                 for package,config in CONFIG['packages'].items()}
    outcomes=classify(results)
    save(OUT/'status.json',{'matched':True,'cpu':route['cpu'],'processes':480,
                            'comparisons':results,'outcomes':outcomes})
    need(all(item['controlsGreen'] and item['projectedExit']==0 for item in outcomes.values()),
         'controls or projection failed')


if __name__=='__main__':
    main()
