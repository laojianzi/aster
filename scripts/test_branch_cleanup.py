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
