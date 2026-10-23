"""Only the real #104 completion can retire the exact inactive install branch."""
import copy
import unittest

import retire_completed_oidc as retirement


class HelmInstallRetirementTests(unittest.TestCase):
    def fixture(self):
        number, branch = 104, 'feat/helm-install-preparation'
        main, tip = 'a' * 40, 'b' * 40
        pr = dict(number=number, state='closed', merged=True, merge_commit_sha=main,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=retirement.REPO)),
                  base=dict(ref='main', repo=dict(full_name=retirement.REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        return number, branch, main, tip, pr, refs

    def test_exact_registration_and_qualified_candidate(self):
        number, branch, main, tip, pr, refs = self.fixture()
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(103, retirement.APPROVED)
        self.assertNotIn(number, retirement.DEPENDENCY_SCOPES)
        self.assertEqual(retirement.APPROVED[102], 'feat/native-helm-journal-archive')
        self.assertEqual(retirement.select_scope([pr], main), number)
        self.assertEqual(retirement.candidate(pr, main, refs, [], lambda _: True,
                                             number=number, branch=branch), tip)
        self.assertIsNone(retirement.candidate(pr, main, {}, [], lambda _: True,
                                              number=number, branch=branch))

    def test_incomplete_moved_active_or_foreign_work_is_not_deleted(self):
        number, branch, main, tip, original, original_refs = self.fixture()
        for mode in ('open', 'unmerged', 'wrong-merge', 'wrong-branch', 'wrong-base',
                     'foreign-head', 'foreign-base', 'invalid-tip', 'moved', 'protected',
                     'unreachable', 'active-head', 'active-base', 'issue-number', 'previous-pr'):
            with self.subTest(mode=mode):
                pr, refs, active = copy.deepcopy(original), copy.deepcopy(original_refs), []
                n = number
                if mode == 'open': pr['state'] = 'open'
                if mode == 'unmerged': pr['merged'] = False
                if mode == 'wrong-merge': pr['merge_commit_sha'] = 'c' * 40
                if mode == 'wrong-branch': pr['head']['ref'] = retirement.APPROVED[102]
                if mode == 'wrong-base': pr['base']['ref'] = 'other'
                if mode == 'foreign-head': pr['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'foreign-base': pr['base']['repo']['full_name'] = 'foreign/repo'
                if mode == 'invalid-tip': pr['head']['sha'] = 'invalid'
                if mode == 'moved': refs[branch]['commit']['sha'] = 'c' * 40
                if mode == 'protected': refs[branch]['protected'] = True
                if mode == 'issue-number': n = 103
                if mode == 'previous-pr': n = 102
                if mode in ('active-head', 'active-base'):
                    p = copy.deepcopy(original)
                    p['head']['ref'], p['base']['ref'] = 'other-head', 'other-base'
                    p[mode.removeprefix('active-')]['ref'] = branch
                    active = [p]
                with self.assertRaises(ValueError):
                    retirement.candidate(pr, main, refs, active, lambda _: mode != 'unreachable',
                                         number=n, branch=branch)

    def test_all_latest_actual_main_gates_are_required(self):
        _, _, main, _, _, _ = self.fixture()
        self.assertEqual(retirement.REQUIRED,
                         ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification'))
        runs = [dict(id=i, name=name, head_sha=main, head_branch='main', event='push',
                     status='completed', conclusion='success')
                for i, name in enumerate(retirement.REQUIRED, 1)]
        self.assertTrue(retirement.workflows_pass(runs, main))
        for index in range(len(runs)):
            for mode in ('missing', 'failed', 'running', 'wrong-sha', 'pr-event', 'wrong-branch', 'newer-failed'):
                with self.subTest(gate=runs[index]['name'], mode=mode):
                    changed = copy.deepcopy(runs)
                    item = changed[index]
                    if mode == 'missing': changed.pop(index)
                    if mode == 'failed': item['conclusion'] = 'failure'
                    if mode == 'running': item['status'] = 'in_progress'
                    if mode == 'wrong-sha': item['head_sha'] = 'c' * 40
                    if mode == 'pr-event': item['event'] = 'pull_request'
                    if mode == 'wrong-branch': item['head_branch'] = 'feat/helm-install-preparation'
                    if mode == 'newer-failed':
                        newer = copy.deepcopy(item)
                        newer.update(id=100, conclusion='failure')
                        changed.append(newer)
                    self.assertFalse(retirement.workflows_pass(changed, main))


if __name__ == '__main__':
    unittest.main()
