#!/usr/bin/env python3
"""Replay the original PR 156 codegen confirmation batches on one 9V74 runner."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import tempfile
from time import monotonic_ns
from zipfile import ZipFile

A = '59a4167f129f0bff0e8425125fa02eae87fd347b'
AT = 'c96b8db43227f6ed447d10b0b32deb39284c7f28'
C = 'f5f0aaf83bc2637e91e1dd61dfe134399adaf2c0'
CT = '914bb042400d1a6dc4f69ab12f13b215998afb06'
D = '5d9fa584a66f7b5a4f284a1aaf7e19a1f550b520'
INPUT_SHA = '356623d4e3e6332893e1cfabca0a294417365d4770382d6dc6a7173fb8793501'
FILES = ('manifest.json', 'compile-profile.txt', 'candidate.patch')
CPU = 'AMD EPYC 9V74 80-Core Processor'
TEST_ARGS = ['-test.run=^$', '-test.benchmem', '-test.benchtime=200ms', '-test.count=1', '-test.cpu=2']
UNITS = ('ns/op', 'B/op', 'allocs/op')
ROW = re.compile(r'^(Benchmark\S+)\s+([1-9]\d*)\s+(\d+(?:\.\d+)?) ns/op\s+(\d+) B/op\s+(\d+) allocs/op$')
ENV_KEYS = ('goos', 'goarch', 'pkg', 'cpu')
PHASES = ('AA', 'DD', 'AD', 'CD')


def require(condition, reason):
    if not condition:
        raise RuntimeError(reason)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def disk_sha(path):
    state = hashlib.sha256()
    with path.open('rb') as file:
        for part in iter(lambda: file.read(1 << 20), b''):
            state.update(part)
    return state.hexdigest()


def out(*args, **options):
    return subprocess.check_output(args, **options)


def git(repo, *args, **options):
    return out('git', '--no-replace-objects', '-C', repo, *args, stderr=subprocess.DEVNULL, **options)


def save(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')


def load(capsule):
    require(disk_sha(capsule) == INPUT_SHA, 'unexpected diagnostic payload digest')
    with ZipFile(capsule) as zipped:
        require(set(zipped.namelist()) == set(FILES) and len(zipped.namelist()) == len(FILES) and zipped.testzip() is None,
                'unexpected diagnostic payload members')
        manifest, profile, patch = (zipped.read(name) for name in FILES)
    manifest = json.loads(manifest)
    shards = manifest['shards']
    require(manifest['cpu'] == CPU and list(shards) == ['05', '06', '08'], 'original CPU or shard inventory changed')
    require([len(s['processLeaves']) for s in shards.values()] == [3, 1, 5]
            and [len(s['targets']) for s in shards.values()] == [1, 1, 2], 'original benchmark leaves changed')
    require(digest(profile) == manifest['compileProfileSHA256'], 'original compile profile changed')
    return manifest, profile, patch


def source_tree(repo, work, manifest, patch):
    for revision, expected in ((A, AT), (C, CT)):
        require(git(repo, 'rev-parse', revision+'^{tree}').decode().strip() == expected,
                f'wrong product source tree {revision}')
    index = os.environ | {'GIT_INDEX_FILE': str(work/'D.index')}
    git(repo, 'read-tree', C, env=index)
    git(repo, 'apply', '--cached', '-', input=patch, env=index)
    require(git(repo, 'write-tree', env=index).decode().strip() == D, 'wrong optimized product tree')
    for revision in (A, C, D):
        for path, expected in manifest['fixtures'].items():
            require(digest(git(repo, 'show', f'{revision}:{path}')) == expected, f'fixture changed {revision}:{path}')
    for path in ('internal/benchlayout', 'scripts/benchrun', 'scripts/benchplan', 'scripts/benchgate', 'scripts/setup-modules.sh'):
        require(len({git(repo,'rev-parse',f'{rev}:{path}') for rev in (A, C, D)}) == 1, f'benchmark tooling changed: {path}')


def schedule():
    for group in (1, 2):
        for turn in range(10):
            for i in range(4):
                phase = PHASES[(i+turn+group-1) % len(PHASES)]
                reverse = phase in ('AD', 'CD') and (turn+group) % 2 == 0
                yield group, turn+1, phase, phase[::-1] if reverse else phase


def parse_process(raw, leaves):
    wanted = ('linux', 'amd64', 'github.com/light-speak/luxo/pkg/codegen', CPU)
    lines = raw.splitlines()
    require(lines.count('PASS') == 1, 'missing successful benchmark process')
    for key, value in zip(ENV_KEYS, wanted):
        require([line.split(': ',1)[1].strip() for line in lines if line.startswith(key+': ')] == [value],
                f'wrong benchmark process {key}')
    measurements = {}
    for line in lines:
        if not line.startswith('Benchmark'):
            continue
        row = ROW.fullmatch(line.strip())
        require(row is not None and row[1] not in measurements, f'malformed or duplicate benchmark row: {line}')
        measurements[row[1]] = ([int(row[2]), float(row[3]), int(row[4]), int(row[5])], line)
    require(set(measurements) == set(leaves) and all(result[0][1] > 0 for result in measurements.values()),
            'missing, nonpositive, or unexpected benchmark leaf')
    return measurements


def archive(repo, revision, directory):
    directory.mkdir()
    cmd = ('git', '--no-replace-objects', '-C', repo, 'archive', revision)
    with subprocess.Popen(cmd, stdout=subprocess.PIPE) as archive_process:
        subprocess.run(('tar','-xf','-','-C',directory), stdin=archive_process.stdout, check=True)
        require(archive_process.wait() == 0, f'failed product archive {revision}')


def prepare(base, old, work, evidence, profile):
    common_env = os.environ | {'GOWORK':'off', 'GOTOOLCHAIN':'local'}
    bindir = work/'bin'
    bindir.mkdir()
    tools = {name:bindir/name for name in ('benchplan','benchgate','benchrun','benchstat')}
    with (evidence/'build.log').open('w') as log:
        for name in ('benchplan','benchgate','benchrun'):
            subprocess.run(('go','build','-o',tools[name],'./scripts/'+name), cwd=old, env=common_env,
                           stdout=log,stderr=subprocess.STDOUT,check=True)
        subprocess.run(('go','install','golang.org/x/perf/cmd/benchstat@v0.0.0-20260825160852-19be9d8e6c70'),
                       cwd=old,env=common_env | {'GOBIN':str(bindir)},stdout=log,stderr=subprocess.STDOUT,check=True)
        actual_profile = out(tools['benchrun'],'performance-profile')
        require(actual_profile == profile, 'native compiler profile differs from original')
        (evidence/'compile-profile.txt').write_bytes(actual_profile)
        sources = {'A':base,'C':work/'C','D':work/'D'}
        archive(old,C,sources['C'])
        archive(old,D,sources['D'])
        save(evidence/'tool-hashes.json',{name:disk_sha(exe) for name,exe in tools.items()})
        for name,source in sources.items():
            subprocess.run(('sh','scripts/setup-modules.sh',source), cwd=source, env=common_env,
                           stdout=log,stderr=subprocess.STDOUT,check=True)
            mode = 'compile-performance-baseline' if name=='A' else 'compile-performance'
            binaries = work/f'bin-{name}'
            subprocess.run((tools['benchrun'],mode,source,binaries,'pkg/codegen','default'),env=common_env,
                           stdout=log,stderr=subprocess.STDOUT,check=True)
            exe = binaries/'pkg-codegen.test'
            section = work/f'{name}.text'
            elf_copy = work/'section-reader.elf'
            original = disk_sha(exe)
            subprocess.run(('objcopy','--dump-section',f'.text={section}',exe,elf_copy),check=True)
            elf_copy.unlink()
            require(disk_sha(exe) == original, 'section inspection modified benchmark binary')
            require(section.stat().st_size > 0, 'ELF has empty .text')
            save(evidence/f'{name}-elf.json',{'sha256':original,'text':disk_sha(section),
                 'goVersion':out('go','version','-m',exe,text=True), 'buildID':out('go','tool','buildid',exe,text=True),
                 'elf':out('readelf','-WS',exe,text=True), 'profile':'default','compileMode':mode,'source':str(source)})
            section.unlink()
    return sources, tools


def observe(arm, role, manifest, tools, sources, work, evidence, point):
    values = {}
    logs = []
    for shard, batch in manifest['shards'].items():
        args = [tools['benchrun'],'run-performance',sources[arm],work/f'bin-{arm}','pkg/codegen','default',
                TEST_ARGS[0],'-test.bench='+batch['filter'],*TEST_ARGS[1:]]
        raw = evidence/f'{point}-{role}-{arm}-{shard}.raw'
        with raw.open('w') as stream, subprocess.Popen(args,stdout=stream,stderr=subprocess.STDOUT,
                                                      start_new_session=True) as process:
            try:
                status = process.wait(timeout=180)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                raise
        require(status == 0, f'native benchmarking failed: {raw}')
        values.update(parse_process(raw.read_text(),batch['processLeaves']))
        logs.append({'name':raw.name,'pid':process.pid,'argv':list(map(str,args))})
    return values, logs


def capture(manifest, tools, sources, work, evidence):
    rows = [leaf for shard in manifest['shards'].values() for leaf in shard['targets']]
    expected = [{'Package':'pkg/codegen','Name':leaf} for leaf in rows]
    save(evidence/'expected.json',{'Base':expected,'Head':expected})
    headers = f'goos: linux\ngoarch: amd64\npkg: github.com/light-speak/luxo/pkg/codegen\ncpu: {CPU}\n'
    with (evidence/'pairs.jsonl').open('w',buffering=1) as raw:
        for group,turn,phase,order in schedule():
            point = f'{phase}-{group}-{turn:02d}'
            started = monotonic_ns()
            by_side = {}
            for index,arm in enumerate(order):
                role = ('R','L')[index] if phase != order else ('L','R')[index]
                by_side[role] = observe(arm,role,manifest,tools,sources,work,evidence,point)
            result = {'group':group,'turn':turn,'phase':phase,'order':order,'elapsedNs':monotonic_ns()-started,
                      'load':list(os.getloadavg()),'processes':{k:v[1] for k,v in by_side.items()}}
            for role,(observed,_) in by_side.items():
                with (evidence/f'{phase}-{group}-{role}.txt').open('a') as stream:
                    stream.write(headers+'\n'.join(observed[leaf][1] for leaf in rows)+'\nPASS\n')
            left,right = (by_side[role][0] for role in ('L','R'))
            result['signed'] = {leaf:[r-l for l,r in zip(left[leaf][0][1:],right[leaf][0][1:])] for leaf in rows}
            result['positive'] = {leaf:[max(v,0) for v in delta] for leaf,delta in result['signed'].items()}
            print(json.dumps(result),file=raw)


def evaluate(tools, base, evidence):
    statuses = {}
    for phase in PHASES:
        for group in (1,2):
            left,right = (evidence/f'{phase}-{group}-{side}.txt' for side in ('L','R'))
            out(tools['benchplan'],'--verify',evidence/'expected.json',left,right)
            for a,b,suffix in ((left,right,''), (right,left,'-reverse')) if phase in ('AA','DD') else ((left,right,''),):
                label = f'{phase}-{group}{suffix}'
                csv = evidence/f'{label}.csv'
                with csv.open('w') as stream,(evidence/f'{label}.warnings').open('w') as warnings:
                    subprocess.run((tools['benchstat'],'-alpha','0.01','-filter','.unit:(sec/op OR B/op OR allocs/op)',
                                    '-format','csv',f'base={a}',f'head={b}'),stdout=stream,stderr=warnings,check=True)
                if phase in ('AA','DD'):
                    statuses[label] = gate(tools['benchgate'],evidence,label,[csv])
    approved = ['--approved-costs',base/'scripts/benchgate/approved-costs.json','--base-sha',A]
    # Both groups already contain every target. Full confirmation applies the same metric thresholds
    # to the same comparisons, including when no primary row requires targeted confirmation.
    for phase in ('AD','CD'):
        args = (approved if phase=='AD' else []) + [evidence/f'{phase}-{g}.csv' for g in (1,2)]
        statuses[phase] = gate(tools['benchgate'],evidence,phase,args)
    save(evidence/'gates.json',statuses)
    require(not any(statuses.values()), 'original strict threshold or same-binary control rejected the run')


def gate(exe, evidence, label, args):
    with (evidence/f'{label}.gate.txt').open('w') as log:
        return subprocess.run([exe,*args],stdout=log,stderr=subprocess.STDOUT).returncode


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('mode',choices=('check','run'))
    parser.add_argument('capsule',type=Path)
    parser.add_argument('root',type=Path)
    parser.add_argument('old',type=Path,nargs='?')
    parser.add_argument('evidence',type=Path,nargs='?')
    args=parser.parse_args()
    manifest, profile, patch = load(args.capsule)
    root=args.root.resolve()
    if args.mode=='check':
        with tempfile.TemporaryDirectory(dir=args.capsule.resolve().parent) as scratch:
            source_tree(root,Path(scratch),manifest,patch)
        print('verified exact A/C/D, original fixtures, capsule, and schedule',D,len(list(schedule())))
        return
    old=args.old.resolve(); evidence=args.evidence.resolve()
    evidence.mkdir(parents=True,exist_ok=False)
    require(platform.system()=='Linux' and platform.machine()=='x86_64','expected native Linux amd64')
    host=out('lscpu',text=True)
    os_release = Path('/etc/os-release').read_text()
    (evidence/'host.txt').write_text(host+out('uname','-a',text=True)+os_release)
    require(re.findall(r'^Model name:\s*(.+)$',host,re.M) == [CPU], 'runner does not match the original 9V74')
    require(re.findall(r'^CPU\(s\):\s*(\d+)$',host,re.M) == ['4'] and 'VERSION_ID="24.04"' in os_release,
            'runner does not match Ubuntu 24.04 / four logical CPUs')
    require(out('go','env','GOVERSION','GOOS','GOARCH','GOAMD64','CGO_ENABLED',text=True).splitlines() ==
            ['go1.27.1','linux','amd64','v1','1'],'wrong native Go benchmark environment')
    for repo,revision in ((root,A),(old,C)):
        require(git(repo,'rev-parse','HEAD').decode().strip()==revision and not git(repo,'status','--porcelain','-uno'),
                f'checkout differs from {revision}')
    save(evidence/'binding.json',{'A':A,'AT':AT,'C':C,'CT':CT,'D':D,'payload':INPUT_SHA,'schedule':list(schedule()),
                                 'goFlags':TEST_ARGS,'metrics':UNITS,'original':manifest})
    with tempfile.TemporaryDirectory(dir=evidence.parent,prefix='bench-work-') as scratch:
        work=Path(scratch)
        os.environ |= {'TMPDIR':scratch,'GOTMPDIR':scratch}
        source_tree(old,work,manifest,patch)
        sources,tools=prepare(root,old,work,evidence,profile)
        capture(manifest,tools,sources,work,evidence)
        evaluate(tools,root,evidence)


if __name__=='__main__':
    main()
