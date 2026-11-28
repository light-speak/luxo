#!/usr/bin/env python3
"""Replay the original #158 cold H2C leaf against exact release and attribution arms."""

import argparse
import json
import os
from hashlib import sha256
from math import isfinite
from pathlib import Path
import re
import runpy
import shutil
from statistics import median
import subprocess
from tempfile import TemporaryDirectory


HERE = Path(__file__).resolve().parent
MANIFEST = HERE / 'manifest.json'
MANIFEST_SHA = '75ff89da6a0d93f0b6880eeeebd7e210714f928f9bc4ba46b593d632e693fdf4'
REF = 'refs/heads/diagnostics/pr158-h2c-preface'


def need(condition, diagnostic):
    if not condition:
        raise RuntimeError(diagnostic)


def call(*args, cwd=None, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env)


def git(repo, *args, env=None):
    return call('git', '--no-replace-objects', '-C', repo, *args, env=env).decode().strip()


def sha(path):
    return sha256(path.read_bytes()).hexdigest()


def write(path, value):
    with path.open('x') as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write('\n')


def route(config, text):
    models = {line.split(':', 1)[1].strip() for line in text.splitlines() if line.startswith('model name')}
    need(len(models) == 1, 'missing or mixed hosted CPUs')
    cpu, = models
    return cpu, cpu in config['routes']


def derive(repo, work, config, *, local):
    a, b, c = (config[role] for role in 'ABC')
    need(git(repo, 'rev-parse', a['commit'] + '^{tree}') == a['tree'], 'wrong A tree')
    if not local:
        need(git(repo, 'rev-parse', 'HEAD') == c['commit'] and
             git(repo, 'rev-parse', 'HEAD^{tree}') == c['tree'] and
             git(repo, 'show', '-s', '--format=%P', 'HEAD').split() == [c['firstParent'], a['commit']], 'wrong immutable candidate commit')
    objects = work / 'objects'
    objects.mkdir()
    env = dict(os.environ, GIT_INDEX_FILE=str(work/'private.index'), GIT_OBJECT_DIRECTORY=str(objects),
               GIT_ALTERNATE_OBJECT_DIRECTORIES=git(repo, 'rev-parse', '--path-format=absolute', '--git-path', 'objects'),
               GIT_NO_LAZY_FETCH='1', GIT_OPTIONAL_LOCKS='0')
    # Only these two B blobs equal A. Rebuild B with an isolated index and object store.
    a_to_c = call('git', '--no-replace-objects', '-C', repo, 'diff', '--binary', '--full-index',
                  a['tree'], c['tree'], '--', *config['patchPaths'], env=env)
    need(a_to_c, 'missing expected H2C patch')
    patch = work/'h2c.patch'
    patch.write_bytes(a_to_c)
    git(repo, 'read-tree', c['tree'], env=env)
    git(repo, 'apply', '--cached', '--reverse', patch, env=env)
    need(git(repo, 'write-tree', env=env) == b['tree'], 'reconstructed B differs')
    need(git(repo, 'diff', '--name-only', b['tree'], c['tree'], env=env).splitlines() == config['patchPaths'], 'B/C extra paths')
    for role in 'ABC':
        source = config[role]
        for path, name in (('go.mod', 'goModBlob'), ('go.sum', 'goSumBlob')):
            need(git(repo, 'rev-parse', source['tree']+':'+path, env=env) == source[name],
                 f'{role} wrong locked module graph')
        sums = git(repo, 'show', source['tree']+':go.sum', env=env).splitlines()
        for name, suffix in (('xNetSum', ''), ('xNetGoModSum', '/go.mod')):
            need(f"golang.org/x/net {source['xNet']}{suffix} {source[name]}" in sums, f'{role} x/net checksum drift')
    if local:
        changed = git(repo, 'diff', '--name-only', config['previousB'], b['tree'], env=env).splitlines()
        need(changed == config['mainOnlyPaths'], 'previous dependency candidate differs beyond the TS merge')
    return env


