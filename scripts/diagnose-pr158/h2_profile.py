"""Compare exact HTTP/2 benchmark heap profiles after the timing matrix."""

from collections import defaultdict
from hashlib import sha256
import re


PACKAGE = 'pkg/lux/h2transport'
BENCHMARK = 'BenchmarkPriorKnowledgeConnectionLifecycle'
PROFILE = (PACKAGE, 'default')
MEASURES = ('allocObjects', 'allocSpace')
ARCHIVE_REFERENCES = {
    'strict': {
        'run': '45866988415', 'attempt': 1, 'artifact': '15144311780',
        'sha256': 'e33f4eeae6d0c2d06054f8b31345fe3dc3915648abac35cf479a3ec7ed64c91d',
    },
    'intel': {
        'run': '45876655016', 'attempt': 1,
        'sha256': 'c3e1dd8631ece1380de7700ac5085595722d643df3d3cae81351fba3671cfc1d',
    },
}
LOCATION = re.compile(r'^\s*(\d+):\s+0x[0-9a-fA-F]+(?:\s+M=\d+)?(?:\s+(\S+))?')


def classify(frames):
    tags = []
    if any(f.startswith('golang.org/x/net/http2.') for f in frames):
        tags.append('xNetTransport')
    if any(f.startswith('net/http.http2') or f.startswith('net/http.(*http2') for f in frames):
        tags.append('stdlibH2')
    if any(f.startswith('github.com/light-speak/luxo/pkg/lux/h2transport.') for f in frames):
        tags.append('luxoTransportOrFixture')
    if any(f.startswith(('fmt.', 'net.(*TCPAddr).String', 'net.JoinHostPort')) for f in frames):
        tags.append('formatter')
        if any(f.endswith('.runPriorKnowledgeConnection') for f in frames) or 'xNetTransport' in tags:
            tags.append('connectionFormatter')
        if any(f.startswith(('testing.BenchmarkResult.', 'testing.(*benchContext).processBench')) for f in frames):
            tags.append('benchmarkResultFormatter')
    return tuple(tags)


def describe(raw):
    header, separator, locations = raw.partition('\nLocations\n')
    if not separator or '\nPeriod: 1\n' not in '\n' + header:
        raise ValueError('invalid allocation profile period or missing symbol table')
    _, separator, samples = header.partition('\nSamples:\n')
    if not separator:
        raise ValueError('allocation profile has no samples')
    types, *rows = samples.splitlines()
    types = [item.split('/', 1)[0] for item in types.split()]
    objects, space = (types.index(kind) for kind in ('alloc_objects', 'alloc_space'))
    frames = defaultdict(list)
    current = None
    table, _, _ = locations.partition('\nMappings\n')
    for line in table.splitlines():
        first = LOCATION.match(line)
        if first:
            current = int(first[1])
            if first[2]:
                frames[current].append(first[2])
        elif current is not None and re.match(r'^\s+\S+\s+\S+:\d+', line):
            frames[current].append(line.strip().split()[0])

    totals = defaultdict(lambda: [0, 0])
    roots = defaultdict(lambda: [0, 0])
    stack_values = defaultdict(lambda: [0, 0])
    records = 0
    for line in rows:
        values, separator, ids = line.partition(':')
        if not separator or not values.split() or not all(v.lstrip('-').isdigit() for v in values.split()):
            continue
        counts = list(map(int, values.split()))
        if len(counts) != len(types) or counts[objects] < 0 or counts[space] < 0:
            raise ValueError('invalid allocation sample values')
        records += 1
        stack = tuple(frame for loc in map(int, ids.split()) for frame in frames[loc])
        metrics = (counts[objects], counts[space])
        if not any(metrics):
            continue
        tags = classify(stack)
        for bucket in ('all', *tags):
            for index, value in enumerate(metrics):
                totals[bucket][index] += value
        if not tags and not stack:
            for index, value in enumerate(metrics):
                totals['unresolvedSymbols'][index] += value
        if any('readMetaFrame' in f for f in stack):
            source = 'client' if any('clientConnReadLoop' in f for f in stack) else 'other'
            key = 'metadataFrame/' + source
            for index, value in enumerate(metrics):
                roots[key][index] += value
        if tags:
            for index, value in enumerate(metrics):
                stack_values[stack][index] += value
    if not records or 'all' not in totals:
        raise ValueError('allocation samples missing')
    def counts(values):
        return dict(zip(MEASURES, values))

    return {'recordCount': records, 'totals': {k: counts(v) for k, v in totals.items()},
            'metadataFrame': {k: counts(v) for k, v in roots.items()},
            'taggedStacks': [{'frames': list(k), 'tags': classify(k), **counts(v)}
                             for k, v in sorted(stack_values.items())]}


