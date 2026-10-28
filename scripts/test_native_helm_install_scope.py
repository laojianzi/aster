"""The native install scope preserves every accepted test, pin and old gate."""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

import helm_native_install_evidence as install
import helm_render_diagnostics_evidence as diagnostics
from helm_native_scope import CALL_SITES, production_calls
from native_screenshot_scope import SCREENSHOT_TEST, restore_screenshot_best_speed
import helm_uninstall_contract_evidence as contract
import run_migration_tests as migration
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]
BASE = '85e9b697b0cc4329f8398197bffcf260af6b5a90'
# Independently read from the accepted main Git blobs before implementation.
FROZEN_PATH_COUNT = 278
FROZEN_TEST_COUNT = 263
FROZEN_PATH_DIGEST = '4d753b6186e0b73192f8cbfbefa8e297c989ca08af9b33b02b53daaabcd519a3'
FROZEN_REQUIREMENTS = {
    'unit-native': (1818, '389969f99db022b54eb7eec24d4ce1630eeeef2b851dc9ab9a8a781215f2bc06'),
    'real-cluster-native': (253, '240174186cd000cd197a42589f4fcdab46a0b1730098c2fd087fa7333f508d91'),
    'native-os-interactions': (148, '5faacbbefa3b658997c58bd49006f24937425141d7f0909c7ed48edd55c83cd8'),
    'migration-checked': (119, '9d97433eef4756751585730e4f938f50f51e20aaf3d0f2b62f52f647954cd61c'),
    'migration-production': (119, '9d97433eef4756751585730e4f938f50f51e20aaf3d0f2b62f52f647954cd61c'),
    'migration-ui-runner': (232, '564f89988d7836017aefd053d38653575bf23118df0854c56a041b6dd1035e50'),
    'migration-terminal-runner': (8, '2596f4904fc28ba407ef03efbe623ccef8cf94f08eab57dec62bb3ebf120a2d9'),
}
NEW_CASE_CENSUS = {
    'NATIVE_NAMES': (42, 'e3dfcc7b294b680ecfd1aa4486d93cdd57cc6bea8cbc6b28768dde7432324662'),
    'REAL_NAMES': (4, 'baef74e367d458a904258354c3c3130f7e6aaea9b33e6c5c150c8f97e1eaaa13'),
}


def digest(names):
    return hashlib.sha256('\n'.join(sorted(names)).encode()).hexdigest()