def materialize(repo, work, config, git_env):
    arms = {}
    for role in 'ABC':
        dest = work / role
        dest.mkdir()
        with subprocess.Popen(['git','--no-replace-objects','-C',repo,'archive',config[role]['tree']],
                              stdout=subprocess.PIPE,env=git_env) as archive:
            subprocess.run(['tar','-xf','-','-C',dest], stdin=archive.stdout, check=True)
            need(archive.wait() == 0, f'could not archive {role}')
        (dest/'.tmp').mkdir()
        call('sh', dest/'scripts/setup-modules.sh', dest, cwd=dest)
        need(sha(dest/'pkg/lux/h2transport/handler_bench_test.go') == config['fixtureSHA256'], 'fixture byte drift')
        arms[role] = dest
    need(len({(path/'go.work').read_bytes() for path in arms.values()}) == 1, 'workspace differs')
    return arms


def helper(path, config, root, out, *, native):
    for name, digest in config['helperFiles'].items():
        need(sha(path/name) == digest, 'untrusted sampler helper ' + name)
    need(git(path, 'rev-parse', 'HEAD') == config['helperCommit'], 'sampler helper revision mismatch')
    functions = runpy.run_path(str(path/'run.py'))
    context = functions['measure'].__globals__
    context.update(ROOT=root, OUT=out, CONFIG={'baseCommit':config['A']['commit']})
    if native:
        context['HOT'][config['case']['package']] += r'|servePriorKnowledge|prefacedConn.*Read|readMetaFrame'
    else:
        def smoke_build_info(go, binary, target, package, work):
            target.mkdir()
            (target/'buildinfo.txt').write_bytes(call(go, 'version', '-m', binary))
        context['elf'] = smoke_build_info
    return functions


def compiler_identity(evidence, source, role, package, profile):
    name = package.replace('/', '-') + ('' if profile == 'default' else '-' + profile)
    link_info = (evidence/name/'buildinfo.txt').read_text().splitlines()
    need(link_info[0].endswith(': go1.27.4'), f'{role} wrong linked compiler')
    version = source['xNet']
    pins = [line.split()[2:] for line in link_info if line.split()[:2] == ['dep','golang.org/x/net']]
    need(pins == [[version, source['xNetSum']]], f'{role} wrong linked x/net content')
    graph = [line.split() for line in (evidence/'modules.txt').read_text().splitlines()]
    need([line for line in graph if line[:1]==['golang.org/x/net']] == [['golang.org/x/net',version]], f'{role} wrong resolved dependency')
    need(json.loads((evidence/(name+'.overlay.json')).read_text()) == {}, 'default workload unexpectedly overlaid')


def run_smoke(arms, bins, config, runtime, out):
    case = config['case']
    package, profile = case['package'], case['profile']
    for role, src in arms.items():
        tally = runtime['sample'](bins[role][package,profile], src/package, '.', '1x')
        names = re.findall(r'^(Benchmark\S+-2)\s+1\s+', tally.decode(), re.M)
        need(sorted(names) == config['inventory'] and b'\nPASS\n' in tally, f'{role} full default inventory differs')
        (out/f'{role}-default-inventory.raw').write_bytes(tally)
        compiler_identity(out/(role+'-build'), config[role], role, package, profile)