def capture(go, call, json_file, need, output, arms, binaries, trees):
    route = output / 'h2-allocation'
    route.mkdir()
    sources = {role: sha256((source / PACKAGE / 'handler_bench_test.go').read_bytes()).hexdigest()
               for role, source in arms.items()}
    need(len(set(sources.values())) == 1, 'HTTP/2 fixture differs across the three arms')
    order = tuple((role, 1) for role in 'ABC') + tuple((role, 512) for role in 'CBA')
    binary_digests = {role: sha256(binaries[role][PROFILE].read_bytes()).hexdigest() for role in 'ABC'}
    json_file(route / 'binding.json', {'trees': trees, 'fixtureSHA256': sources, 'references': ARCHIVE_REFERENCES,
                                      'binarySHA256': binary_digests,
                                      'benchmark': BENCHMARK, 'memProfileRate': 1, 'order': order})
    summaries = {}
    for role, iterations in order:
        binary = binaries[role][PROFILE]
        prefix = route / f'{role}-{iterations}x'
        profile = prefix.with_suffix('.pb.gz')
        smoke = call(binary, '-test.run=^$', f'-test.bench=^{BENCHMARK}$', '-test.benchmem',
                     f'-test.benchtime={iterations}x', '-test.count=1', '-test.cpu=2',
                     '-test.timeout=90s', '-test.memprofilerate=1', f'-test.memprofile={profile}',
                     cwd=arms[role] / PACKAGE)
        prefix.with_suffix('.test.txt').write_bytes(smoke)
        observed = re.findall(rb'^' + BENCHMARK.encode() + rb'-2\s+(\d+)\s+', smoke, re.M)
        need(observed == [str(iterations).encode()] and b'\nPASS\n' in smoke, 'wrong profiled workload')
        need(profile.read_bytes().startswith(b'\x1f\x8b'), 'missing binary memory profile')
        raw = call(go, 'tool', 'pprof', '-raw', binary, profile)
        prefix.with_suffix('.raw').write_bytes(raw)
        summary = describe(raw.decode())
        need(iterations == 1 or summary['totals'].get('xNetTransport'), 'the 512x profile lacks HTTP/2 transport frames')
        summary['profileSHA256'] = sha256(profile.read_bytes()).hexdigest()
        summary['rawSHA256'] = sha256(raw).hexdigest()
        summaries[prefix.name] = summary
    net = {}
    for role in 'ABC':
        need(sha256(binaries[role][PROFILE].read_bytes()).hexdigest() == binary_digests[role], 'profile binary changed')
        small, large = (summaries[f'{role}-{size}x'] for size in (1, 512))
        net[role] = {}
        for group in ('totals', 'metadataFrame'):
            net[role][group] = {
                tag: {unit: large[group].get(tag, {}).get(unit, 0) - small[group].get(tag, {}).get(unit, 0)
                      for unit in MEASURES}
                for tag in large[group].keys() | small[group].keys()
            }
    json_file(route / 'attribution.json', {'scope': 'whole process counters, collected after timing',
                                          'interpretation': '512x minus 1x spans 511 additional connections; '
                                                            'tags can overlap; raw profiles retain all stacks.',
                                          'profiles': summaries, 'netAcross511Connections': net})
    return len(order)
