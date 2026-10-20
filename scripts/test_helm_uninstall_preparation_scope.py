"""The new preparation is additive, inert and cannot replace write confirmation."""
import json
import copy
import retire_completed_oidc as retirement
from pathlib import Path
import tempfile
import unittest
import test_evidence as evidence
import helm_uninstall_contract_evidence as contract
from helm_native_scope import CALL_SITES, production_calls


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

    def test_pinned_chart_time_regressions_are_mandatory(self):
        names = contract.UNINSTALL_PREPARATION_REQUIRED['unit-native']
        self.assertIn('TestHelmUninstallProfilePinnedChartWire', names)
        for name, count in (('TestHelmUninstallProfileChartTimes', 16),
                            ('TestHelmUninstallProfileRejectsChartTimeAmbiguity', 36)):
            self.assertIn(name, names)
            self.assertEqual(sum(n.startswith(name + '/') for n in names), count)

    def test_only_actual_pr87_can_retire_the_exact_branch(self):
        number, branch = 87, 'feat/helm-uninstall-preparation'
        head, tip = 'a' * 40, 'b' * 40
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(86, retirement.APPROVED)
        self.assertNotIn(number, retirement.DEPENDENCY_SCOPES)
        pr = dict(number=number, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=retirement.REPO)),
                  base=dict(ref='main', repo=dict(full_name=retirement.REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(retirement.select_scope([pr], head), number)
        self.assertEqual(retirement.candidate(pr, head, refs, [], lambda _: True,
                                             number=number, branch=branch), tip)
        for mode in ('issue-number', 'open', 'unmerged', 'wrong-merge', 'wrong-branch',
                     'foreign', 'moved', 'protected', 'active', 'unreachable'):
            with self.subTest(mode=mode):
                p, branches = copy.deepcopy(pr), copy.deepcopy(refs)
                if mode == 'issue-number': p['number'] = 86
                if mode == 'open': p['state'] = 'open'
                if mode == 'unmerged': p['merged'] = False
                if mode == 'wrong-merge': p['merge_commit_sha'] = 'c' * 40
                if mode == 'wrong-branch': p['head']['ref'] = 'feat/helm-release-status-transition'
                if mode == 'foreign': p['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'moved': branches[branch]['commit']['sha'] = 'd' * 40
                if mode == 'protected': branches[branch]['protected'] = True
                with self.assertRaises(ValueError):
                    retirement.candidate(p, head, branches, [pr] if mode == 'active' else [],
                                         lambda _: mode != 'unreachable', number=number, branch=branch)

    def test_preparation_has_no_mutation_and_only_admitted_native_consumer(self):
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
        # Scope #94 activates one independently confirmed native controller.
        # The preparation remains GET-only and all other UI/CLI paths stay out.
        name = 'PrepareHelmUninstall'
        self.assertEqual(production_calls(root, name), CALL_SITES[name])

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
