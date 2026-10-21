"""Admission explanations add evidence without replacing accepted execution cases."""
import hashlib
import json
from pathlib import Path
import re
import tempfile
import unittest

import helm_journal_evidence as journal
import helm_uninstall_contract_evidence as contract
import run_migration_tests as migration
import test_evidence as evidence
from go_toolchain_policy import previous_toolchain_bytes
from helm_native_scope import CALL_SITES, production_calls

ROOT = Path(__file__).resolve().parents[1]
BASELINE_PATH = ROOT / 'scripts/fixtures/helm_journal_admission_baseline.json'
BASELINE_COMMIT = 'aa84d7adb3a3c4d19d58d7a39443e49b2eb51490'
BASELINE_SHA256 = 'a67c2846450f78c9fe8cfea87d0a90dd8d6b6470368eb8b895b9dff3e7d77109'

# Independently freeze the accepted #95 inventories. The fixture stores every
# full name for a useful diff; neither it nor the live dictionaries alone is an
# oracle. Hash sorted unique names joined by '\n', without a final newline.
BASELINE_INVENTORIES = {
    'contract/unit-native': (884, '76e515d969d1ca307a39eb1795acc599c3521527e37fef7f8a5b9b038f81337b'),
    'contract/real-cluster-native': (141, '9a310a313229d0afc2f449f296a3369bce706adaeaf7abbeb4215ecdadf3d7c0'),
    'automatic/unit-native': (123, '51aaa18464ca6554b2f0126febe6a83f885249d64e47f2e899680771b6ace504'),
    'automatic/native-os-interactions': (85, 'c536a37eb1ffb35010612697ee719261b1247e8041bd3723b81ce89168066577'),
    'automatic/real-cluster-native': (79, '5a0a97629ab624ac5462a4578cd16eeca4746c091ff3caced6c133edc71706e5'),
    'automatic/migration-checked': (56, '531ef520de015378b3604a177da5d660dab56f485346efe3a6c150b18fa7b039'),
    'automatic/migration-production': (56, '531ef520de015378b3604a177da5d660dab56f485346efe3a6c150b18fa7b039'),
    'migration': (169, 'b3b56f41f9e6d6d4533aff55198e3c5cc604b18525382362311ed8e9d049b225'),
    'journal': (8, '879048217b243403340a15baa434f30ed02592d6b39b0a57521989cb23b2fa4e'),
    'native-uninstall/local': (56, '531ef520de015378b3604a177da5d660dab56f485346efe3a6c150b18fa7b039'),
    'native-uninstall/real-cluster-native': (58, 'b4035c2291667c73c58ab85880b37f44a8ff6575493ba3abade4131c1699de30'),
}

ADMISSION_INVENTORIES = {
    'journal': (53, 'a5788f0a3a2eb5f6beda07e1663eb0eaae7e5c4e7b12c460f4703fccb06bf1d5'),
    'backend': (23, '5f49f57cf95c7c9fffa1fe994d742994bc43663dbb2d180c4f7371f591d39b46'),
    'native-local': (16, '0d4b3f30d41f68d52e09f0906ba7c2e0581ff1bda6047c5d833a239321f64a27'),
    'native-real': (17, '3f2cbfa81f1ebf3aa83804ec83c5a960171d44cbfd5871d79daf2c89f7e28b65'),
}


def inventory(names):
    unique = sorted(set(names))
    return len(unique), hashlib.sha256('\n'.join(unique).encode()).hexdigest()


def admission_names(suite):
    return set(evidence.HELM_JOURNAL_ADMISSION_REQUIRED.get(suite, ())) | set(
        contract.JOURNAL_ADMISSION_REQUIRED.get(suite, ()))


def suite_required(suite):
    if suite in contract.REQUIRED:
        return contract.required(suite)
    explicit = migration.REQUIRED if suite.startswith('migration-') else []
    return evidence.required_for_suite(suite, explicit)


