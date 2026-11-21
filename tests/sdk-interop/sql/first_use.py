"""Measure fresh Go/Dart processes against one owned, already installed PG image."""
import argparse
import collections
from decimal import Decimal
import hashlib
import json
import os
from pathlib import Path
import platform
import queue
import re
import signal
import subprocess
import sys
import threading
import time

import runner

CORE = Path(__file__).resolve().parents[3]
CASES = {
    'ownProducts': 'SELECT "name", "price", "id" FROM "products"',
    'ownPrivateProducts': 'SELECT "name", "secret", "id" FROM "products"',
    'combinedProducts': 'SELECT "name", "secret", "price", "id" FROM "products"',
    'forwardedProducts': 'SELECT "name", "secret", "price", "id" FROM "products"',
    'scalarCalledProducts': 'SELECT "name", "price", "id" FROM "products"',
    'aliasedOwnProducts': 'SELECT "name", "price", "secret", "id" FROM "products"',
}
REQUESTS = 41

POSTGRES_TIMING = re.compile(
    r'^LUXO-PG\|(?P<pid>\d+)\|[^|]*\|LOG:\s+duration: '
    r'(?P<duration>\d+(?:\.\d+)?) ms\s+'
    r'(?P<operation>statement|(?:execute|parse|bind) [^:]+): (?P<sql>.*)$')


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')


def parse_postgres_timings(text, query, count=REQUESTS):
    samples, pending = [], {}
    for line in text.splitlines():
        match = POSTGRES_TIMING.fullmatch(line)
        if match is None:
            if line.startswith('LUXO-PG|') and query in line:
                raise ValueError('unrecognized PostgreSQL timing line: ' + line)
            continue
        if match['sql'] != query:
            continue
        pid, operation = int(match['pid']), match['operation']
        duration = int(Decimal(match['duration']) * 1_000_000)
        kind, _, statement = operation.partition(' ')
        stages = pending.setdefault(pid, {})
        if kind in ('parse', 'bind'):
            if kind in stages or stages.get('statement', statement) != statement or 'bind' in stages:
                raise ValueError('duplicate or mismatched PostgreSQL prepared stages')
            stages.update({kind: duration, 'statement': statement})
            continue
        if kind == 'execute':
            if 'bind' not in stages or stages['statement'] != statement:
                raise ValueError('PostgreSQL execute lacks its matching bind')
        elif stages:
            raise ValueError('simple statement mixed with prepared stages')
        parse, bind = stages.get('parse', 0), stages.get('bind', 0)
        samples.append({'pid': pid, 'durationNs': duration, 'operation': operation,
                        'parseNs': parse, 'bindNs': bind, 'serverNs': parse + bind + duration})
        del pending[pid]
    if pending or len(samples) != count or len({sample['pid'] for sample in samples}) != 1:
        raise ValueError('PostgreSQL timings require the exact query count and one backend')
    return samples


