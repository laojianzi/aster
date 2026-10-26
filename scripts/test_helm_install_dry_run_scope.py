"""Preflight preserves old gates; neither retries nor a green partial log qualify."""
import json
from pathlib import Path
import tempfile
import unittest
import helm_install_dry_run_evidence as dry
import helm_install_evidence as preparation
import helm_uninstall_contract_evidence as contract
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]

class InstallDryRunScopeTests(unittest.TestCase):
    def test_additive_exact_requirements(self):
        for suite, names in dry.INSTALL_DRY_RUN_REQUIRED.items():
            self.assertEqual(len(names), len(set(names)))
            self.assertTrue(set(names) <= set(contract.required(suite)))
            self.assertTrue(set(preparation.INSTALL_REQUIRED[suite]) <= set(contract.required(suite)))
            self.assertFalse(set(names) & set(preparation.INSTALL_REQUIRED[suite]))
        self.assertEqual(len(dry.INSTALL_DRY_RUN_REQUIRED['real-cluster-native']), 11)

    def test_every_case_requires_one_finished_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'events.jsonl'
            for suite, names in dry.INSTALL_DRY_RUN_REQUIRED.items():
                for selected in names:
                    for mode in ('pass', 'missing', 'fail', 'skip', 'unfinished', 'duplicate'):
                        events = []
                        for name in names:
                            if name == selected and mode == 'missing': continue
                            events.append(dict(Action='run',Package='fixture',Test=name))
                            if name == selected and mode == 'unfinished': continue
                            action = mode if name == selected and mode in ('fail','skip') else 'pass'
                            events.append(dict(Action=action,Package='fixture',Test=name))
                            if name == selected and mode == 'duplicate':
                                events.append(dict(Action='pass',Package='fixture',Test=name))
                        events.append(dict(Action='pass',Package='fixture'))
                        path.write_text(''.join(json.dumps(e)+'\n' for e in events))
                        self.assertEqual(evidence.summarize(path,list(names),suite)['valid'],mode=='pass')

    def test_no_execution_or_native_activation(self):
        code=(ROOT/'internal/kube/helm_install_dry_run.go').read_text()
        for forbidden in ('BuildInstallDeployedPatch(', 'store.Begin(', 'exec.Command', '.Create(', '.Patch(', '.Delete('):
            self.assertNotIn(forbidden,code)
        for p in (ROOT/'internal/ui').glob('*.go'):
            if not p.name.endswith('_test.go'):
                self.assertNotIn('PreflightHelmInstall(',p.read_text())
        transport=(ROOT/'internal/kube/helm_install_dry_run_transport.go').read_text()
        for exact in ('http.MethodPost', 'q.Set("dryRun", "All")', 'q.Set("fieldValidation", "Strict")',
                      'helmrelease.InstallFieldManager', 'req.GetBody = nil', 'b.config.RateLimiter.Wait(ctx)'):
            self.assertIn(exact,transport)
        for forbidden in ('http.MethodPatch','http.MethodDelete','http.MethodPut','MaxRetries','time.Sleep'):
            self.assertNotIn(forbidden,transport)

    def test_real_cluster_has_independent_guard_and_fixture_comparison(self):
        text=(ROOT/'e2e/helm_install_dry_run_test.go').read_text()
        for token in ('//go:build e2e','r.GetBody != nil','reflect.DeepEqual(got, want)',
                      'SubjectAccessReviews()', 'ErrInstallConflict', 'ErrLiveForbidden',
                      'apierrors.IsNotFound(err)', 'post', 'competingSecret'):
            self.assertIn(token,text)