def write_events(path, names, target=None, outcome='pass'):
    events = []
    for name in names:
        terminal = outcome if name == target else 'pass'
        if terminal == 'missing':
            continue
        events.append(dict(Action='run', Package='fixture', Test=name))
        if terminal != 'unfinished':
            events.append(dict(Action=terminal, Package='fixture', Test=name))
    events.append(dict(Action='pass', Package='fixture'))
    path.write_text(''.join(json.dumps(event) + '\n' for event in events), encoding='utf-8')


class HelmJournalAdmissionScopeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.baseline = json.loads(BASELINE_PATH.read_text(encoding='utf-8'))

    def old_names(self, key):
        return set(self.baseline['required'][key]['names'])

    def test_fullname_fixture_is_bound_to_the_accepted_main(self):
        self.assertEqual(hashlib.sha256(BASELINE_PATH.read_bytes()).hexdigest(), BASELINE_SHA256)
        self.assertEqual(self.baseline['schema'], 1)
        self.assertEqual(self.baseline['source_commit'], BASELINE_COMMIT)
        self.assertEqual(set(self.baseline['required']), set(BASELINE_INVENTORIES))
        for key, frozen in BASELINE_INVENTORIES.items():
            with self.subTest(inventory=key):
                record = self.baseline['required'][key]
                self.assertEqual(record['names'], sorted(set(record['names'])))
                self.assertEqual((record['count'], record['sha256']), frozen)
                self.assertEqual(inventory(record['names']), frozen)

    def test_every_accepted_contract_and_automatic_fullname_is_preserved(self):
        for suite in contract.REQUIRED:
            with self.subTest(suite=suite):
                current = contract.required(suite)
                added = admission_names(suite)
                old = self.old_names('contract/' + suite)
                self.assertEqual(len(current), len(set(current)))
                self.assertFalse(old & added)
                self.assertEqual(set(current), old | added | set(contract.JOURNAL_ARCHIVE_REQUIRED.get(suite, ())))
        for suite, added in evidence.HELM_JOURNAL_ADMISSION_REQUIRED.items():
            with self.subTest(automatic=suite):
                current = evidence.required_for_suite(suite, [])
                old = self.old_names('automatic/' + suite)
                self.assertEqual(len(current), len(set(current)))
                self.assertFalse(old & set(added))
                self.assertEqual(set(current), old | set(added))

    def test_previous_native_uninstall_migration_and_journal_scopes_remain_intact(self):
        local = self.old_names('native-uninstall/local')
        real = self.old_names('native-uninstall/real-cluster-native')
        self.assertEqual(set(evidence.HELM_NATIVE_UNINSTALL_NAMES), local)
        for suite, names in evidence.HELM_NATIVE_UNINSTALL_REQUIRED.items():
            self.assertEqual(set(names), real if suite == 'real-cluster-native' else local)
        self.assertEqual(set(journal.RECOVERY_REQUIRED), self.old_names('journal'))
        self.assertEqual(set(journal.REQUIRED), self.old_names('journal') | set(journal.ADMISSION_REQUIRED) | set(journal.ARCHIVE_REQUIRED))
        self.assertFalse(set(journal.RECOVERY_REQUIRED) & set(journal.ADMISSION_REQUIRED))
        self.assertEqual(len(journal.REQUIRED), len(set(journal.REQUIRED)))
        self.assertEqual(set(migration.REQUIRED), self.old_names('migration')
                         | set(evidence.HELM_JOURNAL_ADMISSION_NAMES))
        self.assertEqual(len(migration.REQUIRED), len(set(migration.REQUIRED)))

    def test_new_names_use_the_existing_applicable_gates(self):
        local = evidence.HELM_JOURNAL_ADMISSION_NAMES
        self.assertEqual(len(local), len(set(local)))
        self.assertEqual(set(evidence.HELM_JOURNAL_ADMISSION_REQUIRED), {
            'unit-native', 'native-os-interactions', 'real-cluster-native',
            'migration-checked', 'migration-production',
        })
        for suite, names in evidence.HELM_JOURNAL_ADMISSION_REQUIRED.items():
            with self.subTest(suite=suite):
                self.assertEqual(len(names), len(set(names)))
                self.assertTrue(set(local) <= set(names))
                self.assertTrue(set(names) <= set(suite_required(suite)))
                explicit = evidence.required_for_suite(suite, ['TestExistingExplicitContract'] * 2)
                self.assertEqual(explicit.count('TestExistingExplicitContract'), 1)
                self.assertTrue(set(names) <= set(explicit))
        self.assertEqual(set(evidence.HELM_JOURNAL_ADMISSION_REQUIRED['real-cluster-native']) - set(local),
                         {'TestNativeHelmJournalAdmissionAgainstRealCluster'})
        self.assertEqual(set(contract.JOURNAL_ADMISSION_REQUIRED), {'unit-native'})
        self.assertTrue(journal.ADMISSION_REQUIRED)
        self.assertEqual(set(contract.JOURNAL_ADMISSION_REQUIRED['unit-native']),
                         set(journal.ADMISSION_REQUIRED) | set(contract.JOURNAL_ADMISSION_BACKEND_NAMES))
        self.assertTrue(set(local) <= set(migration.REQUIRED))
        self.assertFalse(set(local) & set(migration.TERMINAL_REQUIRED))

    def test_reviewed_admission_fullnames_cannot_disappear_from_both_group_and_gate(self):
        current = {
            'journal': journal.ADMISSION_REQUIRED,
            'backend': contract.JOURNAL_ADMISSION_BACKEND_NAMES,
            'native-local': evidence.HELM_JOURNAL_ADMISSION_NAMES,
            'native-real': evidence.HELM_JOURNAL_ADMISSION_REQUIRED['real-cluster-native'],
        }
        self.assertEqual(set(current), set(ADMISSION_INVENTORIES))
        for key, names in current.items():
            with self.subTest(inventory=key):
                self.assertEqual(len(names), len(set(names)))
                self.assertEqual(inventory(names), ADMISSION_INVENTORIES[key])

    def test_each_new_fullname_rejects_missing_failed_skipped_and_unfinished_results(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'tests.jsonl'
            for suite in evidence.HELM_JOURNAL_ADMISSION_REQUIRED:
                required = suite_required(suite)
                # Prove this complete fixture is valid before changing exactly
                # one outcome; a failure caused by absent old names is no test.
                write_events(path, required)
                self.assertTrue(evidence.summarize(path, required, suite)['valid'], suite)
                for name in sorted(admission_names(suite)):
                    for outcome in ('missing', 'fail', 'skip', 'unfinished'):
                        with self.subTest(suite=suite, name=name, outcome=outcome):
                            write_events(path, required, name, outcome)
                            result = evidence.summarize(path, required, suite)
                            self.assertFalse(result['valid'])
                            self.assertEqual(result['missing_required_tests'], [name])
                            if outcome == 'fail':
                                self.assertEqual(result['failed_tests'], ['fixture:' + name])
                            elif outcome == 'skip':
                                self.assertEqual(result['skipped_tests'], ['fixture:' + name])
                            elif outcome == 'unfinished':
                                self.assertEqual(result['unfinished_tests'], ['fixture:' + name])

    def test_new_native_roots_are_real_tests_in_separate_source_files(self):
        expected = {
            'internal/ui/helm_admission_ui_test.go': {
                'TestNativeHelmAdmissionReview', 'TestNativeHelmAdmissionHistoryAndMinimumWindow',
            },
            'internal/ui/helm_admission_controller_test.go': {
                'TestNativeHelmAdmissionLifecycle', 'TestNativeHelmAdmissionNoExecutionAuthority',
            },
            'internal/ui/helm_admission_e2e_test.go': {'TestNativeHelmJournalAdmissionAgainstRealCluster'},
        }
        declared = set()
        for source, roots in expected.items():
            text = (ROOT / source).read_text()
            found = set(re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)', text, re.M))
            self.assertEqual(found, roots, source)
            declared.update(found)
        names = evidence.HELM_JOURNAL_ADMISSION_REQUIRED['real-cluster-native']
        self.assertEqual(declared, {name.split('/', 1)[0] for name in names})
        self.assertTrue((ROOT / 'internal/ui/helm_admission_e2e_test.go').read_text().startswith('//go:build e2e\n'))
        pattern = migration.root_test_pattern(migration.REQUIRED)
        self.assertNotIn('/', pattern)
        for name in evidence.HELM_JOURNAL_ADMISSION_NAMES:
            self.assertIsNotNone(re.fullmatch(pattern, name.split('/', 1)[0]), name)

    def test_new_journal_roots_exist_on_each_platform_without_skipping_cases(self):
        declared = set()
        for path in (ROOT / 'internal/helmjournal').glob('admission*_test.go'):
            text = path.read_text()
            declared.update(re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)', text, re.M))
            self.assertNotRegex(text, r'\bt\.Skip(?:Now|f)?\s*\(', str(path))
        self.assertEqual(declared, {name.split('/', 1)[0] for name in journal.ADMISSION_REQUIRED})

    def test_new_backend_roots_have_their_own_source_file(self):
        path = ROOT / 'internal/kube/helm_uninstall_admission_test.go'
        text = path.read_text()
        declared = set(re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)', text, re.M))
        self.assertEqual(declared, {name.split('/', 1)[0] for name in contract.JOURNAL_ADMISSION_BACKEND_NAMES})
        self.assertNotRegex(text, r'\bt\.Skip(?:Now|f)?\s*\(')

    def test_read_only_review_has_one_exact_native_consumer(self):
        api = 'ReviewHelmUninstallJournalAdmission'
        self.assertEqual(CALL_SITES[api], {'internal/ui/helm_admission.go': 1})
        self.assertEqual(production_calls(ROOT, api), CALL_SITES[api])
        text = (ROOT / 'internal/ui/helm_admission.go').read_text()
        for name in ('OpenHelmUninstallJournal', 'AcknowledgeHelmUninstallExecution',
                     'ExecuteHelmUninstall', 'Begin', 'MkdirAll', 'MkdirTemp'):
            self.assertNotRegex(text, r'\b' + name + r'\s*\(')
        self.assertNotRegex(text, r'\bos\.(?:Remove|RemoveAll|Chmod|Rename)\s*\(')
        wrapper = (ROOT / 'internal/kube/helm_uninstall_admission.go').read_text()
        self.assertEqual(len(re.findall(r'\bhelmjournal\.ReviewAdmission\s*\(', wrapper)), 1)
        self.assertNotRegex(wrapper, r'\bhelmjournal\.Open\s*\(')
        for name in ('OpenHelmUninstallJournal', 'PrepareHelmUninstall',
                     'AcknowledgeHelmUninstallDryRun', 'PreflightHelmUninstall',
                     'AcknowledgeHelmUninstallExecution', 'ExecuteHelmUninstall',
                     'AcknowledgeHelmUninstallReconciliation', 'ObserveHelmUninstallRecovery'):
            self.assertNotRegex(wrapper, r'\b' + name + r'\s*\(')

    def test_old_tests_and_workflow_pins_and_budgets_are_byte_identical(self):
        # In particular, preserve aa84's 20-minute outer unit job budget and
        # unchanged 5-minute Go package budget; neither is raised by admission.
        for source, frozen in self.baseline['unchanged_source_sha256'].items():
            with self.subTest(source=source):
                # PR #98 admits only the exact reviewed Go patch tokens.
                data = previous_toolchain_bytes(source, (ROOT / source).read_bytes())
                self.assertEqual(hashlib.sha256(data).hexdigest(), frozen)


if __name__ == '__main__':
    unittest.main()