def file_hash(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


class OwnedProcess:
    """One session leader; wait4 accounts for this exact child, never aggregate children."""
    def __init__(self, command, directory, env=None, interactive=False):
        if (CORE / '.tmp').is_symlink():
            raise ValueError('refusing symlinked project scratch')
        directory.resolve().relative_to((CORE / '.tmp').resolve())
        directory.mkdir(parents=True, exist_ok=True)
        self.directory, self.interactive = directory, interactive
        self.lines, self.usage = queue.Queue(), None
        self.stderr = (directory / 'stderr.log').open('wb')
        try:
            self.process = subprocess.Popen(command, env=env, cwd=CORE,
                stdin=subprocess.PIPE if interactive else subprocess.DEVNULL,
                stdout=subprocess.PIPE, stderr=self.stderr, start_new_session=True)
        except BaseException:
            self.stderr.close()
            raise
        self.reader = threading.Thread(target=self._read)
        self.reader.start()

    def _read(self):
        try:
            with (self.directory / 'stdout.log').open('wb') as output:
                for line in iter(self.process.stdout.readline, b''):
                    output.write(line)
                    output.flush()
                    self.lines.put(line)
        finally:
            self.lines.put(None)

    def ready(self, timeout):
        try:
            line = self.lines.get(timeout=timeout)
        except queue.Empty as error:
            raise TimeoutError('child readiness timed out') from error
        if line is None:
            raise RuntimeError('child exited before readiness; see stderr.log')
        return json.loads(line)

    def _signal(self, number):
        try:
            os.killpg(self.process.pid, number)
        except ProcessLookupError:
            pass

    def _collect(self, blocking=False):
        pid, status, usage = os.wait4(self.process.pid, 0 if blocking else os.WNOHANG)
        if not pid:
            return False
        self.process.returncode = os.waitstatus_to_exitcode(status)
        self.usage = {'userNs': round(usage.ru_utime * 1e9),
                      'systemNs': round(usage.ru_stime * 1e9),
                      'maxRSSBytes': usage.ru_maxrss * (1 if sys.platform == 'darwin' else 1024)}
        write_json(self.directory / 'resource.json', dict(self.usage,
            pid=pid, exitCode=self.process.returncode, command=self.process.args))
        self._signal(signal.SIGTERM)
        self.reader.join(2)
        if self.reader.is_alive():
            self._signal(signal.SIGKILL)
            self.reader.join(2)
        if self.reader.is_alive():
            raise RuntimeError('owned stdout reader did not finish')
        self.process.stdout.close()
        self.stderr.close()
        return True

    def wait(self, timeout):
        deadline = time.monotonic() + timeout
        while self.process.returncode is None:
            if self._collect():
                break
            if time.monotonic() >= deadline:
                self._signal(signal.SIGKILL)
                self._collect(True)
                raise TimeoutError('owned child timed out and was reaped')
            time.sleep(.002)
        return self.process.returncode

    def close(self):
        if self.interactive and not self.process.stdin.closed:
            self.process.stdin.close()
        elif self.process.returncode is None:
            self._signal(signal.SIGTERM)
        self.wait(15)

    def last_json(self):
        return json.loads((self.directory / 'stdout.log').read_text().splitlines()[-1])


def validate_probe(report, name, mode, gateway):
    counts = {'h2Requests': REQUESTS, 'otherRequests': 0,
              'rpcRequests': REQUESTS if gateway else 0,
              'jsonRequests': REQUESTS if mode == 'json' else 0,
              'binaryRequests': REQUESTS if mode == 'binary' else 0,
              'unknownMode': 0, 'apiRequests': {name: REQUESTS}, 'apiErrors': 0}
    if any(report.get(key) != value for key, value in counts.items()):
        raise ValueError('probe transport/API counts differ: ' + str(report))
    if collections.Counter(report['sql']) != {CASES[name]: REQUESTS}:
        raise ValueError('probe SQL columns differ: ' + str(report))


def validate_client(report, name, mode):
    steady = report.get('steadyNs', [])
    values = [report.get('firstNs'), report.get('initializationNs'), *steady]
    if (report.get('api') != name or report.get('mode') != mode or
            report.get('verifiedCalls') != REQUESTS or len(steady) != 30 or
            any(type(value) is not int or value < 0 for value in values)):
        raise ValueError('Dart probe did not verify calls and valid timing samples')


def validate_timings(report):
    timing = report.get('timings')
    if not isinstance(timing, dict) or timing.get('queryErrors') != 0 or report.get('timingOverlaps', 0) != 0:
        raise ValueError('timing requires successful queries and no overlapping HTTP handlers')
    for key in ['queryNs', 'handlerNs', 'httpNs']:
        values = timing.get(key, [])
        if (not isinstance(values, list) or len(values) != REQUESTS or
                any(type(value) is not int or value < 0 for value in values)):
            raise ValueError('incomplete or invalid server timing samples: ' + key)
    if any(not query <= handler <= http for query, handler, http in
           zip(timing['queryNs'], timing['handlerNs'], timing['httpNs'])):
        raise ValueError('query/handler/HTTP timing boundaries are not nested')


def build_servers(output, overlay, log):
    binaries = {}
    for variant in ['base', 'current']:
        generated = output / variant / 'generated'
        generated.mkdir(parents=True)
        command = ['go', 'run']
        if variant == 'base':
            command.append('-overlay=' + str(overlay))
        runner.command(command + ['./tests/sdk-interop', '-sql', '-out', str(generated)], log, cwd=CORE)
        binary = generated.parent / 'server'
        runner.command(['go', 'build', '-o', str(binary), str(generated)], log, cwd=CORE)
        binaries[variant] = binary
    for name in ['schema.gen.go', 'model.gen.go', 'db.gen.go', 'gateway_proxy.gen.go', 'main.go']:
        if (output / 'base/generated' / name).read_bytes() != (output / 'current/generated' / name).read_bytes():
            raise ValueError('baseline changed shared fixture input: ' + name)
    return binaries


def reset_database(container, log):
    runner.command(['docker', 'exec', container, 'psql', '-U', 'postgres',
                    '-d', 'selection_fixture', '-c', 'DROP TABLE IF EXISTS products;'], log)


def start_server(binary, address, directory, gateway, timing=False):
    env = dict(os.environ, LUXO_TEST_DATABASE_URL=
        'postgres://postgres:fixture@' + address + '/selection_fixture?sslmode=disable',
        LUXO_SQL_GATEWAY='1' if gateway else '0', LUXO_SQL_TIMING='1' if timing else '0')
    started = time.perf_counter_ns()
    child = OwnedProcess([str(binary)], directory, env, interactive=True)
    try:
        ready = child.ready(15)
        elapsed = time.perf_counter_ns() - started
        certificate = directory / 'certificate.pem'
        certificate.write_text(ready['certificate'])
        return child, ready, certificate, elapsed
    except BaseException:
        child.close()
        raise


def prepare_client(binary, address, output, log):
    directory = output / 'prepare'
    server, ready, certificate, _ = start_server(binary, address, directory / 'server', False)
    try:
        env = dict(os.environ, LUXO_SQL_ENDPOINT=ready['url'],
            LUXO_SQL_SCHEMA_ENDPOINT=ready['plain'], LUXO_SQL_CERTIFICATE=str(certificate),
            LUXO_SQL_SCRATCH=str(directory), LUXO_INTROSPECTION_KEY='benchmark',
            LUXO_SQL_INFERRED='1', LUXO_SQL_PREPARE_ONLY='1')
        runner.command([sys.executable, '-B', str(Path(__file__).with_name('dart.py'))], log, env=env)
    finally:
        server.close()
    if server.process.returncode != 0:
        raise RuntimeError('client preparation server failed')
    return directory / 'dart-consumer'


def condition_binaries(binaries, client, container, address, output, log):
    for variant, binary in binaries.items():
        reset_database(container, log)
        result = trial(binary, client, address, output / ('conditioning-' + variant),
                       'ownProducts', 'json', False)
        write_json(output / ('conditioning-' + variant + '.json'), result)


def trial(binary, client_binary, address, directory, api, mode, gateway, timing=False):
    server, ready, certificate, startup = start_server(binary, address, directory / 'server', gateway, timing)
    client = None
    try:
        started = time.perf_counter_ns()
        client = OwnedProcess([str(client_binary), ready['url'], str(certificate),
                               'probe', mode, api], directory / 'client')
        if client.wait(30) != 0:
            raise RuntimeError('Dart probe failed; see client/stderr.log')
        client_wall = time.perf_counter_ns() - started
        result = client.last_json()
        validate_client(result, api, mode)
    finally:
        if client:
            client.close()
        server.close()
    if server.process.returncode != 0:
        raise RuntimeError('Go probe failed; see server/stderr.log')
    report = server.last_json()
    validate_probe(report, api, mode, gateway)
    if timing:
        validate_timings(report)
    return dict(result, startupNs=startup, clientProcessNs=client_wall,
                serverUsage=server.usage, clientUsage=client.usage,
                serverReport=report)


def measure(args, output, binaries, client, container, address, log):
    rows = []
    for pair in range(1, args.pairs + 1):
        for topology in args.topologies:
            for mode in args.modes:
                for api in args.apis:
                    for variant in (['base', 'current'] if pair % 2 else ['current', 'base']):
                        directory = output / f'{pair}-{topology}-{mode}-{api}-{variant}'
                        reset_database(container, log)
                        result = trial(binaries[variant], client, address, directory,
                                       api, mode, topology == 'gateway', getattr(args, 'timing', False))
                        row = dict(result, pair=pair, topology=topology, variant=variant,
                                   directory=str(directory.relative_to(CORE)))
                        rows.append(row)
                        write_json(output / 'progress.json', rows)
                        print(f'Completed {len(rows)}: {directory.name}', flush=True)
    write_json(output / 'terminal.json', {'rows': rows, 'status': 'terminal'})


def input_manifest(binaries, client, overlay, image):
    baseline = json.loads(overlay.read_text())['Replace']
    names = subprocess.check_output(['rg', '--files', '--no-ignore', 'pkg/codegen', 'pkg/lux',
        '-g', '*.go', '-g', '!**/*_test.go', '-g', '!**/.tmp/**', '-g', '!**/vendor/**'],
        cwd=CORE, text=True).splitlines()
    return {
        'platform': platform.platform(),
        'go': runner.capture(['go', 'version']),
        'cpuCount': os.cpu_count(),
        'runtimeEnvironment': {name: os.environ.get(name) for name in ['GOMAXPROCS', 'GOGC', 'GOMEMLIMIT']},
        'binaries': {name: file_hash(path) for name, path in binaries.items()},
        'client': file_hash(client), 'image': image,
        'sources': {name: file_hash(CORE / name) for name in names},
        'baseline': {name: {'snapshot': path, 'sha256': file_hash(Path(path))} for name, path in baseline.items()},
        'scope': 'Fresh Go and Dart processes; shared warm PG/OS caches. Startup includes DDL, router, TLS/listener and optional same-process gateway/TCP LRPC. Exact wait4 child CPU and process-lifetime peak RSS; not per-request heap or whole-system cold start.'}


def run(args):
    if (CORE / '.tmp').is_symlink():
        raise ValueError('refusing symlinked project scratch')
    output = args.output.resolve()
    output.relative_to((CORE / '.tmp').resolve())
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError('output must be empty')
    if not os.environ.get('DART') or not Path(os.environ['DART']).is_file():
        raise ValueError('DART must name an existing SDK executable; no installation is performed')
    with (output / 'run.log').open('w') as log:
        binaries = build_servers(output, args.baseline_overlay.resolve(), log)
        image = runner.capture(['docker', 'image', 'inspect', args.image, '--format', '{{.Id}}'])
        container = runner.start_database(image, output)
        try:
            address = runner.wait_database(container, log)
            client = prepare_client(binaries['current'], address, output, log)
            condition_binaries(binaries, client, container, address, output, log)
            manifest = input_manifest(binaries, client, args.baseline_overlay, image)
            manifest['diagnosticTiming'] = getattr(args, 'timing', False)
            write_json(output / 'inputs.json', manifest)
            measure(args, output, binaries, client, container, address, log)
        except BaseException as error:
            write_json(output / 'failure.json', {'type': type(error).__name__, 'message': str(error)})
            raise
        finally:
            runner.command(['docker', 'rm', '-f', container], log)
            (output / 'cleanup.txt').write_text('Owned database container removed.\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True)
    parser.add_argument('--baseline-overlay', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--pairs', type=int, choices=[1, 3], default=3)
    parser.add_argument('--timing', action='store_true', help='Optional test-server timing; separate diagnostic evidence, not an uninstrumented cost result')
    parser.add_argument('--apis', nargs='+', choices=list(CASES), default=list(CASES))
    parser.add_argument('--modes', nargs='+', choices=['json', 'binary'], default=['json', 'binary'])
    parser.add_argument('--topologies', nargs='+', choices=['direct', 'gateway'], default=['direct', 'gateway'])
    run(parser.parse_args())


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, runner.interrupted)
    main()
