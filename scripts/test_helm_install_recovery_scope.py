"""Install recovery adds complete read evidence without replacing old gates."""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from native_screenshot_scope import SCREENSHOT_TEST, restore_screenshot_encoder

import helm_install_recovery_evidence as recovery
import helm_uninstall_contract_evidence as contract
import run_migration_tests as migration
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]
BASE = 'c178e3fbadd5f8e0688d0a4350baebd416d3dc81'
# Independently read from BASE's Git blobs, not the changed working tree.
FROZEN_PATH_COUNT = 271
FROZEN_TEST_COUNT = 256
FROZEN_PATH_DIGEST = 'ffe3a0bd8803f40645381859b3cb6d01573de09762730f92a009de52b6d3c12b'
FROZEN_REQUIREMENTS = {
    'unit-native': (1581, 'ea40b78712bf4a06400935c546cd68fac8320452e6285eba329110451fa80c98'),
    'real-cluster-native': (202, 'dc262f551bb33c3cdd3616aa1cb3948e721fae604834bc5cbf90989c9fb7410a'),
    'native-os-interactions': (117, '9a815d2b1a3c93c10fe040d3dd243c8c4965b2e37a6b1b99127f91fef75d80d2'),
    'migration-checked': (88, 'dc487fd9bf41a5d2e5a891830dd84c0ab6a023be4b10a3457ff7a56615c49fe9'),
    'migration-production': (88, 'dc487fd9bf41a5d2e5a891830dd84c0ab6a023be4b10a3457ff7a56615c49fe9'),
    'migration-ui-runner': (201, 'b3e22f3d9f7645e783cf2360d429f394b1d233e91368d30d8a622c448da35ea7'),
    'migration-terminal-runner': (8, '2596f4904fc28ba407ef03efbe623ccef8cf94f08eab57dec62bb3ebf120a2d9'),
}
NEW_CASE_CENSUS = {
    'BACKEND_NAMES': (206, '874c6bd5ae435d2ee530e972b95cf7e3165073c1f00196979aa553bfa677da34'),
    'NATIVE_NAMES': (31, '70ff29cc75fb6cf2ca87b1785257e5aa11883efe7a7f50ae93fec6d7f0587de8'),
    'REAL_NAMES': (20, '9a161dbd50e6d72f9e5e96a553b1841e0a3170abf89c7a2a677d8365d64f8460'),
}


def names_digest(names):
    return hashlib.sha256('\n'.join(sorted(names)).encode()).hexdigest()


