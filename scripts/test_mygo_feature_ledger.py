"""Keep the fixed MyGo change inventory and its required regressions complete."""
import hashlib
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
COMMIT_SET_SHA256 = '84e9e869195a0c021e79873f0042753ffaa8d9ffdda237cf9ed0ec6c44d49a8a'
NAMES = (
    'TestNativeMyGo05GuardedChoiceTransitions',
    'TestNativeMyGo05PendingSensitiveFieldsDoNotInvalidateReview',
    'TestNativeMyGo05QueuedRenewalOptInDoesNotExpandScope',
)


def inventory():
    rows = []
    for line in (ROOT / 'docs/mygo-0.3-0.5-commits.txt').read_text().splitlines():
        if not re.fullmatch(r'[0-9a-f]{40} F[0-9]{2} [+=]', line):
            raise ValueError('invalid commit mapping')
        rows.append(line.split())
    return rows


class MyGoFeatureLedgerTests(unittest.TestCase):
    def test_complete_fixed_ancestry(self):
        rows = inventory()
        shas = [r[0] for r in rows]
        self.assertEqual(len(shas), 179)
        self.assertEqual(len(set(shas)), 179)
        self.assertEqual(hashlib.sha256(('\n'.join(shas) + '\n').encode()).hexdigest(), COMMIT_SET_SHA256)
        self.assertEqual(sum(r[2] == '+' for r in rows), 61)
        self.assertEqual(rows[-1][0], '408b4def1f3e6a8d3fac852396b4a46d82ba22a3')

    def test_each_commit_has_a_documented_decision(self):
        text = (ROOT / 'docs/mygo-0.3-0.5-feature-ledger.md').read_text()
        found = re.findall(r'^\| (F\d{2}) \| [^\n]+? \| (retain|inherit|adapt|defer|reject|not-used|metadata) \| [^\n]+ \|$', text, re.M)
        ids = [r[0] for r in found]
        self.assertEqual(len(ids), 72)
        self.assertEqual(set(ids), {f'F{i:02d}' for i in range(1, 73)})
        self.assertEqual({r[1] for r in inventory()}, set(ids))

    def test_functional_release_commits_are_not_only_metadata(self):
        # Release 0.3.1/2/3 contain input and lifecycle code, not only versions.
        rows = inventory()
        shas = ['3cdb66183ddcdcc1589b16d3309fa8706ef429e1', '88543ba0bc7e131ef00126a56a627c25c735bec6', 'e25a1fc282f93f8af4b7c2ece0b133f62f277265']
        for sha in shas:
            self.assertEqual(next(r[1] for r in rows if r[0] == sha), 'F01')

    def test_new_contracts_remain_mandatory_in_both_modes(self):
        source = (ROOT / 'internal/ui/mygo05_pending_test.go').read_text()
        runner = (ROOT / 'scripts/run_migration_tests.py').read_text()
        for name in NAMES:
            self.assertIn('func ' + name + '(t *testing.T)', source)
            self.assertIn('"' + name + '"', runner.split('TERMINAL_REQUIRED')[0])
        self.assertIn("for mode in ('checked', 'production')", runner)
        self.assertIn("'--require', name", runner)

    def test_module_native_pins_and_review_stay_coupled(self):
        import json
        spec = json.loads((ROOT / 'third_party/native-terminal/UPSTREAM.json').read_text())
        self.assertEqual(spec['version'], 'v0.5.5')
        self.assertEqual(len(spec['files']), 31)
        self.assertIn('github.com/egoist/mygo v0.5.5', (ROOT / 'go.mod').read_text())
        text = (ROOT / 'docs/dependency-review-mygo-0.5.1.md').read_text()
        for boundary in ('previous-frame', 'transfer.Data', 'CRLF', 'physical', 'unreleased'):
            self.assertIn(boundary, text)


if __name__ == '__main__':
    unittest.main()
