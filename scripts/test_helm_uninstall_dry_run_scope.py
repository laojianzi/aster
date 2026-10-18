"""Dry-run acknowledgement is never persistent mutation authority."""
import json
from pathlib import Path
import tempfile
import unittest
import helm_uninstall_contract_evidence as contract
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]

class HelmUninstallDryRunScopeTests(unittest.TestCase):
    def test_all_old_gates_and_new_cases_are_mandatory(self):
        for suite, names in contract.UNINSTALL_DRY_RUN_REQUIRED.items():
            required = contract.required(suite)
            self.assertEqual(len(names), len(set(names)))
            for group in (contract.REQUIRED, contract.STATUS_TRANSITION_REQUIRED,
                          contract.UNINSTALL_PREPARATION_REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            self.assertTrue(set(names) <= set(required))
            for name in names:
                for outcome in ('pass', 'fail', 'skip', 'missing', 'unfinished'):
                    with self.subTest(name=name, outcome=outcome), tempfile.TemporaryDirectory() as tmp:
                        events = []
                        for test in required:
                            state = outcome if test == name else 'pass'
                            if state != 'missing': events.append(dict(Action='run', Package='fixture', Test=test))
                            if state not in ('missing', 'unfinished'):
                                events.append(dict(Action=state, Package='fixture', Test=test))
                        events.append(dict(Action='pass', Package='fixture'))
                        p = Path(tmp) / 'tests.jsonl'
                        p.write_text(''.join(json.dumps(e) + '\n' for e in events))
                        self.assertEqual(evidence.summarize(p, required, suite)['valid'], outcome == 'pass')

    def test_fixed_requests_and_original_expiry(self):
        text = (ROOT / 'internal/kube/helm_uninstall_dry_run.go').read_text()
        for required in ('q.Set("dryRun", metav1.DryRunAll)', 'DryRun: []string{metav1.DryRunAll}',
                         'metav1.DeletePropagationOrphan', 'ResourceVersion: &rv', 'UID: &uid',
                         'req.GetBody = nil', 'context.WithDeadline(ctx, p.ExpiresAt())',
                         'context.WithTimeout(ctx, helmrelease.ReadTimeout)', 's.used = true',
                         's.revoked.Store(true)', 'slices.Equal(p.Targets(), fresh.Targets())',
                         'clear(patch)', 'clear(response)', 'strictjson.UnmarshalStrict',
                         'b.metricsHTTP.Do(req)', 'b.config.RateLimiter.Wait(ctx)',
                         'liveMetadataAccept', 'type helmDryRunBody struct'):
            self.assertIn(required, text)
        for forbidden in ('os/exec', 'http.MethodPost', 'http.MethodPut', 'FinishUninstallStatus',
                          '.DeleteCollection(', '.Patch(', '.Delete(', 'log.Print', 'fmt.Printf',
                          'http.DefaultClient', 'http.Client{', '.Retries(', 'time.Sleep('):
            self.assertNotIn(forbidden, text)
        self.assertEqual(text.count('b.metricsHTTP.Do(req)'), 1)
        self.assertEqual(text.count('if err := revalidate(); err != nil {'), 2)

    def test_no_ui_activation_or_inherited_read_permission(self):
        text = (ROOT / 'internal/kube/helm_uninstall_dry_run.go').read_text()
        self.assertIn('type HelmUninstallDryRunConsent struct{ state *helmDryRunConsentState }', text)
        self.assertIn('if !acknowledged {', text)
        self.assertIn('if s.used || s.revoked.Load()', text)
        for directory in ('cmd', 'internal/ui'):
            for p in (ROOT / directory).rglob('*.go'):
                if p.name.endswith('_test.go'): continue
                self.assertNotIn('PreflightHelmUninstall(', p.read_text())
                self.assertNotIn('AcknowledgeHelmUninstallDryRun(', p.read_text())
        preparation = (ROOT / 'internal/kube/helm_uninstall_preparation.go').read_text()
        self.assertNotIn('PreflightHelmUninstall(', preparation)
        self.assertNotIn('AcknowledgeHelmUninstallDryRun(', preparation)

    def test_disposable_server_and_actual_helm_checks(self):
        text = (ROOT / 'e2e/helm_uninstall_dry_run_test.go').read_text()
        for marker in ('//go:build e2e', 'testcluster.NewHelmMetadata(t, 1)',
                       'metav1.DryRunAll', 'apierrors.IsConflict', 'deny-patch', 'deny-delete',
                       'reflect.DeepEqual(s, got)', 'reflect.DeepEqual(cm, target)',
                       'reflect.DeepEqual(canary, other)', 'changed-target', 'changed-storage'):
            self.assertIn(marker, text)
        text = (ROOT / 'internal/helmrender/uninstall_contract_e2e_test.go').read_text()
        self.assertIn('t.Run("server-dry-run"', text)
        self.assertIn('!reflect.DeepEqual(before, storage)', text)
        self.assertIn('!reflect.DeepEqual(cm, target)', text)

if __name__ == '__main__':
    unittest.main()
