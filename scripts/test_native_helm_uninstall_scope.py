"""The native flow adds evidence and admits only its reviewed API consumers."""
import copy
import hashlib
import json
from pathlib import Path
import re
import tempfile
import unittest

import helm_uninstall_contract_evidence as contract
import retire_completed_oidc as retirement
import run_migration_tests as migration
import test_evidence as evidence
from helm_native_scope import CALL_SITES, production_calls

ROOT = Path(__file__).resolve().parents[1]

# SHA-256 of the sorted unique names joined by '\n', without a final newline.
# Freeze the pre-#94 inventory independently of the live feature dictionaries:
# dropping a requirement from both its group and aggregation must still fail.
LEGACY_CONTRACT = {
    'unit-native': (828, '9400c729a1fa89886e7fc14398ad8f05bfd626c88d6dfbba0b3a0dc8033f1309'),
    'real-cluster-native': (83, '246320f2ea0955c16e084c718793bc8e0457c12ea3100e69a1b380ba28a291b6'),
}
LEGACY_AUTOMATIC = {
    'unit-native': (67, '3c57fa906fd5fe4553b27dcff7184f3601782f4ffba340ca1ee86da84eb5217a'),
    'native-os-interactions': (29, 'c2eb3576f7d6a6903b23390d47c2ea8a7aaaac4d28a15c0bfca9febb2209e671'),
    'real-cluster-native': (21, 'ba547cf52ddd3fb65c85f9fd4cb55fd8c5614f22b8da54415398a6186612dded'),
}
LEGACY_MIGRATION = (113, '19ea43baf7d832a7ddc73ad5752f4bf418308ab16dbb82a52449eba380ee5df1')


def inventory(names):
    unique = sorted(set(names))
    return len(unique), hashlib.sha256('\n'.join(unique).encode()).hexdigest()


def suite_required(suite):
    if suite in LEGACY_CONTRACT:
        return contract.required(suite)
    explicit = migration.REQUIRED if suite.startswith('migration-') else []
    return evidence.required_for_suite(suite, explicit)


def write_events(path, required, target=None, outcome='pass'):
    events = []
    for name in required:
        terminal = outcome if name == target else 'pass'
        if terminal == 'missing':
            continue
        events.append(dict(Action='run', Package='fixture', Test=name))
        if terminal != 'unfinished':
            events.append(dict(Action=terminal, Package='fixture', Test=name))
    events.append(dict(Action='pass', Package='fixture'))
    path.write_text(''.join(json.dumps(event) + '\n' for event in events), encoding='utf-8')


