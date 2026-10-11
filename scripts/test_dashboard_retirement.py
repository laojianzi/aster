"""The scoped lifecycle PR must not retire the unfinished sign-out branch."""
import copy
import unittest
from retire_completed_oidc import APPROVED, DEPENDENCY_SCOPES, REQUIRED, REPO, candidate, select_scope, workflows_pass


class DashboardRetirementTests(unittest.TestCase):
    def test_dashboard_scope_requires_exact_merged_inactive_reachable_tip(self):
        number, branch, head, tip = 40, APPROVED[40], 'a'*40, 'b'*40
        self.assertEqual(branch, 'fix/dependency-dashboard-autoclose')
        self.assertIn(number, DEPENDENCY_SCOPES)
        pr = dict(number=number, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=REPO)),
                  base=dict(ref='main', repo=dict(full_name=REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(select_scope([pr], head), number)
        self.assertEqual(candidate(pr, head, refs, [], lambda _: True, number=number, branch=branch), tip)
        self.assertIsNone(candidate(pr, head, {}, [], lambda _: True, number=number, branch=branch))
        for mode in ['open', 'moved', 'protected', 'active', 'unreachable', 'signout']:
            with self.subTest(mode=mode):
                p, r = copy.deepcopy(pr), copy.deepcopy(refs)
                if mode == 'open': p['state'] = 'open'
                if mode == 'moved': r[branch]['commit']['sha'] = 'c'*40
                if mode == 'protected': r[branch]['protected'] = True
                if mode == 'signout': p['head']['ref'] = 'feat/native-oidc-signout'
                with self.assertRaises(ValueError):
                    candidate(p, head, r, [pr] if mode == 'active' else [],
                              lambda _: mode != 'unreachable', number=number, branch=branch)
        runs = [dict(name=n, id=i, status='completed', conclusion='success', event='push',
                     head_branch='main', head_sha=head) for i, n in enumerate(REQUIRED)]
        self.assertFalse(workflows_pass(runs, head, extra=('Renovate configuration',)))
        runs.append(dict(runs[0], id=100, name='Renovate configuration'))
        self.assertTrue(workflows_pass(runs, head, extra=('Renovate configuration',)))
        runs.append(dict(runs[-1], id=101, conclusion='failure'))
        self.assertFalse(workflows_pass(runs, head, extra=('Renovate configuration',)))


if __name__ == '__main__': unittest.main()
