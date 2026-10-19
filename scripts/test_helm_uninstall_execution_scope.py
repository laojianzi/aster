"""Execution evidence remains additive and its desktop activation stays closed."""
import json
from pathlib import Path
import tempfile
import unittest

import helm_uninstall_contract_evidence as contract
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]


class HelmUninstallExecutionScopeTests(unittest.TestCase):
    def test_missing_execution_scenarios_cannot_qualify_complete_suites(self):
        for suite, names in contract.UNINSTALL_EXECUTION_REQUIRED.items():
            required = contract.required(suite)
            self.assertEqual(len(names), len(set(names)))
            for group in (contract.REQUIRED, contract.STATUS_TRANSITION_REQUIRED,
                          contract.UNINSTALL_PREPARATION_REQUIRED,
                          contract.UNINSTALL_DRY_RUN_REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            self.assertTrue(set(names) <= set(required))
            # The generic validator already tests failure/skip/truncation. This
            # scope regression checks the new omission risk at its actual gate.
            for missing in names:
                with self.subTest(suite=suite, missing=missing), tempfile.TemporaryDirectory() as tmp:
                    events = []
                    for name in required:
                        if name == missing:
                            continue
                        events.extend((dict(Action='run', Package='fixture', Test=name),
                                       dict(Action='pass', Package='fixture', Test=name)))
                    events.append(dict(Action='pass', Package='fixture'))
                    path = Path(tmp) / 'tests.jsonl'
                    path.write_text(''.join(json.dumps(event) + '\n' for event in events))
                    self.assertFalse(evidence.summarize(path, required, suite)['valid'])

    def test_execution_gate_reaches_full_race_coverage_and_real_server_streams(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        for stream, suite in (
            ('artifacts/unit/tests.jsonl', 'unit-native'),
            ('artifacts/unit/coverage/tests.jsonl', 'unit-native'),
            ('artifacts/e2e/tests.jsonl', 'real-cluster-native'),
        ):
            self.assertIn(f'python3 scripts/helm_uninstall_contract_evidence.py {stream} --suite {suite}', workflow)

    def test_dormant_backend_has_no_production_desktop_or_cli_call_site(self):
        # Backend qualification does not quietly activate an execution button
        # before native confirmation, lifecycle and crash recovery are reviewed.
        for directory in ('cmd', 'internal/ui'):
            for path in (ROOT / directory).rglob('*.go'):
                if path.name.endswith('_test.go'):
                    continue
                text = path.read_text()
                self.assertNotIn('AcknowledgeHelmUninstallExecution(', text, str(path))
                self.assertNotIn('ExecuteHelmUninstall(', text, str(path))


if __name__ == '__main__':
    unittest.main()
