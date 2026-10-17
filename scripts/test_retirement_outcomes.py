"""Offline failure injection for retirement effects, leases and private reports."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

import retire_completed_oidc as cleanup


class Fixture:
    def __init__(self, root):
        self.root = Path(root)
        self.head, self.tip = 'a' * 40, 'b' * 40
        self.main = self.head
        self.branch = cleanup.BRANCH
        self.receipt = 'refs/tags/maintenance/completed-pr-25'
        self.pr = dict(number=25, state='closed', merged=True, merge_commit_sha=self.head,
                       head=dict(ref=self.branch, sha=self.tip, repo=dict(full_name=cleanup.REPO)),
                       base=dict(ref='main', repo=dict(full_name=cleanup.REPO)))
        self.branches = {self.branch: dict(name=self.branch, protected=False, commit=dict(sha=self.tip))}
        self.active = []
        self.ancestor = True
        self.runs = [dict(name=n, id=i, status='completed', conclusion='success', event='push',
                          head_branch='main', head_sha=self.head) for i, n in enumerate(cleanup.REQUIRED)]
        self.tag = None
        self.raw_receipt = None
        self.mode = 'ok'
        self.commands = []
        self.after_archive = lambda: None

    def api(self, path):
        if path == 'git/ref/heads/main':
            return {'object': {'sha': self.main}}
        if path.startswith('pulls/'):
            return copy.deepcopy(self.pr) if path == 'pulls/25' else {}
        if path.startswith('actions/runs?'):
            return {'workflow_runs': copy.deepcopy(self.runs)}
        raise AssertionError(path)

    def collection(self, path):
        if path == 'branches':
            if self.mode == 'readback-error' and self.pushes:
                raise ValueError('PRIVATE_PROVIDER_RESPONSE')
            return copy.deepcopy(list(self.branches.values()))
        if path == 'pulls?state=open':
            return copy.deepcopy(self.active)
        raise AssertionError(path)

    @property
    def pushes(self):
        return [c for c in self.commands if c[0] == 'push']

    def git(self, *args, predicate=False):
        self.commands.append(args)
        if args == ('rev-parse', 'HEAD'):
            return self.head
        if args[0] == 'merge-base':
            assert args[1:] == ('--is-ancestor', self.tip, self.head) and predicate
            return self.ancestor
        if args[0] == 'ls-remote':
            assert args[1:] == ('--refs', cleanup.REMOTE, self.receipt)
            if self.raw_receipt is not None:
                return self.raw_receipt
            return self.tag + '\t' + self.receipt if self.tag is not None else ''
        if args[0] == 'bundle':
            assert args[1] == 'create' and args[3] == '--all'
            p = Path(args[2]); p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(b'original fixture archive')
            self.after_archive()
            return ''
        if args[0] == 'push':
            assert len(self.pushes) == 1, 'mutation was repeated'
            assert args == ('push', '--atomic', '--porcelain',
                            '--force-with-lease=refs/heads/' + self.branch + ':' + self.tip,
                            '--force-with-lease=' + self.receipt + ':', cleanup.REMOTE,
                            ':refs/heads/' + self.branch, self.head + ':' + self.receipt)
            if self.mode == 'rejected':
                raise ValueError('PRIVATE_TOKEN')
            if self.mode != 'receipt-only':
                del self.branches[self.branch]
            if self.mode != 'deletion-only':
                self.tag = self.head
            if self.mode == 'main-moved-after-write':
                self.main = 'c' * 40
            if self.mode == 'gates-changed-after-write':
                self.runs[0]['conclusion'] = 'failure'
            if self.mode == 'committed-response-lost':
                raise TimeoutError('PRIVATE_TOKEN in transport output')
            return ''
        raise AssertionError(args)

    def run(self, report):
        cleanup.retire_registered(report, self.head, self.api, self.collection, self.git, self.root)


class RetirementOutcomeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.f = Fixture(self.tmp.name)
        self.report = {'status': 'not-applied'}

    def assert_no_private_data(self):
        self.assertNotIn('PRIVATE_', json.dumps(self.report))

    def test_confirmed_atomic_success_keeps_archive_and_phase_order(self):
        self.f.run(self.report)
        self.assertEqual(self.report['status'], 'applied')
        self.assertEqual(len(self.f.pushes), 1)
        self.assertEqual(self.report['phase'], 'complete')
        self.assertLess(self.report['phases'].index('archive'), self.report['phases'].index('atomic-write'))
        self.assertEqual((self.f.root / 'artifacts/completed-branch/before.bundle').read_bytes(), b'original fixture archive')

    def test_committed_response_loss_is_reconciled_without_repeating_write(self):
        self.f.mode = 'committed-response-lost'
        self.f.run(self.report)
        self.assertEqual(self.report['status'], 'applied-reconciled')
        self.assertEqual(self.report['write_error_type'], 'TimeoutError')
        self.assertIn('reconcile-write', self.report['phases'])
        self.assertEqual(len(self.f.pushes), 1)
        self.assert_no_private_data()

    def test_rejected_write_remains_unknown_and_does_not_retry(self):
        self.f.mode = 'rejected'
        with self.assertRaises(ValueError):
            self.f.run(self.report)
        self.assertEqual(self.report['status'], 'outcome-unknown')
        self.assertEqual(self.report['phase'], 'reconcile-write')
        self.assertEqual(self.report['write_error_type'], 'ValueError')
        self.assertEqual(len(self.f.pushes), 1)
        self.assertIn(self.f.branch, self.f.branches)
        self.assert_no_private_data()

    def test_partial_or_unconfirmed_effects_fail(self):
        for mode in ('deletion-only', 'receipt-only', 'readback-error', 'main-moved-after-write', 'gates-changed-after-write'):
            with self.subTest(mode=mode):
                f = Fixture(self.tmp.name); f.mode = mode
                report = {'status': 'not-applied'}
                with self.assertRaises(ValueError):
                    f.run(report)
                self.assertEqual(report['status'], 'outcome-unknown')
                self.assertEqual(report['phase'], 'confirm-write')
                self.assertEqual(len(f.pushes), 1)
                self.assertNotIn('PRIVATE_', json.dumps(report))

    def test_valid_prior_receipt_requires_no_write_or_new_archive(self):
        self.f.branches.clear(); self.f.tag = self.f.head
        self.f.run(self.report)
        self.assertEqual(self.report['status'], 'already-recorded')
        self.assertFalse(self.f.pushes)
        self.assertFalse(any(c[0] == 'bundle' for c in self.f.commands))
        self.assertIn('verify-gates', self.report['phases'])

    def test_tag_alone_or_wrong_tag_does_not_authorize_cleanup(self):
        for present, value in ((True, 'a' * 40), (False, 'c' * 40)):
            with self.subTest(present=present):
                f = Fixture(self.tmp.name); f.tag = value
                if not present: f.branches.clear()
                with self.assertRaises(ValueError): f.run({'status': 'not-applied'})
                self.assertFalse(f.pushes)

    def test_ambiguous_receipt_is_rejected(self):
        for value in ('not-a-sha\t' + self.f.receipt,
                      self.f.head + '\trefs/tags/other',
                      self.f.head + '\t' + self.f.receipt + '\n' + self.f.head + '\t' + self.f.receipt):
            with self.subTest(value=value):
                f = Fixture(self.tmp.name); f.raw_receipt = value
                with self.assertRaises(ValueError): f.run({'status': 'not-applied'})
                self.assertFalse(f.pushes)

    def test_absent_without_receipt_is_not_claimed_applied(self):
        self.f.branches.clear()
        self.f.run(self.report)
        self.assertEqual(self.report['status'], 'already-absent')
        self.assertFalse(self.f.pushes)

    def test_failed_gate_cannot_be_bypassed_by_receipt(self):
        self.f.branches.clear(); self.f.tag = self.f.head
        self.f.runs[0]['conclusion'] = 'failure'
        self.f.run(self.report)
        self.assertEqual(self.report['status'], 'waiting-for-verification')
        self.assertFalse(self.f.pushes)

    def test_absent_receipt_requires_reachable_inactive_history(self):
        for active in (True, False):
            with self.subTest(active=active):
                f = Fixture(self.tmp.name); f.branches.clear(); f.tag = f.head
                if active: f.active = [f.pr]
                else: f.ancestor = False
                with self.assertRaises(ValueError): f.run({'status': 'not-applied'})
                self.assertFalse(f.pushes)

    def test_changed_preflight_cannot_write(self):
        for mode in ('main', 'tip', 'protection', 'active', 'gate', 'receipt'):
            with self.subTest(mode=mode):
                f = Fixture(self.tmp.name)
                def change():
                    if mode == 'main': f.main = 'c' * 40
                    if mode == 'tip': f.branches[f.branch]['commit']['sha'] = 'c' * 40
                    if mode == 'protection': f.branches[f.branch]['protected'] = True
                    if mode == 'active': f.active = [f.pr]
                    if mode == 'gate': f.runs[0]['conclusion'] = 'failure'
                    if mode == 'receipt': f.tag = f.head
                f.after_archive = change
                with self.assertRaises(ValueError): f.run({'status': 'not-applied'})
                self.assertFalse(f.pushes)

    def test_later_reconciliation_does_not_rewrite_original_failure(self):
        self.f.mode = 'readback-error'
        with self.assertRaises(ValueError): self.f.run(self.report)
        original = json.dumps(self.report, sort_keys=True)
        self.f.mode = 'ok'
        later = {'status': 'not-applied'}
        self.f.run(later)
        self.assertEqual(later['status'], 'already-recorded')
        self.assertEqual(json.dumps(self.report, sort_keys=True), original)
        self.assertEqual(len(self.f.pushes), 1)

    def test_unregistered_current_merge_has_no_mutation(self):
        self.f.pr['merge_commit_sha'] = 'd' * 40
        self.f.run(self.report)
        self.assertEqual(self.report['status'], 'no-registered-completion')
        self.assertFalse(self.f.pushes)

    def test_only_registered_maintenance_pr_can_retire_reused_branch(self):
        number, branch = 83, 'test/helm-uninstall-contract'
        self.assertEqual(cleanup.APPROVED[number], branch)
        self.assertEqual(cleanup.APPROVED[81], branch)
        self.assertNotIn(80, cleanup.APPROVED)
        self.assertNotIn(number, cleanup.DEPENDENCY_SCOPES)
        pr = copy.deepcopy(self.f.pr)
        pr['number'] = number
        pr['head']['ref'] = branch
        refs = {branch: dict(protected=False, commit=dict(sha=self.f.tip))}
        self.assertEqual(cleanup.select_scope([pr], self.f.head), number)
        self.assertEqual(cleanup.candidate(pr, self.f.head, refs, [], lambda _: True,
                                          number=number, branch=branch), self.f.tip)
        for mode in ('issue-number', 'open', 'unmerged', 'wrong-merge', 'wrong-branch',
                     'foreign', 'moved', 'protected', 'active', 'unreachable'):
            with self.subTest(mode=mode):
                p, branches = copy.deepcopy(pr), copy.deepcopy(refs)
                if mode == 'issue-number': p['number'] = 80
                if mode == 'open': p['state'] = 'open'
                if mode == 'unmerged': p['merged'] = False
                if mode == 'wrong-merge': p['merge_commit_sha'] = 'c' * 40
                if mode == 'wrong-branch': p['head']['ref'] = 'fix/helm-uninstall-absence'
                if mode == 'foreign': p['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'moved': branches[branch]['commit']['sha'] = 'd' * 40
                if mode == 'protected': branches[branch]['protected'] = True
                with self.assertRaises(ValueError):
                    cleanup.candidate(p, self.f.head, branches, [pr] if mode == 'active' else [],
                                      lambda _: mode != 'unreachable', number=number, branch=branch)

    def test_original_guard_and_permissions_remain(self):
        root = Path(__file__).resolve().parents[1]
        text = (root / '.github/workflows/retire-completed-oidc.yml').read_text()
        self.assertIn('contents: write', text)
        self.assertIn('actions: read', text)
        self.assertIn('pull-requests: read', text)
        self.assertNotIn('workflow_dispatch:', text)
        self.assertNotIn('write-all', text)


if __name__ == '__main__':
    unittest.main()
