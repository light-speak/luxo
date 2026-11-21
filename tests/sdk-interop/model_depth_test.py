"""Reject incomplete or misleading network acceptance reports."""
import copy
import unittest

from model_depth import validate_report


class ModelDepthReportTest(unittest.TestCase):
    def setUp(self):
        self.report = {
            'assertions': 320,
            'phases': [{'protocol': protocol, 'mode': mode}
                       for protocol in ('h1', 'h2', 'ws') for mode in ('JSON', 'BINARY')],
            'server': {'invoked': {'depthOne': 52, 'depthMany': 48,
                                   'depthEcho': 54, 'depthEchoMany': 36},
                       'active': 0, 'started': 8, 'canceled': 8, 'failed': 8, 'guardedJSON': 12, 'ws': 2, 'malformed': 16,
                       'hostile': {'h1': 6, 'h2': 6, 'ws': 1, 'active': 0, 'malformed': 12, 'recovered': 6}},
        }

    def test_accepts_complete_report(self):
        validate_report(self.report)

    def test_rejects_protocol_fallback_or_missing_mode(self):
        for index, value in ((0, {'protocol': 'h2', 'mode': 'JSON'}),
                             (5, {'protocol': 'ws', 'mode': 'JSON'})):
            with self.subTest(index=index):
                report = copy.deepcopy(self.report)
                report['phases'][index] = value
                with self.assertRaises(ValueError):
                    validate_report(report)

    def test_rejects_missing_recovery_or_extra_invalid_input(self):
        for count in (53, 55):
            with self.subTest(count=count):
                report = copy.deepcopy(self.report)
                report['server']['invoked']['depthEcho'] = count
                with self.assertRaises(ValueError):
                    validate_report(report)

    def test_rejects_unfinished_assertions_and_subscription_leaks(self):
        for path, value in ((('assertions',), 319), (('phases',), []),
                            (('server', 'active'), 1), (('server', 'canceled'), 7),
                            (('server', 'failed'), 7), (('server', 'guardedJSON'), 11), (('server', 'started'), 7),
                            (('server', 'ws'), 1), (('server', 'malformed'), 15),
                            (('server', 'hostile'), {})):
            with self.subTest(path=path):
                report = copy.deepcopy(self.report)
                target = report
                for key in path[:-1]:
                    target = target[key]
                target[path[-1]] = value
                with self.assertRaises(ValueError):
                    validate_report(report)


if __name__ == '__main__':
    unittest.main()
