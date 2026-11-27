"""Count allocations for normal requests on an already-established HTTP/2 connection."""

from hashlib import sha256
import json
import os
from pathlib import Path
import re
import subprocess

BENCHMARK = 'BenchmarkPriorKnowledgePersistentRequests'
ITERATIONS = 128
COMPARISONS = ('BB', 'CC', 'BC')
COUNTERS = ('tiny', 'nonTiny', 'bytes')
PACKAGE = 'pkg/lux/h2transport'
INPUT = Path(__file__).resolve().parent


def process_order():
    for group in (1, 2):
        for repetition in range(1, 11):
            rotate = (group + repetition - 2) % len(COMPARISONS)
            order = (0, 1) if (group + repetition) % 2 == 0 else (1, 0)
            for comparison in COMPARISONS[rotate:] + COMPARISONS[:rotate]:
                for side in order:
                    yield group, repetition, comparison, side, comparison[side]


def metrics_from(raw, cpu, goos='linux', goarch='amd64'):
    if isinstance(raw, bytes):
        raw = raw.decode()
    counters = re.findall(r'^LUXO-H2-PERSISTENT ((?:[0-9]+ ){6}[0-9]+)$', raw, re.M)
    runs = re.findall(r'^(Benchmark\S+)\s+([0-9]+)[^\n]*$', raw, re.M)
    if len(counters) != 1 or runs != [(BENCHMARK + '-2', str(ITERATIONS))] or raw.splitlines().count('PASS') != 1:
        raise ValueError('wrong counter window or benchmark inventory')
    for key, value in (('cpu', cpu), ('goos', goos), ('goarch', goarch), ('pkg', 'github.com/light-speak/luxo/' + PACKAGE)):
        if re.findall(rf'^{key}: (.+)$', raw, re.M) != [value]:
            raise ValueError(f'wrong {key} for the HTTP/2 allocation fixture')
    if re.search(r'(^FAIL|^--- FAIL|^panic:)', raw, re.M):
        raise ValueError('failed HTTP/2 allocation fixture')
    count, completed, clients, servers, tiny, objects, size = map(int, counters[0].split())
    if (count, completed, clients, servers) != (ITERATIONS, ITERATIONS, 1, 1) or min(objects, size) <= 0:
        raise ValueError('wrong request count, peer count or allocation counters')
    return dict(requests=count, completed=completed, clientConnections=clients, serverConnections=servers,
                tiny=tiny, nonTiny=objects, bytes=size)


def h2_files(directory, fixture):
    return {f.name: sha256(f.read_bytes()).hexdigest() for f in sorted(directory.glob('*.go')) if f != fixture}


def compile_arm(go, call, need, role, source, work, evidence, fixture, module):
    directory = source / PACKAGE
    projected = directory / 'persistent_fixture_diagnostic_test.go'
    need(not projected.exists(), 'fixture would overwrite a test in an original arm')
    sources = h2_files(directory, projected)
    projected.write_bytes(fixture)
    env = dict(os.environ, GOWORK=str(source / 'go.work'))
    version = call(go, 'list', '-m', 'golang.org/x/net', cwd=source, env=env).decode().strip()
    need(version == f'golang.org/x/net {module}', f'{role}: wrong x/net module')
    binary = work / f'{role}-persistent.test'
    call(go, 'test', '-trimpath', '-p=1', '-c', '-o', binary, './' + PACKAGE, cwd=source, env=env)
    buildinfo = call(go, 'version', '-m', binary)
    need(b': go1.27.4\n' in buildinfo and f'\tdep\tgolang.org/x/net\t{module}\t'.encode() in buildinfo,
         f'{role}: binary built with the wrong Go or x/net')
    (evidence / f'{role}-buildinfo.txt').write_bytes(buildinfo)
    need(h2_files(directory, projected) == sources and projected.read_bytes() == fixture, f'{role}: source changed')
    return binary, dict(xNet=version, binarySHA256=sha256(binary.read_bytes()).hexdigest(), h2Files=sources)


def pair_results(processes):
    recorded = [(row['block'], row['pairIndex'], row['comparison'], row['slot'], row['arm']) for row in processes]
    if recorded != list(process_order()):
        raise ValueError('incomplete or reordered persistent-request process matrix')
    pairs = []
    for one, two in zip(processes[::2], processes[1::2]):
        left, right = sorted((one, two), key=lambda row: row['slot'])
        delta = {key: right[key] - left[key] for key in COUNTERS}
        pairs.append(dict(block=left['block'], pairIndex=left['pairIndex'], comparison=left['comparison'],
                          order=[one['slot'], two['slot']], delta=delta,
                          deltaPerRequest={key: value / ITERATIONS for key, value in delta.items()}))
    return pairs


def capture(go, call, json_file, need, output, arms, work, trees, cpu, plan_sha):
    plan_file = INPUT / 'persistent-native-plan.json'
    need(sha256(plan_file.read_bytes()).hexdigest() == plan_sha, 'persistent diagnostic plan changed')
    plan = json.loads(plan_file.read_bytes())
    fixture = (INPUT / 'persistent_fixture_test.go.txt').read_bytes()
    need(sha256(fixture).hexdigest() == plan['fixtureSHA256'] and sha256(Path(__file__).read_bytes()).hexdigest()
         == plan['runnerSHA256'], 'persistent request fixture or parser changed')
    need(cpu == plan['cpu'] and {role: trees[role] for role in 'BC'} == plan['trees'] and
         plan['processes'] == 120 and plan['iterations'] == ITERATIONS, 'persistent native inputs changed')
    evidence = output / 'h2-persistent'
    evidence.mkdir()
    built = {role: compile_arm(go, call, need, role, arms[role], work, evidence, fixture, plan['modules'][role])
             for role in 'BC'}
    need(built['B'][1]['h2Files'] == built['C'][1]['h2Files'], 'original H2 files differ between B and C')
    json_file(evidence / 'binding.json', dict(plan=plan, planSHA256=plan_sha, arms={role: built[role][1] for role in 'BC'}))
    processes = []
    for block, repetition, comparison, slot, role in process_order():
        name = f'{block}-{repetition:02d}-{comparison}-{slot}.txt'
        args = (built[role][0], '-test.run=^$', f'-test.bench=^{BENCHMARK}$', '-test.benchmem',
                f'-test.benchtime={ITERATIONS}x', '-test.count=1', '-test.cpu=2', '-test.timeout=60s')
        with (evidence / name).open('xb') as raw:
            result = subprocess.run(args, cwd=arms[role] / PACKAGE, stdout=raw, stderr=subprocess.STDOUT, timeout=75)
        need(result.returncode == 0, f'{name}: persistent request fixture failed')
        raw = (evidence / name).read_bytes()
        processes.append(dict(block=block, pairIndex=repetition, comparison=comparison, slot=slot,
                              arm=role, raw=name, rawSHA256=sha256(raw).hexdigest(), **metrics_from(raw, cpu)))
    pairs = pair_results(processes)
    need(len(processes) == 120 and len(pairs) == 60, 'wrong persistent allocation inventory')
    json_file(evidence / 'processes.json', processes)
    json_file(evidence / 'pairs.json', pairs)
    return len(processes)
