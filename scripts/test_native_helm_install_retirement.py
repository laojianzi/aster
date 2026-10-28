"""Offline safeguards for actual PR #124; these fixtures perform no remote writes."""
import copy
from pathlib import Path
import tempfile
import unittest

import retire_completed_oidc as retirement
from test_retirement_outcomes import Fixture

NUMBER = 124
ISSUE = 123
BRANCH = 'feat/native-helm-install'
RECEIPT = 'refs/tags/maintenance/completed-pr-124'


class NativeInstallFixture(Fixture):
    def __init__(self, root):
        super().__init__(root)
        self.branch = BRANCH
        self.receipt = RECEIPT
        self.pr = dict(number=NUMBER, state='closed', merged=True, merge_commit_sha=self.head,
                       head=dict(ref=BRANCH, sha=self.tip, repo=dict(full_name=retirement.REPO)),
                       base=dict(ref='main', repo=dict(full_name=retirement.REPO)))
        self.branches = {BRANCH: dict(name=BRANCH, protected=False, commit=dict(sha=self.tip))}

    def api(self, path):
        # Intercept every PR route: the legacy pulls/25 alias must not return
        # PR #124 and falsely qualify an ambiguous or unrelated completion.
        if path.startswith('pulls/'):
            return copy.deepcopy(self.pr) if path == 'pulls/124' else {}
        return super().api(path)


