"""Boolean collection evidence is additive and omissions are never passes."""
import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import retire_completed_oidc as retirement
import run_migration_tests as migration
import test_evidence as evidence


class BooleanCollectionsScopeTests(unittest.TestCase):
    def test_retirement_requires_exact_completed_inactive_reachable_pr(self):
        number, branch, head, tip = 66, 'feat/native-schema-boolean-collections', 'a' * 40, 'b' * 40
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(65, retirement.APPROVED)
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
                if mode == 'wrong-branch': p['head']['ref'] = retirement.APPROVED[64]
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
                if mode == 'issue': n = 65
                if mode == 'previous-pr': n = 64
                with self.assertRaises(ValueError):
                    retirement.candidate(p, head, branches, active, lambda _: mode != 'unreachable',
                                         number=n, branch=branch)

    def test_migration_keeps_new_and_old_native_contracts(self):
        for name in evidence.BOOLEAN_COLLECTIONS_REQUIRED['native-os-interactions']:
            self.assertIn(name, migration.REQUIRED)
            self.assertNotIn(name, migration.TERMINAL_REQUIRED)
        for previous in (evidence.INTEGER_MAP_REQUIRED, evidence.INTEGER_SET_REQUIRED,
                         evidence.MANIFEST_CLIPBOARD_REQUIRED):
            for name in previous['native-os-interactions']:
                self.assertIn(name, migration.REQUIRED)
        self.assertEqual(len(migration.REQUIRED), len(set(migration.REQUIRED)))
        self.assertIn('TestTerminalNativeMyGo048WheelRoutingAndClose', migration.TERMINAL_REQUIRED)
        self.assertEqual(retirement.REQUIRED,
                         ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification'))

    def test_suite_union_retains_every_previous_name(self):
        for suite, names in evidence.BOOLEAN_COLLECTIONS_REQUIRED.items():
            got = evidence.required_for_suite(suite, ['TestOldContract', names[0]])
            self.assertEqual(got[0], 'TestOldContract')
            self.assertEqual(set(got), {'TestOldContract', *names,
                             *evidence.INTEGER_MAP_REQUIRED.get(suite, ()),
                             *evidence.INTEGER_SET_REQUIRED.get(suite, ()),
                             *evidence.MANIFEST_CLIPBOARD_REQUIRED.get(suite, ()), *evidence.HELM_RENDER_REQUIRED.get(suite, ()), *evidence.HELM_METADATA_REQUIRED.get(suite, ())})
            self.assertEqual(len(got), len(set(got)))
        self.assertEqual(evidence.required_for_suite('another-suite', ['TestOldContract']),
                         ['TestOldContract'])

    def test_cli_rejects_each_missing_new_name_and_old_scope(self):
        for suite, names in evidence.BOOLEAN_COLLECTIONS_REQUIRED.items():
            required = evidence.required_for_suite(suite, ['TestOldContract'])
            excluded_cases = [*names, 'TestOldContract',
                              evidence.INTEGER_MAP_REQUIRED[suite][0],
                              evidence.INTEGER_SET_REQUIRED[suite][0],
                              evidence.MANIFEST_CLIPBOARD_REQUIRED[suite][0], None]
            for excluded in excluded_cases:
                with self.subTest(suite=suite, excluded=excluded), tempfile.TemporaryDirectory() as temp:
                    path = Path(temp) / 'tests.jsonl'
                    events = []
                    for name in required:
                        if name != excluded:
                            events.extend({'Action': action, 'Package': 'fixture', 'Test': name}
                                          for action in ('run', 'pass'))
                    events.append({'Action': 'pass', 'Package': 'fixture'})
                    path.write_text(''.join(json.dumps(e) + '\n' for e in events), encoding='utf-8')
                    result = subprocess.run([sys.executable, str(Path(evidence.__file__)),
                                             str(path), '--suite', suite, '--require', 'TestOldContract'],
                                            capture_output=True, text=True, timeout=10)
                    report = json.loads(result.stdout)
                    self.assertEqual(result.returncode, 0 if excluded is None else 1)
                    self.assertEqual(report['missing_required_tests'], [] if excluded is None else [excluded])
                    self.assertEqual(json.loads(path.with_name('summary.json').read_text()), report)


if __name__ == '__main__':
    unittest.main()
