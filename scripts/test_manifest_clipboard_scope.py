"""Clipboard scope is additive and cannot qualify with missing old/new tests."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import retire_completed_oidc as retirement
import run_migration_tests as migration
import test_evidence as evidence


class ManifestClipboardScopeTests(unittest.TestCase):
    def test_registration_retains_feature_gates(self):
        self.assertEqual(retirement.APPROVED[64], 'feat/manifest-clipboard-preflight')
        self.assertNotIn(64, retirement.DEPENDENCY_SCOPES)
        self.assertEqual(retirement.REQUIRED,
                         ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification'))
        self.assertEqual(retirement.APPROVED[60], 'feat/native-schema-integer-map-keys')
        self.assertEqual(retirement.APPROVED[62], 'feat/native-schema-integer-set')

    def test_native_cases_are_required_only_in_the_correct_package(self):
        for name in evidence.MANIFEST_CLIPBOARD_REQUIRED['native-os-interactions']:
            self.assertIn(name, migration.REQUIRED)
            self.assertNotIn(name, migration.TERMINAL_REQUIRED)
        for name in ('TestNativeManifestBufferStableIdentitySnapshotAndUndo',
                     'TestNativeDeliveryMinimumWindowUnicodeAndBudget',
                     'TestNativeMyGo048ChunkedBufferPreservesMixedBytesAndHistory',
                     *evidence.INTEGER_SET_REQUIRED['native-os-interactions']):
            self.assertIn(name, migration.REQUIRED)
        self.assertIn('TestTerminalNativeMyGo048WheelRoutingAndClose', migration.TERMINAL_REQUIRED)
        self.assertEqual(len(migration.REQUIRED), len(set(migration.REQUIRED)))

    def test_named_suite_union_preserves_old_requirements(self):
        for suite, names in evidence.MANIFEST_CLIPBOARD_REQUIRED.items():
            got = evidence.required_for_suite(suite, ['TestOldContract', names[0]])
            self.assertEqual(got[0], 'TestOldContract')
            self.assertEqual(set(got), {'TestOldContract', *names,
                                      *evidence.INTEGER_MAP_REQUIRED.get(suite, ()),
                                      *evidence.INTEGER_SET_REQUIRED.get(suite, ()),
                                      *evidence.BOOLEAN_COLLECTIONS_REQUIRED.get(suite, ()), *evidence.HELM_RENDER_REQUIRED.get(suite, ()), *evidence.HELM_METADATA_REQUIRED.get(suite, ()), *evidence.HELM_INSPECTION_REQUIRED.get(suite, ()), *evidence.HELM_UNINSTALL_PREVIEW_REQUIRED.get(suite, ())})
            self.assertEqual(len(got), len(set(got)))
        self.assertEqual(evidence.required_for_suite('another-suite', ['TestOldContract']),
                         ['TestOldContract'])

    def test_cli_requires_every_new_and_previous_scope(self):
        script = Path(evidence.__file__)
        for suite, names in evidence.MANIFEST_CLIPBOARD_REQUIRED.items():
            required = evidence.required_for_suite(suite, ['TestOldContract'])
            # Test every clipboard name, one name from each preceding integer
            # scope, and the explicit old requirement. Never count skips as PASS.
            excluded_cases = [*names, 'TestOldContract',
                              evidence.INTEGER_MAP_REQUIRED[suite][0],
                              evidence.INTEGER_SET_REQUIRED[suite][0], None]
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
                    result = subprocess.run([sys.executable, str(script), str(path), '--suite', suite,
                                             '--require', 'TestOldContract'], capture_output=True,
                                            text=True, timeout=10)
                    report = json.loads(result.stdout)
                    self.assertEqual(result.returncode, 0 if excluded is None else 1)
                    self.assertEqual(report['missing_required_tests'], [] if excluded is None else [excluded])
                    self.assertEqual(json.loads(path.with_name('summary.json').read_text()), report)


if __name__ == '__main__':
    unittest.main()