class NativeHelmInstallRetirementTests(unittest.TestCase):
    def fixture(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        return NativeInstallFixture(tmp.name)

    def test_exact_registration_and_missing_branch_never_claims_new_retirement(self):
        f = self.fixture()
        self.assertEqual(retirement.APPROVED[NUMBER], BRANCH)
        self.assertNotIn(ISSUE, retirement.APPROVED)
        self.assertNotIn(NUMBER, retirement.DEPENDENCY_SCOPES)
        self.assertEqual(retirement.APPROVED[117], 'fix/terminal-preparation-tmpdir')
        self.assertEqual(retirement.APPROVED[122], 'fix/terminal-preparation-tmpdir')
        self.assertEqual(retirement.APPROVED[114], 'feat/helm-install-recovery')
        self.assertEqual(f.api('pulls/124'), f.pr)
        for number in (25, 114, 117, 122, ISSUE):
            self.assertEqual(f.api('pulls/' + str(number)), {})
        self.assertEqual(retirement.select_scope([f.pr], f.head), NUMBER)
        self.assertIsNone(retirement.select_scope([dict(f.pr, number=ISSUE)], f.head))
        self.assertIsNone(retirement.select_scope([f.pr], 'c' * 40))
        self.assertEqual(retirement.candidate(f.pr, f.head, f.branches, [], lambda _: True,
                                             number=NUMBER, branch=BRANCH), f.tip)
        self.assertIsNone(retirement.candidate(f.pr, f.head, {}, [], lambda _: True,
                                              number=NUMBER, branch=BRANCH))
        f.branches.clear()
        report = {'status': 'not-applied'}
        f.run(report)
        self.assertEqual(report['pr'], NUMBER)
        self.assertEqual(report['status'], 'already-absent')
        self.assertFalse(f.pushes)
        self.assertIsNone(f.tag)
        self.assertFalse(any(command[0] == 'bundle' for command in f.commands))

    def test_issue_wrong_pr_and_incomplete_or_unsafe_branch_are_rejected(self):
        f = self.fixture()
        for mode in ('issue-number', 'previous-pr', 'wrong-pr-metadata', 'open', 'unmerged',
                     'wrong-merge', 'wrong-branch', 'wrong-base', 'foreign-head', 'foreign-base',
                     'invalid-tip', 'moved', 'protected', 'unreachable', 'active-head', 'active-base'):
            with self.subTest(mode=mode):
                current, refs, active = copy.deepcopy(f.pr), copy.deepcopy(f.branches), []
                number = NUMBER
                if mode == 'issue-number': number = ISSUE
                if mode == 'previous-pr': number = 122
                if mode == 'wrong-pr-metadata': current['number'] = ISSUE
                if mode == 'open': current['state'] = 'open'
                if mode == 'unmerged': current['merged'] = False
                if mode == 'wrong-merge': current['merge_commit_sha'] = 'c' * 40
                if mode == 'wrong-branch': current['head']['ref'] = 'feat/helm-install-recovery'
                if mode == 'wrong-base': current['base']['ref'] = 'other'
                if mode == 'foreign-head': current['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'foreign-base': current['base']['repo']['full_name'] = 'foreign/repo'
                if mode == 'invalid-tip': current['head']['sha'] = 'invalid'
                if mode == 'moved': refs[BRANCH]['commit']['sha'] = 'c' * 40
                if mode == 'protected': refs[BRANCH]['protected'] = True
                if mode in ('active-head', 'active-base'):
                    linked = copy.deepcopy(f.pr)
                    linked.update(state='open', merged=False)
                    linked.pop('number')
                    linked['head']['ref'], linked['base']['ref'] = 'other-head', 'other-base'
                    linked[mode.removeprefix('active-')]['ref'] = BRANCH
                    active = [linked]
                with self.assertRaises(ValueError):
                    retirement.candidate(current, f.head, refs, active,
                                         lambda _: mode != 'unreachable', number=number, branch=BRANCH)

    def test_all_four_latest_actual_main_gates_block_archive_and_write_until_success(self):
        f = self.fixture()
        self.assertEqual(retirement.REQUIRED,
                         ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification'))
        current = copy.deepcopy(f.runs)
        older_main = copy.deepcopy(current)
        for item in older_main:
            item.update(id=item['id'] + 1000, head_sha='c' * 40)
        self.assertTrue(retirement.workflows_pass(older_main + current, f.head))
        for index in range(len(current)):
            for mode in ('missing', 'failed', 'running', 'wrong-sha', 'pr-event', 'wrong-branch', 'newer-failed'):
                with self.subTest(gate=current[index]['name'], mode=mode):
                    changed = copy.deepcopy(current)
                    item = changed[index]
                    if mode == 'missing': changed.pop(index)
                    if mode == 'failed': item['conclusion'] = 'failure'
                    if mode == 'running': item['status'] = 'in_progress'
                    if mode == 'wrong-sha': item['head_sha'] = 'c' * 40
                    if mode == 'pr-event': item['event'] = 'pull_request'
                    if mode == 'wrong-branch': item['head_branch'] = BRANCH
                    if mode == 'newer-failed':
                        newer = copy.deepcopy(item)
                        newer.update(id=2000, conclusion='failure')
                        changed.append(newer)
                    blocked = self.fixture()
                    blocked.runs = older_main + changed
                    self.assertFalse(retirement.workflows_pass(blocked.runs, blocked.head))
                    report = {'status': 'not-applied'}
                    blocked.run(report)
                    self.assertEqual(report['pr'], NUMBER)
                    self.assertEqual(report['status'], 'waiting-for-verification')
                    self.assertFalse(blocked.pushes)
                    self.assertIn(BRANCH, blocked.branches)
                    self.assertIsNone(blocked.tag)
                    self.assertFalse(any(command[0] == 'bundle' for command in blocked.commands))

    def test_state_machine_bundles_before_atomic_exact_tip_and_receipt_124_write(self):
        f = self.fixture()
        report = {'status': 'not-applied'}
        f.run(report)
        self.assertEqual(report['status'], 'applied')
        self.assertEqual(report['phase'], 'complete')
        self.assertEqual(report['pr'], NUMBER)
        self.assertEqual(report['branch'], BRANCH)
        self.assertEqual(report['head'], f.head)
        self.assertEqual(report['expected_head'], f.tip)
        self.assertEqual(report['receipt'], RECEIPT)
        self.assertEqual(report['confirmation'], dict(main_matches=True, gates_passed=True,
                                                      branch_absent=True, receipt_matches=True))
        self.assertEqual(f.pushes, [('push', '--atomic', '--porcelain',
                                   '--force-with-lease=refs/heads/' + BRANCH + ':' + f.tip,
                                   '--force-with-lease=' + RECEIPT + ':', retirement.REMOTE,
                                   ':refs/heads/' + BRANCH, f.head + ':' + RECEIPT)])
        self.assertFalse(any('completed-pr-123' in arg or 'completed-pr-25' in arg
                             for command in f.commands for arg in command))
        self.assertNotIn(BRANCH, f.branches)
        self.assertEqual(f.tag, f.head)
        archive = next(i for i, command in enumerate(f.commands) if command[0] == 'bundle')
        write = next(i for i, command in enumerate(f.commands) if command[0] == 'push')
        self.assertLess(archive, write)
        self.assertLess(report['phases'].index('archive'), report['phases'].index('atomic-write'))
        self.assertEqual((Path(f.root) / 'artifacts/completed-branch/before.bundle').read_bytes(),
                         b'original fixture archive')


if __name__ == '__main__':
    unittest.main()
