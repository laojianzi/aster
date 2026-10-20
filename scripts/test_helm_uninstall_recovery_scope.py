"""Recovery is additive evidence and cannot quietly activate or resume writes."""
import json
from pathlib import Path
import tempfile
import unittest

import helm_journal_evidence as journal
import helm_uninstall_contract_evidence as contract
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]


class HelmUninstallRecoveryScopeTests(unittest.TestCase):
    def test_new_recovery_names_are_mandatory_without_removing_old_contracts(self):
        for suite, names in contract.UNINSTALL_RECOVERY_REQUIRED.items():
            required = contract.required(suite)
            self.assertEqual(len(names), len(set(names)))
            for group in (contract.REQUIRED, contract.STATUS_TRANSITION_REQUIRED,
                          contract.UNINSTALL_PREPARATION_REQUIRED,
                          contract.UNINSTALL_DRY_RUN_REQUIRED,
                          contract.UNINSTALL_EXECUTION_REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            self.assertTrue(set(names) <= set(required))
            for missing in names:
                with self.subTest(suite=suite, missing=missing), tempfile.TemporaryDirectory() as tmp:
                    events = []
                    for name in required:
                        if name != missing:
                            events.extend((dict(Action='run', Package='fixture', Test=name),
                                           dict(Action='pass', Package='fixture', Test=name)))
                    events.append(dict(Action='pass', Package='fixture'))
                    path = Path(tmp) / 'tests.jsonl'
                    path.write_text(''.join(json.dumps(e) + '\n' for e in events))
                    self.assertFalse(evidence.summarize(path, required, suite)['valid'])

    def test_actual_full_race_coverage_real_server_and_three_os_journal_streams(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        for stream, suite in (
            ('artifacts/unit/tests.jsonl', 'unit-native'),
            ('artifacts/unit/coverage/tests.jsonl', 'unit-native'),
            ('artifacts/e2e/tests.jsonl', 'real-cluster-native'),
        ):
            self.assertIn(f'python3 scripts/helm_uninstall_contract_evidence.py {stream} --suite {suite}', workflow)
        native = (ROOT / '.github/workflows/native-migration.yml').read_text()
        self.assertIn('go test -race -shuffle=on -count=1 -timeout=3m -json ./internal/helmjournal/...', native)
        self.assertIn('python scripts/helm_journal_evidence.py artifacts/journal/tests.jsonl', native)
        self.assertTrue(set(journal.REQUIRED) <= set(contract.UNINSTALL_RECOVERY_REQUIRED['unit-native']))

    def test_recovery_does_not_activate_desktop_or_cli_mutation(self):
        for directory in ('cmd', 'internal/ui'):
            for path in (ROOT / directory).rglob('*.go'):
                if path.name.endswith('_test.go'):
                    continue
                text = path.read_text()
                for call in ('AcknowledgeHelmUninstallExecution(', 'ExecuteHelmUninstall(',
                             'AcknowledgeHelmUninstallReconciliation(', 'ObserveHelmUninstallRecovery('):
                    self.assertNotIn(call, text, str(path))

    def test_offline_journal_and_get_observer_cannot_restore_a_write_path(self):
        for path in (ROOT / 'internal/helmjournal').glob('*.go'):
            if path.name.endswith('_test.go'):
                continue
            text = path.read_text()
            for token in ('"net/http"', 'internal/kube', 'internal/operation', 'HelmUninstallExecutionConsent'):
                self.assertNotIn(token, text, str(path))
        for path in (ROOT / 'internal/kube').glob('helm_uninstall_reconciliation*.go'):
            if path.name.endswith('_test.go'):
                continue
            text = path.read_text()
            for token in ('helmExecutionRun', 'BuildUninstallStatusPatch(', 'http.MethodPatch',
                          'http.MethodDelete', 'http.MethodPost', 'ExecuteHelmUninstall('):
                self.assertNotIn(token, text, str(path))


if __name__ == '__main__':
    unittest.main()
