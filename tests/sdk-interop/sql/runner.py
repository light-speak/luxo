"""Owned SQL fixture with optional local projection timing; no downloads."""
import argparse
import collections
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import tempfile
import time

EXPECTED_SQL = collections.Counter({
    'SELECT * FROM products': 1,
    'SELECT name, id FROM products': 1,
    'SELECT id, name, price, description FROM products': 1,
    'SELECT name, id FROM products LIMIT $1': 1,
    'SELECT COUNT(*) FROM products': 2,
    'SELECT name, id FROM products LIMIT $1 OFFSET $2': 1,
})


def validate_report(report, runs):
    expected = collections.Counter({k: v * runs for k, v in EXPECTED_SQL.items()})
    if report['h2Requests'] != 5 * runs or report['otherRequests'] != 0:
        raise ValueError('unexpected transport counts: ' + str(report))
    if collections.Counter(report['sql']) != expected:
        raise ValueError('unexpected SQL projection: ' + str(report))


def validate_projection_report(report, runs):
    # 20 warmups and three batches of 50 requests per path, per module format.
    count = 170 * runs
    expected = collections.Counter({'SELECT * FROM products': count,
                                    'SELECT name, id FROM products': count})
    if report['h2Requests'] != count * 2 or report['otherRequests'] != 0:
        raise ValueError('unexpected projection transport counts')
    if collections.Counter(report['sql']) != expected:
        raise ValueError('unexpected projection query counts')


def command(args, log, **kwargs):
    return subprocess.run(args, stdout=log, stderr=log, check=True,
                          timeout=180, **kwargs)


def capture(args):
    return subprocess.check_output(args, text=True, timeout=30).strip()


def start_database(image, scratch):
    identifier = capture(['docker', 'run', '-d', '--pull=never',
        '--label', 'luxo.test=selection-evidence', '--memory=512m', '--cpus=1',
        '--tmpfs', '/var/lib/postgresql/data:rw', '-e', 'POSTGRES_PASSWORD=fixture',
        '-e', 'POSTGRES_DB=selection_fixture', '-p', '127.0.0.1::5432', image])
    try:
        (scratch / 'container.json').write_text(json.dumps({'id': identifier, 'image': image}))
    except BaseException:
        subprocess.run(['docker', 'rm', '-f', identifier], check=True, timeout=30)
        raise
    return identifier


def wait_database(identifier, log):
    for _ in range(60):
        ready = subprocess.run(['docker', 'exec', identifier, 'pg_isready',
            '-h', '127.0.0.1', '-U', 'postgres', '-d', 'selection_fixture'],
            stdout=log, stderr=log, timeout=5)
        if ready.returncode == 0:
            return capture(['docker', 'port', identifier, '5432/tcp'])
        time.sleep(.25)
    raise TimeoutError('database readiness timed out')


def run_consumer(args, env, log):
    child = subprocess.Popen(args, env=env, stdout=log, stderr=log, start_new_session=True)
    try:
        code = child.wait(timeout=300)
        if code:
            raise subprocess.CalledProcessError(code, args)
    finally:
        stop_consumer_group(child)


def stop_consumer_group(child):
    # A reaped leader does not imply its descendants have exited.
    deadline = time.monotonic() + 5
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except ProcessLookupError:
        child.wait()
        return
    while time.monotonic() < deadline:
        child.poll()
        try:
            os.killpg(child.pid, 0)
        except ProcessLookupError:
            child.wait()
            return
        time.sleep(.01)
    try:
        os.killpg(child.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    child.wait()


def stop_server(server, scratch):
    try:
        tail, _ = server.communicate('', timeout=15)
        (scratch / 'server.json').write_text(tail)
        if server.returncode:
            raise RuntimeError('fixture server failed')
    except subprocess.TimeoutExpired:
        server.kill()
        server.wait()
        raise


def execute(args, core, scratch, log):
    generated = scratch / 'generated'
    generated.mkdir()
    command(['go', 'run', './tests/sdk-interop', '-sql', '-out', str(generated)], log, cwd=core)
    binary = scratch / 'server'
    command(['go', 'build', '-o', str(binary), str(generated)], log, cwd=core)
    image = capture(['docker', 'image', 'inspect', args.image, '--format', '{{.Id}}'])
    container = start_database(image, scratch)
    server = None
    try:
        address = wait_database(container, log)
        env = dict(os.environ, LUXO_TEST_DATABASE_URL=
            'postgres://postgres:fixture@' + address + '/selection_fixture?sslmode=disable')
        server = subprocess.Popen([str(binary)], stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=log, text=True, env=env)
        if not select.select([server.stdout], [], [], 15)[0]:
            raise TimeoutError('fixture server readiness timed out')
        ready = json.loads(server.stdout.readline())
        certificate = scratch / 'certificate.pem'
        certificate.write_text(ready['certificate'])
        env = dict(os.environ, LUXO_SQL_ENDPOINT=ready['url'],
            LUXO_SQL_SCHEMA_ENDPOINT=ready['plain'], LUXO_SQL_CERTIFICATE=str(certificate),
            LUXO_SQL_SCRATCH=str(scratch), LUXO_INTROSPECTION_KEY='benchmark',
            LUXO_SQL_PROJECTION='1' if args.projection_benchmark else '0')
        run_consumer(args.consumer, env, log)
    finally:
        try:
            if server:
                stop_server(server, scratch)
        finally:
            command(['docker', 'rm', '-f', container], log)
            (scratch / 'cleanup.txt').write_text('Owned database container removed.\n')
    validator = validate_projection_report if args.projection_benchmark else validate_report
    validator(json.loads((scratch / 'server.json').read_text()), args.runs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True, help='already installed PostgreSQL image, never pulled')
    parser.add_argument('--projection-benchmark', action='store_true',
                        help='same-output SQL projection local comparison')
    parser.add_argument('--runs', type=int, choices=(1, 2), default=1)
    parser.add_argument('consumer', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.consumer[:1] == ['--']:
        args.consumer.pop(0)
    if not args.consumer:
        parser.error('a consumer command is required after --')
    core = Path(__file__).resolve().parents[3]
    root = core / '.tmp'
    if root.is_symlink():
        parser.error('refusing symlinked .tmp')
    root.mkdir(exist_ok=True)
    scratch = Path(tempfile.mkdtemp(prefix='sdk-sql-', dir=root))
    print('Evidence: ' + str(scratch), flush=True)
    with (scratch / 'run.log').open('w') as log:
        execute(args, core, scratch, log)
    print('SQL projection and H2 checks passed; see consumer evidence and measurement scope.')


def interrupted(*_):
    raise KeyboardInterrupt


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, interrupted)
    main()
