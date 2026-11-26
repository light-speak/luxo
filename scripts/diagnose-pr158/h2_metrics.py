"""Measure allocation counters for the pinned HTTP/2 new-connection workload."""

from hashlib import sha256
from itertools import product
import os
from pathlib import Path
import re


PACKAGE = 'pkg/lux/h2transport'
FIXTURE = 'handler_bench_test.go'
BENCHMARK = 'BenchmarkPriorKnowledgeConnectionLifecycle'
FIXTURE_SHA = 'd2dbd1919f88fd102ba4547eb9f1d46b3b2046f61509b48f376c720dd669c017'
PROFILE_SHA = '1ac34b80aa878a2dfb1446462c0cd5cf41e413e13e10138fbea89aef59acd6af'
COUNTERS = ('tinyObjects', 'heapObjectsExcludingTiny', 'heapBytes')
ITERATIONS = 128
ORIGINAL_LOOP = b'''\tb.ReportAllocs()
\tfor b.Loop() {
\t\trunPriorKnowledgeConnection(b, httpServer.Listener.Addr().String(), request)
\t\t<-finished
\t}
'''
READ_START = b'''\tsamples := [...]metrics.Sample{
\t\t{Name: "/gc/heap/tiny/allocs:objects"},
\t\t{Name: "/gc/heap/allocs:objects"},
\t\t{Name: "/gc/heap/allocs:bytes"},
\t}
\tmetrics.Read(samples[:])
\tvar start [len(samples)]uint64
\tfor i := range samples {
\t\tstart[i] = samples[i].Value.Uint64()
\t}
'''
READ_END = b'''\tmetrics.Read(samples[:])
\tfor i := range samples {
\t\tif samples[i].Value.Uint64() < start[i] {
\t\t\tb.Fatal("allocation counter decreased")
\t\t}
\t}
\tfmt.Printf("LUXO-H2-ALLOCS %d %d %d %d\\n", b.N, samples[0].Value.Uint64()-start[0],
\t\tsamples[1].Value.Uint64()-start[1], samples[2].Value.Uint64()-start[2])
'''


def instrument(original):
    if sha256(original).hexdigest() != FIXTURE_SHA or original.count(ORIGINAL_LOOP) != 1:
        raise ValueError('unexpected HTTP/2 benchmark fixture')
    with_imports = original.replace(b'\t"context"\n', b'\t"context"\n\t"fmt"\n', 1)
    with_imports = with_imports.replace(b'\t"testing"\n', b'\t"runtime/metrics"\n\t"testing"\n', 1)
    return with_imports.replace(ORIGINAL_LOOP, READ_START + ORIGINAL_LOOP + READ_END)


def metrics_from(output, iterations):
    sample = re.findall(rb'^LUXO-H2-ALLOCS (\d+) (\d+) (\d+) (\d+)$', output, re.M)
    runs = re.findall(rb'^' + BENCHMARK.encode() + rb'-2\s+(\d+)\s+.*allocs/op\s*$', output, re.M)
    if len(sample) != 1 or runs != [str(iterations).encode()] or b'\nPASS\n' not in output:
        raise ValueError('invalid HTTP/2 allocation sample')
    actual, *totals = map(int, sample[0])
    if actual != iterations or min(totals) <= 0:
        raise ValueError('wrong allocation counter window')
    return dict(zip(COUNTERS, totals))


def compile_arm(go, call, json_file, need, role, source, work, evidence):
    original = source / PACKAGE / FIXTURE
    overlay_source = work / f'{role}-metrics.go'
    rendered = instrument(original.read_bytes())
    overlay_source.write_bytes(rendered)
    overlay = work / f'{role}-overlay.json'
    json_file(overlay, {'Replace': {str(original): str(overlay_source)}})
    env = dict(os.environ, GOWORK=str(source / 'go.work'))
    versions = call(go, 'list', '-m', 'golang.org/x/net', cwd=source, env=env).decode().strip()
    need(versions == ('golang.org/x/net v0.58.0' if role == 'B' else 'golang.org/x/net v0.63.0'),
         'wrong x/net module version')
    stock, metrics = (work / f'{role}-{mode}.test' for mode in ('stock', 'metrics'))
    for executable, flags in ((stock, ()), (metrics, ('-overlay', overlay))):
        call(go, 'test', '-trimpath', '-c', *flags, '-o', executable, './' + PACKAGE, cwd=source, env=env)
        (evidence / f'{executable.stem}-buildinfo.txt').write_bytes(call(go, 'version', '-m', executable))
    symbol = r'golang.org/x/net/http2.\(\*Framer\).readMetaFrame($|\.)'
    instructions = call(go, 'tool', 'objdump', '-s', symbol, stock)
    need(b'TEXT ' in instructions and b'frame.go:' in instructions, 'missing stock Framer instructions')
    (evidence / f'{role}-stock-framer.txt').write_bytes(instructions)
    return metrics, {'fixtureSHA256': FIXTURE_SHA, 'projectedFixtureSHA256': sha256(rendered).hexdigest(),
                     'stockSHA256': sha256(stock.read_bytes()).hexdigest(),
                     'metricsSHA256': sha256(metrics.read_bytes()).hexdigest(), 'xNet': versions}


def process_order():
    for block, repetition, pair in product((1, 2), range(1, 11), ('BB', 'CC', 'BC')):
        visit = (0, 1) if (block + repetition) % 2 == 0 else (1, 0)
        for slot in visit:
            yield block, repetition, pair, slot, pair[slot]


def capture(go, call, json_file, need, output, arms, work, trees):
    evidence = output / 'h2-tiny'
    evidence.mkdir()
    binaries, metadata = {}, {}
    for role in 'BC':
        binaries[role], metadata[role] = compile_arm(go, call, json_file, need, role, arms[role], work, evidence)
    need(metadata['B']['projectedFixtureSHA256'] == metadata['C']['projectedFixtureSHA256'], 'fixture overlay differs')
    json_file(evidence / 'binding.json', {'trees': {role: trees[role] for role in 'BC'}, 'arms': metadata,
                                        'originalProfile': {'run': 45879037431, 'attempt': 2,
                                                            'artifact': 15147012855, 'sha256': PROFILE_SHA},
                                        'iterations': ITERATIONS, 'benchmark': BENCHMARK})
    processes = []
    for block, repetition, pair, slot, role in process_order():
        result = call(binaries[role], '-test.run=^$', f'-test.bench=^{BENCHMARK}$', '-test.benchmem',
                      f'-test.benchtime={ITERATIONS}x', '-test.count=1', '-test.cpu=2', '-test.timeout=60s',
                      cwd=arms[role] / PACKAGE)
        counters = metrics_from(result, ITERATIONS)
        filename = f'{block}-{repetition:02d}-{pair}-{slot}.txt'
        (evidence / filename).write_bytes(result)
        processes.append({'block': block, 'pairIndex': repetition, 'comparison': pair, 'slot': slot,
                          'arm': role, **counters, 'raw': filename})
    need(len(processes) == 120, 'incomplete HTTP/2 allocation counter matrix')
    json_file(evidence / 'processes.json', processes)
    return len(processes)
