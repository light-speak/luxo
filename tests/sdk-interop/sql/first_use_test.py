import json
from pathlib import Path
import sys
import tempfile
import unittest

import first_use


class OwnedProcessTests(unittest.TestCase):
    def setUp(self):
        root = Path(__file__).resolve().parents[3] / '.tmp'
        root.mkdir(exist_ok=True)
        self.scratch = tempfile.TemporaryDirectory(prefix='first-use-test-', dir=root)
        self.addCleanup(self.scratch.cleanup)
        self.directory = Path(self.scratch.name)

    def test_exact_child_usage_and_output(self):
        code = 'import json; data=bytearray(24*1024*1024); print(json.dumps({"bytes":len(data)}))'
        child = first_use.OwnedProcess([sys.executable, '-c', code], self.directory)
        self.addCleanup(child.close)
        self.assertEqual(child.wait(10), 0)
        self.assertEqual(child.last_json(), {'bytes': 24 * 1024 * 1024})
        self.assertGreaterEqual(child.usage['maxRSSBytes'], 24 * 1024 * 1024)
        self.assertLess(child.usage['maxRSSBytes'], 512 * 1024 * 1024)
        self.assertGreater(child.usage['userNs'] + child.usage['systemNs'], 0)
        self.assertEqual(json.loads((self.directory / 'resource.json').read_text())['pid'], child.process.pid)

    def test_interactive_ready_and_graceful_exit(self):
        code = 'import json,sys; print(json.dumps({"ready":True}),flush=True); sys.stdin.read(); print(json.dumps({"done":True}))'
        child = first_use.OwnedProcess([sys.executable, '-c', code], self.directory, interactive=True)
        self.addCleanup(child.close)
        self.assertEqual(child.ready(10), {'ready': True})
        child.close()
        self.assertEqual(child.last_json(), {'done': True})
        self.assertEqual(child.process.returncode, 0)

    def test_timeout_reaps_owned_process(self):
        child = first_use.OwnedProcess([sys.executable, '-c', 'import time; time.sleep(30)'], self.directory)
        self.addCleanup(child.close)
        with self.assertRaises(TimeoutError):
            child.wait(.05)
        self.assertIsNotNone(child.process.returncode)
        self.assertIsNotNone(child.usage)

    def test_usage_does_not_inherit_previous_child_peak(self):
        usage = []
        for label, size in [('large', 64), ('small', 1)]:
            child = first_use.OwnedProcess([sys.executable, '-c',
                f'data=bytearray({size}*1024*1024); print("{{}}")'], self.directory / label)
            self.addCleanup(child.close)
            self.assertEqual(child.wait(10), 0)
            usage.append(child.usage['maxRSSBytes'])
        self.assertGreater(usage[0], usage[1] + 32 * 1024 * 1024)

    def test_failed_readiness_is_reaped_with_status(self):
        child = first_use.OwnedProcess([sys.executable, '-c', 'raise SystemExit(7)'],
                                      self.directory, interactive=True)
        self.addCleanup(child.close)
        with self.assertRaises(RuntimeError):
            child.ready(10)
        child.close()
        self.assertEqual(child.process.returncode, 7)

    def test_outside_project_scratch_is_rejected(self):
        with self.assertRaises(ValueError):
            first_use.OwnedProcess([sys.executable, '-c', 'pass'], first_use.CORE)