class NativeHelmInstallScopeTests(unittest.TestCase):
    def test_exact_new_case_census(self):
        for group, (count, checksum) in NEW_CASE_CENSUS.items():
            names = getattr(install, group)
            self.assertEqual(len(names), count)
            self.assertEqual(len(set(names)), count)
            self.assertEqual(digest(names), checksum)
        self.assertFalse(set(install.NATIVE_NAMES) & set(install.REAL_NAMES))

    def test_every_old_requirement_is_preserved_exactly(self):
        for suite, (count, checksum) in FROZEN_REQUIREMENTS.items():
            with self.subTest(suite=suite):
                if suite == 'migration-ui-runner':
                    names, added = migration.REQUIRED, set(install.NATIVE_NAMES) | set(diagnostics.UI_NAMES)
                elif suite == 'migration-terminal-runner':
                    names, added = migration.TERMINAL_REQUIRED, set()
                else:
                    names = (contract.required(suite) if suite in contract.REQUIRED
                             else evidence.required_for_suite(suite, []))
                    added = install.additions(suite) | diagnostics.additions(suite)
                self.assertEqual(len(names), len(set(names)))
                self.assertTrue(added <= set(names))
                prior = set(names) - added
                self.assertEqual(len(prior), count)
                self.assertEqual(digest(prior), checksum)
        self.assertEqual(evidence.required_for_suite('other-suite', ['TestExisting']),
                         ['TestExisting'])

    def test_fixed_new_test_paths_have_exact_required_roots(self):
        expected = {
            'internal/ui/helm_install_ui_fixture_test.go',
            'internal/ui/helm_install_ui_test.go',
            'internal/ui/helm_install_lifecycle_test.go',
            'internal/ui/helm_install_journal_test.go',
            'internal/ui/helm_install_e2e_test.go',
        }
        self.assertEqual(install.INSTALL_TEST_FILES, expected)
        self.assertEqual(install.ADDED_TEST_FILES, expected | diagnostics.ADDED_TEST_FILES)
        native, real = set(), set()
        for path in expected:
            text = (ROOT / path).read_text()
            roots = set(re.findall(r'^func (Test\w+)\(', text, re.M))
            group = real if path.endswith('_e2e_test.go') else native
            self.assertFalse(group & roots, path)
            group.update(roots)
            if path.endswith('_e2e_test.go'):
                self.assertTrue(text.startswith('//go:build e2e\n'))
        self.assertEqual(native, {name.split('/', 1)[0] for name in install.NATIVE_NAMES})
        self.assertEqual(real, {name.split('/', 1)[0] for name in install.REAL_NAMES})
        self.assertTrue(native <= set(install.NATIVE_NAMES))
        self.assertTrue(real <= set(install.REAL_NAMES))

    def test_native_roots_run_in_both_migration_modes(self):
        self.assertTrue(set(install.NATIVE_NAMES) <= set(migration.REQUIRED))
        pattern = re.compile(migration.root_test_pattern(migration.REQUIRED))
        for name in install.NATIVE_NAMES:
            self.assertIsNotNone(pattern.fullmatch(name.split('/', 1)[0]))
        for name in install.REAL_NAMES:
            self.assertIsNone(pattern.fullmatch(name.split('/', 1)[0]))
        for suite in install.AUTOMATIC:
            self.assertTrue(set(install.NATIVE_NAMES) <=
                            set(evidence.required_for_suite(suite, [])))
        self.assertTrue(set(install.REAL_NAMES) <= set(contract.required('real-cluster-native')))
        self.assertFalse(set(install.REAL_NAMES) & set(contract.required('unit-native')))

    def test_each_new_case_requires_one_finished_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'events.jsonl'
            for names in (install.NATIVE_NAMES, install.REAL_NAMES):
                for chosen in names:
                    for mode in ('pass', 'missing', 'fail', 'skip', 'unfinished', 'duplicate'):
                        with self.subTest(name=chosen, mode=mode):
                            events = []
                            for name in names:
                                if name == chosen and mode == 'missing':
                                    continue
                                events.append(dict(Action='run', Package='fixture', Test=name))
                                if name == chosen and mode == 'unfinished':
                                    continue
                                action = mode if name == chosen and mode in ('fail', 'skip') else 'pass'
                                events.append(dict(Action=action, Package='fixture', Test=name))
                                if name == chosen and mode == 'duplicate':
                                    events.append(events[-1])
                            events.append(dict(Action='pass', Package='fixture'))
                            path.write_text(''.join(json.dumps(x) + '\n' for x in events))
                            self.assertEqual(evidence.summarize(path, list(names), 'native-install')['valid'],
                                             mode == 'pass')

    def test_all_263_existing_go_tests_workflows_and_pins_are_byte_preserved(self):
        index = subprocess.check_output(['git', 'ls-files', '--stage', '-z'], cwd=ROOT)
        rows, tracked, count = [], set(), 0
        for entry in index.split(b'\0'):
            if not entry:
                continue
            header, path = entry.decode().split('\t', 1)
            mode, _, stage = header.split()
            self.assertEqual(stage, '0')
            tracked.add(path)
            protected = (path.endswith('_test.go') or path.startswith('.github/workflows/')
                         or path in ('go.mod', 'go.sum')
                         or path.startswith('third_party/native-terminal/'))
            if protected and path not in install.ADDED_TEST_FILES:
                count += path.endswith('_test.go')
                data = (ROOT / path).read_bytes()
                if path == SCREENSHOT_TEST:
                    # Require only the exact reviewed NoCompression postimage,
                    # then restore every accepted BestSpeed byte for this
                    # unchanged 85e9 path/mode/content inventory.
                    data = restore_screenshot_best_speed(data)
                rows.append(path + '\0' + mode + '\0' + hashlib.sha256(data).hexdigest())
        self.assertTrue(install.ADDED_TEST_FILES <= tracked)
        self.assertEqual(count, FROZEN_TEST_COUNT)
        self.assertEqual(len(rows), FROZEN_PATH_COUNT)
        self.assertEqual(digest(rows), FROZEN_PATH_DIGEST)

    def test_install_authority_is_only_in_the_admitted_native_consumers(self):
        admitted = {
            'CaptureInstall': {'internal/ui/helm_install.go': 1},
            'PrepareHelmInstall': {'internal/ui/helm_install.go': 1},
            'AcknowledgeHelmInstallDryRun': {'internal/ui/helm_install.go': 1},
            'PreflightHelmInstall': {'internal/ui/helm_install.go': 1},
            'AcknowledgeHelmInstallExecution': {'internal/ui/helm_install_journal.go': 1},
            'ExecuteHelmInstall': {'internal/ui/helm_install_journal.go': 1},
            'OpenHelmUninstallJournal': {'internal/ui/helm_uninstall_journal.go': 2,
                                       'internal/ui/helm_install_journal.go': 1},
        }
        for name, sites in admitted.items():
            self.assertEqual(CALL_SITES[name], sites)
            self.assertEqual(production_calls(ROOT, name), sites)
        for name in ('BeginInstall', 'BuildInstallCreatePlan', 'BuildInstallDeployedPatch'):
            self.assertEqual(production_calls(ROOT, name), {})
        for path in ('internal/ui/helm_install.go', 'internal/ui/helm_install_view.go',
                     'internal/ui/helm_install_journal.go'):
            text = (ROOT / path).read_text()
            for forbidden in ('exec.Command', 'os.ReadFile', 'os.Stat', 'os.Remove',
                              'os.Rename', 'helmjournal.Open', 'http.NewRequest'):
                self.assertNotIn(forbidden, text, path)


if __name__ == '__main__':
    unittest.main()