def summarize_pairs(out, cpu, runtime, config):
    target = config['case']['name']
    metrics = ('ns/op','B/op','allocs/op')
    outcomes = {}
    rows = (out/'order.tsv').read_text().splitlines()
    expected = []
    package, profile = (config['case'][name] for name in ('package','profile'))
    for block,pair,code,visit in runtime['schedule']():
        for slot in visit:
            record = (len(expected)+1,block,pair,code,code[slot],('left','right')[slot],package,profile)
            expected.append('\t'.join(map(str,record)))
    need(rows == expected and len(rows) == 240, 'invalid ordered process receipt')
    for code in runtime['PAIRS']:
        for block in (1,2):
            groups = []
            for side in ('left','right'):
                raw = (out/f'{code}-{block}.{side}.raw').read_text().splitlines()
                counts = [line[5:].rstrip(' \t') for line in raw if line.startswith('cpu: ')]
                need(counts == [cpu]*10 and raw.count('PASS') == 10, 'invalid CPU, exit or pair count')
                for label, wanted in [('goos: ', 'linux'), ('goarch: ', 'amd64'),
                                      ('pkg: ', 'github.com/light-speak/luxo/'+package)]:
                    need([line[len(label):] for line in raw if line.startswith(label)] == [wanted]*10,
                         'invalid native sample ' + label)
                samples = []
                for row in raw:
                    if not row.startswith('Benchmark'):
                        continue
                    name,iters,*values = row.split()
                    data = dict(zip(values[1::2],map(float,values[::2])))
                    need(name==target and int(iters)>0 and len(values)==2*len(data) and
                         all(unit in data and isfinite(data[unit]) and data[unit]>=0 for unit in metrics),
                         'invalid original leaf/metric')
                    samples.append(data)
                need(len(samples)==10, 'missing original benchmark leaf')
                groups.append(samples)
            outcomes[f'{code}-{block}'] = {unit:summarize_delta([row[unit] for row in groups[0]],
                                                              [row[unit] for row in groups[1]]) for unit in metrics}
    return outcomes


def summarize_delta(a,b):
    change = [head-base for base,head in zip(a,b)]
    return {'baseMedian':median(a),'headMedian':median(b),'deltas':change,'maxPositive':max(0,*change)}


def assess(statuses, out):
    labels = {f'{code}-{group}{suffix}' for code in ('AA','BB','CC')
              for group in (1,2) for suffix in ('','.reverse')} | {'AB-1','AC-1','BC-1'}
    need(statuses.keys() == labels, 'invalid benchmark gate receipt')
    for label, code in statuses.items():
        gate = (out/(label+'.gate.txt')).read_text()
        if code == 0:
            need('performance gate passed:' in gate,'invalid passing gate transcript')
        elif code != 1:
            raise RuntimeError(f'gate process failed in {label}: {code}')
        elif label.split('-')[0] in ('AA','BB','CC'):
            need('performance gate requires confirmation for' in gate,'invalid same-arm gate transcript')
        else:
            need('performance gate rejected ' in gate and 'confirmed performance regression:' in gate,'invalid cross-arm gate transcript')
    ab = (out/'AB-1.gate.txt').read_text()
    confirmed = set(re.findall(r'^confirmed performance regression: \S+ (\S+) ',ab,re.M))
    return statuses['AB-1']==1 and confirmed=={'B/op','allocs/op'} and all(not value for key,value in statuses.items() if key!='AB-1')


