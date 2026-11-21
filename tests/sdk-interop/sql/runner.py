"""Owned SQL fixture with optional local projection timing; no downloads."""
import argparse
import collections
import datetime
import decimal
import json
import os
import re
from pathlib import Path
import select
import signal
import subprocess
import tempfile
import time

EXPECTED_SQL = collections.Counter({
    'SELECT * FROM "products"': 1,
    'SELECT "name", "id" FROM "products"': 1,
    'SELECT "id", "name", "price", "description" FROM "products"': 1,
    'SELECT "name", "id" FROM "products" LIMIT $1': 1,
    'SELECT COUNT(*) FROM "products"': 2,
    'SELECT "name", "id" FROM "products" LIMIT $1 OFFSET $2': 1,
})

RELATIONSHIP_SQL = collections.Counter({
    'SELECT "title", "author_id", "id" FROM "articles" ORDER BY "id" ASC': 2,
    'SELECT "name", "id" FROM "persons" WHERE "id" IN ($1, $2, $3, $4, $5)': 2,
    'SELECT "name", "id" FROM "persons" ORDER BY "id" ASC': 4,
    'SELECT "title", "id", "author_id" FROM "articles" WHERE "author_id" IN ($1, $2, $3, $4)': 2,
    'SELECT "title", "author_id", "id" FROM "articles" ORDER BY "id" ASC LIMIT $1': 2,
    'SELECT "title", "author_id", "id" FROM "articles" ORDER BY "id" ASC LIMIT $1 OFFSET $2': 8,
    'SELECT "title", "id" FROM "articles" ORDER BY "id" ASC LIMIT $1': 2,
    'SELECT COUNT(*) FROM "articles"': 12,
    'SELECT "name", "id" FROM "persons" WHERE "id" IN ($1, $2)': 4,
    'SELECT "name", "id" FROM "persons" WHERE "id" IN ($1)': 4,
})


def canonical_relationship_sql(query):
    match = re.fullmatch(r'SELECT ((?:"[a-z_]+", )*"[a-z_]+") FROM (.+)', query)
    if match is None:
        return query
    # Projection order is irrelevant; duplicates, predicates, windows and work
    # counts remain significant. Wildcards and extra columns are never erased.
    return 'SELECT ' + ', '.join(sorted(match[1].split(', '))) + ' FROM ' + match[2]


def validate_relationship_report(report, runs, gateway):
    expected = {'h2Requests': 18 * runs, 'otherRequests': 0,
                'rpcRequests': 18 * runs if gateway else 0,
                'jsonRequests': 9 * runs, 'binaryRequests': 9 * runs,
                'unknownMode': 0, 'apiErrors': 0,
                'apiRequests': {'articleRows': 2 * runs, 'peopleRows': 4 * runs,
                                'articlePage': 10 * runs, 'boxedArticlePage': 2 * runs}}
    for key, value in expected.items():
        if report.get(key) != value:
            raise ValueError('unexpected relationship ' + key + ': ' + str(report))
    queries = collections.Counter()
    for query, count in RELATIONSHIP_SQL.items():
        queries[canonical_relationship_sql(query)] += count * runs
    actual = collections.Counter(map(canonical_relationship_sql, report['sql']))
    if actual != queries:
        raise ValueError('unexpected relationship SQL: ' + str(actual - queries) + '; missing: ' + str(queries - actual))

PRECISION_INSERT = ('INSERT INTO "precision_records" ("id", "whole", "ratio", "amount", '
                    '"moment", "elapsed", "token") VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *')
PRECISION_READ = 'SELECT * FROM "precision_records" WHERE "id" = $1 LIMIT $2'
NULLABLE_INSERT = ('INSERT INTO "nullable_records" ("id", "number", "items") '
                   'VALUES ($1, $2, $3) RETURNING *')
NULLABLE_READ = 'SELECT * FROM "nullable_records" WHERE "id" = $1 LIMIT $2'


def validate_nullable_report(report, runs, gateway):
    expected = {'h2Requests': 16 * runs, 'otherRequests': 0,
                'rpcRequests': 16 * runs if gateway else 0,
                'jsonRequests': 8 * runs, 'binaryRequests': 8 * runs,
                'unknownMode': 0, 'apiErrors': 0,
                'apiRequests': {'storeNullableRecord': 6 * runs,
                                'readNullableRecord': 8 * runs,
                                'nullableRecords': 2 * runs}}
    for key, value in expected.items():
        if report.get(key) != value:
            raise ValueError('unexpected nullable ' + key + ': ' + str(report))
    queries = {NULLABLE_INSERT: 6 * runs, NULLABLE_READ: 8 * runs,
               'SELECT * FROM "nullable_records"': 2 * runs}
    if collections.Counter(report['sql']) != queries:
        raise ValueError('unexpected nullable SQL: ' + str(report))


