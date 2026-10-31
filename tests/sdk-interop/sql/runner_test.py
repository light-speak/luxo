import unittest
import os
from pathlib import Path
import signal
import sys
import tempfile
import time
import runner


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
                'sql': ['SELECT * FROM products'] * 170 +
                       ['SELECT name, id FROM products'] * 170}

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