def main():
    args = argparse.ArgumentParser(description=__doc__)
    args.add_argument('mode', choices=('check','smoke','native','probe'))
    args.add_argument('--repo',type=Path)
    args.add_argument('--helper',type=Path)
    args.add_argument('--out',type=Path)
    args.add_argument('--go',default='go')
    params=args.parse_args()
    need(sha(MANIFEST)==MANIFEST_SHA,'manifest changed')
    config=json.loads(MANIFEST.read_bytes())
    if params.mode=='probe':
        cpu, matched=route(config,Path('/proc/cpuinfo').read_text())
        print(json.dumps({'cpu':cpu,'matched':matched}))
        return
    need(params.repo is not None and params.out is not None,'repo and project temp output required')
    repo,out=params.repo.resolve(),params.out.resolve()
    need('.tmp' in out.parts,'output must be in project .tmp')
    native=params.mode=='native'
    cpu=''
    if native:
        need((os.environ.get('GITHUB_REPOSITORY'), os.environ.get('GITHUB_REF'), os.environ.get('RUNNER_KIND')) ==
             ('light-speak/luxo', REF, 'github-hosted'), 'unexpected hosted event')
        cpu, matched=route(config,Path('/proc/cpuinfo').read_text())
        need(matched and 'VERSION_ID="24.04"' in Path('/etc/os-release').read_text().splitlines() and
             call('getconf','_NPROCESSORS_ONLN').strip()==b'4', 'unmatched native CPU/OS shape')
    out.mkdir()
    with TemporaryDirectory(prefix='h2c-work.',dir=out.parent) as workdir:
        work=Path(workdir)
        git_env=derive(repo,work,config,local=not native)
        if params.mode=='check':
            write(out/'binding.json',{'source':config,'mode':'offline source only'})
            return
        need(params.helper is not None,'pinned native helper checkout required')
        go=shutil.which(params.go)
        need(go,'missing Go compiler')
        temp=work/'gotmp'
        temp.mkdir()
        os.environ.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='',GOMAXPROCS='2',GOTMPDIR=str(temp),
                          TMPDIR=str(temp),PATH=str(Path(go).parent)+os.pathsep+os.environ['PATH'])
        if not native:
            os.environ.update(GOPROXY='off',GOSUMDB='off')
        fields=('GOVERSION','GOHOSTOS','GOHOSTARCH','GOOS','GOARCH','GOAMD64','CGO_ENABLED','GOFLAGS','GOTOOLCHAIN')
        environment=call(go,'env',*fields).decode().splitlines()
        need(environment[0]=='go1.27.4','wrong patched Go compiler')
        if native:
            need(environment==['go1.27.4','linux','amd64','linux','amd64','v1','1','','local'],'wrong native Go environment')
        arms=materialize(repo,work,config,git_env)
        root=arms['C']
        runtime=helper(params.helper.resolve(),config,root,out,native=native)
        tools, wrapper=work/'bin',work/'wrapper'
        tools.mkdir(); wrapper.mkdir()
        shutil.copyfile(params.helper/'go-capture.py',wrapper/'go')
        (wrapper/'go').chmod(0o700)
        for tool in (('benchrun','benchplan','benchgate') if native else ('benchrun','benchplan')):
            call(go,'build','-p','1','-o',tools/tool,'./scripts/'+tool,cwd=root)
        compile_profile=call(tools/'benchrun','performance-profile',cwd=root)
        need(sha256(compile_profile).hexdigest()==config['compileProfileSHA256'],'wrong compiler profile')
        (out/'compiler-profile.txt').write_bytes(compile_profile)
        case=config['case']
        grouped=runtime['group_cases']([case])
        identities=[{'Package':case['package'],'Name':case['name']}]
        expected=out/'expected.json'
        write(expected,{'Base':identities,'Head':identities})
        targets,batch,planned=(out/f for f in ('targets.tsv','batch.tsv','planned.json'))
        targets.write_text(case['package']+'\t'+case['name']+'\n')
        call(tools/'benchplan','--confirmation-plan',expected,targets,batch,planned)
        call(tools/'benchplan','--verify-profile-batch','confirmation',planned,batch)
        need(batch.read_text() == f"{case['package']}\t{case['profile']}\t^BenchmarkPriorKnowledgeConnectionLifecycle$\n",'wrong official route')
        bins={role:runtime['build_profile'](go,tools,role,src,work,grouped) for role,src in arms.items()}
        run_smoke(arms,bins,config,runtime,out)
        write(out/'binding.json',{'source':config,'compiler':dict(zip(fields,environment)),'goSHA256':sha(Path(go)),
                                 'CCommit':git(repo,'rev-parse','HEAD') if native else None,'cpu':cpu,
                                 'bootID':Path('/proc/sys/kernel/random/boot_id').read_text().strip() if native else None,
                                 'mode':'native' if native else 'local 1x correctness only'})
        if native:
            call(go,'install','golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70',cwd=root,env=dict(os.environ,GOBIN=str(tools)))
            process_count=runtime['measure'](arms,bins,grouped)
            write(out/'pairs.json',summarize_pairs(out,cpu,runtime,config))
            status=runtime['gate'](tools,expected)
            qualified=assess(status,out)
            write(out/'status.json',{'cpu':cpu,'processes':process_count,'gates':status,'qualified':qualified})
            need(qualified,'cold connection gate or attribution did not qualify')


if __name__=='__main__':
    main()
