import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('ordinary', ROOT / 'scripts/diagnose-pr143-ordinary.py')
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


class OrdinaryTest(unittest.TestCase):
    def setUp(self):
        (ROOT / '.tmp').mkdir(exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=ROOT / '.tmp')
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name)

    def inventory(self):
        api.plan(self.path)
        expected = json.loads((self.path / 'expected.json').read_text())['Base']
        return 'cpu: Intel(R) Xeon(R) 6975P-C\n' + ''.join(
            f"{row['Name']} 1 5 ns/op 0 B/op 0 allocs/op\n" for row in expected) + 'PASS\n'

    def test_plan_retains_all_six_and_four_controls(self):
        api.plan(self.path)
        data = json.loads((self.path / 'matrix.json').read_text())
        self.assertEqual(len(data['comparisons']), 7)
        self.assertEqual(data['processes'], 280)
        self.assertEqual(len(json.loads((self.path / 'expected.json').read_text())['Base']), 6)
        self.assertEqual(data['comparisons'][-2], ['ac', 'base', 'candidate-head'])

    def test_exact_inventory(self):
        raw = self.inventory()
        api.inventory(self.path / 'expected.json', raw, 1)
        for bad in [raw + raw, raw.replace('PASS', 'FAIL'), raw.replace('6975P-C', '8573C'),
                    raw.replace('anonymous/after', 'anonymous/other'), raw.replace(' 1 ', ' 2 ', 1)]:
            with self.subTest(bad=bad), self.assertRaises(AssertionError):
                api.inventory(self.path / 'expected.json', bad, 1)
        api.inventory(self.path / 'expected.json', raw * 10, 10)

    def fixture(self):
        dest = self.path / 'pkg/lux/luvia'
        dest.mkdir(parents=True)
        for name in ['luvia.go', 'middleware.go', 'middleware_test.go']:
            shutil.copyfile(ROOT / 'pkg/lux/luvia' / name, dest / name)
        return dest

    def test_candidate_preserves_exact_fixture_and_body(self):
        dest = self.fixture()
        before = (dest / 'middleware_test.go').read_bytes()
        api.apply_candidate(self.path, 'base', self.path / 'proof.json')
        self.assertEqual((dest / 'middleware_test.go').read_bytes(), before)
        self.assertIn('router.IdentityExtractor = gatewayIdentity', (dest / 'luvia.go').read_text())
        self.assertIn('if identity := Identity(ctx); identity != nil', (dest / 'identity.go').read_text())
        with self.assertRaises(AssertionError):
            api.apply_candidate(self.path, 'base', self.path / 'proof.json')

    def test_changed_input_rejected_before_writes(self):
        dest = self.fixture()
        original = (dest / 'luvia.go').read_bytes()
        (dest / 'middleware_test.go').write_text('changed')
        with self.assertRaises(AssertionError):
            api.apply_candidate(self.path, 'base', self.path / 'proof.json')
        self.assertEqual((dest / 'luvia.go').read_bytes(), original)
        self.assertFalse((dest / 'identity.go').exists())

    def test_summary_never_clears_stock_failure(self):
        for controls, stock, deployed, auxiliary, status in [
            (True, 0, 0, 0, 0), (True, 1, 0, 0, 1), (True, 0, 1, 0, 1),
            (True, 0, 0, 1, 1), (False, 0, 0, 0, 4), (True, 4, 0, 0, 4)]:
            with self.subTest(stock=stock, deployed=deployed, status=status):
                self.assertEqual(api.outcome(controls, stock, deployed, auxiliary)[0], status)

    def test_shell_matrix_is_the_registered_matrix(self):
        script = (ROOT / 'scripts/diagnose-pr143-ordinary.sh').read_text()
        expected = 'comparisons=(' + ' '.join(':'.join(row) for row in api.COMPARISONS) + ')'
        self.assertIn(expected, script)
        self.assertNotIn('-funcalign', script)
        self.assertNotIn('-ldflags', script)
        self.assertNotIn('-gcflags', script)

    def test_real_input_manifest(self):
        import subprocess
        if not (ROOT / '.git').exists():
            self.skipTest('archive has no commit metadata; host checkouts are checked by runner')
        rev = '5fc47e2a77a5a964e945dc6867ddb7061ce9e9fd'
        api.verify_inputs(ROOT, rev, 'base', self.path / 'inputs.txt')
        with self.assertRaises(AssertionError):
            api.verify_inputs(ROOT, rev, 'head', self.path / 'invalid.txt')

    def test_summarizer_preserves_stock_rejection(self):
        controls = [f'{name}.{group}{direction}' for name in ('aa', 'bb', 'cc', 'dd')
                    for group in (1, 2) for direction in ('', '.reverse')]
        names = controls + ['ab-combined', 'ac-combined', 'cd-combined']
        for bad in (False, True):
            gates = ''
            for name in names:
                reject = bad and name == 'ab-combined'
                gates += name + ('\t1\n' if reject else '\t0\n')
                (self.path / (name + '.gate.txt')).write_text(
                    'performance gate rejected 2 regression(s)\n' if reject else 'performance gate passed: proof\n')
            (self.path / 'gates.tsv').write_text(gates)
            self.assertEqual(api.summarize(self.path), 1 if bad else 0)
        (self.path / 'gates.tsv').write_text(gates.replace('aa.1\t0\n', ''))
        with self.assertRaises(AssertionError):
            api.summarize(self.path)


if __name__ == '__main__':
    unittest.main()