def verify_nullable_storage(container, scratch, runs):
    query = ('SELECT json_build_array(id,number::text,items::text,number IS NULL,'
             'items IS NULL,coalesce(cardinality(items),-1)) '
             'FROM nullable_records ORDER BY id')
    raw = capture(['docker', 'exec', container, 'psql', '-X', '-U', 'postgres',
                   '-d', 'selection_fixture', '-At', '-c', query])
    (scratch / 'nullable-storage.jsonl').write_text(raw + '\n')
    states = [[None, None, True, True, -1], ['0', '{}', False, False, 0],
              ['9223372036854775807', '{-9223372036854775808,9007199254740993,9223372036854775807}', False, False, 3]]
    bases = (0, 10) if runs == 1 else (0, 10, 100, 110)
    expected = {base + index + 1: state for base in bases for index, state in enumerate(states)}
    rows = [json.loads(line) for line in raw.splitlines()]
    if len(rows) != len(expected) or len({row[0] for row in rows}) != len(expected):
        raise ValueError('nullable storage row count differs')
    for row in rows:
        if expected.get(row[0]) != row[1:]:
            raise ValueError('nullable storage state differs: ' + str(row))


def validate_precision_report(report, runs, gateway):
    expected = {'h2Requests': 30 * runs, 'otherRequests': 0,
                'rpcRequests': 30 * runs if gateway else 0,
                'jsonRequests': 15 * runs, 'binaryRequests': 15 * runs,
                'unknownMode': 0, 'apiErrors': 2 * runs,
                'apiRequests': {'storePrecision': 14 * runs,
                                'readPrecision': 14 * runs,
                                'precisionRecords': 2 * runs}}
    for key, value in expected.items():
        if report.get(key) != value:
            raise ValueError('unexpected precision ' + key + ': ' + str(report))
    queries = collections.Counter({PRECISION_INSERT: 12 * runs,
                                   PRECISION_READ: 14 * runs,
                                   'SELECT * FROM "precision_records"': 2 * runs})
    if collections.Counter(report['sql']) != queries:
        raise ValueError('unexpected precision SQL: ' + str(report))


def precision_epoch(value):
    year = int(value[:4])
    instant = datetime.datetime.fromisoformat(('0001' + value[4:] if year == 0 else value).replace('Z', '+00:00'))
    delta = instant - datetime.datetime(1970, 1, 1, tzinfo=datetime.timezone.utc)
    seconds = (delta.days - (366 if year == 0 else 0)) * 86400 + delta.seconds
    return decimal.Decimal(seconds) + decimal.Decimal(instant.microsecond) / 1000000


def validate_precision_storage(raw, corpus, runs):
    rows = [line.split('\t') for line in raw.splitlines()]
    bases = (0, 10) if runs == 1 else (0, 10, 100, 110)
    expected = {base + index + 1: item for base in bases for index, item in enumerate(corpus)}
    if len(rows) != len(expected):
        raise ValueError('persistent row count differs')
    seen = set()
    for row in rows:
        if len(row) != 7:
            raise ValueError('unexpected persistent row shape')
        identifier = int(row[0])
        if identifier not in expected or identifier in seen:
            raise ValueError('unexpected persistent row')
        seen.add(identifier)
        item = expected[identifier]
        if (row[1] != item['whole'] or row[2] != item['ratioBits'] or
                decimal.Decimal(row[3]) != decimal.Decimal(item['amount']) or
                decimal.Decimal(row[4]) != precision_epoch(item['storedMoment']) or
                decimal.Decimal(row[5]) != decimal.Decimal(item['elapsed']) or
                row[6] != item['token'].lower()):
            raise ValueError('persistent value differs: ' + str(identifier))


def verify_precision_storage(container, scratch, corpus_path, runs):
    query = ('SELECT id,whole,encode(float8send(ratio),\'hex\'),amount,'
             'extract(epoch from moment),extract(epoch from elapsed)*1000000000,token '
             'FROM precision_records ORDER BY id')
    raw = capture(['docker', 'exec', container, 'psql', '-X', '-U', 'postgres',
                   '-d', 'selection_fixture', '-At', '-F', '\t', '-c', query])
    (scratch / 'precision-storage.tsv').write_text(raw + '\n')
    validate_precision_storage(raw, json.loads(corpus_path.read_text()), runs)


def validate_report(report, runs):
    expected = collections.Counter({k: v * runs for k, v in EXPECTED_SQL.items()})
    if report['h2Requests'] != 5 * runs or report['otherRequests'] != 0:
        raise ValueError('unexpected transport counts: ' + str(report))
    if collections.Counter(report['sql']) != expected:
        raise ValueError('unexpected SQL projection: ' + str(report))


def validate_projection_report(report, runs):
    # 20 warmups and three batches of 50 requests per path, per module format.
    count = 170 * runs
    expected = collections.Counter({'SELECT * FROM "products"': count,
                                    'SELECT "name", "id" FROM "products"': count})
    if report['h2Requests'] != count * 2 or report['otherRequests'] != 0:
        raise ValueError('unexpected projection transport counts')
    if collections.Counter(report['sql']) != expected:
        raise ValueError('unexpected projection query counts')


