"""Reject incomplete patch review, missing migration tests and widened cleanup."""
import hashlib
import json
from pathlib import Path
import re
import tempfile
import unittest

from test_evidence import summarize
import retire_completed_oidc as cleanup
import run_migration_tests as migration

ROOT = Path(__file__).resolve().parents[1]
SET_SHA256 = '169285f67a407f240017431b844f4e5cb71ab831ed98f7c260a1d86d7cea59aa'
UI_NAMES = (
    'TestNativeMyGo055ExplicitClockAndCurrentFrameShortcut',
    'TestNativeMyGo055SplitListReportsCurrentSelectionOnce',
)
TERMINAL_NAMES = ('TestTerminalNativeMyGo055RuntimeCopyAndImagePasteStayDenied',)


class MyGoPatchLedgerTests(unittest.TestCase):
    def test_fixed_complete_commit_mapping(self):
        lines = (ROOT / 'docs/mygo-0.5.1-0.5.5-commits.txt').read_text().splitlines()
        self.assertEqual(len(lines), 56)
        for line in lines:
            self.assertRegex(line, r'^[0-9a-f]{40} P[0-9]{2}$')
        rows = [line.split() for line in lines]
        shas = [row[0] for row in rows]
        self.assertEqual(len(set(shas)), 56)
        self.assertEqual(hashlib.sha256(('\n'.join(shas) + '\n').encode()).hexdigest(), SET_SHA256)
        self.assertEqual(shas[-1], '4d8673cd2658a2c6e86b26c44751e57bf8174f91')
        self.assertEqual({row[1] for row in rows}, {f'P{i:02}' for i in range(1, 35)})

    def test_each_family_has_explicit_handling(self):
        text = (ROOT / 'docs/dependency-review-mygo-0.5.5.md').read_text()
        rows = re.findall(r'^\| (P\d{2}) \| [^\n]+ \| (adapt|inherit|defer|not-used|metadata) \| [^\n]+ \|$', text, re.M)
        self.assertEqual(len(rows), 34)
        self.assertEqual({row[0] for row in rows}, {f'P{i:02}' for i in range(1, 35)})
        for boundary in ('previous-frame', 'transfer.Data', 'CRLF', 'physical', 'Advance', 'SetCopyOnSelect', '527', '56'):
            self.assertIn(boundary, text)

    def test_new_cases_are_declared_and_required_in_both_modes(self):
        for names, file, required in (
            (UI_NAMES, 'internal/ui/mygo055_contract_test.go', migration.REQUIRED),
            (TERMINAL_NAMES, 'internal/nativeterm/mygo055_contract_test.go', migration.TERMINAL_REQUIRED),
        ):
            source = (ROOT / file).read_text()
            for name in names:
                self.assertIn('func ' + name + '(t *testing.T)', source)
                self.assertEqual(required.count(name), 1)
        runner = (ROOT / 'scripts/run_migration_tests.py').read_text()
        self.assertIn("for mode in ('checked', 'production')", runner)
        self.assertIn("'--require', name", runner)
        self.assertIn('TestNativeMyGo04ToastExpiresAfterClosingDialog', migration.REQUIRED)
        self.assertIn('TestNativeMyGo05ScaledNavigationStaysAboveStatus', migration.REQUIRED)
        self.assertIn('TestNativeMyGo05QueuedRenewalOptInDoesNotExpandScope', migration.REQUIRED)
        self.assertIn('TestTerminalNativeMyGo05ControlCVRemainRemote', migration.TERMINAL_REQUIRED)

    def test_missing_skipped_failed_and_unfinished_new_cases_fail_evidence(self):
        names = (*UI_NAMES, *TERMINAL_NAMES)
        events = [event for name in names for event in (
            {'Package': 'fixture', 'Test': name, 'Action': 'run'},
            {'Package': 'fixture', 'Test': name, 'Action': 'pass'},
        )] + [{'Package': 'fixture', 'Action': 'pass'}]
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'tests.jsonl'
            def valid(rows):
                path.write_text('\n'.join(json.dumps(event) for event in rows))
                return summarize(path, names, 'patch-ledger-negative')['valid']
            self.assertTrue(valid(events))
            for name in names:
                self.assertFalse(valid([e for e in events if e.get('Test') != name]))
                self.assertFalse(valid([e for e in events if not (e.get('Test') == name and e['Action'] == 'pass')]))
                for action in ('fail', 'skip'):
                    changed = [dict(e, Action=action) if e.get('Test') == name and e['Action'] == 'pass' else e for e in events]
                    self.assertFalse(valid(changed))

    def test_fixed_module_and_native_provenance(self):
        spec = json.loads((ROOT / 'third_party/native-terminal/UPSTREAM.json').read_text())
        self.assertEqual(spec['version'], 'v0.5.5')
        self.assertEqual(spec['ghostty_commit'], 'befcdfd2c3a1cb24d9ec886e93c95b2b5daa7028')
        self.assertEqual(len(spec['files']), 31)
        sums = (ROOT / 'go.sum').read_text()
        self.assertIn('github.com/egoist/mygo v0.5.5 h1:3pvlEwumsck/L99cbDpay/Eqcf51B7zhKNXekiodLOw=', sums)
        self.assertIn('github.com/egoist/mygo v0.5.5/go.mod h1:daHGetyAlBkHhIZOOIcAI7C82yLUY7HgeT2+Z6w2VPU=', sums)
        self.assertIn('github.com/go-jose/go-jose/v4 v4.1.6', (ROOT / 'go.mod').read_text())
        self.assertIn('MyGo v0.5.5', (ROOT / 'README.md').read_text())

    def test_only_exact_patch_scope_can_retire_after_five_gates(self):
        self.assertEqual(cleanup.APPROVED[75], 'renovate/mygo-native-framework')
        self.assertIn(75, cleanup.DEPENDENCY_SCOPES)
        head = 'a' * 40
        runs = [dict(id=i, name=name, head_sha=head, head_branch='main', event='push', status='completed', conclusion='success')
                for i, name in enumerate(cleanup.REQUIRED)]
        self.assertFalse(cleanup.workflows_pass(runs, head, extra=('Renovate configuration',)))
        runs.append(dict(runs[0], id=10, name='Renovate configuration'))
        self.assertTrue(cleanup.workflows_pass(runs, head, extra=('Renovate configuration',)))
        self.assertEqual(cleanup.select_scope([dict(number=75, merged=True, state='closed', merge_commit_sha=head)], head), 75)
        self.assertIsNone(cleanup.select_scope([dict(number=75, merged=False, state='open', merge_commit_sha=head)], head))
        self.assertIsNone(cleanup.select_scope([dict(number=76, merged=True, state='closed', merge_commit_sha=head)], head))


if __name__ == '__main__':
    unittest.main()