class NativeHelmUninstallScopeTests(unittest.TestCase):
    def test_only_actual_pr95_can_retire_the_exact_completed_inactive_branch(self):
        number, branch = 95, 'feat/native-helm-uninstall'
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(94, retirement.APPROVED)
        self.assertNotIn(number, retirement.DEPENDENCY_SCOPES)
        head, tip = 'a' * 40, 'b' * 40
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
                if mode == 'wrong-branch': p['head']['ref'] = retirement.APPROVED[93]
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
                if mode == 'issue': n = 94
                if mode == 'previous-pr': n = 93
                with self.assertRaises(ValueError):
                    retirement.candidate(p, head, branches, active, lambda _: mode != 'unreachable',
                                         number=n, branch=branch)

    def test_exact_legacy_helm_contract_inventory_is_preserved(self):
        for suite, frozen in LEGACY_CONTRACT.items():
            with self.subTest(suite=suite):
                new = set(evidence.HELM_NATIVE_UNINSTALL_REQUIRED[suite])
                required = contract.required(suite)
                self.assertEqual(len(required), len(set(required)))
                self.assertTrue(new <= set(required))
                self.assertEqual(inventory(set(required) - new), frozen)

    def test_exact_legacy_native_and_migration_inventory_is_preserved(self):
        for suite, frozen in LEGACY_AUTOMATIC.items():
            with self.subTest(suite=suite):
                new = set(evidence.HELM_NATIVE_UNINSTALL_REQUIRED[suite])
                self.assertEqual(inventory(set(evidence.required_for_suite(suite, [])) - new), frozen)
        self.assertEqual(len(migration.REQUIRED), len(set(migration.REQUIRED)))
        self.assertEqual(inventory(set(migration.REQUIRED) - set(evidence.HELM_NATIVE_UNINSTALL_NAMES)),
                         LEGACY_MIGRATION)

    def test_native_cases_are_added_to_every_applicable_suite(self):
        local = evidence.HELM_NATIVE_UNINSTALL_NAMES
        self.assertEqual(len(local), len(set(local)))
        self.assertEqual((len(evidence.HELM_NATIVE_JOURNAL_NAMES),
                          len(evidence.HELM_NATIVE_UI_NAMES),
                          len(evidence.HELM_NATIVE_LIFECYCLE_NAMES)), (12, 35, 9))
        self.assertEqual(set(evidence.HELM_NATIVE_UNINSTALL_REQUIRED), {
            'unit-native', 'native-os-interactions', 'real-cluster-native',
            'migration-checked', 'migration-production',
        })
        for suite, names in evidence.HELM_NATIVE_UNINSTALL_REQUIRED.items():
            with self.subTest(suite=suite):
                self.assertEqual(len(names), len(set(names)))
                self.assertTrue(set(local) <= set(names))
                self.assertTrue(set(names) <= set(suite_required(suite)))
                # Existing explicit workflow requirements must survive the new
                # automatic requirements too, even when supplied more than once.
                merged = evidence.required_for_suite(suite, ['TestExistingExplicitContract'] * 2)
                self.assertEqual(merged.count('TestExistingExplicitContract'), 1)
                self.assertTrue(set(names) <= set(merged))
        self.assertEqual(set(evidence.HELM_NATIVE_UNINSTALL_REQUIRED['real-cluster-native']) - set(local), {
            'TestNativeHelmUninstallAgainstRealCluster',
            'TestNativeHelmRecoveryAgainstRealCluster',
        })
        self.assertTrue(set(local) <= set(migration.REQUIRED))

    def test_every_new_name_rejects_missing_failed_skipped_and_unfinished_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'tests.jsonl'
            for suite, names in evidence.HELM_NATIVE_UNINSTALL_REQUIRED.items():
                required = suite_required(suite)
                write_events(path, required)
                self.assertTrue(evidence.summarize(path, required, suite)['valid'], suite)
                for name in names:
                    for outcome in ('missing', 'fail', 'skip', 'unfinished'):
                        with self.subTest(suite=suite, name=name, outcome=outcome):
                            write_events(path, required, name, outcome)
                            result = evidence.summarize(path, required, suite)
                            self.assertFalse(result['valid'])
                            self.assertIn(name, result['missing_required_tests'])
                            if outcome == 'fail':
                                self.assertIn('fixture:' + name, result['failed_tests'])
                            elif outcome == 'skip':
                                self.assertIn('fixture:' + name, result['skipped_tests'])
                            elif outcome == 'unfinished':
                                self.assertIn('fixture:' + name, result['unfinished_tests'])

    def test_migration_selects_complete_roots_and_validates_full_names(self):
        pattern = migration.root_test_pattern(migration.REQUIRED)
        self.assertNotIn('/', pattern)
        for name in migration.REQUIRED:
            self.assertIsNotNone(re.fullmatch(pattern, name.split('/', 1)[0]), name)
        self.assertIsNone(re.fullmatch(pattern, 'TestUnregisteredMigrationCase'))
        source = (ROOT / 'scripts/run_migration_tests.py').read_text()
        self.assertIn("'-run', root_test_pattern(required)", source)
        self.assertIn("for name in required:", source)
        self.assertIn("verify.extend(['--require', name])", source)
        self.assertIn("'-timeout=3m'", source)
        self.assertIn("'-tags=mygo_noinspector'", source)

    def test_required_roots_are_real_native_tests(self):
        sources = (
            'internal/ui/helm_uninstall_journal_test.go',
            'internal/ui/helm_uninstall_ui_test.go',
            'internal/ui/helm_uninstall_lifecycle_test.go',
            'internal/ui/helm_uninstall_e2e_test.go',
        )
        declared = set()
        for source in sources:
            text = (ROOT / source).read_text()
            declared.update(re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)', text, re.M))
        required = evidence.HELM_NATIVE_UNINSTALL_REQUIRED['real-cluster-native']
        self.assertEqual(declared, {name.split('/', 1)[0] for name in required})
        cluster = (ROOT / 'internal/ui/helm_uninstall_e2e_test.go').read_text()
        self.assertTrue(cluster.startswith('//go:build e2e\n'))

    def test_production_api_calls_are_restricted_to_the_two_admitted_files(self):
        for name, admitted in CALL_SITES.items():
            with self.subTest(api=name):
                self.assertEqual(production_calls(ROOT, name), admitted)
        for directory in ('cmd', 'internal/ui'):
            for path in (ROOT / directory).rglob('*.go'):
                if not path.name.endswith('_test.go'):
                    self.assertNotRegex(path.read_text(), r'\bhelmjournal\.Open\s*\(', str(path))

    def test_fixed_journal_has_no_alternate_directory_or_retirement_ui(self):
        helper = (ROOT / 'internal/ui/helm_uninstall_journal.go').read_text()
        self.assertIn('os.UserHomeDir()', helper)
        self.assertIn('filepath.Join(home, ".aster-helm-uninstall")', helper)
        for name in ('helm_uninstall.go', 'helm_uninstall_journal.go', 'helm_uninstall_view.go'):
            path = ROOT / 'internal/ui' / name
            text = path.read_text()
            for api in ('MkdirAll', 'MkdirTemp', 'UserConfigDir', 'UserCacheDir', 'Getenv', 'LookupEnv'):
                self.assertNotRegex(text, r'\b' + api + r'\s*\(', str(path))
            self.assertNotRegex(text, r'\bos\.(?:Remove|RemoveAll|Chmod|Rename)\s*\(', str(path))
            self.assertNotRegex(text, r'\b(?:Resolve|Purge|Replay|Resume)\s*\(', str(path))

    def test_existing_workflows_execute_and_validate_the_new_names(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        for stream, suite in (
            ('artifacts/unit/tests.jsonl', 'unit-native'),
            ('artifacts/unit/coverage/tests.jsonl', 'unit-native'),
            ('artifacts/e2e/tests.jsonl', 'real-cluster-native'),
        ):
            self.assertIn(f'python3 scripts/helm_uninstall_contract_evidence.py {stream} --suite {suite}', workflow)
        self.assertIn('scripts/test_evidence.py artifacts/tests.jsonl --suite native-os-interactions', workflow)
        self.assertIn('python -m unittest discover -s scripts', workflow)
        native = (ROOT / '.github/workflows/native-migration.yml').read_text()
        self.assertIn('python scripts/run_migration_tests.py', native)
        self.assertEqual(retirement.REQUIRED,
                         ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification'))


if __name__ == '__main__':
    unittest.main()
