"""Payload rewriting is not an execution capability; every gate is additive."""
import json
from pathlib import Path
import tempfile
import unittest
import test_evidence as evidence
import helm_uninstall_contract_evidence as contract

class HelmStatusTransitionScopeTests(unittest.TestCase):
    def test_required_transition_tests_cannot_disappear_or_skip(self):
        for suite, names in contract.STATUS_TRANSITION_REQUIRED.items():
            required = contract.required(suite)
            self.assertTrue(set(names) <= set(required))
            for group in (evidence.HELM_INSPECTION_REQUIRED, evidence.HELM_UNINSTALL_PREVIEW_REQUIRED,
                          evidence.HELM_LIVE_OBSERVATION_REQUIRED, contract.REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            for name in names:
                for outcome in ('pass', 'fail', 'skip', 'missing', 'unfinished'):
                    with self.subTest(suite=suite, name=name, outcome=outcome), tempfile.TemporaryDirectory() as tmp:
                        events = []
                        for test in required:
                            state = outcome if test == name else 'pass'
                            if state != 'missing': events.append(dict(Action='run', Package='fixture', Test=test))
                            if state not in ('missing', 'unfinished'): events.append(dict(Action=state, Package='fixture', Test=test))
                        events.append(dict(Action='pass', Package='fixture'))
                        p = Path(tmp) / 'tests.jsonl'
                        p.write_text(''.join(json.dumps(e) + '\n' for e in events))
                        self.assertEqual(evidence.summarize(p, required, suite)['valid'], outcome == 'pass')

    def test_codec_has_no_production_transport_or_ui_call_site(self):
        root = Path(__file__).resolve().parents[1]
        codec = root / 'internal/helmrelease/status_transition.go'
        text = codec.read_text()
        for marker in ('inspectSecretPayload(', 'ctx.Err()', 'MaxCompressedBytes', 'MaxSecretBytes',
                       'MaxReleaseBytes', '"test", "/metadata/uid"', '"test", "/metadata/resourceVersion"',
                       '"replace", "/data/release"', '"add", "/metadata/labels/modifiedAt"'):
            self.assertIn(marker, text)
        for unsafe in ('net/http', 'os/exec', 'kubernetes.NewForConfig', 'fmt.Printf', 'log.Print'):
            self.assertNotIn(unsafe, text)
        for base in ('cmd', 'internal'):
            for path in (root / base).rglob('*.go'):
                if path == codec or path.name.endswith('_test.go'): continue
                self.assertNotIn('BuildUninstallStatusPatch(', path.read_text(), str(path))

    def test_server_cases_use_disposable_opt_in_and_dry_run(self):
        root = Path(__file__).resolve().parents[1]
        text = (root / 'e2e/helm_status_transition_test.go').read_text()
        for marker in ('//go:build e2e', 'testcluster.NewHelmUninstallPreview(t)', 'metav1.DryRunAll',
                       'types.JSONPatchType', 'apierrors.IsForbidden', 'apierrors.IsInvalid',
                       'concurrent.ResourceVersion', 'reflect.DeepEqual(canary, live)'):
            if marker == 'concurrent.ResourceVersion':
                self.assertIn('statusWire(t, concurrent)', text)
            else:
                self.assertIn(marker, text)
        text = (root / 'internal/helmrender/uninstall_contract_e2e_test.go').read_text()
        self.assertIn('status-patch-dry-run', text)
        self.assertIn('transformed actual Helm payload', text)

if __name__ == '__main__':
    unittest.main()
