"""PR #122 may retire its reused branch without qualifying failed PR #117."""
import copy
from pathlib import Path
import tempfile
import unittest

import retire_completed_oidc as retirement
from test_retirement_outcomes import Fixture

NUMBER = 122
ISSUE = 121
BRANCH = 'fix/terminal-preparation-tmpdir'
OLD_MAIN = '54af1534cbbb0b31306093c82380bedb282f100c'
OLD_TIP = '0294ca564c958f18bab2a94c9ed860e0d1768fb8'


def pr(number, main, tip):
    return dict(number=number, state='closed', merged=True, merge_commit_sha=main,
                head=dict(ref=BRANCH, sha=tip, repo=dict(full_name=retirement.REPO)),
                base=dict(ref='main', repo=dict(full_name=retirement.REPO)))


class ContinuationFixture(Fixture):
    def __init__(self, root):
        super().__init__(root)
        self.branch = BRANCH
        self.receipt = 'refs/tags/maintenance/completed-pr-122'
        self.pr = pr(NUMBER, self.head, self.tip)
        self.previous = pr(117, OLD_MAIN, OLD_TIP)
        self.branches = {BRANCH: dict(name=BRANCH, protected=False, commit=dict(sha=self.tip))}

    def api(self, path):
        # Intercept EVERY PR route; the old fixture's pulls/25 alias must never
        # return this new PR and create a false ambiguous completion match.
        if path.startswith('pulls/'):
            return copy.deepcopy({'pulls/122': self.pr, 'pulls/117': self.previous}.get(path, {}))
        return super().api(path)


