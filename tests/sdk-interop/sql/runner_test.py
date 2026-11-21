import unittest
from unittest.mock import patch
import json
import os
from pathlib import Path
import signal
import sys
import tempfile
import time
import runner
import swift as swift_driver


class SwiftBuildDriverTests(unittest.TestCase):
    def test_prepared_overlay_preserves_final_compiler_diagnostics(self):
        core = Path(__file__).resolve().parents[3]
        with tempfile.TemporaryDirectory(prefix='sql-swift-plan-', dir=core / '.tmp') as directory:
            work = Path(directory)
            overlay = work / 'overlay/vfs.yaml'
            arguments = ['-swift-version', '6', '-strict-concurrency=complete', '-warnings-as-errors',
                         '-l', 'LuxoClient', 'Input.swift', '-o', str(work / 'consumer')]
            with patch.object(swift_driver, 'run') as run, patch.object(
                swift_driver.subprocess, 'check_output', side_effect=[str(work), str(overlay)]
            ) as capture:
                swift_driver.build_inferred(core.parent / 'luxo-swift', core, work, arguments)
            prepare = capture.call_args_list[1].args[0]
            self.assertIn('prepare-build', prepare)
            self.assertIn('-swift-version', prepare)
            self.assertIn('6', prepare)
            compile_arguments = run.call_args_list[-1].args[0]
            self.assertEqual(compile_arguments, ['swiftc', '-module-name', 'LuxoGenerated',
                                               *arguments, '-vfsoverlay', str(overlay)])
            self.assertIn('--disable-automatic-resolution', run.call_args_list[0].args[0])


class ReportTests(unittest.TestCase):
    def report(self, runs=1):
        queries = list(runner.EXPECTED_SQL.elements()) * runs
        return {'h2Requests': 5 * runs, 'otherRequests': 0, 'sql': queries}

    def test_valid_and_reordered(self):
        for runs in (1, 2):
            report = self.report(runs)
            report['sql'].reverse()
            runner.validate_report(report, runs)

    def test_rejects_wrong_transport_count_or_projection(self):
        for key, value in [('h2Requests', 4), ('otherRequests', 1),
                           ('sql', ['SELECT * FROM products'] * 7)]:
            report = self.report()
            report[key] = value
            with self.assertRaises(ValueError):
                runner.validate_report(report, 1)

    def test_rejects_missing_extra_or_duplicate_queries(self):
        for change in (-1, 1):
            report = self.report()
            if change < 0:
                report['sql'].pop()
            else:
                report['sql'].append(report['sql'][0])
            with self.assertRaises(ValueError):
                runner.validate_report(report, 1)


class ProjectionReportTests(unittest.TestCase):
    def report(self):
        return {'h2Requests': 340, 'otherRequests': 0,
                'sql': ['SELECT * FROM "products"'] * 170 +
                       ['SELECT "name", "id" FROM "products"'] * 170}

    def test_projection_counts(self):
        runner.validate_projection_report(self.report(), 1)
        report = self.report()
        report['h2Requests'] *= 2
        report['sql'] *= 2
        runner.validate_projection_report(report, 2)

    def test_rejects_missing_wrong_or_extra_work(self):
        for key, value in [('h2Requests', 339), ('otherRequests', 1),
                           ('sql', ['SELECT * FROM products'] * 340)]:
            report = self.report()
            report[key] = value
            with self.assertRaises(ValueError):
                runner.validate_projection_report(report, 1)


