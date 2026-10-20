"""Execution evidence remains additive and native activation has one owner."""
import json
from pathlib import Path
import tempfile
import unittest

import helm_uninstall_contract_evidence as contract
import test_evidence as evidence
import retire_completed_oidc as retirement
from helm_native_scope import CALL_SITES, production_calls

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

    def test_only_actual_pr91_registers_recoverable_retirement(self):
        number, branch = 91, 'feat/helm-uninstall-execution'
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(90, retirement.APPROVED)
        self.assertNotIn(number, retirement.DEPENDENCY_SCOPES)
        head, tip = 'a' * 40, 'b' * 40
        pr = dict(number=number, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=retirement.REPO)),
                  base=dict(ref='main', repo=dict(full_name=retirement.REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(retirement.select_scope([pr], head), number)
        self.assertEqual(retirement.candidate(pr, head, refs, [], lambda _: True,
                                             number=number, branch=branch), tip)
        with self.assertRaises(ValueError):
            retirement.candidate(pr, head, refs, [pr], lambda _: True,
                                 number=number, branch=branch)
        with self.assertRaises(ValueError):
            retirement.candidate(pr, head, refs, [], lambda _: True,
                                 number=90, branch=branch)
        refs[branch]['commit']['sha'] = 'c' * 40
        with self.assertRaises(ValueError):
            retirement.candidate(pr, head, refs, [], lambda _: True,
                                 number=number, branch=branch)

    def test_only_journal_owned_native_worker_can_acknowledge_and_execute(self):
        # Scope #94 admits only the worker whose lifetime owns the concrete
        # journal. Ordinary preview/content/CLI paths cannot call execution.
        for name in ('AcknowledgeHelmUninstallExecution', 'ExecuteHelmUninstall'):
            self.assertEqual(production_calls(ROOT, name), CALL_SITES[name])


if __name__ == '__main__':
    unittest.main()
