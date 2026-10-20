"""Dry-run acknowledgement is never persistent mutation authority."""
import copy
import json
from pathlib import Path
import tempfile
import unittest
import helm_uninstall_contract_evidence as contract
import test_evidence as evidence
import retire_completed_oidc as retirement
from helm_native_scope import CALL_SITES, production_calls

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

    def test_only_actual_pr89_can_retire_the_exact_branch(self):
        number, branch = 89, 'feat/helm-uninstall-dry-run'
        head, tip = 'a' * 40, 'b' * 40
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(88, retirement.APPROVED)
        self.assertNotIn(number, retirement.DEPENDENCY_SCOPES)
        pr = dict(number=number, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=retirement.REPO)),
                  base=dict(ref='main', repo=dict(full_name=retirement.REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(retirement.select_scope([pr], head), number)
        self.assertEqual(retirement.candidate(pr, head, refs, [], lambda _: True,
                                             number=number, branch=branch), tip)
        for mode in ('issue', 'open', 'unmerged', 'merge', 'branch', 'base', 'foreign',
                     'moved', 'protected', 'active', 'unreachable'):
            with self.subTest(mode=mode):
                p, branches = copy.deepcopy(pr), copy.deepcopy(refs)
                if mode == 'issue': p['number'] = 88
                if mode == 'open': p['state'] = 'open'
                if mode == 'unmerged': p['merged'] = False
                if mode == 'merge': p['merge_commit_sha'] = 'c' * 40
                if mode == 'branch': p['head']['ref'] = 'feat/helm-uninstall-preparation'
                if mode == 'base': p['base']['ref'] = 'other'
                if mode == 'foreign': p['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'moved': branches[branch]['commit']['sha'] = 'd' * 40
                if mode == 'protected': branches[branch]['protected'] = True
                with self.assertRaises(ValueError):
                    retirement.candidate(p, head, branches, [pr] if mode == 'active' else [],
                                         lambda _: mode != 'unreachable', number=number, branch=branch)

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

    def test_only_admitted_native_consumer_and_no_inherited_read_permission(self):
        text = (ROOT / 'internal/kube/helm_uninstall_dry_run.go').read_text()
        self.assertIn('type HelmUninstallDryRunConsent struct{ state *helmDryRunConsentState }', text)
        self.assertIn('if !acknowledged {', text)
        self.assertIn('if s.used || s.revoked.Load()', text)
        for name in ('PreflightHelmUninstall', 'AcknowledgeHelmUninstallDryRun'):
            self.assertEqual(production_calls(ROOT, name), CALL_SITES[name])
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