class InferredReportTests(unittest.TestCase):
    def report(self, gateway):
        return {'h2Requests': 32, 'otherRequests': 0,
                'rpcRequests': 32 if gateway else 0,
                'jsonRequests': 16, 'binaryRequests': 16, 'unknownMode': 0,
                'apiErrors': 0,
                'apiRequests': {'selectedProducts': 10, 'pricedProducts': 2,
                                'privateProducts': 2, 'calledProducts': 2,
                                'calledPrivateProducts': 2, 'nestedProducts': 2,
                                'ownProducts': 2, 'ownPrivateProducts': 2,
                                'combinedProducts': 2, 'forwardedProducts': 2,
                                'scalarCalledProducts': 2, 'aliasedOwnProducts': 2},
                'sql': ['SELECT "name", "price", "id" FROM "products"'] * 16 +
                       ['SELECT "name", "id" FROM "products"'] * 4 +
                       ['SELECT "name", "secret", "id" FROM "products"'] * 6 +
                       ['SELECT "name", "secret", "price", "id" FROM "products"'] * 4 +
                       ['SELECT "name", "price", "secret", "id" FROM "products"'] * 2}

    def test_inferred_projection_and_gateway(self):
        for gateway in [False, True]:
            for runs in [1, 2]:
                report = self.report(gateway)
                for name in ['h2Requests', 'rpcRequests', 'jsonRequests', 'binaryRequests']:
                    report[name] *= runs
                report['sql'] *= runs
                report['apiRequests'] = {name: count * runs for name, count in report['apiRequests'].items()}
                runner.validate_inferred_report(report, runs, gateway)

    def test_no_fallback_or_extra_columns(self):
        for key, value in [('h2Requests', 11), ('otherRequests', 1),
                           ('jsonRequests', 11), ('binaryRequests', 9), ('unknownMode', 1),
                           ('rpcRequests', 0), ('sql', ['SELECT * FROM products'] * 12)]:
            report = self.report(True)
            report[key] = value
            with self.assertRaises(ValueError):
                runner.validate_inferred_report(report, 1, True)

    def test_missing_business_field_or_extra_column(self):
        for query in ['SELECT "name", "id" FROM "products"',
                      'SELECT "name", "price", "description", "id" FROM "products"']:
            report = self.report(True)
            report['sql'][0] = query
            with self.assertRaises(ValueError):
                runner.validate_inferred_report(report, 1, True)

    def test_duplicate_api_cannot_replace_another_with_same_sql(self):
        report = self.report(True)
        report['apiRequests']['pricedProducts'] = 4
        report['apiRequests']['ownProducts'] = 0
        with self.assertRaises(ValueError):
            runner.validate_inferred_report(report, 1, True)
        report = self.report(True)
        report['apiErrors'] = 1
        with self.assertRaises(ValueError):
            runner.validate_inferred_report(report, 1, True)