class NativeScreenshotRetirementTests(unittest.TestCase):
    def fixture(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        return ContinuationFixture(tmp.name)

    def test_only_actual_122_selects_the_reused_branch_at_its_own_merge(self):
        f = self.fixture()
        self.assertEqual(retirement.APPROVED[NUMBER], BRANCH)
        self.assertEqual(retirement.APPROVED[117], BRANCH)
        self.assertNotIn(ISSUE, retirement.APPROVED)
        self.assertNotIn(NUMBER, retirement.DEPENDENCY_SCOPES)
        self.assertEqual(f.api('pulls/25'), {})
        self.assertEqual(f.api('pulls/121'), {})
        self.assertEqual(retirement.select_scope([f.previous, f.pr], f.head), NUMBER)
        self.assertIsNone(retirement.select_scope([f.previous], f.head))
        self.assertEqual(retirement.select_scope([f.previous, f.pr], OLD_MAIN), 117)
        self.assertEqual(retirement.candidate(f.pr, f.head, f.branches, [], lambda _: True,
                                             number=NUMBER, branch=BRANCH), f.tip)
        self.assertIsNone(retirement.candidate(f.pr, f.head, {}, [], lambda _: True,
                                              number=NUMBER, branch=BRANCH))

    def test_issue_predecessor_old_tip_and_other_unsafe_scopes_are_rejected(self):
        f = self.fixture()
        for mode in ('issue-number', 'predecessor-number', 'predecessor-metadata', 'old-tip',
                     'open', 'unmerged', 'wrong-merge', 'wrong-branch', 'wrong-base',
                     'foreign-head', 'foreign-base', 'invalid-tip', 'moved', 'protected',
                     'unreachable', 'active-head', 'active-base'):
            with self.subTest(mode=mode):
                current, refs, active = copy.deepcopy(f.pr), copy.deepcopy(f.branches), []
                number = NUMBER
                if mode == 'issue-number': number = ISSUE
                if mode == 'predecessor-number': number = 117
                if mode == 'predecessor-metadata': current = copy.deepcopy(f.previous)
                if mode == 'old-tip': refs[BRANCH]['commit']['sha'] = OLD_TIP
                if mode == 'open': current['state'] = 'open'
                if mode == 'unmerged': current['merged'] = False
                if mode == 'wrong-merge': current['merge_commit_sha'] = OLD_MAIN
                if mode == 'wrong-branch': current['head']['ref'] = 'fix/native-screenshot-encoding'
                if mode == 'wrong-base': current['base']['ref'] = 'other'
                if mode == 'foreign-head': current['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'foreign-base': current['base']['repo']['full_name'] = 'foreign/repo'
                if mode == 'invalid-tip': current['head']['sha'] = 'invalid'
                if mode == 'moved': refs[BRANCH]['commit']['sha'] = 'd' * 40
                if mode == 'protected': refs[BRANCH]['protected'] = True
                if mode in ('active-head', 'active-base'):
                    linked = copy.deepcopy(f.pr)
                    linked['head']['ref'], linked['base']['ref'] = 'other-head', 'other-base'
                    linked[mode.removeprefix('active-')]['ref'] = BRANCH
                    active = [linked]
                with self.assertRaises(ValueError):
                    retirement.candidate(current, f.head, refs, active,
                                         lambda _: mode != 'unreachable', number=number, branch=BRANCH)

    def test_all_latest_new_main_gates_are_required_and_old_failure_remains_failed(self):
        f = self.fixture()
        self.assertEqual(retirement.REQUIRED,
                         ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification'))
        current = copy.deepcopy(f.runs)
        old = copy.deepcopy(f.runs)
        for item in old:
            item.update(id=item['id'] + 1000, head_sha=OLD_MAIN)
        old[0]['conclusion'] = 'failure'
        self.assertTrue(retirement.workflows_pass(old + current, f.head))
        self.assertFalse(retirement.workflows_pass(old + current, OLD_MAIN))
        f.runs = old + current
        f.head = f.main = OLD_MAIN
        historical = {'status': 'not-applied'}
        f.run(historical)
        self.assertEqual(historical['pr'], 117)
        self.assertEqual(historical['status'], 'waiting-for-verification')
        self.assertFalse(f.pushes)
        self.assertFalse(any(c[0] == 'bundle' for c in f.commands))
        for index in range(len(current)):
            for mode in ('missing', 'failed', 'running', 'wrong-sha', 'pr-event', 'wrong-branch', 'newer-failed'):
                with self.subTest(gate=current[index]['name'], mode=mode):
                    changed = copy.deepcopy(current)
                    item = changed[index]
                    if mode == 'missing': changed.pop(index)
                    if mode == 'failed': item['conclusion'] = 'failure'
                    if mode == 'running': item['status'] = 'in_progress'
                    if mode == 'wrong-sha': item['head_sha'] = OLD_MAIN
                    if mode == 'pr-event': item['event'] = 'pull_request'
                    if mode == 'wrong-branch': item['head_branch'] = BRANCH
                    if mode == 'newer-failed':
                        newer = copy.deepcopy(item)
                        newer.update(id=2000, conclusion='failure')
                        changed.append(newer)
                    self.assertFalse(retirement.workflows_pass(old + changed, current[0]['head_sha']))

    def test_real_state_machine_archives_then_leases_only_new_tip_and_receipt_122(self):
        f = self.fixture()
        report = {'status': 'not-applied'}
        f.run(report)
        self.assertEqual(report['status'], 'applied')
        self.assertEqual(report['pr'], NUMBER)
        self.assertEqual(report['head'], f.head)
        self.assertEqual(report['expected_head'], f.tip)
        self.assertEqual(report['receipt'], 'refs/tags/maintenance/completed-pr-122')
        self.assertEqual(report['confirmation'], dict(main_matches=True, gates_passed=True,
                                                      branch_absent=True, receipt_matches=True))
        self.assertEqual(len(f.pushes), 1)
        push = f.pushes[0]
        self.assertIn('--atomic', push)
        self.assertIn('--force-with-lease=refs/heads/' + BRANCH + ':' + f.tip, push)
        self.assertIn('--force-with-lease=refs/tags/maintenance/completed-pr-122:', push)
        self.assertIn(f.head + ':refs/tags/maintenance/completed-pr-122', push)
        self.assertFalse(any('completed-pr-117' in arg for command in f.commands for arg in command))
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
