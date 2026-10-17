"""The new preparation is additive, inert and cannot replace write confirmation."""
import json
from pathlib import Path
import tempfile
import unittest
import test_evidence as evidence
import helm_uninstall_contract_evidence as contract


class HelmUninstallPreparationScopeTests(unittest.TestCase):
    def test_required_preparation_names_cannot_disappear(self):
        for suite, names in contract.UNINSTALL_PREPARATION_REQUIRED.items():
            required = contract.required(suite)
            self.assertEqual(len(names), len(set(names)))
            for group in (contract.REQUIRED, contract.STATUS_TRANSITION_REQUIRED,
                          evidence.HELM_LIVE_OBSERVATION_REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            self.assertTrue(set(names) <= set(required))
            for name in names:
                for outcome in ('pass', 'fail', 'skip', 'missing', 'unfinished'):
                    with self.subTest(suite=suite, name=name, outcome=outcome), tempfile.TemporaryDirectory() as tmp:
                        events = []
                        for test in required:
                            state = outcome if test == name else 'pass'
                            if state != 'missing':
                                events.append(dict(Action='run', Package='fixture', Test=test))
                            if state not in ('missing', 'unfinished'):
                                events.append(dict(Action=state, Package='fixture', Test=test))
                        events.append(dict(Action='pass', Package='fixture'))
                        p = Path(tmp) / 'tests.jsonl'
                        p.write_text(''.join(json.dumps(e) + '\n' for e in events))
                        self.assertEqual(evidence.summarize(p, required, suite)['valid'], outcome == 'pass')

    def test_preparation_has_no_mutation_cli_or_activation(self):
        root = Path(__file__).resolve().parents[1]
        paths = (root / 'internal/helmrelease/uninstall_preparation.go',
                 root / 'internal/kube/helm_uninstall_preparation.go')
        for p in paths:
            text = p.read_text()
            for forbidden in ('os/exec', 'exec.Command', 'http.MethodPatch', 'http.MethodDelete',
                              '.Patch(', '.Delete(', 'BuildUninstallStatusPatch(', 'log.Print', 'fmt.Printf'):
                self.assertNotIn(forbidden, text)
        text = paths[1].read_text()
        self.assertEqual(text.count('b.HelmReleaseMetadata(ctx, ns)'), 2)
        self.assertIn('b.readHelmRevision(', text)
        self.assertIn('b.observeHelmItems(ctx, profile.Preview(), true)', text)
        self.assertIn('helmrelease.ReadTimeout', text)
        self.assertIn('owner  *backendIdentity', text)
        for base in ('cmd', 'internal/ui'):
            for path in (root / base).rglob('*.go'):
                if not path.name.endswith('_test.go'):
                    self.assertNotIn('PrepareHelmUninstall(', path.read_text())

    def test_real_server_and_actual_helm_coverage_is_preserved(self):
        root = Path(__file__).resolve().parents[1]
        text = (root / 'e2e/helm_uninstall_preparation_test.go').read_text()
        for marker in ('//go:build e2e', 'testcluster.NewHelmMetadata(t, 1)',
                       'checkUnchanged(t,', '"multiple-revisions"', '"denied"', 'owner.UID'):
            self.assertIn(marker, text)
        text = (root / 'internal/helmrender/uninstall_contract_e2e_test.go').read_text()
        self.assertIn('t.Run("restricted-preparation"', text)
        self.assertIn('!reflect.DeepEqual(before, storage)', text)
        self.assertIn('!reflect.DeepEqual(cm, target)', text)
        self.assertIn('status-patch-dry-run', text)


if __name__ == '__main__':
    unittest.main()