def validate_inferred_report(report, runs, gateway):
    apis = {'selectedProducts': 10 * runs}
    for name in ('pricedProducts', 'privateProducts', 'calledProducts',
                 'calledPrivateProducts', 'nestedProducts', 'ownProducts',
                 'ownPrivateProducts', 'combinedProducts', 'forwardedProducts',
                 'scalarCalledProducts', 'aliasedOwnProducts'):
        apis[name] = 2 * runs
    expected = collections.Counter({
        'SELECT "name", "price", "id" FROM "products"': 16 * runs,
        'SELECT "name", "id" FROM "products"': 4 * runs,
        'SELECT "name", "secret", "id" FROM "products"': 6 * runs,
        'SELECT "name", "secret", "price", "id" FROM "products"': 4 * runs,
        'SELECT "name", "price", "secret", "id" FROM "products"': 2 * runs,
    })
    if (report['h2Requests'] != 32 * runs or report['otherRequests'] != 0 or
            report.get('rpcRequests', 0) != (32 * runs if gateway else 0) or
            report.get('jsonRequests') != 16 * runs or
            report.get('binaryRequests') != 16 * runs or report.get('unknownMode') != 0):
        raise ValueError('unexpected inferred transport counts: ' + str(report))
    if collections.Counter(report['sql']) != expected:
        raise ValueError('unexpected inferred SQL projection: ' + str(report))
    if report.get('apiRequests') != apis or report.get('apiErrors') != 0:
        raise ValueError('unexpected inferred API calls: ' + str(report))


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
    mode = '-sql-relations' if args.relations else '-sql-precision' if args.precision or args.nullable else '-sql'
    command(['go', 'run', './tests/sdk-interop', mode, '-out', str(generated)], log, cwd=core)
    binary = scratch / 'server'
    command(['go', 'build', '-o', str(binary), str(generated)], log, cwd=core)
    image = capture(['docker', 'image', 'inspect', args.image, '--format', '{{.Id}}'])
    container = start_database(image, scratch)
    server = None
    try:
        address = wait_database(container, log)
        env = dict(os.environ, LUXO_TEST_DATABASE_URL=
            'postgres://postgres:fixture@' + address + '/selection_fixture?sslmode=disable',
            LUXO_SQL_GATEWAY='1' if args.gateway else '0')
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
            LUXO_SQL_PROJECTION='1' if args.projection_benchmark else '0',
            LUXO_SQL_INFERRED='1' if args.inferred or args.relations else '0',
            LUXO_SQL_RELATIONS='1' if args.relations else '0',
            LUXO_SQL_PRECISION='1' if args.precision or args.nullable else '0',
            LUXO_SQL_NULLABLE='1' if args.nullable else '0',
            LUXO_SQL_PRECISION_CORPUS=str(core / 'tests/sdk-interop/precision.json'))
        run_consumer(args.consumer, env, log)
        if args.precision:
            verify_precision_storage(container, scratch,
                core / 'tests/sdk-interop/precision.json', args.runs)
        if args.nullable:
            verify_nullable_storage(container, scratch, args.runs)
    finally:
        try:
            if server:
                stop_server(server, scratch)
        finally:
            command(['docker', 'rm', '-f', container], log)
            (scratch / 'cleanup.txt').write_text('Owned database container removed.\n')
    report = json.loads((scratch / 'server.json').read_text())
    if args.relations:
        validate_relationship_report(report, args.runs, args.gateway)
    elif args.nullable:
        validate_nullable_report(report, args.runs, args.gateway)
    elif args.precision:
        validate_precision_report(report, args.runs, args.gateway)
    elif args.inferred:
        validate_inferred_report(report, args.runs, args.gateway)
    else:
        validator = validate_projection_report if args.projection_benchmark else validate_report
        validator(report, args.runs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True, help='already installed PostgreSQL image, never pulled')
    parser.add_argument('--projection-benchmark', action='store_true',
                        help='same-output SQL projection local comparison')
    parser.add_argument('--inferred', action='store_true', help='compiled automatic selection correctness')
    parser.add_argument('--precision', action='store_true', help='persistent scalar precision correctness')
    parser.add_argument('--nullable', action='store_true', help='persistent nullable integer/list states')
    parser.add_argument('--relations', action='store_true', help='automatic nested relationships and final pagination to SQL')
    parser.add_argument('--gateway', action='store_true', help='use generated gateway over actual loopback LRPC')
    parser.add_argument('--runs', type=int, choices=(1, 2), default=1)
    parser.add_argument('consumer', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if sum([args.inferred, args.projection_benchmark, args.precision, args.nullable, args.relations]) > 1:
        parser.error('inferred, precision, nullable, relations and projection timing are separate modes')
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
