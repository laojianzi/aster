"""Contract characterization is mandatory, not a production CLI capability."""
import json
from pathlib import Path
import tempfile
import unittest
import test_evidence as evidence
import helm_uninstall_contract_evidence as contract_evidence

class HelmUninstallContractScopeTests(unittest.TestCase):
    def test_each_new_case_is_required_and_cannot_hide_failure(self):
        for suite, additions in contract_evidence.REQUIRED.items():
            required = contract_evidence.required(suite) + ['TestPrior']
            self.assertEqual(len(required), len(set(required)))
            for group in (evidence.HELM_LIVE_OBSERVATION_REQUIRED, evidence.HELM_UNINSTALL_PREVIEW_REQUIRED,
                          evidence.HELM_INSPECTION_REQUIRED, evidence.HELM_METADATA_REQUIRED,
                          evidence.HELM_RENDER_REQUIRED, evidence.INTEGER_MAP_REQUIRED,
                          evidence.INTEGER_SET_REQUIRED, evidence.BOOLEAN_COLLECTIONS_REQUIRED,
                          evidence.MANIFEST_CLIPBOARD_REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            for name in additions:
                for outcome in ('pass', 'missing', 'skip', 'fail', 'unfinished'):
                    with self.subTest(suite=suite, name=name, outcome=outcome), tempfile.TemporaryDirectory() as d:
                        events = []
                        for test in required:
                            state = outcome if test == name else 'pass'
                            if state != 'missing': events.append(dict(Action='run', Package='fixture', Test=test))
                            if state not in ('missing', 'unfinished'): events.append(dict(Action=state, Package='fixture', Test=test))
                        events.append(dict(Action='pass', Package='fixture'))
                        p = Path(d) / 'tests.jsonl'
                        p.write_text(''.join(json.dumps(e) + '\n' for e in events))
                        self.assertEqual(evidence.summarize(p, required, suite)['valid'], outcome == 'pass')

    def test_ci_gates_are_separate_commands_not_folded_yaml(self):
        text = (Path(__file__).resolve().parents[1] / '.github/workflows/ci.yml').read_text()
        for suite, step in (('unit-native', 'Validate test evidence'),
                            ('real-cluster-native', 'Validate real-cluster evidence')):
            block = text.split('      - name: ' + step + '\n', 1)[1].split('      - name:', 1)[0]
            self.assertIn('        run: |\n', block)
            self.assertTrue(any(line.startswith('          python3 scripts/test_evidence.py ')
                                for line in block.splitlines()))
            gate = [line for line in block.splitlines()
                    if line.startswith('          python3 scripts/helm_uninstall_contract_evidence.py ')]
            self.assertEqual(len(gate), 1)
            self.assertTrue(gate[0].endswith('--suite ' + suite))

    def test_test_only_execution_and_disposable_cluster_guards(self):
        root = Path(__file__).resolve().parents[1]
        hermetic = (root / 'internal/helmrender/uninstall_contract_test.go').read_text()
        cluster = (root / 'internal/helmrender/uninstall_contract_e2e_test.go').read_text()
        self.assertTrue(cluster.startswith('//go:build e2e\n'))
        for marker in ('readLocal(', 'sha256.Sum256', 'unpackExecutable(', 'isolatedEnvironment(',
                       'exec.CommandContext', 'cmd.WaitDelay', 'httptest.NewServer', 'entry.Options.Preconditions'):
            self.assertIn(marker, hermetic)
        for marker in ('testcluster.Config(t)', 'IsLoopback()', 'cfg.Insecure', 'cfg.ExecProvider',
                       'clientcmd.Write(raw)', 'GenerateName:', 't.Cleanup(', 'PreviewHelmUninstall(',
                       'ObserveHelmPreview(', 'reflect.DeepEqual', 'originalUID'):
            self.assertIn(marker, cluster)
        for file in ('.github/workflows/ci.yml', 'scripts/e2e.sh'):
            self.assertIn('./internal/helmrender/...', (root / file).read_text())
            self.assertIn('scripts/helm_uninstall_contract_evidence.py', (root / file).read_text())
        contract = (root / 'docs/helm-uninstall-contract.md').read_text()
        for boundary in ('NOT enabled', 'Unknown', 'UID', 'resourceVersion', 'No automatic retry',
                         'sole stored revision', 'P3', 'not activated'):
            self.assertIn(boundary, contract)

if __name__ == '__main__':
    unittest.main()
