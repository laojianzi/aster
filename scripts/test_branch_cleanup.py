import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import branch_cleanup as cleanup
from check_mygo_migration import snapshot


class CleanupTests(unittest.TestCase):
    def setUp(self):
        self.plan = json.loads((cleanup.ROOT / cleanup.PLAN_PATH).read_text())

    def test_actual_plan_contains_only_reviewed_unique_refs(self):
        entries = cleanup.validate_plan(self.plan)
        self.assertEqual(len(entries), 16)
        self.assertEqual(sum(e['mode'] == 'archive-superseded' for e in entries), 3)

    def test_invalid_default_duplicates_and_ref_injection_rejected(self):
        for value in ('main', '../main', '--delete', 'feat/x..y', 'feat//x', 'feat/x.lock', 'feat/x\nmain'):
            plan = copy.deepcopy(self.plan)
            plan['branches'][0]['name'] = value
            with self.subTest(value=value), self.assertRaises(ValueError):
                cleanup.validate_plan(plan)
        plan = copy.deepcopy(self.plan)
        plan['branches'].append(plan['branches'][0])
        with self.assertRaises(ValueError):
            cleanup.validate_plan(plan)

    def test_changed_protected_active_or_unmerged_ref_blocks_whole_plan(self):
        e = next(x for x in self.plan['branches'] if x['mode'] == 'merged')
        good = {e['name']: {'commit': {'sha': e['sha']}, 'protected': False}}
        self.assertEqual(cleanup.classify([e], good, [], lambda _: True), [e])
        for cause in ('changed', 'protected', 'active-head', 'active-base', 'unmerged'):
            branches = copy.deepcopy(good)
            prs = []
            if cause == 'changed':
                branches[e['name']]['commit']['sha'] = '0' * 40
            if cause == 'protected':
                branches[e['name']]['protected'] = True
            if cause.startswith('active'):
                pr = {s: {'ref': 'main', 'repo': {'full_name': cleanup.REPO}} for s in ('head', 'base')}
                pr[cause.split('-')[1]]['ref'] = e['name']
                prs = [pr]
            with self.subTest(cause=cause), self.assertRaises(ValueError):
                cleanup.classify([e], branches, prs, lambda _: cause != 'unmerged')
        self.assertEqual(cleanup.classify([e], {}, [], lambda _: True), [])

    def test_atomic_push_leases_prevent_partial_cleanup(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            bare, local = root / 'remote.git', root / 'local'
            env = dict(os.environ, GIT_AUTHOR_NAME='Cleanup test', GIT_AUTHOR_EMAIL='test@example.invalid',
                       GIT_COMMITTER_NAME='Cleanup test', GIT_COMMITTER_EMAIL='test@example.invalid')

            def git(*args, cwd=None, check=True):
                return subprocess.run(['git', *map(str, args)], cwd=cwd, env=env,
                                      capture_output=True, text=True, check=check)

            git('init', '--bare', bare)
            git('init', '-b', 'main', local)
            (local / 'file').write_text('base')
            git('add', '.', cwd=local); git('commit', '-m', 'base', cwd=local)
            head = git('rev-parse', 'HEAD', cwd=local).stdout.strip()
            git('push', bare, 'HEAD:refs/heads/main', 'HEAD:refs/heads/merged', cwd=local)
            (local / 'file').write_text('obsolete')
            git('commit', '-am', 'obsolete', cwd=local)
            obsolete = git('rev-parse', 'HEAD', cwd=local).stdout.strip()
            git('push', bare, 'HEAD:refs/heads/obsolete', cwd=local)
            entries = [{'name': 'merged', 'sha': head, 'mode': 'merged'},
                       {'name': 'obsolete', 'sha': obsolete, 'mode': 'archive-superseded'}]
            # Race a branch update against the reviewed SHA. No ref may change.
            git('--git-dir=' + str(bare), 'update-ref', 'refs/heads/merged', obsolete)
            with patch.object(cleanup, 'REMOTE', str(bare)):
                result = git(*cleanup.push_arguments(entries, head), cwd=local, check=False)
                self.assertNotEqual(result.returncode, 0)
                for name in ('merged', 'obsolete'):
                    git('--git-dir=' + str(bare), 'show-ref', '--verify', 'refs/heads/' + name)
                self.assertNotEqual(git('--git-dir=' + str(bare), 'show-ref', '--verify', cleanup.RECEIPT, check=False).returncode, 0)
                git('--git-dir=' + str(bare), 'update-ref', 'refs/heads/merged', head)
                git(*cleanup.push_arguments(entries, head), cwd=local)
            for name in ('merged', 'obsolete'):
                self.assertNotEqual(git('--git-dir=' + str(bare), 'show-ref', '--verify', 'refs/heads/' + name, check=False).returncode, 0)
            self.assertEqual(git('--git-dir=' + str(bare), 'rev-parse', cleanup.ARCHIVE + 'obsolete').stdout.strip(), obsolete)
            self.assertEqual(git('--git-dir=' + str(bare), 'rev-parse', cleanup.RECEIPT).stdout.strip(), head)
            self.assertEqual(git('--git-dir=' + str(bare), 'rev-parse', 'refs/heads/main').stdout.strip(), head)

    def test_squash_merge_requires_exact_reviewed_pr_and_archives_original(self):
        entry = {'name': 'feat/squashed', 'sha': '1' * 40,
                 'mode': 'merged', 'reason': 'PR #1'}
        branches = {entry['name']: {'commit': {'sha': entry['sha']}, 'protected': False}}
        pr = {'number': 1, 'merged': True, 'state': 'closed',
              'merge_commit_sha': '2' * 40,
              'head': {'ref': entry['name'], 'sha': entry['sha'], 'repo': {'full_name': cleanup.REPO}},
              'base': {'ref': 'main', 'repo': {'full_name': cleanup.REPO}}}
        ancestor = lambda sha: sha == '2' * 40
        result = cleanup.classify([entry], branches, [], ancestor, lambda number: pr)
        self.assertTrue(result[0]['archive'])
        self.assertEqual(result[0]['integration_commit'], '2' * 40)
        self.assertNotIn('archive', entry)  # Reviewed input never mutates.
        self.assertIn('1' * 40 + ':' + cleanup.ARCHIVE + entry['name'],
                      cleanup.push_arguments(result, '3' * 40))
        self.assertEqual(cleanup.classify(result, branches, [], ancestor, lambda _: pr), result)
        for field, value in [('merged', False), ('state', 'open'), ('number', 9),
                             ('merge_commit_sha', None), ('merge_commit_sha', '4' * 40)]:
            bad = copy.deepcopy(pr); bad[field] = value
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                cleanup.classify([entry], branches, [], ancestor, lambda _: bad)
        for side, field, value in [('head', 'ref', 'feat/other'), ('head', 'sha', '9' * 40),
                                   ('head', 'repo', None), ('head', 'repo', {'full_name': 'other/fork'}),
                                   ('base', 'ref', 'other'), ('base', 'repo', {'full_name': 'other/fork'})]:
            bad = copy.deepcopy(pr); bad[side][field] = value
            with self.subTest(side=side, field=field), self.assertRaises(ValueError):
                cleanup.classify([entry], branches, [], ancestor, lambda _: bad)
        # A changed remote tip or an active PR still blocks the entire plan.
        changed = copy.deepcopy(branches); changed[entry['name']]['commit']['sha'] = '9' * 40
        for refs, active in [(changed, []), (branches, [pr])]:
            with self.assertRaises(ValueError):
                cleanup.classify([entry], refs, active, ancestor, lambda _: pr)

    def test_reviewed_pr_number_never_comes_from_arbitrary_reason(self):
        for reason in ('', 'PR #0', 'PR #1/../../secrets', 'PR #1\n', 'other PR #1'):
            with self.subTest(reason=reason), self.assertRaises(ValueError):
                cleanup.merged_pr_number({'reason': reason})
        self.assertEqual(cleanup.merged_pr_number({'reason': 'completed migration PR #23'}), 23)

    def test_real_squash_history_is_archived_with_atomic_ref_update(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); bare = root / 'remote.git'; local = root / 'local'
            env = dict(os.environ, GIT_AUTHOR_NAME='Cleanup test', GIT_AUTHOR_EMAIL='test@example.invalid',
                       GIT_COMMITTER_NAME='Cleanup test', GIT_COMMITTER_EMAIL='test@example.invalid')
            def git(*args, check=True):
                return subprocess.run(['git', *map(str, args)], cwd=local if local.exists() else root,
                                      env=env, capture_output=True, text=True, check=check)
            git('init', '--bare', bare); git('init', '-b', 'main', local)
            (local / 'file').write_text('base'); git('add', '.'); git('commit', '-m', 'base')
            git('switch', '-c', 'feat/squashed')
            (local / 'file').write_text('work'); git('commit', '-am', 'work')
            original = git('rev-parse', 'HEAD').stdout.strip()
            git('switch', 'main'); git('merge', '--squash', 'feat/squashed'); git('commit', '-m', 'integrated')
            merged = git('rev-parse', 'HEAD').stdout.strip()
            git('push', bare, 'main', 'feat/squashed')
            entry = {'name': 'feat/squashed', 'sha': original, 'mode': 'merged', 'reason': 'PR #1'}
            pr = {'number': 1, 'merged': True, 'state': 'closed', 'merge_commit_sha': merged,
                  'head': {'ref': entry['name'], 'sha': original, 'repo': {'full_name': cleanup.REPO}},
                  'base': {'ref': 'main', 'repo': {'full_name': cleanup.REPO}}}
            ancestor = lambda sha: git('merge-base', '--is-ancestor', sha, merged, check=False).returncode == 0
            self.assertFalse(ancestor(original)); self.assertTrue(ancestor(merged))
            ready = cleanup.classify([entry], {entry['name']: {'commit': {'sha': original}, 'protected': False}},
                                     [], ancestor, lambda _: pr)
            with patch.object(cleanup, 'REMOTE', str(bare)):
                git(*cleanup.push_arguments(ready, merged))
            self.assertEqual(git('--git-dir=' + str(bare), 'rev-parse', cleanup.ARCHIVE + entry['name']).stdout.strip(), original)
            self.assertNotEqual(git('--git-dir=' + str(bare), 'show-ref', '--verify', 'refs/heads/feat/squashed', check=False).returncode, 0)
            self.assertEqual(git('--git-dir=' + str(bare), 'rev-parse', 'refs/heads/main').stdout.strip(), merged)

    def test_migration_snapshot_tracks_generated_source_changes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            generated = root / 'internal/nativeterm/upstream/a.go'
            generated.parent.mkdir(parents=True)
            generated.write_text('package upstream\n')
            before = snapshot(root)
            generated.write_text('package changed\n')
            self.assertNotEqual(snapshot(root), before)


if __name__ == '__main__':
    unittest.main()