class PostgresTimingTests(unittest.TestCase):
    def test_simple_and_prepared_statement_logs(self):
        query = first_use.CASES['scalarCalledProducts']
        prefix = 'LUXO-PG|123|luxo|LOG:  duration: '
        for operation in ['statement', 'execute stmtcache_123']:
            parts = (prefix + '0.001 ms  parse stmtcache_123: ' + query + '\n' +
                     prefix + '0.002 ms  bind stmtcache_123: ' + query + '\n') if operation.startswith('execute ') else ''
            text = 'unrelated startup message\n' + parts + prefix + '0.023 ms  ' + operation + ': ' + query + '\n'
            result = first_use.parse_postgres_timings(text, query, count=1)
            self.assertEqual(result, [{'pid': 123, 'durationNs': 23000, 'operation': operation,
                'parseNs': 1000 if parts else 0, 'bindNs': 2000 if parts else 0,
                'serverNs': 26000 if parts else 23000}])

    def test_prepared_stages_must_be_complete_and_match(self):
        query = first_use.CASES['scalarCalledProducts']
        def line(operation):
            return 'LUXO-PG|123|luxo|LOG:  duration: 0.001 ms  ' + operation + ': ' + query + '\n'
        for text in [line('execute one'), line('bind one') + line('execute two'),
                     line('parse one') + line('bind two') + line('execute two'),
                     line('bind one') + line('parse one') + line('execute one'),
                     line('parse one') + line('statement'),
                     line('bind one') * 2 + line('execute one'),
                     line('parse one') * 2 + line('bind one') + line('execute one'),
                     line('statement') + line('bind one')]:
            with self.subTest(text=text), self.assertRaises(ValueError):
                first_use.parse_postgres_timings(text, query, count=1)

    def test_incomplete_duplicate_foreign_or_malformed_logs_fail(self):
        query = first_use.CASES['scalarCalledProducts']
        line = 'LUXO-PG|123|luxo|LOG:  duration: 0.023 ms  statement: ' + query + '\n'
        for text in ['', line * 2, line.replace('0.023', 'invalid'),
                     line.replace(query, 'SELECT * FROM products')]:
            with self.subTest(text=text), self.assertRaises(ValueError):
                first_use.parse_postgres_timings(text, query, count=1)
        with self.assertRaises(ValueError):
            first_use.parse_postgres_timings(line + line.replace('|123|', '|456|'), query, count=2)


class ProbeReportTests(unittest.TestCase):
    def test_timing_report_requires_complete_nested_singleflight_samples(self):
        timings = {'queryNs': [10] * 41, 'handlerNs': [20] * 41,
                   'httpNs': [30] * 41, 'queryErrors': 0}
        report = {'timings': timings}
        first_use.validate_timings(report)
        for key, value in [('queryNs', [10] * 40), ('handlerNs', [True] * 41),
                           ('httpNs', [-1] * 41), ('queryErrors', 1),
                           ('queryNs', [21] * 41), ('httpNs', [19] * 41)]:
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                first_use.validate_timings({'timings': dict(timings, **{key: value})})
        for invalid in ({}, {'timings': None}, dict(report, timingOverlaps=1)):
            with self.subTest(report=invalid), self.assertRaises(ValueError):
                first_use.validate_timings(invalid)

    def test_bad_client_metrics_are_rejected(self):
        report = {'api': 'ownProducts', 'mode': 'json', 'verifiedCalls': 41,
                  'firstNs': 100, 'initializationNs': 10, 'steadyNs': [10] * 30}
        first_use.validate_client(report, 'ownProducts', 'json')
        for key, value in [('verifiedCalls', 40), ('firstNs', -1),
                           ('initializationNs', None), ('steadyNs', [True] * 30),
                           ('steadyNs', [10] * 29)]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                first_use.validate_client(dict(report, **{key: value}), 'ownProducts', 'json')

    def test_rejects_bad_projection_api_or_mode(self):
        report = {'h2Requests': 41, 'otherRequests': 0, 'rpcRequests': 41,
                  'jsonRequests': 41, 'binaryRequests': 0, 'unknownMode': 0,
                  'apiRequests': {'ownProducts': 41}, 'apiErrors': 0,
                  'sql': [first_use.CASES['ownProducts']] * 41}
        first_use.validate_probe(report, 'ownProducts', 'json', True)
        for key, value in [('sql', ['SELECT * FROM products'] * 41),
                           ('apiRequests', {'pricedProducts': 41}),
                           ('binaryRequests', 1), ('rpcRequests', 0), ('apiErrors', 1)]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                first_use.validate_probe(dict(report, **{key: value}), 'ownProducts', 'json', True)


if __name__ == '__main__':
    unittest.main()