class RelationshipReportTests(unittest.TestCase):
    def report(self, runs=1):
        return {'h2Requests': 18 * runs, 'otherRequests': 0, 'rpcRequests': 18 * runs,
                'jsonRequests': 9 * runs, 'binaryRequests': 9 * runs, 'unknownMode': 0,
                'apiErrors': 0, 'apiRequests': {'articleRows': 2 * runs, 'peopleRows': 4 * runs,
                                             'articlePage': 10 * runs, 'boxedArticlePage': 2 * runs},
                'sql': list(runner.RELATIONSHIP_SQL.elements()) * runs}

    def test_exact_query_work_and_equivalent_column_order(self):
        for runs in (1, 2):
            report = self.report(runs)
            runner.validate_relationship_report(report, runs, True)
            report['sql'] = [query.replace('"title", "author_id", "id"',
                                           '"author_id", "title", "id"') for query in report['sql']]
            runner.validate_relationship_report(report, runs, True)
            report['rpcRequests'] = 0
            runner.validate_relationship_report(report, runs, False)

    def test_rejects_wildcards_hidden_columns_and_missing_batch_work(self):
        for change in ('wildcard', 'hidden', 'duplicate', 'missing', 'extra'):
            report = self.report()
            if change == 'wildcard':
                report['sql'][0] = report['sql'][0].replace('"title", "author_id", "id"', '*')
            elif change == 'hidden':
                report['sql'][0] = report['sql'][0].replace('"title"', '"title", "body"')
            elif change == 'duplicate':
                report['sql'][0] = report['sql'][0].replace('"title"', '"title", "title"')
            elif change == 'missing':
                report['sql'].pop()
            else:
                report['sql'].append(report['sql'][0])
            with self.subTest(change=change), self.assertRaises(ValueError):
                runner.validate_relationship_report(report, 1, True)

    def test_rejects_wrong_modes_errors_and_substituted_apis(self):
        for key, value in [('h2Requests', 17), ('otherRequests', 1), ('rpcRequests', 0),
                           ('jsonRequests', 8), ('binaryRequests', 8), ('unknownMode', 1),
                           ('apiErrors', 1), ('apiRequests', {'articleRows': 18})]:
            report = self.report()
            report[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                runner.validate_relationship_report(report, 1, True)


class PrecisionReportTests(unittest.TestCase):
    def report(self, runs=1, gateway=True):
        return {'h2Requests': 30 * runs, 'otherRequests': 0,
                'rpcRequests': 30 * runs if gateway else 0,
                'jsonRequests': 15 * runs, 'binaryRequests': 15 * runs,
                'unknownMode': 0, 'apiErrors': 2 * runs,
                'apiRequests': {'storePrecision': 14 * runs,
                                'readPrecision': 14 * runs,
                                'precisionRecords': 2 * runs},
                'sql': ([runner.PRECISION_INSERT] * 12 +
                        [runner.PRECISION_READ] * 14 +
                        ['SELECT * FROM "precision_records"'] * 2) * runs}

    def test_both_modes_and_real_gateway(self):
        for runs in (1, 2):
            for gateway in (False, True):
                runner.validate_precision_report(self.report(runs, gateway), runs, gateway)

    def test_rejects_missing_work_or_fallback(self):
        for key, value in [('h2Requests', 29), ('rpcRequests', 0),
                           ('otherRequests', 1), ('jsonRequests', 14),
                           ('binaryRequests', 14), ('unknownMode', 1),
                           ('apiErrors', 0), ('sql', [])]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                report = self.report()
                report[key] = value
                runner.validate_precision_report(report, 1, True)

    def storage_rows(self):
        corpus = json.loads((Path(__file__).resolve().parents[1] / 'precision.json').read_text())
        rows = [[str(base + index + 1), item['whole'], item['ratioBits'], item['amount'],
                 str(runner.precision_epoch(item['storedMoment'])), item['elapsed'], item['token'].lower()]
                for base in (0, 10) for index, item in enumerate(corpus)]
        return corpus, rows

    def test_storage_values_include_float_bits_and_pre_epoch_dates(self):
        corpus, rows = self.storage_rows()
        runner.validate_precision_storage('\n'.join('\t'.join(row) for row in rows), corpus, 1)
        self.assertEqual(str(runner.precision_epoch('1969-12-31T23:59:59.999999Z')), '-0.000001')
        self.assertEqual(str(runner.precision_epoch('0000-01-01T00:00:00Z')), '-62167219200')

    def test_storage_rejects_rounding_bad_shape_and_duplicate_rows(self):
        for column, value in [(1, '9223372036854775806'), (2, '0000000000000000'),
                              (3, '12345678901234567890.123456789'), (4, '0'),
                              (5, '9223372036854774000'), (6, 'invalid')]:
            corpus, rows = self.storage_rows()
            rows[0][column] = value
            with self.subTest(column=column), self.assertRaises(ValueError):
                runner.validate_precision_storage('\n'.join('\t'.join(row) for row in rows), corpus, 1)
        corpus, rows = self.storage_rows()
        for malformed in [rows[:-1], [rows[0]] * len(rows), [['1']] * len(rows)]:
            with self.assertRaises(ValueError):
                runner.validate_precision_storage('\n'.join('\t'.join(row) for row in malformed), corpus, 1)


class NullableReportTests(unittest.TestCase):
    def report(self, runs=1):
        return {'h2Requests': 16 * runs, 'otherRequests': 0,
                'rpcRequests': 16 * runs, 'jsonRequests': 8 * runs,
                'binaryRequests': 8 * runs, 'unknownMode': 0, 'apiErrors': 0,
                'apiRequests': {'storeNullableRecord': 6 * runs,
                                'readNullableRecord': 8 * runs,
                                'nullableRecords': 2 * runs},
                'sql': ([runner.NULLABLE_INSERT] * 6 + [runner.NULLABLE_READ] * 8 +
                        ['SELECT * FROM "nullable_records"'] * 2) * runs}

    def test_actual_gateway_both_modes(self):
        for runs in (1, 2):
            runner.validate_nullable_report(self.report(runs), runs, True)

    def test_rejects_missing_calls_wrong_modes_and_queries(self):
        for key, value in [('h2Requests', 15), ('rpcRequests', 0),
                           ('otherRequests', 1), ('jsonRequests', 7),
                           ('binaryRequests', 7), ('unknownMode', 1),
                           ('apiErrors', 1), ('sql', [])]:
            report = self.report()
            report[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                runner.validate_nullable_report(report, 1, True)


class ConsumerCleanupTests(unittest.TestCase):
    def test_exited_parent_does_not_leave_term_ignoring_child(self):
        root = Path(__file__).resolve().parents[3] / '.tmp'
        root.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='sql-cleanup-', dir=root) as directory:
            ready = Path(directory) / 'child.pid'
            child = (
                "import os, signal, time; from pathlib import Path; "
                "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                f"Path({str(ready)!r}).write_text(str(os.getpid())); time.sleep(60)"
            )
            parent = (
                "import subprocess, sys, time\nfrom pathlib import Path\n"
                f"subprocess.Popen([sys.executable, '-c', {child!r}])\n"
                f"p=Path({str(ready)!r})\n"
                "for _ in range(500):\n if p.exists(): break\n time.sleep(.01)\n"
                "assert p.exists()\n"
            )
            pid = None
            try:
                with (Path(directory) / 'log').open('w') as log:
                    runner.run_consumer([sys.executable, '-c', parent], os.environ.copy(), log)
                pid = int(ready.read_text())
                for _ in range(100):
                    try:
                        os.kill(pid, 0)
                    except ProcessLookupError:
                        return
                    time.sleep(.01)
                self.fail('consumer child survived owner cleanup')
            finally:
                if pid is None and ready.exists():
                    pid = int(ready.read_text())
                if pid is not None:
                    try:
                        os.kill(pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass


if __name__ == '__main__':
    unittest.main()