class InstallRecoveryScopeTests(unittest.TestCase):
    def test_exact_new_case_census(self):
        for group, (count, digest) in NEW_CASE_CENSUS.items():
            names = getattr(recovery, group)
            self.assertEqual(len(names), count)
            self.assertEqual(len(set(names)), count)
            self.assertEqual(names_digest(names), digest)

    def test_prior_requirements_remain_exactly_additive(self):
        for suite, (count, digest) in FROZEN_REQUIREMENTS.items():
            with self.subTest(suite=suite):
                if suite == 'migration-ui-runner':
                    names, added = migration.REQUIRED, set(recovery.NATIVE_NAMES)
                elif suite == 'migration-terminal-runner':
                    names, added = migration.TERMINAL_REQUIRED, set()
                else:
                    names = (contract.required(suite) if suite in contract.REQUIRED
                             else evidence.required_for_suite(suite, []))
                    added = recovery.additions(suite)
                self.assertEqual(len(names), len(set(names)))
                self.assertTrue(added <= set(names))
                prior = set(names) - added
                self.assertEqual(len(prior), count)
                self.assertEqual(names_digest(prior), digest)
        self.assertEqual(evidence.required_for_suite('other-suite', ['TestExisting']),
                         ['TestExisting'])

    def test_fixed_new_test_files_have_exact_required_roots(self):
        groups = {
            'backend': set(recovery.BACKEND_NAMES),
            'native': set(recovery.NATIVE_NAMES),
            'real': set(recovery.REAL_NAMES),
        }
        declared = {group: set() for group in groups}
        for path in recovery.ADDED_TEST_FILES:
            self.assertTrue(path.endswith('_test.go'))
            self.assertNotIn('*', path)
            group = ('backend' if path.startswith('internal/kube/') else
                     'real' if path.startswith('e2e/') or path.endswith('_e2e_test.go')
                     else 'native')
            roots = re.findall(r'^func (Test\w+)\(', (ROOT / path).read_text(), re.M)
            self.assertFalse(declared[group] & set(roots), path)
            declared[group].update(roots)
        union = set()
        for group, names in groups.items():
            self.assertTrue(names, group)
            self.assertFalse(union & names)
            union.update(names)
            self.assertEqual(declared[group], {name.split('/', 1)[0] for name in names})
            self.assertTrue(declared[group] <= names)
        for names in (recovery.BACKEND_NAMES, recovery.NATIVE_NAMES, recovery.REAL_NAMES):
            self.assertEqual(len(names), len(set(names)))

    def test_native_roots_run_in_both_migration_modes(self):
        self.assertTrue(set(recovery.NATIVE_NAMES) <= set(migration.REQUIRED))
        pattern = re.compile(migration.root_test_pattern(migration.REQUIRED))
        for name in recovery.NATIVE_NAMES:
            self.assertIsNotNone(pattern.fullmatch(name.split('/', 1)[0]))
        for name in (*recovery.BACKEND_NAMES, *recovery.REAL_NAMES):
            self.assertIsNone(pattern.fullmatch(name.split('/', 1)[0]))
        for suite in ('unit-native', 'native-os-interactions', 'real-cluster-native',
                      'migration-checked', 'migration-production'):
            self.assertTrue(set(recovery.NATIVE_NAMES) <=
                            set(evidence.required_for_suite(suite, [])))

    def test_each_new_case_needs_one_complete_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'tests.jsonl'
            for names in (recovery.BACKEND_NAMES, recovery.NATIVE_NAMES, recovery.REAL_NAMES):
                for selected in names:
                    for mode in ('pass', 'missing', 'fail', 'skip', 'unfinished', 'duplicate'):
                        with self.subTest(name=selected, mode=mode):
                            events = []
                            for name in names:
                                if name == selected and mode == 'missing':
                                    continue
                                events.append(dict(Action='run', Package='fixture', Test=name))
                                if name == selected and mode == 'unfinished':
                                    continue
                                action = mode if name == selected and mode in ('fail', 'skip') else 'pass'
                                events.append(dict(Action=action, Package='fixture', Test=name))
                                if name == selected and mode == 'duplicate':
                                    events.append(events[-1])
                            events.append(dict(Action='pass', Package='fixture'))
                            path.write_text(''.join(json.dumps(event) + '\n' for event in events))
                            self.assertEqual(evidence.summarize(path, list(names), 'install-recovery-fixture')['valid'],
                                             mode == 'pass')

    def test_all_256_accepted_go_tests_workflows_and_pins_are_byte_preserved(self):
        # Path, mode and content remain frozen even in a shallow CI checkout.
        # Removing or exempting an old file also changes this independent hash.
        index = subprocess.check_output(['git', 'ls-files', '--stage', '-z'], cwd=ROOT)
        rows, tracked = [], set()
        test_count = 0
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
            if protected and path not in recovery.ADDED_TEST_FILES:
                test_count += path.endswith('_test.go')
                data = (ROOT / path).read_bytes()
                if path == SCREENSHOT_TEST:
                    data = restore_screenshot_encoder(data)
                digest = hashlib.sha256(data).hexdigest()
                rows.append(path + '\0' + mode + '\0' + digest)
        self.assertTrue(recovery.ADDED_TEST_FILES <= tracked)
        self.assertEqual(test_count, FROZEN_TEST_COUNT)
        self.assertEqual(len(rows), FROZEN_PATH_COUNT)
        self.assertEqual(names_digest(rows), FROZEN_PATH_DIGEST)


if __name__ == '__main__':
    unittest.main()
