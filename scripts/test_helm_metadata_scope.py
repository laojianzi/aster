"""Metadata-only inspection stays additive to the existing executable gates."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

import retire_completed_oidc as retirement
import run_migration_tests as migration
import test_evidence as evidence


class HelmMetadataScopeTests(unittest.TestCase):
    def test_retirement_requires_exact_completed_inactive_reachable_pr(self):
        number, branch, head, tip = 70, 'feat/helm-release-metadata', 'a' * 40, 'b' * 40
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(69, retirement.APPROVED)
        self.assertNotIn(number, retirement.DEPENDENCY_SCOPES)
        pr = dict(number=number, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=retirement.REPO)),
                  base=dict(ref='main', repo=dict(full_name=retirement.REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(retirement.select_scope([pr], head), number)
        self.assertEqual(retirement.candidate(pr, head, refs, [], lambda _: True,
                                             number=number, branch=branch), tip)
        self.assertIsNone(retirement.candidate(pr, head, {}, [], lambda _: True,
                                              number=number, branch=branch))
        for mode in ('open', 'unmerged', 'other-merge', 'wrong-branch', 'wrong-base',
                     'foreign-head', 'foreign-base', 'invalid-tip', 'moved', 'protected',
                     'unreachable', 'active-head', 'active-base', 'issue', 'previous-pr'):
            with self.subTest(mode=mode):
                p, branches, active = copy.deepcopy(pr), copy.deepcopy(refs), []
                n = number
                if mode == 'open': p['state'] = 'open'
                if mode == 'unmerged': p['merged'] = False
                if mode == 'other-merge': p['merge_commit_sha'] = 'c' * 40
                if mode == 'wrong-branch': p['head']['ref'] = retirement.APPROVED[68]
                if mode == 'wrong-base': p['base']['ref'] = 'other-main'
                if mode == 'foreign-head': p['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'foreign-base': p['base']['repo']['full_name'] = 'foreign/repo'
                if mode == 'invalid-tip': p['head']['sha'] = 'invalid'
                if mode == 'moved': branches[branch]['commit']['sha'] = 'c' * 40
                if mode == 'protected': branches[branch]['protected'] = True
                if mode in ('active-head', 'active-base'):
                    other = copy.deepcopy(pr)
                    other['head']['ref'], other['base']['ref'] = 'unrelated-head', 'unrelated-base'
                    other[mode.removeprefix('active-')]['ref'] = branch
                    active = [other]
                if mode == 'issue': n = 69
                if mode == 'previous-pr': n = 68
                with self.assertRaises(ValueError):
                    retirement.candidate(p, head, branches, active, lambda _: mode != 'unreachable',
                                         number=n, branch=branch)

    def test_required_names_are_additive_and_missing_or_skipped_fail(self):
        for suite, names in evidence.HELM_METADATA_REQUIRED.items():
            required = evidence.required_for_suite(suite, ['TestPrior'])
            for group in (evidence.INTEGER_MAP_REQUIRED, evidence.INTEGER_SET_REQUIRED,
                          evidence.MANIFEST_CLIPBOARD_REQUIRED, evidence.BOOLEAN_COLLECTIONS_REQUIRED,
                          evidence.HELM_RENDER_REQUIRED, evidence.HELM_METADATA_REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            self.assertEqual(len(required), len(set(required)))
            for name in names:
                for outcome in ('pass', 'skip', 'fail', 'missing', 'unfinished'):
                    with self.subTest(suite=suite, name=name, outcome=outcome), tempfile.TemporaryDirectory() as temp:
                        path = Path(temp) / 'tests.jsonl'
                        events = []
                        for test in required:
                            status = outcome if test == name else 'pass'
                            if status != 'missing':
                                events.append(dict(Action='run', Package='fixture', Test=test))
                            if status not in ('missing', 'unfinished'):
                                events.append(dict(Action=status, Package='fixture', Test=test))
                        events.append(dict(Action='pass', Package='fixture'))
                        path.write_text(''.join(json.dumps(e) + '\n' for e in events))
                        self.assertEqual(evidence.summarize(path, required, suite)['valid'], outcome == 'pass')
        for name in evidence.HELM_METADATA_REQUIRED['native-os-interactions']:
            self.assertIn(name, migration.REQUIRED)

    def test_production_source_has_no_full_object_or_cli_path(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'internal/kube/helm_releases.go').read_text()
        for forbidden in ('exec.Command', '.dynamic.', '.typed.', 'GetObject(',
                          '.Post(', '.Patch(', '.Delete(', 'BearerToken', 'Impersonate'):
            self.assertNotIn(forbidden, source)
        self.assertIn('req.Header.Set("Accept", helmrelease.Accept)', source)
        self.assertIn('b.config.RateLimiter.Wait(ctx)', source)
        self.assertIn('b.metricsHTTP.Do(req)', source)
        self.assertIn('io.LimitReader', source)
        self.assertIn('helmrelease.ReadTimeout', source)
        contract = (root / 'internal/helmrelease/metadata.go').read_text()
        self.assertIn('application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1', contract)
        self.assertNotIn('application/json;q=', contract)


if __name__ == '__main__':
    unittest.main()
